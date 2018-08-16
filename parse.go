package main

import (
	"errors"
	"fmt"

	"github.com/tyru/vain/node"
)

func parse(name string, inTokens <-chan token) *parser {
	return &parser{
		name:     name,
		inTokens: inTokens,
		outNodes: make(chan node.Node, 1),
	}
}

func (p *parser) Nodes() <-chan node.Node {
	return p.outNodes
}

type parser struct {
	name     string
	inTokens <-chan token
	outNodes chan node.Node
	token    *token  // next() sets read token to this.
	tokbuf   []token // next() doesn't read from inTokens if len(tokbuf) > 0 .
}

type expr interface {
	node.Node
}

type statement interface {
	node.Node
}

func (p *parser) Run() {
	if toplevel, ok := p.acceptTopLevel(); ok {
		p.emit(toplevel)
	}
	close(p.outNodes) // No more nodes will be delivered.
}

// emit passes an node back to the client.
func (p *parser) emit(node node.Node) {
	p.outNodes <- node
}

// errorf returns an error token and terminates the scan
func (p *parser) errorf(format string, args ...interface{}) {
	newargs := make([]interface{}, 0, len(args)+2)
	newargs = append(newargs, p.name, p.token.pos.Line(), p.token.pos.Col()+1)
	newargs = append(newargs, args...)
	err := fmt.Errorf("[parse] %s:%d:%d: "+format, newargs...)
	p.emit(node.NewErrorNode(err, p.token.pos))
}

// lexError is called when tokenError was given.
func (p *parser) lexError() {
	p.emit(node.NewErrorNode(errors.New(p.token.val), p.token.pos))
}

// next returns the next token in the input.
func (p *parser) next() *token {
	var t token
	if len(p.tokbuf) > 0 {
		t = p.tokbuf[len(p.tokbuf)-1]
		p.tokbuf = p.tokbuf[:len(p.tokbuf)-1]
	} else {
		t = <-p.inTokens
	}
	p.token = &t
	return &t
}

func (p *parser) backup(t *token) {
	p.tokbuf = append(p.tokbuf, *t)
}

// peek returns but does not consume
// the next token in the input.
func (p *parser) peek() *token {
	t := p.next()
	p.backup(t)
	return t
}

// accept consumes the next token if its type is typ.
func (p *parser) accept(typ tokenType) bool {
	t := p.next()
	if t.typ == typ {
		return true
	}
	p.backup(t)
	return false
}

// acceptSpaces accepts 1*LF .
func (p *parser) acceptSpaces() bool {
	if p.accept(tokenNewline) {
		for p.accept(tokenNewline) {
		}
		return true
	}
	return false
}

// acceptBlanks accepts 1*( LF | comment ) .
func (p *parser) acceptBlanks() bool {
	t := p.next()
	if t.typ == tokenNewline || t.typ == tokenComment {
		for {
			t = p.next()
			if t.typ == tokenNewline || t.typ == tokenComment {
				continue
			}
			p.backup(t)
			return true
		}
	}
	p.backup(t)
	return false
}

// acceptIdentifierLike accepts token where canBeIdentifier(token) == true
func (p *parser) acceptIdentifierLike() bool {
	if p.canBeIdentifier(p.peek()) {
		p.next()
		return true
	}
	return false
}

type topLevelNode struct {
	body []node.Node
}

// Clone clones itself.
func (n *topLevelNode) Clone() node.Node {
	body := make([]node.Node, len(n.body))
	for i := range n.body {
		body[i] = n.body[i].Clone()
	}
	return &topLevelNode{body}
}

func (n *topLevelNode) TerminalNode() node.Node {
	return n
}

func (n *topLevelNode) Position() *node.Pos {
	return nil
}

func (n *topLevelNode) IsExpr() bool {
	return false
}

func (p *parser) acceptTopLevel() (*node.PosNode, bool) {
	pos := node.NewPos(0, 1, 0)
	toplevel := &topLevelNode{make([]node.Node, 0, 32)}
	for {
		n, ok := p.acceptStmtOrExpr()
		if !ok {
			return node.NewPosNode(pos, toplevel), true
		}
		toplevel.body = append(toplevel.body, n)
	}
}

type commentNode struct {
	value string
}

// Clone clones itself.
func (n *commentNode) Clone() node.Node {
	return &commentNode{n.value}
}

func (n *commentNode) TerminalNode() node.Node {
	return n
}

func (n *commentNode) Position() *node.Pos {
	return nil
}

func (n *commentNode) IsExpr() bool {
	return false
}

func (n *commentNode) Value() string {
	return n.value[1:]
}

// statementOrExpression := *LF ( comment | statement | expr )
func (p *parser) acceptStmtOrExpr() (node.Node, bool) {
	p.acceptSpaces()
	if p.accept(tokenEOF) {
		return nil, false
	}
	if p.accept(tokenError) {
		p.lexError()
		return nil, false
	}

	// Comment
	if p.accept(tokenComment) {
		n := node.NewPosNode(p.token.pos, &commentNode{p.token.val})
		return n, true
	}

	// Statement
	if p.accept(tokenFunc) {
		p.backup(p.token)
		return p.acceptFunction(false)
	}
	if p.accept(tokenConst) {
		p.backup(p.token)
		return p.acceptConstStatement()
	}
	if p.accept(tokenReturn) {
		p.backup(p.token)
		return p.acceptReturnStatement()
	}
	if p.accept(tokenIf) {
		p.backup(p.token)
		return p.acceptIfStatement()
	}
	if p.accept(tokenWhile) {
		p.backup(p.token)
		return p.acceptWhileStatement()
	}
	if p.accept(tokenFor) {
		p.backup(p.token)
		return p.acceptForStatement()
	}
	if p.accept(tokenImport) || p.accept(tokenFrom) {
		p.backup(p.token)
		return p.acceptImportStatement()
	}

	// Expression
	return p.acceptExpr()
}

type constStatement struct {
	left          node.Node
	right         expr
	hasUnderscore bool
}

// Clone clones itself.
func (n *constStatement) Clone() node.Node {
	return &constStatement{n.left.Clone(), n.right.Clone(), n.hasUnderscore}
}

func (n *constStatement) TerminalNode() node.Node {
	return n
}

func (n *constStatement) Position() *node.Pos {
	return nil
}

func (n *constStatement) IsExpr() bool {
	return false
}

// constStatement := "const" assignLhs "=" expr
func (p *parser) acceptConstStatement() (node.Node, bool) {
	if !p.accept(tokenConst) {
		p.errorf("expected %s but got %s", tokenName(tokenConst), tokenName(p.peek().typ))
		return nil, false
	}
	pos := p.token.pos
	left, hasUnderscore, ok := p.acceptAssignLHS()
	if !ok {
		return nil, false
	}
	if !p.accept(tokenEqual) {
		p.errorf("expected %s but got %s", tokenName(tokenEqual), tokenName(p.peek().typ))
		return nil, false
	}
	right, ok := p.acceptExpr()
	if !ok {
		return nil, false
	}
	n := node.NewPosNode(pos, &constStatement{left, right, hasUnderscore})
	return n, true
}

