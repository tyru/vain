package main

import (
	"io"
	"strings"
)

func translate(p *parser) *translator {
	return &translator{p, make(chan io.Reader)}
}

type translator struct {
	parser  *parser
	readers chan io.Reader
}

func (t *translator) run() {
	for node := range t.parser.nodes {
		switch n := node.(type) {
		case *errorNode:
			t.emit(&errorReader{n.err})
		case *importStatement:
			t.emit(newImportStatementReader(n))
		}
	}
	close(t.readers)
}

func (t *translator) emit(r reader) {
	t.readers <- r
}

type reader io.Reader

type errorReader struct {
	err error
}

func (r *errorReader) Read(p []byte) (n int, err error) {
	return 0, r.err
}

type importStatementReader struct {
	io.Reader
}

func newImportStatementReader(stmt *importStatement) *importStatementReader {
	var builder strings.Builder

	builder.WriteString("import ")
	if stmt.brace {
		builder.WriteString("{ ")
	}
	first := true
	for _, pair := range stmt.fnlist {
		if !first {
			builder.WriteString(", ")
		}
		if pair[0] == pair[1] {
			builder.WriteString(pair[0])
		} else {
			builder.WriteString(pair[0] + " as " + pair[1])
		}
		first = false
	}
	if stmt.brace {
		builder.WriteString(" } ")
	}
	builder.WriteString(" from " + stmt.pkg + "\n")

	r := strings.NewReader(builder.String())
	return &importStatementReader{r}
}
