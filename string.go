package main

import (
	"errors"
	"strings"
)

type vainString string

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
			case 'X', 'x', 'U', 'u': // Hex, Unicode
				// TODO
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
