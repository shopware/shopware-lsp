package twig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertToRelativePath(t *testing.T) {
	assert.Equal(t, "", ConvertToRelativePath(""))
	assert.Equal(t, "", ConvertToRelativePath("/"))
	assert.Equal(t, "", ConvertToRelativePath("/Resources/views"))
	assert.Equal(t, "", ConvertToRelativePath("/Resources/views/"))
	assert.Equal(t, "@Storefront/storefront/base.html.twig", ConvertToRelativePath("/Resources/views/storefront/base.html.twig"))
}

func TestGetBundleNameByPath(t *testing.T) {
	assert.Equal(t, "foo", getBundleNameByPath("foo/Resources/views/storefront/base.html.twig"))
	assert.Equal(t, "storefront", getBundleNameByPath("vendor/shopware/storefront/Resources/views/storefront/base.html.twig"))
	assert.Equal(t, "MyFoo", getBundleNameByPath("vendor/store.shopware.com/MyFoo/src/Resources/views/storefront/base.html.twig"))
}

func TestTemplateNames(t *testing.T) {
	assert.Equal(t, []string{"base.html.twig"}, TemplateNames("/project/templates/base.html.twig"))
	assert.Equal(
		t,
		[]string{
			"card.html.twig",
			"@Storefront/card.html.twig",
			"@MyBundle/card.html.twig",
			"MyBundle::card.html.twig",
		},
		TemplateNames("/project/MyBundle/src/Resources/views/card.html.twig"),
	)
}

func writePluginComposer(t *testing.T, root, pluginClass string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(root, 0o755))
	manifest := `{"name":"store.shopware.com/swagcmsextensions","type":"shopware-platform-plugin"}`
	if pluginClass != "" {
		encoded, err := json.Marshal(pluginClass)
		require.NoError(t, err)
		manifest = `{"name":"store.shopware.com/swagcmsextensions","type":"shopware-platform-plugin",` +
			`"extra":{"shopware-plugin-class":` + string(encoded) + `}}`
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(manifest), 0o644))
}

func TestTemplateNamesUseComposerPluginClass(t *testing.T) {
	project := filepath.ToSlash(t.TempDir())
	storePlugin := project + "/vendor/store.shopware.com/swagcmsextensions"
	writePluginComposer(t, storePlugin, `Swag\CmsExtensions\SwagCmsExtensions`)

	names := TemplateNames(storePlugin + "/src/Resources/views/storefront/element/form.html.twig")
	assert.Contains(t, names, "@SwagCmsExtensions/storefront/element/form.html.twig")
	assert.Contains(t, names, "@Storefront/storefront/element/form.html.twig")
	assert.NotContains(t, names, "@swagcmsextensions/storefront/element/form.html.twig")

	// A checkout whose directory differs from the bundle class.
	customPlugin := project + "/custom/plugins/my-plugin"
	writePluginComposer(t, customPlugin, `\Acme\MyPlugin\AcmeMyPlugin`)
	assert.Contains(
		t,
		TemplateNames(customPlugin+"/src/Resources/views/storefront/base.html.twig"),
		"@AcmeMyPlugin/storefront/base.html.twig",
	)
}

func TestTemplateNamesFallBackToDirectoryWithoutPluginClass(t *testing.T) {
	project := filepath.ToSlash(t.TempDir())
	plugin := project + "/vendor/acme/MyBundle"
	writePluginComposer(t, plugin, "")

	assert.Contains(
		t,
		TemplateNames(plugin+"/src/Resources/views/card.html.twig"),
		"@MyBundle/card.html.twig",
	)
}

func TestTemplateNamesFollowComposerChanges(t *testing.T) {
	plugin := filepath.ToSlash(t.TempDir()) + "/swagcmsextensions"
	template := plugin + "/src/Resources/views/storefront/base.html.twig"
	writePluginComposer(t, plugin, `Swag\CmsExtensions\SwagCmsExtensions`)
	assert.Contains(t, TemplateNames(template), "@SwagCmsExtensions/storefront/base.html.twig")

	writePluginComposer(t, plugin, `Swag\CmsExtensions\SwagCmsExtensionsRenamedBundle`)
	assert.Contains(t, TemplateNames(template), "@SwagCmsExtensionsRenamedBundle/storefront/base.html.twig")

	require.NoError(t, os.Remove(filepath.Join(plugin, "composer.json")))
	assert.Contains(t, TemplateNames(template), "@swagcmsextensions/storefront/base.html.twig")
}

func TestIsTemplateAssetPath(t *testing.T) {
	assert.True(t, IsTemplateAssetPath(
		"/project/src/Core/Profiling/Resources/views/Collector/checkmark.svg",
	))
	assert.True(t, IsTemplateAssetPath("/project/templates/mail/logo.svg"))
	// Twig files take the full indexing path instead.
	assert.False(t, IsTemplateAssetPath(
		"/project/src/Storefront/Resources/views/storefront/base.html.twig",
	))
	// Not below a template root, so no loader can address it.
	assert.False(t, IsTemplateAssetPath("/project/public/bundles/storefront/logo.svg"))
	assert.False(t, IsTemplateAssetPath(
		"/project/src/Administration/Resources/app/administration/src/icon.svg",
	))
	assert.False(t, IsTemplateAssetPath("/project/templates/.gitignore"))
	// Shopware symlinks node_modules into a template root, and os.ReadDir
	// reports the link as a file.
	assert.False(t, IsTemplateAssetPath(
		"/project/src/Storefront/Resources/views/components/node_modules",
	))
}
