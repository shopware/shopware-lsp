package diagnostics

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigVersioningAnalyzer_originalNotFoundMessage(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	twigIndexer, err := twig.NewTwigIndexer(tempDir)
	require.NoError(t, err)
	defer func() { _ = twigIndexer.Close() }()

	provider := NewTwigVersioningAnalyzer(twig.NewVersioningService("/tmp", twigIndexer, ""))

	uri := "file:///tmp/myext/Resources/views/storefront/page/checkout/foo.html.twig"
	content := []byte(`{% sw_extends '@Storefront/storefront/page/checkout/foo' %}{# shopware-block: abc123def456@6.4.15.0 #}{% block content %}test{% endblock %}`)

	diagnostics, err := provider.Analyze(ctx, diagnosticsDocument(uri, content))
	require.NoError(t, err)

	assert.Empty(t, diagnostics)
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
	problems, err := NewTwigVersioningAnalyzer(twig.NewVersioningService("/project", index, "")).Analyze(
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

func TestTwigVersioningAnalyzerAcceptsAnyChainCandidateAndReportsChanges(t *testing.T) {
	root := t.TempDir()
	index, err := twig.NewTwigIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	corePath := filepath.Join(
		root, "src", "Storefront", "Resources", "views",
		"storefront", "page", "base.html.twig",
	)
	themePath := filepath.Join(
		root, "custom", "plugins", "Theme", "src", "Resources", "views",
		"storefront", "theme", "base.html.twig",
	)
	require.NoError(t, index.Index(indexer.NewParsedFile(
		corePath,
		[]byte(`{% block content %}core{% endblock %}`),
	)))
	require.NoError(t, index.Index(indexer.NewParsedFile(
		themePath,
		[]byte(`{% sw_extends '@Storefront/storefront/page/base.html.twig' %}
{% block content %}theme{% endblock %}`),
	)))
	hashes, err := index.GetTwigBlockHashes("content")
	require.NoError(t, err)
	var coreHash string
	for _, hash := range hashes {
		if hash.AbsolutePath == corePath {
			coreHash = hash.Hash
		}
	}
	require.NotEmpty(t, coreHash)
	pluginPath := filepath.Join(
		root, "custom", "plugins", "Plugin", "src", "Resources", "views",
		"storefront", "custom", "page.html.twig",
	)
	analyzer := NewTwigVersioningAnalyzer(twig.NewVersioningService(root, index, "6.7.2"))
	matching := `{% sw_extends '@Theme/storefront/theme/base.html.twig' %}
{# shopware-block: ` + coreHash + `@6.7.2.0 #}
{% block content %}local{% endblock %}`
	problems, err := analyzer.Analyze(
		context.Background(),
		lsp.NewTextDocument("file://"+pluginPath, matching, 1),
	)
	require.NoError(t, err)
	for _, problem := range problems {
		assert.NotEqual(t, TwigVersioningOutdatedCode, problem.ID)
	}

	outdated := `{% sw_extends '@Theme/storefront/theme/base.html.twig' %}
{# shopware-block: deadbeef@6.6.0.0 #}
{% block content %}local{% endblock %}`
	problems, err = analyzer.Analyze(
		context.Background(),
		lsp.NewTextDocument("file://"+pluginPath, outdated, 2),
	)
	require.NoError(t, err)
	require.True(t, containsTwigVersioningProblem(problems, TwigVersioningOutdatedCode))
}

func TestTwigVersioningAnalyzerReportsRemovalOnlyForResolvableParent(t *testing.T) {
	root := t.TempDir()
	index, err := twig.NewTwigIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	corePath := filepath.Join(
		root, "src", "Storefront", "Resources", "views",
		"storefront", "page", "base.html.twig",
	)
	require.NoError(t, index.Index(indexer.NewParsedFile(
		corePath,
		[]byte(`{% block other %}core{% endblock %}`),
	)))
	analyzer := NewTwigVersioningAnalyzer(twig.NewVersioningService(root, index, ""))
	pluginPath := filepath.Join(root, "custom", "plugin.html.twig")
	tracked := `{% sw_extends '@Storefront/storefront/page/base.html.twig' %}
{# shopware-block: deadbeef@6.6.0.0 #}
{% block removed %}local{% endblock %}`
	problems, err := analyzer.Analyze(
		context.Background(),
		lsp.NewTextDocument("file://"+pluginPath, tracked, 1),
	)
	require.NoError(t, err)
	require.True(t, containsTwigVersioningProblem(problems, TwigVersioningOriginalMissingCode))

	standalone := strings.Replace(
		tracked,
		"@Storefront/storefront/page/base.html.twig",
		"@Missing/storefront/page/base.html.twig",
		1,
	)
	problems, err = analyzer.Analyze(
		context.Background(),
		lsp.NewTextDocument("file://"+pluginPath, standalone, 2),
	)
	require.NoError(t, err)
	assert.False(t, containsTwigVersioningProblem(problems, TwigVersioningOriginalMissingCode))
}

func TestTwigVersioningAnalyzerTreatsMalformedCommentAsMissing(t *testing.T) {
	root := t.TempDir()
	index, err := twig.NewTwigIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	corePath := filepath.Join(
		root, "src", "Storefront", "Resources", "views",
		"storefront", "page", "base.html.twig",
	)
	require.NoError(t, index.Index(indexer.NewParsedFile(
		corePath,
		[]byte(`{% block content %}core{% endblock %}`),
	)))
	pluginPath := filepath.Join(root, "custom", "plugin.html.twig")
	source := `{% sw_extends '@Storefront/storefront/page/base.html.twig' %}
{# shopware-block: invalid-value@6.6.0.0 #}
{% block content %}local{% endblock %}`
	problems, err := NewTwigVersioningAnalyzer(
		twig.NewVersioningService(root, index, ""),
	).Analyze(
		context.Background(),
		lsp.NewTextDocument("file://"+pluginPath, source, 1),
	)
	require.NoError(t, err)
	require.True(t, containsTwigVersioningProblem(problems, TwigVersioningCommentMissingCode))
}

func TestTwigVersioningAnalyzerReportsExactResolvedParentCopy(t *testing.T) {
	root := t.TempDir()
	index, err := twig.NewTwigIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	upstreamPath := filepath.Join(
		root, "src", "Storefront", "Resources", "views",
		"storefront", "page", "example.html.twig",
	)
	block := "{% block content %}\n    <div>same</div>\n{% endblock %}"
	require.NoError(t, index.Index(indexer.NewParsedFile(upstreamPath, []byte(block))))
	pluginPath := filepath.Join(
		root, "custom", "plugins", "Example", "src", "Resources", "views",
		"storefront", "page", "example.html.twig",
	)
	source := "{% sw_extends '@Storefront/storefront/page/example.html.twig' %}\n" + block
	problems, err := NewTwigVersioningAnalyzer(
		twig.NewVersioningService(root, index, ""),
	).Analyze(
		context.Background(),
		lsp.NewTextDocument(uriutil.FileURI(pluginPath), source, 1),
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.Equal(t, TwigBlockRedundantOverrideCode, problems[0].ID)
	assert.Contains(t, problems[0].Message, "parent()")
	require.Len(t, problems[0].RelatedInformation, 1)
	assert.Equal(
		t,
		uriutil.FileURI(upstreamPath),
		problems[0].RelatedInformation[0].Location.URI,
	)
}

func TestTwigVersioningAnalyzerDoesNotReportDifferentOrFallbackBlock(t *testing.T) {
	root := t.TempDir()
	index, err := twig.NewTwigIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	upstreamPath := filepath.Join(
		root, "src", "Storefront", "Resources", "views",
		"storefront", "page", "example.html.twig",
	)
	require.NoError(t, index.Index(indexer.NewParsedFile(
		upstreamPath,
		[]byte(`{% block other %}parent{% endblock %}`),
	)))
	fallbackPath := filepath.Join(
		root, "custom", "plugins", "Other", "src", "Resources", "views",
		"storefront", "page", "example.html.twig",
	)
	block := `{% block content %}same{% endblock %}`
	require.NoError(t, index.Index(indexer.NewParsedFile(fallbackPath, []byte(block))))
	pluginPath := filepath.Join(
		root, "custom", "plugins", "Example", "src", "Resources", "views",
		"storefront", "page", "example.html.twig",
	)
	analyzer := NewTwigVersioningAnalyzer(twig.NewVersioningService(root, index, ""))
	for _, source := range []string{
		"{% sw_extends '@Storefront/storefront/page/example.html.twig' %}\n" + block,
		"{% sw_extends '@Storefront/storefront/page/example.html.twig' %}\n" +
			`{% block other %}different{% endblock %}`,
	} {
		problems, analyzeErr := analyzer.Analyze(
			context.Background(),
			lsp.NewTextDocument(uriutil.FileURI(pluginPath), source, 1),
		)
		require.NoError(t, analyzeErr)
		assert.False(t, containsTwigVersioningProblem(problems, TwigBlockRedundantOverrideCode))
	}
}

func TestTwigVersioningAnalyzerIgnoresParentDelegation(t *testing.T) {
	root := t.TempDir()
	index, err := twig.NewTwigIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	upstreamPath := filepath.Join(
		root, "src", "Storefront", "Resources", "views",
		"storefront", "page", "example.html.twig",
	)
	require.NoError(t, index.Index(indexer.NewParsedFile(
		upstreamPath,
		[]byte(`{% block content %}parent{% endblock %}`),
	)))
	pluginPath := filepath.Join(root, "custom", "plugin.html.twig")
	source := `{% sw_extends '@Storefront/storefront/page/example.html.twig' %}
{% block content %}
    {{ parent() }}
{% endblock %}`
	problems, err := NewTwigVersioningAnalyzer(
		twig.NewVersioningService(root, index, ""),
	).Analyze(
		context.Background(),
		lsp.NewTextDocument(uriutil.FileURI(pluginPath), source, 1),
	)
	require.NoError(t, err)
	assert.False(t, containsTwigVersioningProblem(problems, TwigVersioningCommentMissingCode))
	assert.False(t, containsTwigVersioningProblem(problems, TwigBlockRedundantOverrideCode))
}

func containsTwigVersioningProblem(
	problems []lsp.Problem,
	id lsp.DiagnosticID,
) bool {
	for _, problem := range problems {
		if problem.ID == id {
			return true
		}
	}
	return false
}
