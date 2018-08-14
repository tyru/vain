package main

import (
	"fmt"
	"reflect"
)

func detect(a *analyzer) *detector {
	return &detector{a.name, a.typedNodes, make(chan node, 1)}
}

type detector struct {
	name       string
	typedNodes chan typedNode
	nodes      chan node
}

// Run converts nodes to be a correct vim script.
// If semantic errors are found, emits *errorNode.
func (d *detector) Run() {
	for tNode := range d.typedNodes {
		switch node := tNode.Node().(type) {
		case *topLevelNode:
			d.emit(d.detect(&tNode))
		case *errorNode:
			d.emit(node)
		default:
			d.err(
				fmt.Errorf(
					"fatal: topLevelNode or errorNode is needed at top level (%+v)",
					reflect.TypeOf(tNode.node),
				),
				node.Position(),
			)
		}
	}
	close(d.nodes)
}

// emit passes an node back to the client.
func (d *detector) emit(node node) {
	d.nodes <- node
}

func (d *detector) err(err error, pos *Pos) {
	d.emit(&errorNode{
		pos,
		fmt.Errorf("[detect] %s:%d:%d: "+err.Error(), d.name, pos.line, pos.col+1),
	})
}

// detect detects semantic errors.
// And it converts typedNode to node.
// If semantic errors exist, *errorNode is returned.
func (d *detector) detect(top *typedNode) node {
	return walkNodes(top, func(_ *walkCtrl, n node) node {
		// TODO detect semantic errors.

		// Unwrap node from typedNode.
		return n.Node()
	})
}
