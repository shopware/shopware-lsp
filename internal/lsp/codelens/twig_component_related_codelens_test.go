package codelens

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
)

func TestTwigComponentRelatedCodeLensLinksTemplateClassUsagesAndBlocks(
	t *testing.T,
) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	classPath := filepath.Join(
		root,
		"src",
		"Twig",
		"Components",
		"Alert.php",
	)
	templatePath := filepath.Join(
		root,
		"templates",
		"components",
		"alert.html.twig",
	)
	usagePath := filepath.Join(
		root,
		"templates",
		"page.html.twig",
	)
	classSource := `<?php
namespace App\Twig\Components;

use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;

#[AsTwigComponent(name: 'Alert', template: 'components/alert.html.twig')]
final class Alert {}
`
	templateSource := `{% block headline %}Default headline{% endblock %}`
	usageSource := `{{ component('Alert') }}
<twig:Alert>
    <twig:block name="headline">Custom headline</twig:block>
</twig:Alert>
`
	for path, source := range map[string]string{
		classPath:    classSource,
		templatePath: templateSource,
		usagePath:    usageSource,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}

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

	for path, source := range map[string]string{
		classPath:    classSource,
		templatePath: templateSource,
		usagePath:    usageSource,
	} {
		file := indexer.NewParsedFile(path, []byte(source))
		require.NoError(t, twigIndex.Index(file))
		require.NoError(t, componentIndex.Index(file))
		if filepath.Ext(path) == ".php" {
			require.NoError(t, phpIndex.Index(file))
		}
	}

	provider := NewTwigComponentRelatedCodeLensProvider(
		componentIndex,
		phpIndex,
	)
	templateLenses := relatedCodeLensesFor(
		t,
		provider,
		templatePath,
		templateSource,
	)
	require.Len(t, templateLenses, 1)
	assert.Equal(t, "Open UX component", templateLenses[0].Command.Title)
	assert.Equal(t, []string{
		relatedTarget(classPath, 7),
		relatedTarget(usagePath, 1),
		relatedTarget(usagePath, 2),
	}, relatedLensTargets(t, templateLenses[0]))

	usageLenses := relatedCodeLensesFor(
		t,
		provider,
		usagePath,
		usageSource,
	)
	require.Len(t, usageLenses, 1)
	assert.Equal(
		t,
		"Open component block",
		usageLenses[0].Command.Title,
	)
	assert.Equal(t, 2, usageLenses[0].Range.Start.Line)
	assert.Equal(t, []string{
		relatedTarget(templatePath, 1),
	}, relatedLensTargets(t, usageLenses[0]))
}

func TestTwigComponentRelatedCodeLensSkipsUnrelatedTwig(
	t *testing.T,
) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	componentIndex, err := twigcomponent.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })

	lenses := relatedCodeLensesFor(
		t,
		NewTwigComponentRelatedCodeLensProvider(componentIndex, nil),
		filepath.Join(root, "templates", "page.html.twig"),
		"<h1>Page</h1>",
	)
	assert.Empty(t, lenses)
}
