package systemconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
)

// SystemConfigEntry represents a system config entry with namespace
type SystemConfigEntry struct {
	Namespace string
	Name      string
	Label     string
	Type      string
	Component string
	FilePath  string
	Line      int
}

// GetNamespaceFromPath extracts the namespace from the file path by looking for composer.json or manifest.xml
func GetNamespaceFromPath(filePath string) (string, error) {
	// Get the directory of the file
	dir := filepath.Dir(filePath)

	// Get the file name without extension
	fileName := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))

	// Look for composer.json or manifest.xml in parent directories
	for {
		composerPath := filepath.Join(dir, "composer.json")
		manifestPath := filepath.Join(dir, "manifest.xml")

		// Check if composer.json exists
		if _, err := os.Stat(composerPath); err == nil {
			// Parse composer.json to get the namespace
			namespace, err := getNamespaceFromComposerJson(composerPath, fileName)
			if err != nil {
				return "", err
			}
			return namespace, nil
		}

		// Check if manifest.xml exists
		if _, err := os.Stat(manifestPath); err == nil {
			// Parse manifest.xml to get the namespace
			namespace, err := getNamespaceFromManifestXml(manifestPath)
			if err != nil {
				return "", err
			}
			return namespace, nil
		}

		// Move up one directory
		parentDir := filepath.Dir(dir)
		if parentDir == dir {
			// We've reached the root directory
			break
		}
		dir = parentDir
	}

	// If no composer.json or manifest.xml is found, use the file name as namespace
	return fmt.Sprintf("core.%s", fileName), nil
}

// getNamespaceFromComposerJson extracts the namespace from composer.json
func getNamespaceFromComposerJson(composerPath, fileName string) (string, error) {
	// Read composer.json
	data, err := os.ReadFile(composerPath)
	if err != nil {
		return "", err
	}

	// Parse composer.json
	var composer struct {
		Name  string `json:"name"`
		Extra struct {
			ShopwarePluginClass string `json:"shopware-plugin-class"`
		} `json:"extra"`
	}
	if err := json.Unmarshal(data, &composer); err != nil {
		return "", err
	}

	if composer.Extra.ShopwarePluginClass == "" {
		return fmt.Sprintf("core.%s", fileName), nil
	}

	// Extract the plugin name from the shopware-plugin-class
	parts := strings.Split(composer.Extra.ShopwarePluginClass, "\\")
	pluginName := parts[len(parts)-1]

	return pluginName + ".config", nil
}

// getNamespaceFromManifestXml extracts the namespace from manifest.xml
func getNamespaceFromManifestXml(manifestPath string) (string, error) {
	// Read manifest.xml
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", err
	}

	tree := xmlparser.Parse(string(data)).Tree
	names := xmlquery.Elements(tree.Root, "name")
	if len(names) == 0 {
		return "", nil
	}

	namespace := strings.TrimSpace(xmlquery.TextContent(names[0]))
	if namespace == "" {
		return "", nil
	}

	return namespace, nil
}

// IndexSystemConfigFile indexes a system config file and returns the entries
func IndexSystemConfigFile(filePath string, data []byte) ([]SystemConfigEntry, error) {
	if !IsSystemConfigXML(data) {
		return nil, fmt.Errorf("not a system config file")
	}

	tree := xmlparser.Parse(string(data)).Tree
	return IndexSystemConfigTree(filePath, tree, xmlsyntax.NewLineIndex(tree.Source))
}

func IndexSystemConfigTree(filePath string, tree *xmlsyntax.Tree, lineIndex *xmlsyntax.LineIndex) ([]SystemConfigEntry, error) {
	if tree == nil || tree.Root == nil || !strings.Contains(tree.Source, "SystemConfig/Schema/config.xsd") {
		return nil, fmt.Errorf("not a system config file")
	}

	namespace, err := GetNamespaceFromPath(filePath)
	if err != nil {
		return nil, err
	}

	fields := findAllSystemConfigFields(tree.Root, nil, filePath, lineIndex)

	// Create entries with namespace
	entries := make([]SystemConfigEntry, 0, len(fields))
	for _, field := range fields {
		entries = append(entries, SystemConfigEntry{
			Namespace: namespace,
			Name:      fmt.Sprintf("%s.%s", namespace, field.Name),
			Label:     field.Label,
			Type:      field.Type,
			Component: field.Component,
			FilePath:  filePath,
			Line:      int(field.Line),
		})
	}

	return entries, nil
}
