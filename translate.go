package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/tyru/vain/node"
)

func translate(name string, inNodes <-chan node.Node) *translator {
	return &translator{name, inNodes, make(chan io.Reader), "  ", 0, make([]io.Reader, 0, 16), 0}
}

type translator struct {
	name           string
	inNodes        <-chan node.Node
	outReaders     chan io.Reader
	indentStr      string
	level          int
	namedExprFuncs []io.Reader
	lambdaFuncID   int
}

func (t *translator) Run() {
	for node := range t.inNodes {
		toplevel := t.toReader(node, nil)
		t.emit(strings.NewReader("scriptencoding utf-8\n"))
		if len(t.namedExprFuncs) > 0 {
			t.emit(strings.NewReader("\" vain: begin named expression functions\n"))
			for i := range t.namedExprFuncs {
				if i > 0 {
					t.emit(strings.NewReader("\n"))
				}
				t.emit(t.namedExprFuncs[i])
			}
			t.namedExprFuncs = t.namedExprFuncs[:0]
			t.emit(strings.NewReader("\n\" vain: end named expression functions\n\n"))
		}
		t.emit(toplevel)
	}
	close(t.outReaders)
}

func (t *translator) Readers() <-chan io.Reader {
	return t.outReaders
}

func (t *translator) emit(r io.Reader) {
	t.outReaders <- r
}

func (t *translator) err(err error, n node.Node) io.Reader {
	if pos := n.Position(); pos != nil {
		return &errorReader{
			fmt.Errorf("[translate] %s:%d:%d: "+err.Error(), t.name, pos.Line(), pos.Col()+1),
		}
	}
	return &errorReader{
		fmt.Errorf("[translate] %s: "+err.Error(), t.name),
	}
}

func (t *translator) incIndent() {
	t.level++
}

func (t *translator) decIndent() {
	t.level--
}

func (t *translator) indent() string {
	return strings.Repeat(t.indentStr, t.level)
}