// assignLhs := identifier | destructuringAssignment
func (p *parser) acceptAssignLHS() (node.Node, bool, bool) {
	var left node.Node
	var hasUnderscore bool
	if p.accept(tokenIdentifier) {
		left = node.NewPosNode(p.token.pos, &identifierNode{p.token.val})
	} else if ids, underscore, listpos, ok := p.acceptDestructuringAssignment(); ok {
		left = node.NewPosNode(listpos, &listNode{ids})
		hasUnderscore = underscore
	} else {
		p.errorf("expected %s or destructuring assignment but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ))
		return nil, false, false
	}
	return left, hasUnderscore, true
}

// destructuringAssignment := "[" *blank
//                            *( identifierOrUnderscore *blank "," )
//                            identifierOrUnderscore *blank [ "," ]
//                          *blank "]"
// identifierOrUnderscore := identifier | "_"
func (p *parser) acceptDestructuringAssignment() ([]expr, bool, *node.Pos, bool) {
	if !p.accept(tokenSqOpen) {
		p.errorf("expected %s but got %s", tokenName(tokenLt), tokenName(p.peek().typ))
		return nil, false, nil, false
	}
	pos := p.token.pos
	p.acceptBlanks()
	if p.accept(tokenSqClose) {
		p.errorf("at least 1 identifier is needed")
		return nil, false, nil, false
	}
	ids := make([]expr, 0, 8)
	var hasUnderscore bool
	for {
		if !p.accept(tokenIdentifier) && !p.accept(tokenUnderscore) {
			p.errorf("expected %s or %s but got %s",
				tokenName(tokenIdentifier), tokenName(tokenUnderscore), tokenName(p.peek().typ))
			return nil, false, nil, false
		}
		if p.token.val == "_" {
			hasUnderscore = true
		}
		ids = append(ids, node.NewPosNode(p.token.pos, &identifierNode{p.token.val}))
		p.acceptBlanks()
		p.accept(tokenComma)
		p.acceptBlanks()
		if p.accept(tokenSqClose) {
			break
		}
	}
	return ids, hasUnderscore, pos, true
}

type returnStatement struct {
	left expr
}

// Clone clones itself.
func (n *returnStatement) Clone() node.Node {
	if n.left == nil {
		return &returnStatement{nil}
	}
	return &returnStatement{n.left.Clone()}
}

func (n *returnStatement) TerminalNode() node.Node {
	return n
}

func (n *returnStatement) Position() *node.Pos {
	return nil
}

func (n *returnStatement) IsExpr() bool {
	return false
}

// returnStatement := "return" ( expr | LF )
// and if tokenCClose is detected instead of expr,
// it must be empty return statement inside block.
func (p *parser) acceptReturnStatement() (node.Node, bool) {
	if !p.accept(tokenReturn) {
		p.errorf("expected %s but got %s", tokenName(tokenReturn), tokenName(p.peek().typ))
		return nil, false
	}
	ret := p.token
	if p.accept(tokenNewline) {
		return node.NewPosNode(ret.pos, &returnStatement{nil}), true
	}
	if p.accept(tokenCClose) { // end of block
		p.backup(p.token)
		return node.NewPosNode(ret.pos, &returnStatement{nil}), true
	}
	expr, ok := p.acceptExpr()
	if !ok {
		return nil, false
	}
	return node.NewPosNode(ret.pos, &returnStatement{expr}), true
}

type ifStatement struct {
	cond expr
	body []node.Node
	els  []node.Node
}

// Clone clones itself.
func (n *ifStatement) Clone() node.Node {
	body := make([]node.Node, len(n.body))
	for i := range n.body {
		body[i] = n.body[i].Clone()
	}
	els := make([]node.Node, len(n.els))
	for i := range n.els {
		els[i] = n.els[i].Clone()
	}
	return &ifStatement{
		n.cond.Clone(), body, els,
	}
}

func (n *ifStatement) TerminalNode() node.Node {
	return n
}

func (n *ifStatement) Position() *node.Pos {
	return nil
}

func (n *ifStatement) IsExpr() bool {
	return false
}

// ifStatement := "if" *blank expr *blank block
//                [ *blank "else" *blank ( ifStatement | block ) ]
func (p *parser) acceptIfStatement() (node.Node, bool) {
	if !p.accept(tokenIf) {
		p.errorf("expected if statement but got %s", tokenName(p.peek().typ))
		return nil, false
	}
	p.acceptBlanks()
	pos := p.token.pos
	cond, ok := p.acceptExpr()
	if !ok {
		return nil, false
	}
	p.acceptBlanks()
	body, ok := p.acceptBlock()
	if !ok {
		return nil, false
	}
	var els []node.Node
	p.acceptBlanks()
	if p.accept(tokenElse) {
		p.acceptBlanks()
		if p.accept(tokenIf) {
			p.backup(p.token)
			ifstmt, ok := p.acceptIfStatement()
			if !ok {
				return nil, false
			}
			els = []node.Node{ifstmt}
		} else if p.accept(tokenCOpen) {
			p.backup(p.token)
			block, ok := p.acceptBlock()
			if !ok {
				return nil, false
			}
			els = block
		} else {
			p.errorf("expected if or block statement but got %s", tokenName(p.peek().typ))
			return nil, false
		}
	}
	n := node.NewPosNode(pos, &ifStatement{cond, body, els})
	return n, true
}

type whileStatement struct {
	cond expr
	body []node.Node
}

// Clone clones itself.
func (n *whileStatement) Clone() node.Node {
	body := make([]node.Node, len(n.body))
	for i := range n.body {
		body[i] = n.body[i].Clone()
	}
	return &whileStatement{
		n.cond.Clone(), body,
	}
}

func (n *whileStatement) TerminalNode() node.Node {
	return n
}

func (n *whileStatement) Position() *node.Pos {
	return nil
}

func (n *whileStatement) IsExpr() bool {
	return false
}

// whileStatement := "while" *blank expr *blank block
func (p *parser) acceptWhileStatement() (node.Node, bool) {
	if !p.accept(tokenWhile) {
		p.errorf("expected while statement but got %s", tokenName(p.peek().typ))
		return nil, false
	}
	p.acceptBlanks()
	pos := p.token.pos
	cond, ok := p.acceptExpr()
	if !ok {
		return nil, false
	}
	p.acceptBlanks()
	body, ok := p.acceptBlock()
	if !ok {
		return nil, false
	}
	n := node.NewPosNode(pos, &whileStatement{cond, body})
	return n, true
}

type forStatement struct {
	left          node.Node
	right         expr
	body          []node.Node
	hasUnderscore bool
}

// Clone clones itself.
func (n *forStatement) Clone() node.Node {
	body := make([]node.Node, len(n.body))
	for i := range n.body {
		body[i] = n.body[i].Clone()
	}
	return &forStatement{
		n.left.Clone(), n.right.Clone(), body, n.hasUnderscore,
	}
}

func (n *forStatement) TerminalNode() node.Node {
	return n
}

func (n *forStatement) Position() *node.Pos {
	return nil
}

func (n *forStatement) IsExpr() bool {
	return false
}

// forStatement := "for" *blank assignLhs *blank "in" *blank expr *blank block
func (p *parser) acceptForStatement() (node.Node, bool) {
	if !p.accept(tokenFor) {
		p.errorf("expected for statement but got %s", tokenName(p.peek().typ))
		return nil, false
	}
	p.acceptBlanks()
	pos := p.token.pos
	left, hasUnderscore, ok := p.acceptAssignLHS()
	if !ok {
		return nil, false
	}
	p.acceptBlanks()
	if !p.accept(tokenIn) {
		p.errorf("expected %s but got %s", tokenName(tokenIn), tokenName(p.peek().typ))
		return nil, false
	}
	p.acceptBlanks()
	right, ok := p.acceptExpr()
	if !ok {
		return nil, false
	}
	p.acceptBlanks()
	body, ok := p.acceptBlock()
	if !ok {
		return nil, false
	}
	n := node.NewPosNode(pos, &forStatement{left, right, body, hasUnderscore})
	return n, true
}

// block := "{" *blank *( statementOrExpression *blank ) "}"
func (p *parser) acceptBlock() ([]node.Node, bool) {
	if !p.accept(tokenCOpen) {
		p.errorf("expected %s but got %s", tokenName(tokenCOpen), tokenName(p.peek().typ))
		return nil, false
	}
	var nodes []node.Node
	p.acceptBlanks()
	if !p.accept(tokenCClose) {
		nodes = make([]node.Node, 0, 16)
		for {
			stmt, ok := p.acceptStmtOrExpr()
			if !ok {
				return nil, false
			}
			p.acceptBlanks()
			nodes = append(nodes, stmt)
			if p.accept(tokenCClose) {
				break
			}
		}
	}
	return nodes, true
}

type importStatement struct {
	pkg      vainString
	pkgAlias string
	fnlist   [][]string
}

// Clone clones itself.
func (n *importStatement) Clone() node.Node {
	fnlist := make([][]string, len(n.fnlist))
	for i := range n.fnlist {
		pair := make([]string, len(n.fnlist[i]))
		copy(pair, n.fnlist[i])
		fnlist[i] = pair
	}
	return &importStatement{
		n.pkg, n.pkgAlias, n.fnlist,
	}
}

func (n *importStatement) TerminalNode() node.Node {
	return n
}

func (n *importStatement) Position() *node.Pos {
	return nil
}

func (n *importStatement) IsExpr() bool {
	return false
}

// importStatement := "import" string [ "as" *blank identifier ] |
//                    "from" string "import" <importFunctionList>
func (p *parser) acceptImportStatement() (*node.PosNode, bool) {
	if p.accept(tokenImport) {
		pos := p.token.pos
		if !p.accept(tokenString) {
			p.errorf("expected %s but got %s", tokenName(tokenString), tokenName(p.peek().typ))
			return nil, false
		}
		pkg := vainString(p.token.val)
		var pkgAlias string
		if p.accept(tokenAs) {
			p.acceptBlanks()
			if !p.accept(tokenIdentifier) {
				p.errorf("expected %s but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ))
				return nil, false
			}
			pkgAlias = p.token.val
		}
		stmt := node.NewPosNode(pos, &importStatement{pkg, pkgAlias, nil})
		return stmt, true

	} else if p.accept(tokenFrom) {
		pos := p.token.pos
		if !p.accept(tokenString) {
			p.errorf("expected %s but got %s", tokenName(tokenString), tokenName(p.peek().typ))
			return nil, false
		}
		pkg := vainString(p.token.val)
		if !p.accept(tokenImport) {
			p.errorf("expected %s but got %s", tokenName(tokenImport), tokenName(p.peek().typ))
			return nil, false
		}
		fnlist, ok := p.acceptImportFunctionList()
		if !ok {
			return nil, false
		}
		stmt := node.NewPosNode(pos, &importStatement{pkg, "", fnlist})
		return stmt, true
	}

	p.errorf("expected %s or %s but got %s",
		tokenName(tokenImport), tokenName(tokenFrom), tokenName(p.peek().typ))
	return nil, false
}

// importFunctionList = importFunctionListItem *( *blank "," *blank importFunctionListItem )
// importFunctionListItem := identifier [ "as" *blank identifier ]
func (p *parser) acceptImportFunctionList() ([][]string, bool) {
	fnlist := make([][]string, 0, 1)
	for {
		if !p.accept(tokenIdentifier) {
			p.errorf("expected %s but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ))
			return nil, false
		}
		orig := p.token.val
		to := orig

		if p.accept(tokenAs) {
			p.acceptBlanks()
			if !p.accept(tokenIdentifier) {
				p.errorf("expected %s but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ))
				return nil, false
			}
			to = p.token.val
			fnlist = append(fnlist, []string{orig, to})
		} else {
			fnlist = append(fnlist, []string{orig})
		}

		p.acceptBlanks()
		if !p.accept(tokenComma) {
			break
		}
		p.acceptBlanks()
	}

	return fnlist, true
}

type funcStmtOrExpr struct {
	isExpr     bool
	mods       []string
	name       string
	args       []argument
	retType    string
	bodyIsStmt bool
	body       []node.Node
}

// Clone clones itself.
func (n *funcStmtOrExpr) Clone() node.Node {
	mods := make([]string, len(n.mods))
	copy(mods, n.mods)
	args := make([]argument, len(n.args))
	for i := range n.args {
		args[i] = *n.args[i].Clone()
	}
	body := make([]node.Node, len(n.body))
	for i := range n.body {
		body[i] = n.body[i].Clone()
	}
	return &funcStmtOrExpr{
		n.isExpr, mods, n.name, args, n.retType, n.bodyIsStmt, body,
	}
}

func (n *funcStmtOrExpr) TerminalNode() node.Node {
	return n
}

func (n *funcStmtOrExpr) Position() *node.Pos {
	return nil
}

func (n *funcStmtOrExpr) IsExpr() bool {
	return n.isExpr
}

// function :=
//        "func" [ functionModifierList ] [ identifier ] functionCallSignature expr1 /
//        "func" [ functionModifierList ] [ identifier ] functionCallSignature block
func (p *parser) acceptFunction(isExpr bool) (*node.PosNode, bool) {
	if !p.accept(tokenFunc) {
		p.errorf("expected %s but got %s", tokenName(tokenFunc), tokenName(p.peek().typ))
		return nil, false
	}
	pos := p.token.pos

	var mods []string
	var name string
	var args []argument
	var retType string
	var bodyIsStmt bool
	var body []node.Node

	// Modifiers
	if p.accept(tokenLt) {
		p.backup(p.token)
		var ok bool
		mods, ok = p.acceptModifiers()
		if !ok {
			return nil, false
		}
	}

	// Function name (if empty, this is an expression not a statement)
	if p.accept(tokenIdentifier) {
		name = p.token.val
	}

	// functionCallSignature
	var ok bool
	args, retType, ok = p.acceptFunctionCallSignature()
	if !ok {
		return nil, false
	}

	// Body
	if p.accept(tokenCOpen) {
		p.backup(p.token)
		bodyIsStmt = true
		block, ok := p.acceptBlock()
		if !ok {
			return nil, false
		}
		body = block
	} else {
		expr, ok := p.acceptExpr()
		if !ok {
			return nil, false
		}
		body = []node.Node{expr}
	}

	f := node.NewPosNode(pos, &funcStmtOrExpr{isExpr, mods, name, args, retType, bodyIsStmt, body})
	return f, true
}

// functionModifierList := "<" *blank
//                            *( functionModifier *blank "," )
//                            functionModifier *blank [ "," ]
//                          *blank ">"
func (p *parser) acceptModifiers() ([]string, bool) {
	if !p.accept(tokenLt) {
		p.errorf("expected %s but got %s", tokenName(tokenLt), tokenName(p.peek().typ))
		return nil, false
	}
	p.acceptBlanks()
	if p.accept(tokenGt) {
		p.errorf("at least 1 modifier is needed")
		return nil, false
	}
	mods := make([]string, 0, 8)
	for {
		if !p.acceptFunctionModifier() {
			p.errorf("expected function modifier but got %s", tokenName(p.peek().typ))
			return nil, false
		}
		mods = append(mods, p.token.val)
		p.acceptBlanks()
		p.accept(tokenComma)
		p.acceptBlanks()
		if p.accept(tokenGt) {
			break
		}
	}
	return mods, true
}

// functionModifier := "noabort" | "autoload" | "global" | "range" | "dict" | "closure"
func (p *parser) acceptFunctionModifier() bool {
	if !p.accept(tokenIdentifier) {
		return false
	}
	switch p.token.val {
	case "noabort":
	case "autoload":
	case "global":
	case "range":
	case "dict":
	case "closure":
	default:
		return false
	}
	return true
}

// functionCallSignature := "(" *blank
//                            *( functionArgument *blank [ "," ] *blank )
//                          ")" [ ":" type ]
func (p *parser) acceptFunctionCallSignature() ([]argument, string, bool) {
	if !p.accept(tokenPOpen) {
		p.errorf("expected %s but got %s", tokenName(tokenPOpen), tokenName(p.peek().typ))
		return nil, "", false
	}
	p.acceptBlanks()

	var args []argument
	if !p.accept(tokenPClose) {
		args = make([]argument, 0, 8)
		for {
			arg, ok := p.acceptFunctionArgument()
			if !ok {
				return nil, "", false
			}
			args = append(args, *arg)
			p.acceptBlanks()
			p.accept(tokenComma)
			p.acceptBlanks()
			if p.accept(tokenPClose) {
				break
			}
		}
	}

	var retType string
	if p.accept(tokenColon) {
		var ok bool
		retType, ok = p.acceptType()
		if !ok {
			return nil, "", false
		}
	}
	return args, retType, true
}

type argument struct {
	name       string
	typ        string
	defaultVal expr
}

func (a *argument) Clone() *argument {
	if a.defaultVal != nil {
		return &argument{a.name, a.typ, a.defaultVal.Clone()}
	}
	return &argument{a.name, a.typ, nil}
}

// functionArgument := identifier ":" *blanks type /
//                     identifier "=" *blanks expr
func (p *parser) acceptFunctionArgument() (*argument, bool) {
	var name string
	var typ string

	if !p.accept(tokenIdentifier) {
		p.errorf("expected %s but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ))
		return nil, false
	}
	name = p.token.val

	if p.accept(tokenColon) {
		p.acceptBlanks()
		var ok bool
		typ, ok = p.acceptType()
		if !ok {
			return nil, false
		}
		return &argument{name, typ, nil}, true
	} else if p.accept(tokenEqual) {
		p.acceptBlanks()
		expr, ok := p.acceptExpr()
		if !ok {
			return nil, false
		}
		return &argument{name, "", expr}, true
	}

	p.errorf("expected %s or %s but got %s",
		tokenName(tokenColon), tokenName(tokenEqual), tokenName(p.peek().typ))
	return nil, false
}

// TODO: Complex type like array, dictionary, generics...
// type := identifier
func (p *parser) acceptType() (string, bool) {
	if !p.accept(tokenIdentifier) {
		p.errorf("expected %s but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ))
		return "", false
	}
	return p.token.val, true
}

func (p *parser) acceptExpr() (expr, bool) {
	return p.acceptExpr1()
}

type ternaryNode struct {
	cond  expr
	left  expr
	right expr
}

// Clone clones itself.
func (n *ternaryNode) Clone() node.Node {
	return &ternaryNode{n.cond.Clone(), n.left.Clone(), n.right.Clone()}
}

func (n *ternaryNode) TerminalNode() node.Node {
	return n
}

func (n *ternaryNode) Position() *node.Pos {
	return nil
}

func (n *ternaryNode) IsExpr() bool {
	return true
}

// expr1 := expr2 [ "?" *blank expr1 *blank ":" *blank expr1 ]
func (p *parser) acceptExpr1() (expr, bool) {
	left, ok := p.acceptExpr2()
	if !ok {
		return nil, false
	}
	if p.accept(tokenQuestion) {
		p.acceptBlanks()
		expr, ok := p.acceptExpr1()
		if !ok {
			return nil, false
		}
		p.acceptBlanks()
		if !p.accept(tokenColon) {
			p.errorf("expected %s but got %s", tokenName(tokenColon), tokenName(p.peek().typ))
			return nil, false
		}
		p.acceptBlanks()
		right, ok := p.acceptExpr1()
		if !ok {
			return nil, false
		}
		left = node.NewPosNode(p.token.pos, &ternaryNode{left, expr, right})
	}
	return left, true
}

type binaryOpNode interface {
	Left() node.Node
	Right() node.Node
}

type orNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *orNode) Clone() node.Node {
	return &orNode{n.left.Clone(), n.right.Clone()}
}

func (n *orNode) TerminalNode() node.Node {
	return n
}

func (n *orNode) Position() *node.Pos {
	return nil
}

func (n *orNode) IsExpr() bool {
	return true
}

func (n *orNode) Left() node.Node {
	return n.left
}

func (n *orNode) Right() node.Node {
	return n.right
}

// expr2 := expr3 *( "||" *blank expr3 )
func (p *parser) acceptExpr2() (expr, bool) {
	left, ok := p.acceptExpr3()
	if !ok {
		return nil, false
	}
	for {
		if p.accept(tokenOrOr) {
			pos := p.token.pos
			n := &orNode{left, nil}
			p.acceptBlanks()
			right, ok := p.acceptExpr3()
			if !ok {
				return nil, false
			}
			n.right = right
			left = node.NewPosNode(pos, n)
		} else {
			break
		}
	}
	return left, true
}

type andNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *andNode) Clone() node.Node {
	return &andNode{n.left.Clone(), n.right.Clone()}
}

func (n *andNode) TerminalNode() node.Node {
	return n
}

func (n *andNode) Position() *node.Pos {
	return nil
}

func (n *andNode) IsExpr() bool {
	return true
}

func (n *andNode) Left() node.Node {
	return n.left
}

func (n *andNode) Right() node.Node {
	return n.right
}

// expr3 := expr4 *( "&&" *blank expr4 )
func (p *parser) acceptExpr3() (expr, bool) {
	left, ok := p.acceptExpr4()
	if !ok {
		return nil, false
	}
	for {
		if p.accept(tokenAndAnd) {
			pos := p.token.pos
			n := &andNode{left, nil}
			p.acceptBlanks()
			right, ok := p.acceptExpr4()
			if !ok {
				return nil, false
			}
			n.right = right
			left = node.NewPosNode(pos, n)
		} else {
			break
		}
	}
	return left, true
}

type equalNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *equalNode) Clone() node.Node {
	return &equalNode{n.left.Clone(), n.right.Clone()}
}

func (n *equalNode) TerminalNode() node.Node {
	return n
}

func (n *equalNode) Position() *node.Pos {
	return nil
}

func (n *equalNode) IsExpr() bool {
	return true
}

func (n *equalNode) Left() node.Node {
	return n.left
}

func (n *equalNode) Right() node.Node {
	return n.right
}

type equalCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *equalCiNode) Clone() node.Node {
	return &equalCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *equalCiNode) TerminalNode() node.Node {
	return n
}

func (n *equalCiNode) Position() *node.Pos {
	return nil
}

func (n *equalCiNode) IsExpr() bool {
	return true
}

func (n *equalCiNode) Left() node.Node {
	return n.left
}

func (n *equalCiNode) Right() node.Node {
	return n.right
}

type nequalNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *nequalNode) Clone() node.Node {
	return &nequalNode{n.left.Clone(), n.right.Clone()}
}

func (n *nequalNode) TerminalNode() node.Node {
	return n
}

func (n *nequalNode) Position() *node.Pos {
	return nil
}

func (n *nequalNode) IsExpr() bool {
	return true
}

func (n *nequalNode) Left() node.Node {
	return n.left
}

func (n *nequalNode) Right() node.Node {
	return n.right
}

type nequalCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *nequalCiNode) Clone() node.Node {
	return &nequalCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *nequalCiNode) TerminalNode() node.Node {
	return n
}

