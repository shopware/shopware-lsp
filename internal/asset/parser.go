package asset

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	jsonquery "github.com/shopware/shopware-lsp/internal/parser/json/query"
	jsonsyntax "github.com/shopware/shopware-lsp/internal/parser/json/syntax"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

func parseManifest(path string, tree *cst.Tree) []Resource {
	if tree == nil || tree.Root == nil {
		return nil
	}
	object := jsonquery.RootValue(tree.Root)
	if object == nil || object.Kind() != jsonsyntax.JsonObject {
		return nil
	}
	var result []Resource
	for _, pair := range jsonquery.Pairs(object) {
		key := jsonquery.PairKey(pair)
		value := jsonquery.PairValue(pair)
		name := normalizeName(jsonquery.StringValue(key))
		if name == "" {
			continue
		}
		target := jsonquery.StringValue(value)
		result = append(result, Resource{
			Name:   name,
			File:   path,
			Target: resolvePublicTarget(path, target),
			Kind:   ManifestAsset,
			Range:  jsonStringRange(key),
		})
	}
	return result
}

func parseEntrypoints(path string, tree *cst.Tree) []Resource {
	if tree == nil || tree.Root == nil {
		return nil
	}
	root := jsonquery.RootValue(tree.Root)
	if root == nil || root.Kind() != jsonsyntax.JsonObject {
		return nil
	}
	entrypoints := jsonquery.Property(root, "entrypoints")
	if entrypoints == nil || entrypoints.Kind() != jsonsyntax.JsonObject {
		return nil
	}
	var result []Resource
	for _, pair := range jsonquery.Pairs(entrypoints) {
		key := jsonquery.PairKey(pair)
		name := jsonquery.StringValue(key)
		if name == "" {
			continue
		}
		result = append(result, Resource{
			Name:   name,
			File:   path,
			Target: entrypointTarget(path, jsonquery.PairValue(pair)),
			Kind:   EncoreEntry,
			Range:  jsonStringRange(key),
		})
	}
	return result
}

func entrypointTarget(path string, value *jsonsyntax.Node) string {
	if value == nil || value.Kind() != jsonsyntax.JsonObject {
		return ""
	}
	for _, property := range []string{"js", "css"} {
		values := jsonquery.Property(value, property)
		if values == nil || values.Kind() != jsonsyntax.JsonArray {
			continue
		}
		for child := range values.ChildNodes() {
			if child.Kind() == jsonsyntax.JsonString {
				return resolvePublicTarget(
					path,
					jsonquery.StringValue(child),
				)
			}
		}
	}
	return ""
}

func parseWebpackConfig(path string, tree *cst.Tree) []Resource {
	if tree == nil || tree.Root == nil {
		return nil
	}
	var result []Resource
	for call := range jsquery.IterateCalls(tree.Root) {
		name := jsquery.CallName(call)
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, "addentry") &&
			!strings.HasSuffix(lower, "addstyleentry") &&
			!strings.HasSuffix(lower, "addsassentry") {
			continue
		}
		entry := jsquery.StringArgument(call, 0)
		entryName := jsquery.StringValue(entry)
		if entryName == "" {
			continue
		}
		target := ""
		if value := jsquery.StringArgument(call, 1); value != nil {
			target = resolveRelativeTarget(
				filepath.Dir(path),
				jsquery.StringValue(value),
			)
		}
		result = append(result, Resource{
			Name:   entryName,
			File:   path,
			Target: target,
			Kind:   EncoreEntry,
			Range:  javascriptStringRange(entry),
		})
	}
	return result
}

func parseImportmap(path string, tree *cst.Tree) []Resource {
	if tree == nil || tree.Root == nil {
		return nil
	}
	sourceType := strings.ToLower(filepath.Base(path))
	var mapping *phpsyntax.Node
	for _, array := range phpquery.Arrays(tree.Root) {
		if parent := array.Parent(); parent != nil &&
			parent.Kind() == phpsyntax.PhpReturnStatement {
			mapping = array
			break
		}
	}
	if mapping == nil {
		return nil
	}
	var result []Resource
	for _, item := range phpquery.ArrayItems(mapping) {
		keyNode := phpquery.ArrayItemKey(item)
		name := phpStaticStringValue(keyNode)
		config := phpquery.ArrayItemValue(item)
		if name == "" || config == nil ||
			config.Kind() != phpsyntax.PhpArray {
			continue
		}
		resource := Resource{
			Name:       name,
			File:       path,
			Kind:       ImportmapModule,
			Range:      phpStringContentRange(keyNode),
			URL:        phpArrayString(config, "url"),
			Version:    phpArrayString(config, "version"),
			ModuleType: phpArrayString(config, "type"),
			Entrypoint: phpArrayBool(config, "entrypoint"),
		}
		configuredPath := phpArrayString(config, "path")
		resource.Target = importmapTarget(
			path,
			sourceType,
			name,
			configuredPath,
			resource.ModuleType,
		)
		result = append(result, resource)
	}
	return result
}

