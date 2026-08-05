package systemconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetNamespaceFromPath(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Test with composer.json
	t.Run("with composer.json", func(t *testing.T) {
		// Create a test directory structure
		pluginDir := filepath.Join(tempDir, "plugin")
		configDir := filepath.Join(pluginDir, "Resources", "config")
		require.NoError(t, os.MkdirAll(configDir, 0755))

		// Create a composer.json file
		composerJson := `{
			"name": "shopware/plugin-name",
			"extra": {
				"shopware-plugin-class": "Shopware\\PluginName\\PluginName"
			}
		}`
		require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "composer.json"), []byte(composerJson), 0644))

		// Create a config file
		configFile := filepath.Join(configDir, "config.xml")
		require.NoError(t, os.WriteFile(configFile, []byte("<config></config>"), 0644))

		// Test namespace extraction
		namespace, err := GetNamespaceFromPath(configFile)
		require.NoError(t, err)
		assert.Equal(t, "PluginName.config", namespace)
	})

	// Test with manifest.xml
	t.Run("with manifest.xml", func(t *testing.T) {
		// Create a test directory structure
		pluginDir := filepath.Join(tempDir, "plugin2")
		configDir := filepath.Join(pluginDir, "Resources", "config")
		require.NoError(t, os.MkdirAll(configDir, 0755))

		// Create a manifest.xml file
		manifestXml := `<?xml version="1.0" encoding="UTF-8"?>
		<manifest xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
				  xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/shopware/master/src/Core/Framework/App/Manifest/Schema/manifest-1.0.xsd">
			<meta>
				<name>MyApp</name>
			</meta>
		</manifest>`
		require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "manifest.xml"), []byte(manifestXml), 0644))

		// Create a config file
		configFile := filepath.Join(configDir, "config.xml")
		require.NoError(t, os.WriteFile(configFile, []byte("<config></config>"), 0644))

		// Test namespace extraction
		namespace, err := GetNamespaceFromPath(configFile)
		require.NoError(t, err)
		assert.Equal(t, "MyApp", namespace)
	})

	// Test with no composer.json or manifest.xml
	t.Run("with no composer.json or manifest.xml", func(t *testing.T) {
		// Create a test directory structure
		configDir := filepath.Join(tempDir, "standalone")
		require.NoError(t, os.MkdirAll(configDir, 0755))

		// Create a config file
		configFile := filepath.Join(configDir, "custom.xml")
		require.NoError(t, os.WriteFile(configFile, []byte("<config></config>"), 0644))

		// Test namespace extraction
		namespace, err := GetNamespaceFromPath(configFile)
		require.NoError(t, err)
		assert.Equal(t, "core.custom", namespace)
	})
}

func TestIndexSystemConfigFile(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create a test directory structure
	pluginDir := filepath.Join(tempDir, "plugin")
	configDir := filepath.Join(pluginDir, "Resources", "config")
	require.NoError(t, os.MkdirAll(configDir, 0755))

	// Create a composer.json file
	composerJson := `{
		"name": "shopware/test-plugin",
		"extra": {
			"shopware-plugin-class": "Shopware\\TestPlugin\\TestPlugin"
		}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "composer.json"), []byte(composerJson), 0644))

	// Create a config file
	configXml := `<?xml version="1.0" encoding="UTF-8" ?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/shopware/master/src/Core/System/SystemConfig/Schema/config.xsd">
	<card>
		<input-field type="text">
			<name>textField</name>
			<label>Text Field</label>
		</input-field>
		<component name="custom-component">
			<name>customComponent</name>
			<label>Custom Component</label>
		</component>
	</card>
</config>`
	configFile := filepath.Join(configDir, "config.xml")
	require.NoError(t, os.WriteFile(configFile, []byte(configXml), 0644))

	// Test indexing
	fileContent, err := os.ReadFile(configFile)
	require.NoError(t, err)

	entries, err := IndexSystemConfigFile(configFile, fileContent)
	require.NoError(t, err)
	assert.Equal(t, 2, len(entries))

	// Check the first entry (textField)
	assert.Equal(t, "TestPlugin.config", entries[0].Namespace)
	assert.Equal(t, "TestPlugin.config.textField", entries[0].Name)
	assert.Equal(t, "Text Field", entries[0].Label)
	assert.Equal(t, "text", entries[0].Type)
	assert.Equal(t, "", entries[0].Component)
	assert.Equal(t, configFile, entries[0].FilePath)

	// Check the second entry (customComponent)
	assert.Equal(t, "TestPlugin.config", entries[1].Namespace)
	assert.Equal(t, "TestPlugin.config.customComponent", entries[1].Name)
	assert.Equal(t, "Custom Component", entries[1].Label)
	assert.Equal(t, "", entries[1].Type)
	assert.Equal(t, "custom-component", entries[1].Component)
	assert.Equal(t, configFile, entries[1].FilePath)
}