func (n *nequalCiNode) Position() *node.Pos {
	return nil
}

func (n *nequalCiNode) IsExpr() bool {
	return true
}

func (n *nequalCiNode) Left() node.Node {
	return n.left
}

func (n *nequalCiNode) Right() node.Node {
	return n.right
}

type greaterNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *greaterNode) Clone() node.Node {
	return &greaterNode{n.left.Clone(), n.right.Clone()}
}

func (n *greaterNode) TerminalNode() node.Node {
	return n
}

func (n *greaterNode) Position() *node.Pos {
	return nil
}

func (n *greaterNode) IsExpr() bool {
	return true
}

func (n *greaterNode) Left() node.Node {
	return n.left
}

func (n *greaterNode) Right() node.Node {
	return n.right
}

type greaterCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *greaterCiNode) Clone() node.Node {
	return &greaterCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *greaterCiNode) TerminalNode() node.Node {
	return n
}

func (n *greaterCiNode) Position() *node.Pos {
	return nil
}

func (n *greaterCiNode) IsExpr() bool {
	return true
}

func (n *greaterCiNode) Left() node.Node {
	return n.left
}

func (n *greaterCiNode) Right() node.Node {
	return n.right
}

type gequalNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *gequalNode) Clone() node.Node {
	return &gequalNode{n.left.Clone(), n.right.Clone()}
}

