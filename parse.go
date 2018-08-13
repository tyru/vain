package main

import (
	"errors"
	"fmt"
)

func parse(l *lexer) *parser {
	return &parser{
		name:  l.name,
		lexer: l,
		nodes: make(chan node, 1),
	}
}

type parser struct {
	name   string
	lexer  *lexer
	nodes  chan node
	token  *token  // next() sets read token to this.
	tokbuf []token // next() doesn't read from lexer.tokens if len(tokbuf) > 0 .
}

type nodeType int

type node interface {
	Position() *Pos
	IsExpr() bool
}

type expr interface {
	node
}

type statement interface {
	node
}

type errorNode struct {
	*Pos
	err error
}

func (node *errorNode) IsExpr() bool {
	return false
}

func (p *parser) Run() {
	if toplevel, ok := parseTopLevel(p); ok {
		toplevel.Pos.offset = 0 // first read node's position is -1. adjust it
		p.emit(toplevel)
	}
	close(p.nodes) // No more nodes will be delivered.
}

// emit passes an node back to the client.
func (p *parser) emit(node node) {
	p.nodes <- node
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
		t = <-p.lexer.tokens
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

func (node *topLevelNode) IsExpr() bool {
	return false
}

func parseTopLevel(p *parser) (*topLevelNode, bool) {
	pos := &Pos{0, 1, 0}
	toplevel := &topLevelNode{pos, make([]node, 0, 32)}
	for {
		node, ok := parseStmtOrExpr(p)
		if !ok {
			return toplevel, true
		}
		toplevel.body = append(toplevel.body, node)
	}
}

// statementOrExpression
func parseStmtOrExpr(p *parser) (node, bool) {
	p.acceptSpaces()
	if p.accept(tokenEOF) {
		return nil, false
	}
	if p.accept(tokenError) {
		p.lexError()
		return nil, false
	}

	// Statement
	if p.accept(tokenImport) || p.accept(tokenFrom) {
		p.backup(p.token)
		return acceptImportStatement(p)
	}
	if p.accept(tokenFunc) {
		p.backup(p.token)
		return acceptFunction(p, false)
	}
	if p.accept(tokenReturn) {
		ret := p.token
		expr, ok := parseExpr(p)
		if !ok {
			return nil, false
		}
		return &returnStatement{ret.pos, expr}, true
	}

	// Expression
	return parseExpr(p)
}

type returnStatement struct {
	*Pos
	left expr
}

func (node *returnStatement) IsExpr() bool {
	return false
}

func (node *returnStatement) Value() node {
	return node.left
}

type importStatement struct {
	*Pos
	pkg      vainString
	pkgAlias string
	fnlist   [][]string
}

func (node *importStatement) IsExpr() bool {
	return false
}

// importStatement := "import" string [ "as" *LF identifier ] |
//                    "from" string "import" <importFunctionList>
func acceptImportStatement(p *parser) (*importStatement, bool) {
	if p.accept(tokenImport) {
		pos := p.token.pos
		if !p.accept(tokenString) {
			p.errorf("expected %s but got %s", tokenName(tokenString), tokenName(p.peek().typ))
			return nil, false
		}
		pkg := vainString(p.token.val)
		var pkgAlias string
		if p.accept(tokenAs) {
			p.acceptSpaces()
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
		fnlist, ok := acceptImportFunctionList(p)
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

// importFunctionList = importFunctionListItem *( *LF "," *LF importFunctionListItem )
// importFunctionListItem := identifier [ "as" *LF identifier ]
func acceptImportFunctionList(p *parser) ([][]string, bool) {
	fnlist := make([][]string, 0, 1)
	for {
		if !p.accept(tokenIdentifier) {
			p.errorf("expected %s but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ))
			return nil, false
		}
		orig := p.token.val
		to := orig

		if p.accept(tokenAs) {
			p.acceptSpaces()
			if !p.accept(tokenIdentifier) {
				p.errorf("expected %s but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ))
				return nil, false
			}
			to = p.token.val
			fnlist = append(fnlist, []string{orig, to})
		} else {
			fnlist = append(fnlist, []string{orig})
		}

		p.acceptSpaces()
		if !p.accept(tokenComma) {
			break
		}
		p.acceptSpaces()
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

func (node *funcStmtOrExpr) IsExpr() bool {
	return node.isExpr
}

// function :=
//        "func" [ functionModifierList ] [ identifier ] functionCallSignature expr1 /
//        "func" [ functionModifierList ] [ identifier ] functionCallSignature "{" *LF *( statementOrExpression *LF ) "}" /
func acceptFunction(p *parser, isExpr bool) (*funcStmtOrExpr, bool) {
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
		mods, ok = acceptModifiers(p)
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
	args, retType, ok = acceptFunctionCallSignature(p)
	if !ok {
		return nil, false
	}

	// Body
	body = make([]node, 0, 32)
	if p.accept(tokenCOpen) {
		bodyIsStmt = true
		p.acceptSpaces()
		for {
			if p.accept(tokenCClose) {
				break
			}
			node, ok := parseStmtOrExpr(p)
			if !ok {
				return nil, false
			}
			body = append(body, node)
			p.acceptSpaces()
		}
	} else {
		expr, ok := parseExpr(p)
		if !ok {
			return nil, false
		}
		body = append(body, expr)
	}

	f := &funcStmtOrExpr{pos, isExpr, mods, name, args, retType, bodyIsStmt, body}
	return f, true
}

// functionModifierList := "<" *LF functionModifier *( *LF "," *LF functionModifier ) *LF ">"
// functionModifier := "noabort" | "autoload" | "global" | "range" | "dict" | "closure"
func acceptModifiers(p *parser) ([]string, bool) {
	if !p.accept(tokenLt) {
		p.errorf("expected %s but got %s", tokenName(tokenLt), tokenName(p.peek().typ))
		return nil, false
	}
	mods := make([]string, 0, 8)
	p.acceptSpaces()
	for {
		if !p.accept(tokenIdentifier) {
			p.errorf("expected %s but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ))
			return nil, false
		}
		switch p.token.val {
		case "noabort":
		case "autoload":
		case "global":
		case "range":
		case "dict":
		case "closure":
		default:
			p.errorf("expected function modifier but got %s", tokenName(p.peek().typ))
			return nil, false
		}
		mods = append(mods, p.token.val)
		p.acceptSpaces()
		if p.accept(tokenComma) {
			p.acceptSpaces()
			if p.accept(tokenGt) {
				break
			}
		} else if p.accept(tokenGt) {
			break
		}
	}
	return mods, true
}

// functionCallSignature := "(" *LF *( functionArgument *LF "," *LF ) ")" [ ":" type ]
func acceptFunctionCallSignature(p *parser) ([]argument, string, bool) {
	if !p.accept(tokenPOpen) {
		p.errorf("expected %s but got %s", tokenName(tokenPOpen), tokenName(p.peek().typ))
		return nil, "", false
	}
	p.acceptSpaces()

	var args []argument
	if !p.accept(tokenPClose) {
		args := make([]argument, 0, 8)
		for {
			arg, ok := acceptFunctionArgument(p)
			if !ok {
				return nil, "", false
			}
			args = append(args, *arg)
			p.acceptSpaces()
			if p.accept(tokenComma) {
				p.acceptSpaces()
				if p.accept(tokenPClose) {
					break
				}
			} else if p.accept(tokenPClose) {
				break
			}
			p.acceptSpaces()
		}
	}

	var retType string
	if p.accept(tokenColon) {
		var ok bool
		retType, ok = acceptType(p)
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

// functionArgument := identifier *LF ":" *LF type /
//                     identifier *LF "=" *LF expr
func acceptFunctionArgument(p *parser) (*argument, bool) {
	var name string
	var typ string

	if !p.accept(tokenIdentifier) {
		p.errorf("expected %s but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ))
		return nil, false
	}
	name = p.token.val

	p.acceptSpaces()
	if p.accept(tokenColon) {
		p.acceptSpaces()
		var ok bool
		typ, ok = acceptType(p)
		if !ok {
			return nil, false
		}
		return &argument{name, typ, nil}, true
	} else if p.accept(tokenEqual) {
		p.acceptSpaces()
		expr, ok := parseExpr(p)
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
func acceptType(p *parser) (string, bool) {
	if !p.accept(tokenIdentifier) {
		p.errorf("expected %s but got %s", tokenName(tokenIdentifier), tokenName(p.peek().typ))
		return "", false
	}
	return p.token.val, true
}

func parseExpr(p *parser) (expr, bool) {
	return parseExpr1(p)
}

type ternaryNode struct {
	*Pos
	cond  expr
	left  expr
	right expr
}

func (node *ternaryNode) IsExpr() bool {
	return true
}

// expr1 := expr2 [ "?" expr1 *LF ":" expr1 ]
func parseExpr1(p *parser) (expr, bool) {
	left, ok := parseExpr2(p)
	if !ok {
		return nil, false
	}
	if p.accept(tokenQuestion) {
		node := &ternaryNode{p.token.pos, left, nil, nil}
		expr, ok := parseExpr1(p)
		if !ok {
			return nil, false
		}
		node.left = expr
		p.acceptSpaces()
		if !p.accept(tokenColon) {
			p.errorf("expected %s but got %s", tokenName(tokenColon), tokenName(p.peek().typ))
			return nil, false
		}
		right, ok := parseExpr1(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
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

func (node *orNode) IsExpr() bool {
	return true
}

func (node *orNode) Left() node {
	return node.left
}

func (node *orNode) Right() node {
	return node.right
}

// expr2 := expr3 *( "||" *LF expr3 )
func parseExpr2(p *parser) (expr, bool) {
	left, ok := parseExpr3(p)
	if !ok {
		return nil, false
	}
	for {
		if p.accept(tokenOrOr) {
			node := &orNode{p.token.pos, left, nil}
			p.acceptSpaces()
			right, ok := parseExpr3(p)
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

func (node *andNode) IsExpr() bool {
	return true
}

func (node *andNode) Left() node {
	return node.left
}

func (node *andNode) Right() node {
	return node.right
}

// expr3 := expr4 *( "&&" *LF expr4 )
func parseExpr3(p *parser) (expr, bool) {
	left, ok := parseExpr4(p)
	if !ok {
		return nil, false
	}
	for {
		if p.accept(tokenAndAnd) {
			node := &andNode{p.token.pos, left, nil}
			p.acceptSpaces()
			right, ok := parseExpr4(p)
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

func (node *isNotCiNode) IsExpr() bool {
	return true
}

func (node *isNotCiNode) Left() node {
	return node.left
}

func (node *isNotCiNode) Right() node {
	return node.right
}

// expr4 := expr5 "=="  *LF expr5 /
//          expr5 "==?" *LF expr5 /
//          expr5 "!="  *LF expr5 /
//          expr5 "!=?" *LF expr5 /
//          expr5 ">"   *LF expr5 /
//          expr5 ">?"  *LF expr5 /
//          expr5 ">="  *LF expr5 /
//          expr5 ">=?" *LF expr5 /
//          expr5 "<"   *LF expr5 /
//          expr5 "<?"  *LF expr5 /
//          expr5 "<="  *LF expr5 /
//          expr5 "<=?" *LF expr5 /
//          expr5 "=~"  *LF expr5 /
//          expr5 "=~?" *LF expr5 /
//          expr5 "!~"  *LF expr5 /
//          expr5 "!~?" *LF expr5 /
//          expr5 "is"  *LF expr5 /
//          expr5 "is?" *LF expr5 /
//          expr5 "isnot"  *LF expr5 /
//          expr5 "isnot?" *LF expr5 /
//          expr5
func parseExpr4(p *parser) (expr, bool) {
	left, ok := parseExpr5(p)
	if !ok {
		return nil, false
	}
	if p.accept(tokenEqEq) {
		node := &equalNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenEqEqCi) {
		node := &equalCiNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenNeq) {
		node := &nequalNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenNeqCi) {
		node := &nequalCiNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenGt) {
		node := &greaterNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenGtCi) {
		node := &greaterCiNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenGtEq) {
		node := &gequalNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenGtEqCi) {
		node := &gequalCiNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenLt) {
		node := &smallerNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenLtCi) {
		node := &smallerCiNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenLtEq) {
		node := &sequalNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenLtEqCi) {
		node := &sequalCiNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenMatch) {
		node := &matchNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenMatchCi) {
		node := &matchCiNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenNoMatch) {
		node := &noMatchNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenNoMatchCi) {
		node := &noMatchCiNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenIs) {
		node := &isNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenIsCi) {
		node := &isCiNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenIsNot) {
		node := &isNotNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenIsNotCi) {
		node := &isNotCiNode{p.token.pos, left, nil}
		p.acceptSpaces()
		right, ok := parseExpr5(p)
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

func (node *subtractNode) IsExpr() bool {
	return true
}

func (node *subtractNode) Left() node {
	return node.left
}

func (node *subtractNode) Right() node {
	return node.right
}

// expr5 := expr6 1*( "+" *LF expr6 ) /
//          expr6 1*( "-" *LF expr6 ) /
//          expr6
func parseExpr5(p *parser) (expr, bool) {
	left, ok := parseExpr6(p)
	if !ok {
		return nil, false
	}
	for {
		if p.accept(tokenPlus) {
			node := &addNode{p.token.pos, left, nil}
			p.acceptSpaces()
			right, ok := parseExpr6(p)
			if !ok {
				return nil, false
			}
			node.right = right
			left = node
		} else if p.accept(tokenMinus) {
			node := &subtractNode{p.token.pos, left, nil}
			p.acceptSpaces()
			right, ok := parseExpr6(p)
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

func (node *remainderNode) IsExpr() bool {
	return true
}

func (node *remainderNode) Left() node {
	return node.left
}

func (node *remainderNode) Right() node {
	return node.right
}

// expr6 := expr7 1*( "*" *LF expr7 ) /
//          expr7 1*( "/" *LF expr7 ) /
//          expr7 1*( "%" *LF expr7 ) /
//          expr7
func parseExpr6(p *parser) (expr, bool) {
	left, ok := parseExpr7(p)
	if !ok {
		return nil, false
	}
	for {
		if p.accept(tokenStar) {
			node := &multiplyNode{p.token.pos, left, nil}
			p.acceptSpaces()
			right, ok := parseExpr7(p)
			if !ok {
				return nil, false
			}
			node.right = right
			left = node
		} else if p.accept(tokenSlash) {
			node := &divideNode{p.token.pos, left, nil}
			p.acceptSpaces()
			right, ok := parseExpr7(p)
			if !ok {
				return nil, false
			}
			node.right = right
			left = node
		} else if p.accept(tokenPercent) {
			node := &remainderNode{p.token.pos, left, nil}
			p.acceptSpaces()
			right, ok := parseExpr7(p)
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
func parseExpr7(p *parser) (expr, bool) {
	if p.accept(tokenNot) {
		node := &notNode{p.token.pos, nil}
		left, ok := parseExpr7(p)
		if !ok {
			return nil, false
		}
		node.left = left
		return node, true
	} else if p.accept(tokenMinus) {
		node := &minusNode{p.token.pos, nil}
		left, ok := parseExpr7(p)
		if !ok {
			return nil, false
		}
		node.left = left
		return node, true
	} else if p.accept(tokenPlus) {
		node := &plusNode{p.token.pos, nil}
		left, ok := parseExpr7(p)
		if !ok {
			return nil, false
		}
		node.left = left
		return node, true
	} else {
		node, ok := parseExpr8(p)
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

func (node *sliceNode) IsExpr() bool {
	return true
}

type callNode struct {
	*Pos
	left  expr
	rlist []expr
}

func (node *callNode) IsExpr() bool {
	return true
}

type subscriptNode struct {
	*Pos
	left  expr
	right expr
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
	right *identifierNode
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

func (node *identifierNode) IsExpr() bool {
	return true
}

// expr8 := expr9 1*( "[" *LF expr1 *LF "]" ) /
//          expr9 1*( "[" *LF [ expr1 ] *LF ":" *LF [ expr1 ] *LF "]" ) /
//          expr9 1*( "." *LF identifier ) /
//          expr9 1*( "(" *LF [ expr1 *LF *( "," *LF expr1 *LF) [ "," ] ] *LF ")" ) /
//          expr9
func parseExpr8(p *parser) (expr, bool) {
	left, ok := parseExpr9(p)
	if !ok {
		return nil, false
	}
	for {
		if p.accept(tokenSqOpen) {
			npos := p.token.pos
			p.acceptSpaces()
			if p.accept(tokenColon) {
				node := &sliceNode{npos, left, []expr{nil, nil}}
				p.acceptSpaces()
				if p.peek().typ != tokenSqClose {
					expr, ok := parseExpr1(p)
					if !ok {
						return nil, false
					}
					node.rlist[1] = expr
				}
				if !p.accept(tokenSqClose) {
					p.errorf("expected %s but got %s", tokenName(tokenSqClose), tokenName(p.peek().typ))
					return nil, false
				}
				left = node
			} else {
				right, ok := parseExpr1(p)
				if !ok {
					return nil, false
				}
				p.acceptSpaces()
				if p.accept(tokenColon) {
					node := &sliceNode{npos, left, []expr{right, nil}}
					p.acceptSpaces()
					if p.peek().typ != tokenSqClose {
						expr, ok := parseExpr1(p)
						if !ok {
							return nil, false
						}
						node.rlist[1] = expr
					}
					if !p.accept(tokenSqClose) {
						p.errorf("expected %s but got %s", tokenName(tokenSqClose), tokenName(p.peek().typ))
						return nil, false
					}
					left = node
				} else {
					node := &subscriptNode{npos, left, right}
					p.acceptSpaces()
					if !p.accept(tokenSqClose) {
						p.errorf("expected %s but got %s", tokenName(tokenSqClose), tokenName(p.peek().typ))
						return nil, false
					}
					left = node
				}
			}
		} else if p.accept(tokenPOpen) {
			node := &callNode{p.token.pos, left, make([]expr, 0, 8)}
			p.acceptSpaces()
			if !p.accept(tokenPClose) {
				for {
					arg, ok := parseExpr1(p)
					if !ok {
						return nil, false
					}
					node.rlist = append(node.rlist, arg)
					p.acceptSpaces()
					if p.accept(tokenComma) {
						p.acceptSpaces()
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
			p.acceptSpaces()
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

func (node *intNode) IsExpr() bool {
	return true
}

type floatNode struct {
	*Pos
	value string
}

func (node *floatNode) IsExpr() bool {
	return true
}

type stringNode struct {
	*Pos
	value vainString
}

func (node *stringNode) IsExpr() bool {
	return true
}

type listNode struct {
	*Pos
	value []expr
}

func (node *listNode) IsExpr() bool {
	return true
}

type dictionaryNode struct {
	*Pos
	value [][]expr
}

func (node *dictionaryNode) IsExpr() bool {
	return true
}

type optionNode struct {
	*Pos
	value string
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

func (node *regNode) IsExpr() bool {
	return true
}

func (node *regNode) Value() string {
	return node.value[1:]
}

// expr9: number /
//        (string ABNF is too complex! e.g. "string\n", 'str''ing') /
//        "[" *LF *( expr1 *LF "," *LF ) "]" /
//        "{" *LF *( ( identifierLike | expr1 ) *LF ":" *LF expr1 *LF "," *LF ) "}" /
//        &option /
//        "(" *LF expr1 *LF ")" /
//        function /
//        identifier /
//        $VAR /
//        @r
func parseExpr9(p *parser) (expr, bool) {
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
		p.acceptSpaces()
		if !p.accept(tokenSqClose) {
			for {
				expr, ok := parseExpr1(p)
				if !ok {
					return nil, false
				}
				node.value = append(node.value, expr)
				p.acceptSpaces()
				if p.accept(tokenComma) {
					p.acceptSpaces()
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
		p.acceptSpaces()
		if !p.accept(tokenCClose) {
			m = make([][]expr, 0, 16)
			for {
				pair := []expr{nil, nil}
				p.acceptSpaces()
				t1 := p.next()
				p.acceptSpaces()
				t2 := p.next()
				if p.canBeIdentifier(t1) && t2.typ == tokenColon {
					pair[0] = &identifierNode{t1.pos, t1.val}
					p.acceptSpaces()
					right, ok := parseExpr1(p)
					if !ok {
						return nil, false
					}
					pair[1] = right
				} else {
					p.backup(t2)
					p.backup(t1)
					left, ok := parseExpr1(p)
					if !ok {
						return nil, false
					}
					p.acceptSpaces()
					if !p.accept(tokenColon) {
						p.errorf("expected %s but got %s", tokenName(tokenColon), tokenName(p.peek().typ))
						return nil, false
					}
					p.acceptSpaces()
					right, ok := parseExpr1(p)
					if !ok {
						return nil, false
					}
					pair[0] = left
					pair[1] = right
				}
				m = append(m, pair)
				p.acceptSpaces()
				if p.accept(tokenComma) {
					p.acceptSpaces()
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
		p.acceptSpaces()
		node, ok := parseExpr1(p)
		if !ok {
			return nil, false
		}
		p.acceptSpaces()
		if !p.accept(tokenPClose) {
			p.errorf("expected %s but got %s", tokenName(tokenPClose), tokenName(p.peek().typ))
			return nil, false
		}
		return node, true
	} else if p.accept(tokenFunc) {
		p.backup(p.token)
		return acceptFunction(p, true)
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
