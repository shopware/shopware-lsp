package twig

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBundleNamespace(t *testing.T) {
	assert.Equal(t, "WebProfiler", BundleNamespace(`Symfony\Bundle\WebProfilerBundle\WebProfilerBundle`))
	assert.Equal(t, "FOSUser", BundleNamespace(`FOS\UserBundle\FOSUserBundle`))
	assert.Equal(t, "Administration", BundleNamespace(`Shopware\Administration\Administration`))
	assert.Equal(t, "Storefront", BundleNamespace(`Shopware\Storefront\Storefront`))
	assert.Equal(t, "", BundleNamespace(`Symfony\Component\HttpKernel\Bundle\Bundle`))
}

// bundleNamespaceFixture indexes templates before their bundle classes, so
// resolution can only succeed at query time.
func bundleNamespaceFixture(t *testing.T) *TwigIndexer {
	t.Helper()
	cacheDir := t.TempDir()
	phpIndex, err := php.NewPHPIndex(cacheDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })

	idx, err := NewTwigIndexer(cacheDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	idx.SetDependencies(phpIndex, nil)

	templates := map[string]string{
		"/project/vendor/symfony/web-profiler-bundle/Resources/views/Profiler/layout.html.twig": "{% macro dump() %}x{% endmacro %}layout",
		"/project/vendor/friendsofsymfony/user-bundle/Resources/views/layout.html.twig":         "user layout",
		"/project/vendor/shopware/administration/src/Resources/views/main.html.twig":            "admin",
		"/project/custom/plugins/AcmeShop/src/Resources/views/storefront/card.html.twig":        "card",
		"/project/vendor/symfony/twig-bundle/templates/error.html.twig":                         "error",
		"/project/vendor/acme/plain-bundle/Resources/views/plain.html.twig":                     "plain",
	}
	for path, content := range templates {
		require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(content))))
	}

	bundles := map[string]string{
		"/project/vendor/symfony/http-kernel/Bundle/Bundle.php": `<?php
namespace Symfony\Component\HttpKernel\Bundle;
abstract class Bundle {}
`,
		"/project/vendor/symfony/http-kernel/Bundle/AbstractBundle.php": `<?php
namespace Symfony\Component\HttpKernel\Bundle;
abstract class AbstractBundle {}
`,
		"/project/vendor/shopware/core/Framework/Bundle.php": `<?php
namespace Shopware\Core\Framework;
use Symfony\Component\HttpKernel\Bundle\Bundle as SymfonyBundle;
abstract class Bundle extends SymfonyBundle {}
`,
		"/project/vendor/symfony/web-profiler-bundle/WebProfilerBundle.php": `<?php
namespace Symfony\Bundle\WebProfilerBundle;
use Symfony\Component\HttpKernel\Bundle\Bundle;
class WebProfilerBundle extends Bundle {}
`,
		"/project/vendor/friendsofsymfony/user-bundle/FOSUserBundle.php": `<?php
namespace FOS\UserBundle;
use Symfony\Component\HttpKernel\Bundle\Bundle;
class FOSUserBundle extends Bundle {}
`,
		"/project/vendor/shopware/administration/src/Administration.php": `<?php
namespace Shopware\Administration;
use Shopware\Core\Framework\Bundle;
class Administration extends Bundle {}
`,
		"/project/custom/plugins/AcmeShop/src/AcmeShopBundle.php": `<?php
namespace Acme\AcmeShop;
use Symfony\Component\HttpKernel\Bundle\AbstractBundle;
class AcmeShopBundle extends AbstractBundle {}
`,
		"/project/vendor/symfony/twig-bundle/TwigBundle.php": `<?php
namespace Symfony\Bundle\TwigBundle;
use Symfony\Component\HttpKernel\Bundle\Bundle;
class TwigBundle extends Bundle {}
`,
		// A class named like a bundle that is not one must not invent a
		// namespace for the templates below its directory.
		"/project/vendor/acme/plain-bundle/PlainBundle.php": `<?php
namespace Acme\Plain;
class PlainBundle {}
`,
	}
	for path, content := range bundles {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(path, []byte(content))))
	}
	return idx
}

func TestTwigIndexerResolvesVendorBundleNamespacesFromBundleClasses(t *testing.T) {
	idx := bundleNamespaceFixture(t)

	for name, wantPath := range map[string]string{
		"@WebProfiler/Profiler/layout.html.twig": "/project/vendor/symfony/web-profiler-bundle/Resources/views/Profiler/layout.html.twig",
		"@FOSUser/layout.html.twig":              "/project/vendor/friendsofsymfony/user-bundle/Resources/views/layout.html.twig",
		"@Administration/main.html.twig":         "/project/vendor/shopware/administration/src/Resources/views/main.html.twig",
		"@AcmeShop/storefront/card.html.twig":    "/project/custom/plugins/AcmeShop/src/Resources/views/storefront/card.html.twig",
		"@Twig/error.html.twig":                  "/project/vendor/symfony/twig-bundle/templates/error.html.twig",
	} {
		files, err := idx.GetTwigFilesByRelPath(name)
		require.NoError(t, err)
		require.Len(t, files, 1, name)
		assert.Equal(t, wantPath, files[0].Path, name)
	}

	// Namespaces no bundle class declares resolve to nothing, even when a
	// spelling rule applied to the directory would invent one, and even
	// for a relative path that does exist below that directory.
	for _, name := range []string{
		"@User/layout.html.twig",
		"@Plain/plain.html.twig",
		"@WebProfiler/Profiler/missing.html.twig",
		"@NoSuchBundle/layout.html.twig",
	} {
		files, err := idx.GetTwigFilesByRelPath(name)
		require.NoError(t, err)
		assert.Empty(t, files, name)
	}

	macros, err := idx.GetMacros("@WebProfiler/Profiler/layout.html.twig")
	require.NoError(t, err)
	require.Len(t, macros, 1)
	assert.Equal(t, "dump", macros[0].Name)
}

func TestTwigIndexerListsAuthoritativeBundleTemplateNames(t *testing.T) {
	idx := bundleNamespaceFixture(t)

	names, err := idx.GetAllTemplateFiles()
	require.NoError(t, err)
	assert.Contains(t, names, "@WebProfiler/Profiler/layout.html.twig")
	assert.Contains(t, names, "@FOSUser/layout.html.twig")
	assert.Contains(t, names, "@Administration/main.html.twig")
	assert.Contains(t, names, "@Twig/error.html.twig")
	// Directory-derived spellings are not authoritative names and must not
	// be offered once the bundle class proves the real namespace.
	assert.NotContains(t, names, "@web-profiler-bundle/Profiler/layout.html.twig")
	assert.NotContains(t, names, "@user-bundle/layout.html.twig")
	assert.NotContains(t, names, "@administration/main.html.twig")
	assert.NotContains(t, names, "@User/layout.html.twig")
}
