package completion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/asset"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssetCompletionForTwigEncoreAndTypedPHPPackages(t *testing.T) {
	root := t.TempDir()
	build := filepath.Join(root, "public", "build")
	require.NoError(t, os.MkdirAll(build, 0o755))
	assetPath := filepath.Join(build, "app.css")
	require.NoError(t, os.WriteFile(assetPath, []byte("body{}"), 0o644))

	assetIndex, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, assetIndex.Close()) })
	require.NoError(t, assetIndex.Index(indexer.NewParsedFile(
		assetPath,
		[]byte("body{}"),
	)))
	webpackPath := filepath.Join(root, "webpack.config.js")
	webpack := `Encore.addEntry('storefront', './assets/app.js');`
	require.NoError(t, assetIndex.Index(indexer.NewParsedFile(
		webpackPath,
		[]byte(webpack),
	)))
	vitePath := filepath.Join(root, "vite.config.ts")
	vite := `export default defineConfig({
  build: {rollupOptions: {input: {frontend: './assets/frontend.ts'}}}
});`
	require.NoError(t, assetIndex.Index(indexer.NewParsedFile(
		vitePath,
		[]byte(vite),
	)))

	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	stubs := `<?php
namespace Symfony\Component\Asset;
interface PackageInterface {
    public function getUrl(string $path): string;
    public function getVersion(string $path): string;
}`
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "vendor", "assets.php"),
		[]byte(stubs),
	)))
	provider := NewAssetCompletionProvider(assetIndex, phpIndex)

	twigTests := []struct {
		source string
		marker string
		label  string
	}{
		{
			source: `{{ asset('build/ap') }}`,
			marker: "build/ap",
			label:  "build/app.css",
		},
		{
			source: `{{ encore_entry_script_tags('store') }}`,
			marker: "store",
			label:  "storefront",
		},
		{
			source: `{{ vite_entry_link_tags('front') }}`,
			marker: "front",
			label:  "frontend",
		},
	}
	for _, test := range twigTests {
		path := filepath.Join(root, "templates", "page.html.twig")
		document := lsp.NewTextDocument(
			"file://"+path,
			test.source,
			1,
		)
		offset := uint32(
			strings.Index(test.source, test.marker) + len(test.marker),
		)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		items := provider.GetCompletions(
			context.Background(),
			securityCompletionRequest(document, node, offset),
		)
		requireCompletion(t, items, test.label)
	}

	phpSource := `<?php
use Symfony\Component\Asset\PackageInterface;
function url(PackageInterface $assets): string {
    return $assets->getUrl('build/ap');
}`
	phpPath := filepath.Join(root, "src", "Usage.php")
	document := lsp.NewTextDocument("file://"+phpPath, phpSource, 1)
	offset := uint32(strings.Index(phpSource, "build/ap") + len("build/ap"))
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		phpPath,
		1,
		node,
		document.SyntaxTree.Root,
	)
	items := provider.GetCompletions(
		ctx,
		securityCompletionRequest(document, node, offset),
	)
	requireCompletion(t, items, "build/app.css")
}

