package main

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/tyru/vain/node"
)

// TODO parenthesis (parser should emit parenNode?)
// TODO newline (see Position()?)
// TODO comment (parser should emit commentNode even in expression?)

func format(name string, inNodes <-chan node.Node) *formatter {
	return &formatter{name, inNodes, make(chan io.Reader), "  ", 0}
}

type formatter struct {
	name       string
	inNodes    <-chan node.Node
	outReaders chan io.Reader
	indentStr  string
	level      int
}

func (f *formatter) Run() {
	for node := range f.inNodes {
		f.emit(f.toReader(node, nil))
	}
	close(f.outReaders)
}

func (f *formatter) Readers() <-chan io.Reader {
	return f.outReaders
}

func (f *formatter) emit(r io.Reader) {
	f.outReaders <- r
}

func (f *formatter) err(err error, n node.Node) io.Reader {
	if pos := n.Position(); pos != nil {
		return &errorReader{
			fmt.Errorf("[fmt] %s:%d:%d: "+err.Error(), f.name, pos.Line(), pos.Col()+1),
		}
	}
	return &errorReader{
		fmt.Errorf("[fmt] %s: "+err.Error(), f.name),
	}
}

func (f *formatter) incIndent() {
	f.level++
}

func (f *formatter) decIndent() {
	f.level--
}

func (f *formatter) indent() string {
	return strings.Repeat(f.indentStr, f.level)
}

func (f *formatter) toReader(node, parent node.Node) io.Reader {
	// f.Printf("%s: %+v (%+v)\n", f.name, node, reflect.TypeOf(node))
	switch n := node.TerminalNode().(type) {
	case error:
		return &errorReader{n}
	case *topLevelNode:
		return f.newTopLevelNodeReader(n)
	case *importStatement:
		return f.newImportStatementReader(n, parent)
	case *funcDeclareStatement:
		return f.newFuncDeclareStatementReader(n, parent)
	case *funcStmtOrExpr:
		return f.newFuncReader(n, parent)
	case *returnStatement:
		return f.newReturnNodeReader(n, parent)
	case *constStatement:
		return f.newAssignStatementReader(n, parent, "const")
	case *letDeclareStatement:
		return f.newLetDeclareStatementReader(n, parent)
	case *letAssignStatement:
		return f.newAssignStatementReader(n, parent, "let")
	case *assignExpr:
		return f.newAssignStatementReader(n, parent, "")
	case *ifStatement:
		return f.newIfStatementReader(n, parent, true)
	case *whileStatement:
		return f.newWhileStatementReader(n, parent)
	case *forStatement:
		return f.newForStatementReader(n, parent)
	case *ternaryNode:
		return f.newTernaryNodeReader(n, parent)
	case *orNode:
		return f.newBinaryOpNodeReader(n, parent, "||")
	case *andNode:
		return f.newBinaryOpNodeReader(n, parent, "&&")
	case *equalNode:
		return f.newBinaryOpNodeReader(n, parent, "==")
	case *equalCiNode:
		return f.newBinaryOpNodeReader(n, parent, "==?")
	case *nequalNode:
		return f.newBinaryOpNodeReader(n, parent, "!=")
	case *nequalCiNode:
		return f.newBinaryOpNodeReader(n, parent, "!=?")
	case *greaterNode:
		return f.newBinaryOpNodeReader(n, parent, ">")
	case *greaterCiNode:
		return f.newBinaryOpNodeReader(n, parent, ">?")
	case *gequalNode:
		return f.newBinaryOpNodeReader(n, parent, ">=")
	case *gequalCiNode:
		return f.newBinaryOpNodeReader(n, parent, ">=?")
	case *smallerNode:
		return f.newBinaryOpNodeReader(n, parent, "<")
	case *smallerCiNode:
		return f.newBinaryOpNodeReader(n, parent, "<?")
	case *sequalNode:
		return f.newBinaryOpNodeReader(n, parent, "<=")
	case *sequalCiNode:
		return f.newBinaryOpNodeReader(n, parent, "<=?")
	case *matchNode:
		return f.newBinaryOpNodeReader(n, parent, "=~")
	case *matchCiNode:
		return f.newBinaryOpNodeReader(n, parent, "=~?")
	case *noMatchNode:
		return f.newBinaryOpNodeReader(n, parent, "!~")
	case *noMatchCiNode:
		return f.newBinaryOpNodeReader(n, parent, "!~?")
	case *isNode:
		return f.newBinaryOpNodeReader(n, parent, "is")
	case *isCiNode:
		return f.newBinaryOpNodeReader(n, parent, "is?")
	case *isNotNode:
		return f.newBinaryOpNodeReader(n, parent, "isnot")
	case *isNotCiNode:
		return f.newBinaryOpNodeReader(n, parent, "isnot?")
	case *addNode:
		return f.newBinaryOpNodeReader(n, parent, "+")
	case *subtractNode:
		return f.newBinaryOpNodeReader(n, parent, "-")
	case *multiplyNode:
		return f.newBinaryOpNodeReader(n, parent, "*")
	case *divideNode:
		return f.newBinaryOpNodeReader(n, parent, "/")
	case *remainderNode:
		return f.newBinaryOpNodeReader(n, parent, "%")
	case *notNode:
		return f.newUnaryOpNodeReader(n, parent, "!")
	case *minusNode:
		return f.newUnaryOpNodeReader(n, parent, "-")
	case *plusNode:
		return f.newUnaryOpNodeReader(n, parent, "+")
	case *sliceNode:
		return f.newSliceNodeReader(n, parent)
	case *callNode:
		return f.newCallNodeReader(n, parent)
	case *subscriptNode:
		return f.newSubscriptNodeReader(n, parent)
	case *dotNode:
		return f.newDotNodeReader(n, parent)
	case *identifierNode:
		return f.newIdentifierNodeReader(n, parent)
	case *intNode:
		return f.newIntNodeReader(n, parent)
	case *floatNode:
		return f.newFloatNodeReader(n, parent)
	case *stringNode:
		return f.newStringNodeReader(n, parent)
	case *listNode:
		return f.newListNodeReader(n, parent)
	case *dictionaryNode:
		return f.newDictionaryNodeReader(n, parent)
	case *optionNode:
		return f.newLiteralNodeReader(n, parent, "&")
	case *envNode:
		return f.newLiteralNodeReader(n, parent, "$")
	case *regNode:
		return f.newLiteralNodeReader(n, parent, "@")
	case *commentNode:
		return f.newCommentNodeReader(n, parent)
	default:
		return f.err(fmt.Errorf("unknown node: %+v", node), node)
	}
}

