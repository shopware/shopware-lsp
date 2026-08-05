package completion

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/environment"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentCompletionReplacesIncompleteVariableName(t *testing.T) {
	root := t.TempDir()
	idx, err := environment.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		filepath.Join(root, ".env"),
		[]byte("DATABASE_URL=mysql://localhost\nAPP_ENV=dev"),
	)))

	source := "parameters:\n  database: '%env(resolve:DATAB"
	path := filepath.Join(root, "config", "services.yaml")
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, "DATAB") + len("DATAB"))
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)

	items := NewEnvironmentCompletionProvider(idx).GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				DocumentContent: document.Text,
				LineIndex:       document.LineIndex,
			},
		},
	)
	var database *protocol.CompletionItem
	for index := range items {
		if items[index].Label == "DATABASE_URL" {
			database = &items[index]
			break
		}
	}
	require.NotNil(t, database)
	assert.Equal(t, int(protocol.VariableCompletion), database.Kind)
	edit, ok := database.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, "DATABASE_URL", edit.NewText)
	assert.Equal(t, len("DATAB"), edit.Range.End.Character-edit.Range.Start.Character)
}

func TestEnvironmentCompletionSupportsAutowireEnvAttribute(t *testing.T) {
	root := t.TempDir()
	idx, err := environment.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		filepath.Join(root, ".env"),
		[]byte("DATABASE_URL=mysql://localhost"),
	)))

	source := "<?php #[Autowire(env: 'resolve:DATAB')] class Config {}"
	path := filepath.Join(root, "src", "Config.php")
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, "DATAB") + len("DATAB"))
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)

	items := NewEnvironmentCompletionProvider(idx).GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				DocumentContent: document.Text,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            node,
			},
		},
	)
	require.Len(t, items, 1)
	assert.Equal(t, "DATABASE_URL", items[0].Label)
	edit, ok := items[0].TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, len("DATAB"), edit.Range.End.Character-edit.Range.Start.Character)
}

func TestEnvironmentCompletionSupportsPHPEnvFunction(t *testing.T) {
	root := t.TempDir()
	idx, err := environment.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		filepath.Join(root, ".env"),
		[]byte("APP_ENV=dev"),
	)))

	source := "<?php env('bool:APP_')"
	path := filepath.Join(root, "config", "services.php")
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, "APP_") + len("APP_"))
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	items := NewEnvironmentCompletionProvider(idx).GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				DocumentContent: document.Text,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(
					offset,
				),
			},
		},
	)
	require.Len(t, items, 1)
	assert.Equal(t, "APP_ENV", items[0].Label)
}
