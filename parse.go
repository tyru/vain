package main

import (
	"errors"
	"fmt"
	"io"
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

func (p *parser) run() {
	for state := parseTop; state != nil; {
		state = state(p)
	}
	close(p.nodes) // No more nodes will be delivered.
}

// lexBug is called when unexpected token was given.
// This is normally lexer's bug.
func (p *parser) lexBug() {
	if p.token.typ == tokenEOF {
		p.unexpectedEOF()
		return
	}
	if p.token.typ == tokenError {
		p.tokenError()
		return
	}
	_, file, line, ok := runtime.Caller(1)
	var msg string
	if ok {
		msg = fmt.Sprintf("fatal: unexpected token was given. this may be a lexer bug:\n  %s (line %d)", file, line)
	} else {
		msg = "fatal: unexpected token was given. this may be a lexer bug"
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
func (p *parser) tokenError() {
	p.nodes <- &errorNode{
		err: errors.New("lex error: " + p.token.val),
		pos: p.start,
	}
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
			return p.errorf(token.val)
		case tokenImport:
			return parseImportStatement
		default:
			return p.errorf("unimplemented token: token = %+v", token)
		}
	}
}

type pos int

func (p pos) Position() pos {
	return p
}

type node interface {
	WriteTo(w io.Writer) (int64, error)
}

type errorNode struct {
	node
	pos
	err error
}

func (node *errorNode) WriteTo(w io.Writer) (int64, error) {
	return 0, node.err
}

type statement interface {
	node
}

func parseImportStatement(p *parser) parseStateFn {
	brace, fnlist, ok := parseImportFunctionList(p)
	if !ok {
		return nil
	}

	if !p.accept(tokenFrom) {
		p.lexBug()
		return nil
	}

	if !p.accept(tokenString) {
		p.lexBug()
		return nil
	}
	pkg, ok := evalString(p, p.token)
	if !ok {
		return nil
	}

	p.emit(&importStatement{
		pos:    p.start,
		brace:  brace,
		fnlist: fnlist,
		pkg:    pkg,
	})
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
				p.lexBug()
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

func parseImportFunctionList(p *parser) (bool, [][]string, bool) {
	var brace bool
	if p.accept(tokenLeftBrace) {
		brace = true
	}

	fnlist := make([][]string, 0, 1)
	for {
		if p.accept(tokenFrom) {
			// End parsing function list.
			p.backup()
			break
		}
		if brace && p.accept(tokenRightBrace) {
			// End parsing function list.
			break
		}

		if !p.accept(tokenIdentifier) {
			p.lexBug()
			return false, nil, false
		}
		orig := p.token.val
		to := orig

		if p.accept(tokenAs) {
			if !p.accept(tokenIdentifier) {
				p.lexBug()
				return false, nil, false
			}
			to = p.token.val
		}
		fnlist = append(fnlist, []string{orig, to})

		p.accept(tokenComma)
	}
	return brace, fnlist, true
}

type importStatement struct {
	statement
	pos
	brace  bool
	fnlist [][]string
	pkg    string
}

func (stmt *importStatement) WriteTo(w io.Writer) (int64, error) {
	var builder strings.Builder
	builder.WriteString("import ")
	if stmt.brace {
		builder.WriteString("{ ")
	}
	first := true
	for _, pair := range stmt.fnlist {
		if !first {
			builder.WriteString(", ")
		}
		if pair[0] == pair[1] {
			builder.WriteString(pair[0])
		} else {
			builder.WriteString(pair[0] + " as " + pair[1])
		}
		first = false
	}
	if stmt.brace {
		builder.WriteString(" } ")
	}
	builder.WriteString(" from " + stmt.pkg + "\n")
	return strings.NewReader(builder.String()).WriteTo(w)
}

type expr interface {
	node
}
