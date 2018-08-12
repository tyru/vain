package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

type translator interface {
	Run()
	Readers() <-chan io.Reader
}

func translateSexp(p *parser) translator {
	return &sexpTranslator{p, make(chan io.Reader), "  "}
}

type sexpTranslator struct {
	parser  *parser
	readers chan io.Reader
	indent  string
}

func (t *sexpTranslator) Run() {
	for node := range t.parser.nodes {
		t.emit(t.toReader(node, 0))
	}
	close(t.readers)
}

func (t *sexpTranslator) Readers() <-chan io.Reader {
	return t.readers
}

func (t *sexpTranslator) emit(r io.Reader) {
	t.readers <- r
}

func (t *sexpTranslator) getIndent(level int) string {
	return strings.Repeat(t.indent, level)
}

func (t *sexpTranslator) toReader(node node, level int) io.Reader {
	switch n := node.(type) {
	case *errorNode:
		return &errorReader{n.err}
	case *topLevelNode:
		rs := make([]io.Reader, 0, len(n.body))
		for i := range n.body {
			// topLevelNode doesn't increment level
			if i > 0 {
				rs = append(rs, strings.NewReader("\n"), t.toReader(n.body[i], level))
			} else {
				rs = append(rs, t.toReader(n.body[i], level))
			}
		}
		return io.MultiReader(rs...)
	case *importStatement:
		return t.newImportStatementReader(n, level)
	case *funcStmtOrExpr:
		return t.newFuncReader(n, level)
	case *ternaryNode:
		return t.newTernaryNodeReader(n, level)
	// case *orNode:
	// 	return t.newOrNodeReader(n, level)
	// case *andNode:
	// 	return t.newAndNodeReader(n, level)
	// case *equalNode:
	// 	return t.newEqualNodeReader(n, level)
	// case *equalCiNode:
	// 	return t.newEqualCiNodeReader(n, level)
	// case *nequalNode:
	// 	return t.newNequalNodeReader(n, level)
	// case *nequalCiNode:
	// 	return t.newNequalCiNodeReader(n, level)
	// case *greaterNode:
	// 	return t.newGreaterNodeReader(n, level)
	// case *greaterCiNode:
	// 	return t.newGreaterCiNodeReader(n, level)
	// case *gequalNode:
	// 	return t.newGequalNodeReader(n, level)
	// case *gequalCiNode:
	// 	return t.newGequalCiNodeReader(n, level)
	// case *smallerNode:
	// 	return t.newSmallerNodeReader(n, level)
	// case *smallerCiNode:
	// 	return t.newSmallerCiNodeReader(n, level)
	// case *sequalNode:
	// 	return t.newSequalNodeReader(n, level)
	// case *sequalCiNode:
	// 	return t.newSequalCiNodeReader(n, level)
	// case *matchNode:
	// 	return t.newMatchNodeReader(n, level)
	// case *matchCiNode:
	// 	return t.newMatchCiNodeReader(n, level)
	// case *noMatchNode:
	// 	return t.newNoMatchNodeReader(n, level)
	// case *noMatchCiNode:
	// 	return t.newNoMatchCiNodeReader(n, level)
	// case *isNode:
	// 	return t.newIsNodeReader(n, level)
	// case *isCiNode:
	// 	return t.newIsCiNodeReader(n, level)
	// case *isNotNode:
	// 	return t.newIsNotNodeReader(n, level)
	// case *isNotCiNode:
	// 	return t.newIsNotCiNodeReader(n, level)
	// case *addNode:
	// 	return t.newAddNodeReader(n, level)
	// case *subtractNode:
	// 	return t.newSubtractNodeReader(n, level)
	// case *multiplyNode:
	// 	return t.newMultiplyNodeReader(n, level)
	// case *divideNode:
	// 	return t.newDivideNodeReader(n, level)
	// case *remainderNode:
	// 	return t.newRemainderNodeReader(n, level)
	// case *notNode:
	// 	return t.newNotNodeReader(n, level)
	// case *minusNode:
	// 	return t.newMinusNodeReader(n, level)
	// case *plusNode:
	// 	return t.newPlusNodeReader(n, level)
	// case *sliceNode:
	// 	return t.newSliceNodeReader(n, level)
	// case *callNode:
	// 	return t.newCallNodeReader(n, level)
	// case *subscriptNode:
	// 	return t.newSubscriptNodeReader(n, level)
	// case *dotNode:
	// 	return t.newDotNodeReader(n, level)
	// case *identifierNode:
	// 	return t.newIdentifierNodeReader(n, level)
	case *numberNode:
		return t.newNumberNodeReader(n, level)
	case *stringNode:
		return t.newStringNodeReader(n, level)
		// case *listNode:
		// 	return t.newListNodeReader(n, level)
		// case *dictionaryNode:
		// 	return t.newDictionaryNodeReader(n, level)
		// case *optionNode:
		// 	return t.newOptionNodeReader(n, level)
		// case *envNode:
		// 	return t.newEnvNodeReader(n, level)
		// case *regNode:
		// 	return t.newRegNodeReader(n, level)
	}
	return emptyReader
}

