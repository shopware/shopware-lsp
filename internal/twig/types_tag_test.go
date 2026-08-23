package twig

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTypesTagDeclarationsSupportOptionalMultilineAndEscapedTypes(
	t *testing.T,
) {
	source := []byte(`{% types {
    user: '\App\Entity\User',
    products?: 'array<App\\Entity\\Product>',
    "createdAt": 'DateTimeImmutable',
} %}`)
	declarations := TypesTagDeclarations(source)
	require.Len(t, declarations, 3)
	assert.Equal(t, "user", declarations[0].Name)
	assert.Equal(t, "App\\Entity\\User", declarations[0].Type)
	assert.False(t, declarations[0].Optional)
	assert.Equal(t, "products", declarations[1].Name)
	assert.Equal(
		t,
		"array<App\\Entity\\Product>",
		declarations[1].Type,
	)
	assert.True(t, declarations[1].Optional)
	assert.Equal(t, "createdAt", declarations[2].Name)
	assert.Equal(t, "DateTimeImmutable", declarations[2].Type)
	for _, declaration := range declarations {
		start := declaration.NameRange.Start
		end := declaration.NameRange.End
		assert.Equal(
			t,
			declaration.Name,
			string(source[start:end]),
		)
	}
}

func TestTypesTagDeclarationsIncludeDocumentationComments(t *testing.T) {
	source := []byte(`{% types {
    ## Unique identifier.
    ## Used in generated markup.
    id: 'string',

    # A regular comment remains non-documentation.
    ## Whether several items can be open.
    multiple?: 'boolean',
} %}`)

	declarations := TypesTagDeclarations(source)
	require.Len(t, declarations, 2)
	assert.Equal(
		t,
		"Unique identifier.\nUsed in generated markup.",
		declarations[0].Documentation,
	)
	assert.Equal(
		t,
		"Whether several items can be open.",
		declarations[1].Documentation,
	)
}

func TestTypesTagCompletionAndClassReferences(t *testing.T) {
	sourceWithCaret := `{% types {
    user: 'App\\Ent<caret>ity\\User',
    products?: 'array<App\\Entity\\Product>',
} %}`
	offset := strings.Index(sourceWithCaret, "<caret>")
	require.NotEqual(t, -1, offset)
	source := []byte(strings.Replace(sourceWithCaret, "<caret>", "", 1))
	context, found := TypesTagCompletionAt(source, uint32(offset))
	require.True(t, found)
	assert.Equal(t, "App\\Ent", context.Prefix)
	assert.Equal(
		t,
		`App\\Entity\\User`,
		string(source[context.Range.Start:context.Range.End]),
	)

	references := TypesTagClassReferences(source)
	require.Len(t, references, 2)
	assert.Equal(t, "App\\Entity\\User", references[0].Name)
	assert.Equal(t, "App\\Entity\\Product", references[1].Name)
	for _, reference := range references {
		assert.Equal(
			t,
			reference.Raw,
			string(source[reference.Range.Start:reference.Range.End]),
		)
	}
	selected, found := TypesTagClassReferenceAt(
		source,
		uint32(strings.Index(string(source), "Product")+2),
	)
	require.True(t, found)
	assert.Equal(t, "App\\Entity\\Product", selected.Name)
}

func TestTypesTagQueriesIgnoreCommentsAndRawBodies(t *testing.T) {
	source := []byte(`{# {% types { fake: 'App\\Fake' } %} #}
{% raw %}{% types { raw: 'App\\Raw' } %}{% endraw %}
{% types { real: 'App\\Real' } %}`)
	declarations := TypesTagDeclarations(source)
	require.Len(t, declarations, 1)
	assert.Equal(t, "real", declarations[0].Name)
}