func (f *formatter) newTopLevelNodeReader(node *topLevelNode) io.Reader {
	var buf bytes.Buffer
	for i := range node.body {
		if i > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString(f.indent())
		_, err := io.Copy(&buf, f.toReader(node.body[i], node))
		if err != nil {
			return f.err(err, node)
		}
	}
	return strings.NewReader(buf.String())
}

func (f *formatter) newImportStatementReader(stmt *importStatement, parent node.Node) io.Reader {
	if len(stmt.fnlist) == 0 {
		return f.newImportPackageStatementReader(stmt, parent)
	}
	return f.newFromImportStatementReader(stmt, parent)
}

func (f *formatter) newImportPackageStatementReader(stmt *importStatement, parent node.Node) io.Reader {
	var buf bytes.Buffer
	buf.WriteString(f.indent())
	buf.WriteString("import ")
	buf.WriteString(string(stmt.pkg))
	if stmt.pkgAlias != "" {
		buf.WriteString(" as ")
		buf.WriteString(stmt.pkgAlias)
	}
	return strings.NewReader(buf.String())
}

func (f *formatter) newFromImportStatementReader(stmt *importStatement, parent node.Node) io.Reader {
	var buf bytes.Buffer
	buf.WriteString(f.indent())
	buf.WriteString("from ")
	buf.WriteString(string(stmt.pkg))
	buf.WriteString(" import ")
	for i := range stmt.fnlist {
		if i > 0 {
			buf.WriteString(", ")
		}
		switch len(stmt.fnlist[i]) {
		case 1:
			buf.WriteString(stmt.fnlist[i][0])
		case 2:
			buf.WriteString(stmt.fnlist[i][0])
			buf.WriteString(" as ")
			buf.WriteString(stmt.fnlist[i][1])
		default:
			return f.err(fmt.Errorf(
				"fatal: unexpected : "+
					"len(importStatement.fnlist[%d]) = %d (it must be 1 or 2)",
				i, len(stmt.fnlist[i]),
			), stmt)
		}
	}
	return strings.NewReader(buf.String())
}

