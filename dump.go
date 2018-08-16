package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/tyru/vain/node"
)

// TODO indent
// TODO newline
// TODO customize indent, newline

func dump(name string, inNodes <-chan node.Node) *dumper {
	return &dumper{name, inNodes, make(chan io.Reader), "  "}
}

type dumper struct {
	name       string
	inNodes    <-chan node.Node
	outReaders chan io.Reader
	indent     string
}

func (d *dumper) Run() {
	for node := range d.inNodes {
		d.emit(d.toReader(node, 0))
	}
	close(d.outReaders)
}

func (d *dumper) Readers() <-chan io.Reader {
	return d.outReaders
}

func (d *dumper) emit(r io.Reader) {
	d.outReaders <- r
}

func (d *dumper) err(err error, n node.Node) io.Reader {
	if pos := n.Position(); pos != nil {
		return &errorReader{
			fmt.Errorf("[dump] %s:%d:%d: "+err.Error(), d.name, pos.Line(), pos.Col()+1),
		}
	}
	return &errorReader{
		fmt.Errorf("[dump] %s: "+err.Error(), d.name),
	}
}

func (d *dumper) getIndent(level int) string {
	return strings.Repeat(d.indent, level)
}

func (d *dumper) toReader(node node.Node, level int) io.Reader {
	// fmt.Printf("%s: %+v (%+v)\n", d.name, node, reflect.TypeOf(node))
	switch n := node.TerminalNode().(type) {
	case error:
		return &errorReader{n}
	case *topLevelNode:
		return d.newTopLevelNodeReader(n, level)
	case *importStatement:
		return d.newImportStatementReader(n, level)
	case *funcStmtOrExpr:
		return d.newFuncReader(n, level)
	case *returnStatement:
		return d.newReturnNodeReader(n, level)
	case *constStatement:
		return d.newConstStatementReader(n, level)
	case *letStatement:
		return d.newLetStatementReader(n, level)
	case *ifStatement:
		return d.newIfStatementReader(n, level)
	case *whileStatement:
		return d.newWhileStatementReader(n, level)
	case *forStatement:
		return d.newForStatementReader(n, level)
	case *ternaryNode:
		return d.newTernaryNodeReader(n, level)
	case *orNode:
		return d.newBinaryOpNodeReader(n, level, "||")
	case *andNode:
		return d.newBinaryOpNodeReader(n, level, "&&")
	case *equalNode:
		return d.newBinaryOpNodeReader(n, level, "==")
	case *equalCiNode:
		return d.newBinaryOpNodeReader(n, level, "==?")
	case *nequalNode:
		return d.newBinaryOpNodeReader(n, level, "!=")
	case *nequalCiNode:
		return d.newBinaryOpNodeReader(n, level, "!=?")
	case *greaterNode:
		return d.newBinaryOpNodeReader(n, level, ">")
	case *greaterCiNode:
		return d.newBinaryOpNodeReader(n, level, ">?")
	case *gequalNode:
		return d.newBinaryOpNodeReader(n, level, ">=")
	case *gequalCiNode:
		return d.newBinaryOpNodeReader(n, level, ">=?")
	case *smallerNode:
		return d.newBinaryOpNodeReader(n, level, "<")
	case *smallerCiNode:
		return d.newBinaryOpNodeReader(n, level, "<?")
	case *sequalNode:
		return d.newBinaryOpNodeReader(n, level, "<=")
	case *sequalCiNode:
		return d.newBinaryOpNodeReader(n, level, "<=?")
	case *matchNode:
		return d.newBinaryOpNodeReader(n, level, "=~")
	case *matchCiNode:
		return d.newBinaryOpNodeReader(n, level, "=~?")
	case *noMatchNode:
		return d.newBinaryOpNodeReader(n, level, "!~")
	case *noMatchCiNode:
		return d.newBinaryOpNodeReader(n, level, "!~?")
	case *isNode:
		return d.newBinaryOpNodeReader(n, level, "is")
	case *isCiNode:
		return d.newBinaryOpNodeReader(n, level, "is?")
	case *isNotNode:
		return d.newBinaryOpNodeReader(n, level, "isnot")
	case *isNotCiNode:
		return d.newBinaryOpNodeReader(n, level, "isnot?")
	case *addNode:
		return d.newBinaryOpNodeReader(n, level, "+")
	case *subtractNode:
		return d.newBinaryOpNodeReader(n, level, "-")
	case *multiplyNode:
		return d.newBinaryOpNodeReader(n, level, "*")
	case *divideNode:
		return d.newBinaryOpNodeReader(n, level, "/")
	case *remainderNode:
		return d.newBinaryOpNodeReader(n, level, "%")
	case *notNode:
		return d.newUnaryOpNodeReader(n, level, "!")
	case *minusNode:
		return d.newUnaryOpNodeReader(n, level, "-")
	case *plusNode:
		return d.newUnaryOpNodeReader(n, level, "+")
	case *sliceNode:
		return d.newSliceNodeReader(n, level)
	case *callNode:
		return d.newCallNodeReader(n, level)
	case *subscriptNode:
		return d.newBinaryOpNodeReader(n, level, "subscript")
	case *dotNode:
		return d.newBinaryOpNodeReader(n, level, "dot")
	case *identifierNode:
		return d.newIdentifierNodeReader(n, level)
	case *intNode:
		return d.newIntNodeReader(n, level)
	case *floatNode:
		return d.newFloatNodeReader(n, level)
	case *stringNode:
		return d.newStringNodeReader(n, level)
	case *listNode:
		return d.newListNodeReader(n, level)
	case *dictionaryNode:
		return d.newDictionaryNodeReader(n, level)
	case *optionNode:
		return d.newLiteralNodeReader(n, level, "option")
	case *envNode:
		return d.newLiteralNodeReader(n, level, "env")
	case *regNode:
		return d.newLiteralNodeReader(n, level, "reg")
	case *commentNode:
		return d.newLiteralNodeReader(n, level, "#")
	default:
		return d.err(fmt.Errorf("unknown node: %+v", node), node)
	}
}

