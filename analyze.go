package main

import (
	"errors"
	"fmt"
	"strconv"
)

func analyze(p *parser) *analyzer {
	return &analyzer{p.name, p, make(chan node, 1)}
}

type analyzer struct {
	name   string
	parser *parser
	nodes  chan node
}

type typedNode struct {
	node
	typ string // expression type
}

// Node calls inner impl node recursively to get real node.
func (node *typedNode) Node() node {
	return node.node.Node()
}

func (a *analyzer) Run() {
	for node := range a.parser.nodes {
		if top, ok := node.Node().(*topLevelNode); ok {
			top, errs := a.analyze(top)
			if len(errs) > 0 {
				for i := range errs {
					a.emit(&errs[i]) // type error, and so on
				}
				continue
			}
			a.emit(top)
		} else if e, ok := node.Node().(*errorNode); ok {
			a.emit(e) // parse error
		} else {
			err := fmt.Errorf("unknown node: %+v", node)
			a.emit(a.err(err, node))
		}
	}
	close(a.nodes)
}

// emit passes an node back to the client.
func (a *analyzer) emit(n node) {
	a.nodes <- n
}

func (a *analyzer) err(err error, n node) *errorNode {
	pos := n.Position()
	return &errorNode{
		pos,
		fmt.Errorf("[analyze] %s:%d:%d: "+err.Error(), a.name, pos.line, pos.col+1),
	}
}

func (a *analyzer) analyze(top *topLevelNode) (node, []errorNode) {
	result, errs := a.convertPre(top)
	if len(errs) > 0 {
		return nil, errs
	}

	tNode, errs := a.infer(result)
	if len(errs) > 0 {
		return nil, errs
	}

	n, errs := a.convertPost(tNode)
	if len(errs) > 0 {
		return nil, errs
	}

	result, ok := n.Node().(*topLevelNode)
	if !ok {
		err := a.err(
			fmt.Errorf("fatal: topLevelNode is needed at top level (%+v)", n.Node()),
			top,
		)
		return nil, []errorNode{*err}
	}

	return result, errs
}

// convertPre converts some specific nodes.
// It also performs some semantic checks.
func (a *analyzer) convertPre(top *topLevelNode) (*topLevelNode, []errorNode) {
	errs := a.checkToplevelReturn(top)
	top.body = a.convertVariableNames(top.body)
	return top, errs
}

// convertPost converts *typedNode to node.
func (a *analyzer) convertPost(tNode *typedNode) (node, []errorNode) {
	return walkNodes(tNode, func(_ *walkCtrl, n node) node {
		// Unwrap node from typedNode.
		return n.Node()
	}), nil
}

