package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestTwigIncludeParameterDefinitionNavigatesToTargetAndParent(
	t *testing.T,
) {
	root := t.TempDir()
	index, err := twig.NewTwigIndexer(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })

	basePath := filepath.Join(root, "templates/base.html.twig")
	cardPath := filepath.Join(root, "templates/card.html.twig")
	for path, source := range map[string]string{
		basePath: `{{ inherited }}`,
		cardPath: `{% extends 'base.html.twig' %}
{{ title }}`,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	provider := NewTwigIncludeParameterDefinitionProvider(index, nil)

	title := twigIncludeParameterDefinitions(
		t,
		provider,
		`{% include 'card.html.twig' with {'title': value} %}`,
		"title",
	)
	require.Len(t, title, 1)
	require.Equal(t, uriutil.FileURI(cardPath), title[0].URI)
	require.Equal(t, 1, title[0].Range.Start.Line)
	require.Equal(t, 3, title[0].Range.Start.Character)

	inherited := twigIncludeParameterDefinitions(
		t,
		provider,
		`{{ include('card.html.twig', {inherited: value}) }}`,
		"inherited",
	)
	require.Len(t, inherited, 1)
	require.Equal(t, uriutil.FileURI(basePath), inherited[0].URI)
}

func twigIncludeParameterDefinitions(
	t *testing.T,
	provider *TwigIncludeParameterDefinitionProvider,
	source,
	needle string,
) []protocol.Location {
	t.Helper()
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		source,
		1,
	)
	offset := uint32(strings.Index(source, needle) + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return provider.GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
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
}
