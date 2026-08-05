package appscript

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/require"
)

func TestIndexResolvesInheritedHookServicesAndDefaults(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	defer func() { _ = idx.Close() }()

	files := map[string]string{
		"/src/RepositoryFacadeHookFactory.php": `<?php
class RepositoryFacadeHookFactory extends HookServiceFactory {
    public function getName(): string { return 'repository'; }
}`,
		"/src/AclFacadeHookFactory.php": `<?php
class AclFacadeHookFactory extends HookServiceFactory {
    public function getName(): string { return 'acl'; }
}`,
		"/src/PageLoadedHook.php": `<?php
abstract class PageLoadedHook extends Hook {
    public static function getServiceIds(): array {
        return [RepositoryFacadeHookFactory::class];
    }
}`,
		"/src/ProductPageLoadedHook.php": `<?php
class ProductPageLoadedHook extends PageLoadedHook {
    public const HOOK_NAME = 'product-page-loaded';
    public function getName(): string { return self::HOOK_NAME; }
}`,
	}
	for path, source := range files {
		require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))
	}

	services, found, err := idx.ServicesForHook("product-page-loaded")
	require.NoError(t, err)
	require.True(t, found)
	require.Contains(t, services, "repository")
	require.Contains(t, services, "acl")

	_, found, err = idx.ServicesForHook("unknown")
	require.NoError(t, err)
	require.False(t, found)
}
