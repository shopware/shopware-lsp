// Command dump_cst prints the debug tree and parse errors for every file path
// read from stdin, one record per file. It is the reference oracle used to
// compare the Rust parsers against the Go parsers during the migration.
//
//	find . -name '*.json' | go run ./cmd/dump_cst -lang json
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

func main() {
	lang := flag.String("lang", "", "language id (php, javascript, twig, vue, json, yaml, scss, xml)")
	flag.Parse()

	registry, err := language.NewBuiltinRegistry()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	definition, ok := registry.ByID(language.ID(*lang))
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown language %q\n", *lang)
		os.Exit(1)
	}

	if err := dump(definition, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// dump writes one record per path read from input. Write errors (for
// example a closed pipe) abort the dump instead of silently truncating it.
func dump(definition language.Definition, input io.Reader, output io.Writer) error {
	out := bufio.NewWriterSize(output, 1<<20)
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		path := scanner.Text()
		content, err := os.ReadFile(path)
		if err != nil {
			if _, err := fmt.Fprintf(out, "=== %s\nREAD ERROR\n", path); err != nil {
				return err
			}
			continue
		}
		result := definition.Parse(string(content))
		if _, err := fmt.Fprintf(out, "=== %s\n%s\n", path, cst.DebugTree(result.Tree.Root)); err != nil {
			return err
		}
		for i := range result.Errors {
			if _, err := fmt.Fprintln(out, result.Errors[i].String()); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return out.Flush()
}
