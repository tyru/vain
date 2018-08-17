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

	"github.com/tyru/vain/node"
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
	fmt.Print(`
Usage: vain COMMAND ARGS

COMMAND
  build
    Transpile .vain files under current directory
`)
}

func build(args []string) error {
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
				if err := genFiles(file); err != nil {
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

func collectTargetFiles(args []string, files chan<- string) error {
	if len(args) > 0 {
		// If arguments were given, pass them as filenames.
		for i := range args {
			if _, err := os.Stat(args[i]); os.IsNotExist(err) {
				return err
			}
		}
		for i := range args {
			files <- args[i]
		}
		return nil
	}
	// Otherwise, collect .vain files under current directory.
	return filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(strings.ToLower(path), ".vain") {
			files <- path
		}
		return nil
	})
}

func genFiles(name string) error {
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

	formatCh := make(chan node.Node, 1)
	analyzeCh := make(chan node.Node, 1)

	lexer := lex(name, content.String())
	parser := parse(name, lexer.Tokens())
	formatter := format(name, formatCh)
	analyzer := analyze(name, analyzeCh)
	translator := translate(name, analyzer.Nodes())

	prettifiedFile := name[:len(name)-len(".vain")] + ".vain.pretty"
	vimFile := name[:len(name)-len(".vain")] + ".vim"
	writeErr := make(chan error, 2)
	var wg sync.WaitGroup

	// 7. []io.Reader -> Write to file.vim
	wg.Add(1)
	go func() {
		writeErr <- writeReaders(translator.Readers(), vimFile)
		wg.Done()
	}()

	// 6. []jsonExpr -> Translate to vim script -> []io.Reader
	go translator.Run()

	// 5. []node.Node -> Check semantic errors, emit intermediate code -> []jsonExpr
	go analyzer.Run()

	// 4.1. []io.Reader -> Write to file.vast
	wg.Add(1)
	go func() {
		writeErr <- writeReaders(formatter.Readers(), prettifiedFile)
		wg.Done()
	}()

	// 4. []node.Node -> Format codes -> []io.Reader
	go formatter.Run()

	// 3. []node.Node -> 4. formatter, 5. analyzer
	go func() {
		for node := range parser.Nodes() {
			formatCh <- node
			analyzeCh <- node
		}
		close(formatCh)
		close(analyzeCh)
	}()

	// 2. []token -> Parse -> []node.Node
	go parser.Run()

	// 1. source code -> Lex -> []token
	go lexer.Run()

	wg.Wait()
	close(writeErr)
	errs := make([]error, 0, 2)
	for err := range writeErr {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return multierror.Append(nil, errs...).ErrorOrNil()
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
