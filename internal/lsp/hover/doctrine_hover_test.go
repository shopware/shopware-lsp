package hover

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/require"
)

func TestDoctrineHoverDescribesTypeRegistration(t *testing.T) {
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	source := `<?php
use App\Doctrine\MoneyType;

return static function ($containerConfigurator): void {
    $containerConfigurator->extension('doctrine', [
        'dbal' => ['types' => ['money' => MoneyType::class]],
    ]);
};`
	document := lsp.NewTextDocument(
		"file:///project/config/packages/doctrine.php",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "'money'") + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewDoctrineHoverProvider(
		doctrineIndex,
		phpIndex,
	).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(
					offset,
				),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(
		t,
		result.Contents.Value,
		"Doctrine DBAL type registration",
	)
	require.Contains(t, result.Contents.Value, "money")
	require.Contains(t, result.Contents.Value, "App\\Doctrine\\MoneyType")
}
