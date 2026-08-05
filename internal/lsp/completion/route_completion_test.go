package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteNameCompletionIncludesPathAndParameters(t *testing.T) {
	provider := routeCompletionFixture(t)
	source := `{{ path('') }}`
	root := twigparser.Parse(source).Tree.Root
	node := twigquery.Nodes(root, twigsyntax.TwigLiteralString)[0]
	items := provider.GetCompletions(
		context.Background(),
		routeCompletionRequest("file:///project/view.twig", root, node, source),
	)
	item := requireCompletion(t, items, "product.show")
	assert.Equal(t, int(protocol.ReferenceCompletion), item.Kind)
	assert.Equal(t, "/products/{id} (id)", item.Detail)
}

func TestRoutePathCompletionInPHPAttributesAndAnnotations(t *testing.T) {
	provider := routeCompletionFixture(t)
	for _, fixture := range []struct {
		name   string
		source string
	}{
		{
			name: "positional attribute",
			source: `<?php
#[Route('/pro<caret>')]
function show() {}
`,
		},
		{
			name: "named attribute",
			source: `<?php
#[Route(name: 'draft', path: '/pro<caret>')]
function show() {}
`,
		},
		{
			name: "positional annotation",
			source: `<?php
/** @Route("/pro<caret>") */
function show() {}
`,
		},
		{
			name: "named annotation",
			source: `<?php
/** @Route(name="draft", path="/pro<caret>") */
function show() {}
`,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source, offset := completionCaret(t, fixture.source)
			document, request := bundleResourceCompletionRequest(
				t,
				"/project/src/Controller.php",
				source,
				offset,
			)
			item := requireCompletion(
				t,
				provider.GetCompletions(context.Background(), request),
				"/products/{id}",
			)
			assert.Equal(t, int(protocol.ReferenceCompletion), item.Kind)
			assert.Equal(t, "product.show", item.Detail)
			edit, ok := item.TextEdit.(protocol.TextEdit)
			require.True(t, ok)
			assert.Equal(t, "/products/{id}", edit.NewText)
			assert.Equal(
				t,
				"/pro",
				completionRangeText(document, edit.Range),
			)
		})
	}
}

func TestGeneratedRouteNameCompletionInPHPAttributesAndAnnotations(
	t *testing.T,
) {
	provider := routeCompletionFixture(t)
	for _, fixture := range []struct {
		name   string
		source string
	}{
		{
			name: "native attribute",
			source: `<?php
namespace App\Controller;
class FooController {
    #[Route(path: '/foo', name: 'dr<caret>')]
    public function foo1(): void {}
}
`,
		},
		{
			name: "legacy annotation",
			source: `<?php
namespace App\Controller;
class FooController {
    /** @Route(path="/foo", name="dr<caret>") */
    public function foo1(): void {}
}
`,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source, offset := completionCaret(t, fixture.source)
			document, request := bundleResourceCompletionRequest(
				t,
				"/project/src/FooController.php",
				source,
				offset,
			)
			item := requireCompletion(
				t,
				provider.GetCompletions(context.Background(), request),
				"app_foo_foo1",
			)
			assert.Equal(t, int(protocol.ReferenceCompletion), item.Kind)
			edit, ok := item.TextEdit.(protocol.TextEdit)
			require.True(t, ok)
			assert.Equal(t, "app_foo_foo1", edit.NewText)
			assert.Equal(
				t,
				"dr",
				completionRangeText(document, edit.Range),
			)
		})
	}
}

func TestPHPDocRouteAssistantTagCompletesCallArguments(t *testing.T) {
	routeIndex, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routeIndex.Close()) })
	require.NoError(t, routeIndex.Index(indexer.NewParsedFile(
		"/project/config/routes.yaml",
		[]byte("product.show:\n  path: /products/{id}\n"),
	)))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/RouteAware.php",
		[]byte(`<?php
interface RouteAware
{
    /** @param string $route #Route */
    public function open(string $route): void;
}

class TestClass implements RouteAware
{
    /** @param string $route #Route */
    public function __construct(string $route) {}

    public function open(string $route): void {}

    /** @param string $value ordinary text */
    public function other(string $value): void {}
}

/** @param string $route #Route */
function open_route(string $route): void {}
`),
	)))
	provider := NewRouteCompletionProvider(routeIndex)
	for _, fixture := range []struct {
		name   string
		source string
		found  bool
	}{
		{
			name:   "constructor",
			source: "<?php new TestClass('prod<caret>');",
			found:  true,
		},
		{
			name: "inherited method contract",
			source: `<?php
$helper = new TestClass('seed');
$helper->open('prod<caret>');
`,
			found: true,
		},
		{
			name:   "function",
			source: "<?php open_route('prod<caret>');",
			found:  true,
		},
		{
			name: "unmarked parameter",
			source: `<?php
$helper = new TestClass('seed');
$helper->other('prod<caret>');
`,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source, offset := completionCaret(t, fixture.source)
			document, request := bundleResourceCompletionRequest(
				t,
				"/project/src/Usage.php",
				source,
				offset,
			)
			ctx := phpIndex.AddDocumentContext(
				context.Background(),
				"/project/src/Usage.php",
				document.Version,
				request.Node,
				request.Root,
			)
			items := provider.GetCompletions(ctx, request)
			if !fixture.found {
				assert.NotContains(
					t,
					completionLabels(items),
					"product.show",
				)
				return
			}
			item := requireCompletion(t, items, "product.show")
			edit, ok := item.TextEdit.(protocol.TextEdit)
			require.True(t, ok)
			assert.Equal(t, "product.show", edit.NewText)
			assert.Equal(
				t,
				"prod",
				completionRangeText(document, edit.Range),
			)
		})
	}
}