func (t *translator) toReader(node, parent node.Node) io.Reader {
	// fmt.Printf("%s: %+v (%+v)\n", t.name, node, reflect.TypeOf(node))
	switch n := node.TerminalNode().(type) {
	case error:
		return &errorReader{n}
	case *topLevelNode:
		return t.newTopLevelNodeReader(n)
	case *importStatement:
		return t.newImportStatementReader(n, parent)
	case *funcDeclareStatement:
		return t.newFuncDeclareStatementReader(n, parent)
	case *funcStmtOrExpr:
		return t.newFuncReader(n, parent)
	case *returnStatement:
		return t.newReturnNodeReader(n, parent)
	case *constStatement:
		return t.newAssignStatementReader(n, parent)
	case *letDeclareStatement:
		return t.newLetDeclareStatementReader(n, parent)
	case *letAssignStatement:
		return t.newAssignStatementReader(n, parent)
	case *assignExpr:
		return t.newAssignStatementReader(n, parent)
	case *ifStatement:
		return t.newIfStatementReader(n, parent, true)
	case *whileStatement:
		return t.newWhileStatementReader(n, parent)
	case *forStatement:
		return t.newForStatementReader(n, parent)
	case *ternaryNode:
		return t.newTernaryNodeReader(n, parent)
	case *orNode:
		return t.newBinaryOpNodeReader(n, parent, "||")
	case *andNode:
		return t.newBinaryOpNodeReader(n, parent, "&&")
	case *equalNode:
		return t.newBinaryOpNodeReader(n, parent, "==#")
	case *equalCiNode:
		return t.newBinaryOpNodeReader(n, parent, "==?")
	case *nequalNode:
		return t.newBinaryOpNodeReader(n, parent, "!=#")
	case *nequalCiNode:
		return t.newBinaryOpNodeReader(n, parent, "!=?")
	case *greaterNode:
		return t.newBinaryOpNodeReader(n, parent, ">#")
	case *greaterCiNode:
		return t.newBinaryOpNodeReader(n, parent, ">?")
	case *gequalNode:
		return t.newBinaryOpNodeReader(n, parent, ">=#")
	case *gequalCiNode:
		return t.newBinaryOpNodeReader(n, parent, ">=?")
	case *smallerNode:
		return t.newBinaryOpNodeReader(n, parent, "<#")
	case *smallerCiNode:
		return t.newBinaryOpNodeReader(n, parent, "<?")
	case *sequalNode:
		return t.newBinaryOpNodeReader(n, parent, "<=#")
	case *sequalCiNode:
		return t.newBinaryOpNodeReader(n, parent, "<=?")
	case *matchNode:
		return t.newBinaryOpNodeReader(n, parent, "=~#")
	case *matchCiNode:
		return t.newBinaryOpNodeReader(n, parent, "=~?")
	case *noMatchNode:
		return t.newBinaryOpNodeReader(n, parent, "!~#")
	case *noMatchCiNode:
		return t.newBinaryOpNodeReader(n, parent, "!~?")
	case *isNode:
		return t.newBinaryOpNodeReader(n, parent, "is#")
	case *isCiNode:
		return t.newBinaryOpNodeReader(n, parent, "is?")
	case *isNotNode:
		return t.newBinaryOpNodeReader(n, parent, "isnot#")
	case *isNotCiNode:
		return t.newBinaryOpNodeReader(n, parent, "isnot?")
	case *addNode:
		return t.newBinaryOpNodeReader(n, parent, "+")
	case *subtractNode:
		return t.newBinaryOpNodeReader(n, parent, "-")
	case *multiplyNode:
		return t.newBinaryOpNodeReader(n, parent, "*")
	case *divideNode:
		return t.newBinaryOpNodeReader(n, parent, "/")
	case *remainderNode:
		return t.newBinaryOpNodeReader(n, parent, "%")
	case *notNode:
		return t.newUnaryOpNodeReader(n, parent, "!")
	case *minusNode:
		return t.newUnaryOpNodeReader(n, parent, "-")
	case *plusNode:
		return t.newUnaryOpNodeReader(n, parent, "+")
	case *sliceNode:
		return t.newSliceNodeReader(n, parent)
	case *callNode:
		return t.newCallNodeReader(n, parent)
	case *subscriptNode:
		return t.newSubscriptNodeReader(n, parent)
	case *dotNode:
		return t.newDotNodeReader(n, parent)
	case *identifierNode:
		return t.newIdentifierNodeReader(n, parent)
	case *intNode:
		return t.newIntNodeReader(n, parent)
	case *floatNode:
		return t.newFloatNodeReader(n, parent)
	case *stringNode:
		return t.newStringNodeReader(n, parent)
	case *listNode:
		return t.newListNodeReader(n, parent)
	case *dictionaryNode:
		return t.newDictionaryNodeReader(n, parent)
	case *optionNode:
		return t.newLiteralNodeReader(n, parent, "&")
	case *envNode:
		return t.newLiteralNodeReader(n, parent, "$")
	case *regNode:
		return t.newLiteralNodeReader(n, parent, "@")
	case *commentNode:
		return t.newCommentNodeReader(n, parent)
	default:
		return t.err(fmt.Errorf("unknown node: %+v", node), node)
	}
}

func (t *translator) newTopLevelNodeReader(node *topLevelNode) io.Reader {
	var buf bytes.Buffer
	for i := range node.body {
		if i > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString(t.indent())
		_, err := io.Copy(&buf, t.toExcmd(node.body[i], node))
		if err != nil {
			return t.err(err, node)
		}
	}
	return strings.NewReader(buf.String())
}

func (t *translator) newImportStatementReader(stmt *importStatement, parent node.Node) io.Reader {
	// TODO
	return emptyReader
}

func (t *translator) newFuncDeclareStatementReader(f *funcDeclareStatement, parent node.Node) io.Reader {
	// TODO
	return emptyReader
}

func (t *translator) newFuncReader(f *funcStmtOrExpr, parent node.Node) io.Reader {
	if !f.IsExpr() {
		// Function statement is required.
		if f.declare.name != "" {
			return t.newFuncStmtReader(f, "")
		}
		if len(f.body) == 0 {
			autoload, global, _ := t.convertModifiers(f.declare.mods)
			name := t.getFuncName(f, autoload, global)
			if name == "" {
				name = t.generateFuncName()
			}
			t.namedExprFuncs = append(t.namedExprFuncs, t.newFuncStmtReader(f, name))
			return strings.NewReader(fmt.Sprintf("function('%s')", name))
		}
		return t.newLambdaReader(f, parent)
	}
	// Function expression is required.
	if f.declare.name != "" || len(f.body) == 0 {
		autoload, global, _ := t.convertModifiers(f.declare.mods)
		name := t.getFuncName(f, autoload, global)
		if name == "" {
			name = t.generateFuncName()
		}
		t.namedExprFuncs = append(t.namedExprFuncs, t.newFuncStmtReader(f, name))
		return strings.NewReader(fmt.Sprintf("function('%s')", name))
	}
	return t.newLambdaReader(f, parent)
}

