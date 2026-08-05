package analytics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceLocatorFindsIDsClassesAliasesPrototypesAndCache(
	t *testing.T,
) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	classPath := filepath.Join(root, "src", "Services.php")
	classSource := `<?php
namespace App;

class Service {}
class Other {}
`
	configPath := filepath.Join(root, "config", "services.yaml")
	configSource := `services:
  _defaults:
    autowire: true

  'App\':
    resource: '../src'

  app.explicit:
    class: App\Service
    tags: [app.catalog]

  app.alias: '@app.explicit'
`
	for path, source := range map[string]string{
		classPath:  classSource,
		configPath: configSource,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}

	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		classPath,
		[]byte(classSource),
	)))
	serviceIndex, err := symfony.NewServiceIndex(root, cache)
	require.NoError(t, err)
	serviceIndex.SetPHPIndex(phpIndex)
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		configPath,
		[]byte(configSource),
	)))
	provider := NewServiceLocatorProvider(serviceIndex, phpIndex)

	classRequest := ServiceLocatorRequest{Identifier: `\App\Service`}
	entries, err := provider.Locate(context.Background(), classRequest)
	require.NoError(t, err)
	require.Len(t, entries, 3)

	explicit := serviceLocatorEntry(t, entries, "app.explicit")
	assert.Equal(t, "App\\Service", explicit.ClassName)
	assert.Equal(t, "App\\Service", explicit.ResolvedClass)
	assert.True(t, explicit.Autowire)
	assert.True(t, explicit.AutowireConfigured)
	assert.Equal(t, []string{"app.catalog"}, explicit.Tags)
	assert.Equal(t, uriutil.FileURI(classPath), explicit.ClassFileURI)
	assert.Positive(t, explicit.ClassLine)
	require.Len(t, explicit.Definitions, 1)
	assert.Equal(t, "explicit", explicit.Definitions[0].Source)
	assert.Equal(t, uriutil.FileURI(configPath), explicit.Definitions[0].FileURI)
	assert.Positive(t, explicit.Definitions[0].SourceLine)
	assert.GreaterOrEqual(
		t,
		explicit.Definitions[0].EndLine,
		explicit.Definitions[0].SourceLine,
	)
	assert.Contains(t, explicit.Definitions[0].Preview, "app.explicit:")
	assert.Contains(t, explicit.Definitions[0].Preview, "tags:")

	alias := serviceLocatorEntry(t, entries, "app.alias")
	assert.Equal(t, "app.explicit", alias.AliasTarget)
	assert.Equal(t, "App\\Service", alias.ResolvedClass)
	require.Len(t, alias.Definitions, 1)
	assert.Equal(t, "explicit", alias.Definitions[0].Source)
	assert.Contains(t, alias.Definitions[0].Preview, "app.alias:")

	prototype := serviceLocatorEntry(t, entries, "App\\Service")
	assert.Equal(t, "App\\Service", prototype.ResolvedClass)
	require.Len(t, prototype.Definitions, 1)
	assert.Equal(t, "prototype", prototype.Definitions[0].Source)
	assert.Contains(t, prototype.Definitions[0].Preview, "'App\\':")
	assert.Contains(t, prototype.Definitions[0].Preview, "resource:")

	byID, err := provider.Locate(
		context.Background(),
		ServiceLocatorRequest{Identifier: "app.alias"},
	)
	require.NoError(t, err)
	require.Len(t, byID, 1)
	assert.Equal(t, alias, byID[0])

	_, err = provider.Locate(
		context.Background(),
		ServiceLocatorRequest{Identifier: "app.missing"},
	)
	assert.ErrorContains(t, err, "was not found")

	require.NoError(t, serviceIndex.Close())
	require.NoError(t, phpIndex.Close())
	restoredPHP, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	restoredServices, err := symfony.NewServiceIndex(root, cache)
	require.NoError(t, err)
	restoredServices.SetPHPIndex(restoredPHP)
	t.Cleanup(func() {
		require.NoError(t, restoredServices.Close())
		require.NoError(t, restoredPHP.Close())
	})
	restoredProvider := NewServiceLocatorProvider(
		restoredServices,
		restoredPHP,
	)
	restoredEntries, err := restoredProvider.Locate(
		context.Background(),
		classRequest,
	)
	require.NoError(t, err)
	assert.Equal(t, entries, restoredEntries)
}

func serviceLocatorEntry(
	t *testing.T,
	entries []ServiceLocatorEntry,
	id string,
) ServiceLocatorEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.ID == id {
			return entry
		}
	}
	t.Fatalf("service %q not found in %#v", id, entries)
	return ServiceLocatorEntry{}
}
