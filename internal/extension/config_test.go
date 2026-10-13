package extension

import (
	"path/filepath"
	"testing"

	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestParseEntireExtensionConfig(t *testing.T) {
	const source = `compatibility_date: "2026-09-01"
build:
  extraBundles:
    - path: src/Extra
      name: Extra
  shopwareVersionConstraint: "~6.7"
  zip:
    composer:
      enabled: false
      before_hooks: ["echo before"]
      after_hooks: ["echo after"]
      excluded_packages: [acme/dev]
    assets:
      enabled: true
      enable_asset_caching: true
      enable_es_build_for_admin: true
      enable_es_build_for_storefront: true
      disable_sass: true
      npm_strict: true
      before_hooks: ["npm run prepare"]
      after_hooks: ["npm run cleanup"]
      additional_caches:
        - path: public/built
          source_paths: [src/source]
    pack:
      excludes:
        paths: [tests, docs]
      before_hooks: ["echo pack"]
    checksum:
      ignore: [README.md]
validation:
  store_compliance: true
  php_version: "8.4"
  ignore:
    - php.lint
    - identifier: phpstan
      path: src/Example.php
      message: "Known issue"
changelog:
  enabled: true
  pattern: "^feat:"
  template: |-
    ## Changes
    {{ .Message }}
  variables: {ticket: "[A-Z]+-\\d+"}
store:
  availabilities: [German, International]
  default_locale: de_DE
  localizations: [de_DE, en_GB]
  type: extension
  icon: src/Resources/icon.png
  automatic_bugfix_version_compatibility: false
  meta_title: {de: "Titel", en: "Title"}
  meta_description: {de: "Beschreibung", en: "Description"}
  description:
    en: |
      First line
      Second line
  installation_manual: {en: "Install it"}
  tags: {en: [shop, catalog]}
  videos: {en: [https://example.com/video]}
  highlights: {en: [Fast]}
  features: {en: [Search]}
  faq:
    en:
      - question: How?
        answer: Easily
        position: 2
  images:
    - file: screenshots/product.png
      activate: {de: true, en: true}
      preview: {de: false, en: true}
      position: 1
      priority: 2
  image_directory: screenshots
  demo_shops:
    - type: frontend
      link: https://example.com
      localization: en_GB
      login_name: demo
      login_password: example
`
	tree := yamlparser.Parse(source).Tree
	actual := ParseConfigTree("/plugin/.config/shopware-extension.yml", tree.Root)
	expected := DefaultConfig()
	require.NoError(t, yaml.Unmarshal([]byte(source), &expected)) // independent test oracle only
	expected.StorageLocation = actual.StorageLocation
	require.Empty(t, actual.DecodeError)
	assert.Equal(t, expected, actual)
	assert.Equal(t, "8.4", actual.Validation.PhpVersion)
	assert.Equal(t, "src/Extra", actual.Build.ExtraBundles[0].Path)
	require.NotNil(t, actual.Store.Description.English)
	assert.Equal(t, "First line\nSecond line\n", *actual.Store.Description.English)
}

func TestExtensionConfigurationLocations(t *testing.T) {
	root := t.TempDir()
	for _, location := range ConfigLocations {
		resolved, ok := ConfigurationRoot(filepath.Join(root, location))
		assert.True(t, ok)
		assert.Equal(t, root, resolved)
	}
	for _, location := range []string{"shopware-extension.yml", ".config/shopware-extension.yaml", "other/shopware-extension.yml"} {
		_, ok := ConfigurationRoot(filepath.Join(root, location))
		assert.False(t, ok, location)
	}
}

func TestParseExtensionConfigRetainsPartialFields(t *testing.T) {
	config := ParseConfigTree("/plugin/.shopware-extension.yml", yamlparser.Parse("build:\n  shopwareVersionConstraint: '~6.7'\nvalidation:\n  store_compliance: \"true\"\n").Tree.Root)
	assert.NotEmpty(t, config.DecodeError)
	assert.Equal(t, "~6.7", config.Build.ShopwareVersionConstraint)
	assert.False(t, config.Validation.StoreCompliance)
	assert.True(t, config.Build.Zip.Assets.Enabled)
	config = ParseConfigTree("/plugin/.shopware-extension.yml", yamlparser.Parse("validation:\n  store_compliance: true\nbuild:\n  extraBundles: [").Tree.Root)
	assert.True(t, config.Validation.StoreCompliance)
}
