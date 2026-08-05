package documentlink

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/symfonyconfig"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestRelatedDocumentLinksResolveUnambiguousTwigTemplates(t *testing.T) {
	root := t.TempDir()
	index, err := twig.NewTwigIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })

	basePath := filepath.Join(root, "templates", "base.html.twig")
	fallbackPath := filepath.Join(root, "templates", "fallback.html.twig")
	for _, path := range []string{basePath, fallbackPath} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("template"), 0o644))
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte("template"),
		)))
	}
	// The same logical name in two namespaces is deliberately ambiguous and
	// remains available through multi-target definition navigation.
	for _, namespace := range []string{"one", "two"} {
		path := filepath.Join(
			root,
			"vendor",
			namespace,
			"templates",
			"shared.html.twig",
		)
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte("shared"),
		)))
	}

	source := `{% extends 'base.html.twig' %}
{% include ['missing.html.twig', 'fallback.html.twig'] %}
{% include 'shared.html.twig' %}`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "templates", "page.html.twig")),
		source,
		1,
	)
	links, err := NewRelatedProvider(index, nil, nil).GetDocumentLinks(
		context.Background(),
		documentLinkRequest(document),
	)
	require.NoError(t, err)
	require.Len(t, links, 2)
	assert.Equal(t, uriutil.FileURI(basePath), links[0].Target)
	assert.Equal(t, uriutil.FileURI(fallbackPath), links[1].Target)
	assert.Equal(t, 0, links[0].Range.Start.Line)
	assert.Equal(t, 1, links[1].Range.Start.Line)
	assert.Contains(t, links[0].Tooltip, `extends template "base.html.twig"`)
}

func TestRelatedDocumentLinksResolveRoutingAndConfigurationResources(
	t *testing.T,
) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config", "packages", "app.yaml")
	sharedPath := filepath.Join(root, "config", "packages", "shared.yaml")
	controllerPath := filepath.Join(root, "src", "Controller.php")
	configurationPath := filepath.Join(
		root,
		"src",
		"DependencyInjection",
		"Configuration.php",
	)
	for _, path := range []string{
		sharedPath,
		controllerPath,
		configurationPath,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("target"), 0o644))
	}
	configuration, err := symfonyconfig.NewIndex(
		filepath.Join(root, "config-cache"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, configuration.Close()) })
	require.NoError(t, configuration.Index(indexer.NewParsedFile(
		configurationPath,
		[]byte(`<?php
use Symfony\Component\Config\Definition\Builder\TreeBuilder;
final class Configuration {
    public function getConfigTreeBuilder(): TreeBuilder {
        return new TreeBuilder('framework');
    }
}
`),
	)))

	source := `imports:
  - resource: shared.yaml
controllers:
  resource: ../../src/Controller.php
  type: attribute
framework: {}`
	document := lsp.NewTextDocument(
		uriutil.FileURI(configPath),
		source,
		1,
	)
	links, err := NewRelatedProvider(
		nil,
		configuration,
		nil,
	).GetDocumentLinks(
		context.Background(),
		documentLinkRequest(document),
	)
	require.NoError(t, err)
	require.Len(t, links, 3)
	assert.Equal(t, []string{
		uriutil.FileURI(sharedPath),
		uriutil.FileURI(controllerPath),
		uriutil.FileURI(configurationPath),
	}, documentLinkTargets(links))
	assert.Equal(t, []int{1, 3, 5}, documentLinkLines(links))
	assert.Equal(
		t,
		`Open "framework" configuration declaration`,
		links[2].Tooltip,
	)
}

func TestRelatedDocumentLinksSkipGlobAndMultipleTargets(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one.yaml", "two.yaml"} {
		path := filepath.Join(root, "config", "routes", name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("route"), 0o644))
	}
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "config", "routes.yaml")),
		`routes:
  resource: routes/*.yaml
  type: yaml`,
		1,
	)

	links, err := NewRelatedProvider(nil, nil, nil).GetDocumentLinks(
		context.Background(),
		documentLinkRequest(document),
	)
	require.NoError(t, err)
	assert.Empty(t, links)
}

func documentLinkRequest(
	document *lsp.TextDocument,
) *lsp.DocumentLinkRequest {
	params := &protocol.DocumentLinkParams{}
	params.TextDocument.URI = document.URI
	return &lsp.DocumentLinkRequest{
		DocumentLinkParams: params,
		Document:           document,
	}
}

func documentLinkTargets(links []protocol.DocumentLink) []string {
	result := make([]string, 0, len(links))
	for _, link := range links {
		result = append(result, link.Target)
	}
	return result
}

func documentLinkLines(links []protocol.DocumentLink) []int {
	result := make([]int, 0, len(links))
	for _, link := range links {
		result = append(result, link.Range.Start.Line)
	}
	return result
}
