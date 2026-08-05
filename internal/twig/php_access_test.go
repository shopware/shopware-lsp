package twig

import (
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/stretchr/testify/require"
)

func TestPHPAccessResolverHandlesVariablesFunctionsAndFilters(t *testing.T) {
	cache := t.TempDir()
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	twigIndex, err := NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	twigIndex.SetDependencies(phpIndex, nil)

	path := "/project/src/Twig/StringExtension.php"
	source := []byte(`<?php
namespace Foo {
    class Bar {
        /** @deprecated */
        public string $deprecatedProperty;

        #[\Deprecated]
        public string $deprecatedAttributeProperty;

        public function getNext(): Bar { return new Bar(); }

        /** @deprecated */
        public function getDeprecated(): static { return $this; }
    }
}
namespace Twig\Extra\String {
    class StringExtension extends \Twig_Extension {
        public function getFilters(): array {
            return [new \Twig_SimpleFilter('u', [$this, 'createBar'])];
        }
        public function getFunctions(): array {
            return [new \Twig_SimpleFunction('ustring', [$this, 'createBar'])];
        }
        public function createBar(?string $text = null): \Foo\Bar {
            return new \Foo\Bar();
        }
    }
}`)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(path, source)))
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(path, source)))
	functions, err := twigIndex.GetTwigFunction("ustring")
	require.NoError(t, err)
	require.Len(t, functions, 1)
	filters, err := twigIndex.GetTwigFilter("u")
	require.NoError(t, err)
	require.Len(t, filters, 1)

	document := twigparser.Parse(`{# @var bar \Foo\Bar #}
{{ bar.next.deprecated }}
{{ bar.deprecatedProperty }}
{{ bar.deprecatedAttributeProperty }}
{{ ustring('value').deprecated }}
{{ 'value'|u.deprecated }}
{{ bar.deprecated.deprecated }}
`)
	root := document.Tree.Root
	resolver := PHPAccessResolver{PHP: phpIndex, Twig: twigIndex}
	require.Equal(t, "Foo\\Bar", resolver.twigFunctionType("ustring").String())
	require.Equal(t, "Foo\\Bar", resolver.twigFilterType("u").String())
	resolvedCounts := make(map[string]int)
	for _, name := range []string{
		"deprecated",
		"deprecatedProperty",
		"deprecatedAttributeProperty",
	} {
		found := false
		for _, node := range twigquery.Nodes(root, twigsyntax.TwigLiteralName) {
			if node.Text() != name {
				continue
			}
			resolution, ok := resolver.ResolveName(
				"/project/templates/page.html.twig",
				root,
				node,
			)
			if !ok {
				continue
			}
			found = true
			resolvedCounts[name]++
			require.NotEmpty(t, resolution.Members)
			require.True(
				t,
				resolution.Members[0].Symbol.Flags.Has(
					semantic.DeprecatedFlag,
				),
				name,
			)
		}
		require.True(t, found, name)
	}
	require.Equal(t, 5, resolvedCounts["deprecated"])
	require.Equal(t, 1, resolvedCounts["deprecatedProperty"])
	require.Equal(t, 1, resolvedCounts["deprecatedAttributeProperty"])
}

func TestTwigTypeAnnotationsSupportBothOrders(t *testing.T) {
	root := twigparser.Parse(`{# @var product \App\Product #}
{# @var \App\Category $category #}
{# @var \App\Customer customer #}
{% types {
    order: '\App\Order',
    products?: '\App\Product[]',
} %}`).Tree.Root
	annotations := TwigTypeAnnotations(root)
	require.Equal(t, "App\\Product", annotations["product"].String())
	require.Equal(t, "App\\Category", annotations["category"].String())
	require.Equal(t, "App\\Customer", annotations["customer"].String())
	require.Equal(t, "App\\Order", annotations["order"].String())
	require.Equal(t, "list<App\\Product>", annotations["products"].String())
}

func TestPHPAccessResolverScopesPreparedAnnotationsToDocument(t *testing.T) {
	first := twigparser.Parse(
		`{# @var value \App\First #}`,
	).Tree.Root
	second := twigparser.Parse(
		`{# @var value \App\Second #}`,
	).Tree.Root

	resolver := (PHPAccessResolver{}).forDocument(first)
	require.Same(t, first, resolver.typeAnnotationsRoot)
	require.Equal(
		t,
		"App\\First",
		resolver.rootNameType("", first, "value").String(),
	)
	require.Equal(
		t,
		"App\\Second",
		resolver.rootNameType("", second, "value").String(),
	)
}

func TestPHPAccessResolverTypesTwigForVariablesFromTypesTag(
	t *testing.T,
) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Product.php",
		[]byte(`<?php
namespace App;
class Product {
    public string $name;
}`),
	)))
	root := twigparser.Parse(`{% types {
    products: 'App\\Product[]',
} %}
{% for key, product in products %}
{{ product.name }}
{% else %}
{{ product.name }}
{% endfor %}`).Tree.Root
	resolver := PHPAccessResolver{PHP: phpIndex}
	var bodyResolved, elseResolved int
	for _, node := range twigquery.Nodes(root, twigsyntax.TwigLiteralName) {
		if strings.TrimSpace(node.Text()) != "name" {
			continue
		}
		_, found := resolver.ResolveName(
			"/project/templates/products.html.twig",
			root,
			node,
		)
		if found {
			bodyResolved++
		} else {
			elseResolved++
		}
	}
	require.Equal(t, 1, bodyResolved)
	require.Equal(t, 1, elseResolved)
}

func TestPHPAccessResolverUsesTwig4LoopContextOnlyInForBody(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/twig/twig/src/Runtime/LoopContext.php",
		[]byte(`<?php
namespace Twig\Runtime;
final class LoopContext {
    public function getPrevious(): mixed {}
}`),
	)))
	root := twigparser.Parse(`{% for entry in entries %}
{{ loop.previous }}
{% else %}
{{ loop.previous }}
{% endfor %}
{{ loop.previous }}`).Tree.Root
	resolver := PHPAccessResolver{PHP: phpIndex}
	var resolved int
	for _, node := range twigquery.Nodes(root, twigsyntax.TwigLiteralName) {
		if node.Text() != "previous" {
			continue
		}
		resolution, ok := resolver.ResolveName(
			"/project/templates/loop.html.twig",
			root,
			node,
		)
		if !ok {
			continue
		}
		resolved++
		require.Equal(t, TwigLoopContextClass, resolution.Receiver.String())
		require.Len(t, resolution.Members, 1)
		require.Equal(t, "getPrevious", resolution.Members[0].Symbol.Name)
	}
	require.Equal(t, 1, resolved)
}
