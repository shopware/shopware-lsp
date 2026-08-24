package app

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/require"
)

func TestEnrichPHPContextReusesDocumentAnalysis(t *testing.T) {
	index, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })

	source := `<?php
class Consumer {
    public function run(): void { $this->execute(); }
}`
	document := lsp.NewTextDocument(
		"file:///project/Consumer.php",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "execute"))
	syntax := lsp.SyntaxContext{
		Document: document,
		Root:     document.SyntaxTree.Root,
		Node:     document.SyntaxTree.Root.NodeAtOffset(offset),
	}

	first := php.GetPHPContext(enrichPHPContext(
		context.Background(), index, syntax,
	))
	second := php.GetPHPContext(enrichPHPContext(
		context.Background(), index, syntax,
	))
	require.NotNil(t, first)
	require.NotNil(t, second)
	require.Same(t, first.Document, second.Document)
	require.Same(t, first.Snapshot, second.Snapshot)

	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/Dependency.php",
		[]byte("<?php class Dependency {}"),
	)))
	third := php.GetPHPContext(enrichPHPContext(
		context.Background(), index, syntax,
	))
	require.NotNil(t, third)
	require.NotSame(t, first.Document, third.Document)
	require.NotSame(t, first.Snapshot, third.Snapshot)
}