func (d *dumper) newTopLevelNodeReader(node *topLevelNode, level int) io.Reader {
	rs := make([]io.Reader, 0, len(node.body))
	for i := range node.body {
		if i > 0 {
			rs = append(rs, strings.NewReader("\n"))
		}
		// topLevelNode doesn'd increment level
		rs = append(rs, d.toReader(node.body[i], level))
	}
	return io.MultiReader(rs...)
}

func (d *dumper) newImportStatementReader(stmt *importStatement, level int) io.Reader {
	args := make([]string, 0, 2)
	pkg, err := d.toJSONString(&stmt.pkg)
	if err != nil {
		return d.err(err, stmt)
	}
	pkgPair := make([]string, 0, 2)
	pkgPair = append(pkgPair, pkg)
	if stmt.pkgAlias != "" {
		pkgPair = append(pkgPair, "'"+stmt.pkgAlias)
	}
	args = append(args, "("+strings.Join(pkgPair, " ")+")")

	if len(stmt.fnlist) > 0 {
		fnPairList := make([]string, 0, len(stmt.fnlist))
		for i := range stmt.fnlist {
			fnPairList = append(fnPairList, "("+strings.Join(stmt.fnlist[i], " ")+")")
		}
		args = append(args, "("+strings.Join(fnPairList, " ")+")")
	}

	s := fmt.Sprintf("%s(import %s)", d.getIndent(level), strings.Join(args, " "))
	return strings.NewReader(s)
}

