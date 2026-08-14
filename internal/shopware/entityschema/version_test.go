package entityschema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBulkEntityExtensionVersionSupport(t *testing.T) {
	require.False(t, BulkEntityExtensionSupported("~6.6.9"))
	require.True(t, BulkEntityExtensionSupported("~6.6.10"))
	require.True(t, BulkEntityExtensionSupported("^6.7"))
	require.True(t, BulkEntityExtensionSupported(""), "unknown target versions must stay permissive")
	require.Equal(t,
		[]DefinitionKind{DefinitionEntity, DefinitionMapping, DefinitionExtension},
		DefinitionKindsForVersion("6.6.9"),
	)
	require.Equal(t,
		[]DefinitionKind{DefinitionEntity, DefinitionMapping, DefinitionExtension, DefinitionBulkExtension},
		DefinitionKindsForVersion("6.6.10"),
	)
}

func TestEnumFieldVersionSupport(t *testing.T) {
	require.False(t, EnumFieldSupported("6.6.9"))
	require.True(t, EnumFieldSupported("6.6.10"))
	require.True(t, EnumFieldSupported("~6.7"))
	require.True(t, EnumFieldSupported(""))
}