func (f *formatter) newFuncDeclareStatementReader(n *funcDeclareStatement, parent node.Node) io.Reader {
	var buf bytes.Buffer
	buf.WriteString("func")
	if len(n.mods) > 0 {
		buf.WriteString(" <")
		for i := range n.mods {
			if i > 0 {
				buf.WriteString(", ")
			}
			buf.WriteString(n.mods[i])
		}
		buf.WriteString(">")
	}
	if n.name != "" {
		buf.WriteString(" ")
		buf.WriteString(n.name)
	}
	if len(n.mods) > 0 {
		buf.WriteString(" ")
	}
	buf.WriteString("(")
	for i := range n.args {
		if i > 0 {
			buf.WriteString(", ")
		}
		r := f.newArgumentReader(&n.args[i], n)
		_, err := io.Copy(&buf, r)
		if err != nil {
			return f.err(err, n)
		}
	}
	buf.WriteString(")")
	if n.retType != "" {
		buf.WriteString(": ")
		buf.WriteString(n.retType)
	}
	return strings.NewReader(buf.String())
}

func (f *formatter) newFuncReader(n *funcStmtOrExpr, parent node.Node) io.Reader {
	var buf bytes.Buffer
	declare := f.newFuncDeclareStatementReader(n.declare, parent)
	_, err := io.Copy(&buf, declare)
	if err != nil {
		return f.err(err, n)
	}
	buf.WriteString(" ")
	if !n.bodyIsStmt { // body is expression
		_, err := io.Copy(&buf, f.toReader(n.body[0], n))
		if err != nil {
			return f.err(err, n.body[0])
		}
		return strings.NewReader(buf.String())
	}
	if len(n.body) == 0 { // empty block
		buf.WriteString("{}")
		return strings.NewReader(buf.String())
	}
	buf.WriteString("{\n")
	f.incIndent()
	for i := range n.body {
		buf.WriteString(f.indent())
		_, err := io.Copy(&buf, f.toReader(n.body[i], n))
		if err != nil {
			return f.err(err, n.body[i])
		}
		buf.WriteString("\n")
	}
	f.decIndent()
	buf.WriteString(f.indent())
	buf.WriteString("}")
	if n.isExpr {
		buf.WriteString(")")
	}
	return strings.NewReader(buf.String())
}

func (f *formatter) newArgumentReader(n *argument, parent node.Node) io.Reader {
	var buf bytes.Buffer
	// TODO change argument.left to *identifierNode
	if vname, ok := n.left.TerminalNode().(*identifierNode); ok {
		r := strings.NewReader(vname.value)
		_, err := io.Copy(&buf, r)
		if err != nil {
			return f.err(err, vname)
		}
	} else {
		return f.err(fmt.Errorf(
			"fatal: unexpected node: argument.left is not *identifierNode (%+v)",
			reflect.TypeOf(n.left),
		), n.left)
	}
	buf.WriteString(": ")
	if n.defaultVal != nil {
		_, err := io.Copy(&buf, f.toReader(n.defaultVal, parent))
		if err != nil {
			return f.err(err, n.defaultVal)
		}
	} else if n.typ != "" {
		buf.WriteString(n.typ)
	} else {
		return f.err(fmt.Errorf(
			"fatal: unexpected node: both argument.typ and n.defaultVal must not be empty (%+v)",
			reflect.TypeOf(n),
		), n.left)
	}
	return strings.NewReader(buf.String())
}

