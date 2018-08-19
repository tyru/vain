package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/tyru/vain/node"
)

func analyze(name string, inNodes <-chan node.Node) *analyzer {
	// TODO Give policies by argument?
	// But some ruleMap are required to emit "correct" vim script intermediate code.
	policies := defaultPolicies

	// 0: unused, 1: checker, 2: converter
	funcs := make([]int, len(walkFuncs))
	var checkNum, converterNum int
	for name, rule := range ruleMap {
		if policies[name] {
			if rule.isChecker {
				funcs[rule.funcID] = 1
				checkNum++
			} else {
				funcs[rule.funcID] = 2
				converterNum++
			}
		}
	}
	checkers := make([]multiWalkFn, 0, checkNum)
	converters := make([]multiWalkFn, 0, converterNum)
	for id := range funcs {
		switch id {
		case 1:
			checkers = append(checkers, walkFuncs[id])
		case 2:
			converters = append(converters, walkFuncs[id])
		}
	}
	return &analyzer{
		name,
		inNodes,
		make(chan node.Node, 1),
		newMultiWalker(checkers...),
		newMultiWalker(converters...),
		policies,
	}
}

type analyzer struct {
	name       string
	inNodes    <-chan node.Node
	outNodes   chan node.Node
	checkers   *multiWalker
	converters *multiWalker
	policies   map[string]bool
}

func (a *analyzer) Nodes() <-chan node.Node {
	return a.outNodes
}

func (a *analyzer) enabled(name string) bool {
	return a.policies[name]
}

const (
	toplevelReturn       = "toplevel-return"
	undeclaredVariable   = "undeclared-variable"
	duplicateDeclaration = "duplicate-declaration"
	// XXX maybe this is unnecessary, because the parser doesn't allow
	// tokenUnderscore as variable name.
	underscoreVariableReference = "underscore-variable-reference"
	convertUnderscoreVariable   = "convert-underscore-variable"
	assignmentToConstVariable   = "assignment-to-const-variable"
)

var walkFuncs = []multiWalkFn{
	checkToplevelReturn,
	checkVariable,
	convertVariableNames,
}

func init() {
	def := []struct {
		name        string
		funcID      int
		isChecker   bool
		isConverter bool
		enabled     bool
	}{
		{
			toplevelReturn,
			0,
			true,
			false,
			true,
		},
		{
			undeclaredVariable,
			1,
			true,
			false,
			true,
		},
		{
			duplicateDeclaration,
			1,
			true,
			false,
			true,
		},
		{
			underscoreVariableReference,
			1,
			true,
			false,
			true,
		},
		{
			convertUnderscoreVariable,
			2,
			false,
			true,
			true,
		},
		{
			assignmentToConstVariable,
			1,
			true,
			false,
			true,
		},
	}
	defaultPolicies = make(map[string]bool, len(def))
	ruleMap = make(map[string]rule, len(def))
	for i := range def {
		defaultPolicies[def[i].name] = def[i].enabled
		ruleMap[def[i].name] = rule{def[i].funcID, def[i].isChecker, def[i].isConverter}
	}
}

type multiWalkFn func(*analyzer, *walkCtrl, node.Node) (node.Node, []node.ErrorNode)

var defaultPolicies map[string]bool
var ruleMap map[string]rule

