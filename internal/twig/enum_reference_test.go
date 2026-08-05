package twig

import (
	"testing"

	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnumReferencesFindEnumAndEnumCasesFirstArguments(t *testing.T) {
	source := `{{ enum('App\\Status') }}
{{ enum_cases("App\\Mode") }}
{{ enum(variable) }}
{{ other('App\\Ignored') }}`
	root := twigparser.Parse(source).Tree.Root
	references := EnumReferences(root)
	require.Len(t, references, 2)
	assert.Equal(t, "App\\Status", references[0].Name)
	assert.Equal(t, "App\\Mode", references[1].Name)
	assert.Equal(
		t,
		`App\\Status`,
		source[references[0].Range.Start:references[0].Range.End],
	)

	literal := twigquery.Nodes(
		root,
		twigsyntax.TwigLiteralString,
	)[0]
	reference, found := EnumReferenceAt(literal)
	require.True(t, found)
	assert.Equal(t, references[0], reference)
}

func TestEscapeTwigClassName(t *testing.T) {
	assert.Equal(
		t,
		`App\\Status`,
		EscapeTwigClassName(`\App\Status`),
	)
}
