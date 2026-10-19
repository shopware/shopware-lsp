package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateDiagnosticsForTwig(t *testing.T) {
	provider := templateDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		`{% extends 'base.html.twig' %}
{% use '@MyBundle/card.html.twig' %}
{% include 'missing.html.twig' %}
{{ source('also-missing.html.twig') }}`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 2)
	for _, diagnostic := range result {
		assert.Equal(t, missingTemplateCode, diagnostic.ID)
	}
}

func TestTemplateDiagnosticsForShopwareBundleNamespaces(t *testing.T) {
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	path := "/project/vendor/store.shopware.com/swagcmsextensions/src/Resources/views/storefront/card.html.twig"
	require.NoError(t, index.Index(indexer.NewParsedFile(path, []byte("card"))))
	// An application template outside the bundle hierarchy must not satisfy a
	// Shopware include just because its relative name matches.
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/templates/app-only.html.twig", []byte("app"),
	)))
	provider := NewTemplateAnalyzer(index, nil)
	for _, test := range []struct {
		source  string
		missing bool
	}{
		{`{% sw_include "@SwagCmsExtensions/storefront/card.html.twig" %}`, false},
		{`{% sw_extends "@SwagCmsExtensions/storefront/card.html.twig" %}`, false},
		{`{% sw_extends {template: "@SwagCmsExtensions/storefront/card.html.twig", scopes: ['default']} %}`, false},
		{`{% sw_include "@UnknownBundle/storefront/card.html.twig" %}`, false},
		{`{% sw_include "storefront/card.html.twig" %}`, false},
		{`{% sw_include "@SwagCmsExtensions/" ~ "storefront/card.html.twig" %}`, false},
		{`{% sw_include ["missing.html.twig", "@SwagCmsExtensions/storefront/card.html.twig"] %}`, false},
		{`{% sw_include "@SwagCmsExtensions/storefront/missing.html.twig" %}`, true},
		{`{% sw_include "@SwagCmsExtensions/app-only.html.twig" %}`, true},
		{`{% sw_include "app-only.html.twig" %}`, false},
		{`{% include "@SwagCmsExtensions/storefront/card.html.twig" %}`, true},
		{`{% extends "@SwagCmsExtensions/storefront/card.html.twig" %}`, true},
		{`{{ include("@SwagCmsExtensions/storefront/card.html.twig") }}`, true},
		{`{% sw_include include("@SwagCmsExtensions/storefront/card.html.twig") %}`, true},
		{`{% include "@swagcmsextensions/storefront/card.html.twig" %}`, false},
	} {
		t.Run(test.source, func(t *testing.T) {
			document := lsp.NewTextDocument("file:///project/page.html.twig", test.source, 1)
			problems, analyzeErr := provider.Analyze(context.Background(), document)
			require.NoError(t, analyzeErr)
			if test.missing {
				require.Len(t, problems, 1)
				assert.Equal(t, missingTemplateCode, problems[0].ID)
				// Diagnostics still report the spelling and range in the document.
				assert.Equal(t, problemRangeText(document, problems[0].Range),
					problems[0].Payload.(map[string]any)["templateName"])
			} else {
				assert.Empty(t, problems)
			}
		})
	}

	require.NoError(t, index.RemovedFiles([]string{path}))
	problems, err := provider.Analyze(context.Background(), lsp.NewTextDocument(
		"file:///project/page.html.twig",
		`{% sw_include "@SwagCmsExtensions/storefront/card.html.twig" %}`, 2,
	))
	require.NoError(t, err)
	require.Len(t, problems, 1)
}

func TestTemplateDiagnosticsResolveStorefrontDistTemplates(t *testing.T) {
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	dist := "/project/src/Storefront/Resources/app/storefront/dist"
	require.NoError(t, index.Index(indexer.NewParsedFile(
		dist+"/assets/icon/solid/arrow-medium-right.svg",
		[]byte("<svg></svg>"),
	)))
	require.NoError(t, index.Index(indexer.NewParsedFile(
		dist+"/storefront/fragment.html.twig",
		[]byte("fragment"),
	)))
	// A Twig-syntax file outside every loader root must not become a
	// suggestion carrying its absolute path.
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/Users/example/project/custom/apps/MyApp/Resources/scripts/ping/ping.twig",
		[]byte("ping"),
	)))
	provider := NewTemplateAnalyzer(index, nil)
	document := lsp.NewTextDocument(
		"file:///project/src/Storefront/Resources/views/storefront/layout/breadcrumb.html.twig",
		`{% set icon = source('@Storefront/assets/icon/solid/arrow-medium-right.svg') %}
{% include '@Storefront/storefront/fragment.html.twig' %}
{{ source('@Storefront/assets/icon/missing.svg') }}
{% include 'ping.twig' %}`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(
		t,
		"Template '@Storefront/assets/icon/missing.svg' not found",
		result[0].Message,
	)
	assert.Equal(t, "Template 'ping.twig' not found", result[1].Message)
	for _, suggestion := range problemSuggestionStrings(result[1]) {
		assert.NotContains(t, suggestion, "/Users/")
		assert.NotContains(t, suggestion, "Resources/scripts")
	}
}

