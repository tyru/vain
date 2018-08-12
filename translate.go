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

func translateSexp(p *parser) translator {
	return &sexpTranslator{p, make(chan io.Reader)}
}

type sexpTranslator struct {
	parser  *parser
	readers chan io.Reader
}

func (t *sexpTranslator) Run() {
	for node := range t.parser.nodes {
		t.emit(toReader(node))
	}
	close(t.readers)
}

func toReader(node node) io.Reader {
	switch n := node.(type) {
	case *errorNode:
		return &errorReader{n.err}
	case *topLevelNode:
		rs := make([]io.Reader, 0, len(n.body))
		for i := range n.body {
			if i > 0 {
				rs = append(rs, strings.NewReader("\n"), toReader(n.body[i]))
			} else {
				rs = append(rs, toReader(n.body[i]))
			}
		}
		return io.MultiReader(rs...)
	case *importStatement:
		return newImportStatementReader(n)
	case *funcStmtOrExpr:
		return newFuncReader(n)
	}
	return nopReader
}

var nopReader = &nopReaderT{}

type nopReaderT struct{}

func (nopReaderT) Read(p []byte) (n int, err error) {
	return len(p), nil
}

func (t *sexpTranslator) Readers() <-chan io.Reader {
	return t.readers
}

func (t *sexpTranslator) emit(r io.Reader) {
	t.readers <- r
}

type errorReader struct {
	err error
}

func (r *errorReader) Read(p []byte) (n int, err error) {
	return 0, r.err
}

type importStatementReader struct {
	io.Reader
}

func newImportStatementReader(stmt *importStatement) io.Reader {
	fnlist, err := toSexp(stmt.fnlist, "[]")
	if err != nil {
		return &errorReader{err}
	}
	pkg, err := toSexp(stmt.pkg, "")
	if err != nil {
		return &errorReader{err}
	}

	s := fmt.Sprintf("(import %v %v %v)", stmt.brace, fnlist, pkg)
	r := strings.NewReader(s)
	return &importStatementReader{r}
}

type funcReader struct {
	io.Reader
}

func newFuncReader(f *funcStmtOrExpr) io.Reader {
	mods, err := toSexp(f.mods, "[]")
	if err != nil {
		return &errorReader{err}
	}
	name, err := toSexp(f.name, "")
	if err != nil {
		return &errorReader{err}
	}
	args, err := toSexp(f.args, "[]")
	if err != nil {
		return &errorReader{err}
	}
	body, err := toSexp(f.body, "[]")
	if err != nil {
		return &errorReader{err}
	}

	s := fmt.Sprintf("(func %v %v %v %v %v)", mods, name, args, f.bodyIsStmt, body)
	r := strings.NewReader(s)
	return &funcReader{r}
}

func toSexp(v interface{}, defVal string) (string, error) {
	switch vv := v.(type) {
	case map[string]interface{}:
		elems := make([]string, 0, len(vv)+1)
		elems = append(elems, "dict")
		for k := range vv {
			s, err := toSexp(vv[k], "")
			if err != nil {
				return "", err
			}
			elems = append(elems, fmt.Sprintf("(%v %v)", k, s))
		}
		s := "(" + strings.Join(elems, " ") + ")"
		return s, nil
	case []interface{}:
		elems := make([]string, 0, len(vv)+1)
		elems = append(elems, "list")
		for i := range vv {
			s, err := toSexp(vv[i], "")
			if err != nil {
				return "", err
			}
			elems = append(elems, s)
		}
		s := "(" + strings.Join(elems, " ") + ")"
		return s, nil
	default:
		b, err := json.Marshal(vv)
		return string(b), err
	}
}
