package symfony

import (
	"strings"
	"testing"

	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigHTMLRouteReferencesAndPathMatching(t *testing.T) {
	source := `<a href="/catalog/42?preview=1">Product</a>
<form action=""></form>
<a href="{{ path('catalog.show') }}">Dynamic</a>
<a href="https://example.test/catalog/42?preview=1#details">Absolute</a>`
	root := twigparser.Parse(source).Tree.Root
	htmlStrings := twigquery.Nodes(root, twigsyntax.HtmlString)
	require.Len(t, htmlStrings, 4)

	link, found := TwigHTMLRouteReferenceAt(htmlStrings[0])
	require.True(t, found)
	assert.Equal(t, "/catalog/42?preview=1", link.Value)
	assert.Equal(t, "/catalog/42", RouteURLPath(link.Value))
	form, found := TwigHTMLRouteReferenceAt(htmlStrings[1])
	require.True(t, found)
	assert.Empty(t, form.Value)
	_, found = TwigHTMLRouteReferenceAt(htmlStrings[2])
	assert.False(t, found)
	external, found := TwigHTMLRouteReferenceAt(htmlStrings[3])
	require.True(t, found)
	assert.Equal(t, "/catalog/42", RouteURLPath(external.Value))

	assert.True(t, RoutePathMatches("/catalog/{id}", "/catalog/42"))
	assert.False(t, RoutePathMatches("/catalog/{id}", "/catalog/42/review"))
	references := TwigHTMLRouteReferences(root)
	require.Len(t, references, 2)
	assert.Equal(t, "/catalog/42?preview=1", references[0].Value)
	assert.Equal(
		t,
		"https://example.test/catalog/42?preview=1#details",
		references[1].Value,
	)
}

func TestNormalizeRouteSearchPath(t *testing.T) {
	for _, test := range []struct {
		value    string
		expected string
	}{
		{value: "", expected: ""},
		{value: "   ", expected: ""},
		{value: "https://www.de.test:8664", expected: "/"},
		{value: "//www.de.test:8664", expected: "/"},
		{value: "https://www.de.test:8664/foo", expected: "/foo"},
		{
			value:    "https://www.de.test:8664/foo?utm=1#details",
			expected: "/foo",
		},
		{
			value:    "https://www.de.test:8664/edit/{id}?utm=1",
			expected: "/edit/{id}",
		},
		{
			value:    "//www.de.test:8664/foo#details",
			expected: "/foo",
		},
		{value: "/foo?utm=1", expected: "/foo"},
		{value: "foo/bar", expected: "foo/bar"},
		{value: "foo:bar", expected: "foo:bar"},
		{
			value:    "/edit/foo%2Ebar~v1?preview=1",
			expected: "/edit/foo%2Ebar~v1",
		},
	} {
		t.Run(test.value, func(t *testing.T) {
			assert.Equal(
				t,
				test.expected,
				NormalizeRouteSearchPath(test.value),
			)
		})
	}
}

func TestRoutePathSearchMatchesPartialAndAbsoluteURLs(t *testing.T) {
	for _, test := range []struct {
		name       string
		routePath  string
		searchPath string
		expected   bool
	}{
		{
			name: "concrete placeholder", routePath: "/edit/{id}",
			searchPath: "/edit/12", expected: true,
		},
		{
			name: "direct route pattern", routePath: "/edit/{id}",
			searchPath: "/edit/{id}", expected: true,
		},
		{
			name: "direct static route", routePath: "/class-like-route",
			searchPath: "/class-like-route", expected: true,
		},
		{
			name: "absolute static URL", routePath: "/class-like-route",
			searchPath: "https://www.de.test:8664/class-like-route?utm=1#details",
			expected:   true,
		},
		{
			name: "absolute concrete URL", routePath: "/edit/{id}",
			searchPath: "https://www.de.test:8664/edit/12?utm=1#details",
			expected:   true,
		},
		{
			name: "dotted placeholder", routePath: "/edit/{id}",
			searchPath: "/edit/foo.bar", expected: true,
		},
		{
			name: "encoded placeholder", routePath: "/edit/{id}",
			searchPath: "/edit/foo%2Ebar~v1", expected: true,
		},
		{
			name: "placeholder cannot consume slash", routePath: "/edit/{id}",
			searchPath: "/edit/foo/bar", expected: false,
		},
		{
			name: "absolute route pattern", routePath: "/car/{edit}/foobar",
			searchPath: "https://www.de.test:8664/car/{edit}/foobar?utm=1",
			expected:   true,
		},
		{
			name: "absolute partial reverse match", routePath: "/car/{edit}/foobar",
			searchPath: "https://www.de.test:8664/car/12/foo?utm=1",
			expected:   true,
		},
		{
			name: "middle partial reverse match", routePath: "/car/{edit}/foobar",
			searchPath: "ar/12/foo", expected: true,
		},
		{
			name: "short placeholder fragment", routePath: "/edit/{id}",
			searchPath: "12", expected: false,
		},
		{
			name: "rooted partial route", routePath: "/edit/{id}",
			searchPath: "/edit/", expected: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(
				t,
				test.expected,
				RoutePathSearchMatches(test.routePath, test.searchPath),
			)
		})
	}
}

