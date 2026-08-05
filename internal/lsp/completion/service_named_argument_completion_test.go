package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYAMLServiceNamedArgumentCompletion(t *testing.T) {
	phpIndex := namedArgumentCompletionPHPIndex(t)
	provider := NewServiceCompletionProvider(nil, phpIndex)

	document, request := namedArgumentCompletionRequest(
		`services:
  app.consumer:
    class: App\ChildConsumer
    arguments:
      $log`,
	)
	items := provider.GetCompletions(context.Background(), request)
	logger := requireCompletion(t, items, "$logger")
	assert.Equal(t, int(protocol.VariableCompletion), logger.Kind)
	assert.Equal(t, `Psr\Log\LoggerInterface`, logger.Detail)
	edit, ok := logger.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, "$log", completionRangeText(document, edit.Range))
	assert.Equal(t, "$logger: ", edit.NewText)
	assert.Contains(t, logger.Documentation.Value, `App\ChildConsumer`)
}

func TestYAMLServiceNamedArgumentCompletionReplacesCompleteKeyAndExcludesUsed(
	t *testing.T,
) {
	phpIndex := namedArgumentCompletionPHPIndex(t)
	provider := NewServiceCompletionProvider(nil, phpIndex)
	source := `services:
  app.consumer:
    class: App\ChildConsumer
    arguments:
      $logger: '@logger'
      $na<caret>: value`
	document, request := namedArgumentCompletionRequest(source)
	items := provider.GetCompletions(context.Background(), request)
	name := requireCompletion(t, items, "$name")
	edit, ok := name.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, "$na", completionRangeText(document, edit.Range))
	assert.Equal(t, "$name", edit.NewText)
	assert.NotContains(t, completionLabels(items), "$logger")
}

func TestYAMLServiceNamedArgumentCompletionSkipsFactoryServices(t *testing.T) {
	phpIndex := namedArgumentCompletionPHPIndex(t)
	provider := NewServiceCompletionProvider(nil, phpIndex)
	_, request := namedArgumentCompletionRequest(
		`services:
  app.consumer:
    class: App\ChildConsumer
    factory: ['@factory', create]
    arguments:
      $log`,
	)
	assert.Empty(t, provider.GetCompletions(context.Background(), request))
}

func namedArgumentCompletionPHPIndex(t *testing.T) *php.PHPIndex {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Consumer.php",
		[]byte(`<?php
namespace Psr\Log;
interface LoggerInterface {}

namespace App;
class ParentConsumer {
    public function __construct(
        \Psr\Log\LoggerInterface $logger,
        string $name,
        int $count = 0,
    ) {}
}
class ChildConsumer extends ParentConsumer {}
`),
	)))
	return phpIndex
}

func namedArgumentCompletionRequest(
	source string,
) (*lsp.TextDocument, *lsp.CompletionRequest) {
	offset := len(source)
	if marker := strings.Index(source, "<caret>"); marker >= 0 {
		source = strings.Replace(source, "<caret>", "", 1)
		offset = marker
	}
	document := lsp.NewTextDocument(
		"file:///project/config/services.yaml",
		source,
		1,
	)
	byteOffset := uint32(offset)
	line, character := document.LineIndex.PositionUTF16(byteOffset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	nodeOffset := byteOffset
	if nodeOffset > 0 {
		nodeOffset--
	}
	return document, &lsp.CompletionRequest{
		CompletionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            document.SyntaxTree.Root.NodeAtOffset(nodeOffset),
		},
	}
}
