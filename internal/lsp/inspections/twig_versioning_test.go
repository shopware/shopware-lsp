package inspections

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigVersionCommentFixAddsAndUpdatesComment(t *testing.T) {
	root := t.TempDir()
	idx, err := twig.NewTwigIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	upstreamPath := filepath.Join(
		root, "src", "Storefront", "Resources", "views",
		"storefront", "page", "example.html.twig",
	)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		upstreamPath,
		[]byte(`{% block content %}upstream{% endblock %}`),
	)))
	service := twig.NewVersioningService(root, idx, "6.7.2")
	currentPath := filepath.Join(
		root, "custom", "plugins", "Example", "src", "Resources", "views",
		"storefront", "page", "example.html.twig",
	)
	for _, test := range []struct {
		name   string
		source string
		code   lsp.DiagnosticID
		title  string
	}{
		{
			name: "add",
			source: `{% sw_extends '@Storefront/storefront/page/example.html.twig' %}
{% block content %}local{% endblock %}`,
			code:  diagnostics.TwigVersioningCommentMissingCode,
			title: "Shopware: Add Twig block version comment",
		},
		{
			name: "update",
			source: `{% sw_extends '@Storefront/storefront/page/example.html.twig' %}
{# shopware-block: deadbeef@6.6.0.0 #}
{% block content %}local{% endblock %}`,
			code:  diagnostics.TwigVersioningOutdatedCode,
			title: "Shopware: Update Twig block version comment",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(uriutil.FileURI(currentPath), test.source, 3)
			payload, err := json.Marshal(diagnostics.TwigVersioningPayload{BlockName: "content"})
			require.NoError(t, err)
			fixContext := lsp.FixContext{
				Document:   document,
				Diagnostic: protocol.Diagnostic{Code: string(test.code)},
				FixPayload: payload,
			}
			fix := twigVersionCommentFix{versioning: service}
			presentation, available, err := fix.Present(context.Background(), fixContext)
			require.NoError(t, err)
			require.True(t, available)
			assert.Equal(t, test.title, presentation.Title)
			plan, err := fix.Build(context.Background(), fixContext)
			require.NoError(t, err)
			require.Len(t, plan.Documents, 1)
			updated, err := plan.Documents[0].Apply()
			require.NoError(t, err)
			assert.Contains(t, updated, "{# shopware-block: ")
			assert.Contains(t, updated, "@6.7.2 #}")
			assert.NotContains(t, updated, "deadbeef")
			parsed, err := twig.ParseTwig(currentPath, []byte(updated))
			require.NoError(t, err)
			require.NotNil(t, parsed.Blocks["content"].VersionComment)
		})
	}
}

func TestTwigVersioningInspectionDeclaresOptInMissingRuleAndBoundActions(t *testing.T) {
	root := t.TempDir()
	idx, err := twig.NewTwigIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	upstreamPath := filepath.Join(
		root, "src", "Storefront", "Resources", "views",
		"storefront", "page", "example.html.twig",
	)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		upstreamPath,
		[]byte(`{% block content %}upstream{% endblock %}`),
	)))
	inspection := NewTwigVersioning(twig.NewVersioningService(root, idx, "6.7.2"))
	definition := inspection.Definition()
	var missing lsp.ProblemDefinition
	for _, problem := range definition.Problems {
		if problem.ID == diagnostics.TwigVersioningCommentMissingCode {
			missing = problem
		}
	}
	assert.True(t, missing.DisabledByDefault)

	documentPath := filepath.Join(
		root, "custom", "plugins", "Example", "src", "Resources", "views",
		"storefront", "page", "example.html.twig",
	)
	document := lsp.NewTextDocument(
		uriutil.FileURI(documentPath),
		`{% sw_extends '@Storefront/storefront/page/example.html.twig' %}
{% block content %}local{% endblock %}`,
		1,
	)
	reporter := &capturingProblemReporter{}
	require.NoError(t, inspection.Inspect(context.Background(), document, reporter))
	require.Len(t, reporter.problems, 1)
	assert.Equal(t, diagnostics.TwigVersioningCommentMissingCode, reporter.problems[0].ID)
	require.Len(t, reporter.problems[0].Fixes, 1)
	assert.Equal(t, twigVersionCommentFixID, reporter.problems[0].Fixes[0].ID)
}

type capturingProblemReporter struct {
	problems []lsp.Problem
}

func (r *capturingProblemReporter) Report(problem lsp.Problem) error {
	r.problems = append(r.problems, problem)
	return nil
}
