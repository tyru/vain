package main

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The original idea of lexer implementation is
//   https://talks.golang.org/2011/lex.slide

type lexer struct {
	name    string     // used only for error reports.
	input   string     // the string being scanned.
	start   int        // start position of this item.
	pos     int        // current position in the input.
	width   int        // width of last rune read from input
	prevPos int        // previous position to restore
	tokens  chan token // channel of scanned items.
}

type token struct {
	typ tokenType // The type of this item.
	pos int       // The starting position, in bytes, of this item in the input string.
	val string    // The value of this item.
}

type tokenType int

const (
	tokenError tokenType = iota // error occurred; value is text of error
	tokenEOF
	tokenIdentifier
	tokenComma
	tokenString
	tokenImport
	tokenLeftBrace
	tokenRightBrace
	tokenAs
	tokenFrom
)

type lexStateFn func(*lexer) lexStateFn

func lex(name, input string) *lexer {
	return &lexer{
		name:   name,
		input:  input,
		tokens: make(chan token),
	}
}

// run lexes the input by executing state functions until
// the state is nil.
func (l *lexer) run() {
	for state := lexTop; state != nil; {
		state = state(l)
	}
	close(l.tokens) // No more tokens will be delivered.
}

const eof = -1

func (l *lexer) eof() bool {
	return l.pos >= len(l.input)
}

// next returns the next rune in the input.
func (l *lexer) next() (r rune) {
	if l.eof() {
		return eof
	}
	r, l.width =
		utf8.DecodeRuneInString(l.input[l.pos:])
	l.pos += l.width
	return r
}

