package diagnostics

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssetDiagnosticsReportMissingAssetAndEncoreEntry(t *testing.T) {
	root := t.TempDir()
	build := filepath.Join(root, "public", "build")
	require.NoError(t, os.MkdirAll(build, 0o755))
	assetPath := filepath.Join(build, "app.css")
	require.NoError(t, os.WriteFile(assetPath, []byte("body{}"), 0o644))
	webpackPath := filepath.Join(root, "webpack.config.js")
	webpack := `Encore.addEntry('storefront', './assets/app.js');`

	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		assetPath,
		[]byte("body{}"),
	)))
	require.NoError(t, index.Index(indexer.NewParsedFile(
		webpackPath,
		[]byte(webpack),
	)))
	source := []byte(`{{ asset('build/ap.css') }}
{{ asset('build/app.css') }}
{{ encore_entry_script_tags('storefrnot') }}
{{ encore_entry_script_tags('storefront') }}`)
	result, err := NewAssetAnalyzer(
		index,
		&php.PHPIndex{},
	).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file://"+filepath.Join(root, "templates", "page.html.twig"),
			source,
		),
	)
	require.NoError(t, err)
	require.Len(t, result, 2)
	codes := []any{result[0].ID, result[1].ID}
	assert.Contains(t, codes, missingAssetCode)
	assert.Contains(t, codes, missingEncoreEntryCode)
	for _, diagnostic := range result {
		suggestions := diagnostic.Payload.(map[string]any)["suggestions"]
		switch diagnostic.ID {
		case missingAssetCode:
			assert.Contains(t, suggestions, "build/app.css")
		case missingEncoreEntryCode:
			assert.Contains(t, suggestions, "storefront")
		}
	}
}

func TestAssetDiagnosticsArePackageAwareAndReportUnknownPackages(
	t *testing.T,
) {
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
	source := []byte(
		`{{ asset('administration/app.js', '@Administration') }}
{{ asset('administration/ap.js', '@Administration') }}
{{ asset('administration/app.js', '@Administrtion') }}`,
	)
	result, err := NewAssetAnalyzer(
		index,
		nil,
	).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file://"+filepath.Join(root, "templates", "page.html.twig"),
			source,
		),
	)
	require.NoError(t, err)
	require.Len(t, result, 2)
	byCode := make(map[lsp.DiagnosticID]lsp.Problem)
	for _, diagnostic := range result {
		byCode[diagnostic.ID] = diagnostic
	}
	assetDiagnostic, found := byCode[missingAssetCode]
	require.True(t, found)
	assert.Contains(t, assetDiagnostic.Message, "administration/ap.js")
	assert.Contains(
		t,
		assetDiagnostic.Payload.(map[string]any)["suggestions"],
		"administration/app.js",
	)
	packageDiagnostic, found := byCode[missingAssetPackageCode]
	require.True(t, found)
	assert.Contains(t, packageDiagnostic.Message, "@Administrtion")
	assert.Contains(
		t,
		packageDiagnostic.Payload.(map[string]any)["suggestions"],
		"@Administration",
	)
}

func TestAssetDiagnosticsValidateLegacyAsseticTagOperands(t *testing.T) {
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
	container := filepath.Join(
		root,
		"app",
		"cache",
		"dev",
		"appDevDebugProjectContainer.xml",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(container), 0o755))
	require.NoError(t, os.WriteFile(container, []byte(`<container><services>
<service id="assetic.asset_manager"><call method="addResource">
<service class="Symfony\Bundle\AsseticBundle\Factory\Resource\ConfigurationResource">
<argument><argument key="named_formula"/></argument>
</service></call></service>
</services></container>`), 0o644))
	require.NoError(t, index.ReloadAsseticCatalog())
	source := []byte(`{% stylesheets
    '@MainBundle/Resources/public/css/*.css'
    '@MainBundle/Resources/public/css/ap.css'
    'css/' ~ dynamic_name
    '@named_formula'
%}
<link href="{{ asset_url }}">
{% endstylesheets %}`)
	result, diagnosticsErr := NewAssetAnalyzer(
		index,
		nil,
	).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file://"+filepath.Join(root, "templates", "page.html.twig"),
			source,
		),
	)
	require.NoError(t, diagnosticsErr)
	require.Len(t, result, 1)
	assert.Equal(t, missingAssetCode, result[0].ID)
	assert.Contains(t, result[0].Message, "css/ap.css")
	assert.Contains(
		t,
		result[0].Payload.(map[string]any)["suggestions"],
		"@MainBundle/Resources/public/css/app.css",
	)

	missingNamedSource := []byte(`{% stylesheets '@named_formla' %}
<link href="{{ asset_url }}">
{% endstylesheets %}`)
	missingNamed, missingNamedErr := NewAssetAnalyzer(
		index,
		nil,
	).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file://"+filepath.Join(
				root,
				"templates",
				"named.html.twig",
			),
			missingNamedSource,
		),
	)
	require.NoError(t, missingNamedErr)
	require.Len(t, missingNamed, 1)
	assert.Contains(t, missingNamed[0].Message, "@named_formla")
	assert.Contains(
		t,
		missingNamed[0].Payload.(map[string]any)["suggestions"],
		"@named_formula",
	)
}

