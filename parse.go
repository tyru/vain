package main

import (
	"errors"
	"fmt"
)

func parse(name string, inTokens <-chan token) *parser {
	return &parser{
		name:     name,
		inTokens: inTokens,
		outNodes: make(chan node, 1),
	}
}

func (p *parser) Nodes() <-chan node {
	return p.outNodes
}

type parser struct {
	name     string
	inTokens <-chan token
	outNodes chan node
	token    *token  // next() sets read token to this.
	tokbuf   []token // next() doesn't read from inTokens if len(tokbuf) > 0 .
}

type node interface {
	Node() node
	Position() *Pos
	IsExpr() bool
}

type expr interface {
	node
}

type statement interface {
	node
}

// errorNode holds an error and node position.
// errorNode is used also instead of error type.
// Because it's a bother to use both 'node' and 'error' variables
// for representing parse error of a node.
type errorNode struct {
	*Pos
	err error
}

func (node *errorNode) Node() node {
	return node
}

func (node *errorNode) IsExpr() bool {
	return false
}

func (p *parser) Run() {
	if toplevel, ok := p.acceptTopLevel(); ok {
		toplevel.Pos.offset = 0 // first read node's position is -1. adjust it
		p.emit(toplevel)
	}
	close(p.outNodes) // No more nodes will be delivered.
}

// emit passes an node back to the client.
func (p *parser) emit(node node) {
	p.outNodes <- node
}

// errorf returns an error token and terminates the scan
func (p *parser) errorf(format string, args ...interface{}) {
	newargs := make([]interface{}, 0, len(args)+2)
	newargs = append(newargs, p.name, p.token.pos.line, p.token.pos.col+1)
	newargs = append(newargs, args...)
	err := fmt.Errorf("[parse] %s:%d:%d: "+format, newargs...)
	p.emit(&errorNode{p.token.pos, err})
}

// lexError is called when tokenError was given.
func (p *parser) lexError() {
	p.emit(&errorNode{p.token.pos, errors.New(p.token.val)})
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
	*Pos
	body []node
}

func (node *topLevelNode) Node() node {
	return node
}

func (node *topLevelNode) IsExpr() bool {
	return false
}

func (p *parser) acceptTopLevel() (*topLevelNode, bool) {
	pos := &Pos{0, 1, 0}
	toplevel := &topLevelNode{pos, make([]node, 0, 32)}
	for {
		node, ok := p.acceptStmtOrExpr()
		if !ok {
			return toplevel, true
		}
		toplevel.body = append(toplevel.body, node)
	}
}

type commentNode struct {
	*Pos
	value string
}

func (node *commentNode) Node() node {
	return node
}

func (node *commentNode) IsExpr() bool {
	return false
}

func (node *commentNode) Value() string {
	return node.value[1:]
}

// statementOrExpression := *LF ( comment | statement | expr )
func (p *parser) acceptStmtOrExpr() (node, bool) {
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
		node := &commentNode{p.token.pos, p.token.val}
		return node, true
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
	*Pos
	left          node
	right         expr
	hasUnderscore bool
}

func (node *constStatement) Node() node {
	return node
}

func (node *constStatement) IsExpr() bool {
	return false
}

// constStatement := "const" assignLhs "=" expr
func (p *parser) acceptConstStatement() (node, bool) {
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
	node := &constStatement{pos, left, right, hasUnderscore}
	return node, true
}