func TestRoutePathMatchesRemainExact(t *testing.T) {
	assert.True(
		t,
		RoutePathMatches(
			"/edit/{id}",
			"https://www.de.test/edit/foo.bar?preview=1#details",
		),
	)
	assert.True(
		t,
		RoutePathMatches("/edit/{id}", "/edit/foo%2Ebar~v1"),
	)
	assert.False(t, RoutePathMatches("/edit/{id}", "/edit/foo/bar"))
	assert.False(t, RoutePathMatches("/edit/{id}", "/edit/"))
}

func TestJSRouteURLReferenceContexts(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		found  bool
	}{
		{
			name:   "fetch",
			source: `fetch('/edit/12')`,
			found:  true,
		},
		{
			name:   "axios",
			source: `axios('/edit/12')`,
			found:  true,
		},
		{
			name:   "axios create URL",
			source: `axios.create('/edit/12')`,
			found:  true,
		},
		{
			name:   "request constructor",
			source: `new Request('/edit/12')`,
			found:  true,
		},
		{
			name:   "URL property",
			source: `const options = { url: '/edit/12' }`,
			found:  true,
		},
		{
			name:   "Axios base URL",
			source: `axios.create({ baseURL: '/edit/12' })`,
			found:  true,
		},
		{
			name:   "unrelated call",
			source: `logger.info('/edit/12')`,
			found:  false,
		},
		{
			name:   "unrelated base URL",
			source: `const options = { baseURL: '/edit/12' }`,
			found:  false,
		},
		{
			name:   "nested fetch value",
			source: `fetch({ path: '/edit/12' })`,
			found:  false,
		},
		{
			name:   "dynamic template",
			source: "fetch(`/edit/${id}`)",
			found:  false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := javascriptparser.Parse(test.source).Tree.Root
			offset := uint32(strings.Index(test.source, "/edit/12") + 3)
			if strings.Contains(test.source, "${id}") {
				offset = uint32(strings.Index(test.source, "/edit/") + 3)
			}
			node := root.NodeAtOffset(offset)
			reference, found := JSRouteURLReferenceAt(node)
			assert.Equal(t, test.found, found)
			if !found {
				return
			}
			assert.Equal(t, "/edit/12", reference.Value)
			assert.Equal(
				t,
				"/edit/12",
				test.source[reference.Range.Start:reference.Range.End],
			)
		})
	}
}

func TestPHPRoutePathReferenceUsesOnlyAttributePathArguments(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		found  bool
	}{
		{
			name:   "positional",
			source: `<?php #[Route('/catalog/products/detail')] function show() {}`,
			found:  true,
		},
		{
			name:   "named",
			source: `<?php #[Route(name: 'catalog', path: '/catalog/products/detail')] function show() {}`,
			found:  true,
		},
		{
			name:   "name",
			source: `<?php #[Route(name: '/catalog/products/detail')] function show() {}`,
			found:  false,
		},
		{
			name:   "methods",
			source: `<?php #[Route('/catalog', methods: ['/catalog/products/detail'])] function show() {}`,
			found:  false,
		},
		{
			name:   "unrelated attribute",
			source: `<?php #[Cache('/catalog/products/detail')] function show() {}`,
			found:  false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tree := phpparser.Parse(test.source).Tree
			offset := uint32(strings.LastIndex(
				test.source,
				"/catalog/products/detail",
			) + len("/catalog/prod"))
			node := tree.Root.NodeAtOffset(offset)
			reference, found := PHPRoutePathReferenceAt(node)
			assert.Equal(t, test.found, found)
			if !found {
				return
			}
			assert.Equal(t, "/catalog/products/detail", reference.Value)
			prefix, ok := reference.PrefixAt(offset)
			require.True(t, ok)
			assert.Equal(t, "/catalog/products", prefix)
		})
	}
}

