package symfony

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteResourceResolverResolvesLegacyBundleResources(
	t *testing.T,
) {
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	indexRouteResourceBundleFixture(
		t,
		phpIndex,
		filepath.Join(root, "vendor", "acme", "foo-bundle"),
		"Acme\\Foo",
		"FooBundle",
	)
	bundleRoot := filepath.Join(root, "vendor", "acme", "foo-bundle")
	routePath := filepath.Join(
		bundleRoot,
		"Resources",
		"config",
		"routes.xml",
	)
	controllerPath := filepath.Join(
		bundleRoot,
		"Controller",
		"CatalogController.php",
	)
	for path, source := range map[string]string{
		routePath:      "<routes/>",
		controllerPath: "<?php final class CatalogController {}",
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}

	resolver := NewRouteResourceResolver(phpIndex)
	assert.Equal(t, []string{routePath}, resolver.Files(
		"/project/config/routes.xml",
		RouteResourceReference{
			Path: `@FooBundle\\Resources//config/routes.xml`,
		},
	))
	assert.Equal(t, []string{controllerPath}, resolver.Files(
		"/project/config/routes.yaml",
		RouteResourceReference{
			Path:   "@FooBundle/Controller/",
			Loader: "attribute",
		},
	))
	assert.True(t, resolver.Matches(
		"/project/config/routes.yaml",
		controllerPath,
		RouteResourceReference{
			Path:   "@FooBundle/Controller/**/*.php",
			Loader: "attribute",
		},
	))
	candidates := resolver.BundleResourceCandidates(context.Background())
	assert.Contains(t, candidates, BundleResourceCandidate{
		Value: "@FooBundle/Resources/config/routes.xml",
		Path:  routePath,
	})
	assert.Contains(t, candidates, BundleResourceCandidate{
		Value: "@FooBundle/Controller/CatalogController.php",
		Path:  controllerPath,
	})
}

func TestRouteResourceResolverRefreshesBundleCatalogByPHPRevision(
	t *testing.T,
) {
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	firstRoot := filepath.Join(root, "first")
	indexRouteResourceBundleFixture(
		t,
		phpIndex,
		firstRoot,
		"First",
		"SharedBundle",
	)
	firstRoute := filepath.Join(firstRoot, "routes.xml")
	require.NoError(t, os.WriteFile(firstRoute, []byte("<routes/>"), 0o644))

	resolver := NewRouteResourceResolver(phpIndex)
	reference := RouteResourceReference{
		Path: "@SharedBundle/routes.xml",
	}
	assert.Equal(t, []string{firstRoute}, resolver.Files(
		"/project/config/routes.xml",
		reference,
	))

	secondRoot := filepath.Join(root, "second")
	indexRouteResourceBundleFixture(
		t,
		phpIndex,
		secondRoot,
		"Second",
		"SharedBundle",
	)
	secondRoute := filepath.Join(secondRoot, "routes.xml")
	require.NoError(t, os.WriteFile(secondRoute, []byte("<routes/>"), 0o644))
	assert.Equal(t, []string{firstRoute, secondRoute}, resolver.Files(
		"/project/config/routes.xml",
		reference,
	))
}

func TestRouteBundleResourcePartsRejectsIncompleteNames(t *testing.T) {
	for _, resource := range []string{
		"",
		"FooBundle/routes.xml",
		"@FooBundle",
		"@/routes.xml",
	} {
		_, _, found := routeBundleResourceParts(resource)
		assert.False(t, found, resource)
	}
	name, relative, found := routeBundleResourceParts(
		`@FooBundle///Resources\config/routes.xml`,
	)
	require.True(t, found)
	assert.Equal(t, "FooBundle", name)
	assert.Equal(t, "Resources/config/routes.xml", relative)
}

func indexRouteResourceBundleFixture(
	t *testing.T,
	phpIndex *php.PHPIndex,
	root,
	namespace,
	name string,
) {
	t.Helper()
	interfacePath := filepath.Join(
		filepath.Dir(root),
		"BundleInterface.php",
	)
	interfaceSource := `<?php
namespace Symfony\Component\HttpKernel\Bundle;
interface BundleInterface {}
`
	require.NoError(t, os.MkdirAll(filepath.Dir(interfacePath), 0o755))
	require.NoError(t, os.WriteFile(
		interfacePath,
		[]byte(interfaceSource),
		0o644,
	))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		interfacePath,
		[]byte(interfaceSource),
	)))

	bundlePath := filepath.Join(root, name+".php")
	bundleSource := "<?php\nnamespace " + namespace + ";\nfinal class " +
		name +
		" implements \\Symfony\\Component\\HttpKernel\\Bundle\\BundleInterface {}\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(bundlePath), 0o755))
	require.NoError(t, os.WriteFile(
		bundlePath,
		[]byte(bundleSource),
		0o644,
	))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		bundlePath,
		[]byte(bundleSource),
	)))
}
