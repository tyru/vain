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

func genFiles(name string) error {
	src, err := os.Open(name)
	if err != nil {
		return err
	}
	defer src.Close()

	var content strings.Builder
	_, err = io.Copy(&content, src)
	if err != nil {
		return err
	}

	sexpCh := make(chan node, 1)
	vimCh := make(chan node, 1)

	transpiler := transpile(name, content.String())
	sexpTranslator := translateSexp(name, sexpCh)
	vimTranslator := translateVim(name, vimCh)
	vastFile := name[:len(name)-len(".vain")] + ".vast"
	vimFile := name[:len(name)-len(".vain")] + ".vim"
	writeErr := make(chan error, 2)
	var wg sync.WaitGroup

	// 4. []io.Reader -> file.vast
	wg.Add(1)
	go func() {
		writeErr <- writeReaders(sexpTranslator.Readers(), vastFile)
		wg.Done()
	}()

	// 4. []io.Reader -> file.vim
	wg.Add(1)
	go func() {
		writeErr <- writeReaders(vimTranslator.Readers(), vimFile)
		wg.Done()
	}()

	// 3. []node -> []io.Reader
	// io.Reader can also have errors.
	go sexpTranslator.Run()
	go vimTranslator.Run()

	// 2. []node -> sexpTranslator, vimTranslator
	go func() {
		for node := range transpiler.Nodes() {
			sexpCh <- node
			vimCh <- node
		}
		close(sexpCh)
		close(vimCh)
	}()

	// 1. source code -> []node
	go transpiler.Run()

	wg.Wait()
	close(writeErr)
	for err := range writeErr {
		if err != nil {
			return err
		}
	}
	return nil
}

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

func transpile(name, input string) *transpiler {
	return &transpiler{name, input, make(chan node, 1)}
}

func (t *transpiler) Nodes() <-chan node {
	return t.outNodes
}

type transpiler struct {
	name     string
	input    string
	outNodes chan node
}

func (t *transpiler) Run() {
	done := make(chan bool, 1)
	lexer := lex(t.name, t.input)
	parser := parse(t.name, lexer.Tokens())
	analyzer := analyze(t.name, parser.Nodes())

	// 4. []node -> send to t.outNodes
	go func() {
		for node := range analyzer.Nodes() {
			t.outNodes <- node
		}
		close(t.outNodes)
		done <- true
	}()

	// 3. []node -> Check semantic errors -> []node
	go analyzer.Run()

	// 2. []token -> Parse -> []node
	go parser.Run()

	// 1. source code -> Lex -> []token
	go lexer.Run()

	<-done
}
