package form

import (
	"strings"
	"testing"

	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig/parser"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigFieldContextUsesOnlyDirectFormViewChildren(t *testing.T) {
	source := `{{ checkout.email.vars }}
{{ checkout. }}
{{ checkout.vars.compound }}
{{ checkout.vars. }}
{{ ordinary.title }}`
	parsed := twigparser.Parse(source)
	require.NotNil(t, parsed.Tree)
	variables := []php.TwigTemplateVariable{{
		Name:      "checkout",
		FormTypes: []string{"App\\Form\\CheckoutType"},
	}}

	references := TwigFieldReferences(parsed.Tree.Root, variables)
	require.Len(t, references, 1)
	assert.Equal(t, "checkout", references[0].Variable)
	assert.Equal(t, "email", references[0].Name)
	assert.Equal(
		t,
		[]string{"App\\Form\\CheckoutType"},
		references[0].FormTypes,
	)

	offset := strings.Index(source, "checkout. }}") + len("checkout.")
	require.Positive(t, offset)
	node := parsed.Tree.Root.NodeAtOffset(uint32(offset - 1))
	context, found := TwigFieldContextAt(node, uint32(offset), variables)
	require.True(t, found)
	assert.Equal(t, "checkout", context.Variable)
	assert.Empty(t, context.Name)
	assert.Nil(t, context.Node)

	viewVars := TwigViewVarReferences(parsed.Tree.Root, variables)
	require.Len(t, viewVars, 1)
	assert.Equal(t, "compound", viewVars[0].Name)
	assert.Equal(t, "checkout", viewVars[0].Variable)

	offset = strings.Index(source, "checkout.vars. }}") +
		len("checkout.vars.")
	require.Positive(t, offset)
	node = parsed.Tree.Root.NodeAtOffset(uint32(offset - 1))
	viewContext, found := TwigViewVarContextAt(
		node,
		uint32(offset),
		variables,
	)
	require.True(t, found)
	assert.Equal(t, "checkout", viewContext.Variable)
	assert.Empty(t, viewContext.Name)
}

func TestTwigFormFunctionReferencesUseControllerFormProvenance(t *testing.T) {
	source := `{{ form_start(checkout) }}
{{ form(checkout.email) }}
{{ form_end(checkout) }}
{{ form_rest(checkout) }}
{{ form_start(view: checkout) }}
{{ form_end(form = checkout) }}
{{ form_row(checkout.email) }}
{{ form(ordinary) }}`
	parsed := twigparser.Parse(source)
	require.NotNil(t, parsed.Tree)
	variables := []php.TwigTemplateVariable{{
		Name:      "checkout",
		FormTypes: []string{"App\\Form\\CheckoutType"},
	}}

	references := TwigFormFunctionReferences(parsed.Tree.Root, variables)
	require.Len(t, references, 6)
	assert.Equal(
		t,
		[]string{
			"form_start",
			"form",
			"form_end",
			"form_rest",
			"form_start",
			"form_end",
		},
		[]string{
			references[0].Function,
			references[1].Function,
			references[2].Function,
			references[3].Function,
			references[4].Function,
			references[5].Function,
		},
	)
	for _, reference := range references {
		assert.Equal(t, "checkout", reference.Variable)
		assert.Equal(
			t,
			[]string{"App\\Form\\CheckoutType"},
			reference.FormTypes,
		)
		assert.NotZero(t, reference.Range.Len())
	}
}
