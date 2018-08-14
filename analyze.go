package main

import (
	"errors"
	"fmt"
	"sync"
)

func analyze(p *parser) *analyzer {
	return &analyzer{p.name, p.nodes, make(chan typedNode, 1)}
}

type analyzer struct {
	name       string
	nodes      chan node
	typedNodes chan typedNode
}

type typedNode interface {
	node
	Type() *nodeType
}

type typedTopLevelNode struct {
	*topLevelNode
}

func (node *typedTopLevelNode) Type() *nodeType {
	return nil
}

type typedErrorNode struct {
	*errorNode
}

func (node *typedErrorNode) Type() *nodeType {
	return nil
}

// node is a expression type of this node.
type nodeType struct {
	typ string // expression type
}

// Type returns type itself.
func (t *nodeType) Type() *nodeType {
	return t
}

func (a *analyzer) Run() {
	for node := range a.nodes {
		if toplevel, ok := node.(*topLevelNode); ok {
			a.emit(a.analyze(toplevel))
		} else if e, ok := node.(*errorNode); ok {
			a.emit(&typedErrorNode{e}) // parse error
		} else {
			a.err(fmt.Errorf("unknown node: %+v", node), node)
		}
	}
	close(a.typedNodes)
}

// emit passes an node back to the client.
func (a *analyzer) emit(tNode typedNode) {
	a.typedNodes <- tNode
}

func (a *analyzer) err(err error, node node) {
	pos := node.Position()
	errNode := &errorNode{
		pos,
		fmt.Errorf("[analyze] %s:%d:%d: "+err.Error(), a.name, pos.line, pos.col+1),
	}
	a.emit(&typedErrorNode{errNode})
}

func (a *analyzer) analyze(top *topLevelNode) typedNode {
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		a.analyzeToplevelReturn(top)
		wg.Done()
	}()

	// TODO

	wg.Wait()
	return &typedTopLevelNode{top}
}

// analyzeToplevelReturn checks if returnStatement exists under topLevelNode.
// It doesn't check inside expression and function.
func (a *analyzer) analyzeToplevelReturn(n *topLevelNode) {
	for i := range n.body {
		if n.IsExpr() {
			continue
		}
		if _, ok := n.body[i].(*funcStmtOrExpr); ok {
			continue
		}
		// TODO Check inner nodes of if, while, for, try, ...
		var found *returnStatement
		walkNodes(n.body[i], func(inner node) bool {
			if _, ok := inner.(*funcStmtOrExpr); ok {
				return false
			}
			if ret, ok := inner.(*returnStatement); ok {
				found = ret
				return false
			}
			return true
		})
		if found != nil {
			a.err(errors.New("return statement found at top level"), found)
		}
	}
}

// walkNodes walks node recursively and call f with each node.
// if f(node) == false, walk stops walking inner nodes.
func walkNodes(node node, f func(node) bool) {
	if !f(node) {
		return
	}
	switch n := node.(type) {
	case *topLevelNode:
		for i := range n.body {
			walkNodes(n.body[i], f)
		}
	case *importStatement:
	case *funcStmtOrExpr:
		for i := range n.args {
			if n.args[i].defaultVal != nil {
				walkNodes(n.args[i].defaultVal, f)
			}
		}
		for i := range n.body {
			walkNodes(n.body[i], f)
		}
	case *returnStatement:
		walkNodes(n.left, f)
	case *ifStatement:
		walkNodes(n.cond, f)
		for i := range n.body {
			walkNodes(n.body[i], f)
		}
		for i := range n.els {
			walkNodes(n.els[i], f)
		}
	case *ternaryNode:
		walkNodes(n.cond, f)
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *orNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *andNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *equalNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *equalCiNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *nequalNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *nequalCiNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *greaterNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *greaterCiNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *gequalNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *gequalCiNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *smallerNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *smallerCiNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *sequalNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *sequalCiNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *matchNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *matchCiNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *noMatchNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *noMatchCiNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *isNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *isCiNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *isNotNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *isNotCiNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *addNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *subtractNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *multiplyNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *divideNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *remainderNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *notNode:
		walkNodes(n.left, f)
	case *minusNode:
		walkNodes(n.left, f)
	case *plusNode:
		walkNodes(n.left, f)
	case *sliceNode:
		walkNodes(n.left, f)
		for i := range n.rlist {
			walkNodes(n.rlist[i], f)
		}
	case *callNode:
		walkNodes(n.left, f)
		for i := range n.rlist {
			walkNodes(n.rlist[i], f)
		}
	case *subscriptNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *dotNode:
		walkNodes(n.left, f)
		walkNodes(n.right, f)
	case *identifierNode:
	case *intNode:
	case *floatNode:
	case *stringNode:
	case *listNode:
		for i := range n.value {
			walkNodes(n.value[i], f)
		}
	case *dictionaryNode:
		for i := range n.value {
			walkNodes(n.value[i][0], f)
			walkNodes(n.value[i][1], f)
		}
	case *optionNode:
	case *envNode:
	case *regNode:
	}
}
