package node

// Pos is offset position from start of the file.
type Pos struct {
	Offset int // Current position in the input.
	Line   int // The line number of a token (1-origin).
	Col    int // The offset from the previous newline of a token (0-origin).
}

// Position returns pos itself.
func (p *Pos) Position() *Pos {
	return p
}

// Positioner can return the position of the node.
type Positioner interface {
	Position() *Pos
}
