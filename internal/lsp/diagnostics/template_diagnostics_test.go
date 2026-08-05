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
	require.Len(t, result, 2)
	assert.Equal(
		t,
		"attribute-missing.html.twig",
		problemRangeText(document, result[0].Range),
	)
	assert.Equal(
		t,
		"annotation-missing.html.twig",
		problemRangeText(document, result[1].Range),
	)
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
	require.Len(t, result, 2)
	assert.Equal(t, "Template", problemRangeText(document, result[0].Range))
	assert.Equal(
		t,
		"admin/product/list.html.twig",
		result[0].Payload.(map[string]any)["templateName"],
	)
	assert.Equal(t, "Template", problemRangeText(document, result[1].Range))
	assert.Equal(
		t,
		"admin/product/edit.html.twig",
		result[1].Payload.(map[string]any)["templateName"],
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
