package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/tyru/vain/node"
)

func analyze(name string, inNodes <-chan node.Node) *analyzer {
	// TODO Give policies by argument?
	// But some rules are required to emit "correct" vim script intermediate code.
	policies := defaultPolicies
	checkers := make([]checkFn, 0, len(ruleChecker))
	added := make(map[string]bool, len(ruleChecker))
	for name, checker := range ruleChecker {
		if policies[name] && !added[name] {
			checkers = append(checkers, checker)
			added[name] = true
		}
	}
	return &analyzer{
		name,
		inNodes,
		make(chan node.Node, 1),
		newSeriesChecker(checkers...),
		policies,
	}
}

type analyzer struct {
	name     string
	inNodes  <-chan node.Node
	outNodes chan node.Node
	checker  *seriesChecker
	policies map[string]bool
}

func (a *analyzer) Nodes() <-chan node.Node {
	return a.outNodes
}

func (a *analyzer) enabled(name string) bool {
	return a.policies[name]
}

const (
	toplevelReturn              = "toplevel-return"
	undeclaredVariable          = "undeclared-variable"
	duplicateDeclaration        = "duplicate-declaration"
	underscoreVariableReference = "underscore-variable-reference"
)

func init() {
	def := []struct {
		name    string
		check   checkFn
		enabled bool
	}{
		{
			toplevelReturn,
			checkToplevelReturn,
			true,
		},
		{
			undeclaredVariable,
			checkVariable,
			true,
		},
		{
			duplicateDeclaration,
			checkVariable,
			true,
		},
		{
			underscoreVariableReference,
			checkVariable,
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
		// TODO Run each check functions concurrently:
		// Pool goroutines to process the checks and run them in the goroutines.
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

func newScope() *scope {
	return &scope{make([]map[string]*identifierNode, 0, 4)}
}

type scope struct {
	vars []map[string]*identifierNode
}

func (s *scope) push() {
	s.vars = append(s.vars, make(map[string]*identifierNode, 8))
}

func (s *scope) pop() {
	s.vars = s.vars[:len(s.vars)-1]
}

func (s *scope) getVar(name string) *identifierNode {
	return s.vars[len(s.vars)-1][name]
}

func (s *scope) getOuterVar(name string) *identifierNode {
	for i := len(s.vars) - 1; i >= 0; i-- {
		if s.vars[i][name] != nil {
			return s.vars[i][name]
		}
	}
	return nil
}

func (s *scope) addVar(id *identifierNode) {
	s.vars[len(s.vars)-1][id.value] = id
}

// checkVariable checks:
// * toplevel-return
//   * Variables are used before declaration.
// * undeclared-variable
//   * Duplicate variable decralations exist.
// * underscore-variable-reference
//   * Underscore identifier ("_") is used for the variable which is referenced.
func checkVariable(a *analyzer, _ *walkCtrl, tNode *typedNode) []node.ErrorNode {
	switch nn := tNode.TerminalNode().(type) {
	case *topLevelNode:
		return a.checkVariable(nn.body, newScope())
	case *funcStmtOrExpr:
		return a.checkVariable(nn.body, newScope())
	default:
		return nil
	}
}

// Check the scope of the function, but won't check another function's scope.
func (a *analyzer) checkVariable(body []node.Node, scope *scope) []node.ErrorNode {
	errs := make([]node.ErrorNode, 0, 4)
	scope.push()
	for i := range body {
		if vs := a.getDeclaredVars(body[i]); len(vs) > 0 { // Found declaration.
			for i := range vs {
				var id *identifierNode
				if nn, ok := vs[i].TerminalNode().(*identifierNode); ok {
					id = nn
				} else {
					continue
				}
				if v := scope.getVar(id.value); v != nil {
					if a.enabled(duplicateDeclaration) {
						var declared string
						if pos := v.Position(); pos != nil {
							declared = fmt.Sprintf(": already declared at (%d,%d)", pos.Line(), pos.Col()+1)
						}
						err := a.err(
							fmt.Errorf("duplicate variable: %s%s", id.value, declared),
							vs[i],
						)
						errs = append(errs, *err)
					}
					continue
				}
				if id.value != "_" {
					scope.addVar(id)
				}
			}
		}
		if e := a.checkInnerBlock(body[i], scope); len(e) > 0 { // Check if,while,...
			errs = append(errs, e...)
		}
		vs, e := a.getReferenceVars(body[i])
		errs = append(errs, e...)
		if len(vs) > 0 { // Found referenced variables.
			for i := range vs {
				var id *identifierNode
				if nn, ok := vs[i].TerminalNode().(*identifierNode); ok {
					id = nn
				} else {
					continue
				}
				if v := scope.getOuterVar(id.value); v == nil && a.enabled(undeclaredVariable) {
					err := a.err(
						errors.New("undefined: "+id.value),
						vs[i],
					)
					errs = append(errs, *err)
				}
			}
		}
	}
	scope.pop()
	return errs
}

func (a *analyzer) checkInnerBlock(n node.Node, scope *scope) []node.ErrorNode {
	switch nn := n.TerminalNode().(type) {
	case *ifStatement:
		return a.checkVariable(nn.body, scope)
	case *whileStatement:
		return a.checkVariable(nn.body, scope)
	case *forStatement:
		return a.checkVariable(nn.body, scope)
	default:
		return nil
	}
}

// Get variable identifier nodes in a declaration.
// Returned nodes also have a position (node.Position() != nil)
// if original node has a position.
func (a *analyzer) getDeclaredVars(n node.Node) []node.Node {
	var left node.Node
	switch nn := n.TerminalNode().(type) {
	case *constStatement:
		left = nn.left
	case *letStatement:
		left = nn.left
	case *forStatement:
		left = nn.left
	default:
		return nil
	}
	switch nn := left.TerminalNode().(type) {
	case *listNode:
		vars := make([]node.Node, 0, len(nn.value))
		for i := range nn.value {
			if _, ok := nn.value[i].TerminalNode().(*identifierNode); ok {
				vars = append(vars, nn.value[i])
			}
		}
		return vars
	case *identifierNode:
		return []node.Node{left}
	default:
		return nil
	}
}

// Get variable identifier nodes which references to.
// Returned nodes also have a position (node.Position() != nil)
// if original node has a position.
func (a *analyzer) getReferenceVars(n node.Node) ([]node.Node, []node.ErrorNode) {
	errs := make([]node.ErrorNode, 0, 4)
	ids := make([]node.Node, 0, 8)
	declroutes := make([][]int, 0, 8)
	walkNode(n, func(ctrl *walkCtrl, n node.Node) node.Node {
		switch nn := n.TerminalNode().(type) {
		case *constStatement:
			lhs := append(ctrl.route(), 0)
			declroutes = append(declroutes, lhs)
		case *letStatement:
			lhs := append(ctrl.route(), 0)
			declroutes = append(declroutes, lhs)
		case *forStatement:
			lhs := append(ctrl.route(), 0)
			declroutes = append(declroutes, lhs)
		case *identifierNode:
			// The identifierNode is used for variable name, and
			// is not in left-hand side of declaration node.
			if nn.isVarname && !containsRoute(ctrl.route(), declroutes) {
				if nn.value == "_" {
					if a.enabled(underscoreVariableReference) {
						err := a.err(
							errors.New("underscore variable can be used only in declaration"),
							n,
						)
						errs = append(errs, *err)
					}
				} else {
					ids = append(ids, n)
				}
			}
		}
		return n
	})
	return ids, errs
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
	case *constStatement:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *letStatement:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
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

func containsRoute(r []int, routes [][]int) bool {
	for i := range routes {
		contains := true
		for j := range routes[i] {
			if r[j] != routes[i][j] {
				contains = false
				break
			}
		}
		if contains {
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
		if s.ctrlList[i].followInner && !containsRoute(route, s.ignoredPaths[i]) {
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
