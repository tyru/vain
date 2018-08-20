package main

import (
	"bufio"
	"errors"
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

	// Load standard libraries.
	// TODO Don't load standard library files twice
	// if they are specified as arguments.
	stdlib, err := loadStdlib()
	if err != nil {
		fmt.Printf("warning: could not read standard library: %s\n", err.Error())
	}

	// 3. Collect errors
	go func() {
		for err := range buildErrs {
			errs = append(errs, err)
		}
		done <- true
	}()

	var wg sync.WaitGroup
	files := make(chan string, 32)

	// 2. files -> Transpile given files -> file.vim
	wg.Add(1)
	go func() {
		for file := range files {
			wg.Add(1)
			go func(file string) {
				if err := buildFile(file, stdlib); err != nil {
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
func collectTargetFiles(files []string, out chan<- string) error {
	if len(files) == 0 {
		files = []string{"."}
	}
	for i := range files {
		err := filepath.Walk(files[i], func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if strings.HasSuffix(strings.ToLower(path), ".vain") {
				out <- path
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func buildFile(name string, stdlib *NamespaceDB) error {
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
	parser := parse(name, lexer.Tokens(), false)
	analyzer := analyze(name, parser.Nodes(), ToplevelNamespace)
	translator := translate(name, analyzer.Nodes())

	vimFile := name[:len(name)-len(".vain")] + ".vim"
	writeErr := make(chan error, 1)

	// 5. []io.Reader -> Write to file.vim
	go func() {
		writeErr <- writeReaders(translator.Readers(), vimFile)
	}()

	// 4. []node.Node -> Translate to vim script -> []io.Reader
	go translator.Run()

	// 3. []node.Node -> Check semantic errors, emit intermediate code -> []node.Node
	go analyzer.Run(stdlib)

	// 2. []token -> Parse -> []node.Node
	go parser.Run()

	// 1. source code -> Lex -> []token
	go lexer.Run()

	return <-writeErr
}

// Collect .vain files from $VAINROOT/lib .
func collectStdlibFiles() ([]string, error) {
	vainroot := "."
	if v := os.Getenv("VAINROOT"); v != "" {
		vainroot = v
	}
	libDir := filepath.Join(vainroot, "lib")
	if fi, err := os.Stat(libDir); err != nil {
		return nil, err
	} else if !fi.IsDir() {
		return nil, errors.New("$VAINROOT/lib is not a directory")
	}

	files := make([]string, 0, 32)
	ch := make(chan string, 32)
	done := make(chan bool, 1)
	var err error

	go func() {
		for f := range ch {
			files = append(files, f)
		}
		done <- true
	}()

	go func() {
		err = collectTargetFiles([]string{libDir}, ch)
		close(ch)
	}()

	<-done
	return files, err
}

func loadStdlib() (*NamespaceDB, error) {
	// Get standard library files synchronously.
	filenames, err := collectStdlibFiles()
	if err != nil {
		return nil, err
	}

	// Read the contents and save as []nameAndContent .
	type nameAndContent struct {
		name    string
		content string
	}
	files := make([]nameAndContent, 0, len(filenames))
	for _, name := range filenames {
		src, err := os.Open(name)
		if err != nil {
			return nil, err
		}

		var content strings.Builder
		_, err = io.Copy(&content, src)
		src.Close()
		if err != nil {
			return nil, err
		}

		files = append(files, nameAndContent{name, content.String()})
	}

	var wgAnalyze sync.WaitGroup
	nodes := make(chan node.Node, len(files))
	errs := make([]error, len(files))
	analyzer := analyze("<stdlib>", nodes, ToplevelNamespace)

	// 5. Handle analyzer errors.
	wgAnalyze.Add(1)
	go func() {
		for n := range analyzer.Nodes() {
			if err, ok := n.TerminalNode().(error); ok {
				errs = append(errs, err)
			}
		}
		wgAnalyze.Done()
	}()

	// 4. []node.Node -> Parse nodes and create namespace DB.
	wgAnalyze.Add(1)
	go func() {
		analyzer.Run(nil)
		wgAnalyze.Done()
	}()

	var wgNode sync.WaitGroup

	for _, file := range files {
		lexer := lex(file.name, file.content)
		parser := parse(file.name, lexer.Tokens(), true)

		// 3. []node.Node -> nodes
		wgNode.Add(1)
		go func() {
			for n := range parser.Nodes() {
				nodes <- n
			}
			wgNode.Done()
		}()

		// 2. []token -> Parse -> []node.Node
		wgNode.Add(1)
		go func() {
			parser.Run()
			wgNode.Done()
		}()

		// 1. source code -> Lex -> []token
		wgNode.Add(1)
		go func() {
			lexer.Run()
			wgNode.Done()
		}()
	}

	wgNode.Wait()
	close(nodes)
	wgAnalyze.Wait()

	err = multierror.Append(nil, errs...).ErrorOrNil()
	return analyzer.NamespaceDB(), err
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

	// 2. files -> Transpile given files -> file.vain.pretty
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
	parser := parse(name, lexer.Tokens(), false)
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