func TestRouteNameCompletionInTwigRouteComparison(t *testing.T) {
	provider := routeCompletionFixture(t)
	source := `{% if app.request.attributes.get('_route') in ['product.'] %}{% endif %}`
	root := twigparser.Parse(source).Tree.Root
	literal := twigquery.Nodes(root, twigsyntax.TwigLiteralString)[1]
	item := requireCompletion(
		t,
		provider.GetCompletions(
			context.Background(),
			routeCompletionRequest(
				"file:///project/view.twig",
				root,
				literal,
				source,
			),
		),
		"product.show",
	)
	assert.Equal(t, "/products/{id} (id)", item.Detail)

	prefixSource := `{% if app.request.attributes.get('_route') starts with 'product.' %}{% endif %}`
	prefixRoot := twigparser.Parse(prefixSource).Tree.Root
	prefixLiteral := twigquery.Nodes(
		prefixRoot,
		twigsyntax.TwigLiteralString,
	)[1]
	assert.Empty(t, provider.GetCompletions(
		context.Background(),
		routeCompletionRequest(
			"file:///project/view.twig",
			prefixRoot,
			prefixLiteral,
			prefixSource,
		),
	))
}

func TestRouteParameterCompletionInPHPAndTwig(t *testing.T) {
	provider := routeCompletionFixture(t)

	phpSource := `<?php $this->redirectToRoute('product.show', ['' => 1]);`
	phpRoot := phpparser.Parse(phpSource).Tree.Root
	phpStrings := phpquery.Nodes(phpRoot, phpsyntax.PhpString)
	require.Len(t, phpStrings, 2)
	phpItems := provider.GetCompletions(
		context.Background(),
		routeCompletionRequest(
			"file:///project/Controller.php",
			phpRoot,
			phpStrings[1],
			phpSource,
		),
	)
	requireCompletion(t, phpItems, "id")
	requireCompletion(t, phpItems, "_fragment")

	twigSource := `{{ path('product.show', {'': product.id}) }}`
	twigRoot := twigparser.Parse(twigSource).Tree.Root
	twigStrings := twigquery.Nodes(twigRoot, twigsyntax.TwigLiteralString)
	require.Len(t, twigStrings, 2)
	twigItems := provider.GetCompletions(
		context.Background(),
		routeCompletionRequest(
			"file:///project/view.twig",
			twigRoot,
			twigStrings[1],
			twigSource,
		),
	)
	requireCompletion(t, twigItems, "id")
}

func TestRouteParameterCompletionSupportsTwigIdentifierHashKeys(t *testing.T) {
	provider := routeCompletionFixture(t)
	for _, sourceWithCaret := range []string{
		`{{ path('product.show', {<caret>}) }}`,
		`{{ path('product.show', {i<caret>d}) }}`,
		`{{ path('product.show', {i<caret>d: product.id}) }}`,
		`{{ path('product.show', {'other': 1, i<caret>d}) }}`,
		`{{ path('product.show', {'other': 1, i<caret>d: product.id}) }}`,
	} {
		t.Run(sourceWithCaret, func(t *testing.T) {
			caret := strings.Index(sourceWithCaret, "<caret>")
			require.NotEqual(t, -1, caret)
			source := strings.Replace(sourceWithCaret, "<caret>", "", 1)
			document := lsp.NewTextDocument(
				"file:///project/view.twig",
				source,
				1,
			)
			offset := uint32(caret)
			node := document.SyntaxTree.Root.NodeAtOffset(offset)
			if node == nil && offset > 0 {
				node = document.SyntaxTree.Root.NodeAtOffset(offset - 1)
			}
			require.NotNil(t, node)
			requireCompletion(
				t,
				provider.GetCompletions(
					context.Background(),
					routeCompletionRequest(
						document.URI,
						document.SyntaxTree.Root,
						node,
						source,
					),
				),
				"id",
			)
		})
	}
}

