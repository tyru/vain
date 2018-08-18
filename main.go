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
		err = cmdBuild(os.Args[2:])
	case "fmt":
		err = cmdFormat(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`
Usage: vain COMMAND ARGS

COMMAND
  build
    Transpile .vain files under current directory
`)
}

func cmdBuild(args []string) error {
	buildErrs := make(chan error, 16)
	errs := make([]error, 0, 16)
	done := make(chan bool, 1)

	// 3. Collect errors
	go func() {
		for err := range buildErrs {
			errs = append(errs, err)
		}
		done <- true
	}()

	var wg sync.WaitGroup
	files := make(chan string, 32)

	// 2. files -> Transpile given files -> file.vast, file.vim
	wg.Add(1)
	go func() {
		for file := range files {
			wg.Add(1)
			go func(file string) {
				if err := buildFile(file); err != nil {
					buildErrs <- err
				}
				wg.Done()
			}(file)
		}
		wg.Done()
	}()

	// 1. Collect .vain files -> files
	wg.Add(1)
	go func() {
		if err := collectTargetFiles(args, files); err != nil {
			buildErrs <- err
		}
		close(files)
		wg.Done()
	}()

	wg.Wait()
	close(buildErrs)
	<-done

	return multierror.Append(nil, errs...).ErrorOrNil()
}

// collectTargetFiles collects .vain files under current directory.
// If arguments were given, pass them as filenames.
// If the argument is a directory, collect filenames recursively.
func collectTargetFiles(args []string, files chan<- string) error {
	if len(args) == 0 {
		args = []string{"."}
	}
	for i := range args {
		err := filepath.Walk(args[i], func(path string, info os.FileInfo, err error) error {
			if err != nil {
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
	}
	return nil
}

func buildFile(name string) error {
	src, err := os.Open(name)
	if err != nil {
		return err
	}

	var content strings.Builder
	_, err = io.Copy(&content, src)
	src.Close()
	if err != nil {
		return err
	}

	lexer := lex(name, content.String())
	parser := parse(name, lexer.Tokens())
	analyzer := analyze(name, parser.Nodes())
	translator := translate(name, analyzer.Nodes())

	vimFile := name[:len(name)-len(".vain")] + ".vim"
	writeErr := make(chan error, 1)

	// 5. []io.Reader -> Write to file.vim
	go func() {
		writeErr <- writeReaders(translator.Readers(), vimFile)
	}()

	// 4. []jsonExpr -> Translate to vim script -> []io.Reader
	go translator.Run()

	// 3. []node.Node -> Check semantic errors, emit intermediate code -> []jsonExpr
	go analyzer.Run()

	// 2. []token -> Parse -> []node.Node
	go parser.Run()

	// 1. source code -> Lex -> []token
	go lexer.Run()

	return <-writeErr
}

// Write given readers to temporary file with a buffer.
// And after successful write, rename to dst.
func writeReaders(readers <-chan io.Reader, dst string) error {
	tmpfile, err := ioutil.TempFile("", "vainsrc")
	if err != nil {
		return err
	}
	dstbuf := bufio.NewWriter(tmpfile)

	for r := range readers {
		if _, e := io.Copy(dstbuf, r); e != nil {
			err = e
			break
		}
	}

	if err != nil {
		tmpfile.Close()
		os.Remove(tmpfile.Name())
		return err
	}
	if err := dstbuf.Flush(); err != nil {
		return err
	}
	tmpfile.Close()
	return os.Rename(tmpfile.Name(), dst)
}

func cmdFormat(args []string) error {
	buildErrs := make(chan error, 16)
	errs := make([]error, 0, 16)
	done := make(chan bool, 1)

	// 3. Collect errors
	go func() {
		for err := range buildErrs {
			errs = append(errs, err)
		}
		done <- true
	}()

	var wg sync.WaitGroup
	files := make(chan string, 32)

	// 2. files -> Transpile given files -> file.vast, file.vim
	wg.Add(1)
	go func() {
		for file := range files {
			wg.Add(1)
			go func(file string) {
				if err := formatFile(file); err != nil {
					buildErrs <- err
				}
				wg.Done()
			}(file)
		}
		wg.Done()
	}()

	// 1. Collect .vain files -> files
	wg.Add(1)
	go func() {
		if err := collectTargetFiles(args, files); err != nil {
			buildErrs <- err
		}
		close(files)
		wg.Done()
	}()

	wg.Wait()
	close(buildErrs)
	<-done

	return multierror.Append(nil, errs...).ErrorOrNil()
}

func formatFile(name string) error {
	src, err := os.Open(name)
	if err != nil {
		return err
	}

	var content strings.Builder
	_, err = io.Copy(&content, src)
	src.Close()
	if err != nil {
		return err
	}

	lexer := lex(name, content.String())
	parser := parse(name, lexer.Tokens())
	formatter := format(name, parser.Nodes())

	vimFile := name + ".pretty"
	done := make(chan error, 1)

	// 4. []io.Reader -> Write to file.vim
	go func() {
		done <- writeReaders(formatter.Readers(), vimFile)
	}()

	// 3. []node.Node -> Format codes -> []io.Reader
	go formatter.Run()

	// 2. []token -> Parse -> []node.Node
	go parser.Run()

	// 1. source code -> Lex -> []token
	go lexer.Run()

	return <-done
}