func phpArrayString(array *phpsyntax.Node, name string) string {
	for _, item := range phpquery.ArrayItems(array) {
		if phpStaticStringValue(phpquery.ArrayItemKey(item)) == name {
			return phpStaticStringValue(phpquery.ArrayItemValue(item))
		}
	}
	return ""
}

func phpArrayBool(array *phpsyntax.Node, name string) bool {
	for _, item := range phpquery.ArrayItems(array) {
		if phpStaticStringValue(phpquery.ArrayItemKey(item)) != name {
			continue
		}
		value := phpquery.ArrayItemValue(item)
		return value != nil &&
			strings.EqualFold(strings.TrimSpace(value.Text()), "true")
	}
	return false
}

func importmapTarget(
	mappingPath,
	sourceType,
	name,
	configuredPath,
	moduleType string,
) string {
	base := filepath.Dir(mappingPath)
	if configuredPath != "" {
		return resolveRelativeTarget(base, configuredPath)
	}
	if sourceType == "importmap.php" {
		base = filepath.Join(base, "assets", "vendor")
	}
	name = filepath.FromSlash(strings.TrimLeft(name, "/"))
	if name == "" {
		return ""
	}
	if strings.EqualFold(moduleType, "css") ||
		sourceType == "installed.php" &&
			strings.HasSuffix(strings.ToLower(name), ".css") {
		return resolveRelativeTarget(base, name)
	}
	parts := strings.Split(filepath.ToSlash(name), "/")
	switch {
	case strings.HasPrefix(name, "@") && len(parts) == 2:
		return resolveRelativeTarget(
			base,
			filepath.Join(name, parts[1]+".index.js"),
		)
	case len(parts) > 1:
		return resolveRelativeTarget(base, name+".js")
	default:
		return resolveRelativeTarget(
			base,
			filepath.Join(name, name+".index.js"),
		)
	}
}

func resolvePublicTarget(metadataPath, target string) string {
	target = strings.TrimSpace(strings.ReplaceAll(target, `\`, "/"))
	if target == "" {
		return ""
	}
	if strings.HasPrefix(target, "/") {
		normalized := filepath.ToSlash(metadataPath)
		for _, marker := range []string{"/public/", "/web/"} {
			if position := strings.Index(normalized, marker); position >= 0 {
				root := normalized[:position+len(marker)-1]
				return filepath.Clean(filepath.FromSlash(
					root + "/" + strings.TrimLeft(target, "/"),
				))
			}
		}
	}
	return resolveRelativeTarget(filepath.Dir(metadataPath), target)
}

func resolveRelativeTarget(base, target string) string {
	target = strings.TrimSpace(strings.ReplaceAll(target, `\`, "/"))
	target = strings.TrimPrefix(target, "./")
	if target == "" || strings.Contains(target, "://") {
		return ""
	}
	resolved := filepath.Clean(filepath.Join(base, filepath.FromSlash(target)))
	if _, err := os.Stat(resolved); err == nil {
		return resolved
	}
	return resolved
}

func jsonStringRange(node *jsonsyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	if len(text) >= 1 && text[0] == '"' {
		rng.Start++
	}
	if len(text) >= 2 && text[len(text)-1] == '"' {
		rng.End--
	}
	return rng
}

func javascriptStringRange(node *jssyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	if len(text) >= 1 &&
		(text[0] == '\'' || text[0] == '"' || text[0] == '`') {
		rng.Start++
	}
	if len(text) >= 2 &&
		(text[len(text)-1] == '\'' ||
			text[len(text)-1] == '"' ||
			text[len(text)-1] == '`') {
		rng.End--
	}
	return rng
}