func (l *lexer) nextRunBy(pred func(rune) bool) string {
	var builder strings.Builder
	var r rune
	for {
		r = l.next()
		if r == eof {
			return ""
		}
		if !pred(r) {
			l.backup()
			break
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

// ignore skips over the pending input before this point.
func (l *lexer) ignore() {
	l.start = l.pos
}

// ignoreRun skips over the pending input before this point.
func (l *lexer) ignoreRun(valid string) {
	l.acceptRun(valid)
	l.ignore()
}

// ignoreSpaces skips over the pending input before this point.
func (l *lexer) ignoreSpaces() rune {
	l.acceptRun(" \t\r\n")
	l.ignore()
	r := l.next()
	if r == eof {
		return eof
	}
	l.backup()
	return r
}

// backup steps back one rune.
// Can be called only once per call of next.
func (l *lexer) backup() {
	l.pos -= l.width
}

// save saves current position.
func (l *lexer) save() {
	l.prevPos = l.pos
}

// restore restores previous position.
func (l *lexer) restore() {
	l.pos = l.prevPos
}

// peek returns but does not consume
// the next rune in the input.
func (l *lexer) peek() rune {
	r := l.next()
	l.backup()
	return r
}

// accept consumes the next rune
// if it's from the valid set.
func (l *lexer) accept(valid string) bool {
	return l.acceptBy(func(r rune) bool {
		return strings.ContainsRune(valid, r)
	})
}

// acceptBy consumes the next rune
// if it's pred(r) == true
func (l *lexer) acceptBy(pred func(rune) bool) bool {
	r := l.next()
	if r != eof && pred(r) {
		return true
	}
	l.backup()
	return false
}

// acceptRun consumes a run of runes from the valid set.
func (l *lexer) acceptRun(valid string) {
	l.acceptRunBy(func(r rune) bool {
		return strings.ContainsRune(valid, r)
	})
}

// acceptRunBy consumes a run of runes while pred(r) == true
func (l *lexer) acceptRunBy(pred func(rune) bool) {
	for {
		r := l.next()
		if r == eof {
			return
		}
		if !pred(r) {
			l.backup()
			return
		}
	}
}

// acceptKeyword consumes a run of string
func (l *lexer) acceptKeyword(kw string) bool {
	l.save()
	runes := []rune(kw)
	n := len(runes)
	for i := 0; i < n; i++ {
		r := l.next()
		if r == eof || r != runes[i] {
			l.restore()
			return false
		}
	}
	return true
}

// emit passes an token back to the client.
func (l *lexer) emit(t tokenType) {
	l.tokens <- token{t, l.start, l.input[l.start:l.pos]}
	l.start = l.pos
}

// errorf returns an error token and terminates the scan
// by passing back a nil pointer that will be the next
// state, terminating l.run.
func (l *lexer) errorf(format string, args ...interface{}) lexStateFn {
	l.tokens <- token{
		tokenError,
		l.pos,
		fmt.Sprintf(format, args...),
	}
	return nil
}

func isAlphaNumeric(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func lexTop(l *lexer) lexStateFn {
	if l.ignoreSpaces() == eof {
		l.emit(tokenEOF)
		return nil
	}

	// Statements begins with alpha numeric keyword.
	// Try to parse statements.
	if l.acceptBy(isAlphaNumeric) {
		l.backup()
		l.save()
		word := l.nextRunBy(isAlphaNumeric)
		switch word {
		case "import":
			l.emit(tokenImport)
			return lexImport
		}
		l.restore()
	}

	// If not statements, try to parse expression.

	// TODO
	l.next()

	return lexTop
}

func lexImport(l *lexer) lexStateFn {
	if l.ignoreSpaces() == eof {
		return l.errorf("unexpected EOF in import statement")
	}

	// left brace
	brace := false
	if l.accept("{") {
		l.emit(tokenLeftBrace)
		brace = true
	}

	if l.ignoreSpaces() == eof {
		return l.errorf("unexpected EOF in import statement")
	}

	for {
		// import identifier
		if acceptImportFunction(l) {
			l.emit(tokenIdentifier)
		} else {
			return l.errorf("expected package name in import statement")
		}

		if l.ignoreSpaces() == eof {
			return l.errorf("unexpected EOF in import statement")
		}

		// as
		if l.acceptKeyword("as") {
			l.emit(tokenAs)

			if l.ignoreSpaces() == eof {
				return l.errorf("unexpected EOF in import statement")
			}

			// import function
			if acceptImportFunction(l) {
				l.emit(tokenIdentifier)
			} else {
				return l.errorf("expected package name in import statement")
			}
		}

		if l.ignoreSpaces() == eof {
			return l.errorf("unexpected EOF in import statement")
		}

		// right brace
		if brace && l.accept("}") {
			l.emit(tokenRightBrace)
			break
		}

		// if found comma, continue to the next import function
		if l.accept(",") {
			l.emit(tokenComma)
			if l.ignoreSpaces() == eof {
				return l.errorf("unexpected EOF in import statement")
			}
			continue
		}
		break
	}

	if l.ignoreSpaces() == eof {
		return l.errorf("unexpected EOF in import statement")
	}

	// from
	if !l.acceptKeyword("from") {
		return l.errorf("expected \"from\" keyword in import statement")
	}
	l.emit(tokenFrom)

	if l.ignoreSpaces() == eof {
		return l.errorf("unexpected EOF in import statement")
	}

	// package name
	if err := acceptString(l); err != nil {
		return l.errorf("expected package name in import statement: " + err.Error())
	}
	l.emit(tokenString)

	return lexTop
}

// import { <import function> } from 'pkg'
// import { <import function>, <import function> } from 'pkg'
// import <import function> from 'pkg'
// import <import function> as foo from 'pkg'
func acceptImportFunction(l *lexer) bool {
	if l.accept("*") {
		return true
	} else if l.acceptBy(isAlphaNumeric) {
		l.acceptRunBy(isAlphaNumeric)
		return true
	}
	return false
}

// A string literal is same as Vim script.
// "foo" (foo)
// 'bar' (bar)
// 'foo"bar' (foo"bar)
// "foo'bar" (foo'bar)
// "foo\"bar" (foo"bar)
// 'foo''bar' (foo'bar)
// "foo\\bar" (foo\bar)
// 'foo\bar' (foo\bar)
func acceptString(l *lexer) error {
	l.save()
	var double bool
	if l.accept("'") {
		double = false
	} else if l.accept("\"") {
		double = true
	} else {
		return errors.New("expected string literal")
	}
	for {
		switch l.next() {
		case eof:
			l.restore()
			return errors.New("unexpected EOF in string literal")
		case '\\':
			if double && l.next() == eof {
				l.restore()
				return errors.New("unexpected EOF in string literal")
			}
		case '"':
			if double { // end
				return nil
			}
		case '\'':
			if double {
				continue
			}
			switch l.next() {
			case eof:
				l.restore()
				return errors.New("unexpected EOF in string literal")
			case '\'':
				continue
			default: // end
				l.backup()
				return nil
			}
		}
	}
	// never reach here
}
