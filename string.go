package main

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
)

type vainString string

func unevalString(s string) (*vainString, error) {
	vs := vainString("'" + strings.Replace(s, "'", "''", -1) + "'")
	return &vs, nil
}

func (vs *vainString) eval() (string, error) {
	s := string(*vs)
	// single quote
	if s[0] == '\'' {
		return strings.Replace(s[1:len(s)-1], "''", "'", -1), nil
	}
	// double quote
	rs := []rune(s[1 : len(s)-1])
	var result strings.Builder
	for i := 0; i < len(rs); i++ {
		switch rs[i] {
		case '\\':
			i++
			if i >= len(rs) {
				return "", errors.New("missing quote")
			}
			switch rs[i] {
			case 'b': // BS (not BEL!)
				result.WriteRune('\x08')
			case 'e': // ESC
				result.WriteRune('\x1B')
			case 'f': // FF
				result.WriteRune('\x0C')
			case 'n': // NL
				result.WriteRune('\x0A')
			case 'r': // CR
				result.WriteRune('\x0D')
			case 't': // HT
				result.WriteRune('\x09')
			case 'X', 'x': // Hex (TODO refactor this *fantastic* code)
				value := make([]rune, 2)
				i++
				if i >= len(rs) || !isHexChar(rs[i]) { // "\x" == "x", "\X" == "X"
					result.WriteRune(rs[i-1])
					i--
					continue
				}
				value[0] = rs[i]
				i++
				if i >= len(rs) || !isHexChar(rs[i]) { // read "\x1" as "\x01"
					value[1] = value[0]
					value[0] = '0'
				} else {
					value[1] = rs[i]
				}
				r, _, _, err := strconv.UnquoteChar(`\x`+string(value), '"')
				if err != nil {
					return "", errors.New("cannot evaluate hex (\\x): " + err.Error())
				}
				result.WriteRune(r)
			case 'U', 'u': // Unicode (TODO refactor this *fantastic* code)
				value := make([]rune, 4)
				i++
				if i >= len(rs) || !isHexChar(rs[i]) { // "\u" == "u", "\U" == "U"
					result.WriteRune(rs[i-1])
					i--
					continue
				}
				value[0] = rs[i]
				i++
				if i >= len(rs) || !isHexChar(rs[i]) { // read "\u1" as "\u0001"
					value[3] = value[0]
					value[2] = '0'
					value[1] = '0'
					value[0] = '0'
					goto Convert
				}
				value[1] = rs[i]
				i++
				if i >= len(rs) || !isHexChar(rs[i]) { // read "\u12" as "\u0012"
					value[3] = value[1]
					value[2] = value[0]
					value[1] = '0'
					value[0] = '0'
					goto Convert
				}
				value[2] = rs[i]
				i++
				if i >= len(rs) || !isHexChar(rs[i]) { // read "\u123" as "\u0123"
					value[3] = value[2]
					value[2] = value[1]
					value[1] = value[0]
					value[0] = '0'
					goto Convert
				}
				value[3] = rs[i]
			Convert:
				r, _, _, err := strconv.UnquoteChar(`\u`+string(value), '"')
				if err != nil {
					return "", errors.New("cannot evaluate unicode codepoint (\\u): " + err.Error())
				}
				result.WriteRune(r)
			case '0', '1', '2', '3', '4', '5', '6', '7': // Octal
				// TODO
			case '<': // Special key, e.g.: "\<C-W>"
				// TODO
			}
		default:
			result.WriteRune(rs[i])
		}
	}
	return result.String(), nil
}

func isHexChar(r rune) bool {
	return unicode.Is(unicode.ASCII_Hex_Digit, r)
}