func (n *gequalNode) TerminalNode() node.Node {
	return n
}

func (n *gequalNode) Position() *node.Pos {
	return nil
}

func (n *gequalNode) IsExpr() bool {
	return true
}

func (n *gequalNode) Left() node.Node {
	return n.left
}

func (n *gequalNode) Right() node.Node {
	return n.right
}

type gequalCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *gequalCiNode) Clone() node.Node {
	return &gequalCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *gequalCiNode) TerminalNode() node.Node {
	return n
}

func (n *gequalCiNode) Position() *node.Pos {
	return nil
}

func (n *gequalCiNode) IsExpr() bool {
	return true
}

func (n *gequalCiNode) Left() node.Node {
	return n.left
}

func (n *gequalCiNode) Right() node.Node {
	return n.right
}

type smallerNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *smallerNode) Clone() node.Node {
	return &smallerNode{n.left.Clone(), n.right.Clone()}
}

func (n *smallerNode) TerminalNode() node.Node {
	return n
}

func (n *smallerNode) Position() *node.Pos {
	return nil
}

func (n *smallerNode) IsExpr() bool {
	return true
}

func (n *smallerNode) Left() node.Node {
	return n.left
}

func (n *smallerNode) Right() node.Node {
	return n.right
}

type smallerCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *smallerCiNode) Clone() node.Node {
	return &smallerCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *smallerCiNode) TerminalNode() node.Node {
	return n
}