func (d *dumper) newFuncReader(f *funcStmtOrExpr, level int) io.Reader {
	args := make([]string, 0, len(f.args))
	for i := range f.args {
		arg := f.args[i]
		if arg.typ != "" {
			args = append(args, fmt.Sprintf("(%s : %s)", arg.name, arg.typ))
		} else {
			var buf bytes.Buffer
			_, err := io.Copy(&buf, d.toReader(arg.defaultVal, level+1))
			if err != nil {
				return d.err(err, f)
			}
			args = append(args, fmt.Sprintf("(%s = %s)", arg.name, buf.String()))
		}
	}
	bodyList := make([]string, 0, len(f.body))
	for i := range f.body {
		var buf bytes.Buffer
		_, err := io.Copy(&buf, d.toReader(f.body[i], level+1))
		if err != nil {
			return d.err(err, f.body[i])
		}
		bodyList = append(bodyList, buf.String())
	}

	vs, err := unevalString(f.retType)
	if err != nil {
		return d.err(err, f)
	}
	retType, err := d.toJSONString(vs)

	// TODO Check len(bodyList) == 1 when f.bodyIsStmt == false in analyzer.
	var body string
	if len(bodyList) > 0 {
		body = bodyList[0]
	} else {
		body = "null"
	}
	if f.bodyIsStmt {
		body = "(" + strings.Join(bodyList, " ") + ")"
	}
	s := fmt.Sprintf("%s(func (%s) %s (%s) %s %s)",
		d.getIndent(level),
		strings.Join(f.mods, " "),
		f.name,
		strings.Join(args, " "),
		retType,
		body)
	return strings.NewReader(s)
}

func (d *dumper) newReturnNodeReader(node *returnStatement, level int) io.Reader {
	if node.left == nil {
		return strings.NewReader("(return)")
	}
	var value bytes.Buffer
	_, err := io.Copy(&value, d.toReader(node.left, level))
	if err != nil {
		return d.err(err, node.left)
	}
	s := fmt.Sprintf("(return %s)", value.String())
	return strings.NewReader(s)
}

func (d *dumper) newConstStatementReader(node *constStatement, level int) io.Reader {
	var left bytes.Buffer
	if list, ok := node.left.TerminalNode().(*listNode); ok { // Destructuring
		left.WriteString("(")
		for i := range list.value {
			if i > 0 {
				left.WriteString(" ")
			}
			_, err := io.Copy(&left, d.toReader(list.value[i], level))
			if err != nil {
				return d.err(err, list.value[i])
			}
		}
		left.WriteString(")")
	} else {
		_, err := io.Copy(&left, d.toReader(node.left, level))
		if err != nil {
			return d.err(err, node.left)
		}
	}
	var right bytes.Buffer
	_, err := io.Copy(&right, d.toReader(node.right, level))
	if err != nil {
		return d.err(err, node.right)
	}
	s := fmt.Sprintf("(const %s %s)", left.String(), right.String())
	return strings.NewReader(s)
}

func (d *dumper) newLetStatementReader(node *letStatement, level int) io.Reader {
	var buf bytes.Buffer
	buf.WriteString("(let ")
	if list, ok := node.left.TerminalNode().(*listNode); ok { // Destructuring
		buf.WriteString("(")
		for i := range list.value {
			if i > 0 {
				buf.WriteString(" ")
			}
			_, err := io.Copy(&buf, d.toReader(list.value[i], level))
			if err != nil {
				return d.err(err, list.value[i])
			}
		}
		buf.WriteString(")")
	} else {
		_, err := io.Copy(&buf, d.toReader(node.left, level))
		if err != nil {
			return d.err(err, node.left)
		}
	}
	if node.right != nil {
		buf.WriteString(" ")
		_, err := io.Copy(&buf, d.toReader(node.right, level))
		if err != nil {
			return d.err(err, node.right)
		}
	}
	buf.WriteString(")")
	return strings.NewReader(buf.String())
}