func TestPHPRouteAnnotationPathReferenceUsesOnlyPathArguments(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		found  bool
		value  string
	}{
		{
			name: "default",
			source: `<?php
/** @Route("/catalog/<caret>products") */
function show() {}
`,
			found: true,
			value: "/catalog/products",
		},
		{
			name: "named path",
			source: `<?php
/** @Route(name="catalog", path="/catalog/<caret>products") */
function show() {}
`,
			found: true,
			value: "/catalog/products",
		},
		{
			name: "annotation namespace alias",
			source: `<?php
/** @Routing\Route(path="/catalog/<caret>products") */
function show() {}
`,
			found: true,
			value: "/catalog/products",
		},
		{
			name: "name",
			source: `<?php
/** @Route(name="/catalog/<caret>products") */
function show() {}
`,
		},
		{
			name: "methods",
			source: `<?php
/** @Route("/catalog", methods={"/catalog/<caret>products"}) */
function show() {}
`,
		},
		{
			name: "second positional argument",
			source: `<?php
/** @Route("/catalog", "/catalog/<caret>products") */
function show() {}
`,
		},
		{
			name: "unrelated annotation",
			source: `<?php
/** @Cache("/catalog/<caret>products") */
function show() {}
`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			offset := strings.Index(test.source, "<caret>")
			require.NotEqual(t, -1, offset)
			source := strings.Replace(test.source, "<caret>", "", 1)
			reference, found := PHPRouteAnnotationPathReferenceAt(
				source,
				uint32(offset),
			)
			assert.Equal(t, test.found, found)
			if !found {
				return
			}
			assert.Equal(t, test.value, reference.Value)
			assert.Equal(
				t,
				test.value,
				source[reference.Range.Start:reference.Range.End],
			)
		})
	}
}

func TestPHPRouteNameReferencesUseOnlyNamedNameArguments(t *testing.T) {
	for _, test := range []struct {
		name       string
		source     string
		annotation bool
		found      bool
	}{
		{
			name:   "attribute name",
			source: `<?php #[Route(path: '/catalog', name: 'draft<caret>')] function show() {}`,
			found:  true,
		},
		{
			name:   "attribute path",
			source: `<?php #[Route(path: '/catalog<caret>', name: 'draft')] function show() {}`,
		},
		{
			name: "annotation name",
			source: `<?php
/** @Route(path="/catalog", name="draft<caret>") */
function show() {}
`,
			annotation: true,
			found:      true,
		},
		{
			name: "annotation default",
			source: `<?php
/** @Route("/catalog<caret>") */
function show() {}
`,
			annotation: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			offset := strings.Index(test.source, "<caret>")
			require.NotEqual(t, -1, offset)
			source := strings.Replace(test.source, "<caret>", "", 1)
			var (
				reference PHPRoutePathReference
				found     bool
			)
			if test.annotation {
				reference, found = PHPRouteAnnotationNameReferenceAt(
					source,
					uint32(offset),
				)
			} else {
				tree := phpparser.Parse(source).Tree
				node := tree.Root.NodeAtOffset(uint32(offset))
				reference, found = PHPRouteNameReferenceAt(node)
			}
			assert.Equal(t, test.found, found)
			if found {
				assert.Equal(t, "draft", reference.Value)
			}
		})
	}
}

func TestPHPRouteNameSuggestionMirrorsControllerConvention(t *testing.T) {
	for _, test := range []struct {
		name     string
		source   string
		marker   string
		expected string
	}{
		{
			name: "app controller",
			source: `<?php
namespace App\Controller;
class FooController {
    #[Route(name: 'draft')]
    public function foo1(): void {}
}`,
			marker:   "draft",
			expected: "app_foo_foo1",
		},
		{
			name: "bundle and nested controllers",
			source: `<?php
namespace Foo\ParkResortBundle\Controller\SubController\BundleController;
class FooController {
    public function nestedFooAction(): void {}
}`,
			marker:   "nestedFooAction",
			expected: "foo_parkresort_sub_bundle_foo_nestedfoo",
		},
		{
			name: "leading underscore method",
			source: `<?php
namespace App\Controller;
class FooController {
    public function _fragmentAction(): void {}
}`,
			marker:   "_fragmentAction",
			expected: "app_foo_fragment",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tree := phpparser.Parse(test.source).Tree
			offset := uint32(strings.Index(test.source, test.marker))
			node := tree.Root.NodeAtOffset(offset)
			assert.Equal(
				t,
				test.expected,
				PHPRouteNameSuggestion(tree.Root, node, offset),
			)
		})
	}
}

