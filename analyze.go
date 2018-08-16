package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/tyru/vain/node"
)

func analyze(name string, inNodes <-chan node.Node) *analyzer {
	// TODO give policies by argument?
	checkers := make([]checkFn, 0, len(ruleChecker))
	for name, checker := range ruleChecker {
		if defaultPolicies[name] {
			checkers = append(checkers, checker)
		}
	}
	return &analyzer{name, inNodes, make(chan node.Node, 1), newSeriesChecker(checkers...)}
}

type analyzer struct {
	name     string
	inNodes  <-chan node.Node
	outNodes chan node.Node
	checker  *seriesChecker
}

func (a *analyzer) Nodes() <-chan node.Node {
	return a.outNodes
}

func init() {
	def := []struct {
		name    string
		check   checkFn
		enabled bool
	}{
		{
			"toplevel-return",
			checkToplevelReturn,
			true,
		},
		{
			"undeclared-variables",
			checkUndeclaredVariables,
			true,
		},
		{
			"duplicate-variables",
			checkDuplicateVariables,
			true,
		},
	}
	defaultPolicies = make(map[string]bool, len(def))
	ruleChecker = make(map[string]checkFn, len(def))
	for i := range def {
		defaultPolicies[def[i].name] = def[i].enabled
		ruleChecker[def[i].name] = def[i].check
	}
}

type checkFn func(*analyzer, *walkCtrl, *typedNode) []node.ErrorNode

var defaultPolicies map[string]bool
var ruleChecker map[string]checkFn

type typedNode struct {
	node.Node
	typ string // expression type
}

func (n *typedNode) Clone() node.Node {
	var inner node.Node
	if n.Node != nil {
		inner = n.Node.Clone()
	}
	return &typedNode{inner, n.typ}
}

func (a *analyzer) Run() {
	for n := range a.inNodes {
		if _, ok := n.TerminalNode().(*topLevelNode); ok {
			top, errs := a.analyze(n)
			if len(errs) > 0 {
				for i := range errs {
					a.emit(&errs[i]) // type error, and so on
				}
				continue
			}
			a.emit(top)
		} else if e, ok := n.TerminalNode().(*node.ErrorNode); ok {
			a.emit(e) // parse error
		} else {
			err := fmt.Errorf("unknown node: %+v", n)
			a.emit(a.err(err, n))
		}
	}
	close(a.outNodes)
}

// emit passes an node back to the client.
func (a *analyzer) emit(n node.Node) {
	a.outNodes <- n
}

func (a *analyzer) err(err error, n node.Node) *node.ErrorNode {
	if pos := n.Position(); pos != nil {
		return node.NewErrorNode(
			fmt.Errorf("[analyze] %s:%d:%d: "+err.Error(), a.name, pos.Line(), pos.Col()+1),
			pos,
		)
	}
	return node.NewErrorNode(
		fmt.Errorf("[analyze] %s: "+err.Error(), a.name),
		nil,
	)
}

func (a *analyzer) analyze(top node.Node) (node.Node, []node.ErrorNode) {
	// Infer type (convert the node to *typedNode).
	tNode, errs := a.infer(top)
	if len(errs) > 0 {
		return nil, errs
	}

	// Perform semantics checks.
	errs = a.check(tNode)
	if len(errs) > 0 {
		return nil, errs
	}

	// Convert node.
	tNode = a.convertPre(tNode)

	// Convert *typedNode to node.
	top, ok := a.convertPost(tNode).TerminalNode().(*topLevelNode)
	if !ok {
		err := a.err(
			fmt.Errorf("fatal: topLevelNode is needed at top level (%+v)", top),
			top,
		)
		return nil, []node.ErrorNode{*err}
	}

	return top, errs
}

// check checks the semantic errors.
func (a *analyzer) check(tNode *typedNode) []node.ErrorNode {
	errs := make([]node.ErrorNode, 0, 16)
	walkNode(tNode, func(ctrl *walkCtrl, n node.Node) node.Node {
		tNode := n.(*typedNode) // NOTE: Given node must be *typedNode.
		errs = append(errs, a.checker.check(a, ctrl, tNode)...)
		return n
	})
	return errs
}

// checkToplevelReturn checks if returnStatement exists under topLevelNode.
// It doesn't check inside expression and function.
func checkToplevelReturn(a *analyzer, ctrl *walkCtrl, tNode *typedNode) []node.ErrorNode {
	if tNode.IsExpr() {
		ctrl.dontFollowInner()
		return nil
	}
	n := tNode.TerminalNode()
	if _, ok := n.(*funcStmtOrExpr); ok {
		ctrl.dontFollowInner()
		return nil
	}
	if _, ok := n.(*returnStatement); ok {
		err := a.err(
			errors.New("return statement found at top level"),
			tNode,
		)
		return []node.ErrorNode{*err}
	}
	return nil
}

