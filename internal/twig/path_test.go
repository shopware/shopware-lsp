package twig

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestTemplateNamesForStorefrontDist(t *testing.T) {
	// Shopware's TwigLoaderConfigCompilerPass registers each bundle's
	// Resources/app/storefront/dist directory under the bundle namespace.
	assert.Equal(
		t,
		[]string{"@Storefront/assets/icon/solid/arrow-medium-right.svg"},
		TemplateNames("/project/src/Storefront/Resources/app/storefront/dist/assets/icon/solid/arrow-medium-right.svg"),
	)
	assert.Equal(
		t,
		[]string{"@MyPlugin/assets/icon/check.svg"},
		TemplateNames("/project/custom/plugins/MyPlugin/src/Resources/app/storefront/dist/assets/icon/check.svg"),
	)
	assert.Equal(
		t,
		[]string{"@MyApp/assets/icon/check.svg"},
		TemplateNames("/project/custom/apps/MyApp/Resources/app/storefront/dist/assets/icon/check.svg"),
	)
	assert.Equal(
		t,
		"@Storefront/assets/icon/solid/arrow-medium-right.svg",
		ConvertToRelativePath("/project/src/Storefront/Resources/app/storefront/dist/assets/icon/solid/arrow-medium-right.svg"),
	)
}

func TestTemplateNamesOutsideTemplateRoots(t *testing.T) {
	// A Twig-syntax file no loader root contains has no template name;
	// deriving one from its absolute path would leak that path into
	// template suggestions.
	assert.Empty(t, TemplateNames(
		"/Users/example/project/custom/apps/MyApp/Resources/scripts/ping/ping.twig",
	))
	assert.Empty(t, TemplateNames("/project/page.html.twig"))
}

func TestIsTemplateAssetPath(t *testing.T) {
	assert.True(t, IsTemplateAssetPath(
		"/project/src/Core/Profiling/Resources/views/Collector/checkmark.svg",
	))
	assert.True(t, IsTemplateAssetPath("/project/templates/mail/logo.svg"))
	assert.True(t, IsTemplateAssetPath(
		"/project/src/Storefront/Resources/app/storefront/dist/assets/icon/check.svg",
	))
	assert.False(t, IsTemplateAssetPath(
		"/project/src/Storefront/Resources/app/storefront/dist/storefront/page.html.twig",
	))
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
