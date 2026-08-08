package twig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersioningServiceFollowsExtendsChain(t *testing.T) {
	root := t.TempDir()
	idx, err := NewTwigIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	corePath := filepath.Join(
		root, "src", "Storefront", "Resources", "views",
		"storefront", "page", "base.html.twig",
	)
	themePath := filepath.Join(
		root, "custom", "plugins", "Theme", "src", "Resources", "views",
		"storefront", "theme", "base.html.twig",
	)
	pluginPath := filepath.Join(
		root, "custom", "plugins", "Plugin", "src", "Resources", "views",
		"storefront", "custom", "page.html.twig",
	)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		corePath,
		[]byte(`{% block page_content %}core{% endblock %}`),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		themePath,
		[]byte(`{% sw_extends '@Storefront/storefront/page/base.html.twig' %}
{# shopware-block: deadbeef@1.0.0 #}
{% block page_content %}theme{% endblock %}`),
	)))
	plugin, err := ParseTwig(pluginPath, []byte(`{% sw_extends '@Theme/storefront/theme/base.html.twig' %}
{% block page_content %}plugin{% endblock %}`))
	require.NoError(t, err)

	resolution, err := NewVersioningService(root, idx, "6.7.0").Resolve(
		*plugin,
		"page_content",
	)
	require.NoError(t, err)
	require.True(t, resolution.ParentResolved)
	require.Len(t, resolution.Candidates, 2)
	assert.Equal(t, themePath, resolution.Candidates[0].AbsolutePath)
	assert.Equal(t, corePath, resolution.Candidates[1].AbsolutePath)
	assert.True(t, resolution.Candidates[0].HasVersioningComment)
}

func TestVersioningServiceResolvesAllBlocksAgainstOneChain(t *testing.T) {
	root := t.TempDir()
	idx, err := NewTwigIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	corePath := filepath.Join(
		root, "src", "Storefront", "Resources", "views",
		"storefront", "page", "base.html.twig",
	)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		corePath,
		[]byte(`{% block first %}one{% endblock %}
{% block second %}two{% endblock %}`),
	)))
	plugin, err := ParseTwig(
		filepath.Join(root, "custom", "plugin.html.twig"),
		[]byte(`{% sw_extends '@Storefront/storefront/page/base.html.twig' %}
{% block first %}local one{% endblock %}
{% block second %}local two{% endblock %}`),
	)
	require.NoError(t, err)

	resolutions, err := NewVersioningService(root, idx, "").ResolveBlocks(*plugin)
	require.NoError(t, err)
	require.Len(t, resolutions, 2)
	for _, name := range []string{"first", "second"} {
		require.Len(t, resolutions[name].Candidates, 1)
		assert.Equal(t, corePath, resolutions[name].Candidates[0].AbsolutePath)
		assert.True(t, resolutions[name].ParentResolved)
	}
}

func TestVersioningServiceFallbackIgnoresTrackedSiblingOverrides(t *testing.T) {
	root := t.TempDir()
	idx, err := NewTwigIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	corePath := filepath.Join(
		root, "src", "Storefront", "Resources", "views",
		"storefront", "page", "base.html.twig",
	)
	siblingPath := filepath.Join(
		root, "custom", "plugins", "A", "src", "Resources", "views",
		"storefront", "page", "base.html.twig",
	)
	currentPath := filepath.Join(
		root, "custom", "plugins", "B", "src", "Resources", "views",
		"storefront", "page", "base.html.twig",
	)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		corePath,
		[]byte(`{% block page_content %}core{% endblock %}`),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		siblingPath,
		[]byte(`{# shopware-block: deadbeef@1.0.0 #}
{% block page_content %}sibling{% endblock %}`),
	)))
	current, err := ParseTwig(currentPath, []byte(`{% sw_extends '@Missing/storefront/page/base.html.twig' %}
{% block page_content %}current{% endblock %}`))
	require.NoError(t, err)
	resolution, err := NewVersioningService(root, idx, "").Resolve(*current, "page_content")
	require.NoError(t, err)
	require.Len(t, resolution.Candidates, 1)
	assert.Equal(t, corePath, resolution.Candidates[0].AbsolutePath)
	assert.False(t, resolution.ParentResolved)
}

func TestVersioningServiceDistinguishesRemovedBlockFromMissingCheckout(t *testing.T) {
	root := t.TempDir()
	idx, err := NewTwigIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	corePath := filepath.Join(
		root, "src", "Storefront", "Resources", "views",
		"storefront", "page", "base.html.twig",
	)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		corePath,
		[]byte(`{% block different_block %}core{% endblock %}`),
	)))
	service := NewVersioningService(root, idx, "")
	resolved, err := ParseTwig(
		filepath.Join(root, "custom", "resolved.html.twig"),
		[]byte(`{% sw_extends '@Storefront/storefront/page/base.html.twig' %}
{% block removed_block %}current{% endblock %}`),
	)
	require.NoError(t, err)
	resolution, err := service.Resolve(*resolved, "removed_block")
	require.NoError(t, err)
	assert.True(t, resolution.ParentResolved)
	assert.Empty(t, resolution.Candidates)

	standalone, err := ParseTwig(
		filepath.Join(root, "custom", "standalone.html.twig"),
		[]byte(`{% sw_extends '@Missing/storefront/page/base.html.twig' %}
{% block removed_block %}current{% endblock %}`),
	)
	require.NoError(t, err)
	resolution, err = service.Resolve(*standalone, "removed_block")
	require.NoError(t, err)
	assert.False(t, resolution.ParentResolved)
	assert.Empty(t, resolution.Candidates)
}

func TestVersioningServiceBuildsPortableCommentEdits(t *testing.T) {
	root := t.TempDir()
	idx, err := NewTwigIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	upstreamRoot := filepath.Join(root, "custom", "plugins", "Theme")
	require.NoError(t, os.MkdirAll(upstreamRoot, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(upstreamRoot, "composer.json"),
		[]byte(`{"version":"2.5.0"}`),
		0o644,
	))
	upstreamPath := filepath.Join(
		upstreamRoot, "src", "Resources", "views", "storefront", "card.html.twig",
	)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		upstreamPath,
		[]byte(`{% block card %}upstream{% endblock %}`),
	)))
	currentPath := filepath.Join(
		root, "custom", "plugins", "Plugin", "src", "Resources", "views",
		"storefront", "card.html.twig",
	)
	source := "{% sw_extends '@Theme/storefront/card.html.twig' %}\n  {% block card %}local{% endblock %}\n"
	service := NewVersioningService(root, idx, "")
	rng, replacement, err := service.VersionCommentEdit(currentPath, source, "card")
	require.NoError(t, err)
	assert.Equal(t, "  ", source[rng.Start:rng.End]+replacement[:2])
	assert.Contains(t, replacement, "@2.5.0 #}")
	updated := source[:rng.Start] + replacement + source[rng.End:]
	parsed, err := ParseTwig(currentPath, []byte(updated))
	require.NoError(t, err)
	require.NotNil(t, parsed.Blocks["card"].VersionComment)
	assert.Equal(t, "2.5.0", parsed.Blocks["card"].VersionComment.Version)
}
