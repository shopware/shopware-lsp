package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/shopware/shopware-lsp/internal/php/stubs/catalog"
	stubgen "github.com/shopware/shopware-lsp/internal/php/stubs/generate"
)

func main() {
	var source string
	var lockPath string
	var output string
	var skipRevisionCheck bool
	flag.StringVar(&source, "source", "../phpstorm-stubs", "path to a phpstorm-stubs checkout")
	flag.StringVar(
		&lockPath,
		"lock",
		"internal/php/stubs/phpstorm-stubs.lock.json",
		"path to the pinned source manifest",
	)
	flag.StringVar(
		&output,
		"output",
		"internal/php/stubs/phpstorm-stubs.msgpack",
		"generated catalog path",
	)
	flag.BoolVar(
		&skipRevisionCheck,
		"skip-revision-check",
		false,
		"allow generation from a source tree without the locked Git revision",
	)
	flag.Parse()

	lock, err := stubgen.LoadLock(lockPath)
	if err != nil {
		fatal(err)
	}
	if !skipRevisionCheck {
		if err := stubgen.VerifySource(source, lock); err != nil {
			fatal(err)
		}
	}
	generated, stats, err := stubgen.Build(source, lock)
	if err != nil {
		fatal(err)
	}
	data, err := catalog.Encode(generated)
	if err != nil {
		fatal(fmt.Errorf("encode generated PHP stub catalog: %w", err))
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		fatal(fmt.Errorf("create generated PHP stub directory: %w", err))
	}
	if err := os.WriteFile(output, data, 0o644); err != nil {
		fatal(fmt.Errorf("write generated PHP stub catalog: %w", err))
	}
	fmt.Printf(
		"generated %d records from %d symbols in %d files (%d source bytes, %d catalog bytes) at %s\n",
		stats.Records,
		stats.ParsedSymbols,
		stats.Files,
		stats.SourceBytes,
		len(data),
		output,
	)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