func TestAssetDiagnosticsValidateImportmapEntrypoints(t *testing.T) {
	root := t.TempDir()
	importmapPath := filepath.Join(root, "importmap.php")
	importmap := `<?php
return [
    'app' => ['path' => './assets/app.js', 'entrypoint' => true],
    'bootstrap' => ['version' => '5.3.2'],
];`
	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		importmapPath,
		[]byte(importmap),
	)))
	source := []byte(`{{ importmap('app') }}
{{ importmap('ap') }}
{{ importmap('bootstrap') }}`)
	result, err := NewAssetAnalyzer(index, nil).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file://"+filepath.Join(root, "templates", "page.html.twig"),
			source,
		),
	)
	require.NoError(t, err)
	require.Len(t, result, 2)
	for _, diagnostic := range result {
		assert.Equal(t, missingImportmapCode, diagnostic.ID)
		assert.Contains(t, diagnostic.Message, "AssetMapper entrypoint")
		if strings.Contains(diagnostic.Message, "'ap'") {
			assert.Contains(
				t,
				diagnostic.Payload.(map[string]any)["suggestions"],
				"app",
			)
		}
	}
}

func TestAssetDiagnosticsValidateViteEntrypoints(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "vite.config.js")
	config := `export default defineConfig({
  build: {rollupOptions: {input: {frontend: './assets/frontend.js'}}}
});`
	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		configPath,
		[]byte(config),
	)))
	source := []byte(`{{ vite_entry_script_tags('frontend') }}
{{ vite_entry_link_tags('fronend') }}`)
	result, diagnosticsErr := NewAssetAnalyzer(
		index,
		nil,
	).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file://"+filepath.Join(root, "templates", "page.html.twig"),
			source,
		),
	)
	require.NoError(t, diagnosticsErr)
	require.Len(t, result, 1)
	assert.Equal(t, missingViteEntryCode, result[0].ID)
	assert.Contains(
		t,
		result[0].Payload.(map[string]any)["suggestions"],
		"frontend",
	)
}

func TestAssetDiagnosticsValidateStaticTwigHTMLAssets(t *testing.T) {
	root := t.TempDir()
	logo := filepath.Join(root, "public", "images", "logo.svg")
	require.NoError(t, os.MkdirAll(filepath.Dir(logo), 0o755))
	require.NoError(t, os.WriteFile(logo, []byte("<svg/>"), 0o644))
	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		logo,
		[]byte("<svg/>"),
	)))
	source := []byte(`<img src="/images/logo.svg">
<img src="images/log.svg">
<img src="https://cdn.example.test/missing.svg">
<img src="data:image/svg+xml;base64,abc">
<img src="{{ asset(dynamicPath) }}">
<script src="/_webpack_hot_proxy_/storefront/hot-reloading.js"></script>`)
	result, err := NewAssetAnalyzer(index, nil).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file://"+filepath.Join(root, "templates", "page.html.twig"),
			source,
		),
	)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, missingAssetCode, result[0].ID)
	assert.Contains(t, result[0].Message, "images/log.svg")
	assert.Contains(
		t,
		result[0].Payload.(map[string]any)["suggestions"],
		"images/logo.svg",
	)
}
