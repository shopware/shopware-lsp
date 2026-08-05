package definition

import (
	"context"
	"os"
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

func TestTwigTestDefinitionNavigatesToRegistration(t *testing.T) {
	root := t.TempDir()
	extensionPath := filepath.Join(root, "src", "AppExtension.php")
	source := `<?php
class AppExtension extends \Twig\Extension\AbstractExtension
{
    public function getTests(): array
    {
        return [new \Twig\TwigTest('positive', $this->positive(...))];
    }
    public function positive(int $value): bool { return $value > 0; }
}`
	require.NoError(t, os.MkdirAll(filepath.Dir(extensionPath), 0o700))
	require.NoError(t, os.WriteFile(extensionPath, []byte(source), 0o600))
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		extensionPath,
		[]byte(source),
	)))
	provider := NewTwigDefinitionProvider(root, twigIndex, nil, nil)
	values, err := twigIndex.GetTwigTest("positive")
	require.NoError(t, err)
	require.Len(t, values, 1)

	for _, template := range []string{
		`{% if value is pos<caret>itive %}{% endif %}`,
		`{{ value is positive<caret>(1) }}`,
		`{% set valid = value is not positive<caret> %}`,
	} {
		documentSource := strings.Replace(template, "<caret>", "", 1)
		offset := strings.Index(template, "<caret>")
		document := lsp.NewTextDocument(
			uriutil.FileURI(filepath.Join(
				root,
				"templates",
				"page.html.twig",
			)),
			documentSource,
			1,
		)
		line, character := document.LineIndex.PositionUTF16(uint32(offset))
		params := &protocol.DefinitionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		locations := provider.GetDefinition(
			context.Background(),
			&lsp.DefinitionRequest{
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
			},
		)
		require.Len(t, locations, 1, template)
		assert.Equal(t, uriutil.FileURI(extensionPath), locations[0].URI)
		assert.Equal(t, values[0].Line-1, locations[0].Range.Start.Line)
	}
}
