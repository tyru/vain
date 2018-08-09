package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/go-multierror"
)

func main() {
	if len(os.Args) == 1 {
		usage()
		return
	}
	var err error
	switch os.Args[1] {
	case "build":
		err = build(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func usage() {
	// TODO
	fmt.Print(`
Usage: vain COMMAND ARGS

COMMAND
  build
    Transpile .vain files under current directory
`)
}

func build(args []string) error {
	files := make(chan string, 32)

	// Transpile given files
	transpileErrs := make([]error, 16)
	go func() {
		for src := range files {
			// file.vain -> file.vim
			dst := src[:len(src)-len(".vain")] + ".vim"
			err := transpileFile(dst, src)
			if err != nil {
				transpileErrs = append(transpileErrs, err)
			}
		}
	}()

	// Collect .vain files
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			close(files)
			return err
		}
		if strings.HasSuffix(strings.ToLower(path), ".vain") {
			files <- path
		}
		return nil
	})
	if err != nil {
		return err
	}

	return multierror.Append(nil, transpileErrs...).ErrorOrNil()
}

func transpileFile(dstpath, srcpath string) error {
	src, err := os.Open(srcpath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(dstpath)
	if err != nil {
		return err
	}
	defer dst.Close()

	return transpile(bufio.NewWriter(dst), src, srcpath)
}

func transpile(dst io.Writer, src io.Reader, srcpath string) error {
	outCh := make(chan node, 32)
	parser := newParser(outCh, src, srcpath)

	// parser goroutine
	go func() {
		// https://talks.golang.org/2011/lex.slide#26
		for state := parseNode; state != nil; {
			state = state(parser)
		}
	}()

	errs := make([]error, 0, 32)
	for node := range outCh {
		_, err := node.WriteTo(dst)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return multierror.Append(nil, errs...).ErrorOrNil()
}

func newParser(outCh chan<- node, src io.Reader, srcpath string) *parser {
	return &parser{
		outCh:    outCh,
		reader:   bufio.NewReader(src),
		filename: srcpath,
	}
}

type parser struct {
	outCh    chan<- node
	reader   *bufio.Reader
	filename string
}

func (p *parser) errorf(format string, args ...interface{}) stateFn {
	p.outCh <- &nodeError{fmt.Errorf(format, args...)}
	return nil
}

type stateFn func(*parser) stateFn

func parseNode(p *parser) stateFn {
	return p.errorf("parseNode failed")
}

type node interface {
	WriteTo(w io.Writer) (int64, error)
}

type nodeError struct {
	err error
}

func (node *nodeError) WriteTo(w io.Writer) (int64, error) {
	return 0, node.err
}

type statement interface {
	node
}

type importStatement struct {
	statement
}

func (stmt *importStatement) WriteTo(w io.Writer) (int64, error) {
	var builder bytes.Buffer
	builder.WriteString("import hoge from fuga\n")
	return builder.WriteTo(w)
}

type expr interface {
	node
}
