package main

import (
	"errors"
	"fmt"
	"runtime/debug"
)

func parse(l *lexer) *parser {
	return &parser{
		name:  l.name,
		lexer: l,
		nodes: make(chan node, 1),
		start: -1,
	}
}

type parser struct {
	name   string
	lexer  *lexer
	nodes  chan node
	token  *token  // next() sets read token to this.
	tokbuf []token // next() doesn't read from lexer.tokens if len(tokbuf) > 0 .
	start  Pos     // start position of node.
}

type nodeType int

type node interface {
	Position() Pos
}

type expr interface {
	node
}

type statement interface {
	node
}

type errorNode struct {
	Pos
	err error
}

func (p *parser) Run() {
	if toplevel, ok := parseTopLevel(p); ok {
		toplevel.Pos = 0 // first read node's position is -1. adjust it
		p.emit(toplevel)
	}
	close(p.nodes) // No more nodes will be delivered.
}

// emit passes an node back to the client.
func (p *parser) emit(node node) {
	p.nodes <- node
	p.start = -1
}

// emitErrorf is called when unexpected token was given.
// This is normally lexer's bug.
func (p *parser) emitErrorf(msg string, args ...interface{}) {
	if p.token.typ == tokenEOF {
		p.unexpectedEOF()
		return
	}
	if p.token.typ == tokenError {
		p.tokenError()
		return
	}
	if msg == "" {
		// TODO: Don't print callstack in future.
		msg = fmt.Sprintf("%s:%d: fatal: unexpected token: %+v\n%s",
			p.name, p.token.line, p.token, string(debug.Stack()))
	} else {
		newargs := make([]interface{}, 0, len(args)+2)
		newargs = append(newargs, p.name, p.token.line)
		newargs = append(newargs, args...)
		msg = fmt.Sprintf("%s:%d: "+msg, newargs...)
	}
	p.nodes <- &errorNode{err: errors.New(msg), Pos: p.token.pos}
}

// unexpectedEOF is called when tokenEOF was given and it's unexpected.
func (p *parser) unexpectedEOF() {
	p.nodes <- &errorNode{
		err: fmt.Errorf("%s:%d: unexpected EOF", p.name, p.token.line),
		Pos: p.token.pos,
	}
}

// tokenError is called when tokenError was given.
func (p *parser) tokenError() {
	p.nodes <- &errorNode{
		err: fmt.Errorf("%s:%d: %s", p.name, p.token.line, p.token.val),
		Pos: p.token.pos,
	}
}

// next returns the next token in the input.
func (p *parser) next() *token {
	var t *token
	if len(p.tokbuf) > 0 {
		t = &p.tokbuf[len(p.tokbuf)-1]
		p.tokbuf = p.tokbuf[:len(p.tokbuf)-1]
	} else {
		tt := <-p.lexer.tokens
		t = &tt
	}
	p.token = t
	if p.start < 0 {
		p.start = Pos(t.pos)
	}
	return t
}

