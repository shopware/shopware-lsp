package projectdetect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetectProjectKinds(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		kind  Kind
	}{
		{name: "unknown PHP", files: map[string]string{"src/Test.php": "<?php"}, kind: KindUnknown},
		{name: "configured", files: map[string]string{".config/shopware/lsp.json": `{"version":1}`}, kind: KindConfigured},
		{name: "platform", files: map[string]string{"composer.json": `{"name":"shopware/platform"}`}, kind: KindShopware},
		{name: "production package", files: map[string]string{"composer.json": `{"name":"shopware/production"}`}, kind: KindShopware},
		{name: "plugin type", files: map[string]string{"composer.json": `{"type":"shopware-platform-plugin"}`}, kind: KindShopware},
		{name: "plugin class", files: map[string]string{"composer.json": `{"extra":{"shopware-plugin-class":"Acme\\Plugin"}}`}, kind: KindShopware},
		{name: "Shopware dependency", files: map[string]string{"composer.json": `{"require-dev":{"shopware/core":"*"}}`}, kind: KindShopware},
		{name: "Shopware lock", files: map[string]string{"composer.json": `{}`, "composer.lock": `{"packages":[{"name":"shopware/core"}]}`}, kind: KindShopware},
		{name: "Shopware app", files: map[string]string{"manifest.xml": `<manifest xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/platform/trunk/src/Core/Framework/App/Manifest/Schema/manifest-3.0.xsd"></manifest>`}, kind: KindShopware},
		{name: "Symfony dependency", files: map[string]string{"composer.json": `{"require":{"symfony/framework-bundle":"*"}}`}, kind: KindSymfony},
		{name: "Symfony lock", files: map[string]string{"composer.lock": `{"packages-dev":[{"name":"symfony/framework-bundle"}]}`}, kind: KindSymfony},
		{name: "Symfony bundles", files: map[string]string{"config/bundles.php": `<?php return [Symfony\Bundle\FrameworkBundle\FrameworkBundle::class => ['all' => true]];`}, kind: KindSymfony},
		{name: "Symfony components only", files: map[string]string{"composer.json": `{"require":{"symfony/console":"*","symfony/http-kernel":"*"}}`}, kind: KindUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeFiles(t, root, test.files)
			result, err := Detect(root)
			require.NoError(t, err)
			require.Equal(t, test.kind, result.Kind)
			require.Equal(t, test.kind != KindUnknown, result.Supported)
			if result.Supported {
				require.NotEmpty(t, result.Evidence)
			}
		})
	}
}

func TestDetectPrefersShopwareOverSymfony(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"composer.json": `{"require":{"shopware/core":"*","symfony/framework-bundle":"*"}}`,
	})
	result, err := Detect(root)
	require.NoError(t, err)
	require.Equal(t, KindShopware, result.Kind)
}

func TestDetectRejectsMalformedComposerMetadata(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"composer.json": `{`})
	_, err := Detect(root)
	require.ErrorContains(t, err, "parse composer.json")

	root = t.TempDir()
	writeFiles(t, root, map[string]string{"composer.lock": `{`})
	_, err = Detect(root)
	require.ErrorContains(t, err, "parse composer.lock")
}

func TestExplicitConfigurationStillOptsIntoMalformedProjectMetadata(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".config/shopware/lsp.json": `{"version":1}`,
		"composer.json":             `{`,
	})
	result, err := Detect(root)
	require.NoError(t, err)
	require.Equal(t, KindConfigured, result.Kind)
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
}
