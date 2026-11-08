package theme

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/extension"
)

type IconProvider struct {
	projectRoot      string
	extensionIndexer *extension.ExtensionIndexer
}

func NewIconProvider(projectRoot string, extensionIndexer *extension.ExtensionIndexer) *IconProvider {
	return &IconProvider{
		projectRoot:      projectRoot,
		extensionIndexer: extensionIndexer,
	}
}

func (p *IconProvider) getExtensions() []extension.ShopwareExtension {
	var extensions []extension.ShopwareExtension

	// Get extensions from indexer
	indexedExtensions, err := p.extensionIndexer.GetAll()
	if err == nil {
		extensions = append(extensions, indexedExtensions...)
	}

	// Add default Shopware theme paths
	defaultThemePaths := []string{
		filepath.Join(p.projectRoot, "src", "Storefront"),
		filepath.Join(p.projectRoot, "vendor", "shopware", "storefront"),
		filepath.Join(p.projectRoot, "vendor", "shopware", "platform", "src", "Storefront"),
	}

	for _, themePath := range defaultThemePaths {
		if _, err := os.Stat(themePath); err == nil {
			defaultTheme := extension.ShopwareExtension{
				Name: "Storefront",
				Type: extension.ShopwareExtensionTypeBundle,
				Path: themePath,
			}
			extensions = append(extensions, defaultTheme)
			break // Only add the first found default theme
		}
	}

	return extensions
}

func (p *IconProvider) GetIconPacks() []string {
	extensions := p.getExtensions()
	iconPacksMap := make(map[string]bool)

	for _, ext := range extensions {
		iconPath := filepath.Join(extensionResourceRoot(ext.Path), "Resources", "app", "storefront", "dist", "assets", "icon")

		if _, err := os.Stat(iconPath); os.IsNotExist(err) {
			continue
		}

		entries, err := os.ReadDir(iconPath)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				iconPacksMap[entry.Name()] = true
			}
		}
	}

	iconPacks := make([]string, 0, len(iconPacksMap))
	for pack := range iconPacksMap {
		iconPacks = append(iconPacks, pack)
	}

	return iconPacks
}

// GetIcons returns all icons for a specific pack by reading the .svg files in the pack folder
func (p *IconProvider) GetIcons(pack string) []string {
	extensions := p.getExtensions()
	var icons []string

	for _, ext := range extensions {
		iconPackPath := filepath.Join(extensionResourceRoot(ext.Path), "Resources", "app", "storefront", "dist", "assets", "icon", pack)

		if _, err := os.Stat(iconPackPath); os.IsNotExist(err) {
			continue
		}

		entries, err := os.ReadDir(iconPackPath)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".svg") {
				iconName := strings.TrimSuffix(entry.Name(), ".svg")
				icons = append(icons, iconName)
			}
		}
	}

	// Remove duplicates
	iconMap := make(map[string]bool)
	for _, icon := range icons {
		iconMap[icon] = true
	}

	uniqueIcons := make([]string, 0, len(iconMap))
	for icon := range iconMap {
		uniqueIcons = append(uniqueIcons, icon)
	}

	return uniqueIcons
}

// GetIcon returns the filepath for a specific icon in a pack
func (p *IconProvider) GetIcon(pack, icon string) string {
	extensions := p.getExtensions()

	for _, ext := range extensions {
		iconFilePath := filepath.Join(extensionResourceRoot(ext.Path), "Resources", "app", "storefront", "dist", "assets", "icon", pack, icon+".svg")

		if _, err := os.Stat(iconFilePath); err == nil {
			return iconFilePath
		}
	}

	return ""
}

// extensionResourceRoot is the directory holding an extension's Resources
// folder. Bundle extensions point at their class file, whose directory is the
// bundle root.
func extensionResourceRoot(path string) string {
	if strings.EqualFold(filepath.Ext(path), ".php") {
		return filepath.Dir(path)
	}
	return path
}