func TestPHPRoutePathPrefixRequiresIntermediateSegment(t *testing.T) {
	source := `<?php #[Route('/catalog/products/detail')] function show() {}`
	tree := phpparser.Parse(source).Tree
	pathStart := strings.Index(source, "/catalog/products/detail")
	node := tree.Root.NodeAtOffset(uint32(pathStart + 2))
	reference, found := PHPRoutePathReferenceAt(node)
	require.True(t, found)

	prefix, ok := reference.PrefixAt(uint32(pathStart + len("/catalog/prod")))
	require.True(t, ok)
	assert.Equal(t, "/catalog/products", prefix)
	_, ok = reference.PrefixAt(uint32(pathStart + len("/catalog/products/det")))
	assert.False(t, ok)
}

func TestTwigRouteParameterReferenceSupportsIdentifierHashKeys(t *testing.T) {
	tests := []struct {
		source    string
		marker    string
		route     string
		parameter string
		found     bool
	}{
		{
			source: `{{ path('product.show', {'slug': product.slug}) }}`,
			marker: "slug':", route: "product.show", parameter: "slug",
			found: true,
		},
		{
			source: `{{ path('product.show', {slug: product.slug}) }}`,
			marker: "slug:", route: "product.show", parameter: "slug",
			found: true,
		},
		{
			source: `{{ path('product.show', {slug}) }}`,
			marker: "slug}", route: "product.show", parameter: "slug",
			found: true,
		},
		{
			source: `{{ path('product.show', {slug: product.value}) }}`,
			marker: "value", found: false,
		},
		{
			source: `{{ include('product.show', {slug: product.slug}) }}`,
			marker: "slug:", found: false,
		},
	}
	for _, test := range tests {
		t.Run(test.source+"/"+test.marker, func(t *testing.T) {
			root := twigparser.Parse(test.source).Tree.Root
			offset := strings.Index(test.source, test.marker)
			require.NotEqual(t, -1, offset)
			node := root.NodeAtOffset(uint32(offset + 1))
			require.NotNil(t, node)
			route, parameter, found :=
				TwigRouteParameterReferenceAt(node)
			assert.Equal(t, test.found, found)
			assert.Equal(t, test.route, route)
			assert.Equal(t, test.parameter, parameter)
		})
	}
}

func TestTwigRouteComparisonReferences(t *testing.T) {
	source := `{% if app.request.attributes.get('_route') == 'route.equal' %}{% endif %}
{% if 'route.reverse' !== app.request.attributes.get("_route") %}{% endif %}
{% if app.request.attributes.get('_route') is same as('route.same') %}{% endif %}
{% if app.request.attributes.get('_route') is not same as('route.not_same') %}{% endif %}
{% if app.request.attributes.get('_route') in ['route.in_a', 'route.in_b'] %}{% endif %}
{% if app.request.attributes.get('_route') not in ['route.not_in'] %}{% endif %}`
	root := twigparser.Parse(source).Tree.Root
	references := TwigRouteComparisonReferences(root)
	var names []string
	for _, reference := range references {
		names = append(names, reference.Value)
		assert.Equal(
			t,
			reference.Value,
			source[reference.Range.Start:reference.Range.End],
		)
		assert.True(t, IsTwigRouteName(reference.Node))
	}
	assert.ElementsMatch(t, []string{
		"route.equal",
		"route.reverse",
		"route.same",
		"route.not_same",
		"route.in_a",
		"route.in_b",
		"route.not_in",
	}, names)
}

func TestTwigRouteComparisonRejectsPartialAndUnrelatedStrings(t *testing.T) {
	for _, source := range []string{
		`{% if app.request.attributes.get('_route') starts with 'product.' %}{% endif %}`,
		`{% if app.request.query.get('_route') == 'product.show' %}{% endif %}`,
		`{% if app.request.attributes.get('_controller') == 'product.show' %}{% endif %}`,
		`{% if route == 'product.show' %}{% endif %}`,
		`{% if app.request.attributes.get('_route') == "product.#{kind}" %}{% endif %}`,
		`{% if app.request.attributes.get('_route') in [['product.show']] %}{% endif %}`,
	} {
		root := twigparser.Parse(source).Tree.Root
		assert.Empty(t, TwigRouteComparisonReferences(root), source)
		for _, literal := range twigquery.Nodes(
			root,
			twigsyntax.TwigLiteralString,
		) {
			if twigquery.StringValue(literal) == "product.show" {
				assert.False(t, IsTwigRouteName(literal), source)
			}
		}
	}
}
