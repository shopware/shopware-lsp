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
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestTwigTemplateDefinitionSupportsTemplateAnnotation(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "templates", "page.html.twig")
	require.NoError(t, os.MkdirAll(filepath.Dir(targetPath), 0o755))
	require.NoError(t, os.WriteFile(targetPath, []byte("page"), 0o644))
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		targetPath,
		[]byte("page"),
	)))

	source := `<?php
/** @Template("page.html.twig") */
final class PageController {}
`
	path := filepath.Join(root, "src", "PageController.php")
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, "page.html.twig") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewTwigDefinitionProvider(
		root,
		index,
		nil,
		nil,
	).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:  document,
				Root:      document.SyntaxTree.Root,
				Node:      document.SyntaxTree.Root.NodeAtOffset(offset),
				LineIndex: document.LineIndex,
			},
		},
	)
	require.Len(t, locations, 1)
	require.Equal(t, uriutil.FileURI(targetPath), locations[0].URI)
}

func TestPHPDocTemplateAssistantTagDefinition(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "templates", "page.html.twig")
	require.NoError(t, os.MkdirAll(filepath.Dir(targetPath), 0o755))
	require.NoError(t, os.WriteFile(targetPath, []byte("page"), 0o644))
	twigIndex, err := twig.NewTwigIndexer(filepath.Join(root, "twig"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		targetPath,
		[]byte("page"),
	)))
	phpIndex, err := php.NewPHPIndex(filepath.Join(root, "php"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "src", "TemplateAssistant.php"),
		[]byte(`<?php
/** @param string $template #Template */
function resolve_template(string $template): void {}
`),
	)))
	source := "<?php resolve_template('page.html.twig');"
	path := filepath.Join(root, "src", "Usage.php")
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, "page.html.twig") + 3)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		document.Version,
		node,
		document.SyntaxTree.Root,
	)
	locations := NewTwigDefinitionProvider(
		root,
		twigIndex,
		nil,
		phpIndex,
	).GetDefinition(
		ctx,
		securityDefinitionRequest(document, node, offset),
	)
	require.Len(t, locations, 1)
	require.Equal(t, uriutil.FileURI(targetPath), locations[0].URI)
}

func TestStandalonePHPTemplateStringDefinitionIsExactAndLowNoise(
	t *testing.T,
) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "templates", "page.html.twig")
	require.NoError(t, os.MkdirAll(filepath.Dir(targetPath), 0o755))
	require.NoError(t, os.WriteFile(targetPath, []byte("page"), 0o644))
	twigIndex, err := twig.NewTwigIndexer(filepath.Join(root, "twig"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		targetPath,
		[]byte("page"),
	)))
	provider := NewTwigDefinitionProvider(root, twigIndex, nil, nil)

	for _, test := range []struct {
		name      string
		source    string
		marker    string
		wantCount int
	}{
		{
			name:      "standalone exact template",
			source:    `<?php $template = 'page.html.twig';`,
			marker:    "page.html.twig",
			wantCount: 1,
		},
		{
			name:      "unknown template",
			source:    `<?php $template = 'missing.html.twig';`,
			marker:    "missing.html.twig",
			wantCount: 0,
		},
		{
			name:      "dynamic interpolated string",
			source:    `<?php $template = "page.$locale.html.twig";`,
			marker:    "page.",
			wantCount: 0,
		},
		{
			name:      "ordinary PHP string",
			source:    `<?php $template = 'page.html.txt';`,
			marker:    "page.html.txt",
			wantCount: 0,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(root, "src", "Standalone.php")
			document := lsp.NewTextDocument(
				uriutil.FileURI(path),
				test.source,
				1,
			)
			offset := uint32(strings.Index(test.source, test.marker) + 2)
			node := document.SyntaxTree.Root.NodeAtOffset(offset)
			locations := provider.GetDefinition(
				context.Background(),
				securityDefinitionRequest(document, node, offset),
			)
			require.Len(t, locations, test.wantCount)
			if test.wantCount != 0 {
				require.Equal(
					t,
					uriutil.FileURI(targetPath),
					locations[0].URI,
				)
			}
		})
	}
}
