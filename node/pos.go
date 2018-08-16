package node

// Pos is offset position from start of the file.
type Pos struct {
	offset int // Current position in the input.
	line   int // The line number of a token (1-origin).
	col    int // The offset from the previous newline of a token (0-origin).
}

// NewPos is the constructor for Pos.
func NewPos(offset, line, col int) *Pos {
	return &Pos{offset, line, col}
}

// Position returns pos itself.
func (p *Pos) Position() *Pos {
	return p
}

// Offset returns the current position in the input.
func (p *Pos) Offset() int {
	return p.offset
}

// Line returns the line number of a token (1-origin).
func (p *Pos) Line() int {
	return p.line
}

// Col returns the offset from the previous newline of a token (0-origin).
func (p *Pos) Col() int {
	return p.col
}
