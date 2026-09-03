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

func TestContainerConstantCompletion(t *testing.T) {
	phpIndex := containerConstantCompletionPHPIndex(t)
	provider := NewContainerConstantCompletionProvider(phpIndex)

	document, request := containerConstantCompletionRequest(
		`value: !php/const App\Mode::A`,
	)
	items := provider.GetCompletions(context.Background(), request)
	active := requireCompletion(t, items, "ACTIVE")
	assert.Equal(t, int(protocol.ConstantCompletion), active.Kind)
	edit, ok := active.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, "A", completionRangeText(document, edit.Range))
	assert.Equal(t, "ACTIVE", edit.NewText)
	assert.NotContains(t, completionLabels(items), "SECRET")

	_, globalRequest := containerConstantCompletionRequest(
		`value: !php/const APP`,
	)
	requireCompletion(
		t,
		provider.GetCompletions(context.Background(), globalRequest),
		"APP_LIMIT",
	)

	_, fullRequest := containerConstantCompletionRequest(
		`value: !php/const App\M`,
	)
	requireCompletion(
		t,
		provider.GetCompletions(context.Background(), fullRequest),
		"App\\Mode::ACTIVE",
	)
}

func TestContainerConstantCompletionSupportsEmptyAndLegacyValues(
	t *testing.T,
) {
	phpIndex := containerConstantCompletionPHPIndex(t)
	provider := NewContainerConstantCompletionProvider(phpIndex)
	for _, source := range []string{
		`value: !php/const `,
		`value: !php/const:`,
	} {
		_, request := containerConstantCompletionRequest(source)
		requireCompletion(
			t,
			provider.GetCompletions(context.Background(), request),
			"APP_LIMIT",
		)
	}
}

func TestContainerEnumCompletion(t *testing.T) {
	phpIndex := containerConstantCompletionPHPIndex(t)
	provider := NewContainerConstantCompletionProvider(phpIndex)

	document, request := containerConstantCompletionRequest(
		`value: !php/enum App\State::A`,
	)
	items := provider.GetCompletions(context.Background(), request)
	active := requireCompletion(t, items, "ACTIVE")
	assert.Equal(t, int(protocol.EnumMemberCompletion), active.Kind)
	edit, ok := active.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, "A", completionRangeText(document, edit.Range))
	assert.NotContains(t, completionLabels(items), "ALIAS")

	_, fullRequest := containerConstantCompletionRequest(
		`value: !php/enum App\S`,
	)
	items = provider.GetCompletions(context.Background(), fullRequest)
	requireCompletion(t, items, "App\\State")
	requireCompletion(t, items, "App\\State::ACTIVE")
	assert.NotContains(t, completionLabels(items), "App\\Mode::ACTIVE")
	assert.NotContains(t, completionLabels(items), "APP_LIMIT")
}

func containerConstantCompletionPHPIndex(t *testing.T) *php.PHPIndex {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Mode.php",
		[]byte(`<?php
namespace {
    const APP_LIMIT = 10;
}
namespace App {
    class Mode {
        public const ACTIVE = 'active';
        private const SECRET = 'secret';
    }
    enum State {
        public const ALIAS = self::ACTIVE;
        case ACTIVE;
    }
}
`),
	)))
	return phpIndex
}

func containerConstantCompletionRequest(
	source string,
) (*lsp.TextDocument, *lsp.CompletionRequest) {
	document := lsp.NewTextDocument(
		"file:///project/config/services.yaml",
		source,
		1,
	)
	offset := uint32(len(source))
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return document, &lsp.CompletionRequest{
		CompletionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node: document.SyntaxTree.Root.NodeAtOffset(
				offset - 1,
			),
		},
	}
}

func completionRangeText(
	document *lsp.TextDocument,
	rng protocol.Range,
) string {
	start := document.LineIndex.OffsetUTF16(
		uint32(rng.Start.Line),
		uint32(rng.Start.Character),
	)
	end := document.LineIndex.OffsetUTF16(
		uint32(rng.End.Line),
		uint32(rng.End.Character),
	)
	return strings.TrimSpace(string(document.Text[start:end]))
}