func (d *dumper) newIfStatementReader(node *ifStatement, level int) io.Reader {
	var cond bytes.Buffer
	_, err := io.Copy(&cond, d.toReader(node.cond, level))
	if err != nil {
		return d.err(err, node.cond)
	}
	var bodyList []string
	for i := range node.body {
		var buf bytes.Buffer
		_, err = io.Copy(&buf, d.toReader(node.body[i], level))
		if err != nil {
			return d.err(err, node.body[i])
		}
		bodyList = append(bodyList, buf.String())
	}
	var s string
	if len(node.els) == 0 {
		s = fmt.Sprintf("(if %s (%s))",
			cond.String(), strings.Join(bodyList, " "))
	} else {
		var elseList []string
		for i := range node.els {
			var buf bytes.Buffer
			_, err = io.Copy(&buf, d.toReader(node.els[i], level))
			if err != nil {
				return d.err(err, node.els[i])
			}
			elseList = append(elseList, buf.String())
		}
		s = fmt.Sprintf("(if %s (%s) (%s))",
			cond.String(), strings.Join(bodyList, " "), strings.Join(elseList, " "))
	}
	return strings.NewReader(s)
}

func (d *dumper) newWhileStatementReader(node *whileStatement, level int) io.Reader {
	var cond bytes.Buffer
	_, err := io.Copy(&cond, d.toReader(node.cond, level))
	if err != nil {
		return d.err(err, node.cond)
	}
	var bodyList []string
	for i := range node.body {
		var buf bytes.Buffer
		_, err = io.Copy(&buf, d.toReader(node.body[i], level))
		if err != nil {
			return d.err(err, node.body[i])
		}
		bodyList = append(bodyList, buf.String())
	}
	s := fmt.Sprintf("(while %s (%s))", cond.String(), strings.Join(bodyList, " "))
	return strings.NewReader(s)
}

func (d *dumper) newForStatementReader(node *forStatement, level int) io.Reader {
	var left bytes.Buffer
	if list, ok := node.left.TerminalNode().(*listNode); ok { // Destructuring
		left.WriteString("(")
		for i := range list.value {
			if i > 0 {
				left.WriteString(" ")
			}
			_, err := io.Copy(&left, d.toReader(list.value[i], level))
			if err != nil {
				return d.err(err, list.value[i])
			}
		}
		left.WriteString(")")
	} else {
		_, err := io.Copy(&left, d.toReader(node.left, level))
		if err != nil {
			return d.err(err, node.left)
		}
	}
	var right bytes.Buffer
	_, err := io.Copy(&right, d.toReader(node.right, level))
	if err != nil {
		return d.err(err, node.right)
	}
	var bodyList []string
	for i := range node.body {
		var buf bytes.Buffer
		_, err = io.Copy(&buf, d.toReader(node.body[i], level))
		if err != nil {
			return d.err(err, node.body[i])
		}
		bodyList = append(bodyList, buf.String())
	}
	s := fmt.Sprintf("(for %s %s (%s))", left.String(), right.String(), strings.Join(bodyList, " "))
	return strings.NewReader(s)
}

func (d *dumper) newTernaryNodeReader(node *ternaryNode, level int) io.Reader {
	var cond bytes.Buffer
	_, err := io.Copy(&cond, d.toReader(node.cond, level))
	if err != nil {
		return d.err(err, node.cond)
	}
	var left bytes.Buffer
	_, err = io.Copy(&left, d.toReader(node.left, level))
	if err != nil {
		return d.err(err, node.left)
	}
	var right bytes.Buffer
	_, err = io.Copy(&right, d.toReader(node.right, level))
	if err != nil {
		return d.err(err, node.right)
	}
	s := fmt.Sprintf("(?: %s %s %s)", cond.String(), left.String(), right.String())
	return strings.NewReader(s)
}

func (d *dumper) newBinaryOpNodeReader(node binaryOpNode, level int, opstr string) io.Reader {
	var left bytes.Buffer
	_, err := io.Copy(&left, d.toReader(node.Left(), level))
	if err != nil {
		return d.err(err, node.Left())
	}
	var right bytes.Buffer
	_, err = io.Copy(&right, d.toReader(node.Right(), level))
	if err != nil {
		return d.err(err, node.Right())
	}
	s := fmt.Sprintf("(%s %s %s)", opstr, left.String(), right.String())
	return strings.NewReader(s)
}