func (t *translator) isVoidExprFunc(f *funcStmtOrExpr, parent node.Node) bool {
	if !f.IsExpr() {
		// Function statement is required.
		if f.declare.name != "" {
			return false
		}
		return true
	}
	// Function expression is required.
	switch parent.(type) {
	case *topLevelNode:
		return true
	case *funcStmtOrExpr:
		return true
	}
	return false
}

func (t *translator) getFuncName(f *funcStmtOrExpr, autoload, global bool) string {
	if f.declare.name == "" {
		return ""
	}
	if autoload {
		return f.declare.name
	} else if global {
		// TODO Check if function name starts with uppercase letter in analyzer.
		return f.declare.name
	}
	return "s:" + f.declare.name
}

func (t *translator) generateFuncName() string {
	// TODO check conflict
	t.lambdaFuncID++
	return fmt.Sprintf("s:_vain_dummy_lambda%d", t.lambdaFuncID)
}

func (t *translator) newFuncStmtReader(f *funcStmtOrExpr, name string) io.Reader {
	autoload, global, vimmods := t.convertModifiers(f.declare.mods)
	if name == "" {
		name = t.getFuncName(f, autoload, global)
	}
	var buf bytes.Buffer
	buf.WriteString("function! ")
	buf.WriteString(name)
	buf.WriteString("(")
	for i := range f.declare.args {
		if i > 0 {
			buf.WriteString(",")
		}
		_, err := io.Copy(&buf, t.toExcmd(f.declare.args[i].left, f))
		if err != nil {
			return t.err(err, f.declare.args[i].left)
		}
	}
	buf.WriteString(")")
	if len(vimmods) > 0 {
		buf.WriteString(" ")
		buf.WriteString(strings.Join(vimmods, " "))
	}
	buf.WriteString("\n")
	t.incIndent()
	for i := range f.body {
		buf.WriteString(t.indent())
		_, err := io.Copy(&buf, t.toExcmd(f.body[i], f))
		if err != nil {
			return t.err(err, f.body[i])
		}
		buf.WriteString("\n")
	}
	t.decIndent()
	buf.WriteString(t.indent())
	buf.WriteString("endfunction")
	return strings.NewReader(buf.String())
}

func (t *translator) newLambdaReader(f *funcStmtOrExpr, parent node.Node) io.Reader {
	var buf bytes.Buffer
	buf.WriteString("{")
	for i := range f.declare.args {
		if i > 0 {
			buf.WriteString(",")
		}
		_, err := io.Copy(&buf, t.toExcmd(f.declare.args[i].left, f))
		if err != nil {
			return t.err(err, f.declare.args[i].left)
		}
	}
	buf.WriteString("->")
	_, err := io.Copy(&buf, t.toReader(f.body[0], parent))
	if err != nil {
		return t.err(err, f.body[0])
	}
	buf.WriteString("}")
	return strings.NewReader(buf.String())
}

func (t *translator) convertModifiers(mods []string) (autoload, global bool, newmods []string) {
	newmods = make([]string, 0, len(mods)+1)
	abort := true
	for i := range mods {
		switch mods[i] {
		case "noabort":
			abort = false
		case "autoload":
		case "global":
		case "range":
			newmods = append(newmods, mods[i])
		case "dict":
			newmods = append(newmods, mods[i])
		case "closure":
			newmods = append(newmods, mods[i])
		}
	}
	if abort {
		newmods = append(newmods, "abort")
	}
	return
}

