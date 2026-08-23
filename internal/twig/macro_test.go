package twig

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
)

func TestMacrosInDocumentCollectsSignatureAndRanges(t *testing.T) {
	source := `{## Renders a form input. ##}
{% macro input(
    ## HTML field name.
    name,
    ## Initial field value.
    value = '',
    required = false,
) %}
<input name="{{ name }}" value="{{ value }}">
{% endmacro %}
`
	file := indexer.NewParsedFile(
		"/project/templates/macros/forms.html.twig",
		[]byte(source),
	)
	macros := MacrosInDocument(file.Path, file.SyntaxTree().Root)
	require.Len(t, macros, 1)
	macro := macros[0]
	require.Equal(t, "input", macro.Name)
	require.Equal(t, "input(name, value = '', required = false)", macro.Signature())
	require.Equal(t, "Renders a form input.", macro.Documentation)
	require.Equal(t, []string{"macros/forms.html.twig"}, macro.Templates)
	require.Equal(t, "input", source[macro.NameRange.Start:macro.NameRange.End])
	require.Equal(t, []string{"name", "value", "required"}, []string{
		macro.Parameters[0].Name,
		macro.Parameters[1].Name,
		macro.Parameters[2].Name,
	})
	require.Equal(t, "HTML field name.", macro.Parameters[0].Documentation)
	require.Equal(t, "Initial field value.", macro.Parameters[1].Documentation)
	require.Empty(t, macro.Parameters[2].Documentation)
}

func TestMacroReferencesResolveNamespaceDirectAliasAndSelf(t *testing.T) {
	source := `{% import 'macros/forms.html.twig' as forms %}
{% from 'macros/forms.html.twig' import input as field %}
{% import _self as local %}
{{ forms.input('name') }}
{{ field('name') }}
{{ local.helper() }}
{% macro helper() %}{% endmacro %}
`
	path := "/project/templates/page.html.twig"
	file := indexer.NewParsedFile(path, []byte(source))
	references := MacroReferencesInDocument(path, file.SyntaxTree().Root)

	var inputReferences, helperReferences []MacroReference
	for _, reference := range references {
		switch {
		case reference.Name == "input" &&
			reference.Role == MacroUsageReference:
			inputReferences = append(inputReferences, reference)
		case reference.Name == "helper":
			helperReferences = append(helperReferences, reference)
		}
	}
	require.Len(t, inputReferences, 3)
	for _, reference := range inputReferences {
		require.Equal(
			t,
			[]string{"macros/forms.html.twig"},
			reference.Templates,
		)
	}
	require.Len(t, helperReferences, 2)
	require.Equal(t, MacroDeclarationReference, helperReferences[0].Role)
	require.Equal(t, MacroUsageReference, helperReferences[1].Role)
	require.Equal(t, []string{"page.html.twig"}, helperReferences[1].Templates)

	offset := uint32(strings.Index(source, "forms.input") + len("forms."))
	node := file.SyntaxTree().Root.NodeAtOffset(offset)
	context, found := MacroCompletionAt(
		path,
		file.SyntaxTree().Root,
		node,
	)
	require.True(t, found)
	require.Equal(t, []string{"macros/forms.html.twig"}, context.Templates)
}

func TestMacroCompletionAtIncompleteNamespaceAccessor(t *testing.T) {
	source := `{% import 'macros/forms.html.twig' as forms %}
{{ forms. }}
`
	path := "/project/templates/page.html.twig"
	file := indexer.NewParsedFile(path, []byte(source))
	offset := uint32(strings.LastIndex(source, "forms.") + len("forms."))
	node := file.SyntaxTree().Root.NodeAtOffset(offset)
	require.NotNil(t, node)
	context, found := MacroCompletionAt(
		path,
		file.SyntaxTree().Root,
		node,
	)
	require.True(t, found)
	require.Equal(t, []string{"macros/forms.html.twig"}, context.Templates)
}