func (f *formatter) newIfStatementReader(node *ifStatement, parent node.Node, top bool) io.Reader {
	var buf bytes.Buffer
	buf.WriteString("if ")
	r := f.toReader(node.cond, node)
	_, err := io.Copy(&buf, f.paren(r, node.cond))
	if err != nil {
		return f.err(err, node.cond)
	}
	buf.WriteString(" {\n")
	f.incIndent()
	for i := range node.body {
		buf.WriteString(f.indent())
		_, err = io.Copy(&buf, f.toReader(node.body[i], node))
		if err != nil {
			return f.err(err, node.body[i])
		}
		buf.WriteString("\n")
	}
	f.decIndent()
	if len(node.els) > 0 {
		if ifstmt, ok := node.els[0].(*ifStatement); ok { // else if
			buf.WriteString(f.indent())
			buf.WriteString("} else ")
			r := f.newIfStatementReader(ifstmt, node, false)
			_, err = io.Copy(&buf, r)
			if err != nil {
				return f.err(err, node.els[0])
			}
		} else { // else
			buf.WriteString(f.indent())
			buf.WriteString("} else {\n")
			f.incIndent()
			for i := range node.els {
				buf.WriteString(f.indent())
				_, err = io.Copy(&buf, f.toReader(node.els[i], node))
				if err != nil {
					return f.err(err, node.els[i])
				}
				buf.WriteString("\n")
			}
			f.decIndent()
		}
	}
	if top {
		buf.WriteString(f.indent())
		buf.WriteString("}")
	}
	return strings.NewReader(buf.String())
}

func (f *formatter) newWhileStatementReader(node *whileStatement, parent node.Node) io.Reader {
	var buf bytes.Buffer
	buf.WriteString("while ")
	_, err := io.Copy(&buf, f.toReader(node.cond, node))
	if err != nil {
		return f.err(err, node.cond)
	}
	buf.WriteString(" {\n")
	f.incIndent()
	for i := range node.body {
		buf.WriteString(f.indent())
		_, err = io.Copy(&buf, f.toReader(node.body[i], node))
		if err != nil {
			return f.err(err, node.body[i])
		}
		buf.WriteString("\n")
	}
	f.decIndent()
	buf.WriteString(f.indent())
	buf.WriteString("}")
	return strings.NewReader(buf.String())
}

func (f *formatter) newForStatementReader(node *forStatement, parent node.Node) io.Reader {
	var buf bytes.Buffer
	buf.WriteString("for ")
	_, err := io.Copy(&buf, f.toReader(node.left, parent))
	if err != nil {
		return f.err(err, node.left)
	}
	buf.WriteString(" in ")
	_, err = io.Copy(&buf, f.toReader(node.right, parent))
	if err != nil {
		return f.err(err, node.right)
	}
	buf.WriteString(" {\n")
	f.incIndent()
	for i := range node.body {
		buf.WriteString(f.indent())
		_, err = io.Copy(&buf, f.toReader(node.body[i], node))
		if err != nil {
			return f.err(err, node.body[i])
		}
		buf.WriteString("\n")
	}
	f.decIndent()
	buf.WriteString(f.indent())
	buf.WriteString("}")
	return strings.NewReader(buf.String())
}

func (f *formatter) newReturnNodeReader(n *returnStatement, parent node.Node) io.Reader {
	if n.left == nil {
		return strings.NewReader("return")
	}
	var buf bytes.Buffer
	buf.WriteString("return ")
	_, err := io.Copy(&buf, f.paren(f.toReader(n.left, parent), n.left))
	if err != nil {
		return f.err(err, n.left)
	}
	return strings.NewReader(buf.String())
}

func (f *formatter) newAssignStatementReader(node assignNode, parent node.Node, opstr string) io.Reader {
	var buf bytes.Buffer
	if opstr != "" {
		buf.WriteString(opstr)
		buf.WriteString(" ")
	}
	_, err := io.Copy(&buf, f.toReader(node.Left(), parent))
	if err != nil {
		return f.err(err, node.Left())
	}
	buf.WriteString(" = ")
	_, err = io.Copy(&buf, f.toReader(node.Right(), parent))
	if err != nil {
		return f.err(err, node.Right())
	}
	return strings.NewReader(buf.String())
}

func (f *formatter) newLetDeclareStatementReader(n *letDeclareStatement, parent node.Node) io.Reader {
	var buf bytes.Buffer
	buf.WriteString("let ")
	for i := range n.left {
		r := f.newArgumentReader(&n.left[i], n)
		_, err := io.Copy(&buf, r)
		if err != nil {
			return f.err(err, n)
		}
	}
	return strings.NewReader(buf.String())
}

func (f *formatter) newTernaryNodeReader(n *ternaryNode, parent node.Node) io.Reader {
	var buf bytes.Buffer
	cond := f.toReader(n.cond, parent)
	_, err := io.Copy(&buf, f.paren(cond, n.cond))
	if err != nil {
		return f.err(err, n.cond)
	}
	buf.WriteString(" ? ")
	left := f.toReader(n.left, parent)
	_, err = io.Copy(&buf, f.paren(left, n.left))
	if err != nil {
		return f.err(err, n.left)
	}
	buf.WriteString(" : ")
	right := f.toReader(n.right, parent)
	_, err = io.Copy(&buf, f.paren(right, n.right))
	if err != nil {
		return f.err(err, n.right)
	}
	return strings.NewReader(buf.String())
}

