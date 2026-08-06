package entityschema

import (
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestRewriteDefinitionPreservesCustomMembers(t *testing.T) {
	spec := exampleSpec()
	original, err := RenderDefinition(spec)
	require.NoError(t, err)
	original = original[:len(original)-2] + "\n    public function custom(): string { return 'keep'; }\n}\n"
	spec.Fields = append(spec.Fields, FieldSpec{ID: "description", Kind: FieldLongText, PropertyName: "description", StorageName: "description", Editable: true})
	result, err := RewriteDefinition(original, spec)
	require.NoError(t, err)
	require.Contains(t, result, "function custom")
	require.Contains(t, result, "new LongTextField('description', 'description')")
	require.Empty(t, phpparser.Parse(result).Errors)
}

func TestRewriteEntityPreservesCustomMembers(t *testing.T) {
	before := exampleSpec()
	original, err := RenderEntity(before)
	require.NoError(t, err)
	original = original[:len(original)-2] + "\n    public function custom(): string { return 'keep'; }\n}\n"
	after := before
	after.Fields = append(after.Fields, FieldSpec{ID: "description", Kind: FieldLongText, PropertyName: "description", StorageName: "description", Editable: true})
	result, err := RewriteEntity(original, before, after)
	require.NoError(t, err)
	require.Contains(t, result, "function custom")
	require.Contains(t, result, "$description")
	require.Empty(t, phpparser.Parse(result).Errors)
}
