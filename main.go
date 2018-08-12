package main

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
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
	done := make(chan bool, 1)

	// 2. files -> Transpile given files
	transpileErrs := make([]error, 16)
	go func() {
		for src := range files {
			// file.vain -> file.vast
			dst := src[:len(src)-len(".vain")] + ".vast"
			err := transpileFile(dst, src)
			if err != nil {
				transpileErrs = append(transpileErrs, err)
			}
		}

		done <- true
	}()

	// 1. Collect .vain files -> files
	var err error
	go func() {
		err = filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if strings.HasSuffix(strings.ToLower(path), ".vain") {
				files <- path
			}
			return nil
		})
		close(files)
	}()

	<-done

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

	tmpfile, err := ioutil.TempFile("", "example")
	if err != nil {
		return err
	}

	err = transpile(tmpfile, src, srcpath)
	tmpfile.Close()
	if err != nil {
		os.Remove(tmpfile.Name())
		return err
	}

	return os.Rename(tmpfile.Name(), dstpath)
}

func transpile(dst io.Writer, src io.Reader, srcpath string) error {
	var content strings.Builder
	_, err := io.Copy(&content, src)
	if err != nil {
		return err
	}
	dstbuf := bufio.NewWriter(dst)

	done := make(chan bool, 1)
	lexer := lex(srcpath, content.String())
	parser := parse(lexer)
	translator := translateSexp(parser)
	errs := make([]error, 0, 32)

	// 4. Output
	go func() {
		for r := range translator.Readers() {
			_, err := io.Copy(dstbuf, r)
			if err != nil {
				errs = append(errs, err)
			}
		}
		done <- true
	}()

	// 3. Translate
	go translator.Run()

	// 2. Parse
	go parser.Run()

	// 1. Lex
	go lexer.Run()

	<-done

	err = dstbuf.Flush()
	if err != nil {
		errs = append(errs, err)
	}
	return multierror.Append(nil, errs...).ErrorOrNil()
}
