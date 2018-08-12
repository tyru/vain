package main

import (
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
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
	token  *token  // read token after accept()
	tokbuf []token // next() doesn't read from lexer.tokens if len(tokbuf) > 0
	start  Pos     // start position of node
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
		msg = fmt.Sprintf("%s: fatal: unexpected token: %+v\n%s",
			p.name, p.token, string(debug.Stack()))
	} else {
		msg = fmt.Sprintf(msg, args...)
	}
	p.nodes <- &errorNode{err: errors.New(msg), Pos: p.token.pos}
}

// unexpectedEOF is called when tokenEOF was given and it's unexpected.
func (p *parser) unexpectedEOF() {
	p.nodes <- &errorNode{
		err: errors.New("unexpected EOF"),
		Pos: p.token.pos,
	}
}

// tokenError is called when tokenError was given.
func (p *parser) tokenError() {
	p.nodes <- &errorNode{
		err: errors.New(p.token.val),
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

func (p *parser) accept(typ tokenType) bool {
	t := p.next()
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
		return parseFunc(p)
	}

	// Expression
	return parseExpr(p)
}

type importStatement struct {
	Pos
	brace  bool
	fnlist [][]string
	pkg    string
}

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
	pkg, ok := evalString(p, p.token)
	if !ok {
		return nil, false
	}

	stmt := &importStatement{p.start, brace, fnlist, pkg}
	return stmt, true
}

func evalString(p *parser, t *token) (string, bool) {
	// single quote
	if t.val[0] == '\'' {
		return strings.Replace(t.val[1:len(t.val)-1], "''", "'", -1), true
	}
	// double quote
	s := []rune(t.val[1 : len(t.val)-1])
	var result strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
			if i >= len(s) {
				p.emitErrorf("")
				return "", false
			}
			switch s[i] {
			case 'b':
				result.WriteRune('\x16')
			case 'e':
				result.WriteRune('\x27')
			case 'f':
				result.WriteRune('\x0C')
			case 'n':
				result.WriteRune('\x15')
			case 'r':
				result.WriteRune('\x0D')
			case 't':
				result.WriteRune('\x05')
			case 'X', 'x', 'U', 'u': // Hex, Unicode
				// TODO
			case '0', '1', '2', '3', '4', '5', '6', '7': // Octal
				// TODO
			case '<': // Special key, e.g.: "\<C-W>"
				// TODO
			}
		default:
			result.WriteRune(s[i])
		}
	}
	return result.String(), true
}

