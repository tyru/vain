package main

import (
	"io"
	"strings"
)

type translator interface {
	Run()
	Readers() <-chan io.Reader
}

var emptyReader = strings.NewReader("")

type errorReader struct {
	err error
}

func (r *errorReader) Read(p []byte) (n int, err error) {
	return 0, r.err
}
