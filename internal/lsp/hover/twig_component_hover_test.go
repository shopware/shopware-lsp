package hover

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestTwigLiveComponentHoverShowsComputedAndWritableProps(t *testing.T) {
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

	classPath := filepath.Join(root, "src/Twig/Components/Search.php")
	class := []byte(`<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveAction;
use Symfony\UX\LiveComponent\Attribute\LiveArg;
use Symfony\UX\LiveComponent\Attribute\LiveProp;
#[AsLiveComponent]
final class Search {
    #[LiveProp(writable: true)]
    public string $query = '';

    public function getTotal(): int {}

    #[LiveAction]
    public function submit(
        #[LiveArg('pageNumber')] int $page,
        ?string $query = null,
    ): void {}
}`)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))
	require.NoError(t, componentIndex.Index(indexer.NewParsedFile(
		classPath,
		class,
	)))

	templatePath := filepath.Join(
		root,
		"templates/components/Search.html.twig",
	)
	template := `{{ computed.total }} {{ query }} {{ live_action('submit') }}
<button data-live-action-param="submit" data-live-page-number-param="2">Next</button>`
	require.NoError(t, os.MkdirAll(filepath.Dir(templatePath), 0o755))
	require.NoError(t, os.WriteFile(
		templatePath,
		[]byte(template),
		0o644,
	))
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		templatePath,
		[]byte(template),
	)))
	provider := NewTwigComponentHoverProvider(root, componentIndex)

	computed := componentHoverAt(
		t,
		provider,
		templatePath,
		template,
		"total",
	)
	require.Contains(t, computed.Contents.Value, "Computed component value")
	require.Contains(t, computed.Contents.Value, "PHP type: `int`")
	require.Contains(t, computed.Contents.Value, "cached")

	query := componentHoverAt(
		t,
		provider,
		templatePath,
		template,
		"query",
	)
	require.Contains(t, query.Contents.Value, "writable from the browser")

	action := componentHoverAt(
		t,
		provider,
		templatePath,
		template,
		"submit",
	)
	require.Contains(t, action.Contents.Value, "Symfony UX Live Action")
	require.Contains(t, action.Contents.Value, "int $page")
	require.Contains(t, action.Contents.Value, "null|string $query = …")

	argument := componentHoverAt(
		t,
		provider,
		templatePath,
		template,
		"page-number",
	)
	require.Contains(t, argument.Contents.Value, "Live Action argument")
	require.Contains(t, argument.Contents.Value, "PHP type: `int`")
	require.Contains(t, argument.Contents.Value, "$page")
	require.Contains(t, argument.Contents.Value, "#[LiveArg]")

	pagePath := filepath.Join(root, "templates/page.html.twig")
	usage := `<twig:Search />`
	component := componentHoverAt(
		t,
		provider,
		pagePath,
		usage,
		"Search",
	)
	require.Contains(t, component.Contents.Value, "Live component")
}

func TestTwigComponentHoverShowsCompiledDynamicTemplateMethod(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	containerDir := filepath.Join(root, "var", "cache", "dev-test")
	require.NoError(t, os.MkdirAll(containerDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(
			containerDir,
			"Shopware_Core_KernelDevDebugContainer.xml",
		),
		[]byte(`<container><services>
<service id="ux.twig_component.component_factory">
  <argument/><argument/><argument/><argument/>
  <argument type="collection">
    <argument key="Dynamic" type="collection">
      <argument key="class">App\Twig\Components\Dynamic</argument>
      <argument key="template_from_method">getTemplate</argument>
    </argument>
  </argument>
  <argument type="collection"/>
</service>
</services></container>`),
		0o644,
	))
	serviceIndex, err := symfony.NewServiceIndex(root, cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	componentIndex, err := twigcomponent.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, componentIndex.Close()) })
	componentIndex.SetDependencies(nil, serviceIndex, nil)

	source := `<twig:Dynamic />`
	result := componentHoverAt(
		t,
		NewTwigComponentHoverProvider(root, componentIndex),
		filepath.Join(root, "templates/page.html.twig"),
		source,
		"Dynamic",
	)
	require.Contains(
		t,
		result.Contents.Value,
		"Dynamic template method: `getTemplate`",
	)
	require.Contains(t, result.Contents.Value, "compiled Symfony container")
}

func componentHoverAt(
	t *testing.T,
	provider *TwigComponentHoverProvider,
	path,
	source,
	needle string,
) *protocol.Hover {
	t.Helper()
	document := lsp.NewTextDocument(
		uriutil.FileURI(path),
		source,
		1,
	)
	offset := uint32(strings.Index(source, needle) + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := provider.GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(
					offset,
				),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}
