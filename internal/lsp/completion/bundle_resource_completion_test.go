package completion

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBundleResourceCompletionAcrossConfigurationSyntaxes(
	t *testing.T,
) {
	root, provider := bundleResourceCompletionFixture(t)
	label := "FooBundle/Resources/config/routing.yml"
	for _, fixture := range []struct {
		name       string
		file       string
		source     string
		expectedAt string
	}{
		{
			name:       "quoted YAML resource",
			file:       "routes.yaml",
			source:     "imports:\n  - resource: '@Foo<caret>'\n",
			expectedAt: "@Foo",
		},
		{
			name:       "empty flow YAML resource",
			file:       "routes.yaml",
			source:     "imports:\n  - { resource: <caret> }\n",
			expectedAt: "",
		},
		{
			name: "nested YAML route resource path",
			file: "routes.yaml",
			source: `controllers:
  resource:
    path: '@Foo<caret>'
    namespace: App\Controller
  type: attribute
`,
			expectedAt: "@Foo",
		},
		{
			name:       "XML import",
			file:       "routes.xml",
			source:     `<routes><import resource="@Foo<caret>"/></routes>`,
			expectedAt: "@Foo",
		},
		{
			name: "PHP configurator import",
			file: "routes.php",
			source: `<?php
use Symfony\Component\Routing\Loader\Configurator\RoutingConfigurator;
return static function (RoutingConfigurator $routes): void {
    $routes->import('@Foo<caret>', 'attribute');
};`,
			expectedAt: "@Foo",
		},
		{
			name: "empty PHP configurator import",
			file: "routes.php",
			source: `<?php
use Symfony\Component\Routing\Loader\Configurator\RoutingConfigurator;
return static function (RoutingConfigurator $routes): void {
    $routes->import('<caret>', 'attribute');
};`,
			expectedAt: "",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source, offset := completionCaret(t, fixture.source)
			path := filepath.Join(root, "config", fixture.file)
			require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
			document, request := bundleResourceCompletionRequest(
				t,
				path,
				source,
				offset,
			)
			items := provider.GetCompletions(
				context.Background(),
				request,
			)
			item := requireCompletion(t, items, label)
			assert.Equal(t, int(protocol.FileCompletion), item.Kind)
			edit, ok := item.TextEdit.(protocol.TextEdit)
			require.True(t, ok)
			assert.Equal(
				t,
				"@FooBundle/Resources/config/routing.yml",
				edit.NewText,
			)
			start := document.LineIndex.OffsetUTF16(
				uint32(edit.Range.Start.Line),
				uint32(edit.Range.Start.Character),
			)
			end := document.LineIndex.OffsetUTF16(
				uint32(edit.Range.End.Line),
				uint32(edit.Range.End.Character),
			)
			assert.Equal(t, fixture.expectedAt, source[start:end])
			assert.LessOrEqual(t, start, uint32(offset))
			assert.GreaterOrEqual(t, end, uint32(offset))
		})
	}
}

func TestBundleResourceCompletionIncludesConventionalAndLocalFiles(
	t *testing.T,
) {
	root, provider := bundleResourceCompletionFixture(t)
	localDir := filepath.Join(root, "config", "routes")
	require.NoError(t, os.MkdirAll(localDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(localDir, "local.yaml"),
		[]byte("local: {}\n"),
		0o644,
	))
	source, offset := completionCaret(
		t,
		"imports:\n  - { resource: '<caret>' }\n",
	)
	path := filepath.Join(root, "config", "app.yaml")
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	_, request := bundleResourceCompletionRequest(t, path, source, offset)
	items := provider.GetCompletions(context.Background(), request)

	requireCompletion(
		t,
		items,
		"FooBundle/Resources/config/routing.yml",
	)
	requireCompletion(t, items, "FooBundle/config/routes.xml")
	requireCompletion(t, items, "FooBundle/Controller/CatalogController.php")
	requireCompletion(t, items, "FooBundle/src/Controller/ModernController.php")
	localFolder := requireCompletion(t, items, "routes")
	assert.Equal(t, int(protocol.FolderCompletion), localFolder.Kind)
	localFile := requireCompletion(t, items, "routes/local.yaml")
	assert.Equal(t, int(protocol.FileCompletion), localFile.Kind)
}

func TestBundleResourceCompletionRejectsUnrelatedContexts(t *testing.T) {
	root, provider := bundleResourceCompletionFixture(t)
	for _, fixture := range []struct {
		file   string
		source string
	}{
		{
			file:   "routes.yaml",
			source: "other: '@Foo<caret>'\n",
		},
		{
			file:   "services.xml",
			source: `<services><service resource="@Foo<caret>"/></services>`,
		},
		{
			file: "routes.php",
			source: `<?php
return static function (object $loader): void {
    $loader->import('@Foo<caret>');
};`,
		},
	} {
		source, offset := completionCaret(t, fixture.source)
		path := filepath.Join(root, "config", fixture.file)
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
		_, request := bundleResourceCompletionRequest(t, path, source, offset)
		assert.Empty(t, provider.GetCompletions(
			context.Background(),
			request,
		))
	}
}

func bundleResourceCompletionFixture(
	t *testing.T,
) (string, *BundleResourceCompletionProvider) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "config"), 0o755))
	phpIndex, err := php.NewPHPIndex(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	interfacePath := filepath.Join(root, "BundleInterface.php")
	interfaceSource := `<?php
namespace Symfony\Component\HttpKernel\Bundle;
interface BundleInterface {}
`
	bundleRoot := filepath.Join(root, "vendor", "acme", "foo-bundle")
	bundlePath := filepath.Join(bundleRoot, "FooBundle.php")
	bundleSource := `<?php
namespace Acme\Foo;
final class FooBundle implements \Symfony\Component\HttpKernel\Bundle\BundleInterface {}
`
	for path, source := range map[string]string{
		interfacePath: interfaceSource,
		bundlePath:    bundleSource,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	for path, source := range map[string]string{
		filepath.Join(bundleRoot, "Resources", "config", "routing.yml"):        "routes",
		filepath.Join(bundleRoot, "config", "routes.xml"):                      "<routes/>",
		filepath.Join(bundleRoot, "Controller", "CatalogController.php"):       "<?php",
		filepath.Join(bundleRoot, "src", "Controller", "ModernController.php"): "<?php",
		filepath.Join(bundleRoot, "README.md"):                                 "not a conventional resource",
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	return root, NewBundleResourceCompletionProvider(phpIndex)
}

func bundleResourceCompletionRequest(
	t *testing.T,
	path,
	source string,
	offset int,
) (*lsp.TextDocument, *lsp.CompletionRequest) {
	t.Helper()
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	if node == nil && offset > 0 {
		node = document.SyntaxTree.Root.NodeAtOffset(uint32(offset - 1))
	}
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return document, &lsp.CompletionRequest{
		CompletionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            node,
		},
	}
}

func completionCaret(t *testing.T, source string) (string, int) {
	t.Helper()
	offset := strings.Index(source, "<caret>")
	require.NotEqual(t, -1, offset)
	return strings.Replace(source, "<caret>", "", 1), offset
}
