package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/symfonyconfig"
)

func TestSymfonyConfigCompletionForRootAndConditionalPHPArrays(
	t *testing.T,
) {
	index, err := symfonyconfig.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/src/DependencyInjection/Configuration.php",
		[]byte(`<?php
namespace App\DependencyInjection;
use Symfony\Component\Config\Definition\Builder\TreeBuilder;
final class Configuration {
    public function getConfigTreeBuilder(): TreeBuilder {
        return new TreeBuilder('app_root');
    }
}
`),
	)))
	provider := NewSymfonyConfigCompletionProvider(index)

	for _, fixture := range []struct {
		uri    string
		source string
	}{
		{
			uri:    "file:///project/config/packages/app.php",
			source: "<?php return ['app_' => []];",
		},
		{
			uri:    "file:///project/config/packages/app.php",
			source: "<?php return ['when@prod' => ['app_' => []]];",
		},
		{
			uri:    "file:///project/config/packages/app.yaml",
			source: "app_: {}\n",
		},
		{
			uri: "file:///project/config/packages/app.yaml",
			source: `when@prod:
  app_: {}
`,
		},
	} {
		document := lsp.NewTextDocument(
			fixture.uri,
			fixture.source,
			1,
		)
		offset := uint32(
			strings.LastIndex(fixture.source, "app_") + 2,
		)
		items := provider.GetCompletions(
			context.Background(),
			securityCompletionRequest(
				document,
				document.SyntaxTree.Root.NodeAtOffset(offset),
				offset,
			),
		)
		requireCompletion(t, items, "app_root")
	}
}

func TestSymfonyConfigCompletionRejectsNestedOrdinaryArrays(t *testing.T) {
	index, err := symfonyconfig.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for _, fixture := range []struct {
		uri    string
		source string
	}{
		{
			uri:    "file:///project/config/packages/app.php",
			source: "<?php return ['data' => ['app_' => []]];",
		},
		{
			uri: "file:///project/config/packages/app.yaml",
			source: `data:
  app_: {}
`,
		},
	} {
		document := lsp.NewTextDocument(
			fixture.uri,
			fixture.source,
			1,
		)
		offset := uint32(
			strings.LastIndex(fixture.source, "app_") + 2,
		)
		items := NewSymfonyConfigCompletionProvider(index).GetCompletions(
			context.Background(),
			securityCompletionRequest(
				document,
				document.SyntaxTree.Root.NodeAtOffset(offset),
				offset,
			),
		)
		require.Empty(t, items)
	}
}
