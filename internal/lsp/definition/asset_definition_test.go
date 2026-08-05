package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/asset"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssetDefinitionNavigatesPhysicalManifestAndEncoreTargets(t *testing.T) {
	root := t.TempDir()
	build := filepath.Join(root, "public", "build")
	assets := filepath.Join(root, "assets")
	require.NoError(t, os.MkdirAll(build, 0o755))
	require.NoError(t, os.MkdirAll(assets, 0o755))
	targets := map[string]string{
		filepath.Join(build, "app.css"):      "body{}",
		filepath.Join(build, "app.js"):       "console.log('app')",
		filepath.Join(assets, "main.js"):     "export default {};",
		filepath.Join(assets, "frontend.ts"): "export const app = {};",
	}
	for path, source := range targets {
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	manifestPath := filepath.Join(build, "manifest.json")
	manifest := `{"build/app.js": "/build/app.js"}`
	webpackPath := filepath.Join(root, "webpack.config.js")
	webpack := `Encore.addEntry('storefront', './assets/main.js');`
	vitePath := filepath.Join(root, "vite.config.ts")
	vite := `export default defineConfig({
  build: {rollupOptions: {input: {frontend: './assets/frontend.ts'}}}
});`
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0o644))
	require.NoError(t, os.WriteFile(webpackPath, []byte(webpack), 0o644))
	require.NoError(t, os.WriteFile(vitePath, []byte(vite), 0o644))

	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for path, source := range map[string]string{
		filepath.Join(build, "app.css"): targets[filepath.Join(build, "app.css")],
		manifestPath:                    manifest,
		webpackPath:                     webpack,
		vitePath:                        vite,
	} {
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	provider := NewAssetDefinitionProvider(index, &php.PHPIndex{})
	tests := []struct {
		source string
		value  string
		target string
	}{
		{
			source: `{{ asset('build/app.css') }}`,
			value:  "build/app.css",
			target: filepath.Join(build, "app.css"),
		},
		{
			source: `{{ asset('build/app.js') }}`,
			value:  "build/app.js",
			target: filepath.Join(build, "app.js"),
		},
		{
			source: `{{ encore_entry_script_tags('storefront') }}`,
			value:  "storefront",
			target: filepath.Join(assets, "main.js"),
		},
		{
			source: `{{ vite_entry_script_tags('frontend') }}`,
			value:  "frontend",
			target: filepath.Join(assets, "frontend.ts"),
		},
		{
			source: `<script src="/build/app.js"></script>`,
			value:  "build/app.js",
			target: filepath.Join(build, "app.js"),
		},
	}
	for _, test := range tests {
		document := lsp.NewTextDocument(
			"file://"+filepath.Join(root, "templates", "page.html.twig"),
			test.source,
			1,
		)
		offset := uint32(strings.Index(test.source, test.value) + 2)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		locations := provider.GetDefinition(
			context.Background(),
			securityDefinitionRequest(document, node, offset),
		)
		require.NotEmpty(t, locations, test.value)
		assert.Equal(t, uriutil.FileURI(test.target), locations[0].URI)
	}
}

func TestAssetDefinitionResolvesNamedPackageAndItsDeclaration(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(
		root,
		"public",
		"bundles",
		"administration",
		"administration",
		"app.js",
	)
	config := filepath.Join(
		root,
		"src",
		"Administration",
		"Resources",
		"config",
		"routes.xml",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(config), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("app"), 0o644))
	require.NoError(t, os.WriteFile(config, []byte("<routes/>"), 0o644))
	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		target,
		[]byte("app"),
	)))
	require.NoError(t, index.Index(indexer.NewParsedFile(
		config,
		[]byte("<routes/>"),
	)))
	provider := NewAssetDefinitionProvider(index, nil)
	source := `{{ asset('administration/app.js', '@Administration') }}`
	document := lsp.NewTextDocument(
		"file://"+filepath.Join(root, "templates", "page.html.twig"),
		source,
		1,
	)
	for _, test := range []struct {
		value  string
		target string
	}{
		{value: "administration/app.js", target: target},
		{value: "@Administration", target: config},
	} {
		offset := uint32(strings.Index(source, test.value) + 2)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		locations := provider.GetDefinition(
			context.Background(),
			securityDefinitionRequest(document, node, offset),
		)
		require.NotEmpty(t, locations)
		assert.Equal(t, uriutil.FileURI(test.target), locations[0].URI)
	}
}