// import { <import function> } from 'pkg'
// import { <import function>, <import function> } from 'pkg'
// import <import function> from 'pkg'
// import <import function> as foo from 'pkg'
func parseImportFunctionList(p *parser) (bool, [][]string, bool) {
	var brace bool
	if p.accept(tokenCOpen) {
		brace = true
	}

	fnlist := make([][]string, 0, 1)
	for {
		if !p.accept(tokenIdentifier) && !p.accept(tokenStar) {
			p.emitErrorf("")
			return false, nil, false
		}
		orig := p.token.val
		to := orig

		if p.accept(tokenAs) {
			if !p.accept(tokenIdentifier) && !p.accept(tokenStar) {
				p.emitErrorf("")
				return false, nil, false
			}
			to = p.token.val
		}
		fnlist = append(fnlist, []string{orig, to})

		if brace && p.accept(tokenCClose) {
			break
		}
		if p.accept(tokenFrom) {
			p.backup(p.token)
			break
		}
		if p.accept(tokenComma) {
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
		mods, ok = parseModifiers(p)
		if !ok {
			return nil, false
		}
	}

	// Function name (if empty, this is functionExpression not functionStatement)
	if p.accept(tokenIdentifier) {
		name = p.token.val
	}

	var ok bool
	args, ok = parseCallSignature(p)
	if !ok {
		return nil, false
	}

	// Body
	body = make([]node, 0, 32)
	if p.accept(tokenCOpen) {
		bodyIsStmt = true
		for {
			if p.accept(tokenCClose) {
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
	if !p.accept(tokenLt) {
		p.emitErrorf("")
		return nil, false
	}
	mods := make([]string, 0, 8)
	for {
		if !p.accept(tokenIdentifier) {
			p.emitErrorf("")
			return nil, false
		}
		mods = append(mods, p.token.val)
		if p.accept(tokenComma) {
			continue
		}
		if p.accept(tokenGt) {
			break
		}
		p.emitErrorf("")
		return nil, false
	}
	return mods, true
}

func parseCallSignature(p *parser) ([]argument, bool) {
	var args []argument

	if !p.accept(tokenPOpen) {
		p.emitErrorf("")
		return nil, false
	}
	for {
		if p.accept(tokenPClose) {
			break
		}
		arg, ok := parseArgument(p)
		if !ok {
			return nil, false
		}
		args = append(args, *arg)
		if !p.accept(tokenComma) {
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

	if !p.accept(tokenIdentifier) {
		p.emitErrorf("")
		return nil, false
	}
	name = p.token.val

	if p.accept(tokenColon) {
		var ok bool
		typ, ok = parseType(p)
		if !ok {
			return nil, false
		}
		return &argument{name, typ, nil}, true
	}

	// name = defaultValue
	if p.accept(tokenEqual) {
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

// expr1 := expr2 [ "?" expr1 ":" expr1 ]
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

type orNode struct {
	Pos
	left  expr
	right expr
}

// expr2 := expr3 *( "||" expr3 )
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

// expr3 := expr4 *( "&&" expr4 )
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

type equalCiNode struct {
	Pos
	left  expr
	right expr
}

type nequalNode struct {
	Pos
	left  expr
	right expr
}

type nequalCiNode struct {
	Pos
	left  expr
	right expr
}

type greaterNode struct {
	Pos
	left  expr
	right expr
}

type greaterCiNode struct {
	Pos
	left  expr
	right expr
}

type gequalNode struct {
	Pos
	left  expr
	right expr
}

type gequalCiNode struct {
	Pos
	left  expr
	right expr
}

type smallerNode struct {
	Pos
	left  expr
	right expr
}

type smallerCiNode struct {
	Pos
	left  expr
	right expr
}

type sequalNode struct {
	Pos
	left  expr
	right expr
}

type sequalCiNode struct {
	Pos
	left  expr
	right expr
}

type matchNode struct {
	Pos
	left  expr
	right expr
}

type matchCiNode struct {
	Pos
	left  expr
	right expr
}

type noMatchNode struct {
	Pos
	left  expr
	right expr
}

type noMatchCiNode struct {
	Pos
	left  expr
	right expr
}

type isNode struct {
	Pos
	left  expr
	right expr
}

type isCiNode struct {
	Pos
	left  expr
	right expr
}

type isNotNode struct {
	Pos
	left  expr
	right expr
}

type isNotCiNode struct {
	Pos
	left  expr
	right expr
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
	if p.accept(tokenEqEq) {
		node := &equalNode{}
		node.Pos = p.token.pos
		node.left = left
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

type subtractNode struct {
	Pos
	left  expr
	right expr
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
		if p.accept(tokenPlus) {
			node := &addNode{}
			node.Pos = p.token.pos
			node.left = left
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

type divideNode struct {
	Pos
	left  expr
	right expr
}

type remainderNode struct {
	Pos
	left  expr
	right expr
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
		if p.accept(tokenStar) {
			node := &multiplyNode{}
			node.Pos = p.token.pos
			node.left = left
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

type notNode struct {
	Pos
	left expr
}

type minusNode struct {
	Pos
	left expr
}

type plusNode struct {
	Pos
	left expr
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

type dotNode struct {
	Pos
	left  expr
	right expr
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
			if p.accept(tokenColon) {
				node := &sliceNode{}
				node.Pos = npos
				node.left = left
				node.rlist = []expr{nil, nil}
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
				if p.accept(tokenColon) {
					node := &sliceNode{}
					node.Pos = npos
					node.left = left
					node.rlist = []expr{right, nil}
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
					if p.accept(tokenSqClose) {
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
			if !p.accept(tokenPClose) {
				for {
					arg, ok := parseExpr1(p)
					if !ok {
						return nil, false
					}
					node.rlist = append(node.rlist, arg)
					if p.accept(tokenComma) {
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
			if !p.accept(tokenIdentifier) {
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
	value string
}

type listNode struct {
	Pos
	value []expr
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

type dictionaryNode struct {
	Pos
	value [][]expr
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
	if p.accept(tokenNumber) {
		node := &numberNode{}
		node.Pos = p.token.pos
		node.value = p.token.val
		return node, true

	} else if p.accept(tokenString) {
		node := &stringNode{}
		node.Pos = p.token.pos
		node.value = p.token.val
		return node, true

	} else if p.accept(tokenSqOpen) {
		node := &listNode{}
		node.Pos = p.token.pos
		node.value = make([]expr, 0, 16)
		if !p.accept(tokenSqClose) {
			for {
				expr, ok := parseExpr1(p)
				if !ok {
					return nil, false
				}
				node.value = append(node.value, expr)
				if p.accept(tokenComma) {
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
		if !p.accept(tokenCClose) {
			m := make([][]expr, 0, 16)
			for {
				pair := []expr{nil, nil}
				if p.accept(tokenCClose) {
					break
				}
				t1 := p.next()
				t2 := p.next()
				if p.canBeIdentifier(t1) && t2.typ == tokenColon {
					pair[0] = &stringNode{p.token.pos, p.token.val}
					p.next()
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
					if !p.accept(tokenColon) {
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

	} else if p.accept(tokenPOpen) {
		node, ok := parseExpr1(p)
		if !ok {
			return nil, false
		}
		if !p.accept(tokenPClose) {
			p.emitErrorf("")
			return nil, false
		}
		return node, true

	} else if p.accept(tokenFunc) {
		p.backup(p.token)
		return parseFunc(p)

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
