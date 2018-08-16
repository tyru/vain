package node

// Node is a primitive type to be used by parser, analyzer, translator.
type Node interface {
	TerminalNode() Node
	Position() *Pos
	IsExpr() bool
}

// PosNode has the node and its position.
type PosNode struct {
	*Pos
	Node
}

// Position returns pos.
func (p *PosNode) Position() *Pos {
	return p.Pos.Position()
}

// ErrorNode has the node, its error, and maybe its position (nil-able).
// ErrorNode is also used for node error like syntax error.
// Because it's a bother to use the above variables
// for representing parse error of a node.
type ErrorNode struct {
	Err error
	*Pos
}

func (node *ErrorNode) Error() string {
	return node.Err.Error()
}

// TerminalNode returns itself.
func (node *ErrorNode) TerminalNode() Node {
	return node
}

// IsExpr returns false.
func (node *ErrorNode) IsExpr() bool {
	return false
}
