package semantic

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/stretchr/testify/require"
)

func TestTwigUXToolkitSemanticTokens(t *testing.T) {
	source := `{# Regular comment #}
{# @var other \App\Entity\Other #}
{#
 @prop items App\Entity\Item[] List of items
 @prop value string|int|null The value
 @block content The item content
#}
{{ '@prop ignored string Not a comment' }}
{# @prop incomplete string #}
`
	document := lsp.NewTextDocument(
		"file:///templates/components/Card.html.twig",
		source,
		1,
	)
	provider := NewTwigUXToolkitProvider()
	tokens, err := provider.GetSemanticTokens(
		context.Background(),
		&lsp.SemanticTokensRequest{
			Document: document,
		},
	)
	require.NoError(t, err)
	require.Len(t, tokens, 11)

	expected := []struct {
		text      string
		tokenType uint32
	}{
		{"@var", protocol.SemanticTokenKeyword},
		{"other", protocol.SemanticTokenVariable},
		{`\App\Entity\Other`, protocol.SemanticTokenType},
		{"@prop", protocol.SemanticTokenKeyword},
		{"items", protocol.SemanticTokenProperty},
		{`App\Entity\Item[]`, protocol.SemanticTokenType},
		{"@prop", protocol.SemanticTokenKeyword},
		{"value", protocol.SemanticTokenProperty},
		{"string|int|null", protocol.SemanticTokenType},
		{"@block", protocol.SemanticTokenKeyword},
		{"content", protocol.SemanticTokenProperty},
	}
	for index, expectation := range expected {
		token := tokens[index]
		require.Equal(t, expectation.tokenType, token.Type)
		require.Equal(
			t,
			expectation.text,
			string(document.Text[token.Range.Start:token.Range.End]),
		)
	}
}

func TestTwigVarSemanticTokensSupportBothOrdersAndMultipleDeclarations(
	t *testing.T,
) {
	source := `{#
 @var items \App\Entity\User[]|\App\Entity\Admin[]
 @var \App\Entity\Category $category
 @var ShortName ambiguous
#}
{{ '@var ignored \App\Ignored' }}`
	document := lsp.NewTextDocument(
		"file:///templates/catalog.html.twig",
		source,
		1,
	)
	tokens, err := NewTwigUXToolkitProvider().GetSemanticTokens(
		context.Background(),
		&lsp.SemanticTokensRequest{Document: document},
	)
	require.NoError(t, err)

	expected := []struct {
		text      string
		tokenType uint32
	}{
		{"@var", protocol.SemanticTokenKeyword},
		{"items", protocol.SemanticTokenVariable},
		{`\App\Entity\User[]|\App\Entity\Admin[]`, protocol.SemanticTokenType},
		{"@var", protocol.SemanticTokenKeyword},
		{`\App\Entity\Category`, protocol.SemanticTokenType},
		{"$category", protocol.SemanticTokenVariable},
		{"@var", protocol.SemanticTokenKeyword},
		{"ShortName", protocol.SemanticTokenVariable},
		{"ambiguous", protocol.SemanticTokenType},
	}
	require.Len(t, tokens, len(expected))
	for index, expectation := range expected {
		token := tokens[index]
		require.Equal(t, expectation.tokenType, token.Type)
		require.Equal(
			t,
			expectation.text,
			string(document.Text[token.Range.Start:token.Range.End]),
		)
	}
}

func TestTwigUXToolkitSemanticTokensIgnoreOtherDocuments(t *testing.T) {
	provider := NewTwigUXToolkitProvider()
	document := lsp.NewTextDocument(
		"file:///templates/component.php",
		`<?php // @prop value string Description`,
		1,
	)
	tokens, err := provider.GetSemanticTokens(
		context.Background(),
		&lsp.SemanticTokensRequest{Document: document},
	)
	require.NoError(t, err)
	require.Empty(t, tokens)
}