func TestTemplateDiagnosticsForTwigTemplateExpressions(t *testing.T) {
	provider := templateDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		`{% include '@Storefront/storefront/section/cms-section-' ~ section.type ~ '.html.twig' %}
{% include 'ignored-by-tag.html.twig' ignore missing %}
{{ include('ignored-by-function.html.twig', ignore_missing: true) }}
{{ source('ignored-source.html.twig', true) }}
{% include ['missing-fallback.html.twig', 'base.html.twig'] %}
{% include ['missing-one.html.twig', 'missing-two.html.twig'] %}
{% include 'base' ~ '.html.twig' %}
{% include 'missing/' ~ 'card.html.twig' %}
{% form_theme form with ['base.html.twig', 'missing-theme.html.twig'] %}`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 3)

	assert.Equal(
		t,
		"None of the fallback templates were found: 'missing-one.html.twig', 'missing-two.html.twig'",
		result[0].Message,
	)
	assert.Equal(t, []string{
		"missing-one.html.twig",
		"missing-two.html.twig",
	}, result[0].Payload.(map[string]any)["templateNames"])
	assert.Equal(
		t,
		"Template 'missing/card.html.twig' not found",
		result[1].Message,
	)
	assert.Equal(
		t,
		"'missing/' ~ 'card.html.twig'",
		problemRangeText(document, result[1].Range),
	)
	assert.Equal(
		t,
		"Template 'missing-theme.html.twig' not found",
		result[2].Message,
	)
}

func TestTemplateDiagnosticsForTypedPHPRender(t *testing.T) {
	provider := templateDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/Controller.php",
		`<?php
namespace App;
class Controller extends \Symfony\Bundle\FrameworkBundle\Controller\AbstractController {
    public function page(): void {
        $this->render('base.html.twig');
        $this->render('missing.html.twig');
    }
}`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "Template 'missing.html.twig' not found", result[0].Message)
}

func TestTemplateDiagnosticsIncludeTypoSuggestions(t *testing.T) {
	provider := templateDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		`{% extends 'bsae.html.twig' %}`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Contains(t, problemSuggestionStrings(result[0]), "base.html.twig")
	assert.Equal(
		t,
		"bsae.html.twig",
		problemRangeText(document, result[0].Range),
	)
}

func TestTemplateDiagnosticsValidatePHPDocAssistantReferences(t *testing.T) {
	provider := templateDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/src/Usage.php",
		`<?php
resolve_template('bsae.html.twig');
resolve_template('base.html.twig');
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, missingTemplateCode, result[0].ID)
	assert.Equal(
		t,
		"bsae.html.twig",
		problemRangeText(document, result[0].Range),
	)
	assert.Contains(
		t,
		problemSuggestionStrings(result[0]),
		"base.html.twig",
	)
}

func TestTemplateDiagnosticsForAttributeAndAnnotationDeclarations(
	t *testing.T,
) {
	provider := templateDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/src/ArticleController.php",
		`<?php
namespace App\Controller;
use Symfony\Bridge\Twig\Attribute\Template;
class ArticleController {
    #[Template('attribute-missing.html.twig')]
    public function attribute(): array { return []; }

    /** @Template(template="annotation-missing.html.twig") */
    public function annotation(): array { return []; }
}`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(
		t,
		"attribute-missing.html.twig",
		problemRangeText(document, result[0].Range),
	)
}

func TestTemplateDiagnosticsIgnoreGenericTemplateTags(t *testing.T) {
	provider := templateDiagnosticsFixture(t)
	document := lsp.NewTextDocument("file:///project/src/Collection.php", `<?php
/** @template TElement */
class Collection {
    /**
     * @template T
     * @param callable(TElement): T $callback
     * @return array<T>
     */
    public function map(callable $callback): array { return []; }

    /** @template-covariant T */
    public function values(): array { return []; }
}`, 1)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestTemplateDiagnosticsGuessEmptyControllerDeclarations(
	t *testing.T,
) {
	provider := templateDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/src/Admin/ProductController.php",
		`<?php
namespace App\Controller\Admin;
use Symfony\Bridge\Twig\Attribute\Template;
class ProductController {
    #[Template]
    public function listAction(): array { return []; }

    /** @Template() */
    public function editAction(): array { return []; }

    /** @Template */
    public function existingAction(): array { return []; }

    #[Template(name: 'not-a-template')]
    public function ignored(): array { return []; }

    public function bodyString(): string { return '@Template'; }
}`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "Template", problemRangeText(document, result[0].Range))
	assert.Equal(
		t,
		"admin/product/list.html.twig",
		result[0].Payload.(map[string]any)["templateName"],
	)
}

func templateDiagnosticsFixture(t *testing.T) *TemplateAnalyzer {
	t.Helper()
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		"/project/templates/base.html.twig",
		[]byte("base"),
	)))
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		"/project/MyBundle/src/Resources/views/card.html.twig",
		[]byte("card"),
	)))
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		"/project/templates/admin/product/existing.html.twig",
		[]byte("existing"),
	)))

	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/TemplateAssistant.php",
		[]byte(`<?php
/** @param string $template #Template */
function resolve_template(string $template): void {}
`),
	)))
	return NewTemplateAnalyzer(twigIndex, phpIndex)
}
