package asset

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndexCollectsPublicManifestAndEncoreResources(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	build := filepath.Join(root, "public", "build")
	assets := filepath.Join(root, "assets")
	media := filepath.Join(root, "public", "media")
	require.NoError(t, os.MkdirAll(build, 0o755))
	require.NoError(t, os.MkdirAll(assets, 0o755))
	require.NoError(t, os.MkdirAll(media, 0o755))
	files := map[string]string{
		filepath.Join(build, "app.123.css"): "body{}",
		filepath.Join(build, "app.123.js"):  "console.log('app')",
		filepath.Join(build, "manifest.json"): `{
  "build/app.css": "/build/app.123.css",
  "build/app.js": "/build/app.123.js"
}`,
		filepath.Join(build, "entrypoints.json"): `{
  "entrypoints": {
    "app": {"js": ["/build/app.123.js"], "css": ["/build/app.123.css"]},
    "admin": {"js": []}
  }
}`,
		filepath.Join(root, "webpack.config.js"): `
Encore.addEntry('storefront', './assets/storefront.js')
    .addStyleEntry('theme', './assets/theme.css');`,
		filepath.Join(assets, "storefront.js"): "export default {};",
		filepath.Join(assets, "theme.css"):     "body{}",
		filepath.Join(media, "upload.jpg"):     "generated",
	}
	for path, source := range files {
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}

	idx, err := NewIndex(root, cache)
	require.NoError(t, err)
	scanner, err := indexer.NewFileScanner(
		root,
		filepath.Join(cache, "scanner.db"),
	)
	require.NoError(t, err)
	scanner.AddIndexer(idx)
	require.NoError(t, scanner.IndexAll(context.Background()))
	require.NoError(t, scanner.Close())

	names, err := idx.Names()
	require.NoError(t, err)
	assert.Contains(t, names, "build/app.123.css")
	assert.Contains(t, names, "build/app.css")
	assert.NotContains(t, names, "media/upload.jpg")
	entries, err := idx.EntryNames()
	require.NoError(t, err)
	assert.Contains(t, entries, "app")
	assert.Contains(t, entries, "admin")
	assert.Contains(t, entries, "storefront")
	assert.Contains(t, entries, "theme")
	firstCatalog, err := idx.NameCatalog()
	require.NoError(t, err)
	secondCatalog, err := idx.NameCatalog()
	require.NoError(t, err)
	require.NotEmpty(t, firstCatalog.Assets)
	require.Same(t, &firstCatalog.Assets[0], &secondCatalog.Assets[0])

	manifest, err := idx.Find("build/app.css", ManifestAsset)
	require.NoError(t, err)
	require.Len(t, manifest, 1)
	assert.Equal(
		t,
		filepath.Join(build, "app.123.css"),
		manifest[0].Target,
	)
	storefront, err := idx.Find("storefront", EncoreEntry)
	require.NoError(t, err)
	require.Len(t, storefront, 1)
	assert.Equal(
		t,
		filepath.Join(assets, "storefront.js"),
		storefront[0].Target,
	)
	require.NoError(t, idx.Close())

	restored, err := NewIndex(root, cache)
	require.NoError(t, err)
	restoredEntries, err := restored.EntryNames()
	require.NoError(t, err)
	assert.Equal(t, entries, restoredEntries)
	require.NoError(t, restored.Close())
}