func (t *translator) newIfStatementReader(node *ifStatement, parent node.Node, top bool) io.Reader {
	var cond bytes.Buffer
	_, err := io.Copy(&cond, t.toReader(node.cond, node))
	if err != nil {
		return t.err(err, node.cond)
	}
	var bodyList []string
	for i := range node.body {
		var buf bytes.Buffer
		_, err = io.Copy(&buf, t.toReader(node.body[i], node))
		if err != nil {
			return t.err(err, node.body[i])
		}
		bodyList = append(bodyList, buf.String())
	}
	var buf bytes.Buffer
	buf.WriteString("if ")
	buf.WriteString(cond.String())
	buf.WriteString("\n")
	t.incIndent()
	for i := range bodyList {
		buf.WriteString(t.indent())
		buf.WriteString(bodyList[i])
		buf.WriteString("\n")
	}
	t.decIndent()
	if len(node.els) > 0 {
		if ifstmt, ok := node.els[0].(*ifStatement); ok { // else if
			buf.WriteString(t.indent())
			buf.WriteString("else")
			r := t.newIfStatementReader(ifstmt, node, false)
			_, err = io.Copy(&buf, r)
			if err != nil {
				return t.err(err, node.els[0])
			}
		} else { // else
			buf.WriteString(t.indent())
			buf.WriteString("else\n")
			t.incIndent()
			for i := range node.els {
				buf.WriteString(t.indent())
				_, err = io.Copy(&buf, t.toReader(node.els[i], node))
				if err != nil {
					return t.err(err, node.els[i])
				}
				buf.WriteString("\n")
			}
			t.decIndent()
		}
	}
	if top {
		buf.WriteString(t.indent())
		buf.WriteString("endif")
	}
	return strings.NewReader(buf.String())
}

func (t *translator) newWhileStatementReader(node *whileStatement, parent node.Node) io.Reader {
	var buf bytes.Buffer
	buf.WriteString("while ")
	_, err := io.Copy(&buf, t.toReader(node.cond, node))
	if err != nil {
		return t.err(err, node.cond)
	}
	buf.WriteString("\n")
	t.incIndent()
	for i := range node.body {
		buf.WriteString(t.indent())
		_, err = io.Copy(&buf, t.toReader(node.body[i], node))
		if err != nil {
			return t.err(err, node.body[i])
		}
		buf.WriteString("\n")
	}
	t.decIndent()
	buf.WriteString(t.indent())
	buf.WriteString("endwhile")
	return strings.NewReader(buf.String())
}

func (t *translator) newForStatementReader(node *forStatement, parent node.Node) io.Reader {
	var buf bytes.Buffer
	buf.WriteString("for ")
	_, err := io.Copy(&buf, t.toReader(node.left, parent))
	if err != nil {
		return t.err(err, node.left)
	}
	buf.WriteString(" in ")
	_, err = io.Copy(&buf, t.toReader(node.right, parent))
	if err != nil {
		return t.err(err, node.right)
	}
	buf.WriteString("\n")
	t.incIndent()
	for i := range node.body {
		buf.WriteString(t.indent())
		_, err = io.Copy(&buf, t.toReader(node.body[i], node))
		if err != nil {
			return t.err(err, node.body[i])
		}
		buf.WriteString("\n")
	}
	t.decIndent()
	buf.WriteString(t.indent())
	buf.WriteString("endfor")
	return strings.NewReader(buf.String())
}

func (t *translator) newReturnNodeReader(node *returnStatement, parent node.Node) io.Reader {
	if node.left == nil {
		return strings.NewReader("return")
	}
	var value bytes.Buffer
	_, err := io.Copy(&value, t.toReader(node.left, parent))
	if err != nil {
		return t.err(err, node.left)
	}
	s := fmt.Sprintf("return %s", t.paren(value.String(), node.left))
	return strings.NewReader(s)
}

func (t *translator) newAssignStatementReader(node assignNode, parent node.Node) io.Reader {
	var buf bytes.Buffer
	buf.WriteString("let ")
	_, err := io.Copy(&buf, t.toReader(node.Left(), parent))
	if err != nil {
		return t.err(err, node.Left())
	}
	buf.WriteString(" = ")
	_, err = io.Copy(&buf, t.toReader(node.Right(), parent))
	if err != nil {
		return t.err(err, node.Right())
	}
	return strings.NewReader(buf.String())
}

func (t *translator) newLetDeclareStatementReader(node *letDeclareStatement, parent node.Node) io.Reader {
	return emptyReader // TODO
}

