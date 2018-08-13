package main

import (
	"errors"
	"fmt"
	"sync"
)

func analyze(p *parser) *analyzer {
	return &analyzer{p.name, p, make(chan node, 1)}
}

type analyzer struct {
	name   string
	parser *parser
	nodes  chan node
}

// Analysis is a result embedded to node.
type Analysis struct {
	typ string // expression type
}

// AnalysisInfo returns Analysis itself.
func (a *Analysis) AnalysisInfo() *Analysis {
	return a
}

func (a *analyzer) Run() {
	for node := range a.parser.nodes {
		if toplevel, ok := node.(*topLevelNode); ok {
			a.analyze(toplevel)
		}
		a.emit(node) // parse errors are also emitted
	}
	close(a.nodes)
}

// emit passes an node back to the client.
func (a *analyzer) emit(node node) {
	a.nodes <- node
}

func (a *analyzer) err(err error, node node) {
	pos := node.Position()
	a.emit(&errorNode{
		pos,
		nil,
		fmt.Errorf("[analyze] %s:%d:%d: "+err.Error(), a.name, pos.line, pos.col+1),
	})
}

func (a *analyzer) analyze(node *topLevelNode) {
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		a.detectToplevelReturn(node)
		wg.Done()
	}()

	// TODO

	wg.Wait()
}

// detectToplevelReturn checks if returnStatement exists under topLevelNode.
// It doesn't check inside expression and function.
func (a *analyzer) detectToplevelReturn(n *topLevelNode) {
	for i := range n.body {
		if n.IsExpr() {
			continue
		}
		if _, ok := n.body[i].(*funcStmtOrExpr); ok {
			continue
		}
		// TODO Check inner nodes of if, while, for, try, ...
		var found *returnStatement
		a.walk(n.body[i], func(inner node) bool {
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

// walk walks node recursively and call f with each node.
// if f(node) == false, walk stops walking inner nodes.
func (a *analyzer) walk(node node, f func(node) bool) {
	if !f(node) {
		return
	}
	switch n := node.(type) {
	case *topLevelNode:
		for i := range n.body {
			a.walk(n.body[i], f)
		}
	case *importStatement:
	case *funcStmtOrExpr:
		for i := range n.args {
			if n.args[i].defaultVal != nil {
				a.walk(n.args[i].defaultVal, f)
			}
		}
		for i := range n.body {
			a.walk(n.body[i], f)
		}
	case *returnStatement:
		a.walk(n.left, f)
	case *ifStatement:
		a.walk(n.cond, f)
		for i := range n.body {
			a.walk(n.body[i], f)
		}
		for i := range n.els {
			a.walk(n.els[i], f)
		}
	case *ternaryNode:
		a.walk(n.cond, f)
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *orNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *andNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *equalNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *equalCiNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *nequalNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *nequalCiNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *greaterNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *greaterCiNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *gequalNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *gequalCiNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *smallerNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *smallerCiNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *sequalNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *sequalCiNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *matchNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *matchCiNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *noMatchNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *noMatchCiNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *isNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *isCiNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *isNotNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *isNotCiNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *addNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *subtractNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *multiplyNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *divideNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *remainderNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *notNode:
		a.walk(n.left, f)
	case *minusNode:
		a.walk(n.left, f)
	case *plusNode:
		a.walk(n.left, f)
	case *sliceNode:
		a.walk(n.left, f)
		for i := range n.rlist {
			a.walk(n.rlist[i], f)
		}
	case *callNode:
		a.walk(n.left, f)
		for i := range n.rlist {
			a.walk(n.rlist[i], f)
		}
	case *subscriptNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *dotNode:
		a.walk(n.left, f)
		a.walk(n.right, f)
	case *identifierNode:
	case *intNode:
	case *floatNode:
	case *stringNode:
	case *listNode:
		for i := range n.value {
			a.walk(n.value[i], f)
		}
	case *dictionaryNode:
		for i := range n.value {
			a.walk(n.value[i][0], f)
			a.walk(n.value[i][1], f)
		}
	case *optionNode:
	case *envNode:
	case *regNode:
	}
}
