package node

// Node is a primitive type to be used by parser, analyzer, translator.
type Node interface {
	Clone() Node
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

// Clone clones itself.
func (n *PosNode) Clone() Node {
	var pos *Pos
	if n.Pos != nil {
		pos = NewPos(n.Pos.offset, n.Pos.line, n.Pos.col)
	}
	var inner Node
	if n.Node != nil {
		inner = n.Node.Clone()
	}
	return &PosNode{pos, inner}
}

// Position returns pos.
func (n *PosNode) Position() *Pos {
	return n.Pos.Position()
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

// Clone clones itself.
func (n *ErrorNode) Clone() Node {
	var pos *Pos
	if n.Pos != nil {
		pos = NewPos(n.Pos.offset, n.Pos.line, n.Pos.col)
	}
	return &ErrorNode{n.err, pos}
}

func (n *ErrorNode) Error() string {
	return n.err.Error()
}

// TerminalNode returns itself.
func (n *ErrorNode) TerminalNode() Node {
	return n
}

// IsExpr returns false.
func (n *ErrorNode) IsExpr() bool {
	return false
}