func TestPublicPathSelectionUsesRootComponentBoundaries(t *testing.T) {
	root := t.TempDir()
	index, err := NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })

	publicRoot := filepath.Join(root, "public")
	webAsset := filepath.Join(root, "web", "build", "app.js")
	relative, found := index.publicRelative(publicRoot)
	require.True(t, found)
	require.Empty(t, relative)
	relative, found = index.publicRelative(webAsset)
	require.True(t, found)
	require.Equal(t, filepath.Join("build", "app.js"), relative)

	_, found = index.publicRelative(filepath.Join(root, "publicity", "app.js"))
	require.False(t, found)
	_, found = index.publicRelative(
		filepath.Join(root, "public", "..", "private", "app.js"),
	)
	require.False(t, found)

	require.True(t, index.ShouldIndexPath(
		filepath.Join(root, "public", "build", "app.js"),
	))
	require.True(t, index.ShouldIndexPath(
		filepath.Join(root, "public", "media.jpg"),
	))
	require.False(t, index.ShouldIndexPath(
		filepath.Join(root, "public", "MeDiA", "upload.jpg"),
	))
	require.False(t, index.ShouldEnterDirectory(
		filepath.Join(root, "public", "UPLOADS"),
	))
	require.True(t, index.ShouldEnterDirectory(
		filepath.Join(root, "public", "build"),
	))

	assetPath := filepath.Join(root, "public", "build", "app.js")
	require.Zero(t, testing.AllocsPerRun(1_000, func() {
		_, _ = index.publicRelative(assetPath)
	}))
	require.Zero(t, testing.AllocsPerRun(1_000, func() {
		_ = index.ShouldIndexPath(assetPath)
	}))

	relativeIndex := &Index{
		root:           ".",
		publicRoots:    [2]string{"public", "web"},
		normalizedRoot: ".",
	}
	relative, bundle, found := relativeIndex.bundlePublicRelative(
		filepath.Join(
			"src",
			"DemoBundle",
			"Resources",
			"public",
			"app.js",
		),
	)
	require.True(t, found)
	require.Equal(t, "app.js", relative)
	require.Equal(t, "demo", bundle)
}

