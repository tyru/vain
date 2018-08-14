package main

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

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
	var wg sync.WaitGroup

	// 2. files -> Transpile given files
	astErrs := make([]error, 16)
	vimErrs := make([]error, 16)
	wg.Add(1)
	go func() {
		for src := range files {
			// file.vain -> file.vast
			wg.Add(1)
			go func(src string) {
				dst := src[:len(src)-len(".vain")] + ".vast"
				err := transpileFile(dst, src, translateSexp)
				if err != nil {
					astErrs = append(astErrs, err)
				}
				wg.Done()
			}(src)
			// file.vain -> file.vim
			wg.Add(1)
			go func(src string) {
				dst := src[:len(src)-len(".vain")] + ".vim"
				err := transpileFile(dst, src, translateVim)
				if err != nil {
					vimErrs = append(vimErrs, err)
				}
				wg.Done()
			}(src)
		}
		wg.Done()
	}()

	// 1. Collect .vain files -> files
	var err error
	go func() {
		if len(args) > 0 {
			// If arguments were given, pass them as filenames.
			for i := range args {
				if _, e := os.Stat(args[i]); os.IsNotExist(e) {
					err = e
					close(files)
					return
				}
			}
			for i := range args {
				files <- args[i]
			}
			close(files)
			return
		}
		// Otherwise, collect .vain files under current directory.
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

	wg.Wait()

	if err != nil {
		return err
	}
	e := multierror.Append(nil, astErrs...)
	e = multierror.Append(e, vimErrs...)
	return e.ErrorOrNil()
}

func transpileFile(dstpath, srcpath string, translate func(*detector) translator) error {
	src, err := os.Open(srcpath)
	if err != nil {
		return err
	}
	defer src.Close()

	tmpfile, err := ioutil.TempFile("", "example")
	if err != nil {
		return err
	}

	err = transpile(tmpfile, src, srcpath, translate)
	tmpfile.Close()
	if err != nil {
		os.Remove(tmpfile.Name())
		return err
	}

	return os.Rename(tmpfile.Name(), dstpath)
}

func transpile(dst io.Writer, src io.Reader, srcpath string, translate func(*detector) translator) error {
	var content strings.Builder
	_, err := io.Copy(&content, src)
	if err != nil {
		return err
	}
	dstbuf := bufio.NewWriter(dst)

	done := make(chan bool, 1)
	lexer := lex(srcpath, content.String())
	parser := parse(lexer)
	analyzer := analyze(parser)
	detector := detect(analyzer)
	translator := translate(detector)
	errs := make([]error, 0, 32)

	// 6. []io.Reader -> Output
	go func() {
		for r := range translator.Readers() {
			_, err := io.Copy(dstbuf, r)
			if err != nil {
				errs = append(errs, err)
			}
		}
		done <- true
	}()

	// 5. []node -> Translate to string -> []io.Reader
	// io.Reader can also have errors.
	go translator.Run()

	// 4. []typedNode -> Detect semantic errors -> []node
	go detector.Run()

	// 3. []node -> Add types -> []typedNode
	go analyzer.Run()

	// 2. []token -> Parse -> []node
	go parser.Run()

	// 1. source code -> Lex -> []token
	go lexer.Run()

	<-done

	err = dstbuf.Flush()
	if err != nil {
		errs = append(errs, err)
	}
	return multierror.Append(nil, errs...).ErrorOrNil()
}