type rule struct {
	funcID      int  // The index number of walkFuncs.
	isChecker   bool // If true, this is checker function.
	isConverter bool // If true, this is converter function.
}

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
		if top, ok := n.TerminalNode().(*topLevelNode); ok {
			result, errs := a.analyze(top)
			if len(errs) > 0 {
				for i := range errs {
					a.emit(&errs[i]) // type error, and so on
				}
				continue
			}
			a.emit(result)
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

func (a *analyzer) analyze(top *topLevelNode) (node.Node, []node.ErrorNode) {
	// Perform semantics checks.
	errs := a.check(top)
	if len(errs) > 0 {
		return nil, errs
	}

	// Infer type (convert the node to *typedNode).
	tNode, errs := a.infer(top)
	if len(errs) > 0 {
		return nil, errs
	}

	// Convert node.
	tNode, errs = a.convert(tNode)
	if len(errs) > 0 {
		return nil, errs
	}

	// Convert *typedNode to node.
	top, err := a.unwrapNode(tNode)
	if err != nil {
		return nil, []node.ErrorNode{*err}
	}

	return top, nil
}

// check checks the semantic errors.
// The checker functions don't change the node.
// it only checks the nodes before infering the types.
// TODO Run each check functions concurrently:
// Pool goroutines to process the checks and run them in the goroutines.
func (a *analyzer) check(top *topLevelNode) []node.ErrorNode {
	errs := make([]node.ErrorNode, 0, 16)
	walkNode(top, func(ctrl *walkCtrl, n node.Node) node.Node {
		_, e := a.checkers.walk(a, ctrl, n)
		errs = append(errs, e...)
		return n
	})
	return errs
}

// checkToplevelReturn checks if returnStatement exists under topLevelNode.
// It doesn't check inside expression and function.
func checkToplevelReturn(a *analyzer, ctrl *walkCtrl, n node.Node) (node.Node, []node.ErrorNode) {
	if n.IsExpr() {
		ctrl.dontFollowInner()
		return n, nil
	}
	switch n.TerminalNode().(type) {
	case *funcStmtOrExpr:
		ctrl.dontFollowInner()
		return n, nil
	case *returnStatement:
		err := a.err(
			errors.New("return statement found at top level"),
			n,
		)
		return n, []node.ErrorNode{*err}
	default:
		return n, nil
	}
}

func newScope() *scope {
	return &scope{
		make([]map[string]*identifierNode, 0, 4),
		make([]map[string]bool, 0, 4),
	}
}

type scope struct {
	vars    []map[string]*identifierNode
	isConst []map[string]bool
}

func (s *scope) push() {
	s.vars = append(s.vars, make(map[string]*identifierNode, 8))
	s.isConst = append(s.isConst, make(map[string]bool, 8))
}

func (s *scope) pop() {
	s.vars = s.vars[:len(s.vars)-1]
	s.isConst = s.isConst[:len(s.isConst)-1]
}

func (s *scope) getVar(name string) (id *identifierNode, isConst bool) {
	id = s.vars[len(s.vars)-1][name]
	isConst = s.isConst[len(s.vars)-1][name]
	return
}

func (s *scope) getOuterVar(name string) (id *identifierNode, isConst bool) {
	for i := len(s.vars) - 1; i >= 0; i-- {
		if s.vars[i][name] != nil {
			id = s.vars[i][name]
			isConst = s.isConst[i][name]
			break
		}
	}
	return
}

func (s *scope) addVar(id *identifierNode) {
	s.vars[len(s.vars)-1][id.value] = id
	s.isConst[len(s.vars)-1][id.value] = false
}

func (s *scope) addConstVar(id *identifierNode) {
	s.vars[len(s.vars)-1][id.value] = id
	s.isConst[len(s.vars)-1][id.value] = true
}

// checkVariable checks:
// * toplevel-return
//   * Variables are used before declaration.
// * undeclared-variable
//   * Duplicate variable decralations exist.
// * underscore-variable-reference
//   * Underscore identifier ("_") is used for the variable which is referenced.
func checkVariable(a *analyzer, _ *walkCtrl, n node.Node) (node.Node, []node.ErrorNode) {
	switch nn := n.TerminalNode().(type) {
	case *topLevelNode:
		return n, a.checkVariable(nn.body, newScope())
	case *funcStmtOrExpr:
		return n, a.checkVariable(nn.body, newScope())
	default:
		return n, nil
	}
}

// Check the scope of the function, but won't check another function's scope.
func (a *analyzer) checkVariable(body []node.Node, scope *scope) []node.ErrorNode {
	errs := make([]node.ErrorNode, 0, 4)
	scope.push()
	for i := range body {
		if _, ok := body[i].TerminalNode().(*funcStmtOrExpr); ok {
			continue // Skip another function
		}
		if vs, isConst := a.getDeclaredVars(body[i]); len(vs) > 0 { // Found declaration.
			for i := range vs {
				var id *identifierNode
				if nn, ok := vs[i].TerminalNode().(*identifierNode); ok {
					id = nn
				} else {
					continue
				}
				if v, _ := scope.getVar(id.value); v != nil {
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
					if isConst {
						scope.addConstVar(id)
					} else {
						scope.addVar(id)
					}
				}
			}
		}
		if e := a.checkInnerBlock(body[i], scope); len(e) > 0 { // Check if,while,...
			errs = append(errs, e...)
		}
		vs, assigned, e := a.getReferenceVars(body[i])
		errs = append(errs, e...)
		for i := range vs { // Found reference variables.
			var id *identifierNode
			if nn, ok := vs[i].TerminalNode().(*identifierNode); ok {
				id = nn
			} else {
				continue
			}
			v, isConst := scope.getOuterVar(id.value)
			if v == nil && a.enabled(undeclaredVariable) {
				err := a.err(
					errors.New("undefined: "+id.value),
					vs[i],
				)
				errs = append(errs, *err)
			}
			if assigned[i] && v != nil && isConst && a.enabled(assignmentToConstVariable) {
				err := a.err(
					errors.New("assignment to const variable: "+id.value),
					vs[i],
				)
				errs = append(errs, *err)
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
func (a *analyzer) getDeclaredVars(n node.Node) ([]node.Node, bool) {
	switch nn := n.TerminalNode().(type) {
	case assignNode:
		isConst := false
		switch nn.(type) {
		case *assignExpr:
			// *assignExpr is assignNode, but is not a declaration!
			return nil, false
		case *constStatement:
			isConst = true
		}
		return nn.GetLeftIdentifiers(), isConst
	case *letDeclareStatement:
		return nn.GetLeftIdentifiers(), false
	case *funcDeclareStatement:
		var id node.Node = &identifierNode{nn.name, true}
		if pos := n.Position(); pos != nil {
			id = node.NewPosNode(pos, id)
		}
		return []node.Node{id}, false
	default:
		return nil, false
	}
}

// Get variable identifier nodes which references to.
// Returned nodes also have a position (node.Position() != nil)
// if original node has a position.
func (a *analyzer) getReferenceVars(n node.Node) ([]node.Node, []bool, []node.ErrorNode) {
	errs := make([]node.ErrorNode, 0, 4)
	ids := make([]node.Node, 0, 8)
	assigned := make([]bool, 0, 8)
	declRoutes := make([][]int, 0, 8)
	assignRoutes := make([][]int, 0, 8)
	walkNode(n, func(ctrl *walkCtrl, n node.Node) node.Node {
		switch nn := n.TerminalNode().(type) {
		case *funcStmtOrExpr:
			ctrl.dontFollowInner() // skip another function.
		case *funcDeclareStatement:
			ctrl.dontFollowInner() // skip another function.
		case *assignExpr:
			// *assignExpr is assignNode, but is not a declaration!
			lhs := append(ctrl.route(), 0)
			assignRoutes = append(assignRoutes, lhs)
		case assignNode:
			lhs := append(ctrl.route(), 0)
			declRoutes = append(declRoutes, lhs)
		case *letDeclareStatement:
			lhs := append(ctrl.route(), 0)
			declRoutes = append(declRoutes, lhs)
		case *identifierNode:
			// The identifierNode is used for variable name, and
			// is not in left-hand side of declaration node.
			if nn.isVarname && !containsRoute(ctrl.route(), declRoutes) {
				if nn.value == "_" {
					if a.enabled(underscoreVariableReference) {
						err := a.err(
							errors.New("underscore variable can be used only in declaration"),
							n,
						)
						errs = append(errs, *err)
					}
				} else {
					assigned = append(assigned, containsRoute(ctrl.route(), assignRoutes))
					ids = append(ids, n)
				}
			}
		}
		return n
	})
	return ids, assigned, errs
}

// convert converts some specific nodes.
// convert *does not* change n inplacely.
// It clones the node, convert, and return it.
func (a *analyzer) convert(tNode *typedNode) (*typedNode, []node.ErrorNode) {
	tNode = tNode.Clone().(*typedNode)
	errs := make([]node.ErrorNode, 0, 16)
	tNode, ok := walkNode(tNode, func(ctrl *walkCtrl, n node.Node) node.Node {
		n, e := a.converters.walk(a, ctrl, n)
		errs = append(errs, e...)
		return n
	}).(*typedNode)
	if !ok {
		err := a.err(
			fmt.Errorf("fatal: convert(): typedNode is required after convert (node = %+v)", tNode),
			tNode,
		)
		return tNode, []node.ErrorNode{*err}
	}
	return tNode, errs
}

// convertVariableNames converts variable names in the scope of body.
// For example, "_varname" -> "__varname", "_" -> "_unused{nr}".
func convertVariableNames(a *analyzer, ctrl *walkCtrl, n node.Node) (node.Node, []node.ErrorNode) {
	switch nn := n.TerminalNode().(type) {
	case *topLevelNode:
		a.convertVariableNames(nn.body, newScope())
	case *funcStmtOrExpr:
		a.convertVariableNames(nn.body, newScope())
	default:
		return n, nil
	}
	return n, nil
}

// TODO shadowing
// TODO use scope
func (a *analyzer) convertVariableNames(body []node.Node, scope *scope) {
	nr := 0
	for i := range body {
		body[i] = walkNode(body[i], func(ctrl *walkCtrl, n node.Node) node.Node {
			var ids []node.Node
			switch nn := n.TerminalNode().(type) {
			case *funcStmtOrExpr:
				ctrl.dontFollowInner()
				return n
			case assignNode:
				ids = nn.GetLeftIdentifiers()
			default:
				return n
			}
			for i := range ids {
				id := ids[i].TerminalNode().(*identifierNode)
				// "_varname" -> "__varname"
				if id.value[0] == '_' && len(id.value) != 1 {
					id.value = "__" + id.value[1:]
				}
				// "_" -> "_unused{nr}"
				if id.value == "_" {
					id.value = "_unused" + strconv.Itoa(nr)
					nr++
				}
			}
			return n
		})
	}
}

// infer infers each node's type and return the tree of *typedNode.
func (a *analyzer) infer(top node.Node) (*typedNode, []node.ErrorNode) {
	typedTop := walkNode(top, func(_ *walkCtrl, n node.Node) node.Node {
		return &typedNode{n.Clone(), ""} // TODO
	}).(*typedNode) // returned node must be *topLevelNode
	return typedTop, nil
}

// unwrapNode converts *typedNode to *topLevelNode.
func (a *analyzer) unwrapNode(tNode *typedNode) (*topLevelNode, *node.ErrorNode) {
	top, ok := walkNode(tNode, func(_ *walkCtrl, n node.Node) node.Node {
		// Unwrap node from typedNode.
		return n.TerminalNode()
	}).(*topLevelNode)
	if !ok {
		return nil, a.err(
			fmt.Errorf("fatal: topLevelNode is required at top level (%+v)", top),
			top,
		)
	}
	return top, nil
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
		ctrl.followInner = true
		ctrl.pop()
		return r
	}

	switch nn := r.TerminalNode().(type) {
	case *topLevelNode:
		for i := range nn.body {
			nn.body[i] = ctrl.walk(nn.body[i], i, f)
		}
	case *importStatement:
	case *funcDeclareStatement:
		ctrl.push(0)
		for i := range nn.args {
			ctrl.push(i)
			nn.args[i].left = ctrl.walk(nn.args[i].left, 0, f)
			if nn.args[i].defaultVal != nil {
				nn.args[i].defaultVal = ctrl.walk(nn.args[i].defaultVal, 1, f)
			}
			ctrl.pop()
		}
		ctrl.pop()
	case *funcStmtOrExpr:
		ctrl.walk(nn.declare, 0, f)
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
	case *letDeclareStatement:
		ctrl.push(0)
		for i := range nn.left {
			nn.left[i].left = ctrl.walk(nn.left[i].left, i, f)
		}
		ctrl.pop()
	case *letAssignStatement:
		nn.left = ctrl.walk(nn.left, 0, f)
		nn.right = ctrl.walk(nn.right, 1, f)
	case *assignExpr:
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

type multiWalker struct {
	callbacks    []multiWalkFn
	ctrlList     []*walkCtrl
	ignoredPaths [][][]int
}

func newMultiWalker(callbacks ...multiWalkFn) *multiWalker {
	ctrlList := make([]*walkCtrl, len(callbacks))
	ignoredPaths := make([][][]int, len(callbacks))
	for i := range callbacks {
		ctrlList[i] = newWalkCtrl()
		ignoredPaths[i] = make([][]int, 0)
	}
	return &multiWalker{callbacks, ctrlList, ignoredPaths}
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
func (s *multiWalker) walk(a *analyzer, ctrl *walkCtrl, n node.Node) (node.Node, []node.ErrorNode) {
	errs := make([]node.ErrorNode, 0, 8)
	route := ctrl.route()
	var e []node.ErrorNode
	for i := range s.callbacks {
		if s.ctrlList[i].followInner && !containsRoute(route, s.ignoredPaths[i]) {
			n, e = s.callbacks[i](a, s.ctrlList[i], n)
			errs = append(errs, e...)
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
	return n, errs
}
