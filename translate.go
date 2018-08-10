package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type translator interface {
	Run()
	Readers() <-chan io.Reader
}

func translateAST(p *parser) translator {
	return &astTranslator{p, make(chan io.Reader)}
}

type astTranslator struct {
	parser  *parser
	readers chan io.Reader
}

func (t *astTranslator) Run() {
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

func (t *astTranslator) Readers() <-chan io.Reader {
	return t.readers
}

func (t *astTranslator) emit(r reader) {
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

func newImportStatementReader(stmt *importStatement) reader {
	var builder strings.Builder

	fnlist, err := json.Marshal(stmt.fnlist)
	if err != nil {
		return &errorReader{err}
	}
	pkg, err := json.Marshal(stmt.pkg)
	if err != nil {
		return &errorReader{err}
	}

	builder.WriteString(fmt.Sprintf("importStatement(%v,%v,%v)\n", stmt.brace, string(fnlist), string(pkg)))

	r := strings.NewReader(builder.String())
	return &importStatementReader{r}
}
