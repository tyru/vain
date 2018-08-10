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

type errorNode struct {
	pos
	err error
}

type importStatement struct {
	pos
	brace  bool
	fnlist [][]string
	pkg    string
}

func (p *parser) run() {
	for state := parseTop; state != nil; {
		state = state(p)
	}
	close(p.nodes) // No more nodes will be delivered.
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
}

// unexpectedEOF is called when tokenEOF was given and it's unexpected.
func (p *parser) unexpectedEOF() {
	p.nodes <- &errorNode{
		err: errors.New("unexpected EOF"),
		pos: p.start,
	}
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
// state, terminating l.run.
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
		default:
			return p.errorf("unimplemented token: token = %+v", token)
		}
	}
}

func parseImportStatement(p *parser) parseStateFn {
	brace, fnlist, ok := parseImportFunctionList(p)
	if !ok {
		return nil
	}

	if !p.accept(tokenFrom) {
		p.emitErrorf("")
		return nil
	}

	if !p.accept(tokenString) {
		p.emitErrorf("")
		return nil
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