// nextNosp returns the next token in the input.
// This skips tokenNewline.
func (p *parser) nextNosp() *token {
	for {
		if t := p.next(); t.typ != tokenNewline {
			return t
		}
	}
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

// peekNosp returns but does not consume
// the next token in the input.
// This skips tokenNewline.
func (p *parser) peekNosp() *token {
	t := p.next()
	p.backup(t)
	return t
}

func (p *parser) accept(typ tokenType) bool {
	t := p.next()
	if t.typ == typ {
		return true
	}
	p.backup(t)
	return false
}

func (p *parser) acceptNosp(typ tokenType) bool {
	t := p.nextNosp()
	if t.typ == typ {
		return true
	}
	p.backup(t)
	return false
}

// errorf returns an error token and terminates the scan
// by passing back a nil pointer that will be the next
// state, terminating l.Run.
func (p *parser) errorf(format string, args ...interface{}) {
	p.emit(&errorNode{err: fmt.Errorf(format, args...), Pos: p.token.pos})
}

type topLevelNode struct {
	Pos
	body []node
}

func parseTopLevel(p *parser) (*topLevelNode, bool) {
	toplevel := &topLevelNode{p.start, make([]node, 0, 32)}
	for {
		node, ok := parseStmtOrExpr(p)
		if !ok {
			return toplevel, true
		}
		toplevel.body = append(toplevel.body, node)
	}
}

func parseStmtOrExpr(p *parser) (node, bool) {
	if p.acceptNosp(tokenEOF) {
		return nil, false
	}
	if p.acceptNosp(tokenError) {
		p.tokenError()
		return nil, false
	}

	// Statement
	if p.acceptNosp(tokenImport) {
		p.backup(p.token)
		return parseImportStatement(p)
	}
	if p.acceptNosp(tokenFunc) {
		p.backup(p.token)
		return parseFunc(p)
	}

	// Expression
	return parseExpr(p)
}

type importStatement struct {
	Pos
	brace  bool
	fnlist [][]string
	pkg    vainString
}

func parseImportStatement(p *parser) (*importStatement, bool) {
	if !p.acceptNosp(tokenImport) {
		p.emitErrorf("")
		return nil, false
	}

	brace, fnlist, ok := parseImportFunctionList(p)
	if !ok {
		return nil, false
	}

	if !p.acceptNosp(tokenFrom) {
		p.emitErrorf("")
		return nil, false
	}

	if !p.acceptNosp(tokenString) {
		p.emitErrorf("")
		return nil, false
	}
	pkg := vainString(p.token.val)

	stmt := &importStatement{p.start, brace, fnlist, pkg}
	return stmt, true
}

// import { <import function> } from 'pkg'
// import { <import function>, <import function> } from 'pkg'
// import <import function> from 'pkg'
// import <import function> as foo from 'pkg'
func parseImportFunctionList(p *parser) (bool, [][]string, bool) {
	var brace bool
	if p.acceptNosp(tokenCOpen) {
		brace = true
	}

	fnlist := make([][]string, 0, 1)
	for {
		if !p.acceptNosp(tokenIdentifier) && !p.acceptNosp(tokenStar) {
			p.emitErrorf("")
			return false, nil, false
		}
		orig := p.token.val
		to := orig

		if p.acceptNosp(tokenAs) {
			if !p.acceptNosp(tokenIdentifier) && !p.acceptNosp(tokenStar) {
				p.emitErrorf("")
				return false, nil, false
			}
			to = p.token.val
		}
		fnlist = append(fnlist, []string{orig, to})

		if brace && p.acceptNosp(tokenCClose) {
			break
		}
		if p.acceptNosp(tokenFrom) {
			p.backup(p.token)
			break
		}
		if p.acceptNosp(tokenComma) {
			continue
		}

		p.emitErrorf("unexpected token %+v in import statement", p.token.val)
		return false, nil, false
	}

	return brace, fnlist, true
}

type funcStmtOrExpr struct {
	Pos
	mods       []string
	name       string
	args       []argument
	bodyIsStmt bool
	body       []node
}

func parseFunc(p *parser) (*funcStmtOrExpr, bool) {
	if !p.acceptNosp(tokenFunc) {
		p.emitErrorf("")
		return nil, false
	}

	var mods []string
	var name string
	var args []argument
	var bodyIsStmt bool
	var body []node

	// Modifiers
	if p.acceptNosp(tokenLt) {
		p.backup(p.token)
		var ok bool
		mods, ok = parseModifiers(p)
		if !ok {
			return nil, false
		}
	}

	// Function name (if empty, this is functionExpression not functionStatement)
	if p.acceptNosp(tokenIdentifier) {
		name = p.token.val
	}

	var ok bool
	args, ok = parseCallSignature(p)
	if !ok {
		return nil, false
	}

	// Body
	body = make([]node, 0, 32)
	if p.acceptNosp(tokenCOpen) {
		bodyIsStmt = true
		for {
			if p.acceptNosp(tokenCClose) {
				break
			}
			node, ok := parseStmtOrExpr(p)
			if !ok {
				return nil, false
			}
			body = append(body, node)
		}
	} else {
		expr, ok := parseExpr(p)
		if !ok {
			return nil, false
		}
		body = append(body, expr)
	}

	f := &funcStmtOrExpr{p.start, mods, name, args, bodyIsStmt, body}
	return f, true
}

func parseModifiers(p *parser) ([]string, bool) {
	if !p.acceptNosp(tokenLt) {
		p.emitErrorf("")
		return nil, false
	}
	mods := make([]string, 0, 8)
	for {
		if !p.acceptNosp(tokenIdentifier) {
			p.emitErrorf("")
			return nil, false
		}
		mods = append(mods, p.token.val)
		if p.acceptNosp(tokenComma) {
			continue
		}
		if p.acceptNosp(tokenGt) {
			break
		}
		p.emitErrorf("")
		return nil, false
	}
	return mods, true
}

func parseCallSignature(p *parser) ([]argument, bool) {
	var args []argument

	if !p.acceptNosp(tokenPOpen) {
		p.emitErrorf("")
		return nil, false
	}
	for {
		if p.acceptNosp(tokenPClose) {
			break
		}
		arg, ok := parseArgument(p)
		if !ok {
			return nil, false
		}
		args = append(args, *arg)
		if !p.acceptNosp(tokenComma) {
			p.emitErrorf("")
			return nil, false
		}
	}
	return args, true
}

type argument struct {
	name       string
	typ        string
	defaultVal expr
}

// argument := identifier ":" type /
//             identifier "=" expr
func parseArgument(p *parser) (*argument, bool) {
	var name string
	var typ string

	if !p.acceptNosp(tokenIdentifier) {
		p.emitErrorf("")
		return nil, false
	}
	name = p.token.val

	if p.acceptNosp(tokenColon) {
		var ok bool
		typ, ok = parseType(p)
		if !ok {
			return nil, false
		}
		return &argument{name, typ, nil}, true
	}

	// name = defaultValue
	if p.acceptNosp(tokenEqual) {
		expr, ok := parseExpr(p)
		if !ok {
			return nil, false
		}
		return &argument{name, "", expr}, true
	}

	p.emitErrorf("")
	return nil, false
}

// TODO: Complex type like array, dictionary, generics...
func parseType(p *parser) (string, bool) {
	if !p.acceptNosp(tokenIdentifier) {
		p.emitErrorf("")
		return "", false
	}
	return p.token.val, true
}

func parseExpr(p *parser) (expr, bool) {
	return parseExpr1(p)
}

type ternaryNode struct {
	Pos
	cond  expr
	left  expr
	right expr
}

// expr1 := expr2 [ "?" expr1 ":" expr1 ]
func parseExpr1(p *parser) (expr, bool) {
	left, ok := parseExpr2(p)
	if !ok {
		return nil, false
	}
	if p.acceptNosp(tokenQuestion) {
		node := &ternaryNode{}
		node.cond = left
		expr, ok := parseExpr1(p)
		if !ok {
			return nil, false
		}
		node.left = expr
		if !p.acceptNosp(tokenColon) {
			p.emitErrorf("")
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
	Pos
	left  expr
	right expr
}

func (node *orNode) Left() node {
	return node.left
}

func (node *orNode) Right() node {
	return node.right
}

// expr2 := expr3 *( "||" expr3 )
func parseExpr2(p *parser) (expr, bool) {
	left, ok := parseExpr3(p)
	if !ok {
		return nil, false
	}
	for {
		if p.acceptNosp(tokenOrOr) {
			node := &orNode{}
			node.Pos = p.token.pos
			node.left = left
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
	Pos
	left  expr
	right expr
}

func (node *andNode) Left() node {
	return node.left
}

func (node *andNode) Right() node {
	return node.right
}

// expr3 := expr4 *( "&&" expr4 )
func parseExpr3(p *parser) (expr, bool) {
	left, ok := parseExpr4(p)
	if !ok {
		return nil, false
	}
	for {
		if p.acceptNosp(tokenAndAnd) {
			node := &andNode{}
			node.Pos = p.token.pos
			node.left = left
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
	Pos
	left  expr
	right expr
}

func (node *equalNode) Left() node {
	return node.left
}

func (node *equalNode) Right() node {
	return node.right
}

type equalCiNode struct {
	Pos
	left  expr
	right expr
}

func (node *equalCiNode) Left() node {
	return node.left
}

func (node *equalCiNode) Right() node {
	return node.right
}

type nequalNode struct {
	Pos
	left  expr
	right expr
}

func (node *nequalNode) Left() node {
	return node.left
}

func (node *nequalNode) Right() node {
	return node.right
}

type nequalCiNode struct {
	Pos
	left  expr
	right expr
}

func (node *nequalCiNode) Left() node {
	return node.left
}

func (node *nequalCiNode) Right() node {
	return node.right
}

type greaterNode struct {
	Pos
	left  expr
	right expr
}

func (node *greaterNode) Left() node {
	return node.left
}

func (node *greaterNode) Right() node {
	return node.right
}

type greaterCiNode struct {
	Pos
	left  expr
	right expr
}

func (node *greaterCiNode) Left() node {
	return node.left
}

func (node *greaterCiNode) Right() node {
	return node.right
}

type gequalNode struct {
	Pos
	left  expr
	right expr
}

func (node *gequalNode) Left() node {
	return node.left
}

func (node *gequalNode) Right() node {
	return node.right
}

type gequalCiNode struct {
	Pos
	left  expr
	right expr
}

func (node *gequalCiNode) Left() node {
	return node.left
}

func (node *gequalCiNode) Right() node {
	return node.right
}

type smallerNode struct {
	Pos
	left  expr
	right expr
}

func (node *smallerNode) Left() node {
	return node.left
}

func (node *smallerNode) Right() node {
	return node.right
}

type smallerCiNode struct {
	Pos
	left  expr
	right expr
}

func (node *smallerCiNode) Left() node {
	return node.left
}

func (node *smallerCiNode) Right() node {
	return node.right
}

type sequalNode struct {
	Pos
	left  expr
	right expr
}

func (node *sequalNode) Left() node {
	return node.left
}

func (node *sequalNode) Right() node {
	return node.right
}

type sequalCiNode struct {
	Pos
	left  expr
	right expr
}

func (node *sequalCiNode) Left() node {
	return node.left
}

func (node *sequalCiNode) Right() node {
	return node.right
}

type matchNode struct {
	Pos
	left  expr
	right expr
}

func (node *matchNode) Left() node {
	return node.left
}

func (node *matchNode) Right() node {
	return node.right
}

type matchCiNode struct {
	Pos
	left  expr
	right expr
}

func (node *matchCiNode) Left() node {
	return node.left
}

func (node *matchCiNode) Right() node {
	return node.right
}

type noMatchNode struct {
	Pos
	left  expr
	right expr
}

func (node *noMatchNode) Left() node {
	return node.left
}

func (node *noMatchNode) Right() node {
	return node.right
}

type noMatchCiNode struct {
	Pos
	left  expr
	right expr
}

func (node *noMatchCiNode) Left() node {
	return node.left
}

func (node *noMatchCiNode) Right() node {
	return node.right
}

type isNode struct {
	Pos
	left  expr
	right expr
}

func (node *isNode) Left() node {
	return node.left
}

func (node *isNode) Right() node {
	return node.right
}

type isCiNode struct {
	Pos
	left  expr
	right expr
}

func (node *isCiNode) Left() node {
	return node.left
}

func (node *isCiNode) Right() node {
	return node.right
}

type isNotNode struct {
	Pos
	left  expr
	right expr
}

func (node *isNotNode) Left() node {
	return node.left
}

func (node *isNotNode) Right() node {
	return node.right
}

type isNotCiNode struct {
	Pos
	left  expr
	right expr
}

func (node *isNotCiNode) Left() node {
	return node.left
}

func (node *isNotCiNode) Right() node {
	return node.right
}

// expr4 := expr5 "=="  expr5 /
//          expr5 "==?" expr5 /
//          expr5 "!="  expr5 /
//          expr5 "!=?" expr5 /
//          expr5 ">"   expr5 /
//          expr5 ">?"  expr5 /
//          expr5 ">="  expr5 /
//          expr5 ">=?" expr5 /
//          expr5 "<"   expr5 /
//          expr5 "<?"  expr5 /
//          expr5 "<="  expr5 /
//          expr5 "<=?" expr5 /
//          expr5 "=~"  expr5 /
//          expr5 "=~?" expr5 /
//          expr5 "!~"  expr5 /
//          expr5 "!~?" expr5 /
//          expr5 "is"  expr5 /
//          expr5 "is?" expr5 /
//          expr5 "isnot"  expr5 /
//          expr5 "isnot?" expr5 /
//          expr5
func parseExpr4(p *parser) (expr, bool) {
	left, ok := parseExpr5(p)
	if !ok {
		return nil, false
	}
	if p.acceptNosp(tokenEqEq) {
		node := &equalNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenEqEqCi) {
		node := &equalCiNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenNeq) {
		node := &nequalNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenNeqCi) {
		node := &nequalCiNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenGt) {
		node := &greaterNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenGtCi) {
		node := &greaterCiNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenGtEq) {
		node := &gequalNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenGtEqCi) {
		node := &gequalCiNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenLt) {
		node := &smallerNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenLtCi) {
		node := &smallerCiNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenLtEq) {
		node := &sequalNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenLtEqCi) {
		node := &sequalCiNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenMatch) {
		node := &matchNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenMatchCi) {
		node := &matchCiNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenNoMatch) {
		node := &noMatchNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenNoMatchCi) {
		node := &noMatchCiNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenIs) {
		node := &isNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenIsCi) {
		node := &isCiNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenIsNot) {
		node := &isNotNode{}
		node.Pos = p.token.pos
		node.left = left
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.acceptNosp(tokenIsNotCi) {
		node := &isNotCiNode{}
		node.Pos = p.token.pos
		node.left = left
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
	Pos
	left  expr
	right expr
}

func (node *addNode) Left() node {
	return node.left
}

func (node *addNode) Right() node {
	return node.right
}

type subtractNode struct {
	Pos
	left  expr
	right expr
}

func (node *subtractNode) Left() node {
	return node.left
}

func (node *subtractNode) Right() node {
	return node.right
}

// expr5 := expr6 1*( "+" expr6 ) /
//          expr6 1*( "-" expr6 ) /
//          expr6
func parseExpr5(p *parser) (expr, bool) {
	left, ok := parseExpr6(p)
	if !ok {
		return nil, false
	}
	for {
		if p.acceptNosp(tokenPlus) {
			node := &addNode{}
			node.Pos = p.token.pos
			node.left = left
			right, ok := parseExpr6(p)
			if !ok {
				return nil, false
			}
			node.right = right
			left = node
		} else if p.acceptNosp(tokenMinus) {
			node := &subtractNode{}
			node.Pos = p.token.pos
			node.left = left
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
	Pos
	left  expr
	right expr
}

func (node *multiplyNode) Left() node {
	return node.left
}

func (node *multiplyNode) Right() node {
	return node.right
}

type divideNode struct {
	Pos
	left  expr
	right expr
}

func (node *divideNode) Left() node {
	return node.left
}

func (node *divideNode) Right() node {
	return node.right
}

type remainderNode struct {
	Pos
	left  expr
	right expr
}

func (node *remainderNode) Left() node {
	return node.left
}

func (node *remainderNode) Right() node {
	return node.right
}

// expr6 := expr7 1*( "*" expr7 ) /
//          expr7 1*( "/" expr7 ) /
//          expr7 1*( "%" expr7 ) /
//          expr7
func parseExpr6(p *parser) (expr, bool) {
	left, ok := parseExpr7(p)
	if !ok {
		return nil, false
	}
	for {
		if p.acceptNosp(tokenStar) {
			node := &multiplyNode{}
			node.Pos = p.token.pos
			node.left = left
			right, ok := parseExpr7(p)
			if !ok {
				return nil, false
			}
			node.right = right
			left = node
		} else if p.acceptNosp(tokenSlash) {
			node := &divideNode{}
			node.Pos = p.token.pos
			node.left = left
			right, ok := parseExpr7(p)
			if !ok {
				return nil, false
			}
			node.right = right
			left = node
		} else if p.acceptNosp(tokenPercent) {
			node := &remainderNode{}
			node.Pos = p.token.pos
			node.left = left
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
	Pos
	left expr
}

func (node *notNode) Value() node {
	return node.left
}

type minusNode struct {
	Pos
	left expr
}

func (node *minusNode) Value() node {
	return node.left
}

type plusNode struct {
	Pos
	left expr
}

func (node *plusNode) Value() node {
	return node.left
}

// expr7 := "!" expr7 /
//          "-" expr7 /
//          "+" expr7 /
//          expr8
func parseExpr7(p *parser) (expr, bool) {
	if p.acceptNosp(tokenNot) {
		node := &notNode{}
		node.Pos = p.token.pos
		left, ok := parseExpr7(p)
		if !ok {
			return nil, false
		}
		node.left = left
		return node, true
	} else if p.acceptNosp(tokenMinus) {
		node := &minusNode{}
		node.Pos = p.token.pos
		left, ok := parseExpr7(p)
		if !ok {
			return nil, false
		}
		node.left = left
		return node, true
	} else if p.acceptNosp(tokenPlus) {
		node := &plusNode{}
		node.Pos = p.token.pos
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
	Pos
	left  expr
	rlist []expr
}

type callNode struct {
	Pos
	left  expr
	rlist []expr
}

type subscriptNode struct {
	Pos
	left  expr
	right expr
}

func (node *subscriptNode) Left() node {
	return node.left
}

func (node *subscriptNode) Right() node {
	return node.right
}

type dotNode struct {
	Pos
	left  expr
	right expr
}

func (node *dotNode) Left() node {
	return node.left
}

func (node *dotNode) Right() node {
	return node.right
}

type identifierNode struct {
	Pos
	value string
}

// expr8 := expr9 1*( "[" expr1 "]" ) /
//          expr9 1*( "[" expr1 ":" expr1 "]" ) /
//          expr9 1*( "." identifier ) /
//          expr9 1*( "(" [ expr1 *( "," expr1) [ "," ] ] ")" ) /
//          expr9
func parseExpr8(p *parser) (expr, bool) {
	left, ok := parseExpr9(p)
	if !ok {
		return nil, false
	}
	for {
		if p.accept(tokenSqOpen) {
			npos := p.token.pos
			if p.acceptNosp(tokenColon) {
				node := &sliceNode{}
				node.Pos = npos
				node.left = left
				node.rlist = []expr{nil, nil}
				if p.peekNosp().typ != tokenSqClose {
					expr, ok := parseExpr1(p)
					if !ok {
						return nil, false
					}
					node.rlist[1] = expr
				}
				if !p.acceptNosp(tokenSqClose) {
					p.emitErrorf("")
					return nil, false
				}
				left = node
			} else {
				right, ok := parseExpr1(p)
				if !ok {
					return nil, false
				}
				if p.acceptNosp(tokenColon) {
					node := &sliceNode{}
					node.Pos = npos
					node.left = left
					node.rlist = []expr{right, nil}
					if p.peekNosp().typ != tokenSqClose {
						expr, ok := parseExpr1(p)
						if !ok {
							return nil, false
						}
						node.rlist[1] = expr
					}
					if !p.acceptNosp(tokenSqClose) {
						p.emitErrorf("")
						return nil, false
					}
					left = node
				} else {
					node := &subscriptNode{}
					node.Pos = npos
					node.left = left
					node.right = right
					if p.acceptNosp(tokenSqClose) {
						p.emitErrorf("")
						return nil, false
					}
					left = node
				}
			}

		} else if p.accept(tokenPOpen) {
			node := &callNode{}
			node.Pos = p.token.pos
			node.left = left
			node.rlist = make([]expr, 8)
			if !p.acceptNosp(tokenPClose) {
				for {
					arg, ok := parseExpr1(p)
					if !ok {
						return nil, false
					}
					node.rlist = append(node.rlist, arg)
					if p.acceptNosp(tokenComma) {
						if p.acceptNosp(tokenPClose) {
							break
						}
					} else if p.acceptNosp(tokenPClose) {
						break
					} else {
						p.emitErrorf("")
						return nil, false
					}
				}
			}
			left = node

		} else if p.accept(tokenDot) {
			dot := p.token
			if !p.acceptNosp(tokenIdentifier) {
				p.emitErrorf("")
				return nil, false
			}
			node := &dotNode{}
			node.Pos = dot.pos
			node.left = left
			node.right = &identifierNode{p.token.pos, p.token.val}

		} else {
			break
		}
	}
	return left, true
}

type numberNode struct {
	Pos
	value string
}

type stringNode struct {
	Pos
	value vainString
}

type listNode struct {
	Pos
	value []expr
}

type dictionaryNode struct {
	Pos
	value [][]expr
}

type optionNode struct {
	Pos
	value string
}

type envNode struct {
	Pos
	value string
}

type regNode struct {
	Pos
	value string
}

// expr9: number
//        "string"
//        'string'
//        [expr1, ...]
//        {expr1: expr1, ...}
//        &option
//        (expr1)
//        func (arg: typ, ...) expr1
//        func (arg: typ, ...) { statementOrExpression }
//        func name(arg: typ, ...) expr1
//        func name(arg: typ, ...) { statementOrExpression }
//        variable
//        $VAR
//        @r
func parseExpr9(p *parser) (expr, bool) {
	if p.acceptNosp(tokenNumber) {
		node := &numberNode{}
		node.Pos = p.token.pos
		node.value = p.token.val
		return node, true

	} else if p.acceptNosp(tokenString) {
		node := &stringNode{}
		node.Pos = p.token.pos
		node.value = vainString(p.token.val)
		return node, true

	} else if p.acceptNosp(tokenSqOpen) {
		node := &listNode{}
		node.Pos = p.token.pos
		node.value = make([]expr, 0, 16)
		if !p.acceptNosp(tokenSqClose) {
			for {
				expr, ok := parseExpr1(p)
				if !ok {
					return nil, false
				}
				node.value = append(node.value, expr)
				if p.acceptNosp(tokenComma) {
					if p.acceptNosp(tokenSqClose) {
						break
					}
				} else if p.acceptNosp(tokenSqClose) {
					break
				} else {
					p.emitErrorf("")
					return nil, false
				}
			}
		}
		return node, true

	} else if p.acceptNosp(tokenCOpen) {
		npos := p.token.pos
		var m [][]expr
		if !p.acceptNosp(tokenCClose) {
			m := make([][]expr, 0, 16)
			for {
				pair := []expr{nil, nil}
				if p.acceptNosp(tokenCClose) {
					break
				}
				t1 := p.nextNosp()
				t2 := p.nextNosp()
				if p.canBeIdentifier(t1) && t2.typ == tokenColon {
					pair[0] = &stringNode{p.token.pos, vainString(p.token.val)}
					p.nextNosp()
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
					if !p.acceptNosp(tokenColon) {
						p.emitErrorf("")
						return nil, false
					}
					right, ok := parseExpr1(p)
					if !ok {
						return nil, false
					}
					pair[0] = left
					pair[1] = right
				}
				m = append(m, pair)
			}
		}
		node := &dictionaryNode{}
		node.Pos = npos
		node.value = m
		return node, true

	} else if p.acceptNosp(tokenPOpen) {
		node, ok := parseExpr1(p)
		if !ok {
			return nil, false
		}
		if !p.acceptNosp(tokenPClose) {
			p.emitErrorf("")
			return nil, false
		}
		return node, true

	} else if p.acceptNosp(tokenFunc) {
		p.backup(p.token)
		return parseFunc(p)

	} else if p.acceptNosp(tokenOption) {
		node := &optionNode{}
		node.Pos = p.token.pos
		node.value = p.token.val
		return node, true

	} else if p.acceptNosp(tokenIdentifier) {
		node := &identifierNode{}
		node.Pos = p.token.pos
		node.value = p.token.val
		return node, true

	} else if p.canBeIdentifier(p.peekNosp()) {
		token := p.nextNosp()
		node := &identifierNode{}
		node.Pos = token.pos
		node.value = token.val
		return node, true

	} else if p.acceptNosp(tokenEnv) {
		node := &envNode{}
		node.Pos = p.token.pos
		node.value = p.token.val
		return node, true

	} else if p.acceptNosp(tokenReg) {
		node := &regNode{}
		node.Pos = p.token.pos
		node.value = p.token.val
		return node, true
	}

	p.emitErrorf("")
	return nil, false
}

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