func (n *smallerCiNode) Position() *node.Pos {
	return nil
}

func (n *smallerCiNode) IsExpr() bool {
	return true
}

func (n *smallerCiNode) Left() node.Node {
	return n.left
}

func (n *smallerCiNode) Right() node.Node {
	return n.right
}

type sequalNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *sequalNode) Clone() node.Node {
	return &sequalNode{n.left.Clone(), n.right.Clone()}
}

func (n *sequalNode) TerminalNode() node.Node {
	return n
}

func (n *sequalNode) Position() *node.Pos {
	return nil
}

func (n *sequalNode) IsExpr() bool {
	return true
}

func (n *sequalNode) Left() node.Node {
	return n.left
}

func (n *sequalNode) Right() node.Node {
	return n.right
}

type sequalCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *sequalCiNode) Clone() node.Node {
	return &sequalCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *sequalCiNode) TerminalNode() node.Node {
	return n
}

func (n *sequalCiNode) Position() *node.Pos {
	return nil
}

func (n *sequalCiNode) IsExpr() bool {
	return true
}

func (n *sequalCiNode) Left() node.Node {
	return n.left
}

func (n *sequalCiNode) Right() node.Node {
	return n.right
}

type matchNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *matchNode) Clone() node.Node {
	return &matchNode{n.left.Clone(), n.right.Clone()}
}

func (n *matchNode) TerminalNode() node.Node {
	return n
}

func (n *matchNode) Position() *node.Pos {
	return nil
}

func (n *matchNode) IsExpr() bool {
	return true
}

func (n *matchNode) Left() node.Node {
	return n.left
}

func (n *matchNode) Right() node.Node {
	return n.right
}

type matchCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *matchCiNode) Clone() node.Node {
	return &matchCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *matchCiNode) TerminalNode() node.Node {
	return n
}

func (n *matchCiNode) Position() *node.Pos {
	return nil
}

func (n *matchCiNode) IsExpr() bool {
	return true
}

func (n *matchCiNode) Left() node.Node {
	return n.left
}

func (n *matchCiNode) Right() node.Node {
	return n.right
}

type noMatchNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *noMatchNode) Clone() node.Node {
	return &noMatchNode{n.left.Clone(), n.right.Clone()}
}

func (n *noMatchNode) TerminalNode() node.Node {
	return n
}

func (n *noMatchNode) Position() *node.Pos {
	return nil
}

func (n *noMatchNode) IsExpr() bool {
	return true
}

func (n *noMatchNode) Left() node.Node {
	return n.left
}

func (n *noMatchNode) Right() node.Node {
	return n.right
}

type noMatchCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *noMatchCiNode) Clone() node.Node {
	return &noMatchCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *noMatchCiNode) TerminalNode() node.Node {
	return n
}

func (n *noMatchCiNode) Position() *node.Pos {
	return nil
}

func (n *noMatchCiNode) IsExpr() bool {
	return true
}

func (n *noMatchCiNode) Left() node.Node {
	return n.left
}

func (n *noMatchCiNode) Right() node.Node {
	return n.right
}

type isNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *isNode) Clone() node.Node {
	return &isNode{n.left.Clone(), n.right.Clone()}
}

func (n *isNode) TerminalNode() node.Node {
	return n
}

func (n *isNode) Position() *node.Pos {
	return nil
}

func (n *isNode) IsExpr() bool {
	return true
}

func (n *isNode) Left() node.Node {
	return n.left
}

func (n *isNode) Right() node.Node {
	return n.right
}

type isCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *isCiNode) Clone() node.Node {
	return &isCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *isCiNode) TerminalNode() node.Node {
	return n
}

func (n *isCiNode) Position() *node.Pos {
	return nil
}

func (n *isCiNode) IsExpr() bool {
	return true
}

func (n *isCiNode) Left() node.Node {
	return n.left
}

func (n *isCiNode) Right() node.Node {
	return n.right
}

type isNotNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *isNotNode) Clone() node.Node {
	return &isNotNode{n.left.Clone(), n.right.Clone()}
}

func (n *isNotNode) TerminalNode() node.Node {
	return n
}

func (n *isNotNode) Position() *node.Pos {
	return nil
}

func (n *isNotNode) IsExpr() bool {
	return true
}

func (n *isNotNode) Left() node.Node {
	return n.left
}

func (n *isNotNode) Right() node.Node {
	return n.right
}

type isNotCiNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *isNotCiNode) Clone() node.Node {
	return &isNotCiNode{n.left.Clone(), n.right.Clone()}
}

func (n *isNotCiNode) TerminalNode() node.Node {
	return n
}

func (n *isNotCiNode) Position() *node.Pos {
	return nil
}

func (n *isNotCiNode) IsExpr() bool {
	return true
}

func (n *isNotCiNode) Left() node.Node {
	return n.left
}

func (n *isNotCiNode) Right() node.Node {
	return n.right
}