func (f *formatter) newBinaryOpNodeReader(node binaryOpNode, parent node.Node, opstr string) io.Reader {
	var buf bytes.Buffer
	r := f.toReader(node.Left(), parent)
	_, err := io.Copy(&buf, f.paren(r, node.Left()))
	if err != nil {
		return f.err(err, node.Left())
	}
	buf.WriteString(" ")
	buf.WriteString(opstr)
	buf.WriteString(" ")
	r = f.toReader(node.Right(), parent)
	_, err = io.Copy(&buf, f.paren(r, node.Right()))
	if err != nil {
		return f.err(err, node.Right())
	}
	return strings.NewReader(buf.String())
}

func (f *formatter) newUnaryOpNodeReader(node unaryOpNode, parent node.Node, opstr string) io.Reader {
	var buf bytes.Buffer
	buf.WriteString(opstr)
	r := f.toReader(node.Value(), parent)
	_, err := io.Copy(&buf, f.paren(r, node.Value()))
	if err != nil {
		return f.err(err, node.Value())
	}
	return strings.NewReader(buf.String())
}

func (f *formatter) newSliceNodeReader(node *sliceNode, parent node.Node) io.Reader {
	var buf bytes.Buffer
	r := f.toReader(node.left, parent)
	_, err := io.Copy(&buf, f.paren(r, node.left))
	if err != nil {
		return f.err(err, node.left)
	}
	buf.WriteString("[")
	if node.rlist[0] != nil {
		_, err := io.Copy(&buf, f.toReader(node.rlist[0], parent))
		if err != nil {
			return f.err(err, node.rlist[0])
		}
	}
	buf.WriteString(":")
	if node.rlist[1] != nil {
		_, err := io.Copy(&buf, f.toReader(node.rlist[1], parent))
		if err != nil {
			return f.err(err, node.rlist[1])
		}
	}
	buf.WriteString("]")
	return strings.NewReader(buf.String())
}

func (f *formatter) newCallNodeReader(node *callNode, parent node.Node) io.Reader {
	var buf bytes.Buffer
	r := f.toReader(node.left, parent)
	_, err := io.Copy(&buf, f.paren(r, node.left))
	if err != nil {
		return f.err(err, node.left)
	}
	buf.WriteString("(")
	for i := range node.rlist {
		if i > 0 {
			buf.WriteString(", ")
		}
		_, err := io.Copy(&buf, f.toReader(node.rlist[i], parent))
		if err != nil {
			return f.err(err, node.rlist[i])
		}
	}
	buf.WriteString(")")
	return strings.NewReader(buf.String())
}

func (f *formatter) newSubscriptNodeReader(node *subscriptNode, parent node.Node) io.Reader {
	var buf bytes.Buffer
	r := f.toReader(node.left, parent)
	_, err := io.Copy(&buf, f.paren(r, node.left))
	if err != nil {
		return f.err(err, node.left)
	}
	buf.WriteString("[")
	_, err = io.Copy(&buf, f.toReader(node.right, parent))
	if err != nil {
		return f.err(err, node.right)
	}
	buf.WriteString("]")
	return strings.NewReader(buf.String())
}

func (f *formatter) newDotNodeReader(node *dotNode, parent node.Node) io.Reader {
	var buf bytes.Buffer
	r := f.toReader(node.left, parent)
	_, err := io.Copy(&buf, f.paren(r, node.left))
	if err != nil {
		return f.err(err, node.left)
	}
	buf.WriteString(".")
	r = f.toReader(node.right, parent)
	_, err = io.Copy(&buf, f.paren(r, node.right))
	if err != nil {
		return f.err(err, node.right)
	}
	return strings.NewReader(buf.String())
}

func (f *formatter) newIdentifierNodeReader(node *identifierNode, parent node.Node) io.Reader {
	return strings.NewReader(node.value)
}

func (f *formatter) newIntNodeReader(node *intNode, parent node.Node) io.Reader {
	return strings.NewReader(node.value)
}