// assignLhs := identifier | destructuringAssignment
func (p *parser) acceptAssignLHS() (node, bool, bool) {
	var left node
	var hasUnderscore bool
	if p.accept(tokenIdentifier) {
		left = &identifierNode{p.token.pos, p.token.val}
	} else if ids, underscore, listpos, ok := p.acceptDestructuringAssignment(); ok {
		left = &listNode{listpos, ids}
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
func (p *parser) acceptDestructuringAssignment() ([]expr, bool, *Pos, bool) {
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
		ids = append(ids, &identifierNode{p.token.pos, p.token.val})
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
	*Pos
	left expr
}

func (node *returnStatement) Node() node {
	return node
}

func (node *returnStatement) IsExpr() bool {
	return false
}

// returnStatement := "return" ( expr | LF )
// and if tokenCClose is detected instead of expr,
// it must be empty return statement inside block.
func (p *parser) acceptReturnStatement() (node, bool) {
	if !p.accept(tokenReturn) {
		p.errorf("expected %s but got %s", tokenName(tokenReturn), tokenName(p.peek().typ))
		return nil, false
	}
	ret := p.token
	if p.accept(tokenNewline) {
		return &returnStatement{ret.pos, nil}, true
	}
	if p.accept(tokenCClose) { // end of block
		p.backup(p.token)
		return &returnStatement{ret.pos, nil}, true
	}
	expr, ok := p.acceptExpr()
	if !ok {
		return nil, false
	}
	return &returnStatement{ret.pos, expr}, true
}

type ifStatement struct {
	*Pos
	cond expr
	body []node
	els  []node
}

func (node *ifStatement) Node() node {
	return node
}

func (node *ifStatement) IsExpr() bool {
	return false
}

// ifStatement := "if" *blank expr *blank block
//                [ *blank "else" *blank ( ifStatement | block ) ]
func (p *parser) acceptIfStatement() (node, bool) {
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
	var els []node
	p.acceptBlanks()
	if p.accept(tokenElse) {
		p.acceptBlanks()
		if p.accept(tokenIf) {
			p.backup(p.token)
			ifstmt, ok := p.acceptIfStatement()
			if !ok {
				return nil, false
			}
			els = []node{ifstmt}
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
	node := &ifStatement{pos, cond, body, els}
	return node, true
}

type whileStatement struct {
	*Pos
	cond expr
	body []node
}

func (node *whileStatement) Node() node {
	return node
}

func (node *whileStatement) IsExpr() bool {
	return false
}

// whileStatement := "while" *blank expr *blank block
func (p *parser) acceptWhileStatement() (node, bool) {
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
	node := &whileStatement{pos, cond, body}
	return node, true
}

type forStatement struct {
	*Pos
	left          node
	right         expr
	body          []node
	hasUnderscore bool
}

func (node *forStatement) Node() node {
	return node
}

func (node *forStatement) IsExpr() bool {
	return false
}

// forStatement := "for" *blank assignLhs *blank "in" *blank expr *blank block
func (p *parser) acceptForStatement() (node, bool) {
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
	node := &forStatement{pos, left, right, body, hasUnderscore}
	return node, true
}

// block := "{" *blank *( statementOrExpression *blank ) "}"
func (p *parser) acceptBlock() ([]node, bool) {
	if !p.accept(tokenCOpen) {
		p.errorf("expected %s but got %s", tokenName(tokenCOpen), tokenName(p.peek().typ))
		return nil, false
	}
	var nodes []node
	p.acceptBlanks()
	if !p.accept(tokenCClose) {
		nodes = make([]node, 0, 16)
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
	*Pos
	pkg      vainString
	pkgAlias string
	fnlist   [][]string
}

func (node *importStatement) Node() node {
	return node
}

func (node *importStatement) IsExpr() bool {
	return false
}

// importStatement := "import" string [ "as" *blank identifier ] |
//                    "from" string "import" <importFunctionList>
func (p *parser) acceptImportStatement() (*importStatement, bool) {
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
		stmt := &importStatement{pos, pkg, pkgAlias, nil}
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
		stmt := &importStatement{pos, pkg, "", fnlist}
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
	*Pos
	isExpr     bool
	mods       []string
	name       string
	args       []argument
	retType    string
	bodyIsStmt bool
	body       []node
}

func (node *funcStmtOrExpr) Node() node {
	return node
}

func (node *funcStmtOrExpr) IsExpr() bool {
	return node.isExpr
}

// function :=
//        "func" [ functionModifierList ] [ identifier ] functionCallSignature expr1 /
//        "func" [ functionModifierList ] [ identifier ] functionCallSignature block
func (p *parser) acceptFunction(isExpr bool) (*funcStmtOrExpr, bool) {
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
	var body []node

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
		body = []node{expr}
	}

	f := &funcStmtOrExpr{pos, isExpr, mods, name, args, retType, bodyIsStmt, body}
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
	*Pos
	cond  expr
	left  expr
	right expr
}

func (node *ternaryNode) Node() node {
	return node
}

func (node *ternaryNode) IsExpr() bool {
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
		left = &ternaryNode{p.token.pos, left, expr, right}
	}
	return left, true
}

type binaryOpNode interface {
	Left() node
	Right() node
}

type orNode struct {
	*Pos
	left  expr
	right expr
}

func (node *orNode) Node() node {
	return node
}

func (node *orNode) IsExpr() bool {
	return true
}

func (node *orNode) Left() node {
	return node.left
}

func (node *orNode) Right() node {
	return node.right
}

// expr2 := expr3 *( "||" *blank expr3 )
func (p *parser) acceptExpr2() (expr, bool) {
	left, ok := p.acceptExpr3()
	if !ok {
		return nil, false
	}
	for {
		if p.accept(tokenOrOr) {
			node := &orNode{p.token.pos, left, nil}
			p.acceptBlanks()
			right, ok := p.acceptExpr3()
			if !ok {
				return nil, false
			}
			node.right = right
			left = node
		} else {
			break
		}
	}
	return left, true
}

type andNode struct {
	*Pos
	left  expr
	right expr
}

func (node *andNode) Node() node {
	return node
}

func (node *andNode) IsExpr() bool {
	return true
}

func (node *andNode) Left() node {
	return node.left
}

func (node *andNode) Right() node {
	return node.right
}

// expr3 := expr4 *( "&&" *blank expr4 )
func (p *parser) acceptExpr3() (expr, bool) {
	left, ok := p.acceptExpr4()
	if !ok {
		return nil, false
	}
	for {
		if p.accept(tokenAndAnd) {
			node := &andNode{p.token.pos, left, nil}
			p.acceptBlanks()
			right, ok := p.acceptExpr4()
			if !ok {
				return nil, false
			}
			node.right = right
			left = node
		} else {
			break
		}
	}
	return left, true
}

type equalNode struct {
	*Pos
	left  expr
	right expr
}

func (node *equalNode) Node() node {
	return node
}

func (node *equalNode) IsExpr() bool {
	return true
}

func (node *equalNode) Left() node {
	return node.left
}

func (node *equalNode) Right() node {
	return node.right
}

type equalCiNode struct {
	*Pos
	left  expr
	right expr
}

func (node *equalCiNode) Node() node {
	return node
}

func (node *equalCiNode) IsExpr() bool {
	return true
}

func (node *equalCiNode) Left() node {
	return node.left
}

func (node *equalCiNode) Right() node {
	return node.right
}

type nequalNode struct {
	*Pos
	left  expr
	right expr
}

func (node *nequalNode) Node() node {
	return node
}

func (node *nequalNode) IsExpr() bool {
	return true
}

func (node *nequalNode) Left() node {
	return node.left
}

func (node *nequalNode) Right() node {
	return node.right
}

type nequalCiNode struct {
	*Pos
	left  expr
	right expr
}

func (node *nequalCiNode) Node() node {
	return node
}

func (node *nequalCiNode) IsExpr() bool {
	return true
}

func (node *nequalCiNode) Left() node {
	return node.left
}

func (node *nequalCiNode) Right() node {
	return node.right
}

type greaterNode struct {
	*Pos
	left  expr
	right expr
}

func (node *greaterNode) Node() node {
	return node
}

func (node *greaterNode) IsExpr() bool {
	return true
}

func (node *greaterNode) Left() node {
	return node.left
}

func (node *greaterNode) Right() node {
	return node.right
}

type greaterCiNode struct {
	*Pos
	left  expr
	right expr
}

func (node *greaterCiNode) Node() node {
	return node
}

func (node *greaterCiNode) IsExpr() bool {
	return true
}

func (node *greaterCiNode) Left() node {
	return node.left
}

func (node *greaterCiNode) Right() node {
	return node.right
}

type gequalNode struct {
	*Pos
	left  expr
	right expr
}

func (node *gequalNode) Node() node {
	return node
}

func (node *gequalNode) IsExpr() bool {
	return true
}

func (node *gequalNode) Left() node {
	return node.left
}

func (node *gequalNode) Right() node {
	return node.right
}

type gequalCiNode struct {
	*Pos
	left  expr
	right expr
}

func (node *gequalCiNode) Node() node {
	return node
}

func (node *gequalCiNode) IsExpr() bool {
	return true
}

func (node *gequalCiNode) Left() node {
	return node.left
}

func (node *gequalCiNode) Right() node {
	return node.right
}

type smallerNode struct {
	*Pos
	left  expr
	right expr
}

func (node *smallerNode) Node() node {
	return node
}

func (node *smallerNode) IsExpr() bool {
	return true
}

func (node *smallerNode) Left() node {
	return node.left
}

func (node *smallerNode) Right() node {
	return node.right
}

type smallerCiNode struct {
	*Pos
	left  expr
	right expr
}

func (node *smallerCiNode) Node() node {
	return node
}

func (node *smallerCiNode) IsExpr() bool {
	return true
}

func (node *smallerCiNode) Left() node {
	return node.left
}

func (node *smallerCiNode) Right() node {
	return node.right
}

type sequalNode struct {
	*Pos
	left  expr
	right expr
}

func (node *sequalNode) Node() node {
	return node
}

func (node *sequalNode) IsExpr() bool {
	return true
}

func (node *sequalNode) Left() node {
	return node.left
}

func (node *sequalNode) Right() node {
	return node.right
}

type sequalCiNode struct {
	*Pos
	left  expr
	right expr
}

func (node *sequalCiNode) Node() node {
	return node
}

func (node *sequalCiNode) IsExpr() bool {
	return true
}

func (node *sequalCiNode) Left() node {
	return node.left
}

func (node *sequalCiNode) Right() node {
	return node.right
}

type matchNode struct {
	*Pos
	left  expr
	right expr
}

func (node *matchNode) Node() node {
	return node
}

func (node *matchNode) IsExpr() bool {
	return true
}

func (node *matchNode) Left() node {
	return node.left
}

func (node *matchNode) Right() node {
	return node.right
}

type matchCiNode struct {
	*Pos
	left  expr
	right expr
}

func (node *matchCiNode) Node() node {
	return node
}

func (node *matchCiNode) IsExpr() bool {
	return true
}

func (node *matchCiNode) Left() node {
	return node.left
}

func (node *matchCiNode) Right() node {
	return node.right
}

type noMatchNode struct {
	*Pos
	left  expr
	right expr
}

func (node *noMatchNode) Node() node {
	return node
}

func (node *noMatchNode) IsExpr() bool {
	return true
}

func (node *noMatchNode) Left() node {
	return node.left
}

func (node *noMatchNode) Right() node {
	return node.right
}

type noMatchCiNode struct {
	*Pos
	left  expr
	right expr
}

func (node *noMatchCiNode) Node() node {
	return node
}

func (node *noMatchCiNode) IsExpr() bool {
	return true
}

func (node *noMatchCiNode) Left() node {
	return node.left
}

func (node *noMatchCiNode) Right() node {
	return node.right
}

type isNode struct {
	*Pos
	left  expr
	right expr
}

func (node *isNode) Node() node {
	return node
}

func (node *isNode) IsExpr() bool {
	return true
}

func (node *isNode) Left() node {
	return node.left
}

func (node *isNode) Right() node {
	return node.right
}

type isCiNode struct {
	*Pos
	left  expr
	right expr
}

func (node *isCiNode) Node() node {
	return node
}

func (node *isCiNode) IsExpr() bool {
	return true
}

func (node *isCiNode) Left() node {
	return node.left
}

func (node *isCiNode) Right() node {
	return node.right
}

type isNotNode struct {
	*Pos
	left  expr
	right expr
}

func (node *isNotNode) Node() node {
	return node
}

func (node *isNotNode) IsExpr() bool {
	return true
}

func (node *isNotNode) Left() node {
	return node.left
}

func (node *isNotNode) Right() node {
	return node.right
}

type isNotCiNode struct {
	*Pos
	left  expr
	right expr
}

func (node *isNotCiNode) Node() node {
	return node
}

func (node *isNotCiNode) IsExpr() bool {
	return true
}

func (node *isNotCiNode) Left() node {
	return node.left
}

func (node *isNotCiNode) Right() node {
	return node.right
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
		node := &equalNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenEqEqCi) {
		node := &equalCiNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenNeq) {
		node := &nequalNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenNeqCi) {
		node := &nequalCiNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenGt) {
		node := &greaterNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenGtCi) {
		node := &greaterCiNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenGtEq) {
		node := &gequalNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenGtEqCi) {
		node := &gequalCiNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenLt) {
		node := &smallerNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenLtCi) {
		node := &smallerCiNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenLtEq) {
		node := &sequalNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenLtEqCi) {
		node := &sequalCiNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenMatch) {
		node := &matchNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenMatchCi) {
		node := &matchCiNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenNoMatch) {
		node := &noMatchNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenNoMatchCi) {
		node := &noMatchCiNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenIs) {
		node := &isNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenIsCi) {
		node := &isCiNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenIsNot) {
		node := &isNotNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenIsNotCi) {
		node := &isNotCiNode{p.token.pos, left, nil}
		p.acceptBlanks()
		right, ok := p.acceptExpr5()
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	}
	return left, true
}

type addNode struct {
	*Pos
	left  expr
	right expr
}

func (node *addNode) Node() node {
	return node
}

func (node *addNode) IsExpr() bool {
	return true
}

func (node *addNode) Left() node {
	return node.left
}

func (node *addNode) Right() node {
	return node.right
}

type subtractNode struct {
	*Pos
	left  expr
	right expr
}

func (node *subtractNode) Node() node {
	return node
}

func (node *subtractNode) IsExpr() bool {
	return true
}

func (node *subtractNode) Left() node {
	return node.left
}

func (node *subtractNode) Right() node {
	return node.right
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
			node := &addNode{p.token.pos, left, nil}
			p.acceptBlanks()
			right, ok := p.acceptExpr6()
			if !ok {
				return nil, false
			}
			node.right = right
			left = node
		} else if p.accept(tokenMinus) {
			node := &subtractNode{p.token.pos, left, nil}
			p.acceptBlanks()
			right, ok := p.acceptExpr6()
			if !ok {
				return nil, false
			}
			node.right = right
			left = node
		} else {
			break
		}
	}
	return left, true
}