// expr4 := expr5 "=="  *blank expr5 /
//          expr5 "==?" *blank expr5 /
//          expr5 "!="  *blank expr5 /
//          expr5 "!=?" *blank expr5 /
//          expr5 ">"   *blank expr5 /
//          expr5 ">?"  *blank expr5 /
//          expr5 ">="  *blank expr5 /
//          expr5 ">=?" *blank expr5 /
//          expr5 "<"   *blank expr5 /
//          expr5 "<?"  *blank expr5 /
//          expr5 "<="  *blank expr5 /
//          expr5 "<=?" *blank expr5 /
//          expr5 "=~"  *blank expr5 /
//          expr5 "=~?" *blank expr5 /
//          expr5 "!~"  *blank expr5 /
//          expr5 "!~?" *blank expr5 /
//          expr5 "is"  *blank expr5 /
//          expr5 "is?" *blank expr5 /
//          expr5 "isnot"  *blank expr5 /
//          expr5 "isnot?" *blank expr5 /
//          expr5
func (p *parser) acceptExpr4() (expr, bool) {
	left, ok := p.acceptExpr5()
	if !ok {
		return nil, false
	}
	if p.accept(tokenEqEq) {
		pos := p.token.pos
		n := &equalNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenEqEqCi) {
		pos := p.token.pos
		n := &equalCiNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenNeq) {
		pos := p.token.pos
		n := &nequalNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenNeqCi) {
		pos := p.token.pos
		n := &nequalCiNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenGt) {
		pos := p.token.pos
		n := &greaterNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenGtCi) {
		pos := p.token.pos
		n := &greaterCiNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenGtEq) {
		pos := p.token.pos
		n := &gequalNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenGtEqCi) {
		pos := p.token.pos
		n := &gequalCiNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenLt) {
		pos := p.token.pos
		n := &smallerNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenLtCi) {
		pos := p.token.pos
		n := &smallerCiNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenLtEq) {
		pos := p.token.pos
		n := &sequalNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenLtEqCi) {
		pos := p.token.pos
		n := &sequalCiNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenMatch) {
		pos := p.token.pos
		n := &matchNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenMatchCi) {
		pos := p.token.pos
		n := &matchCiNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenNoMatch) {
		pos := p.token.pos
		n := &noMatchNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenNoMatchCi) {
		pos := p.token.pos
		n := &noMatchCiNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenIs) {
		pos := p.token.pos
		n := &isNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenIsCi) {
		pos := p.token.pos
		n := &isCiNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenIsNot) {
		pos := p.token.pos
		n := &isNotNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	} else if p.accept(tokenIsNotCi) {
		pos := p.token.pos
		n := &isNotCiNode{left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		n.right = right
		left = node.NewPosNode(pos, n)
	}
	return left, true
}

type addNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *addNode) Clone() node.Node {
	return &addNode{n.left.Clone(), n.right.Clone()}
}

func (n *addNode) TerminalNode() node.Node {
	return n
}

func (n *addNode) Position() *node.Pos {
	return nil
}

func (n *addNode) IsExpr() bool {
	return true
}

func (n *addNode) Left() node.Node {
	return n.left
}

func (n *addNode) Right() node.Node {
	return n.right
}

type subtractNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *subtractNode) Clone() node.Node {
	return &subtractNode{n.left.Clone(), n.right.Clone()}
}

func (n *subtractNode) TerminalNode() node.Node {
	return n
}

func (n *subtractNode) Position() *node.Pos {
	return nil
}

func (n *subtractNode) IsExpr() bool {
	return true
}

func (n *subtractNode) Left() node.Node {
	return n.left
}

func (n *subtractNode) Right() node.Node {
	return n.right
}

// expr5 := expr6 1*( "+" *blank expr6 ) /
//          expr6 1*( "-" *blank expr6 ) /
//          expr6
func (p *parser) acceptExpr5() (expr, bool) {
	left, ok := p.acceptExpr6()
	if !ok {
		return nil, false
	}
	for {
		if p.accept(tokenPlus) {
			pos := p.token.pos
			n := &addNode{left, nil}
			p.acceptBlanks()
			right, ok := p.acceptExpr6()
			if !ok {
				return nil, false
			}
			n.right = right
			left = node.NewPosNode(pos, n)
		} else if p.accept(tokenMinus) {
			pos := p.token.pos
			n := &subtractNode{left, nil}
			p.acceptBlanks()
			right, ok := p.acceptExpr6()
			if !ok {
				return nil, false
			}
			n.right = right
			left = node.NewPosNode(pos, n)
		} else {
			break
		}
	}
	return left, true
}

type multiplyNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *multiplyNode) Clone() node.Node {
	return &multiplyNode{n.left.Clone(), n.right.Clone()}
}

func (n *multiplyNode) TerminalNode() node.Node {
	return n
}

func (n *multiplyNode) Position() *node.Pos {
	return nil
}

func (n *multiplyNode) IsExpr() bool {
	return true
}

func (n *multiplyNode) Left() node.Node {
	return n.left
}

func (n *multiplyNode) Right() node.Node {
	return n.right
}

type divideNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *divideNode) Clone() node.Node {
	return &divideNode{n.left.Clone(), n.right.Clone()}
}

func (n *divideNode) TerminalNode() node.Node {
	return n
}

func (n *divideNode) Position() *node.Pos {
	return nil
}

func (n *divideNode) IsExpr() bool {
	return true
}

func (n *divideNode) Left() node.Node {
	return n.left
}

func (n *divideNode) Right() node.Node {
	return n.right
}

type remainderNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *remainderNode) Clone() node.Node {
	return &remainderNode{n.left.Clone(), n.right.Clone()}
}

func (n *remainderNode) TerminalNode() node.Node {
	return n
}

func (n *remainderNode) Position() *node.Pos {
	return nil
}

func (n *remainderNode) IsExpr() bool {
	return true
}

func (n *remainderNode) Left() node.Node {
	return n.left
}

func (n *remainderNode) Right() node.Node {
	return n.right
}

// expr6 := expr7 1*( "*" *blank expr7 ) /
//          expr7 1*( "/" *blank expr7 ) /
//          expr7 1*( "%" *blank expr7 ) /
//          expr7
func (p *parser) acceptExpr6() (expr, bool) {
	left, ok := p.acceptExpr7()
	if !ok {
		return nil, false
	}
	for {
		if p.accept(tokenStar) {
			pos := p.token.pos
			n := &multiplyNode{left, nil}
			p.acceptBlanks()
			right, ok := p.acceptExpr7()
			if !ok {
				return nil, false
			}
			n.right = right
			left = node.NewPosNode(pos, n)
		} else if p.accept(tokenSlash) {
			pos := p.token.pos
			n := &divideNode{left, nil}
			p.acceptBlanks()
			right, ok := p.acceptExpr7()
			if !ok {
				return nil, false
			}
			n.right = right
			left = node.NewPosNode(pos, n)
		} else if p.accept(tokenPercent) {
			pos := p.token.pos
			n := &remainderNode{left, nil}
			p.acceptBlanks()
			right, ok := p.acceptExpr7()
			if !ok {
				return nil, false
			}
			n.right = right
			left = node.NewPosNode(pos, n)
		} else {
			break
		}
	}
	return left, true
}

type unaryOpNode interface {
	Value() node.Node
}

type notNode struct {
	left expr
}

// Clone clones itself.
func (n *notNode) Clone() node.Node {
	return &notNode{n.left.Clone()}
}

func (n *notNode) TerminalNode() node.Node {
	return n
}

func (n *notNode) Position() *node.Pos {
	return nil
}

func (n *notNode) IsExpr() bool {
	return true
}

func (n *notNode) Value() node.Node {
	return n.left
}

type minusNode struct {
	left expr
}

// Clone clones itself.
func (n *minusNode) Clone() node.Node {
	return &minusNode{n.left.Clone()}
}

func (n *minusNode) TerminalNode() node.Node {
	return n
}

func (n *minusNode) Position() *node.Pos {
	return nil
}

func (n *minusNode) IsExpr() bool {
	return true
}

func (n *minusNode) Value() node.Node {
	return n.left
}

type plusNode struct {
	left expr
}

// Clone clones itself.
func (n *plusNode) Clone() node.Node {
	return &plusNode{n.left.Clone()}
}

