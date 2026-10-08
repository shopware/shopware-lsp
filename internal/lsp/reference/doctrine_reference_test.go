package reference

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctrineDBALReferencesUseUnsavedRegistrations(t *testing.T) {
	root := t.TempDir()
	idx, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	firstPath := filepath.Join(root, "first.yaml")
	secondPath := filepath.Join(root, "second.yaml")
	source := "doctrine:\n  dbal:\n    types:\n      money: App\\MoneyType\n"
	for _, path := range []string{firstPath, secondPath} {
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
		require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))
	}
	provider := NewDoctrineReferenceProvider(idx, phpIndex)
	for _, test := range []struct {
		name  string
		count int
	}{{"money", 2}, {"euros", 1}} {
		t.Run(test.name, func(t *testing.T) {
			unsaved := strings.Replace(source, "money", test.name, 1)
			document := lsp.NewTextDocument(uriutil.FileURI(firstPath), unsaved, 2)
			offset := uint32(strings.Index(unsaved, test.name) + 1)
			line, column := document.LineIndex.PositionUTF16(offset)
			params := &protocol.ReferenceParams{}
			params.TextDocument.URI = document.URI
			params.Position = protocol.Position{Line: int(line), Character: int(column)}
			result, err := provider.GetReferences(context.Background(), &lsp.ReferenceRequest{ReferenceParams: params, SyntaxContext: lsp.SyntaxContext{Document: document, Root: document.SyntaxTree.Root, LineIndex: document.LineIndex}})
			require.NoError(t, err)
			require.Len(t, result, test.count)
			assert.Equal(t, document.URI, result[len(result)-1].URI)
			assert.Equal(t, len(test.name), result[len(result)-1].Range.End.Character-result[len(result)-1].Range.Start.Character)
		})
	}
}
