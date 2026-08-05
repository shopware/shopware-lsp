package twig

import (
	"testing"

	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConstantReferencesResolveGlobalClassAndObjectForms(t *testing.T) {
	source := `{# @var suite \BugDemo\CardSuite #}
{{ constant('App\\Cards::CLUBS') }}
{{ constant('\\BugDemo\\NAMESPACED_CONST') }}
{{ constant('SPADES', suite) }}
{{ constant('IGNORED', factory()) }}
{{ constant(dynamic) }}`
	root := twigparser.Parse(source).Tree.Root
	resolver := PHPAccessResolver{}
	references := ConstantReferencesInDocument(
		"/project/templates/cards.html.twig",
		root,
		resolver,
	)
	require.Len(t, references, 3)
	assert.Equal(t, "App\\Cards", references[0].Class)
	assert.Equal(t, "CLUBS", references[0].Name)
	assert.Equal(t, "", references[1].Class)
	assert.Equal(t, "BugDemo\\NAMESPACED_CONST", references[1].Name)
	assert.Equal(t, "BugDemo\\CardSuite", references[2].Class)
	assert.Equal(t, "SPADES", references[2].Name)
	for _, reference := range references {
		assert.Equal(
			t,
			reference.Name == "CLUBS" ||
				reference.Name == "SPADES" ||
				reference.Name == "BugDemo\\NAMESPACED_CONST",
			true,
		)
		assert.NotEmpty(
			t,
			source[reference.Range.Start:reference.Range.End],
		)
	}

	literals := twigquery.Nodes(root, twigsyntax.TwigLiteralString)
	at := ConstantReferencesAt(
		"/project/templates/cards.html.twig",
		root,
		literals[2],
		resolver,
	)
	require.Len(t, at, 1)
	assert.Equal(t, "BugDemo\\CardSuite", at[0].Class)

	context, found := ConstantCompletionContextAt(
		"/project/templates/cards.html.twig",
		root,
		literals[2],
		resolver,
	)
	require.True(t, found)
	assert.True(t, context.ObjectArgument)
	assert.Equal(t, []string{"BugDemo\\CardSuite"}, context.ReceiverClasses)
}

func TestConstantReferenceKeysSeparateGlobalAndClassConstants(t *testing.T) {
	assert.NotEqual(
		t,
		ConstantReferenceKey(ConstantReference{Name: "VALUE"}),
		ConstantReferenceKey(ConstantReference{
			Class: "App\\Config",
			Name:  "VALUE",
		}),
	)
	assert.Equal(
		t,
		"class\x00app\\config\x00value",
		ConstantReferenceKey(ConstantReference{
			Class: "\\App\\Config\\",
			Name:  "VALUE",
		}),
	)
}