func (t *translator) newTernaryNodeReader(node *ternaryNode, parent node.Node) io.Reader {
	var cond bytes.Buffer
	_, err := io.Copy(&cond, t.toReader(node.cond, parent))
	if err != nil {
		return t.err(err, node.cond)
	}
	var left bytes.Buffer
	_, err = io.Copy(&left, t.toReader(node.left, parent))
	if err != nil {
		return t.err(err, node.left)
	}
	var right bytes.Buffer
	_, err = io.Copy(&right, t.toReader(node.right, parent))
	if err != nil {
		return t.err(err, node.right)
	}
	s := fmt.Sprintf("%s ? %s : %s",
		t.paren(cond.String(), node.cond),
		t.paren(left.String(), node.left),
		t.paren(right.String(), node.right))
	return strings.NewReader(s)
}

func (t *translator) newBinaryOpNodeReader(node binaryOpNode, parent node.Node, opstr string) io.Reader {
	var left bytes.Buffer
	_, err := io.Copy(&left, t.toReader(node.Left(), parent))
	if err != nil {
		return t.err(err, node.Left())
	}
	var right bytes.Buffer
	_, err = io.Copy(&right, t.toReader(node.Right(), parent))
	if err != nil {
		return t.err(err, node.Right())
	}
	s := fmt.Sprintf("%s %s %s",
		t.paren(left.String(), node.Left()),
		opstr,
		t.paren(right.String(), node.Right()))
	return strings.NewReader(s)
}

func (t *translator) newUnaryOpNodeReader(node unaryOpNode, parent node.Node, opstr string) io.Reader {
	var value bytes.Buffer
	_, err := io.Copy(&value, t.toReader(node.Value(), parent))
	if err != nil {
		return t.err(err, node.Value())
	}
	s := fmt.Sprintf("%s%s", opstr, t.paren(value.String(), node.Value()))
	return strings.NewReader(s)
}

func (t *translator) newSliceNodeReader(node *sliceNode, parent node.Node) io.Reader {
	var left bytes.Buffer
	_, err := io.Copy(&left, t.toReader(node.left, parent))
	if err != nil {
		return t.err(err, node.left)
	}
	from := "null"
	if node.rlist[0] != nil {
		var buf bytes.Buffer
		_, err := io.Copy(&buf, t.toReader(node.rlist[0], parent))
		if err != nil {
			return t.err(err, node.rlist[0])
		}
		from = buf.String()
	}
	to := "null"
	if node.rlist[1] != nil {
		var buf bytes.Buffer
		_, err := io.Copy(&buf, t.toReader(node.rlist[1], parent))
		if err != nil {
			return t.err(err, node.rlist[1])
		}
		to = buf.String()
	}
	if len(from) == 1 && strings.ContainsAny(from, "gwtslav") {
		from = from + " "
	}
	s := fmt.Sprintf("%s[%s:%s]", t.paren(left.String(), node.left), from, to)
	return strings.NewReader(s)
}

func (t *translator) newCallNodeReader(node *callNode, parent node.Node) io.Reader {
	var left bytes.Buffer
	_, err := io.Copy(&left, t.toReader(node.left, parent))
	if err != nil {
		return t.err(err, node.left)
	}
	rlist := make([]string, 0, len(node.rlist))
	for i := range node.rlist {
		var arg bytes.Buffer
		_, err := io.Copy(&arg, t.toReader(node.rlist[i], parent))
		if err != nil {
			return t.err(err, node.rlist[i])
		}
		rlist = append(rlist, arg.String())
	}
	s := fmt.Sprintf("%s(%s)", t.paren(left.String(), node.left), strings.Join(rlist, ","))
	return strings.NewReader(s)
}

func (t *translator) newSubscriptNodeReader(node *subscriptNode, parent node.Node) io.Reader {
	var left bytes.Buffer
	_, err := io.Copy(&left, t.toReader(node.left, parent))
	if err != nil {
		return t.err(err, node.left)
	}
	var right bytes.Buffer
	_, err = io.Copy(&right, t.toReader(node.right, parent))
	if err != nil {
		return t.err(err, node.right)
	}
	s := fmt.Sprintf("%s[%s]", t.paren(left.String(), node.left), right.String())
	return strings.NewReader(s)
}