var emptyReader = strings.NewReader("")

type errorReader struct {
	err error
}

func (r *errorReader) Read(p []byte) (n int, err error) {
	return 0, r.err
}

type lazyReader struct {
	init   func() (io.Reader, int, error)
	reader io.Reader
	once   sync.Once
}

func (r *lazyReader) Read(p []byte) (n int, err error) {
	r.once.Do(func() {
		r.reader, n, err = r.init()
	})
	if err != nil {
		return n, err
	}
	return r.reader.Read(p)
}

func (t *sexpTranslator) newImportStatementReader(stmt *importStatement, level int) io.Reader {
	fnlist, err := toSexp(stmt.fnlist, "[]")
	if err != nil {
		return &errorReader{err}
	}
	pkg, err := toSexp(stmt.pkg, "")
	if err != nil {
		return &errorReader{err}
	}

	s := fmt.Sprintf("%s(import %v %v %v)", t.getIndent(level), stmt.brace, fnlist, pkg)
	return strings.NewReader(s)
}

func (t *sexpTranslator) newFuncReader(f *funcStmtOrExpr, level int) io.Reader {
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

	s := fmt.Sprintf("%s(func %v %v %v %v %v)", t.getIndent(level), mods, name, args, f.bodyIsStmt, body)
	return strings.NewReader(s)
}

func (t *sexpTranslator) newTernaryNodeReader(node *ternaryNode, level int) io.Reader {
	return &lazyReader{init: func() (io.Reader, int, error) {
		var cond bytes.Buffer
		n, err := io.Copy(&cond, t.toReader(node.cond, level))
		if err != nil {
			return nil, int(n), err
		}
		var left bytes.Buffer
		n, err = io.Copy(&left, t.toReader(node.left, level))
		if err != nil {
			return nil, int(n), err
		}
		var right bytes.Buffer
		n, err = io.Copy(&right, t.toReader(node.right, level))
		if err != nil {
			return nil, int(n), err
		}
		s := fmt.Sprintf("(?: %v %v %v)", cond.String(), left.String(), right.String())
		r := strings.NewReader(s)
		return r, 0, nil
	}}
}

func (t *sexpTranslator) newNumberNodeReader(node *numberNode, level int) io.Reader {
	return strings.NewReader(node.value)
}

func (t *sexpTranslator) newStringNodeReader(node *stringNode, level int) io.Reader {
	value, err := toSexp(node.value, "")
	if err != nil {
		return &errorReader{err}
	}
	return strings.NewReader(value)
}

func toSexp(v interface{}, defVal string) (string, error) {
	switch vv := v.(type) {
	case vainString:
		// Convert to JSON string.
		s, err := vv.eval()
		if err != nil {
			return "", err
		}
		return toSexp(s, defVal)
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