func TestAssetCompletionUsesNamedPackageLogicalPathsAndNames(t *testing.T) {
	root := t.TempDir()
	adminAsset := filepath.Join(
		root,
		"public",
		"bundles",
		"administration",
		"administration",
		"app.js",
	)
	configPath := filepath.Join(
		root,
		"src",
		"Administration",
		"Resources",
		"config",
		"routes.xml",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(adminAsset), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	require.NoError(t, os.WriteFile(adminAsset, []byte("app"), 0o644))
	require.NoError(t, os.WriteFile(configPath, []byte("<routes/>"), 0o644))
	assetIndex, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, assetIndex.Close()) })
	require.NoError(t, assetIndex.Index(indexer.NewParsedFile(
		adminAsset,
		[]byte("app"),
	)))
	require.NoError(t, assetIndex.Index(indexer.NewParsedFile(
		configPath,
		[]byte("<routes/>"),
	)))
	provider := NewAssetCompletionProvider(assetIndex, nil)
	tests := []struct {
		source string
		marker string
		label  string
	}{
		{
			source: `{{ asset('administration/ap', '@Administration') }}`,
			marker: "administration/ap",
			label:  "administration/app.js",
		},
		{
			source: `{{ asset('administration/app.js', '@Admin') }}`,
			marker: "@Admin",
			label:  "@Administration",
		},
	}
	for _, test := range tests {
		document := lsp.NewTextDocument(
			"file://"+filepath.Join(root, "templates", "page.html.twig"),
			test.source,
			1,
		)
		offset := uint32(
			strings.Index(test.source, test.marker) + len(test.marker),
		)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		items := provider.GetCompletions(
			context.Background(),
			securityCompletionRequest(document, node, offset),
		)
		requireCompletion(t, items, test.label)
	}
}

