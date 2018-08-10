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
		case *funcStatement:
			t.emit(newFuncStatementReader(n))
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
	fnlist, err := marshalDefault(stmt.fnlist, "[]")
	if err != nil {
		return &errorReader{err}
	}
	pkg, err := json.Marshal(stmt.pkg)
	if err != nil {
		return &errorReader{err}
	}

	s := fmt.Sprintf("importStatement(%v,%v,%v)\n", stmt.brace, string(fnlist), string(pkg))
	r := strings.NewReader(s)
	return &importStatementReader{r}
}

type funcStatementReader struct {
	io.Reader
}

func newFuncStatementReader(stmt *funcStatement) reader {
	mods, err := marshalDefault(stmt.mods, "[]")
	if err != nil {
		return &errorReader{err}
	}
	name, err := json.Marshal(stmt.name)
	if err != nil {
		return &errorReader{err}
	}
	args, err := marshalDefault(stmt.args, "[]")
	if err != nil {
		return &errorReader{err}
	}
	body, err := marshalDefault(stmt.body, "[]")
	if err != nil {
		return &errorReader{err}
	}

	s := fmt.Sprintf("funcStatement(%v,%v,%v,%v)\n",
		string(mods), string(name), string(args), string(body))
	r := strings.NewReader(s)
	return &funcStatementReader{r}
}

func marshalDefault(v interface{}, defVal string) ([]byte, error) {
	b, err := json.Marshal(v)
	if string(b) == "null" {
		b = []byte(defVal)
	}
	return b, err
}