// checkUndeclaredVariables checks if variables are used before declaration.
func checkUndeclaredVariables(a *analyzer, ctrl *walkCtrl, tNode *typedNode) []node.ErrorNode {
	return nil // TODO
}

// checkDuplicateVariables checks if duplicate variable decralations exist.
func checkDuplicateVariables(a *analyzer, ctrl *walkCtrl, tNode *typedNode) []node.ErrorNode {
	return nil // TODO
}

// convertPre converts some specific nodes.
// convertPre *does not* change n inplacely.
// It clones the node, convert, and return it.
func (a *analyzer) convertPre(tNode *typedNode) *typedNode {
	if top, ok := tNode.TerminalNode().Clone().(*topLevelNode); ok {
		top.body = a.convertVariableNames(top.body)
		tNode = &typedNode{top, ""}
	}
	return tNode
}

// convertUnderscore converts variable name in the scope of body.
// For example, "_varname" -> "__varname", "_" -> "_unused{nr}".
func (a *analyzer) convertVariableNames(body []node.Node) []node.Node {
	nr := 0
	newbody := make([]node.Node, 0, len(body))
	for i := range body {
		b := walkNode(body[i], func(ctrl *walkCtrl, n node.Node) node.Node {
			if f, ok := n.TerminalNode().(*funcStmtOrExpr); ok {
				f.body = a.convertVariableNames(f.body)
				ctrl.dontFollowInner()
				return f
			}
			var con *constStatement
			if c, ok := n.TerminalNode().(*constStatement); ok {
				con = c
			} else {
				return n
			}
			if !con.hasUnderscore {
				return n
			}
			var ids []*identifierNode
			if l, ok := con.left.TerminalNode().(*listNode); ok { // Destructuring
				for i := range l.value {
					if id, ok := l.value[i].TerminalNode().(*identifierNode); ok {
						ids = append(ids, id)
					}
				}
			} else {
				return n
			}
			for i := range ids {
				// "_varname" -> "__varname"
				if ids[i].value[0] == '_' && len(ids[i].value) != 1 {
					ids[i].value = "__" + ids[i].value[1:]
				}
				// "_" -> "_unused{nr}"
				if ids[i].value == "_" {
					ids[i].value = "_unused" + strconv.Itoa(nr)
					nr++
				}
			}
			return con
		})
		newbody = append(newbody, b)
	}
	return newbody
}

// infer infers each node's type and return the tree of *typedNode.
func (a *analyzer) infer(top node.Node) (*typedNode, []node.ErrorNode) {
	typedTop := walkNode(top, func(_ *walkCtrl, n node.Node) node.Node {
		return &typedNode{n, ""} // TODO
	}).(*typedNode) // returned node must be *topLevelNode
	return typedTop, nil
}

// convertPost converts *typedNode to node.
func (a *analyzer) convertPost(tNode *typedNode) node.Node {
	return walkNode(tNode, func(_ *walkCtrl, n node.Node) node.Node {
		// Unwrap node from typedNode.
		return n.TerminalNode()
	})
}

// newWalkCtrl is the constructor for walkCtrl.
func newWalkCtrl() *walkCtrl {
	return &walkCtrl{true, make([]int, 0, 16)}
}

// walkCtrl is passed to callback.
// Callback can control walking flow by calling its methods.
type walkCtrl struct {
	followInner bool
	routes      []int
}

// dontFollowInner controls walking flow.
// When it is called, all inner nodes are skipped.
func (ctrl *walkCtrl) dontFollowInner() {
	ctrl.followInner = false
}

// route returns the route of current node.
// Note that route may become wrong if node structure is changed.
// Because this is indices of each node.
func (ctrl *walkCtrl) route() []int {
	paths := make([]int, len(ctrl.routes))
	copy(paths, ctrl.routes)
	return paths
}

// walkNode is newWalkCtrl().walk(n, 0, f)
func walkNode(n node.Node, f func(*walkCtrl, node.Node) node.Node) node.Node {
	return newWalkCtrl().walk(n, 0, f)
}

func (ctrl *walkCtrl) push(id int) {
	ctrl.routes = append(ctrl.routes, id)
}

func (ctrl *walkCtrl) pop() {
	ctrl.routes = ctrl.routes[:len(ctrl.routes)-1]
}