func TestAssetDefinitionNavigatesLegacyAsseticBundleGlob(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(
		root,
		"src",
		"MainBundle",
		"Resources",
		"public",
		"css",
		"app.css",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("body{}"), 0o644))
	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		target,
		[]byte("body{}"),
	)))
	source := `{% stylesheets '@MainBundle/Resources/public/css/*.css' %}
<link href="{{ asset_url }}">
{% endstylesheets %}`
	document := lsp.NewTextDocument(
		"file://"+filepath.Join(root, "templates", "page.html.twig"),
		source,
		1,
	)
	offset := uint32(strings.Index(source, "css/*.css") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	locations := NewAssetDefinitionProvider(index, nil).GetDefinition(
		context.Background(),
		securityDefinitionRequest(document, node, offset),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(target), locations[0].URI)

	container := filepath.Join(
		root,
		"app",
		"cache",
		"dev",
		"appDevDebugProjectContainer.xml",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(container), 0o755))
	require.NoError(t, os.WriteFile(container, []byte(
		`<container><services><service id="assetic.asset_manager">`+
			`<call method="addResource"><service class="`+
			`Symfony\Bundle\AsseticBundle\Factory\Resource\ConfigurationResource">`+
			`<argument><argument key="app_css"><argument>`+
			`<argument>`+target+`</argument></argument></argument></argument>`+
			`</service></call></service></services></container>`,
	), 0o644))
	require.NoError(t, index.ReloadAsseticCatalog())
	namedSource := `{% stylesheets '@app_css' %}
<link href="{{ asset_url }}">
{% endstylesheets %}`
	namedDocument := lsp.NewTextDocument(
		"file://"+filepath.Join(root, "templates", "named.html.twig"),
		namedSource,
		1,
	)
	namedOffset := uint32(strings.Index(namedSource, "@app_css") + 2)
	namedNode := namedDocument.SyntaxTree.Root.NodeAtOffset(namedOffset)
	namedLocations := NewAssetDefinitionProvider(index, nil).GetDefinition(
		context.Background(),
		securityDefinitionRequest(
			namedDocument,
			namedNode,
			namedOffset,
		),
	)
	require.NotEmpty(t, namedLocations)
	assert.Equal(t, uriutil.FileURI(target), namedLocations[0].URI)
}

func TestAssetDefinitionNavigatesImportmapTargetAndDeclaration(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "assets", "app.js")
	importmapPath := filepath.Join(root, "importmap.php")
	importmap := `<?php
return [
    'app' => [
        'path' => './assets/app.js',
        'entrypoint' => true,
    ],
];`
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("app"), 0o644))
	require.NoError(t, os.WriteFile(importmapPath, []byte(importmap), 0o644))
	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		importmapPath,
		[]byte(importmap),
	)))

	source := `{{ importmap('app') }}`
	document := lsp.NewTextDocument(
		"file://"+filepath.Join(root, "templates", "page.html.twig"),
		source,
		1,
	)
	offset := uint32(strings.Index(source, "app") + 1)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	locations := NewAssetDefinitionProvider(index, nil).GetDefinition(
		context.Background(),
		securityDefinitionRequest(document, node, offset),
	)
	require.Len(t, locations, 2)
	assert.Equal(t, uriutil.FileURI(target), locations[0].URI)
	assert.Equal(t, uriutil.FileURI(importmapPath), locations[1].URI)
	assert.Equal(t, 2, locations[1].Range.Start.Line)
}
