package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigOperatorCompletionIncludesLegacyModernAndAliases(
	t *testing.T,
) {
	provider := twigOperatorCompletionFixture(t)
	items := twigOperatorCompletionItems(
		t,
		provider,
		`{% if value <caret> %}`,
	)
	labels := completionLabels(items)
	assert.Contains(t, labels, "**")
	assert.Contains(t, labels, "-")
	assert.Contains(t, labels, "ends with")
	assert.Contains(t, labels, "not")
	assert.Contains(t, labels, "or")
	assert.Contains(t, labels, "starts with")
	assert.Contains(t, labels, "legacy-or")
	assert.Contains(t, labels, "b-custom")
	assert.Contains(t, labels, "expression_not")
	assert.Contains(t, labels, "? :")

	alias := requireCompletion(t, items, "? :")
	assert.Equal(t, int(protocol.OperatorCompletion), alias.Kind)
	assert.Equal(t, "Custom Twig binary operator alias", alias.Detail)
	assert.Contains(t, alias.Documentation.Value, "AppExtension.php")
	unary := requireCompletion(t, items, "expression_not")
	assert.Equal(t, "Custom Twig unary operator", unary.Detail)
}

func TestTwigOperatorCompletionCanLeaveBuiltinsToHostIDE(t *testing.T) {
	provider := twigOperatorCompletionFixture(t).WithoutBuiltinTwigCompletions()
	items := twigOperatorCompletionItems(
		t,
		provider,
		`{% if value <caret> %}`,
	)
	labels := completionLabels(items)
	assert.NotContains(t, labels, "or")
	assert.NotContains(t, labels, "starts with")
	assert.Contains(t, labels, "legacy-or")
	assert.Contains(t, labels, "b-custom")
}

func TestTwigOperatorCompletionContextMatchesTwigIfOperands(t *testing.T) {
	provider := twigOperatorCompletionFixture(t)
	tests := []struct {
		name   string
		source string
		found  bool
	}{
		{
			name:   "after operand",
			source: `{% if foo <caret> %}`,
			found:  true,
		},
		{
			name:   "after not operand",
			source: `{% if not foo <caret> %}`,
			found:  true,
		},
		{
			name:   "after malformed leading operator and operand",
			source: `{% if and foo <caret> %}`,
			found:  true,
		},
		{
			name:   "after complete complex expression",
			source: `{% if foo is red and blue <caret> %}`,
			found:  true,
		},
		{
			name:   "operator fragment after operand",
			source: `{% if foo fa<caret>ke %}`,
			found:  true,
		},
		{
			name:   "before first operand",
			source: `{% if <caret>foo %}`,
		},
		{
			name:   "inside first operand after not",
			source: `{% if not fa<caret>ke %}`,
		},
		{
			name:   "twig test position",
			source: `{% if foo is <caret> %}`,
		},
		{
			name:   "output expression",
			source: `{{ foo <caret> }}`,
		},
		{
			name:   "other statement",
			source: `{% set foo = value <caret> %}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			items := twigOperatorCompletionItems(
				t,
				provider,
				test.source,
			)
			assert.Equal(
				t,
				test.found,
				strings.Contains(
					strings.Join(completionLabels(items), "\x00"),
					"b-custom",
				),
			)
		})
	}
}

func twigOperatorCompletionFixture(
	t *testing.T,
) *TwigCompletionProvider {
	t.Helper()
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		"/project/src/AppExtension.php",
		[]byte(`<?php
class AppExtension extends \Twig\Extension\AbstractExtension
{
    public function getOperators(): array
    {
        return [[], ['legacy-or' => []]];
    }

    public function getExpressionParsers(): array
    {
        return [
            new BinaryOperatorExpressionParser(Node::class, 'b-custom', 18),
            new BinaryOperatorExpressionParser(Node::class, 'elvis', 5, aliases: ['? :']),
            new UnaryOperatorExpressionParser(Node::class, 'expression_not', 70),
        ];
    }
}`),
	)))
	return NewTwigCompletionProvider(
		"/project",
		twigIndex,
		nil,
		nil,
	)
}

func twigOperatorCompletionItems(
	t *testing.T,
	provider *TwigCompletionProvider,
	source string,
) []protocol.CompletionItem {
	t.Helper()
	offset := strings.Index(source, "<caret>")
	require.NotEqual(t, -1, offset)
	source = strings.Replace(source, "<caret>", "", 1)
	_, request := twigCompletionAt(
		"file:///project/templates/operator.html.twig",
		source,
		offset,
	)
	return provider.GetCompletions(context.Background(), request)
}
