package twig

import (
	"testing"

	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtensionUsagesInDocumentFindsExactFunctionsFiltersAndTests(
	t *testing.T,
) {
	source := `{{ product_number_function('123') }}
{{ value|product_number_filter }}
{% apply product_number_filter %}value{% endapply %}
{{ forms.input('email') }}
{{ value|product_number_filter(argument) }}
{% if value is product_number_test %}
{% elseif value is product_number_test('strict') %}
{% endif %}`
	root := twigparser.Parse(source).Tree.Root
	usages := ExtensionUsagesInDocument("/project/template.html.twig", root)

	var functions, filters, tests []ExtensionUsage
	for _, usage := range usages {
		switch usage.Kind {
		case ExtensionFunctionUsage:
			functions = append(functions, usage)
		case ExtensionFilterUsage:
			filters = append(filters, usage)
		case ExtensionTestUsage:
			tests = append(tests, usage)
		}
		assert.Equal(
			t,
			usage.Name,
			source[usage.Range.Start:usage.Range.End],
		)
	}
	require.Len(t, functions, 1)
	assert.Equal(t, "product_number_function", functions[0].Name)
	require.Len(t, filters, 3)
	for _, filter := range filters {
		assert.Equal(t, "product_number_filter", filter.Name)
	}
	require.Len(t, tests, 2)
	for _, twigTest := range tests {
		assert.Equal(t, "product_number_test", twigTest.Name)
	}
}

func TestExtensionUsageAtMatchesOnlyNameRange(t *testing.T) {
	source := `{{ form_start(value) }}`
	root := twigparser.Parse(source).Tree.Root
	usage, found := ExtensionUsageAt(
		"/project/template.html.twig",
		root,
		uint32(len("{{ form_")),
	)
	require.True(t, found)
	assert.Equal(t, ExtensionFunctionUsage, usage.Kind)
	assert.Equal(t, "form_start", usage.Name)

	_, found = ExtensionUsageAt(
		"/project/template.html.twig",
		root,
		uint32(len("{{ form_start(")),
	)
	assert.False(t, found)
}
