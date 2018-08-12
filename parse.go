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
	var t token
	if len(p.tokbuf) > 0 {
		t = p.tokbuf[len(p.tokbuf)-1]
		p.tokbuf = p.tokbuf[:len(p.tokbuf)-1]
	} else {
		t = <-p.lexer.tokens
	}
	p.token = &t
	if p.start < 0 {
		p.start = Pos(t.pos)
	}
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

// statementOrExpression
func parseStmtOrExpr(p *parser) (node, bool) {
	p.acceptSpaces()
	if p.accept(tokenEOF) {
		return nil, false
	}
	if p.accept(tokenError) {
		p.tokenError()
		return nil, false
	}

	// Statement
	if p.accept(tokenImport) {
		p.backup(p.token)
		return parseImportStatement(p)
	}
	if p.accept(tokenFunc) {
		p.backup(p.token)
		return acceptFunction(p)
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

// importStatement := "import" <importFunctionList> "from" string
func parseImportStatement(p *parser) (*importStatement, bool) {
	if !p.accept(tokenImport) {
		p.emitErrorf("")
		return nil, false
	}

	brace, fnlist, ok := parseImportFunctionList(p)
	if !ok {
		return nil, false
	}

	if !p.accept(tokenFrom) {
		p.emitErrorf("")
		return nil, false
	}

	if !p.accept(tokenString) {
		p.emitErrorf("")
		return nil, false
	}
	pkg := vainString(p.token.val)

	stmt := &importStatement{p.start, brace, fnlist, pkg}
	return stmt, true
}

// importFunctionList = "{" *LF importFunctionListBody *LF "}" / importFunctionListBody
// importFunctionListBody := importFunctionListItem *( *LF "," *LF importFunctionListItem )
// importFunctionListItem := ( identifier | "*" ) [ "as" *LF identifier ]
func parseImportFunctionList(p *parser) (bool, [][]string, bool) {
	var brace bool
	if p.accept(tokenCOpen) {
		brace = true
		p.acceptSpaces()
	}

	// importFunctionListBody
	fnlist := make([][]string, 0, 1)
	for {
		if !p.accept(tokenIdentifier) && !p.accept(tokenStar) {
			p.emitErrorf("")
			return false, nil, false
		}
		orig := p.token.val
		to := orig

		if p.accept(tokenAs) {
			p.acceptSpaces()
			if !p.accept(tokenIdentifier) {
				p.emitErrorf("")
				return false, nil, false
			}
			to = p.token.val
		}
		fnlist = append(fnlist, []string{orig, to})

		p.acceptSpaces()
		if !p.accept(tokenComma) {
			break
		}
		p.acceptSpaces()
	}

	if brace && !p.accept(tokenCClose) {
		p.emitErrorf("")
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

// function :=
//        "func" [ functionModifiers ] [ identifier ] functionCallSignature expr1 /
//        "func" [ functionModifiers ] [ identifier ] functionCallSignature "{" *LF *( statementOrExpression *LF ) "}" /
func acceptFunction(p *parser) (*funcStmtOrExpr, bool) {
	if !p.accept(tokenFunc) {
		p.emitErrorf("")
		return nil, false
	}

	var mods []string
	var name string
	var args []argument
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
	args, ok = acceptFunctionCallSignature(p)
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

	f := &funcStmtOrExpr{p.start, mods, name, args, bodyIsStmt, body}
	return f, true
}

// functionModifiers := "<" *LF identifier *( *LF "," *LF identifier ) *LF ">"
func acceptModifiers(p *parser) ([]string, bool) {
	if !p.accept(tokenLt) {
		p.emitErrorf("")
		return nil, false
	}
	mods := make([]string, 0, 8)
	p.acceptSpaces()
	for {
		if !p.accept(tokenIdentifier) {
			p.emitErrorf("")
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

// functionCallSignature := "(" *LF *( functionArgument *LF "," *LF ) ")"
func acceptFunctionCallSignature(p *parser) ([]argument, bool) {
	if !p.accept(tokenPOpen) {
		p.emitErrorf("")
		return nil, false
	}
	p.acceptSpaces()

	var args []argument
	if !p.accept(tokenPClose) {
		args := make([]argument, 0, 8)
		for {
			arg, ok := acceptFunctionArgument(p)
			if !ok {
				return nil, false
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
	return args, true
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
		p.emitErrorf("")
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

	p.emitErrorf("")
	return nil, false
}

// TODO: Complex type like array, dictionary, generics...
// type := identifier
func acceptType(p *parser) (string, bool) {
	if !p.accept(tokenIdentifier) {
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

// expr1 := expr2 [ "?" expr1 *LF ":" expr1 ]
func parseExpr1(p *parser) (expr, bool) {
	left, ok := parseExpr2(p)
	if !ok {
		return nil, false
	}
	if p.accept(tokenQuestion) {
		node := &ternaryNode{}
		node.cond = left
		expr, ok := parseExpr1(p)
		if !ok {
			return nil, false
		}
		node.left = expr
		p.acceptSpaces()
		if !p.accept(tokenColon) {
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

// expr2 := expr3 *( "||" *LF expr3 )
func parseExpr2(p *parser) (expr, bool) {
	left, ok := parseExpr3(p)
	if !ok {
		return nil, false
	}
	for {
		if p.accept(tokenOrOr) {
			node := &orNode{}
			node.Pos = p.token.pos
			node.left = left
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

// expr3 := expr4 *( "&&" *LF expr4 )
func parseExpr3(p *parser) (expr, bool) {
	left, ok := parseExpr4(p)
	if !ok {
		return nil, false
	}
	for {
		if p.accept(tokenAndAnd) {
			node := &andNode{}
			node.Pos = p.token.pos
			node.left = left
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
		node := &equalNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenEqEqCi) {
		node := &equalCiNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenNeq) {
		node := &nequalNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenNeqCi) {
		node := &nequalCiNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenGt) {
		node := &greaterNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenGtCi) {
		node := &greaterCiNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenGtEq) {
		node := &gequalNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenGtEqCi) {
		node := &gequalCiNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenLt) {
		node := &smallerNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenLtCi) {
		node := &smallerCiNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenLtEq) {
		node := &sequalNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenLtEqCi) {
		node := &sequalCiNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenMatch) {
		node := &matchNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenMatchCi) {
		node := &matchCiNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenNoMatch) {
		node := &noMatchNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenNoMatchCi) {
		node := &noMatchCiNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenIs) {
		node := &isNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenIsCi) {
		node := &isCiNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenIsNot) {
		node := &isNotNode{}
		node.Pos = p.token.pos
		node.left = left
		p.acceptSpaces()
		right, ok := parseExpr5(p)
		if !ok {
			return nil, false
		}
		node.right = right
		left = node
	} else if p.accept(tokenIsNotCi) {
		node := &isNotCiNode{}
		node.Pos = p.token.pos
		node.left = left
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
			node := &addNode{}
			node.Pos = p.token.pos
			node.left = left
			p.acceptSpaces()
			right, ok := parseExpr6(p)
			if !ok {
				return nil, false
			}
			node.right = right
			left = node
		} else if p.accept(tokenMinus) {
			node := &subtractNode{}
			node.Pos = p.token.pos
			node.left = left
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
			node := &multiplyNode{}
			node.Pos = p.token.pos
			node.left = left
			p.acceptSpaces()
			right, ok := parseExpr7(p)
			if !ok {
				return nil, false
			}
			node.right = right
			left = node
		} else if p.accept(tokenSlash) {
			node := &divideNode{}
			node.Pos = p.token.pos
			node.left = left
			p.acceptSpaces()
			right, ok := parseExpr7(p)
			if !ok {
				return nil, false
			}
			node.right = right
			left = node
		} else if p.accept(tokenPercent) {
			node := &remainderNode{}
			node.Pos = p.token.pos
			node.left = left
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
	if p.accept(tokenNot) {
		node := &notNode{}
		node.Pos = p.token.pos
		left, ok := parseExpr7(p)
		if !ok {
			return nil, false
		}
		node.left = left
		return node, true
	} else if p.accept(tokenMinus) {
		node := &minusNode{}
		node.Pos = p.token.pos
		left, ok := parseExpr7(p)
		if !ok {
			return nil, false
		}
		node.left = left
		return node, true
	} else if p.accept(tokenPlus) {
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
				node := &sliceNode{}
				node.Pos = npos
				node.left = left
				node.rlist = []expr{nil, nil}
				p.acceptSpaces()
				if p.peek().typ != tokenSqClose {
					expr, ok := parseExpr1(p)
					if !ok {
						return nil, false
					}
					node.rlist[1] = expr
				}
				if !p.accept(tokenSqClose) {
					p.emitErrorf("")
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
					node := &sliceNode{}
					node.Pos = npos
					node.left = left
					node.rlist = []expr{right, nil}
					p.acceptSpaces()
					if p.peek().typ != tokenSqClose {
						expr, ok := parseExpr1(p)
						if !ok {
							return nil, false
						}
						node.rlist[1] = expr
					}
					if !p.accept(tokenSqClose) {
						p.emitErrorf("")
						return nil, false
					}
					left = node
				} else {
					node := &subscriptNode{}
					node.Pos = npos
					node.left = left
					node.right = right
					p.acceptSpaces()
					if !p.accept(tokenSqClose) {
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
						p.emitErrorf("")
						return nil, false
					}
				}
			}
			left = node
		} else if p.accept(tokenDot) {
			dot := p.token
			p.acceptSpaces()
			if !p.accept(tokenIdentifier) {
				p.emitErrorf("")
				return nil, false
			}
			node := &dotNode{}
			node.Pos = dot.pos
			node.left = left
			node.right = &identifierNode{p.token.pos, p.token.val}
			left = node
		} else {
			break
		}
	}
	return left, true
}

type literalNode interface {
	Value() string
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

func (node *optionNode) Value() string {
	return node.value[1:]
}

type envNode struct {
	Pos
	value string
}

func (node *envNode) Value() string {
	return node.value[1:]
}

type regNode struct {
	Pos
	value string
}

func (node *regNode) Value() string {
	return node.value[1:]
}

// expr9: number /
//        (string ABNF is too complex! e.g. "string\n", 'str''ing') /
//        "[" *LF *( expr1 *LF "," *LF ) "]" /
//        "{" *LF *( ( identifier | expr1 ) *LF ":" *LF expr1 *LF "," *LF ) "}" /
//        &option /
//        "(" *LF expr1 *LF ")" /
//        function /
//        identifier /
//        identifierLike /
//        $VAR /
//        @r
func parseExpr9(p *parser) (expr, bool) {
	if p.accept(tokenNumber) {
		node := &numberNode{}
		node.Pos = p.token.pos
		node.value = p.token.val
		return node, true

	} else if p.accept(tokenString) {
		node := &stringNode{}
		node.Pos = p.token.pos
		node.value = vainString(p.token.val)
		return node, true

	} else if p.accept(tokenSqOpen) {
		node := &listNode{}
		node.Pos = p.token.pos
		node.value = make([]expr, 0, 16)
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
					p.emitErrorf("")
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
						p.emitErrorf("")
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
		node := &dictionaryNode{}
		node.Pos = npos
		node.value = m
		return node, true

	} else if p.accept(tokenPOpen) {
		p.acceptSpaces()
		node, ok := parseExpr1(p)
		if !ok {
			return nil, false
		}
		p.acceptSpaces()
		if !p.accept(tokenPClose) {
			p.emitErrorf("")
			return nil, false
		}
		return node, true

	} else if p.accept(tokenFunc) {
		p.backup(p.token)
		return acceptFunction(p)

	} else if p.accept(tokenOption) {
		node := &optionNode{}
		node.Pos = p.token.pos
		node.value = p.token.val
		return node, true

	} else if p.accept(tokenIdentifier) {
		node := &identifierNode{}
		node.Pos = p.token.pos
		node.value = p.token.val
		return node, true

	} else if p.canBeIdentifier(p.peek()) {
		token := p.next()
		node := &identifierNode{}
		node.Pos = token.pos
		node.value = token.val
		return node, true

	} else if p.accept(tokenEnv) {
		node := &envNode{}
		node.Pos = p.token.pos
		node.value = p.token.val
		return node, true

	} else if p.accept(tokenReg) {
		node := &regNode{}
		node.Pos = p.token.pos
		node.value = p.token.val
		return node, true
	}

	p.emitErrorf("")
	return nil, false
}

// identifierLike
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