func TestAssetCompletionInLegacyAsseticTags(t *testing.T) {
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
	source := `{% stylesheets '@MainBundle/Resources/public/css/ap' %}
<link href="{{ asset_url }}">
{% endstylesheets %}`
	document := lsp.NewTextDocument(
		"file://"+filepath.Join(root, "templates", "page.html.twig"),
		source,
		1,
	)
	offset := uint32(strings.Index(source, "css/ap") + len("css/ap"))
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	items := NewAssetCompletionProvider(index, nil).GetCompletions(
		context.Background(),
		securityCompletionRequest(document, node, offset),
	)
	requireCompletion(
		t,
		items,
		"@MainBundle/Resources/public/css/app.css",
	)
	for _, item := range items {
		assert.NotContains(t, fmt.Sprint(item.TextEdit), "{{ asset(")
	}

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
<argument><argument key="jquery_js"/></argument>
</service></call></service>
</services></container>`), 0o644))
	require.NoError(t, index.ReloadAsseticCatalog())
	namedSource := `{% javascripts '@jquery_' %}
<script src="{{ asset_url }}"></script>
{% endjavascripts %}`
	namedDocument := lsp.NewTextDocument(
		"file://"+filepath.Join(root, "templates", "scripts.html.twig"),
		namedSource,
		1,
	)
	namedOffset := uint32(
		strings.Index(namedSource, "@jquery_") + len("@jquery_"),
	)
	namedNode := namedDocument.SyntaxTree.Root.NodeAtOffset(namedOffset)
	namedItems := NewAssetCompletionProvider(index, nil).GetCompletions(
		context.Background(),
		securityCompletionRequest(
			namedDocument,
			namedNode,
			namedOffset,
		),
	)
	requireCompletion(t, namedItems, "@jquery_js")
}

func TestAssetPackageCompletionIsTypeAwareInPHP(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config", "packages", "framework.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(config), 0o755))
	configSource := `framework:
  assets:
    packages:
      uploads:
        base_path: /uploads
`
	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		config,
		[]byte(configSource),
	)))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "vendor", "Packages.php"),
		[]byte(`<?php
namespace Symfony\Component\Asset;
class Packages {
    public function getUrl(string $path, ?string $packageName = null): string {}
}`),
	)))
	provider := NewAssetCompletionProvider(index, phpIndex)
	tests := []struct {
		class string
		want  bool
	}{
		{
			class: `\Symfony\Component\Asset\Packages`,
			want:  true,
		},
		{class: `\App\Unrelated`, want: false},
	}
	for _, test := range tests {
		source := `<?php
class Usage {
    public function url(` + test.class + ` $packages): string {
        return $packages->getUrl(packageName: 'upl', path: 'image.png');
    }
}`
		path := filepath.Join(root, "src", "Usage.php")
		document := lsp.NewTextDocument("file://"+path, source, 1)
		offset := uint32(strings.Index(source, "upl") + len("upl"))
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			path,
			1,
			node,
			document.SyntaxTree.Root,
		)
		items := provider.GetCompletions(
			ctx,
			securityCompletionRequest(document, node, offset),
		)
		if test.want {
			requireCompletion(t, items, "uploads")
		} else {
			require.NotContains(t, completionLabels(items), "uploads")
		}
	}
}

func TestAssetCompletionOffersOnlyImportmapEntrypoints(t *testing.T) {
	root := t.TempDir()
	importmapPath := filepath.Join(root, "importmap.php")
	source := `<?php
return [
    'app' => ['path' => './assets/app.js', 'entrypoint' => true],
    'bootstrap' => ['version' => '5.3.2'],
];`
	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		importmapPath,
		[]byte(source),
	)))
	documentSource := `{{ importmap('ap') }}`
	document := lsp.NewTextDocument(
		"file://"+filepath.Join(root, "templates", "page.html.twig"),
		documentSource,
		1,
	)
	offset := uint32(strings.LastIndex(documentSource, "ap") + len("ap"))
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	items := NewAssetCompletionProvider(index, nil).GetCompletions(
		context.Background(),
		securityCompletionRequest(document, node, offset),
	)
	requireCompletion(t, items, "app")
	require.NotContains(t, completionLabels(items), "bootstrap")
}

func TestAssetCompletionWrapsTypedTwigHTMLAttributes(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		filepath.Join(root, "public", "build", "theme.css"): "body{}",
		filepath.Join(root, "public", "build", "app.js"):    "app",
		filepath.Join(root, "public", "images", "logo.svg"): "<svg/>",
	}
	for path, source := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for path, source := range files {
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	tests := []struct {
		source  string
		marker  string
		label   string
		newText string
		exclude string
		offset  int
	}{
		{
			source:  `<link rel="stylesheet" href="build/th">`,
			marker:  "build/th",
			label:   "build/theme.css",
			newText: `{{ asset('build/theme.css') }}`,
			exclude: "build/app.js",
		},
		{
			source:  `<script src='build/ap'></script>`,
			marker:  "build/ap",
			label:   "build/app.js",
			newText: `{{ asset("build/app.js") }}`,
			exclude: "build/theme.css",
		},
		{
			source:  `<img src="images/lo">`,
			marker:  "images/lo",
			label:   "images/logo.svg",
			newText: `{{ asset('images/logo.svg') }}`,
			exclude: "build/app.js",
		},
		{
			source:  `<img src="">`,
			label:   "images/logo.svg",
			newText: `{{ asset('images/logo.svg') }}`,
			exclude: "build/app.js",
			offset:  strings.Index(`<img src="">`, `""`) + 1,
		},
	}
	provider := NewAssetCompletionProvider(index, nil)
	for _, test := range tests {
		document := lsp.NewTextDocument(
			"file://"+filepath.Join(root, "templates", "page.html.twig"),
			test.source,
			1,
		)
		offset := test.offset
		if offset == 0 {
			offset = strings.Index(test.source, test.marker) + len(test.marker)
		}
		documentOffset := uint32(offset)
		node := document.SyntaxTree.Root.NodeAtOffset(documentOffset)
		items := provider.GetCompletions(
			context.Background(),
			securityCompletionRequest(document, node, documentOffset),
		)
		require.NotContains(t, completionLabels(items), test.exclude)
		var selected *protocol.CompletionItem
		for position := range items {
			if items[position].Label == test.label {
				selected = &items[position]
				break
			}
		}
		require.NotNil(t, selected)
		edit, ok := selected.TextEdit.(protocol.TextEdit)
		require.True(t, ok)
		assert.Equal(t, test.newText, edit.NewText)
		assert.Equal(t, 0, edit.Range.Start.Line)
		expectedStart := strings.Index(test.source, test.marker)
		if test.marker == "" {
			expectedStart = test.offset
		}
		assert.Equal(t, expectedStart, edit.Range.Start.Character)
	}
}
