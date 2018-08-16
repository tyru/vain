package main

import "strings"

var emptyReader = strings.NewReader("")

type errorReader struct {
	err error
}

func (r *errorReader) Read(p []byte) (n int, err error) {
	return 0, r.err
}
