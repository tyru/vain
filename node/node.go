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

// NewPosNode is the constructor for PosNode.
func NewPosNode(pos *Pos, n Node) *PosNode {
	return &PosNode{pos, n}
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
	err error
	*Pos
}

// NewErrorNode is the constructor for ErrorNode.
func NewErrorNode(err error, pos *Pos) *ErrorNode {
	return &ErrorNode{err, pos}
}

func (node *ErrorNode) Error() string {
	return node.err.Error()
}

// TerminalNode returns itself.
func (node *ErrorNode) TerminalNode() Node {
	return node
}

// IsExpr returns false.
func (node *ErrorNode) IsExpr() bool {
	return false
}
