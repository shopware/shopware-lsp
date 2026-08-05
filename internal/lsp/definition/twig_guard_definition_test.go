package definition

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigGuardDefinitionNavigatesToRegisteredCallables(t *testing.T) {
	root := t.TempDir()
	extensionPath := filepath.Join(root, "src", "AppExtension.php")
	extensionSource := []byte(`<?php
class AppExtension extends \Twig\Extension\AbstractExtension
{
    public function getFunctions(): array
    {
        return [new \Twig\TwigFunction('asset_url', $this->asset(...))];
    }
    public function getFilters(): array
    {
        return [new \Twig\TwigFilter('money', $this->money(...))];
    }
    public function getTests(): array
    {
        return [new \Twig\TwigTest('positive', $this->positive(...))];
    }
    public function asset(string $path): string { return $path; }
    public function money(int $value): string { return (string) $value; }
    public function positive(int $value): bool { return $value > 0; }
}`)
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		extensionPath,
		extensionSource,
	)))
	provider := NewTwigDefinitionProvider(root, twigIndex, nil, nil)

	for _, test := range []struct {
		source string
		line   int
	}{
		{
			source: `{% guard function asset_<caret>url %}`,
			line:   twigGuardFunctionLine(t, twigIndex, "asset_url"),
		},
		{
			source: `{% guard filter mon<caret>ey %}`,
			line:   twigGuardFilterLine(t, twigIndex, "money"),
		},
		{
			source: `{% guard test pos<caret>itive %}`,
			line:   twigGuardTestLine(t, twigIndex, "positive"),
		},
	} {
		locations := provider.GetDefinition(
			context.Background(),
			twigGuardDefinitionRequest(t, root, test.source),
		)
		require.Len(t, locations, 1, test.source)
		assert.Equal(t, uriutil.FileURI(extensionPath), locations[0].URI)
		assert.Equal(t, test.line-1, locations[0].Range.Start.Line)
	}
}

func twigGuardDefinitionRequest(
	t *testing.T,
	root,
	source string,
) *lsp.DefinitionRequest {
	t.Helper()
	offset := strings.Index(source, "<caret>")
	require.NotEqual(t, -1, offset)
	source = strings.Replace(source, "<caret>", "", 1)
	document := lsp.NewTextDocument(
		uriutil.FileURI(
			filepath.Join(root, "templates", "page.html.twig"),
		),
		source,
		1,
	)
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return &lsp.DefinitionRequest{
		DefinitionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node: document.SyntaxTree.Root.NodeAtOffset(
				uint32(offset),
			),
		},
	}
}

func twigGuardFunctionLine(
	t *testing.T,
	index *twig.TwigIndexer,
	name string,
) int {
	t.Helper()
	values, err := index.GetTwigFunction(name)
	require.NoError(t, err)
	require.Len(t, values, 1)
	return values[0].Line
}

func twigGuardFilterLine(
	t *testing.T,
	index *twig.TwigIndexer,
	name string,
) int {
	t.Helper()
	values, err := index.GetTwigFilter(name)
	require.NoError(t, err)
	require.Len(t, values, 1)
	return values[0].Line
}

func twigGuardTestLine(
	t *testing.T,
	index *twig.TwigIndexer,
	name string,
) int {
	t.Helper()
	values, err := index.GetTwigTest(name)
	require.NoError(t, err)
	require.Len(t, values, 1)
	return values[0].Line
}