type multiplyNode struct {
	*Pos
	left  expr
	right expr
}

func (node *multiplyNode) Node() node {
	return node
}

func (node *multiplyNode) IsExpr() bool {
	return true
}

func (node *multiplyNode) Left() node {
	return node.left
}

func (node *multiplyNode) Right() node {
	return node.right
}

type divideNode struct {
	*Pos
	left  expr
	right expr
}

func (node *divideNode) Node() node {
	return node
}

func (node *divideNode) IsExpr() bool {
	return true
}

func (node *divideNode) Left() node {
	return node.left
}

func (node *divideNode) Right() node {
	return node.right
}

type remainderNode struct {
	*Pos
	left  expr
	right expr
}

func (node *remainderNode) Node() node {
	return node
}

func (node *remainderNode) IsExpr() bool {
	return true
}

func (node *remainderNode) Left() node {
	return node.left
}

func (node *remainderNode) Right() node {
	return node.right
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
			node := &multiplyNode{p.token.pos, left, nil}
			p.acceptBlanks()
			right, ok := p.acceptExpr7()
			if !ok {
				return nil, false
			}
			node.right = right
			left = node
		} else if p.accept(tokenSlash) {
			node := &divideNode{p.token.pos, left, nil}
			p.acceptBlanks()
			right, ok := p.acceptExpr7()
			if !ok {
				return nil, false
			}
			node.right = right
			left = node
		} else if p.accept(tokenPercent) {
			node := &remainderNode{p.token.pos, left, nil}
			p.acceptBlanks()
			right, ok := p.acceptExpr7()
			if !ok {
				return nil, false
			}
			node.right = right
			left = node
		} else {
			break
		}
	}
	return left, true
}