// walk walks node recursively and call f with each node.
// if f(node) == false, walk stops walking inner nodes.
func (ctrl *walkCtrl) walk(n node.Node, id int, f func(*walkCtrl, node.Node) node.Node) node.Node {
	if n == nil {
		return nil
	}
	ctrl.push(id)
	r := f(ctrl, n)
	if !ctrl.followInner {
		ctrl.pop()
		return r
	}

	switch nn := r.TerminalNode().(type) {
	case *topLevelNode:
		for i := range nn.body {
			nn.body[i] = ctrl.walk(nn.body[i], i, f)
		}
	case *importStatement:
	case *funcStmtOrExpr:
		ctrl.push(0)
		for i := range nn.args {
			if nn.args[i].defaultVal != nil {
				nn.args[i].defaultVal = ctrl.walk(nn.args[i].defaultVal, i, f)
			}
		}
		ctrl.pop()
		ctrl.push(1)
		for i := range nn.body {
			nn.body[i] = ctrl.walk(nn.body[i], i, f)
		}
		ctrl.pop()
	case *returnStatement:
		nn.left = ctrl.walk(nn.left, 0, f)
	case *ifStatement:
		nn.cond = ctrl.walk(nn.cond, 0, f)
		ctrl.push(1)
		for i := range nn.body {
			nn.body[i] = ctrl.walk(nn.body[i], i, f)
		}
		ctrl.pop()
		ctrl.push(2)
		for i := range nn.els {
			nn.els[i] = ctrl.walk(nn.els[i], i, f)
		}
		ctrl.pop()
	case *whileStatement:
		nn.cond = ctrl.walk(nn.cond, 0, f)
		ctrl.push(1)
		for i := range nn.body {
			nn.body[i] = ctrl.walk(nn.body[i], i, f)
		}
		ctrl.pop()
	case *forStatement:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
		ctrl.push(2)
		for i := range nn.body {
			nn.body[i] = ctrl.walk(nn.body[i], i, f)
		}
		ctrl.pop()
	case *ternaryNode:
		nn.cond = ctrl.walk(nn.cond, 0, f)
		nn.left = ctrl.walk(nn.left, 1, f)
		nn.right = ctrl.walk(nn.right, 2, f)
	case *orNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *andNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *equalNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *equalCiNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *nequalNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *nequalCiNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *greaterNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *greaterCiNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *gequalNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *gequalCiNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *smallerNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *smallerCiNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *sequalNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *sequalCiNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *matchNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *matchCiNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *noMatchNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *noMatchCiNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *isNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *isCiNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *isNotNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *isNotCiNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *addNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *subtractNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *multiplyNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *divideNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *remainderNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *notNode:
		nn.left = ctrl.walk(nn.left, 0, f)
	case *minusNode:
		nn.left = ctrl.walk(nn.left, 0, f)
	case *plusNode:
		nn.left = ctrl.walk(nn.left, 0, f)
	case *sliceNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		ctrl.push(1)
		for i := range nn.rlist {
			nn.rlist[i] = ctrl.walk(nn.rlist[i], i, f)
		}
		ctrl.pop()
	case *callNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		ctrl.push(1)
		for i := range nn.rlist {
			nn.rlist[i] = ctrl.walk(nn.rlist[i], i, f)
		}
		ctrl.pop()
	case *subscriptNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *dotNode:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *identifierNode:
	case *intNode:
	case *floatNode:
	case *stringNode:
	case *listNode:
		for i := range nn.value {
			nn.value[i] = ctrl.walk(nn.value[i], i, f)
		}
	case *dictionaryNode:
		for i := range nn.value {
			ctrl.push(i)
			val := nn.value[i]
			for j := range val {
				if val[j] != nil {
					val[j] = ctrl.walk(val[j], j, f)
				}
			}
			ctrl.pop()
		}
	case *optionNode:
	case *envNode:
	case *regNode:
	}

	ctrl.pop()
	return r
}

type seriesChecker struct {
	checkers     []checkFn
	ctrlList     []*walkCtrl
	ignoredPaths [][][]int
}

func newSeriesChecker(checkers ...checkFn) *seriesChecker {
	ctrlList := make([]*walkCtrl, len(checkers))
	ignoredPaths := make([][][]int, len(checkers))
	for i := range checkers {
		ctrlList[i] = newWalkCtrl()
		ignoredPaths[i] = make([][]int, 0)
	}
	return &seriesChecker{checkers, ctrlList, ignoredPaths}
}

func (s *seriesChecker) isIgnored(route []int, paths [][]int) bool {
	for i := range paths {
		ignored := true
		for j := range paths[i] {
			if route[j] != paths[i][j] {
				ignored = false
				break
			}
		}
		if ignored {
			return true
		}
	}
	return false
}

// check calls check functions for node.
// If all of check functions called dontFollowInner(),
// check also calls dontFollowInner() for parent *walkCtrl.
func (s *seriesChecker) check(a *analyzer, ctrl *walkCtrl, tNode *typedNode) []node.ErrorNode {
	errs := make([]node.ErrorNode, 0, 8)
	route := ctrl.route()
	for i := range s.checkers {
		if s.ctrlList[i].followInner && !s.isIgnored(route, s.ignoredPaths[i]) {
			errs = append(errs, s.checkers[i](a, s.ctrlList[i], tNode)...)
			if !s.ctrlList[i].followInner {
				s.ignoredPaths[i] = append(s.ignoredPaths[i], route)
				s.ctrlList[i].followInner = true
			}
		}
	}
	followInner := false
	for i := range s.ctrlList {
		if s.ctrlList[i].followInner {
			followInner = true
		}
	}
	if !followInner {
		ctrl.dontFollowInner()
	}
	return errs
}
