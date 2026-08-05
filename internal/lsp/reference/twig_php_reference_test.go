package reference

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

func TestTwigPHPReferenceProviderFindsTypedMembersAndClasses(t *testing.T) {
	projectRoot := t.TempDir()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	phpPath := filepath.Join(projectRoot, "src", "Model.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(phpPath), 0o700))
	phpSource := `<?php
namespace App;
class Product {
    public string $name;
    public function getNumber(): string { return ''; }
}
enum Status { case ACTIVE; }
`
	require.NoError(t, os.WriteFile(phpPath, []byte(phpSource), 0o600))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		[]byte(phpSource),
	)))

	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	twigIndex.SetDependencies(phpIndex, nil)
	memberPath := filepath.Join(projectRoot, "templates", "member.html.twig")
	memberSource := `{# @var product \App\Product #}
{{ product.number }} {{ product.getNumber() }}`
	classPath := filepath.Join(projectRoot, "templates", "class.html.twig")
	classSource := `{# @var status \App\Status #}
{{ enum_cases('App\\Status') }}`
	require.NoError(t, os.MkdirAll(filepath.Dir(memberPath), 0o700))
	require.NoError(t, os.WriteFile(memberPath, []byte(memberSource), 0o600))
	require.NoError(t, os.WriteFile(classPath, []byte(classSource), 0o600))
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		memberPath,
		[]byte(memberSource),
	)))
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		classPath,
		[]byte(classSource),
	)))
	provider := NewTwigPHPReferenceProvider(twigIndex, phpIndex)

	for _, test := range []struct {
		name     string
		needle   string
		expected int
	}{
		{"getter", "getNumber", 2},
		{"class", "Status {", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(phpPath),
				phpSource,
				1,
			)
			offset := uint32(strings.Index(phpSource, test.needle) + 2)
			line, character := document.LineIndex.PositionUTF16(offset)
			params := &protocol.ReferenceParams{}
			params.TextDocument.URI = document.URI
			params.Position.Line = int(line)
			params.Position.Character = int(character)
			request := &lsp.ReferenceRequest{
				ReferenceParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document:        document,
					DocumentContent: document.Text,
					DocumentTree:    document.SyntaxTree,
					LineIndex:       document.LineIndex,
					Root:            document.SyntaxTree.Root,
					Node: document.SyntaxTree.Root.NodeAtOffset(
						offset,
					),
				},
			}
			ctx := phpIndex.AddDocumentContext(
				context.Background(),
				phpPath,
				document.Version,
				request.Node,
				request.Root,
			)
			locations, referenceErr := provider.GetReferences(ctx, request)
			require.NoError(t, referenceErr)
			require.Len(t, locations, test.expected)
		})
	}
}

func TestTwigPHPReferenceProviderFindsExactExtensionSymbolUsagesFromTwig(
	t *testing.T,
) {
	projectRoot := t.TempDir()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	twigIndex.SetDependencies(phpIndex, nil)

	sources := map[string]string{
		"index.html.twig":     `{{ form_start() }}`,
		"secondary.html.twig": `{{ form_start() }}`,
		"other.html.twig":     `{{ form() }} {{ form_end() }}`,
	}
	paths := make(map[string]string, len(sources))
	for name, source := range sources {
		path := filepath.Join(projectRoot, "templates", name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
		require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
		paths[name] = path
	}

	currentPath := paths["index.html.twig"]
	source := sources["index.html.twig"]
	document := lsp.NewTextDocument(
		uriutil.FileURI(currentPath),
		source,
		2,
	)
	offset := uint32(strings.Index(source, "form_start") + 3)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	request := &lsp.ReferenceRequest{
		ReferenceParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node: document.SyntaxTree.Root.NodeAtOffset(
				offset,
			),
		},
	}
	locations, err := NewTwigPHPReferenceProvider(
		twigIndex,
		phpIndex,
	).GetReferences(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, locations, 2)
	require.ElementsMatch(t, []string{
		uriutil.FileURI(paths["index.html.twig"]),
		uriutil.FileURI(paths["secondary.html.twig"]),
	}, []string{
		locations[0].URI,
		locations[1].URI,
	})
}

func TestTwigPHPReferenceProviderLinksExtensionMethodsToTwigUsages(
	t *testing.T,
) {
	projectRoot := t.TempDir()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	phpPath := filepath.Join(projectRoot, "src", "AppExtension.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(phpPath), 0o700))
	phpSource := `<?php
namespace App\Twig;
use Twig\Attribute\AsTwigFunction;
use Twig\Attribute\AsTwigTest;
class AppExtension {
    #[AsTwigFunction('product_number_function')]
    public function formatProductNumberFunction(string $number): string {
        return $number;
    }

    #[AsTwigTest('product_number_test')]
    public function isProductNumber(string $number): bool {
        return $number !== '';
    }
}`
	require.NoError(t, os.WriteFile(phpPath, []byte(phpSource), 0o600))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		[]byte(phpSource),
	)))
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	twigIndex.SetDependencies(phpIndex, nil)
	for _, name := range []string{"first.html.twig", "second.html.twig"} {
		path := filepath.Join(projectRoot, "templates", name)
		source := `{{ product_number_function('123') }}
{% if '123' is product_number_test %}{% endif %}`
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
		require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	// Index the extension after its templates to prove query-time callback
	// resolution does not depend on discovery order.
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		phpPath,
		[]byte(phpSource),
	)))

	document := lsp.NewTextDocument(
		uriutil.FileURI(phpPath),
		phpSource,
		1,
	)
	for _, method := range []string{
		"formatProductNumberFunction",
		"isProductNumber",
	} {
		offset := uint32(strings.Index(phpSource, method) + 3)
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.ReferenceParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		request := &lsp.ReferenceRequest{
			ReferenceParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(
					offset,
				),
			},
		}
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			phpPath,
			document.Version,
			request.Node,
			request.Root,
		)
		locations, referenceErr := NewTwigPHPReferenceProvider(
			twigIndex,
			phpIndex,
		).GetReferences(ctx, request)
		require.NoError(t, referenceErr)
		require.Len(t, locations, 2, method)
	}
}
