package main

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
)

func parse(l *lexer) *parser {
	return &parser{
		lexer: l,
		nodes: make(chan node),
		start: -1,
	}
}

type parser struct {
	lexer     *lexer
	nodes     chan node
	token     *token // read token after accept()
	nextToken *token // next() doesn't read from lexer.tokens if nextToken != nil
	start     pos    // start position of node
}

type nodeType int

type parseStateFn func(*parser) parseStateFn

type pos int

func (p pos) Position() pos {
	return p
}

type node interface {
	Position() pos
}

type expr interface {
	node
}

type statement interface {
	node
}

type errorNode struct {
	pos
	err error
}

func (p *parser) Run() {
	for state := parseTop; state != nil; {
		state = state(p)
	}
	close(p.nodes) // No more nodes will be delivered.
}

// emitErrorf is called when unexpected token was given.
// This is normally lexer's bug.
func (p *parser) emitErrorf(msg string, args ...interface{}) parseStateFn {
	if p.token.typ == tokenEOF {
		return p.unexpectedEOF()
	}
	if p.token.typ == tokenError {
		return p.tokenError()
	}
	if msg == "" {
		_, file, line, ok := runtime.Caller(1)
		if ok {
			msg = fmt.Sprintf("fatal: unexpected token was given. this may be a lexer bug:\n  %s (line %d)", file, line)
		} else {
			msg = "fatal: unexpected token was given. this may be a lexer bug"
		}
	} else {
		msg = fmt.Sprintf(msg, args...)
	}
	// TODO: show caller functions, and so on
	p.nodes <- &errorNode{err: errors.New(msg), pos: p.start}
	return nil
}

// unexpectedEOF is called when tokenEOF was given and it's unexpected.
func (p *parser) unexpectedEOF() parseStateFn {
	p.nodes <- &errorNode{
		err: errors.New("unexpected EOF"),
		pos: p.start,
	}
	return nil
}

// tokenError is called when tokenError was given.
func (p *parser) tokenError() parseStateFn {
	p.nodes <- &errorNode{
		err: errors.New(p.token.val),
		pos: p.start,
	}
	return nil
}

func (p *parser) next() *token {
	var t *token
	if p.nextToken != nil {
		t = p.nextToken
	} else {
		tt := <-p.lexer.tokens
		t = &tt
	}
	p.nextToken = nil
	p.token = t
	if p.start < 0 {
		p.start = pos(t.pos)
	}
	return t
}

func (p *parser) backup() {
	p.nextToken = p.token
}

func (p *parser) accept(typ tokenType) bool {
	if p.next().typ == typ {
		return true
	}
	p.backup()
	return false
}

// errorf returns an error token and terminates the scan
// by passing back a nil pointer that will be the next
// state, terminating l.Run.
func (p *parser) errorf(format string, args ...interface{}) parseStateFn {
	p.emit(&errorNode{err: fmt.Errorf(format, args...), pos: p.start})
	return nil
}

// emit passes an node back to the client.
func (p *parser) emit(node node) {
	p.nodes <- node
	p.start = -1
}

func parseTop(p *parser) parseStateFn {
	for {
		token := p.next()
		switch token.typ {
		case tokenEOF:
			return nil
		case tokenError:
			return p.tokenError()
		case tokenImport:
			return parseImportStatement
		case tokenFunc:
			return parseFuncStatement
		default:
			return p.errorf("unimplemented token: token = %+v", token)
		}
	}
}

type importStatement struct {
	pos
	brace  bool
	fnlist [][]string
	pkg    string
}

func parseImportStatement(p *parser) parseStateFn {
	brace, fnlist, ok := parseImportFunctionList(p)
	if !ok {
		return nil
	}

	if !p.accept(tokenFrom) {
		return p.emitErrorf("")
	}

	if !p.accept(tokenString) {
		return p.emitErrorf("")
	}
	pkg, ok := evalString(p, p.token)
	if !ok {
		return nil
	}

	p.emit(&importStatement{p.start, brace, fnlist, pkg})
	return parseTop
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
	if p.accept(tokenLeftBrace) {
		brace = true
	}

	fnlist := make([][]string, 0, 1)
	for {
		if !p.accept(tokenIdentifier) && !p.accept(tokenAsterisk) {
			p.emitErrorf("")
			return false, nil, false
		}
		orig := p.token.val
		to := orig

		if p.accept(tokenAs) {
			if !p.accept(tokenIdentifier) && !p.accept(tokenAsterisk) {
				p.emitErrorf("")
				return false, nil, false
			}
			to = p.token.val
		}
		fnlist = append(fnlist, []string{orig, to})

		if brace && p.accept(tokenRightBrace) {
			break
		}
		if p.accept(tokenFrom) {
			p.backup()
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

type funcStatement struct {
	pos
	mods []string
	name string
	args []argument
	body []node
}

type argument struct {
	name       string
	typ        string
	defaultVal expr
}

func parseFuncStatement(p *parser) parseStateFn {
	var mods []string
	var name string
	var args []argument
	var body []node

	// Modifiers
	if p.accept(tokenLeftAngleBracket) {
		p.backup()
		var ok bool
		mods, ok = parseModifiers(p)
		if !ok {
			return nil
		}
	}

	// Function name
	if !p.accept(tokenIdentifier) {
		return p.emitErrorf("")
	}
	name = p.token.val

	// Arguments
	if !p.accept(tokenLeftParen) {
		return p.emitErrorf("")
	}
	for {
		if p.accept(tokenRightParen) {
			break
		}
		arg, ok := parseArgument(p)
		if !ok {
			return nil
		}
		args = append(args, *arg)
		if !p.accept(tokenComma) {
			return p.emitErrorf("")
		}
	}

	if !p.accept(tokenLeftBrace) {
		return p.emitErrorf("")
	}

	// TODO parse expr

	if !p.accept(tokenRightBrace) {
		return p.emitErrorf("")
	}

	p.emit(&funcStatement{p.start, mods, name, args, body})
	return parseTop
}

func parseModifiers(p *parser) ([]string, bool) {
	if !p.accept(tokenLeftAngleBracket) {
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
		if p.accept(tokenRightAngleBracket) {
			break
		}
		p.emitErrorf("")
		return nil, false
	}
	return mods, true
}

// name: type
// name = defaultValue
func parseArgument(p *parser) (*argument, bool) {
	var name string
	var typ string

	if !p.accept(tokenIdentifier) {
		p.emitErrorf("")
		return nil, false
	}
	name = p.token.val

	// name: type
	if p.accept(tokenColon) {
		if !p.accept(tokenIdentifier) {
			p.emitErrorf("")
			return nil, false
		}
		typ = p.token.val
		return &argument{name, typ, nil}, true
	}

	// name = defaultValue
	if p.accept(tokenEqual) {
		// TODO parse expr
		if !p.accept(tokenIdentifier) {
			p.emitErrorf("")
			return nil, false
		}
		return &argument{name, "", nil}, true
	}

	p.emitErrorf("")
	return nil, false
}
