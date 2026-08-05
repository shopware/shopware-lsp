package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigVersioningAnalyzer_originalNotFoundMessage(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	twigIndexer, err := twig.NewTwigIndexer(tempDir)
	require.NoError(t, err)
	defer func() { _ = twigIndexer.Close() }()

	provider := NewTwigVersioningAnalyzer(twigIndexer)

	uri := "file:///tmp/myext/Resources/views/storefront/page/checkout/foo.html.twig"
	content := []byte(`{% sw_extends '@Storefront/storefront/page/checkout/foo' %}{# shopware-block: abc123def456@6.4.15.0 #}{% block content %}test{% endblock %}`)

	diagnostics, err := provider.Analyze(ctx, diagnosticsDocument(uri, content))
	require.NoError(t, err)

	require.Len(t, diagnostics, 1)
	assert.Contains(t, diagnostics[0].Message, "Original block not found in Storefront for block 'content'")
	assert.Equal(t, protocol.DiagnosticSeverityWarning, diagnostics[0].Severity)
	assert.Equal(t, "shopware-lsp", diagnostics[0].Source)
}

func TestTwigVersioningAnalyzer_nilIndexerNoPanic(t *testing.T) {
	ctx := context.Background()
	provider := NewTwigVersioningAnalyzer(nil)
	require.NotNil(t, provider)

	content := []byte(`{% block foo %}{% endblock %}`)
	uri := "file:///tmp/ext/Resources/views/storefront/page/bar.html.twig"
	diagnostics, err := provider.Analyze(ctx, diagnosticsDocument(uri, content))
	require.NoError(t, err)
	assert.Empty(t, diagnostics)
}

func TestTwigVersioningAnalyzerReportsDeprecatedUpstreamBlock(t *testing.T) {
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	storefrontPath := "/project/src/Storefront/Resources/views/storefront/page/example.html.twig"
	require.NoError(t, index.Index(indexer.NewParsedFile(
		storefrontPath,
		[]byte("{# @deprecated tag:v6.7.0 - use page_new #}\n{% block page_old %}old{% endblock %}"),
	)))
	extensionPath := "/project/custom/plugins/Example/src/Resources/views/storefront/page/example.html.twig"
	source := "{% sw_extends '@Storefront/storefront/page/example.html.twig' %}\n{% block page_old %}custom{% endblock %}"
	problems, err := NewTwigVersioningAnalyzer(index).Analyze(
		context.Background(),
		lsp.NewTextDocument("file://"+extensionPath, source, 1),
	)
	require.NoError(t, err)
	var found bool
	for _, problem := range problems {
		if problem.ID == "twig.block.deprecated" {
			found = true
			assert.Contains(t, problem.Message, "tag:v6.7.0")
			assert.Contains(t, problem.Message, "page_new")
		}
	}
	require.True(t, found)
}
