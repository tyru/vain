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
	start   Pos        // start position of this item.
	pos     Pos        // current position in the input.
	width   int        // width of last rune read from input
	prevPos Pos        // previous position to restore
	tokens  chan token // channel of scanned items.
	line    int        // 1+number of newlines seen
}

type token struct {
	typ  tokenType // The type of this item.
	pos  Pos       // The starting position, in bytes, of this item in the input string.
	val  string    // The value of this item.
	line int       // The line number of this item.
}

// Pos is offset byte position from start of the file.
type Pos int

// Position returns pos itself
func (p Pos) Position() Pos {
	return p
}

type tokenType int

const (
	tokenError tokenType = iota // error occurred; value is text of error
	tokenEOF
	tokenNewline
	tokenIdentifier
	tokenComma
	tokenEqual
	tokenEqEq
	tokenEqEqCi
	tokenColon
	tokenQuestion
	tokenStar
	tokenSlash
	tokenPercent
	tokenSqOpen
	tokenSqClose
	tokenCOpen
	tokenCClose
	tokenPOpen
	tokenPClose
	tokenInt
	tokenFloat
	tokenString
	tokenOption
	tokenEnv
	tokenReg
	tokenBoolean
	tokenNone
	tokenNot
	tokenNeq
	tokenNeqCi
	tokenLt
	tokenLtCi
	tokenLtEq
	tokenLtEqCi
	tokenGt
	tokenGtCi
	tokenGtEq
	tokenGtEqCi
	tokenMatch
	tokenMatchCi
	tokenNoMatch
	tokenNoMatchCi
	tokenIs
	tokenIsCi
	tokenIsNot
	tokenIsNotCi
	tokenOr
	tokenOrOr
	tokenAnd
	tokenAndAnd
	tokenPlus
	tokenMinus
	tokenArrow
	tokenDot
	tokenDotDotDot
	tokenConst
	tokenLet
	tokenFunc
	tokenReturn
	tokenImport
	tokenAs
	tokenFrom
)

type lexStateFn func(*lexer) lexStateFn

func lex(name, input string) *lexer {
	return &lexer{
		name:   name,
		input:  input,
		tokens: make(chan token),
		line:   1,
	}
}

// Run lexes the input by executing state functions until
// the state is nil.
func (l *lexer) Run() {
	for state := lexTop; state != nil; {
		state = state(l)
	}
	close(l.tokens) // No more tokens will be delivered.
}

const eof = -1

func (l *lexer) eof() bool {
	return int(l.pos) >= len(l.input)
}

// next returns the next rune in the input.
func (l *lexer) next() (r rune) {
	if l.eof() {
		return eof
	}
	r, l.width =
		utf8.DecodeRuneInString(l.input[l.pos:])
	l.pos = Pos(int(l.pos) + l.width)
	if r == '\n' {
		l.line++
	}
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
	l.line += strings.Count(l.input[l.start:l.pos], "\n")
}

// ignoreRun skips over the pending input before this point.
func (l *lexer) ignoreRun(valid string) {
	l.acceptRun(valid)
	l.ignore()
}

// acceptSpaces skips over the pending input before this point.
func (l *lexer) acceptSpaces() rune {
	l.acceptRun(" \t\r\n")
	r := l.next()
	if r == eof {
		return eof
	}
	l.backup()
	return r
}

func (l *lexer) emitNewlines() {
	n := strings.Count(l.input[l.start:l.pos], "\n")
	for i := 0; i < n; i++ {
		l.emit(tokenNewline)
	}
}

