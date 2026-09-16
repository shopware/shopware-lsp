package php

import (
	"fmt"
	"os"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/shopware/shopware-lsp/internal/php/binder"
	"github.com/shopware/shopware-lsp/internal/php/inference"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

// Package-level sinks keep the compiler from optimizing away benchmarked
// calls whose results would otherwise be discarded.
var benchmarkParseResult phpparser.Result
var benchmarkDocument *semantic.Document
var benchmarkClass semantic.Symbol

func benchmarkSource(b *testing.B, path string) []byte {
	b.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	return content
}

func BenchmarkPureGoParsing(b *testing.B) {
	content := benchmarkSource(b, "testdata/01.php")
	b.ReportAllocs()
	b.SetBytes(int64(len(content)))
	for b.Loop() {
		benchmarkParseResult = phpparser.ParseBytes(content)
	}
}

func BenchmarkSemanticBinding(b *testing.B) {
	content := benchmarkSource(b, "testdata/01.php")
	root := phpparser.ParseBytes(content).Tree.Root
	semanticBinder := binder.New()
	sample := semanticBinder.Bind("testdata/01.php", 1, root)
	b.ReportAllocs()
	for b.Loop() {
		benchmarkDocument = semanticBinder.Bind("testdata/01.php", 1, root)
	}
	b.ReportMetric(float64(len(sample.Symbols)), "symbols/doc")
	b.ReportMetric(float64(sample.TypeFactCount()), "type-facts/doc")
}

func BenchmarkSemanticAnalysis(b *testing.B) {
	content := benchmarkSource(b, "testdata/01.php")
	root := phpparser.ParseBytes(content).Tree.Root
	document := binder.New().Bind("testdata/01.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{document})
	analyzer := inference.New(snapshot)
	sample := analyzer.Analyze(document, root)
	b.ReportAllocs()
	for b.Loop() {
		benchmarkDocument = analyzer.Analyze(document, root)
	}
	b.ReportMetric(float64(sample.TypeFactCount()), "type-facts/doc")
}

// BenchmarkPHPIndexClassLookup measures request-time class resolution against
// a populated index rather than a single-document one, so SQLite lookup costs
// stay visible as the workspace graph grows.
func BenchmarkPHPIndexClassLookup(b *testing.B) {
	idx, err := NewPHPIndex(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = idx.Close() })
	const documentCount = 128
	for i := range documentCount {
		source := fmt.Sprintf(
			"<?php\nnamespace Bench\\Domain%03d;\n\nclass Service%03d\n{\n    public function handle(string $id): string\n    {\n        return $id;\n    }\n}\n",
			i,
			i,
		)
		path := fmt.Sprintf("src/Domain%03d/Service%03d.php", i, i)
		if err := idx.Index(indexer.NewParsedFile(path, []byte(source))); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		symbol, found := idx.FindClass("Bench\\Domain042\\Service042")
		if !found {
			b.Fatal("indexed class not found")
		}
		benchmarkClass = symbol
	}
}
