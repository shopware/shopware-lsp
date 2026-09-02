package extension

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

// isShopwareBundle checks if a class extends Shopware\Core\Framework\Bundle or Shopware\Core\Framework\Plugin
func isShopwareBundle(class semantic.Symbol) bool {
	if class.Kind != semantic.ClassSymbol || len(class.Extends()) == 0 {
		return false
	}

	parent := strings.TrimPrefix(class.Extends()[0], "\\")
	return parent == "Shopware\\Core\\Framework\\Bundle" ||
		parent == "Shopware\\Core\\Framework\\Plugin"
}

// createBundleFromClass creates a ShopwareExtension instance from a PHP class
func createBundleFromClass(class semantic.Symbol) ShopwareExtension {
	// Extract the last part of the fully qualified class name
	nameParts := strings.Split(class.FullyQualified, "\\")
	name := class.FullyQualified
	if len(nameParts) > 0 {
		name = nameParts[len(nameParts)-1]
	}

	return ShopwareExtension{
		Name: name,
		Path: class.Path,
		Type: ShopwareExtensionTypeBundle,
	}
}

var coreBundles = []string{
	"Administration.php",
	"Checkout.php",
	"DevOps.php",
	"Framework.php",
	"Plugin.php",
	"Maintenance.php",
	"Profiling.php",
	"Service.php",
	"Content.php",
	"System.php",
	"Elasticsearch.php",
	"Storefront.php",
}

// isValidForIndex checks if a file should be indexed
func isValidForIndex(filePath string) bool {
	if hasExcludedBundlePathComponent(filePath) {
		return false
	}

	// Skip hidden files
	fileName := filepath.Base(filePath)
	if strings.HasPrefix(fileName, ".") {
		return false
	}

	if slices.Contains(coreBundles, fileName) {
		// Skip all core bundle files
		return false
	}

	// Handle test files but make exceptions for bundle and plugin classes
	if containsFold(fileName, "test") {
		// Skip all test files except TestBundle.php and TestPlugin.php (which may be valid bundle classes)
		if !hasSuffixFold(fileName, "bundle.php") &&
			!hasSuffixFold(fileName, "plugin.php") {
			return false
		}
	}

	// If we got this far, the file should be indexed
	return true
}

func hasExcludedBundlePathComponent(path string) bool {
	componentStart := 0
	for componentEnd := 0; componentEnd <= len(path); componentEnd++ {
		if componentEnd < len(path) &&
			path[componentEnd] != '/' &&
			path[componentEnd] != '\\' {
			continue
		}
		component := path[componentStart:componentEnd]
		if strings.EqualFold(component, "tests") ||
			strings.EqualFold(component, "test") ||
			strings.EqualFold(component, "fixtures") ||
			strings.EqualFold(component, "_fixture") ||
			strings.EqualFold(component, "_fixtures") {
			return true
		}
		componentStart = componentEnd + 1
	}
	return false
}

func containsFold(source, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for index := 0; index+len(needle) <= len(source); index++ {
		if strings.EqualFold(source[index:index+len(needle)], needle) {
			return true
		}
	}
	return false
}

func hasSuffixFold(source, suffix string) bool {
	return len(source) >= len(suffix) &&
		strings.EqualFold(source[len(source)-len(suffix):], suffix)
}