// backup steps back one rune.
// Can be called only once per call of next.
func (l *lexer) backup() {
	l.pos = Pos(int(l.pos) - l.width)
	// Correct newline count.
	if l.width == 1 && l.input[l.pos] == '\n' {
		l.line--
	}
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
// Check boundary if boundary == true
func (l *lexer) acceptKeyword(kw string, boundary bool) bool {
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
	if boundary {
		// Next thing mustn't be alphanumeric.
		if isAlphaNumeric(l.next()) {
			return false
		}
		l.backup()
	}
	return true
}

// emit passes an token back to the client.
func (l *lexer) emit(t tokenType) {
	tok := token{t, l.start, l.input[l.start:l.pos], l.line}
	l.tokens <- tok
	l.start = l.pos
}

// errorf returns an error token and terminates the scan
// by passing back a nil pointer that will be the next
// state, terminating l.Run.
func (l *lexer) errorf(format string, args ...interface{}) lexStateFn {
	l.tokens <- token{
		tokenError,
		l.pos,
		fmt.Sprintf(format, args...),
		l.line,
	}
	return nil
}

func isWordHead(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isAlpha(r rune) bool {
	return unicode.IsLetter(r)
}

func isNumeric(r rune) bool {
	return unicode.IsDigit(r)
}

func isAlphaNumeric(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func lexTop(l *lexer) lexStateFn {
	if l.acceptSpaces() == eof {
		l.emit(tokenEOF)
		return nil
	}
	l.emitNewlines()
	l.ignore()

	if l.acceptBy(isNumeric) {
		l.backup()
		return lexNumber
	}

	r := l.next()
	switch r {
	case '\'', '"':
		l.backup()
		return lexString
	case '[':
		l.emit(tokenSqOpen)
		return lexTop
	case ']':
		l.emit(tokenSqClose)
		return lexTop
	case '<':
		if l.acceptKeyword("=?", false) {
			l.emit(tokenLtEqCi)
			return lexTop
		}
		if l.accept("=") {
			l.emit(tokenLtEq)
			return lexTop
		}
		if l.accept("?") {
			l.emit(tokenLtCi)
			return lexTop
		}
		l.emit(tokenLt)
		return lexTop
	case '>':
		if l.acceptKeyword("=?", false) {
			l.emit(tokenGtEqCi)
			return lexTop
		}
		if l.accept("=") {
			l.emit(tokenGtEq)
			return lexTop
		}
		if l.accept("?") {
			l.emit(tokenGtCi)
			return lexTop
		}
		l.emit(tokenGt)
		return lexTop
	case '|':
		if l.accept("|") {
			l.emit(tokenOrOr)
			return lexTop
		}
		l.emit(tokenOr)
		return lexTop
	case '&':
		l.backup()
		return lexOption
	case '$':
		l.backup()
		return lexEnv
	case '@':
		l.backup()
		return lexReg
	case '{':
		l.emit(tokenCOpen)
		return lexTop
	case '}':
		l.emit(tokenCClose)
		return lexTop
	case '(':
		l.emit(tokenPOpen)
		return lexTop
	case ')':
		l.emit(tokenPClose)
		return lexTop
	case '!':
		if l.acceptKeyword("~?", false) {
			l.emit(tokenNoMatchCi)
			return lexTop
		}
		if l.accept("~") {
			l.emit(tokenNoMatch)
			return lexTop
		}
		if l.acceptKeyword("=?", false) {
			l.emit(tokenNeqCi)
			return lexTop
		}
		if l.accept("=") {
			l.emit(tokenNeq)
			return lexTop
		}
		l.emit(tokenNot)
		return lexTop
	case '?':
		l.emit(tokenQuestion)
		return lexTop
	case '*':
		l.emit(tokenStar)
		return lexTop
	case '/':
		l.emit(tokenSlash)
		return lexTop
	case '%':
		l.emit(tokenPercent)
		return lexTop
	case ',':
		l.emit(tokenComma)
		return lexTop
	case '=':
		if l.acceptKeyword("~?", false) {
			l.emit(tokenMatchCi)
			return lexTop
		}
		if l.accept("~") {
			l.emit(tokenMatch)
			return lexTop
		}
		if l.acceptKeyword("=?", false) {
			l.emit(tokenEqEqCi)
			return lexTop
		}
		if l.accept("=") {
			l.emit(tokenEqEq)
			return lexTop
		}
		l.emit(tokenEqual)
		return lexTop
	case '+':
		l.emit(tokenPlus)
		return lexTop
	case '-':
		if l.accept(">") {
			l.emit(tokenArrow)
			return lexTop
		}
		l.emit(tokenMinus)
		return lexTop
	case '.':
		if l.acceptKeyword("..", false) {
			l.emit(tokenDotDotDot)
			return lexTop
		}
		l.emit(tokenDot)
		return lexTop
	case ':':
		l.emit(tokenColon)
		return lexTop
	default:
		l.backup()
	}

	// Reserved words
	w := l.nextRunBy(isAlphaNumeric)
	switch w {
	case "is":
		if l.accept("?") {
			l.emit(tokenIsCi)
			return lexTop
		}
		l.emit(tokenIs)
		return lexTop
	case "isnot":
		if l.accept("?") {
			l.emit(tokenIsNotCi)
			return lexTop
		}
		l.emit(tokenIsNot)
		return lexTop
	case "const":
		l.emit(tokenConst)
		return lexTop
	case "let":
		l.emit(tokenLet)
		return lexTop
	case "func":
		l.emit(tokenFunc)
		return lexTop
	case "return":
		l.emit(tokenReturn)
		return lexTop
	case "import":
		l.emit(tokenImport)
		return lexTop
	case "as":
		l.emit(tokenAs)
		return lexTop
	case "from":
		l.emit(tokenFrom)
		return lexTop
	case "true", "false":
		l.emit(tokenBoolean)
		return lexTop
	case "null", "none":
		l.emit(tokenNone)
		return lexTop
	}

	if w != "" {
		l.emit(tokenIdentifier)
		return lexTop
	}

	return l.errorf("unknown token")
}

func lexNumber(l *lexer) lexStateFn {
	if acceptFloat(l) {
		l.emit(tokenFloat)
	} else if acceptInt(l) {
		l.emit(tokenInt)
	} else {
		return l.errorf("expected number literal")
	}
	return lexTop
}

func acceptInt(l *lexer) bool {
	digits := "0123456789"
	if l.accept("0") && l.accept("xX") {
		digits = "0123456789abcdefABCDEF"
	}
	l.acceptRun(digits)
	// Next thing mustn't be alphanumeric.
	if isAlphaNumeric(l.next()) {
		return false
	}
	l.backup()
	return true
}

func acceptFloat(l *lexer) bool {
	digits := "0123456789"
	if l.accept("0") && l.accept("xX") {
		digits = "0123456789abcdefABCDEF"
	}
	l.acceptRun(digits)
	if l.accept(".") {
		l.acceptRun(digits)
	}
	if l.accept("eE") {
		l.accept("+-")
		l.acceptRun("0123456789")
	}
	// Next thing mustn't be alphanumeric.
	if isAlphaNumeric(l.next()) {
		return false
	}
	l.backup()
	return true
}

func lexString(l *lexer) lexStateFn {
	if err := acceptString(l); err != nil {
		return l.errorf(err.Error())
	}
	l.emit(tokenString)
	return lexTop
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

func lexOption(l *lexer) lexStateFn {
	l.accept("&")
	if l.accept("&") {
		l.emit(tokenAndAnd)
		return lexTop
	}

	if l.acceptBy(isWordHead) {
		l.backup()
		err := acceptOption(l)
		if err != nil {
			return l.errorf(err.Error())
		}
		l.emit(tokenOption)
		return lexTop
	}

	l.emit(tokenAnd)
	return lexTop
}

func acceptOption(l *lexer) error {
	l.save()
	if l.acceptKeyword("g:", false) || l.acceptKeyword("l:", false) {
		if l.nextRunBy(isAlphaNumeric) == "" {
			l.restore()
			return errors.New("option name was missing")
		}
		return nil
	}
	if l.nextRunBy(isAlphaNumeric) == "" {
		l.restore()
		return errors.New("option name was missing")
	}
	return nil
}

func lexEnv(l *lexer) lexStateFn {
	l.save()
	l.accept("$")
	w := l.nextRunBy(isAlphaNumeric)
	if w == "" {
		l.restore()
		return l.errorf("environment variable name was missing")
	}
	l.emit(tokenEnv)
	return lexTop
}

func lexReg(l *lexer) lexStateFn {
	// @ is same as @"
	l.accept("@")
	l.acceptBy(func(r rune) bool {
		// :h registers
		return r == '"' ||
			isNumeric(r) ||
			r == '-' ||
			isAlpha(r) ||
			r == ':' ||
			r == '.' ||
			r == '%' ||
			r == '#' ||
			r == '=' ||
			r == '*' ||
			r == '+' ||
			r == '~' ||
			r == '_' ||
			r == '/'
	})
	l.emit(tokenReg)
	return lexTop
}
