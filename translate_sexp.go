package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func translateSexp(a *analyzer) translator {
	return &sexpTranslator{a.name, a.nodes, make(chan io.Reader), "  "}
}

type sexpTranslator struct {
	name    string
	nodes   <-chan node
	readers chan io.Reader
	indent  string
}

func (t *sexpTranslator) Run() {
	for node := range t.nodes {
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
	// fmt.Printf("%s: %+v (%+v)\n", t.name, node, reflect.TypeOf(node))
	switch n := node.(type) {
	case *errorNode:
		return &errorReader{n.err}
	case *topLevelNode:
		return t.newTopLevelNodeReader(n, level)
	case *importStatement:
		return t.newImportStatementReader(n, level)
	case *funcStmtOrExpr:
		return t.newFuncReader(n, level)
	case *ternaryNode:
		return t.newTernaryNodeReader(n, level)
	case *orNode:
		return t.newBinaryOpNodeReader(n, level, "||")
	case *andNode:
		return t.newBinaryOpNodeReader(n, level, "&&")
	case *equalNode:
		return t.newBinaryOpNodeReader(n, level, "==")
	case *equalCiNode:
		return t.newBinaryOpNodeReader(n, level, "==?")
	case *nequalNode:
		return t.newBinaryOpNodeReader(n, level, "!=")
	case *nequalCiNode:
		return t.newBinaryOpNodeReader(n, level, "!=?")
	case *greaterNode:
		return t.newBinaryOpNodeReader(n, level, ">")
	case *greaterCiNode:
		return t.newBinaryOpNodeReader(n, level, ">?")
	case *gequalNode:
		return t.newBinaryOpNodeReader(n, level, ">=")
	case *gequalCiNode:
		return t.newBinaryOpNodeReader(n, level, ">=?")
	case *smallerNode:
		return t.newBinaryOpNodeReader(n, level, "<")
	case *smallerCiNode:
		return t.newBinaryOpNodeReader(n, level, "<?")
	case *sequalNode:
		return t.newBinaryOpNodeReader(n, level, "<=")
	case *sequalCiNode:
		return t.newBinaryOpNodeReader(n, level, "<=?")
	case *matchNode:
		return t.newBinaryOpNodeReader(n, level, "=~")
	case *matchCiNode:
		return t.newBinaryOpNodeReader(n, level, "=~?")
	case *noMatchNode:
		return t.newBinaryOpNodeReader(n, level, "!~")
	case *noMatchCiNode:
		return t.newBinaryOpNodeReader(n, level, "!~?")
	case *isNode:
		return t.newBinaryOpNodeReader(n, level, "is")
	case *isCiNode:
		return t.newBinaryOpNodeReader(n, level, "is?")
	case *isNotNode:
		return t.newBinaryOpNodeReader(n, level, "isnot")
	case *isNotCiNode:
		return t.newBinaryOpNodeReader(n, level, "isnot?")
	case *addNode:
		return t.newBinaryOpNodeReader(n, level, "+")
	case *subtractNode:
		return t.newBinaryOpNodeReader(n, level, "-")
	case *multiplyNode:
		return t.newBinaryOpNodeReader(n, level, "*")
	case *divideNode:
		return t.newBinaryOpNodeReader(n, level, "/")
	case *remainderNode:
		return t.newBinaryOpNodeReader(n, level, "%")
	case *notNode:
		return t.newUnaryOpNodeReader(n, level, "!")
	case *minusNode:
		return t.newUnaryOpNodeReader(n, level, "-")
	case *plusNode:
		return t.newUnaryOpNodeReader(n, level, "+")
	case *sliceNode:
		return t.newSliceNodeReader(n, level)
	case *callNode:
		return t.newCallNodeReader(n, level)
	case *subscriptNode:
		return t.newBinaryOpNodeReader(n, level, "subscript")
	case *dotNode:
		return t.newBinaryOpNodeReader(n, level, "dot")
	case *identifierNode:
		return t.newIdentifierNodeReader(n, level)
	case *numberNode:
		return t.newNumberNodeReader(n, level)
	case *stringNode:
		return t.newStringNodeReader(n, level)
	case *listNode:
		return t.newListNodeReader(n, level)
	case *dictionaryNode:
		return t.newDictionaryNodeReader(n, level)
	case *optionNode:
		return t.newLiteralNodeReader(n, level, "option")
	case *envNode:
		return t.newLiteralNodeReader(n, level, "env")
	case *regNode:
		return t.newLiteralNodeReader(n, level, "reg")
	default:
		return &errorReader{fmt.Errorf("unknown node: %+v", node)}
	}
}

func (t *sexpTranslator) newTopLevelNodeReader(node *topLevelNode, level int) io.Reader {
	rs := make([]io.Reader, 0, len(node.body))
	for i := range node.body {
		if i > 0 {
			rs = append(rs, strings.NewReader("\n"))
		}
		// topLevelNode doesn't increment level
		rs = append(rs, t.toReader(node.body[i], level))
	}
	return io.MultiReader(rs...)
}

func (t *sexpTranslator) newImportStatementReader(stmt *importStatement, level int) io.Reader {
	args := make([]string, 0, 2)
	pkg, err := t.toJSONString(&stmt.pkg)
	if err != nil {
		return &errorReader{err}
	}
	pkgPair := make([]string, 0, 2)
	pkgPair = append(pkgPair, pkg)
	if stmt.pkgAlias != "" {
		pkgPair = append(pkgPair, "'"+stmt.pkgAlias)
	}
	args = append(args, "("+strings.Join(pkgPair, " ")+")")

	if stmt.fnlist != nil {
		fnPairList := make([]string, 0, len(stmt.fnlist))
		for i := range stmt.fnlist {
			fnPairList = append(fnPairList, "("+strings.Join(stmt.fnlist[i], " ")+")")
		}
		args = append(args, "("+strings.Join(fnPairList, " ")+")")
	}

	s := fmt.Sprintf("%s(import %s)", t.getIndent(level), strings.Join(args, " "))
	return strings.NewReader(s)
}

func (t *sexpTranslator) newFuncReader(f *funcStmtOrExpr, level int) io.Reader {
	args := make([]string, 0, len(f.args))
	for i := range f.args {
		arg := f.args[i]
		if arg.typ != "" {
			args = append(args, fmt.Sprintf("(%s : %s)", arg.name, arg.typ))
		} else {
			var buf bytes.Buffer
			_, err := io.Copy(&buf, t.toReader(arg.defaultVal, level+1))
			if err != nil {
				return &errorReader{err}
			}
			args = append(args, fmt.Sprintf("(%s = %s)", arg.name, buf.String()))
		}
	}
	bodyList := make([]string, 0, len(f.body))
	for i := range f.body {
		var buf bytes.Buffer
		_, err := io.Copy(&buf, t.toReader(f.body[i], level+1))
		if err != nil {
			return &errorReader{err}
		}
		bodyList = append(bodyList, buf.String())
	}

	var s string
	if f.bodyIsStmt {
		s = fmt.Sprintf("%s(func (%s) %s (%s) (%s))",
			t.getIndent(level),
			strings.Join(f.mods, " "),
			f.name,
			strings.Join(args, " "),
			strings.Join(bodyList, " "))
	} else {
		s = fmt.Sprintf("%s(func (%s) %s (%s) %s)",
			t.getIndent(level),
			strings.Join(f.mods, " "),
			f.name,
			strings.Join(args, " "),
			bodyList[0])
	}
	return strings.NewReader(s)
}

func (t *sexpTranslator) newTernaryNodeReader(node *ternaryNode, level int) io.Reader {
	var cond bytes.Buffer
	_, err := io.Copy(&cond, t.toReader(node.cond, level))
	if err != nil {
		return &errorReader{err}
	}
	var left bytes.Buffer
	_, err = io.Copy(&left, t.toReader(node.left, level))
	if err != nil {
		return &errorReader{err}
	}
	var right bytes.Buffer
	_, err = io.Copy(&right, t.toReader(node.right, level))
	if err != nil {
		return &errorReader{err}
	}
	s := fmt.Sprintf("(?: %s %s %s)", cond.String(), left.String(), right.String())
	return strings.NewReader(s)
}

func (t *sexpTranslator) newBinaryOpNodeReader(node binaryOpNode, level int, opstr string) io.Reader {
	var left bytes.Buffer
	_, err := io.Copy(&left, t.toReader(node.Left(), level))
	if err != nil {
		return &errorReader{err}
	}
	var right bytes.Buffer
	_, err = io.Copy(&right, t.toReader(node.Right(), level))
	if err != nil {
		return &errorReader{err}
	}
	s := fmt.Sprintf("(%s %s %s)", opstr, left.String(), right.String())
	return strings.NewReader(s)
}

func (t *sexpTranslator) newUnaryOpNodeReader(node unaryOpNode, level int, opstr string) io.Reader {
	var value bytes.Buffer
	_, err := io.Copy(&value, t.toReader(node.Value(), level))
	if err != nil {
		return &errorReader{err}
	}
	s := fmt.Sprintf("(%s %s)", opstr, value.String())
	return strings.NewReader(s)
}

func (t *sexpTranslator) newSliceNodeReader(node *sliceNode, level int) io.Reader {
	var left bytes.Buffer
	_, err := io.Copy(&left, t.toReader(node.left, level))
	if err != nil {
		return &errorReader{err}
	}
	from := "null"
	if node.rlist[0] != nil {
		var buf bytes.Buffer
		_, err := io.Copy(&buf, t.toReader(node.rlist[0], level))
		if err != nil {
			return &errorReader{err}
		}
		from = buf.String()
	}
	to := "null"
	if node.rlist[1] != nil {
		var buf bytes.Buffer
		_, err := io.Copy(&buf, t.toReader(node.rlist[1], level))
		if err != nil {
			return &errorReader{err}
		}
		to = buf.String()
	}
	s := fmt.Sprintf("(slice %s %s %s)", left.String(), from, to)
	return strings.NewReader(s)
}

func (t *sexpTranslator) newCallNodeReader(node *callNode, level int) io.Reader {
	var left bytes.Buffer
	_, err := io.Copy(&left, t.toReader(node.left, level))
	if err != nil {
		return &errorReader{err}
	}
	rlist := make([]string, 0, len(node.rlist))
	for i := range node.rlist {
		var arg bytes.Buffer
		_, err := io.Copy(&arg, t.toReader(node.rlist[i], level))
		if err != nil {
			return &errorReader{err}
		}
		rlist = append(rlist, arg.String())
	}
	var s string
	if len(rlist) > 0 {
		s = fmt.Sprintf("(call %s %s)", left.String(), strings.Join(rlist, " "))
	} else {
		s = fmt.Sprintf("(call %s)", left.String())
	}
	return strings.NewReader(s)
}

func (t *sexpTranslator) newIdentifierNodeReader(node *identifierNode, level int) io.Reader {
	return strings.NewReader("'" + node.value)
}

func (t *sexpTranslator) newNumberNodeReader(node *numberNode, level int) io.Reader {
	return strings.NewReader(node.value)
}

func (t *sexpTranslator) newStringNodeReader(node *stringNode, level int) io.Reader {
	value, err := t.toJSONString(&node.value)
	if err != nil {
		return &errorReader{err}
	}
	return strings.NewReader(value)
}

func (t *sexpTranslator) newLiteralNodeReader(node literalNode, level int, opstr string) io.Reader {
	value, err := json.Marshal(node.Value())
	if err != nil {
		return &errorReader{err}
	}
	s := fmt.Sprintf("(%s %s)", opstr, value)
	return strings.NewReader(s)
}

func (t *sexpTranslator) newListNodeReader(node *listNode, level int) io.Reader {
	args := make([]string, 0, len(node.value)+1)
	args = append(args, "list")
	for i := range node.value {
		var arg bytes.Buffer
		_, err := io.Copy(&arg, t.toReader(node.value[i], level))
		if err != nil {
			return &errorReader{err}
		}
		args = append(args, arg.String())
	}
	s := "(" + strings.Join(args, " ") + ")"
	return strings.NewReader(s)
}

func (t *sexpTranslator) newDictionaryNodeReader(node *dictionaryNode, level int) io.Reader {
	args := make([]string, 0, len(node.value)+1)
	args = append(args, "dict")
	for i := range node.value {
		keyNode := node.value[i][0]
		var key bytes.Buffer
		_, err := io.Copy(&key, t.toReader(keyNode, level))
		if err != nil {
			return &errorReader{err}
		}
		valNode := node.value[i][1]
		var val bytes.Buffer
		_, err = io.Copy(&val, t.toReader(valNode, level))
		if err != nil {
			return &errorReader{err}
		}
		args = append(args, fmt.Sprintf("(%s %s)", key.String(), val.String()))
	}
	s := "(" + strings.Join(args, " ") + ")"
	return strings.NewReader(s)
}

func (t *sexpTranslator) toJSONString(vs *vainString) (string, error) {
	s, err := vs.eval()
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
