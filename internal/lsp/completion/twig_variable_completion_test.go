package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigControllerVariableCompletion(t *testing.T) {
	provider := twigVariableCompletionFixture(t)
	document, request := twigCompletionAt(
		"file:///project/templates/product/show.html.twig",
		`{{ pro }}`,
		strings.Index(`{{ pro }}`, "pro")+3,
	)
	items := provider.GetCompletions(context.Background(), request)

	product := requireCompletion(t, items, "product")
	assert.Equal(t, int(protocol.VariableCompletion), product.Kind)
	assert.Equal(t, "App\\Product", product.Detail)
	assert.Contains(t, product.Documentation.Value, "ProductController.php")
	assert.NotNil(t, document)
}

func TestTwigControllerVariableMemberCompletion(t *testing.T) {
	provider := twigVariableCompletionFixture(t)

	_, request := twigCompletionAt(
		"file:///project/templates/product/show.html.twig",
		`{{ product. }}`,
		strings.Index(`{{ product. }}`, ".")+1,
	)
	items := provider.GetCompletions(context.Background(), request)
	name := requireCompletion(t, items, "name")
	assert.Equal(t, int(protocol.PropertyCompletion), name.Kind)
	title := requireCompletion(t, items, "title")
	assert.Contains(t, title.Detail, "getTitle()")
	requireCompletion(t, items, "category")
	requireCompletion(t, items, "calculate")
	kind := requireCompletion(t, items, "KIND")
	assert.Equal(t, int(protocol.ConstantCompletion), kind.Kind)

	_, nestedRequest := twigCompletionAt(
		"file:///project/templates/product/show.html.twig",
		`{{ product.category. }}`,
		strings.LastIndex(`{{ product.category. }}`, ".")+1,
	)
	nestedItems := provider.GetCompletions(
		context.Background(),
		nestedRequest,
	)
	requireCompletion(t, nestedItems, "label")
}

func TestTwigControllerVariablesDoNotLeakAcrossTemplates(t *testing.T) {
	provider := twigVariableCompletionFixture(t)
	_, request := twigCompletionAt(
		"file:///project/templates/product/edit.html.twig",
		`{{ pro }}`,
		strings.Index(`{{ pro }}`, "pro")+3,
	)
	assert.NotContains(
		t,
		completionLabels(provider.GetCompletions(
			context.Background(),
			request,
		)),
		"product",
	)
}