func (n *plusNode) TerminalNode() node.Node {
	return n
}

func (n *plusNode) Position() *node.Pos {
	return nil
}

func (n *plusNode) IsExpr() bool {
	return true
}

func (n *plusNode) Value() node.Node {
	return n.left
}

// expr7 := "!" expr7 /
//          "-" expr7 /
//          "+" expr7 /
//          expr8
func (p *parser) acceptExpr7() (expr, bool) {
	if p.accept(tokenNot) {
		left, ok := p.acceptExpr7()
		if !ok {
			return nil, false
		}
		n := node.NewPosNode(p.token.pos, &notNode{left})
		return n, true
	} else if p.accept(tokenMinus) {
		left, ok := p.acceptExpr7()
		if !ok {
			return nil, false
		}
		n := node.NewPosNode(p.token.pos, &minusNode{left})
		return n, true
	} else if p.accept(tokenPlus) {
		left, ok := p.acceptExpr7()
		if !ok {
			return nil, false
		}
		n := node.NewPosNode(p.token.pos, &plusNode{left})
		return n, true
	} else {
		n, ok := p.acceptExpr8()
		if !ok {
			return nil, false
		}
		return n, true
	}
}

type sliceNode struct {
	left  expr
	rlist []expr
}

// Clone clones itself.
func (n *sliceNode) Clone() node.Node {
	rlist := make([]expr, len(n.rlist))
	for i := range n.rlist {
		if n.rlist[i] != nil {
			rlist[i] = n.rlist[i].Clone()
		} else {
			rlist[i] = nil
		}
	}
	return &sliceNode{n.left.Clone(), rlist}
}

func (n *sliceNode) TerminalNode() node.Node {
	return n
}

func (n *sliceNode) Position() *node.Pos {
	return nil
}

func (n *sliceNode) IsExpr() bool {
	return true
}

type callNode struct {
	left  expr
	rlist []expr
}

// Clone clones itself.
func (n *callNode) Clone() node.Node {
	rlist := make([]expr, len(n.rlist))
	for i := range n.rlist {
		rlist[i] = n.rlist[i].Clone()
	}
	return &callNode{n.left.Clone(), rlist}
}

func (n *callNode) TerminalNode() node.Node {
	return n
}

func (n *callNode) Position() *node.Pos {
	return nil
}

func (n *callNode) IsExpr() bool {
	return true
}

type subscriptNode struct {
	left  expr
	right expr
}

// Clone clones itself.
func (n *subscriptNode) Clone() node.Node {
	return &subscriptNode{n.left.Clone(), n.right.Clone()}
}

func (n *subscriptNode) TerminalNode() node.Node {
	return n
}

func (n *subscriptNode) Position() *node.Pos {
	return nil
}

func (n *subscriptNode) IsExpr() bool {
	return true
}

func (n *subscriptNode) Left() node.Node {
	return n.left
}

func (n *subscriptNode) Right() node.Node {
	return n.right
}

type dotNode struct {
	left  expr
	right node.Node
}

// Clone clones itself.
func (n *dotNode) Clone() node.Node {
	return &dotNode{n.left.Clone(), n.right.Clone()}
}

func (n *dotNode) TerminalNode() node.Node {
	return n
}

func (n *dotNode) Position() *node.Pos {
	return nil
}

func (n *dotNode) IsExpr() bool {
	return true
}

func (n *dotNode) Left() node.Node {
	return n.left
}

func (n *dotNode) Right() node.Node {
	return n.right
}

type identifierNode struct {
	value string
}

// Clone clones itself.
func (n *identifierNode) Clone() node.Node {
	return &identifierNode{n.value}
}

func (n *identifierNode) TerminalNode() node.Node {
	return n
}

func (n *identifierNode) Position() *node.Pos {
	return nil
}

func (n *identifierNode) IsExpr() bool {
	return true
}

