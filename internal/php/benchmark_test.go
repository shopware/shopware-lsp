package php

import (
	"os"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/shopware/shopware-lsp/internal/php/binder"
	"github.com/shopware/shopware-lsp/internal/php/inference"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

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
	b.ResetTimer()
	for range b.N {
		_ = phpparser.ParseBytes(content)
	}
}

func BenchmarkSemanticBinding(b *testing.B) {
	content := benchmarkSource(b, "testdata/01.php")
	root := phpparser.ParseBytes(content).Tree.Root
	semanticBinder := binder.New()
	sample := semanticBinder.Bind("testdata/01.php", 1, root)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = semanticBinder.Bind("testdata/01.php", 1, root)
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
	b.ResetTimer()
	for range b.N {
		_ = analyzer.Analyze(document, root)
	}
	b.ReportMetric(float64(sample.TypeFactCount()), "type-facts/doc")
}

func BenchmarkPHPIndexClassLookup(b *testing.B) {
	idx, err := NewPHPIndex(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = idx.Close() })
	content := benchmarkSource(b, "testdata/01.php")
	if err := idx.Index(indexer.NewParsedFile("testdata/01.php", content)); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, _ = idx.FindClass(
			"Shopware\\Core\\Content\\Category\\Service\\NavigationLoader",
		)
	}
}