func TestTwig3LoopVariableCompletionIsScoped(t *testing.T) {
	provider := twigVariableCompletionFixture(t)
	_, hasLoopContext := twig.LoopContextType(provider.phpIndex)
	require.False(t, hasLoopContext)
	tests := []struct {
		name   string
		source string
		found  bool
	}{
		{
			name: "loop body",
			source: `{% for entry in entries %}
{{ loop. }}
{% endfor %}`,
			found: true,
		},
		{
			name: "nested else keeps outer loop",
			source: `{% for entry in entries %}
{% for child in entry.children %}{% else %}{{ loop. }}{% endfor %}
{% endfor %}`,
			found: true,
		},
		{
			name: "for else",
			source: `{% for entry in entries %}{% else %}
{{ loop. }}
{% endfor %}`,
		},
		{
			name:   "outside loop",
			source: `{{ loop. }}`,
		},
		{
			name: "unrelated receiver",
			source: `{% for entry in entries %}
{{ entry. }}
{% endfor %}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			offset := strings.Index(test.source, ". }}") + 1
			require.Positive(t, offset)
			_, request := twigCompletionAt(
				"file:///project/templates/loop.html.twig",
				test.source,
				offset,
			)
			accessor := twigquery.ClosestNodeOfKind(
				request.Node,
				twigsyntax.TwigAccessor,
			)
			assert.Equal(
				t,
				test.found,
				twig.LoopAccessorInScope(accessor),
			)
			items := provider.GetCompletions(
				context.Background(),
				request,
			)
			labels := completionLabels(items)
			if !test.found {
				assert.NotContains(t, labels, "index")
				return
			}
			for _, name := range []string{
				"index",
				"index0",
				"revindex",
				"revindex0",
				"first",
				"last",
				"length",
				"parent",
			} {
				item := requireCompletion(t, items, name)
				assert.Equal(t, int(protocol.PropertyCompletion), item.Kind)
			}
			assert.NotContains(t, labels, "previous")
		})
	}
}

func TestTwig4LoopVariableCompletionUsesRuntimeClass(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/twig/twig/src/Runtime/LoopContext.php",
		[]byte(`<?php
namespace Twig\Runtime;
final class LoopContext {
    public function getIndex(): int {}
    public function getIndex0(): int {}
    public function isFirst(): bool {}
    public function isLast(): bool {}
    public function getPrevious(): mixed {}
    public function getNext(): mixed {}
    public function hasChanged(mixed $value): bool {}
    public function cycle(mixed ...$values): mixed {}
    public function getDepth(): int {}
    public function getDepth0(): int {}
    public function getFixtureOnly(): string {}
    public function setIgnored(mixed $value): void {}
    public function __invoke(iterable $iterator): iterable {}
    private function getPrivateValue(): string {}
}`),
	)))
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	provider := NewTwigCompletionProvider(
		"/project",
		twigIndex,
		nil,
		phpIndex,
	)
	source := `{% for entry in entries %}{{ loop. }}{% endfor %}`
	_, request := twigCompletionAt(
		"file:///project/templates/loop.html.twig",
		source,
		strings.Index(source, ". }}")+1,
	)
	items := provider.GetCompletions(context.Background(), request)
	for _, name := range []string{
		"index",
		"index0",
		"first",
		"last",
		"previous",
		"next",
		"changed",
		"cycle",
		"depth",
		"depth0",
		"fixtureOnly",
	} {
		requireCompletion(t, items, name)
	}
	labels := completionLabels(items)
	assert.NotContains(t, labels, "setIgnored")
	assert.NotContains(t, labels, "__invoke")
	assert.NotContains(t, labels, "privateValue")
	assert.NotContains(t, labels, "revindex")
}

func TestTwigGlobalVariableAndMemberCompletion(t *testing.T) {
	cache := t.TempDir()
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	twigIndex.SetDependencies(phpIndex, nil)
	source := []byte(`<?php
namespace App;
use Twig\Extension\AbstractExtension;
class Clock {
    public string $timezone;
    public function getNow(): string {}
}
class StorefrontExtension extends AbstractExtension {
    public function getGlobals(): array {
        return ['clock' => new Clock()];
    }
}`)
	path := "/project/src/StorefrontExtension.php"
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(path, source)))
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(path, source)))
	provider := NewTwigCompletionProvider(
		"/project",
		twigIndex,
		nil,
		phpIndex,
	)

	_, request := twigCompletionAt(
		"file:///project/templates/page.html.twig",
		`{{ clo }}`,
		strings.Index(`{{ clo }}`, "clo")+3,
	)
	clock := requireCompletion(
		t,
		provider.GetCompletions(context.Background(), request),
		"clock",
	)
	assert.Equal(t, "App\\Clock", clock.Detail)
	assert.Contains(t, clock.Documentation.Value, "Twig extension")

	_, memberRequest := twigCompletionAt(
		"file:///project/templates/page.html.twig",
		`{{ clock. }}`,
		strings.Index(`{{ clock. }}`, ".")+1,
	)
	members := provider.GetCompletions(
		context.Background(),
		memberRequest,
	)
	requireCompletion(t, members, "timezone")
	requireCompletion(t, members, "now")

	controller := []byte(`<?php
namespace App;
class LocalClock {
    public string $localOnly;
}
class PageController {
    public function page() {
        return $this->render('page.html.twig', [
            'clock' => new LocalClock(),
        ]);
    }
}`)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/PageController.php",
		controller,
	)))
	localMembers := provider.GetCompletions(
		context.Background(),
		memberRequest,
	)
	requireCompletion(t, localMembers, "localOnly")
	assert.NotContains(t, completionLabels(localMembers), "timezone")
}

func twigVariableCompletionFixture(t *testing.T) *TwigCompletionProvider {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/ProductController.php",
		[]byte(`<?php
namespace App;

class Category {
    public function getLabel(): string {}
}

class Product {
    public const KIND = 'product';
    public string $name;
    public function getTitle(): string {}
    public function getCategory(): Category {}
    public function calculate(): int {}
}

class ProductController {
    public function show(Product $product) {
        return $this->render('product/show.html.twig', [
            'product' => $product,
        ]);
    }
}`),
	)))
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	return NewTwigCompletionProvider(
		"/project",
		twigIndex,
		nil,
		phpIndex,
	)
}

func twigCompletionAt(
	uri,
	source string,
	offset int,
) (*lsp.TextDocument, *lsp.CompletionRequest) {
	document := lsp.NewTextDocument(uri, source, 1)
	nodeOffset := offset
	if nodeOffset > 0 {
		nodeOffset--
	}
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(nodeOffset))
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = uri
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
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
			Node:            node,
		},
	}
}

func completionLabels(items []protocol.CompletionItem) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.Label)
	}
	return result
}