func (t *translator) newDotNodeReader(node *dotNode, parent node.Node) io.Reader {
	var left bytes.Buffer
	_, err := io.Copy(&left, t.toReader(node.left, parent))
	if err != nil {
		return t.err(err, node.left)
	}
	var right bytes.Buffer
	_, err = io.Copy(&right, t.toReader(node.right, parent))
	if err != nil {
		return t.err(err, node.right)
	}
	s := fmt.Sprintf("%s.%s",
		t.paren(left.String(), node.left),
		t.paren(right.String(), node.right))
	return strings.NewReader(s)
}

func (t *translator) newIdentifierNodeReader(node *identifierNode, parent node.Node) io.Reader {
	return strings.NewReader(node.value)
}

func (t *translator) newIntNodeReader(node *intNode, parent node.Node) io.Reader {
	return strings.NewReader(node.value)
}

func (t *translator) newFloatNodeReader(node *floatNode, parent node.Node) io.Reader {
	return strings.NewReader(node.value)
}

func (t *translator) newStringNodeReader(node *stringNode, parent node.Node) io.Reader {
	return strings.NewReader(string(node.value))
}

func (t *translator) newLiteralNodeReader(node literalNode, parent node.Node, opstr string) io.Reader {
	return strings.NewReader(opstr + node.Value())
}

func (t *translator) newListNodeReader(node *listNode, parent node.Node) io.Reader {
	args := make([]string, 0, len(node.value))
	for i := range node.value {
		var arg bytes.Buffer
		_, err := io.Copy(&arg, t.toReader(node.value[i], parent))
		if err != nil {
			return t.err(err, node.value[i])
		}
		args = append(args, arg.String())
	}
	s := "[" + strings.Join(args, ",") + "]"
	return strings.NewReader(s)
}

func (t *translator) newDictionaryNodeReader(node *dictionaryNode, parent node.Node) io.Reader {
	args := make([]string, 0, len(node.value))
	for i := range node.value {
		var key bytes.Buffer
		keyNode := node.value[i][0]
		if id, ok := keyNode.(*identifierNode); ok {
			key.WriteString(string(*unevalString(id.value)))
		} else {
			_, err := io.Copy(&key, t.toReader(keyNode, parent))
			if err != nil {
				return t.err(err, keyNode)
			}
		}
		var val bytes.Buffer
		valNode := node.value[i][1]
		_, err := io.Copy(&val, t.toReader(valNode, parent))
		if err != nil {
			return t.err(err, valNode)
		}
		args = append(args, fmt.Sprintf("%s:%s", key.String(), val.String()))
	}
	s := "{" + strings.Join(args, ",") + "}"
	return strings.NewReader(s)
}

func (t *translator) newCommentNodeReader(node *commentNode, parent node.Node) io.Reader {
	// return strings.NewReader("\"" + node.value[1:])
	return emptyReader
}

func (t *translator) toJSONString(vs *vainString) (string, error) {
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

// paren returns string wrapped by "(", ")" if needsParen(node) == true.
func (t *translator) paren(s string, node node.Node) string {
	if t.needsParen(node) {
		return "(" + s + ")"
	}
	return s
}

// needsParen returns true if node should be wrapped by parentheses.
func (t *translator) needsParen(node node.Node) bool {
	switch node.(type) {
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

func (t *translator) toExcmd(n, parent node.Node) io.Reader {
	rs := make([]io.Reader, 0, 2)
	_, isCall := n.(*callNode)
	if !isCall && t.isVoidExpr(n, parent) {
		// TODO Comment out each line. it may be safe because
		// currently an expression is output as one line...
		rs = append(rs, strings.NewReader("\" "))
	} else if isCall {
		rs = append(rs, strings.NewReader("call "))
	}
	rs = append(rs, t.toReader(n, parent))
	return io.MultiReader(rs...)
}

func (t *translator) isVoidExpr(n, parent node.Node) bool {
	// TODO Do this in analyzer.
	return false
	// switch parent.(type) {
	// case *topLevelNode:
	// case *funcStmtOrExpr:
	// default:
	// 	return false
	// }
	// if f, ok := n.(*funcStmtOrExpr); ok {
	// 	return t.isVoidExprFunc(f, parent)
	// } else if !n.IsExpr() {
	// 	return false
	// } else if _, ok := n.TerminalNode().(*callNode); ok {
	// 	return false
	// }
	// return true
}
