package diagnostics

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYAMLCompatibilityDiagnosticsFindInvalidQuotedBackslashes(t *testing.T) {
	document := lsp.NewTextDocument(
		"file:///project/config/services.yaml",
		"class: \"Foo\\Bar\"\n",
		1,
	)
	result, err := yamlCompatibilityProvider("2.8.0").Analyze(
		context.Background(),
		document,
	)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, yamlInvalidQuotedEscapeCode, result[0].ID)
	assert.Equal(t, yamlInvalidQuotedEscapeMessage, result[0].Message)
	assert.Equal(t, `"Foo\Bar"`, problemRangeText(document, result[0].Range))
	assert.Equal(
		t,
		`"Foo\\Bar"`,
		result[0].Payload.(map[string]any)["replacement"],
	)
	assertYAMLCompatibilityDiagnostic(t, result[0])
}

func TestYAMLCompatibilityDiagnosticsAcceptValidQuotedEscapes(t *testing.T) {
	for _, value := range []string{
		`"Foo\\Bar"`,
		`"Foo\nBar"`,
		`"Foo\rBar"`,
		`"Foo\tBar"`,
		`"Foo\_Bar"`,
		`'Foo\Bar'`,
		`Foo\Bar`,
	} {
		t.Run(value, func(t *testing.T) {
			document := lsp.NewTextDocument(
				"file:///project/config/services.yaml",
				"class: "+value+"\n",
				1,
			)
			result, err := yamlCompatibilityProvider("2.8.0").
				Analyze(context.Background(), document)
			require.NoError(t, err)
			assert.Empty(t, problemsWithCode(
				result,
				yamlInvalidQuotedEscapeCode,
			))
		})
	}

	long := `"Foo\Bar` + strings.Repeat("a", 255) + `"`
	document := lsp.NewTextDocument(
		"file:///project/config/services.yaml",
		"class: "+long+"\n",
		1,
	)
	result, err := yamlCompatibilityProvider("2.8.0").Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	assert.Empty(t, problemsWithCode(
		result,
		yamlInvalidQuotedEscapeCode,
	))
}

func TestYAMLCompatibilityDiagnosticsFindUnquotedIndicators(t *testing.T) {
	document := lsp.NewTextDocument(
		"file:///project/config/services.yaml",
		`at: @foo
tick: `+"`foo"+`
pipe: |foo
fold: >foo
percent: %foo
quoted_at: '@foo'
quoted_percent: "%foo"
single: @
`,
		1,
	)
	result, err := yamlCompatibilityProvider("2.8.0").Analyze(
		context.Background(),
		document,
	)

	require.NoError(t, err)
	indicators := problemsWithCode(result, yamlUnquotedIndicatorCode)
	require.Len(t, indicators, 5)
	expected := map[string]string{
		"@foo": "Deprecated usage of '@' at the beginning of unquoted string",
		"`foo": "Deprecated usage of '`' at the beginning of unquoted string",
		"|foo": "Deprecated usage of '|' at the beginning of unquoted string",
		">foo": "Deprecated usage of '>' at the beginning of unquoted string",
		"%foo": "Not quoting a scalar starting with the '%' indicator character is deprecated since Symfony 3.1",
	}
	for _, diagnostic := range indicators {
		text := problemRangeText(document, diagnostic.Range)
		assert.Equal(t, expected[text], diagnostic.Message)
		assert.Equal(
			t,
			"'"+text+"'",
			diagnostic.Payload.(map[string]any)["replacement"],
		)
		assertYAMLCompatibilityDiagnostic(t, diagnostic)
	}
}

func TestYAMLCompatibilityDiagnosticsFindUnquotedMappingColon(t *testing.T) {
	for _, source := range []string{
		"class: foobar: fff\n",
		"services:\n   class: foobar: fff\n",
	} {
		t.Run(source, func(t *testing.T) {
			document := lsp.NewTextDocument(
				"file:///project/config/services.yaml",
				source,
				1,
			)
			result, err := yamlCompatibilityProvider("2.8.0").
				Analyze(context.Background(), document)

			require.NoError(t, err)
			values := problemsWithCode(result, yamlUnquotedColonCode)
			require.Len(t, values, 1)
			assert.Equal(t, yamlUnquotedColonMessage, values[0].Message)
			assert.Equal(
				t,
				"foobar: fff",
				problemRangeText(document, values[0].Range),
			)
			assert.Equal(
				t,
				"'foobar: fff'",
				values[0].Payload.(map[string]any)["replacement"],
			)
			assertYAMLCompatibilityDiagnostic(t, values[0])
		})
	}
}

func TestYAMLCompatibilityDiagnosticsIgnoreLegalColonContexts(t *testing.T) {
	for _, source := range []string{
		"class: [foobar:fff]\n",
		"class: [foo, foobar:fff]\n",
		"class: {foobar:fff}\n",
		"class: foobar:ddd\n",
		"class: foobar: ddd \n fff\n",
		"class: " + strings.Repeat("a", 201) + ": value\n",
	} {
		t.Run(source, func(t *testing.T) {
			document := lsp.NewTextDocument(
				"file:///project/config/services.yaml",
				source,
				1,
			)
			result, err := yamlCompatibilityProvider("2.8.0").
				Analyze(context.Background(), document)
			require.NoError(t, err)
			assert.Empty(t, problemsWithCode(
				result,
				yamlUnquotedColonCode,
			))
		})
	}
}

func TestYAMLCompatibilityDiagnosticsRequireSymfony28(t *testing.T) {
	document := lsp.NewTextDocument(
		"file:///project/config/services.yaml",
		"class: \"Foo\\Bar\"\nreference: @foo\nlabel: foo: bar\n",
		1,
	)
	for name, provider := range map[string]*YAMLCompatibilityAnalyzer{
		"Symfony 2.7":     yamlCompatibilityProvider("2.7.9"),
		"unknown version": NewYAMLCompatibilityAnalyzer(nil),
	} {
		t.Run(name, func(t *testing.T) {
			result, err := provider.Analyze(
				context.Background(),
				document,
			)
			require.NoError(t, err)
			assert.Empty(t, result)
		})
	}
}

func TestYAMLCompatibilityDiagnosticsHandleIncompleteCST(t *testing.T) {
	for _, source := range []string{
		"class: \"Foo\\\n",
		"class: @\n",
		"class: [foo\n",
	} {
		document := lsp.NewTextDocument(
			"file:///project/config/services.yaml",
			source,
			1,
		)
		_, err := yamlCompatibilityProvider("7.3.0").Analyze(
			context.Background(),
			document,
		)
		require.NoError(t, err, fmt.Sprintf("source %q", source))
	}
}

func yamlCompatibilityProvider(
	version string,
) *YAMLCompatibilityAnalyzer {
	return NewYAMLCompatibilityAnalyzer(&project.Model{
		Dependencies: []project.Package{{
			Name:    "symfony/http-kernel",
			Version: version,
		}},
	})
}

func problemsWithCode(
	diagnostics []lsp.Problem,
	code lsp.DiagnosticID,
) []lsp.Problem {
	var result []lsp.Problem
	for _, diagnostic := range diagnostics {
		if diagnostic.ID == code {
			result = append(result, diagnostic)
		}
	}
	return result
}

func assertYAMLCompatibilityDiagnostic(
	t *testing.T,
	diagnostic lsp.Problem,
) {
	t.Helper()
	assert.Equal(t, protocol.DiagnosticSeverityHint, diagnostic.Severity)
	assert.Equal(t, "symfony", diagnostic.Source)
	assert.Equal(
		t,
		[]protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
		diagnostic.Tags,
	)
}
