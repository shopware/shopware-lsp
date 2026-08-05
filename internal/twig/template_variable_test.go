package twig

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
)

func TestTemplateInputVariablesExcludeLocalsAndRetainProps(t *testing.T) {
	source := `{% props title, tone = 'info' %}
{% set local = source %}
{% import 'macros.html.twig' as forms %}
{% from 'macros.html.twig' import input as field %}
{% for key, item in items %}
    {{ item.name }} {{ outside }}
{% endfor %}
{{ product.name }}
{{ helper(value = argument) }}
{{ {'ignored': hashValue} }}
{% component Alert with {message: componentValue} %}{% endcomponent %}
{{ forms.input(title) }} {{ field(title) }}
{% macro card(label) %}{{ label }} {{ macroGlobal }}{% endmacro %}
`
	root := twigparser.Parse(source).Tree.Root
	variables := TemplateInputVariablesInDocument(
		"/project/templates/card.html.twig",
		root,
	)
	var names []string
	for _, variable := range variables {
		names = append(names, variable.Name)
		require.Equal(
			t,
			variable.Name,
			source[variable.Range.Start:variable.Range.End],
		)
	}
	require.ElementsMatch(t, []string{
		"argument",
		"componentValue",
		"hashValue",
		"items",
		"macroGlobal",
		"outside",
		"product",
		"source",
		"title",
		"tone",
	}, names)
	for _, excluded := range []string{
		"Alert",
		"field",
		"forms",
		"helper",
		"ignored",
		"item",
		"key",
		"label",
		"local",
		"name",
		"value",
	} {
		require.False(t, slices.Contains(names, excluded), excluded)
	}
}

func TestTemplateDependenciesRespectProvidedAndIsolatedContexts(
	t *testing.T,
) {
	source := `{% extends 'base.html.twig' %}
{% include 'nested.html.twig' with {foo: supplied} %}
{% include 'isolated.html.twig' with {value: isolatedValue} only %}
{{ include('function.html.twig', {known: explicit}) }}
{{ include('hidden.html.twig', {}, with_context: false) }}
{% embed 'layout.html.twig' with {slot: content} %}{% endembed %}
`
	dependencies := TemplateDependenciesInDocument(
		twigparser.Parse(source).Tree.Root,
	)
	require.ElementsMatch(t, []TemplateDependency{
		{Template: "base.html.twig", Propagate: true},
		{
			Template:  "nested.html.twig",
			Provided:  []string{"foo"},
			Propagate: true,
		},
		{
			Template:  "isolated.html.twig",
			Provided:  []string{"value"},
			Propagate: false,
		},
		{
			Template:  "layout.html.twig",
			Provided:  []string{"slot"},
			Propagate: true,
		},
		{
			Template:  "function.html.twig",
			Provided:  []string{"known"},
			Propagate: true,
		},
		{
			Template:  "hidden.html.twig",
			Propagate: false,
		},
	}, dependencies)
}

func TestIncludeParameterContextRecognizesSupportedForms(t *testing.T) {
	tests := []struct {
		source   string
		needle   string
		template string
		kind     IncludeParameterKind
		name     string
	}{
		{
			source:   `{{ include('card.html.twig', {'title': value}) }}`,
			needle:   "title",
			template: "card.html.twig",
			kind:     IncludeFunctionParameter,
			name:     "title",
		},
		{
			source:   `{% include 'card.html.twig' with {plainKey: value} %}`,
			needle:   "plainKey",
			template: "card.html.twig",
			kind:     IncludeTagParameter,
			name:     "plainKey",
		},
		{
			source:   `{% embed 'layout.html.twig' with {"slot": value} %}{% endembed %}`,
			needle:   "slot",
			template: "layout.html.twig",
			kind:     EmbedTagParameter,
			name:     "slot",
		},
	}
	for _, test := range tests {
		root := twigparser.Parse(test.source).Tree.Root
		offset := uint32(strings.Index(test.source, test.needle) + 1)
		node := root.NodeAtOffset(offset)
		context, found := IncludeParameterContextAt(
			root,
			node,
			offset,
		)
		require.True(t, found, test.source)
		require.Equal(t, test.template, context.Template)
		require.Equal(t, test.kind, context.Kind)
		parameter, found := IncludeParameterAt(root, node, offset)
		require.True(t, found)
		require.Equal(t, test.name, parameter.Name)
		require.Equal(
			t,
			test.name,
			test.source[parameter.Range.Start:parameter.Range.End],
		)
	}
}

func TestIncludeParameterContextRejectsNestedHashKeysAndValues(
	t *testing.T,
) {
	source := `{% include 'card.html.twig' with {
    direct: value,
    nested: {ignored: other}
} %}`
	root := twigparser.Parse(source).Tree.Root
	for _, needle := range []string{"value", "ignored", "other"} {
		offset := uint32(strings.Index(source, needle) + 1)
		_, found := IncludeParameterAt(
			root,
			root.NodeAtOffset(offset),
			offset,
		)
		require.False(t, found, needle)
	}
	offset := uint32(strings.Index(source, "direct") + 1)
	parameter, found := IncludeParameterAt(
		root,
		root.NodeAtOffset(offset),
		offset,
	)
	require.True(t, found)
	require.Equal(t, "direct", parameter.Name)
}

func TestTwigIndexerStoresRestoresAndResolvesTemplateContracts(
	t *testing.T,
) {
	cache := t.TempDir()
	root := t.TempDir()
	index, err := NewTwigIndexer(cache)
	require.NoError(t, err)

	files := map[string]string{
		"base.html.twig":   `{{ baseValue }}`,
		"nested.html.twig": `{{ supplied }} {{ nestedValue }}`,
		"card.html.twig": `{% extends 'base.html.twig' %}
{% include 'nested.html.twig' with {supplied: explicitValue} %}
{{ ownValue }}`,
	}
	for name, source := range files {
		require.NoError(t, index.Index(indexer.NewParsedFile(
			filepath.Join(root, "templates", name),
			[]byte(source),
		)))
	}

	assertContract := func(index *TwigIndexer) {
		t.Helper()
		variables, queryErr := index.GetTemplateVariables(
			"card.html.twig",
		)
		require.NoError(t, queryErr)
		var names []string
		for _, variable := range variables {
			names = append(names, variable.Name)
		}
		require.ElementsMatch(t, []string{
			"baseValue",
			"explicitValue",
			"nestedValue",
			"ownValue",
		}, names)
		require.NotContains(t, names, "supplied")
	}
	assertContract(index)
	require.NoError(t, index.Close())

	restored, err := NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	assertContract(restored)
}