type unaryOpNode interface {
	Value() node
}

type notNode struct {
	*Pos
	left expr
}

func (node *notNode) Node() node {
	return node
}

func (node *notNode) IsExpr() bool {
	return true
}

func (node *notNode) Value() node {
	return node.left
}

type minusNode struct {
	*Pos
	left expr
}

func (node *minusNode) Node() node {
	return node
}

func (node *minusNode) IsExpr() bool {
	return true
}

func (node *minusNode) Value() node {
	return node.left
}

type plusNode struct {
	*Pos
	left expr
}

func (node *plusNode) Node() node {
	return node
}

func (node *plusNode) IsExpr() bool {
	return true
}

func (node *plusNode) Value() node {
	return node.left
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
		node := &notNode{p.token.pos, left}
		return node, true
	} else if p.accept(tokenMinus) {
		left, ok := p.acceptExpr7()
		if !ok {
			return nil, false
		}
		node := &minusNode{p.token.pos, left}
		return node, true
	} else if p.accept(tokenPlus) {
		left, ok := p.acceptExpr7()
		if !ok {
			return nil, false
		}
		node := &plusNode{p.token.pos, left}
		return node, true
	} else {
		node, ok := p.acceptExpr8()
		if !ok {
			return nil, false
		}
		return node, true
	}
}

