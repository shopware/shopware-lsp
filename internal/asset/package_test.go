package asset

import (
	"path/filepath"
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackagesInXMLCollectTagsAndInferBundlePackages(t *testing.T) {
	path := filepath.Join(
		"/project",
		"src",
		"Administration",
		"Resources",
		"config",
		"services.xml",
	)
	source := `<container><services>
<service id="assets.theme">
  <tag name="assets.package" package="theme"/>
</service>
</services></container>`
	packages := PackagesInXML(path, xmlparser.Parse(source).Tree.Root)
	require.Len(t, packages, 2)
	assert.Equal(t, "@Administration", packages[0].Name)
	assert.Equal(t, "bundles/administration", packages[0].BasePath)
	assert.True(t, packages[0].Inferred)
	assert.Equal(t, "theme", packages[1].Name)
	assert.False(t, packages[1].Inferred)
	assert.NotZero(t, packages[1].Range.Len())
}

func TestPackagesInPHPCollectSymfonyAndShopwarePackageTags(t *testing.T) {
	source := `<?php
$services->set('theme', ThemePackage::class)
    ->tag('shopware.asset', ['asset' => 'theme']);
$services->set('uploads', Package::class)
    ->tag('assets.package', ['package' => 'uploads']);
$services->set('dynamic', Package::class)
    ->tag('assets.package', ['package' => $name]);
`
	packages := PackagesInPHP(
		"/project/src/Storefront/DependencyInjection/theme.php",
		phpparser.Parse(source).Tree.Root,
	)
	require.Len(t, packages, 2)
	assert.Equal(t, "theme", packages[0].Name)
	assert.Equal(t, "uploads", packages[1].Name)
	assert.NotZero(t, packages[0].Range.Len())
}

func TestPackagesInYAMLCollectBasePathsAndIgnoreDynamicPaths(t *testing.T) {
	path := filepath.Join(
		"/project",
		"config",
		"packages",
		"framework.yaml",
	)
	source := `framework:
  assets:
    packages:
      uploads:
        base_path: /uploads
      remote:
        base_path: '%env(CDN_PATH)%'
`
	packages := PackagesInYAML(path, yamlparser.Parse(source).Tree.Root)
	require.Len(t, packages, 2)
	assert.Equal(t, "remote", packages[0].Name)
	assert.Empty(t, packages[0].BasePath)
	assert.Equal(t, "uploads", packages[1].Name)
	assert.Equal(t, "uploads", packages[1].BasePath)
	assert.NotZero(t, packages[1].Range.Len())
}
