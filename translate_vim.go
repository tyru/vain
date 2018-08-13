package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func translateVim(a *analyzer) translator {
	return &vimTranslator{a.name, a.nodes, make(chan io.Reader), "  ", make([]io.Reader, 0, 16)}
}

type vimTranslator struct {
	name           string
	nodes          <-chan node
	readers        chan io.Reader
	indent         string
	namedExprFuncs []io.Reader
}

func (t *vimTranslator) Run() {
	for node := range t.nodes {
		toplevel := t.toReader(node, node, 0)
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
	close(t.readers)
}

func (t *vimTranslator) Readers() <-chan io.Reader {
	return t.readers
}

func (t *vimTranslator) emit(r io.Reader) {
	t.readers <- r
}

func (t *vimTranslator) errParse(node *errorNode) io.Reader {
	return &errorReader{node.err}
}

func (t *vimTranslator) err(err error, node node) io.Reader {
	return &errorReader{fmt.Errorf("[translate/vim] %s:%d: "+err.Error(), t.name, node.LineNum())}
}

func (t *vimTranslator) getIndent(level int) string {
	return strings.Repeat(t.indent, level)
}

func (t *vimTranslator) toReader(node, parent node, level int) io.Reader {
	// fmt.Printf("%s: %+v (%+v)\n", t.name, node, reflect.TypeOf(node))
	switch n := node.(type) {
	case *errorNode:
		return t.errParse(n)
	case *topLevelNode:
		return t.newTopLevelNodeReader(n, level)
	case *importStatement:
		return t.newImportStatementReader(n, parent, level)
	case *funcStmtOrExpr:
		return t.newFuncReader(n, parent, level)
	case *ternaryNode:
		return t.newTernaryNodeReader(n, parent, level)
	case *orNode:
		return t.newBinaryOpNodeReader(n, parent, level, "||")
	case *andNode:
		return t.newBinaryOpNodeReader(n, parent, level, "&&")
	case *equalNode:
		return t.newBinaryOpNodeReader(n, parent, level, "==")
	case *equalCiNode:
		return t.newBinaryOpNodeReader(n, parent, level, "==?")
	case *nequalNode:
		return t.newBinaryOpNodeReader(n, parent, level, "!=")
	case *nequalCiNode:
		return t.newBinaryOpNodeReader(n, parent, level, "!=?")
	case *greaterNode:
		return t.newBinaryOpNodeReader(n, parent, level, ">")
	case *greaterCiNode:
		return t.newBinaryOpNodeReader(n, parent, level, ">?")
	case *gequalNode:
		return t.newBinaryOpNodeReader(n, parent, level, ">=")
	case *gequalCiNode:
		return t.newBinaryOpNodeReader(n, parent, level, ">=?")
	case *smallerNode:
		return t.newBinaryOpNodeReader(n, parent, level, "<")
	case *smallerCiNode:
		return t.newBinaryOpNodeReader(n, parent, level, "<?")
	case *sequalNode:
		return t.newBinaryOpNodeReader(n, parent, level, "<=")
	case *sequalCiNode:
		return t.newBinaryOpNodeReader(n, parent, level, "<=?")
	case *matchNode:
		return t.newBinaryOpNodeReader(n, parent, level, "=~")
	case *matchCiNode:
		return t.newBinaryOpNodeReader(n, parent, level, "=~?")
	case *noMatchNode:
		return t.newBinaryOpNodeReader(n, parent, level, "!~")
	case *noMatchCiNode:
		return t.newBinaryOpNodeReader(n, parent, level, "!~?")
	case *isNode:
		return t.newBinaryOpNodeReader(n, parent, level, "is")
	case *isCiNode:
		return t.newBinaryOpNodeReader(n, parent, level, "is?")
	case *isNotNode:
		return t.newBinaryOpNodeReader(n, parent, level, "isnot")
	case *isNotCiNode:
		return t.newBinaryOpNodeReader(n, parent, level, "isnot?")
	case *addNode:
		return t.newBinaryOpNodeReader(n, parent, level, "+")
	case *subtractNode:
		return t.newBinaryOpNodeReader(n, parent, level, "-")
	case *multiplyNode:
		return t.newBinaryOpNodeReader(n, parent, level, "*")
	case *divideNode:
		return t.newBinaryOpNodeReader(n, parent, level, "/")
	case *remainderNode:
		return t.newBinaryOpNodeReader(n, parent, level, "%")
	case *notNode:
		return t.newUnaryOpNodeReader(n, parent, level, "!")
	case *minusNode:
		return t.newUnaryOpNodeReader(n, parent, level, "-")
	case *plusNode:
		return t.newUnaryOpNodeReader(n, parent, level, "+")
	case *sliceNode:
		return t.newSliceNodeReader(n, parent, level)
	case *callNode:
		return t.newCallNodeReader(n, parent, level)
	case *subscriptNode:
		return t.newSubscriptNodeReader(n, parent, level)
	case *dotNode:
		return t.newDotNodeReader(n, parent, level)
	case *identifierNode:
		return t.newIdentifierNodeReader(n, parent, level)
	case *intNode:
		return t.newIntNodeReader(n, parent, level)
	case *floatNode:
		return t.newFloatNodeReader(n, parent, level)
	case *stringNode:
		return t.newStringNodeReader(n, parent, level)
	case *listNode:
		return t.newListNodeReader(n, parent, level)
	case *dictionaryNode:
		return t.newDictionaryNodeReader(n, parent, level)
	case *optionNode:
		return t.newLiteralNodeReader(n, parent, level, "&")
	case *envNode:
		return t.newLiteralNodeReader(n, parent, level, "$")
	case *regNode:
		return t.newLiteralNodeReader(n, parent, level, "@")
	default:
		return t.err(fmt.Errorf("unknown node: %+v", node), node)
	}
}

func (t *vimTranslator) newTopLevelNodeReader(node *topLevelNode, level int) io.Reader {
	rs := make([]io.Reader, 0, len(node.body))
	for i := range node.body {
		if i > 0 {
			rs = append(rs, strings.NewReader("\n"))
		}
		r := t.toExcmd(node.body[i], node, level)
		rs = append(rs, r)
	}
	return io.MultiReader(rs...)
}

func (t *vimTranslator) newImportStatementReader(stmt *importStatement, parent node, level int) io.Reader {
	// TODO
	return strings.NewReader("")
}

func (t *vimTranslator) newFuncReader(f *funcStmtOrExpr, parent node, level int) io.Reader {
	if !f.IsExpr() {
		// Function statement is required.
		if f.name != "" {
			return t.newFuncStmtReader(f, level)
		}
		// TODO Check len(f.body) == 1 here in analyzer.
		if len(f.body) == 0 {
			return emptyReader
		}
		return t.newLambdaReader(f, parent, level)
	}
	// Function expression is required.
	if f.name != "" {
		if !t.isVoidExprFunc(f, parent) {
			t.namedExprFuncs = append(t.namedExprFuncs, t.newFuncStmtReader(f, level))
		}
		autoload, global, _ := t.convertModifiers(f.mods)
		name := t.getFuncName(f, autoload, global)
		return strings.NewReader(fmt.Sprintf("function('%s')", name))
	}
	// TODO Check len(f.body) == 1 here in analyzer.
	if len(f.body) == 0 {
		return emptyReader
	}
	return t.newLambdaReader(f, parent, level)
}

func (t *vimTranslator) isVoidExprFunc(f *funcStmtOrExpr, parent node) bool {
	if !f.IsExpr() {
		// Function statement is required.
		if f.name != "" {
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

func (t *vimTranslator) getFuncName(f *funcStmtOrExpr, autoload, global bool) string {
	if autoload {
		return f.name
	} else if global {
		// TODO Check if function name starts with uppercase letter in analyzer.
		return f.name
	}
	return "s:" + f.name
}

func (t *vimTranslator) newFuncStmtReader(f *funcStmtOrExpr, level int) io.Reader {
	autoload, global, vimmods := t.convertModifiers(f.mods)
	name := t.getFuncName(f, autoload, global)
	var buf bytes.Buffer
	buf.WriteString("function! ")
	buf.WriteString(name)
	buf.WriteString("(")
	for i := range f.args {
		if i > 0 {
			buf.WriteString(",")
		}
		buf.WriteString(f.args[i].name)
	}
	buf.WriteString(")")
	if len(vimmods) > 0 {
		buf.WriteString(" ")
		buf.WriteString(strings.Join(vimmods, " "))
	}
	buf.WriteString("\n")
	for i := range f.body {
		r := t.toExcmd(f.body[i], f, level)
		_, err := io.Copy(&buf, r)
		if err != nil {
			return t.err(err, f.body[i])
		}
		buf.WriteString("\n")
	}
	buf.WriteString("endfunction")
	return strings.NewReader(buf.String())
}

func (t *vimTranslator) newLambdaReader(f *funcStmtOrExpr, parent node, level int) io.Reader {
	var buf bytes.Buffer
	buf.WriteString("{")
	for i := range f.args {
		if i > 0 {
			buf.WriteString(",")
		}
		buf.WriteString(f.args[i].name)
	}
	buf.WriteString("->")
	_, err := io.Copy(&buf, t.toReader(f.body[0], parent, level))
	if err != nil {
		return t.err(err, f.body[0])
	}
	buf.WriteString("}")
	return strings.NewReader(buf.String())
}

func (t *vimTranslator) convertModifiers(mods []string) (autoload, global bool, newmods []string) {
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

func (t *vimTranslator) newTernaryNodeReader(node *ternaryNode, parent node, level int) io.Reader {
	var cond bytes.Buffer
	_, err := io.Copy(&cond, t.toReader(node.cond, parent, level))
	if err != nil {
		return t.err(err, node.cond)
	}
	var left bytes.Buffer
	_, err = io.Copy(&left, t.toReader(node.left, parent, level))
	if err != nil {
		return t.err(err, node.left)
	}
	var right bytes.Buffer
	_, err = io.Copy(&right, t.toReader(node.right, parent, level))
	if err != nil {
		return t.err(err, node.right)
	}
	s := fmt.Sprintf("%s ? %s : %s",
		t.paren(cond.String(), node.cond),
		t.paren(left.String(), node.left),
		t.paren(right.String(), node.right))
	return strings.NewReader(s)
}

func (t *vimTranslator) newBinaryOpNodeReader(node binaryOpNode, parent node, level int, opstr string) io.Reader {
	var left bytes.Buffer
	_, err := io.Copy(&left, t.toReader(node.Left(), parent, level))
	if err != nil {
		return t.err(err, node.Left())
	}
	var right bytes.Buffer
	_, err = io.Copy(&right, t.toReader(node.Right(), parent, level))
	if err != nil {
		return t.err(err, node.Right())
	}
	s := fmt.Sprintf("%s %s %s",
		t.paren(left.String(), node.Left()),
		opstr,
		t.paren(right.String(), node.Right()))
	return strings.NewReader(s)
}

func (t *vimTranslator) newUnaryOpNodeReader(node unaryOpNode, parent node, level int, opstr string) io.Reader {
	var value bytes.Buffer
	_, err := io.Copy(&value, t.toReader(node.Value(), parent, level))
	if err != nil {
		return t.err(err, node.Value())
	}
	s := fmt.Sprintf("%s%s", opstr, t.paren(value.String(), node.Value()))
	return strings.NewReader(s)
}

func (t *vimTranslator) newSliceNodeReader(node *sliceNode, parent node, level int) io.Reader {
	var left bytes.Buffer
	_, err := io.Copy(&left, t.toReader(node.left, parent, level))
	if err != nil {
		return t.err(err, node.left)
	}
	from := "null"
	if node.rlist[0] != nil {
		var buf bytes.Buffer
		_, err := io.Copy(&buf, t.toReader(node.rlist[0], parent, level))
		if err != nil {
			return t.err(err, node.rlist[0])
		}
		from = buf.String()
	}
	to := "null"
	if node.rlist[1] != nil {
		var buf bytes.Buffer
		_, err := io.Copy(&buf, t.toReader(node.rlist[1], parent, level))
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

func (t *vimTranslator) newCallNodeReader(node *callNode, parent node, level int) io.Reader {
	var left bytes.Buffer
	_, err := io.Copy(&left, t.toReader(node.left, parent, level))
	if err != nil {
		return t.err(err, node.left)
	}
	rlist := make([]string, 0, len(node.rlist))
	for i := range node.rlist {
		var arg bytes.Buffer
		_, err := io.Copy(&arg, t.toReader(node.rlist[i], parent, level))
		if err != nil {
			return t.err(err, node.rlist[i])
		}
		rlist = append(rlist, arg.String())
	}
	s := fmt.Sprintf("%s(%s)", t.paren(left.String(), node.left), strings.Join(rlist, ","))
	return strings.NewReader(s)
}

func (t *vimTranslator) newSubscriptNodeReader(node *subscriptNode, parent node, level int) io.Reader {
	var left bytes.Buffer
	_, err := io.Copy(&left, t.toReader(node.left, parent, level))
	if err != nil {
		return t.err(err, node.left)
	}
	var right bytes.Buffer
	_, err = io.Copy(&right, t.toReader(node.right, parent, level))
	if err != nil {
		return t.err(err, node.right)
	}
	s := fmt.Sprintf("%s[%s]", t.paren(left.String(), node.left), right.String())
	return strings.NewReader(s)
}

func (t *vimTranslator) newDotNodeReader(node *dotNode, parent node, level int) io.Reader {
	var left bytes.Buffer
	_, err := io.Copy(&left, t.toReader(node.left, parent, level))
	if err != nil {
		return t.err(err, node.left)
	}
	s := fmt.Sprintf("%s.%s", t.paren(left.String(), node.left), node.right.value)
	return strings.NewReader(s)
}

func (t *vimTranslator) newIdentifierNodeReader(node *identifierNode, parent node, level int) io.Reader {
	return strings.NewReader(node.value)
}

func (t *vimTranslator) newIntNodeReader(node *intNode, parent node, level int) io.Reader {
	return strings.NewReader(node.value)
}

func (t *vimTranslator) newFloatNodeReader(node *floatNode, parent node, level int) io.Reader {
	return strings.NewReader(node.value)
}

func (t *vimTranslator) newStringNodeReader(node *stringNode, parent node, level int) io.Reader {
	return strings.NewReader(string(node.value))
}

func (t *vimTranslator) newLiteralNodeReader(node literalNode, parent node, level int, opstr string) io.Reader {
	return strings.NewReader(opstr + node.Value())
}

func (t *vimTranslator) newListNodeReader(node *listNode, parent node, level int) io.Reader {
	args := make([]string, 0, len(node.value))
	for i := range node.value {
		var arg bytes.Buffer
		_, err := io.Copy(&arg, t.toReader(node.value[i], parent, level))
		if err != nil {
			return t.err(err, node.value[i])
		}
		args = append(args, arg.String())
	}
	s := "[" + strings.Join(args, ",") + "]"
	return strings.NewReader(s)
}

func (t *vimTranslator) newDictionaryNodeReader(node *dictionaryNode, parent node, level int) io.Reader {
	args := make([]string, 0, len(node.value))
	for i := range node.value {
		var key bytes.Buffer
		keyNode := node.value[i][0]
		if id, ok := keyNode.(*identifierNode); ok {
			s, err := unevalString(id.value)
			if err != nil {
				return t.err(err, keyNode)
			}
			key.WriteString(string(*s))
		} else {
			_, err := io.Copy(&key, t.toReader(keyNode, parent, level))
			if err != nil {
				return t.err(err, keyNode)
			}
		}
		var val bytes.Buffer
		valNode := node.value[i][1]
		_, err := io.Copy(&val, t.toReader(valNode, parent, level))
		if err != nil {
			return t.err(err, valNode)
		}
		args = append(args, fmt.Sprintf("%s:%s", key.String(), val.String()))
	}
	s := "{" + strings.Join(args, ",") + "}"
	return strings.NewReader(s)
}

func (t *vimTranslator) toJSONString(vs *vainString) (string, error) {
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
func (t *vimTranslator) paren(s string, node node) string {
	if t.needsParen(node) {
		return "(" + s + ")"
	}
	return s
}

// needsParen returns true if node should be wrapped by parentheses.
func (t *vimTranslator) needsParen(node node) bool {
	switch node.(type) {
	case *topLevelNode:
		return false
	case *importStatement:
		return false
	case *funcStmtOrExpr:
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
	}
	return true
}

// walk walks node recursively and call f with each node.
// if f(node) == false, walk stops walking inner nodes.
func (t *vimTranslator) walk(node node, f func(node) bool) {
	if !f(node) {
		return
	}
	switch n := node.(type) {
	case *topLevelNode:
		for i := range n.body {
			t.walk(n.body[i], f)
		}
	case *importStatement:
	case *funcStmtOrExpr:
		for i := range n.args {
			if n.args[i].defaultVal != nil {
				t.walk(n.args[i].defaultVal, f)
			}
		}
		for i := range n.body {
			t.walk(n.body[i], f)
		}
	case *ternaryNode:
		t.walk(n.cond, f)
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *orNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *andNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *equalNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *equalCiNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *nequalNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *nequalCiNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *greaterNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *greaterCiNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *gequalNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *gequalCiNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *smallerNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *smallerCiNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *sequalNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *sequalCiNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *matchNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *matchCiNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *noMatchNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *noMatchCiNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *isNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *isCiNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *isNotNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *isNotCiNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *addNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *subtractNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *multiplyNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *divideNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *remainderNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *notNode:
		t.walk(n.left, f)
	case *minusNode:
		t.walk(n.left, f)
	case *plusNode:
		t.walk(n.left, f)
	case *sliceNode:
		t.walk(n.left, f)
		for i := range n.rlist {
			t.walk(n.rlist[i], f)
		}
	case *callNode:
		t.walk(n.left, f)
		for i := range n.rlist {
			t.walk(n.rlist[i], f)
		}
	case *subscriptNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *dotNode:
		t.walk(n.left, f)
		t.walk(n.right, f)
	case *identifierNode:
	case *intNode:
	case *floatNode:
	case *stringNode:
	case *listNode:
		for i := range n.value {
			t.walk(n.value[i], f)
		}
	case *dictionaryNode:
		for i := range n.value {
			t.walk(n.value[i][0], f)
			t.walk(n.value[i][1], f)
		}
	case *optionNode:
	case *envNode:
	case *regNode:
	}
}

func (t *vimTranslator) toExcmd(n, parent node, level int) io.Reader {
	rs := make([]io.Reader, 0, 2)
	_, isCall := n.(*callNode)
	if !isCall && t.isVoidExpr(n, parent) {
		// TODO Comment out each line. it may be safe because
		// currently an expression is output as one line...
		rs = append(rs, strings.NewReader("\" "))
	} else if isCall {
		rs = append(rs, strings.NewReader("call "))
	}
	// topLevelNode doesn't increment level
	if _, ok := parent.(*topLevelNode); !ok {
		level++
	}
	rs = append(rs, t.toReader(n, parent, level))
	return io.MultiReader(rs...)
}

func (t *vimTranslator) isVoidExpr(n, parent node) bool {
	switch parent.(type) {
	case *topLevelNode:
	case *funcStmtOrExpr:
	default:
		return false
	}
	if f, ok := n.(*funcStmtOrExpr); ok {
		return t.isVoidExprFunc(f, parent)
	} else if !n.IsExpr() {
		return false
	} else if _, ok := n.(*callNode); ok {
		return false
	}
	return true
}