// convertUnderscore converts variable name in the scope of body.
// For example, "_varname" -> "__varname", "_" -> "_unused{nr}".
func (a *analyzer) convertVariableNames(body []node) []node {
	nr := 0
	newbody := make([]node, 0, len(body))
	for i := range body {
		b := walkNodes(body[i], func(ctrl *walkCtrl, n node) node {
			if f, ok := n.Node().(*funcStmtOrExpr); ok {
				f.body = a.convertVariableNames(f.body)
				ctrl.dontFollowInner()
				return f
			}
			var con *constStatement
			if c, ok := n.Node().(*constStatement); ok {
				con = c
			} else {
				return n
			}
			if !con.hasUnderscore {
				return n
			}
			var ids []*identifierNode
			if l, ok := con.left.Node().(*listNode); ok { // Destructuring
				for i := range l.value {
					if id, ok := l.value[i].(*identifierNode); ok {
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
func (a *analyzer) infer(top *topLevelNode) (*typedNode, []errorNode) {
	typedTop := walkNodes(top, func(_ *walkCtrl, n node) node {
		return &typedNode{n, ""} // TODO
	}).(*typedNode) // returned node must be *topLevelNode
	return typedTop, nil
}

// checkToplevelReturn checks if returnStatement exists under topLevelNode.
// It doesn't check inside expression and function.
func (a *analyzer) checkToplevelReturn(top *topLevelNode) []errorNode {
	errNodes := make([]errorNode, 0, 8)
	// Check inner nodes of if, while, for, try, ...
	walkNodes(top, func(ctrl *walkCtrl, n node) node {
		if n.IsExpr() {
			ctrl.dontFollowInner()
			return n
		}
		if _, ok := n.Node().(*funcStmtOrExpr); ok {
			ctrl.dontFollowInner()
			return n
		}
		if ret, ok := n.Node().(*returnStatement); ok {
			errNodes = append(errNodes, *a.err(
				errors.New("return statement found at top level"),
				ret,
			))
		}
		return n
	})
	return errNodes
}

type walkCtrl struct {
	followInner    bool
	followSiblings bool
}

func (ctrl *walkCtrl) dontFollowInner() {
	ctrl.followInner = false
}

func (ctrl *walkCtrl) dontFollowSiblings() {
	ctrl.followSiblings = false
}

// walkNodes walks node recursively and call f with each node.
// if f(node) == false, walk stops walking inner nodes.
func walkNodes(n node, f func(*walkCtrl, node) node) node {
	ctrl := &walkCtrl{true, true}
	return doWalk(ctrl, n, f)
}

func doWalk(ctrl *walkCtrl, n node, f func(*walkCtrl, node) node) node {
	if n == nil {
		return nil
	}
	r := f(ctrl, n)
	if !ctrl.followInner {
		return r
	}
	switch nn := r.Node().(type) {
	case *topLevelNode:
		for i := range nn.body {
			nn.body[i] = doWalk(ctrl, nn.body[i], f)
			if !ctrl.followSiblings {
				ctrl.followSiblings = true
				return r
			}
		}
		return r
	case *importStatement:
		return r
	case *funcStmtOrExpr:
		for i := range nn.args {
			if nn.args[i].defaultVal != nil {
				nn.args[i].defaultVal = doWalk(ctrl, nn.args[i].defaultVal, f)
				if !ctrl.followSiblings {
					ctrl.followSiblings = true
					return r
				}
			}
		}
		for i := range nn.body {
			nn.body[i] = doWalk(ctrl, nn.body[i], f)
			if !ctrl.followSiblings {
				ctrl.followSiblings = true
				return r
			}
		}
		return r
	case *returnStatement:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *ifStatement:
		nn.cond = doWalk(ctrl, nn.cond, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		for i := range nn.body {
			nn.body[i] = doWalk(ctrl, nn.body[i], f)
			if !ctrl.followSiblings {
				ctrl.followSiblings = true
				return r
			}
		}
		for i := range nn.els {
			nn.els[i] = doWalk(ctrl, nn.els[i], f)
			if !ctrl.followSiblings {
				ctrl.followSiblings = true
				return r
			}
		}
		return r
	case *whileStatement:
		nn.cond = doWalk(ctrl, nn.cond, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		for i := range nn.body {
			nn.body[i] = doWalk(ctrl, nn.body[i], f)
			if !ctrl.followSiblings {
				ctrl.followSiblings = true
				return r
			}
		}
		return r
	case *ternaryNode:
		nn.cond = doWalk(ctrl, nn.cond, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *orNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *andNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *equalNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *equalCiNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *nequalNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *nequalCiNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *greaterNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *greaterCiNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *gequalNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *gequalCiNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *smallerNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *smallerCiNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *sequalNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *sequalCiNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *matchNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *matchCiNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *noMatchNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *noMatchCiNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *isNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *isCiNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *isNotNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *isNotCiNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *addNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *subtractNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *multiplyNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *divideNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *remainderNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *notNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *minusNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *plusNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *sliceNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		for i := range nn.rlist {
			nn.rlist[i] = doWalk(ctrl, nn.rlist[i], f)
			if !ctrl.followSiblings {
				ctrl.followSiblings = true
				return r
			}
		}
		return r
	case *callNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		for i := range nn.rlist {
			nn.rlist[i] = doWalk(ctrl, nn.rlist[i], f)
			if !ctrl.followSiblings {
				ctrl.followSiblings = true
				return r
			}
		}
		return r
	case *subscriptNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *dotNode:
		nn.left = doWalk(ctrl, nn.left, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		nn.right = doWalk(ctrl, nn.right, f)
		if !ctrl.followSiblings {
			ctrl.followSiblings = true
			return r
		}
		return r
	case *identifierNode:
		return r
	case *intNode:
		return r
	case *floatNode:
		return r
	case *stringNode:
		return r
	case *listNode:
		for i := range nn.value {
			nn.value[i] = doWalk(ctrl, nn.value[i], f)
			if !ctrl.followSiblings {
				ctrl.followSiblings = true
				return r
			}
		}
		return r
	case *dictionaryNode:
		for i := range nn.value {
			val := nn.value[i]
			for j := range val {
				if val[j] != nil {
					val[j] = doWalk(ctrl, val[j], f)
					if !ctrl.followSiblings {
						ctrl.followSiblings = true
						return r
					}
				}
			}
		}
		return r
	case *optionNode:
		return r
	case *envNode:
		return r
	case *regNode:
		return r
	default:
		return r
	}
}