// expr8 := expr9 1*( "[" *blank expr1 *blank "]" ) /
//          expr9 1*( "[" *blank [ expr1 *blank ] ":" *blank [ expr1 *blank ] "]" ) /
//          expr9 1*( "." *blank identifier ) /
//          expr9 1*( "(" *blank [ expr1 *blank *( "," *blank expr1 *blank) [ "," ] ] *blank ")" ) /
//          expr9
func (p *parser) acceptExpr8() (expr, bool) {
	left, ok := p.acceptExpr9()
	if !ok {
		return nil, false
	}
	for {
		if p.accept(tokenSqOpen) {
			npos := p.token.pos
			p.acceptBlanks()
			if p.accept(tokenColon) {
				n := &sliceNode{left, []expr{nil, nil}}
				p.acceptBlanks()
				if p.peek().typ != tokenSqClose {
					expr, ok := p.acceptExpr1()
					if !ok {
						return nil, false
					}
					n.rlist[1] = expr
					p.acceptBlanks()
				}
				if !p.accept(tokenSqClose) {
					p.errorf("expected %s but got %s", tokenName(tokenSqClose), tokenName(p.peek().typ))
					return nil, false
				}
				left = node.NewPosNode(npos, n)
			} else {
				right, ok := p.acceptExpr1()
				if !ok {
					return nil, false
				}
				p.acceptBlanks()
				if p.accept(tokenColon) {
					n := &sliceNode{left, []expr{right, nil}}
					p.acceptBlanks()
					if p.peek().typ != tokenSqClose {
						expr, ok := p.acceptExpr1()
						if !ok {
							return nil, false
						}
						n.rlist[1] = expr
						p.acceptBlanks()
					}
					if !p.accept(tokenSqClose) {
						p.errorf("expected %s but got %s", tokenName(tokenSqClose), tokenName(p.peek().typ))
						return nil, false
					}
					left = node.NewPosNode(npos, n)
				} else {
					n := &subscriptNode{left, right}
					p.acceptBlanks()
					if !p.accept(tokenSqClose) {
						p.errorf("expected %s but got %s", tokenName(tokenSqClose), tokenName(p.peek().typ))
						return nil, false
					}
					left = node.NewPosNode(npos, n)
				}
			}
		} else if p.accept(tokenPOpen) {
			pos := p.token.pos
			n := &callNode{left, make([]expr, 0, 8)}
			p.acceptBlanks()
			if !p.accept(tokenPClose) {
				for {
					arg, ok := p.acceptExpr1()
					if !ok {
						return nil, false
					}
					n.rlist = append(n.rlist, arg)
					p.acceptBlanks()
					if p.accept(tokenComma) {
						p.acceptBlanks()
						if p.accept(tokenPClose) {
							break
						}
					} else if p.accept(tokenPClose) {
						break
					} else {
						p.errorf("expected %s or %s but got %s",
							tokenName(tokenComma), tokenName(tokenPClose), tokenName(p.peek().typ))
						return nil, false
					}
				}
			}
			left = node.NewPosNode(pos, n)
		} else if p.accept(tokenDot) {
			dot := p.token
			p.acceptBlanks()
			if !p.accept(tokenIdentifier) {
				p.errorf("expected %s but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ))
				return nil, false
			}
			right := node.NewPosNode(p.token.pos, &identifierNode{p.token.val})
			left = node.NewPosNode(dot.pos, &dotNode{left, right})
		} else {
			break
		}
	}
	return left, true
}

type literalNode interface {
	node.Node
	Value() string
}

type intNode struct {
	value string
}

// Clone clones itself.
func (n *intNode) Clone() node.Node {
	return &intNode{n.value}
}

func (n *intNode) TerminalNode() node.Node {
	return n
}

func (n *intNode) Position() *node.Pos {
	return nil
}

func (n *intNode) IsExpr() bool {
	return true
}

type floatNode struct {
	value string
}

// Clone clones itself.
func (n *floatNode) Clone() node.Node {
	return &floatNode{n.value}
}

func (n *floatNode) TerminalNode() node.Node {
	return n
}

func (n *floatNode) Position() *node.Pos {
	return nil
}

func (n *floatNode) IsExpr() bool {
	return true
}

type stringNode struct {
	value vainString
}

// Clone clones itself.
func (n *stringNode) Clone() node.Node {
	return &stringNode{n.value}
}

func (n *stringNode) TerminalNode() node.Node {
	return n
}

func (n *stringNode) Position() *node.Pos {
	return nil
}

func (n *stringNode) IsExpr() bool {
	return true
}

type listNode struct {
	value []expr
}

// Clone clones itself.
func (n *listNode) Clone() node.Node {
	value := make([]expr, len(n.value))
	for i := range n.value {
		value[i] = n.value[i].Clone()
	}
	return &listNode{value}
}

func (n *listNode) TerminalNode() node.Node {
	return n
}

func (n *listNode) Position() *node.Pos {
	return nil
}

func (n *listNode) IsExpr() bool {
	return true
}

type dictionaryNode struct {
	value [][]expr
}

// Clone clones itself.
func (n *dictionaryNode) Clone() node.Node {
	value := make([][]expr, len(n.value))
	for i := range n.value {
		kv := make([]expr, len(n.value[i]))
		for j := range n.value[i] {
			kv[j] = n.value[i][j].Clone()
		}
		value[i] = kv
	}
	return &dictionaryNode{value}
}

func (n *dictionaryNode) TerminalNode() node.Node {
	return n
}

func (n *dictionaryNode) Position() *node.Pos {
	return nil
}

func (n *dictionaryNode) IsExpr() bool {
	return true
}

type optionNode struct {
	value string
}

// Clone clones itself.
func (n *optionNode) Clone() node.Node {
	return &optionNode{n.value}
}

func (n *optionNode) TerminalNode() node.Node {
	return n
}

func (n *optionNode) Position() *node.Pos {
	return nil
}

func (n *optionNode) IsExpr() bool {
	return true
}

func (n *optionNode) Value() string {
	return n.value[1:]
}

type envNode struct {
	value string
}

// Clone clones itself.
func (n *envNode) Clone() node.Node {
	return &envNode{n.value}
}

func (n *envNode) TerminalNode() node.Node {
	return n
}

func (n *envNode) Position() *node.Pos {
	return nil
}

func (n *envNode) IsExpr() bool {
	return true
}

func (n *envNode) Value() string {
	return n.value[1:]
}

type regNode struct {
	value string
}

// Clone clones itself.
func (n *regNode) Clone() node.Node {
	return &regNode{n.value}
}

func (n *regNode) TerminalNode() node.Node {
	return n
}

func (n *regNode) Position() *node.Pos {
	return nil
}

func (n *regNode) IsExpr() bool {
	return true
}

func (n *regNode) Value() string {
	return n.value[1:]
}

// expr9: number /
//        (string ABNF is too complex! e.g. "string\n", 'str''ing') /
//        "[" *blank *( expr1 *blank "," *blank ) "]" /
//        "{" *blank *( ( identifierLike | expr1 ) *blank ":" *blank expr1 *blank "," *blank ) "}" /
//        &option /
//        "(" *blank expr1 *blank ")" /
//        function /
//        identifier /
//        $VAR /
//        @r
func (p *parser) acceptExpr9() (expr, bool) {
	if p.accept(tokenInt) {
		n := node.NewPosNode(p.token.pos, &intNode{p.token.val})
		return n, true
	} else if p.accept(tokenFloat) {
		n := node.NewPosNode(p.token.pos, &floatNode{p.token.val})
		return n, true
	} else if p.accept(tokenString) {
		n := node.NewPosNode(p.token.pos, &stringNode{vainString(p.token.val)})
		return n, true
	} else if p.accept(tokenSqOpen) {
		n := &listNode{make([]expr, 0, 16)}
		p.acceptBlanks()
		if !p.accept(tokenSqClose) {
			for {
				expr, ok := p.acceptExpr1()
				if !ok {
					return nil, false
				}
				n.value = append(n.value, expr)
				p.acceptBlanks()
				if p.accept(tokenComma) {
					p.acceptBlanks()
					if p.accept(tokenSqClose) {
						break
					}
				} else if p.accept(tokenSqClose) {
					break
				} else {
					p.errorf("expected %s or %s but got %s",
						tokenName(tokenComma), tokenName(tokenSqClose), tokenName(p.peek().typ))
					return nil, false
				}
			}
		}
		return node.NewPosNode(p.token.pos, n), true
	} else if p.accept(tokenCOpen) {
		npos := p.token.pos
		var m [][]expr
		p.acceptBlanks()
		if !p.accept(tokenCClose) {
			m = make([][]expr, 0, 16)
			for {
				pair := []expr{nil, nil}
				p.acceptBlanks()
				t1 := p.next()
				p.acceptBlanks()
				t2 := p.next()
				if p.canBeIdentifier(t1) && t2.typ == tokenColon {
					pair[0] = node.NewPosNode(t1.pos, &identifierNode{t1.val})
					p.acceptBlanks()
					right, ok := p.acceptExpr1()
					if !ok {
						return nil, false
					}
					pair[1] = right
				} else {
					p.backup(t2)
					p.backup(t1)
					left, ok := p.acceptExpr1()
					if !ok {
						return nil, false
					}
					p.acceptBlanks()
					if !p.accept(tokenColon) {
						p.errorf("expected %s but got %s", tokenName(tokenColon), tokenName(p.peek().typ))
						return nil, false
					}
					p.acceptBlanks()
					right, ok := p.acceptExpr1()
					if !ok {
						return nil, false
					}
					pair[0] = left
					pair[1] = right
				}
				m = append(m, pair)
				p.acceptBlanks()
				if p.accept(tokenComma) {
					p.acceptBlanks()
					if p.accept(tokenSqClose) {
						break
					}
				} else if p.accept(tokenCClose) {
					break
				}
			}
		}
		n := node.NewPosNode(npos, &dictionaryNode{m})
		return n, true
	} else if p.accept(tokenPOpen) {
		p.acceptBlanks()
		n, ok := p.acceptExpr1()
		if !ok {
			return nil, false
		}
		p.acceptBlanks()
		if !p.accept(tokenPClose) {
			p.errorf("expected %s but got %s", tokenName(tokenPClose), tokenName(p.peek().typ))
			return nil, false
		}
		return n, true
	} else if p.accept(tokenFunc) {
		p.backup(p.token)
		return p.acceptFunction(true)
	} else if p.accept(tokenOption) {
		n := node.NewPosNode(p.token.pos, &optionNode{p.token.val})
		return n, true
	} else if p.accept(tokenIdentifier) {
		n := node.NewPosNode(p.token.pos, &identifierNode{p.token.val})
		return n, true
	} else if p.accept(tokenEnv) {
		n := node.NewPosNode(p.token.pos, &envNode{p.token.val})
		return n, true
	} else if p.accept(tokenReg) {
		n := node.NewPosNode(p.token.pos, &regNode{p.token.val})
		return n, true
	}
	p.errorf("expected expression but got %s", tokenName(p.peek().typ))
	return nil, false
}

// identifierLike := identifier | <value matches with '^\h\w*$'>
func (p *parser) canBeIdentifier(t *token) bool {
	if t.typ == tokenIdentifier {
		return true
	}
	if len(t.val) == 0 {
		return false
	}
	val := []rune(t.val)
	if !isWordHead(val[0]) {
		return false
	}
	for _, r := range val[1:] {
		if !isAlphaNumeric(r) {
			return false
		}
	}
	return true
}