func (d *dumper) newUnaryOpNodeReader(node unaryOpNode, level int, opstr string) io.Reader {
	var value bytes.Buffer
	_, err := io.Copy(&value, d.toReader(node.Value(), level))
	if err != nil {
		return d.err(err, node.Value())
	}
	s := fmt.Sprintf("(%s %s)", opstr, value.String())
	return strings.NewReader(s)
}

func (d *dumper) newSliceNodeReader(node *sliceNode, level int) io.Reader {
	var left bytes.Buffer
	_, err := io.Copy(&left, d.toReader(node.left, level))
	if err != nil {
		return d.err(err, node.left)
	}
	from := "null"
	if node.rlist[0] != nil {
		var buf bytes.Buffer
		_, err := io.Copy(&buf, d.toReader(node.rlist[0], level))
		if err != nil {
			return d.err(err, node.rlist[0])
		}
		from = buf.String()
	}
	to := "null"
	if node.rlist[1] != nil {
		var buf bytes.Buffer
		_, err := io.Copy(&buf, d.toReader(node.rlist[1], level))
		if err != nil {
			return d.err(err, node.rlist[1])
		}
		to = buf.String()
	}
	s := fmt.Sprintf("(slice %s %s %s)", left.String(), from, to)
	return strings.NewReader(s)
}

func (d *dumper) newCallNodeReader(node *callNode, level int) io.Reader {
	var left bytes.Buffer
	_, err := io.Copy(&left, d.toReader(node.left, level))
	if err != nil {
		return d.err(err, node.left)
	}
	rlist := make([]string, 0, len(node.rlist))
	for i := range node.rlist {
		var arg bytes.Buffer
		_, err := io.Copy(&arg, d.toReader(node.rlist[i], level))
		if err != nil {
			return d.err(err, node.rlist[i])
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

func (d *dumper) newIdentifierNodeReader(node *identifierNode, level int) io.Reader {
	return strings.NewReader("'" + node.value)
}

func (d *dumper) newIntNodeReader(node *intNode, level int) io.Reader {
	return strings.NewReader(node.value)
}

func (d *dumper) newFloatNodeReader(node *floatNode, level int) io.Reader {
	return strings.NewReader(node.value)
}

func (d *dumper) newStringNodeReader(node *stringNode, level int) io.Reader {
	value, err := d.toJSONString(&node.value)
	if err != nil {
		return d.err(err, node)
	}
	return strings.NewReader(value)
}

func (d *dumper) newLiteralNodeReader(node literalNode, level int, opstr string) io.Reader {
	value, err := json.Marshal(node.Value())
	if err != nil {
		return d.err(err, node)
	}
	s := fmt.Sprintf("(%s %s)", opstr, value)
	return strings.NewReader(s)
}

func (d *dumper) newListNodeReader(node *listNode, level int) io.Reader {
	args := make([]string, 0, len(node.value)+1)
	args = append(args, "list")
	for i := range node.value {
		var arg bytes.Buffer
		_, err := io.Copy(&arg, d.toReader(node.value[i], level))
		if err != nil {
			return d.err(err, node.value[i])
		}
		args = append(args, arg.String())
	}
	s := "(" + strings.Join(args, " ") + ")"
	return strings.NewReader(s)
}

func (d *dumper) newDictionaryNodeReader(node *dictionaryNode, level int) io.Reader {
	args := make([]string, 0, len(node.value)+1)
	args = append(args, "dict")
	for i := range node.value {
		keyNode := node.value[i][0]
		var key bytes.Buffer
		_, err := io.Copy(&key, d.toReader(keyNode, level))
		if err != nil {
			return d.err(err, keyNode)
		}
		valNode := node.value[i][1]
		var val bytes.Buffer
		_, err = io.Copy(&val, d.toReader(valNode, level))
		if err != nil {
			return d.err(err, valNode)
		}
		args = append(args, fmt.Sprintf("(%s %s)", key.String(), val.String()))
	}
	s := "(" + strings.Join(args, " ") + ")"
	return strings.NewReader(s)
}

func (d *dumper) toJSONString(vs *vainString) (string, error) {
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