func TestIndexResolvesNamedBundleBasePathAndThemePackages(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	files := map[string]string{
		filepath.Join(
			root,
			"public",
			"bundles",
			"administration",
			"administration",
			"app.js",
		): "console.log('admin')",
		filepath.Join(
			root,
			"public",
			"theme",
			"theme-id",
			"assets",
			"logo.svg",
		): "<svg/>",
		filepath.Join(
			root,
			"src",
			"Administration",
			"Resources",
			"config",
			"routes.xml",
		): "<routes/>",
		filepath.Join(
			root,
			"src",
			"Storefront",
			"Resources",
			"config",
			"services.xml",
		): `<container><services><service id="theme">
<tag name="assets.package" package="theme"/>
</service></services></container>`,
	}
	for path, source := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	index, err := NewIndex(root, cache)
	require.NoError(t, err)
	for path, source := range files {
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	names, err := index.PackageNames()
	require.NoError(t, err)
	assert.Contains(t, names, "@Administration")
	assert.Contains(t, names, "theme")
	admin, err := index.FindAssetsForPackage(
		"administration/app.js",
		"@Administration",
	)
	require.NoError(t, err)
	require.Len(t, admin, 1)
	theme, err := index.FindAssetsForPackage(
		"assets/logo.svg",
		"theme",
	)
	require.NoError(t, err)
	require.Len(t, theme, 1)
	adminNames, err := index.NamesForPackage("@Administration")
	require.NoError(t, err)
	assert.Contains(t, adminNames, "administration/app.js")
	themeNames, err := index.NamesForPackage("theme")
	require.NoError(t, err)
	assert.Contains(t, themeNames, "assets/logo.svg")
	require.NoError(t, index.Close())

	restored, err := NewIndex(root, cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	restoredPackages, err := restored.PackageNames()
	require.NoError(t, err)
	assert.Equal(t, names, restoredPackages)
	restoredAdmin, err := restored.FindAssetsForPackage(
		"administration/app.js",
		"@Administration",
	)
	require.NoError(t, err)
	assert.Equal(t, admin, restoredAdmin)
}

func TestIndexResolvesLegacyAsseticBundleFilesDirectoriesAndGlobs(
	t *testing.T,
) {
	root := t.TempDir()
	cache := t.TempDir()
	cssDir := filepath.Join(
		root,
		"src",
		"MainBundle",
		"Resources",
		"public",
		"css",
	)
	app := filepath.Join(cssDir, "app.css")
	theme := filepath.Join(cssDir, "theme.scss")
	require.NoError(t, os.MkdirAll(cssDir, 0o755))
	require.NoError(t, os.WriteFile(app, []byte("body{}"), 0o644))
	require.NoError(t, os.WriteFile(theme, []byte("$color: red;"), 0o644))

	idx, err := NewIndex(root, cache)
	require.NoError(t, err)
	scanner, err := indexer.NewFileScanner(
		root,
		filepath.Join(cache, "scanner.db"),
	)
	require.NoError(t, err)
	scanner.AddIndexer(idx)
	require.NoError(t, scanner.IndexAll(context.Background()))
	require.NoError(t, scanner.Close())
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	names, err := idx.AsseticNames("@MainBundle")
	require.NoError(t, err)
	assert.Equal(t, []string{"css/app.css", "css/theme.scss"}, names)
	exact, err := idx.FindAsseticAssets(
		"css/app.css",
		"@MainBundle",
	)
	require.NoError(t, err)
	require.Len(t, exact, 1)
	assert.Equal(t, app, exact[0].Target)
	glob, err := idx.FindAsseticAssets(
		"css/*.css",
		"@MainBundle",
	)
	require.NoError(t, err)
	require.Len(t, glob, 1)
	assert.Equal(t, app, glob[0].Target)
	directory, err := idx.FindAsseticAssets(
		"css/",
		"@MainBundle",
	)
	require.NoError(t, err)
	assert.Len(t, directory, 2)
}

func TestIndexCollectsImportmapEntrypointsAndInstalledModules(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	appTarget := filepath.Join(root, "assets", "app.js")
	vendorTarget := filepath.Join(
		root,
		"assets",
		"vendor",
		"bootstrap",
		"bootstrap.index.js",
	)
	importmapPath := filepath.Join(root, "importmap.php")
	installedPath := filepath.Join(root, "assets", "vendor", "installed.php")
	importmapSource := `<?php
return [
    'app' => [
        'path' => './assets/app.js',
        'entrypoint' => true,
    ],
    'bootstrap' => [
        'version' => '5.3.2',
    ],
];`
	installedSource := `<?php return [
    'bootstrap' => [
        'version' => '5.3.2',
        'dependencies' => [],
    ],
];`
	files := map[string]string{
		appTarget:     "console.log('app')",
		vendorTarget:  "export default {};",
		importmapPath: importmapSource,
		installedPath: installedSource,
		filepath.Join(root, "vendor", "composer", "installed.php"): `<?php
return ['packages' => []];`,
	}
	for path, source := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}

	idx, err := NewIndex(root, cache)
	require.NoError(t, err)
	for path, source := range files {
		require.NoError(t, idx.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	entrypoints, err := idx.ImportmapEntryNames()
	require.NoError(t, err)
	assert.Equal(t, []string{"app"}, entrypoints)
	app, err := idx.FindImportmapEntrypoint("app")
	require.NoError(t, err)
	require.Len(t, app, 1)
	assert.Equal(t, appTarget, app[0].Target)
	assert.True(t, app[0].Entrypoint)
	assert.NotZero(t, app[0].Range.Len())
	bootstrap, err := idx.Find("bootstrap", ImportmapModule)
	require.NoError(t, err)
	require.Len(t, bootstrap, 2)
	assert.Equal(t, "5.3.2", bootstrap[0].Version)
	assert.Contains(t, []string{
		bootstrap[0].Target,
		bootstrap[1].Target,
	}, vendorTarget)
	missingEntrypoint, err := idx.FindImportmapEntrypoint("bootstrap")
	require.NoError(t, err)
	assert.Empty(t, missingEntrypoint)
	composerModules, err := idx.Find("packages", ImportmapModule)
	require.NoError(t, err)
	assert.Empty(t, composerModules)
	require.NoError(t, idx.Close())

	restored, err := NewIndex(root, cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	restoredEntrypoints, err := restored.ImportmapEntryNames()
	require.NoError(t, err)
	assert.Equal(t, entrypoints, restoredEntrypoints)
	restoredApp, err := restored.FindImportmapEntrypoint("app")
	require.NoError(t, err)
	assert.Equal(t, app, restoredApp)
}