func TestRouteCompletionWrapsTwigHTMLURLs(t *testing.T) {
	provider := routeCompletionFixture(t)
	tests := []struct {
		source  string
		marker  string
		newText string
	}{
		{
			source:  `<a href="/prod">Product</a>`,
			marker:  "/prod",
			newText: `{{ path('product.show', {'id': 'x'}) }}`,
		},
		{
			source:  `<form action=''></form>`,
			marker:  "",
			newText: `{{ path("product.show", {"id": "x"}) }}`,
		},
	}
	for _, test := range tests {
		document := lsp.NewTextDocument(
			"file:///project/view.twig",
			test.source,
			1,
		)
		var offset uint32
		if test.marker == "" {
			offset = uint32(strings.Index(test.source, "''") + 1)
		} else {
			offset = uint32(
				strings.Index(test.source, test.marker) + len(test.marker),
			)
		}
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		item := requireCompletion(
			t,
			provider.GetCompletions(
				context.Background(),
				routeCompletionRequest(
					document.URI,
					document.SyntaxTree.Root,
					node,
					test.source,
				),
			),
			"product.show",
		)
		edit, ok := item.TextEdit.(protocol.TextEdit)
		require.True(t, ok)
		assert.Equal(t, test.newText, edit.NewText)
		assert.Equal(t, 0, edit.Range.Start.Line)
	}
}

func TestRouteCompletionReplacesJavaScriptRequestURLWithPath(t *testing.T) {
	provider := routeCompletionFixture(t)
	for _, source := range []string{
		`fetch('/prod')`,
		`axios.create({ baseURL: '/prod' })`,
		`const options = { url: '/prod' }`,
	} {
		document := lsp.NewTextDocument(
			"file:///project/request.js",
			source,
			1,
		)
		offset := uint32(strings.Index(source, "/prod") + len("/prod"))
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		item := requireCompletion(
			t,
			provider.GetCompletions(
				context.Background(),
				routeCompletionRequest(
					document.URI,
					document.SyntaxTree.Root,
					node,
					source,
				),
			),
			"product.show",
		)
		pathItem := requireCompletion(t, provider.GetCompletions(
			context.Background(),
			routeCompletionRequest(
				document.URI,
				document.SyntaxTree.Root,
				node,
				source,
			),
		), "/products/{id}")
		assert.Equal(t, "product.show", pathItem.Detail)
		edit, ok := item.TextEdit.(protocol.TextEdit)
		require.True(t, ok)
		assert.Equal(t, "/products/{id}", edit.NewText)
		assert.Equal(
			t,
			protocol.Range{
				Start: protocol.Position{
					Line:      0,
					Character: strings.Index(source, "/prod"),
				},
				End: protocol.Position{
					Line: strings.Count(
						source[:strings.Index(source, "/prod")+len("/prod")],
						"\n",
					),
					Character: strings.Index(source, "/prod") + len("/prod"),
				},
			},
			edit.Range,
		)
	}
}

func TestRouteCompletionIgnoresUnrelatedJavaScriptStrings(t *testing.T) {
	provider := routeCompletionFixture(t)
	source := `logger.info('/prod')`
	document := lsp.NewTextDocument(
		"file:///project/request.js",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "/prod") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	assert.Empty(t, provider.GetCompletions(
		context.Background(),
		routeCompletionRequest(
			document.URI,
			document.SyntaxTree.Root,
			node,
			source,
		),
	))
}

func routeCompletionFixture(t *testing.T) *RouteCompletionProvider {
	t.Helper()
	idx, err := symfony.NewRouteIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/config/routes.yaml",
		[]byte("product.show:\n    path: /products/{id}\n"),
	)))
	return NewRouteCompletionProvider(idx)
}

func routeCompletionRequest(
	uri string,
	root,
	node *cst.Node,
	source string,
) *lsp.CompletionRequest {
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = uri
	return &lsp.CompletionRequest{
		CompletionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Root:            root,
			Node:            node,
			DocumentContent: []byte(source),
			LineIndex:       cst.NewLineIndex(source),
		},
	}
}

func requireCompletion(
	t *testing.T,
	items []protocol.CompletionItem,
	label string,
) protocol.CompletionItem {
	t.Helper()
	for _, item := range items {
		if item.Label == label {
			return item
		}
	}
	require.Failf(t, "completion not found", "%q not in %#v", label, items)
	return protocol.CompletionItem{}
}
