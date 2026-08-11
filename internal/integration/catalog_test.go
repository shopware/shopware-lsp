package integration

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCatalogIsStableAndUnique(t *testing.T) {
	catalog := CurrentCatalog()
	require.Equal(t, ProtocolVersion, catalog.ProtocolVersion)
	require.NotEmpty(t, catalog.ClientCommands)
	require.NotEmpty(t, catalog.Scaffolds)

	commands := make(map[string]struct{}, len(catalog.ClientCommands))
	for _, command := range catalog.ClientCommands {
		require.NotEmpty(t, command.ID)
		_, duplicate := commands[command.ID]
		require.False(t, duplicate, command.ID)
		commands[command.ID] = struct{}{}
	}
	require.Contains(t, commands, "shopware.admin.extendComponent")
	require.Contains(t, commands, "shopware.twig.extendBlock")

	scaffolds := make(map[string]struct{}, len(catalog.Scaffolds))
	for _, scaffold := range catalog.Scaffolds {
		key := scaffold.Family + ":" + scaffold.Kind
		_, duplicate := scaffolds[key]
		require.False(t, duplicate, key)
		scaffolds[key] = struct{}{}
	}
	require.Contains(t, scaffolds, "shopware:entity-definition")
	require.Contains(t, scaffolds, "shopware:plugin")
	require.Contains(t, scaffolds, "symfony:controller")
}
