package diagnostics

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
)

func TestTwigComponentDiagnosticsMissingAndMixedSyntax(t *testing.T) {
	root := t.TempDir()
	twigIndex, err := twig.NewTwigIndexer(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	componentIndex, err := twigcomponent.NewIndex(
		filepath.Join(root, ".cache"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })
	componentIndex.SetDependencies(nil, nil, twigIndex)
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "templates/components/Alert.html.twig"),
		[]byte(`{% block content %}{% endblock %}`),
	)))

	document := diagnosticsDocument(
		"file:///project/templates/page.html.twig",
		[]byte(`<twig:Alret />
<twig:Alert>
  <twig:block name="contnt">Hi</twig:block>
  {% block content %}Hi{% endblock %}
</twig:Alert>`),
	)
	values, err := NewTwigComponentAnalyzer(
		componentIndex,
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, values, 3)
	require.Equal(t, missingTwigComponentCode, values[0].ID)
	require.Contains(
		t,
		values[0].Payload.(map[string]any)["suggestions"],
		"Alert",
	)
	require.Equal(t, missingComponentBlockCode, values[1].ID)
	require.Contains(
		t,
		values[1].Payload.(map[string]any)["suggestions"],
		"content",
	)
	require.Equal(t, mixedComponentSyntaxCode, values[2].ID)
	require.Equal(
		t, problemRangeText(document, values[2].Range),
		"content",
	)
}

func TestTwigComponentDiagnosticsRejectSelfMacroImports(t *testing.T) {
	root := t.TempDir()
	twigIndex, err := twig.NewTwigIndexer(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	componentIndex, err := twigcomponent.NewIndex(
		filepath.Join(root, ".cache"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })
	componentIndex.SetDependencies(nil, nil, twigIndex)
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "templates/components/Alert.html.twig"),
		[]byte(`Alert`),
	)))

	document := diagnosticsDocument(
		"file:///project/templates/page.html.twig",
		[]byte(`{% from _self import outside %}
<twig:Alert>
  {% from _self import html_component %}
  {% from 'macros.html.twig' import valid %}
</twig:Alert>
{% component 'Alert' %}
  {% from _self import twig_component %}
{% endcomponent %}`),
	)
	values, err := NewTwigComponentAnalyzer(
		componentIndex,
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, values, 2)
	for _, value := range values {
		require.Equal(t, componentSelfImportCode, value.ID)
		require.Equal(t, "_self", problemRangeText(document, value.Range))
		require.Contains(t, value.Message, "full template path")
	}
}

func TestTwigComponentDiagnosticsReportMissingLiveAction(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	componentIndex, err := twigcomponent.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })
	componentIndex.SetDependencies(phpIndex, nil, twigIndex)

	classPath := filepath.Join(root, "src/Twig/Components/Cart.php")
	class := []byte(`<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveAction;
use Symfony\UX\LiveComponent\Attribute\LiveArg;
#[AsLiveComponent]
final class Cart {
    #[LiveAction]
    public function save(#[LiveArg('itemId')] int $id): void {}
}`)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(classPath, class)))
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))
	templatePath := filepath.Join(
		root,
		"templates/components/Cart.html.twig",
	)
	source := []byte(`{{ live_action('save') }}
<button data-live-action-param="debounce(300)|svae">Typo</button>
<button data-live-action-param="save" data-live-itme-id-param="42">Argument typo</button>
<button data-live-action-param="{{ dynamicAction }}">Dynamic</button>`)
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		templatePath,
		source,
	)))
	document := diagnosticsDocument(
		"file://"+templatePath,
		source,
	)
	values, err := NewTwigComponentAnalyzer(
		componentIndex,
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, values, 2)
	require.Equal(t, missingLiveActionCode, values[0].ID)
	require.Equal(t, "svae", problemRangeText(document, values[0].Range))
	require.Contains(
		t,
		values[0].Payload.(map[string]any)["suggestions"],
		"save",
	)
	require.Equal(t, missingLiveArgumentCode, values[1].ID)
	require.Equal(
		t,
		"itme-id",
		problemRangeText(document, values[1].Range),
	)
	require.Contains(
		t,
		values[1].Payload.(map[string]any)["suggestions"],
		"item-id",
	)
}