type sliceNode struct {
	*Pos
	left  expr
	rlist []expr
}

func (node *sliceNode) Node() node {
	return node
}

func (node *sliceNode) IsExpr() bool {
	return true
}

type callNode struct {
	*Pos
	left  expr
	rlist []expr
}

func (node *callNode) Node() node {
	return node
}

func (node *callNode) IsExpr() bool {
	return true
}

type subscriptNode struct {
	*Pos
	left  expr
	right expr
}

func (node *subscriptNode) Node() node {
	return node
}

func (node *subscriptNode) IsExpr() bool {
	return true
}

func (node *subscriptNode) Left() node {
	return node.left
}

func (node *subscriptNode) Right() node {
	return node.right
}

type dotNode struct {
	*Pos
	left  expr
	right node
}

func (node *dotNode) Node() node {
	return node
}

func (node *dotNode) IsExpr() bool {
	return true
}

func (node *dotNode) Left() node {
	return node.left
}

func (node *dotNode) Right() node {
	return node.right
}

type identifierNode struct {
	*Pos
	value string
}

func (node *identifierNode) Node() node {
	return node
}

func (node *identifierNode) IsExpr() bool {
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
				node := &sliceNode{npos, left, []expr{nil, nil}}
				p.acceptBlanks()
				if p.peek().typ != tokenSqClose {
					expr, ok := p.acceptExpr1()
					if !ok {
						return nil, false
					}
					node.rlist[1] = expr
					p.acceptBlanks()
				}
				if !p.accept(tokenSqClose) {
					p.errorf("expected %s but got %s", tokenName(tokenSqClose), tokenName(p.peek().typ))
					return nil, false
				}
				left = node
			} else {
				right, ok := p.acceptExpr1()
				if !ok {
					return nil, false
				}
				p.acceptBlanks()
				if p.accept(tokenColon) {
					node := &sliceNode{npos, left, []expr{right, nil}}
					p.acceptBlanks()
					if p.peek().typ != tokenSqClose {
						expr, ok := p.acceptExpr1()
						if !ok {
							return nil, false
						}
						node.rlist[1] = expr
						p.acceptBlanks()
					}
					if !p.accept(tokenSqClose) {
						p.errorf("expected %s but got %s", tokenName(tokenSqClose), tokenName(p.peek().typ))
						return nil, false
					}
					left = node
				} else {
					node := &subscriptNode{npos, left, right}
					p.acceptBlanks()
					if !p.accept(tokenSqClose) {
						p.errorf("expected %s but got %s", tokenName(tokenSqClose), tokenName(p.peek().typ))
						return nil, false
					}
					left = node
				}
			}
		} else if p.accept(tokenPOpen) {
			node := &callNode{p.token.pos, left, make([]expr, 0, 8)}
			p.acceptBlanks()
			if !p.accept(tokenPClose) {
				for {
					arg, ok := p.acceptExpr1()
					if !ok {
						return nil, false
					}
					node.rlist = append(node.rlist, arg)
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
			left = node
		} else if p.accept(tokenDot) {
			dot := p.token
			p.acceptBlanks()
			if !p.accept(tokenIdentifier) {
				p.errorf("expected %s but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ))
				return nil, false
			}
			right := &identifierNode{p.token.pos, p.token.val}
			left = &dotNode{dot.pos, left, right}
		} else {
			break
		}
	}
	return left, true
}

type literalNode interface {
	node
	Value() string
}

type intNode struct {
	*Pos
	value string
}

func (node *intNode) Node() node {
	return node
}

func (node *intNode) IsExpr() bool {
	return true
}

type floatNode struct {
	*Pos
	value string
}

func (node *floatNode) Node() node {
	return node
}

func (node *floatNode) IsExpr() bool {
	return true
}

type stringNode struct {
	*Pos
	value vainString
}

func (node *stringNode) Node() node {
	return node
}

func (node *stringNode) IsExpr() bool {
	return true
}

type listNode struct {
	*Pos
	value []expr
}

func (node *listNode) Node() node {
	return node
}

func (node *listNode) IsExpr() bool {
	return true
}

type dictionaryNode struct {
	*Pos
	value [][]expr
}

func (node *dictionaryNode) Node() node {
	return node
}

func (node *dictionaryNode) IsExpr() bool {
	return true
}

type optionNode struct {
	*Pos
	value string
}

func (node *optionNode) Node() node {
	return node
}

func (node *optionNode) IsExpr() bool {
	return true
}

func (node *optionNode) Value() string {
	return node.value[1:]
}

type envNode struct {
	*Pos
	value string
}

func (node *envNode) Node() node {
	return node
}

func (node *envNode) IsExpr() bool {
	return true
}

func (node *envNode) Value() string {
	return node.value[1:]
}

type regNode struct {
	*Pos
	value string
}

func (node *regNode) Node() node {
	return node
}

func (node *regNode) IsExpr() bool {
	return true
}

func (node *regNode) Value() string {
	return node.value[1:]
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
		node := &intNode{p.token.pos, p.token.val}
		return node, true
	} else if p.accept(tokenFloat) {
		node := &floatNode{p.token.pos, p.token.val}
		return node, true
	} else if p.accept(tokenString) {
		node := &stringNode{p.token.pos, vainString(p.token.val)}
		return node, true
	} else if p.accept(tokenSqOpen) {
		node := &listNode{p.token.pos, make([]expr, 0, 16)}
		p.acceptBlanks()
		if !p.accept(tokenSqClose) {
			for {
				expr, ok := p.acceptExpr1()
				if !ok {
					return nil, false
				}
				node.value = append(node.value, expr)
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
		return node, true
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
					pair[0] = &identifierNode{t1.pos, t1.val}
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
		node := &dictionaryNode{npos, m}
		return node, true
	} else if p.accept(tokenPOpen) {
		p.acceptBlanks()
		node, ok := p.acceptExpr1()
		if !ok {
			return nil, false
		}
		p.acceptBlanks()
		if !p.accept(tokenPClose) {
			p.errorf("expected %s but got %s", tokenName(tokenPClose), tokenName(p.peek().typ))
			return nil, false
		}
		return node, true
	} else if p.accept(tokenFunc) {
		p.backup(p.token)
		return p.acceptFunction(true)
	} else if p.accept(tokenOption) {
		node := &optionNode{p.token.pos, p.token.val}
		return node, true
	} else if p.accept(tokenIdentifier) {
		node := &identifierNode{p.token.pos, p.token.val}
		return node, true
	} else if p.accept(tokenEnv) {
		node := &envNode{p.token.pos, p.token.val}
		return node, true
	} else if p.accept(tokenReg) {
		node := &regNode{p.token.pos, p.token.val}
		return node, true
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
