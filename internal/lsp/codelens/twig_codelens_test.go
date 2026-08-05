package codelens

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/twig"
)

func TestTwigCodeLensLinksReferencesInheritanceAndBlocks(t *testing.T) {
	root := t.TempDir()
	basePath := filepath.Join(root, "templates", "base.html.twig")
	childPath := filepath.Join(root, "templates", "child.html.twig")
	grandPath := filepath.Join(root, "templates", "grand.html.twig")
	pagePath := filepath.Join(root, "templates", "page.html.twig")
	baseSource := `{% block title %}Base{% endblock %}
{% block body %}Body{% endblock %}
`
	childSource := `{% extends 'base.html.twig' %}
{% block title %}Child{% endblock %}
`
	grandSource := `{% extends 'child.html.twig' %}
{% block title %}Grand{% endblock %}
{% block body %}Grand body{% endblock %}
`
	pageSource := `{% include 'base.html.twig' %}
`
	sources := map[string]string{
		basePath:  baseSource,
		childPath: childSource,
		grandPath: grandSource,
		pagePath:  pageSource,
	}
	for path, source := range sources {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}

	twigIndex, err := twig.NewTwigIndexer(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	for path, source := range sources {
		require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	provider := NewTwigCodeLensProvider(twigIndex)

	baseLenses := relatedCodeLensesFor(
		t,
		provider,
		basePath,
		baseSource,
	)
	assert.Equal(t, []string{
		relatedTarget(childPath, 1),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, baseLenses, "Open extending template"),
	))
	assert.Equal(t, []string{
		relatedTarget(pagePath, 1),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, baseLenses, "Open template reference"),
	))
	titleImplementations := relatedLensByTitle(
		t,
		baseLenses,
		"Open 2 block implementations",
	)
	assert.Equal(t, 0, titleImplementations.Range.Start.Line)
	assert.Equal(t, []string{
		relatedTarget(childPath, 2),
		relatedTarget(grandPath, 2),
	}, relatedLensTargets(t, titleImplementations))
	bodyImplementation := relatedLensByTitle(
		t,
		baseLenses,
		"Open block implementation",
	)
	assert.Equal(t, 1, bodyImplementation.Range.Start.Line)
	assert.Equal(t, []string{
		relatedTarget(grandPath, 3),
	}, relatedLensTargets(t, bodyImplementation))

	childLenses := relatedCodeLensesFor(
		t,
		provider,
		childPath,
		childSource,
	)
	assert.Equal(t, []string{
		relatedTarget(grandPath, 1),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, childLenses, "Open extending template"),
	))
	assert.Equal(t, []string{
		relatedTarget(basePath, 1),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, childLenses, "Open parent block"),
	))
	assert.Equal(t, []string{
		relatedTarget(grandPath, 2),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, childLenses, "Open block implementation"),
	))

	grandLenses := relatedCodeLensesFor(
		t,
		provider,
		grandPath,
		grandSource,
	)
	assert.Equal(t, []string{
		relatedTarget(basePath, 1),
		relatedTarget(childPath, 2),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, grandLenses, "Open 2 parent blocks"),
	))
	assert.Equal(t, []string{
		relatedTarget(basePath, 2),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, grandLenses, "Open parent block"),
	))

	unsavedGrandSource := `{% extends 'base.html.twig' %}
{% block title %}Grand{% endblock %}
`
	unsavedGrandLenses := relatedCodeLensesFor(
		t,
		provider,
		grandPath,
		unsavedGrandSource,
	)
	assert.Equal(t, []string{
		relatedTarget(basePath, 1),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(
			t,
			unsavedGrandLenses,
			"Open parent block",
		),
	))
}

func TestTwigCodeLensLinksSameNameTemplateOverrides(t *testing.T) {
	root := t.TempDir()
	firstPath := filepath.Join(
		root,
		"app",
		"templates",
		"card.html.twig",
	)
	secondPath := filepath.Join(
		root,
		"plugin",
		"templates",
		"card.html.twig",
	)
	for _, path := range []string{firstPath, secondPath} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("card"), 0o644))
	}
	twigIndex, err := twig.NewTwigIndexer(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	for _, path := range []string{firstPath, secondPath} {
		require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
			path,
			[]byte("card"),
		)))
	}

	lenses := relatedCodeLensesFor(
		t,
		NewTwigCodeLensProvider(twigIndex),
		firstPath,
		"card",
	)
	require.Len(t, lenses, 1)
	assert.Equal(t, "Open template override", lenses[0].Command.Title)
	assert.Equal(t, []string{
		relatedTarget(secondPath, 1),
	}, relatedLensTargets(t, lenses[0]))
}