func (f *formatter) newFloatNodeReader(node *floatNode, parent node.Node) io.Reader {
	return strings.NewReader(node.value)
}

func (f *formatter) newStringNodeReader(node *stringNode, parent node.Node) io.Reader {
	return strings.NewReader(string(node.value))
}

func (f *formatter) newLiteralNodeReader(node literalNode, parent node.Node, opstr string) io.Reader {
	return strings.NewReader(opstr + node.Value())
}

func (f *formatter) newListNodeReader(node *listNode, parent node.Node) io.Reader {
	args := make([]string, 0, len(node.value))
	for i := range node.value {
		var arg bytes.Buffer
		_, err := io.Copy(&arg, f.toReader(node.value[i], parent))
		if err != nil {
			return f.err(err, node.value[i])
		}
		args = append(args, arg.String())
	}
	s := "[" + strings.Join(args, ",") + "]"
	return strings.NewReader(s)
}

func (f *formatter) newDictionaryNodeReader(node *dictionaryNode, parent node.Node) io.Reader {
	args := make([]string, 0, len(node.value))
	for i := range node.value {
		var key bytes.Buffer
		keyNode := node.value[i][0]
		if id, ok := keyNode.(*identifierNode); ok {
			key.WriteString(string(*unevalString(id.value)))
		} else {
			_, err := io.Copy(&key, f.toReader(keyNode, parent))
			if err != nil {
				return f.err(err, keyNode)
			}
		}
		var val bytes.Buffer
		valNode := node.value[i][1]
		_, err := io.Copy(&val, f.toReader(valNode, parent))
		if err != nil {
			return f.err(err, valNode)
		}
		args = append(args, fmt.Sprintf("%s: %s", key.String(), val.String()))
	}
	s := "{" + strings.Join(args, ", ") + "}"
	return strings.NewReader(s)
}

func (f *formatter) newCommentNodeReader(node *commentNode, parent node.Node) io.Reader {
	return strings.NewReader(node.value)
}

// paren returns string wrapped by "(", ")" if needsParen(node) == true.
func (f *formatter) paren(r io.Reader, node node.Node) io.Reader {
	if f.needsParen(node) {
		var buf bytes.Buffer
		_, err := io.Copy(&buf, r)
		if err != nil {
			return f.err(err, node)
		}
		return strings.NewReader("(" + buf.String() + ")")
	}
	return r
}

// needsParen returns true if node should be wrapped by parentheses.
func (f *formatter) needsParen(node node.Node) bool {
	switch node.TerminalNode().(type) {
	case *topLevelNode:
		return false
	case *importStatement:
		return false
	case *funcStmtOrExpr:
		return false
	case *returnStatement:
		return false
	case *ternaryNode:
		return true
	case *orNode:
		return true
	case *andNode:
		return true
	case *equalNode:
		return true
	case *equalCiNode:
		return true
	case *nequalNode:
		return true
	case *nequalCiNode:
		return true
	case *greaterNode:
		return true
	case *greaterCiNode:
		return true
	case *gequalNode:
		return true
	case *gequalCiNode:
		return true
	case *smallerNode:
		return true
	case *smallerCiNode:
		return true
	case *sequalNode:
		return true
	case *sequalCiNode:
		return true
	case *matchNode:
		return true
	case *matchCiNode:
		return true
	case *noMatchNode:
		return true
	case *noMatchCiNode:
		return true
	case *isNode:
		return true
	case *isCiNode:
		return true
	case *isNotNode:
		return true
	case *isNotCiNode:
		return true
	case *addNode:
		return true
	case *subtractNode:
		return true
	case *multiplyNode:
		return true
	case *divideNode:
		return true
	case *remainderNode:
		return true
	case *notNode:
		return false
	case *minusNode:
		return false
	case *plusNode:
		return false
	case *sliceNode:
		return false
	case *callNode:
		return false
	case *subscriptNode:
		return false
	case *dotNode:
		return false
	case *identifierNode:
		return false
	case *intNode:
		return false
	case *floatNode:
		return false
	case *stringNode:
		return false
	case *listNode:
		return false
	case *dictionaryNode:
		return false
	case *optionNode:
		return false
	case *envNode:
		return false
	case *regNode:
		return false
	case *commentNode:
		return false
	}
	return true
}
