package main

import (
	"fmt"
	"sync"
)

func detect(a *analyzer) *detector {
	return &detector{a.name, a.typedNodes, make(chan node, 1)}
}

type detector struct {
	name       string
	typedNodes chan typedNode
	nodes      chan node
}

// Run rewrites nodes to be a correct vim script.
func (d *detector) Run() {
	for tNode := range d.typedNodes {
		if top, ok := tNode.(*typedTopLevelNode); ok {
			d.emit(d.detect(top))
		} else if e, ok := tNode.(*typedErrorNode); ok {
			d.emit(e)
		} else {
			d.err(fmt.Errorf("unknown node: %+v", tNode), tNode)
		}
	}
	close(d.nodes)
}

// emit passes an node back to the client.
func (d *detector) emit(node node) {
	d.nodes <- node
}

func (d *detector) err(err error, node node) {
	pos := node.Position()
	errNode := &errorNode{
		pos,
		fmt.Errorf("[analyze] %s:%d:%d: "+err.Error(), d.name, pos.line, pos.col+1),
	}
	d.emit(&typedErrorNode{errNode})
}

func (d *detector) detect(top *typedTopLevelNode) node {
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		d.rewriteUnderscore(top)
		wg.Done()
	}()

	// TODO

	wg.Wait()
	return top.topLevelNode
}

// rewriteUnderscore rewrites underscore nodes to be unused variables
// which doesn't conflict with others.
func (d *detector) rewriteUnderscore(top *typedTopLevelNode) {
	bodies := make(chan []node, 16)
	done := make(chan bool, 1)

	go func() {
		for body := range bodies {
			d.rewriteUnderscoreBody(body)
		}
		done <- true
	}()

	walkNodes(top, func(n node) bool {
		switch nn := n.(type) {
		case *topLevelNode:
			bodies <- nn.body
		case *funcStmtOrExpr:
			bodies <- nn.body
		}
		return true
	})
	close(bodies)

	<-done
}

func (d *detector) rewriteUnderscoreBody(body []node) {
	// TODO
}
