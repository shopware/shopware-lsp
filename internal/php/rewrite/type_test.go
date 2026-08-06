package phprewrite

import (
	"testing"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/stretchr/testify/require"
)

func TestNativeTypeEditsCompose(t *testing.T) {
	t.Parallel()
	source := `<?php
class Demo
{
    private $value;

    public function convert($input): string {}
}`
	editor, root := testEditor(t, source)
	class := phpquery.Classes(root)[0]
	method := phpquery.Methods(class)[0]
	parameters := phpquery.IterateParameters(method)
	require.True(t, parameters.Next())
	require.NoError(t, editor.SetParameterType(parameters.Node(), "array"))
	require.NoError(t, editor.SetPropertyType(phpquery.Properties(class)[0], "mixed"))
	require.NoError(t, editor.SetReturnType(method, "\\Traversable"))
	require.Equal(t, `<?php
class Demo
{
    private mixed $value;

    public function convert(array $input): \Traversable {}
}`, applyTestEditor(t, source, editor))
}

func TestNativeTypeEditsReplaceCompositeTypes(t *testing.T) {
	t.Parallel()
	source := `<?php
function convert(string|int $input): ?string {}`
	editor, root := testEditor(t, source)
	function := phpquery.Functions(root)[0]
	parameters := phpquery.IterateParameters(function)
	require.True(t, parameters.Next())
	require.NoError(t, editor.SetParameterType(parameters.Node(), "?array"))
	require.NoError(t, editor.SetReturnType(function, "bool"))
	require.Equal(t, `<?php
function convert(?array $input): bool {}`, applyTestEditor(t, source, editor))
}
