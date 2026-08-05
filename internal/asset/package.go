package asset

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
)

func PackagesInXML(path string, root *xmlsyntax.Node) []Package {
	result := inferredBundlePackage(path)
	for _, tag := range xmlquery.Elements(root, "tag") {
		tagName := xmlquery.AttributeValue(xmlquery.Attribute(tag, "name"))
		packageAttribute := "package"
		if tagName == "shopware.asset" {
			packageAttribute = "asset"
		} else if tagName != "assets.package" {
			continue
		}
		attribute := xmlquery.Attribute(tag, packageAttribute)
		name := strings.TrimSpace(xmlquery.AttributeValue(attribute))
		if name == "" {
			continue
		}
		result = append(result, Package{
			Name:  name,
			File:  path,
			Range: attribute.RangeTrimmedTrivia(),
		})
	}
	return deduplicatePackages(result)
}

func PackagesInPHP(path string, root *phpsyntax.Node) []Package {
	var result []Package
	for _, call := range phpquery.Nodes(
		root,
		phpsyntax.PhpMemberCall,
		phpsyntax.PhpScopedCall,
		phpsyntax.PhpFunctionCall,
	) {
		if !strings.EqualFold(phpquery.CallMethodName(call), "tag") {
			continue
		}
		tagNode := phpquery.ArgumentExpression(call, 0)
		tagName := phpStaticStringValue(tagNode)
		optionName := "package"
		if tagName == "shopware.asset" {
			optionName = "asset"
		} else if tagName != "assets.package" {
			continue
		}
		options := phpquery.ArgumentExpression(call, 1)
		if options == nil || options.Kind() != phpsyntax.PhpArray {
			continue
		}
		for _, item := range phpquery.ArrayItems(options) {
			key := phpquery.ArrayItemKey(item)
			if phpStaticStringValue(key) != optionName {
				continue
			}
			value := phpquery.ArrayItemValue(item)
			name := phpStaticStringValue(value)
			if name == "" {
				continue
			}
			result = append(result, Package{
				Name:  name,
				File:  path,
				Range: phpStringContentRange(value),
			})
		}
	}
	return deduplicatePackages(result)
}

func PackagesInYAML(path string, root *yamlsyntax.Node) []Package {
	result := inferredBundlePackage(path)
	document := yamlquery.RootValue(root)
	framework := yamlquery.Property(document, "framework")
	assets := yamlquery.Property(framework, "assets")
	packages := yamlquery.Property(assets, "packages")
	if !yamlquery.IsMapping(packages) {
		return deduplicatePackages(result)
	}
	for _, pair := range yamlquery.Pairs(packages) {
		key := yamlquery.PairKey(pair)
		name := strings.TrimSpace(yamlquery.ScalarValue(key))
		if name == "" {
			continue
		}
		basePath := ""
		if config := yamlquery.PairValue(pair); yamlquery.IsMapping(config) {
			value := yamlquery.ScalarValue(
				yamlquery.Property(config, "base_path"),
			)
			if !strings.Contains(value, "%") {
				basePath = normalizeName(value)
			}
		}
		result = append(result, Package{
			Name:     name,
			BasePath: basePath,
			File:     path,
			Range:    scalarContentRange(key),
		})
	}
	return deduplicatePackages(result)
}

func inferredBundlePackage(path string) []Package {
	normalized := filepath.ToSlash(filepath.Clean(path))
	const marker = "/Resources/config/"
	index := strings.LastIndex(normalized, marker)
	if index < 0 {
		return nil
	}
	bundle := filepath.Base(normalized[:index])
	if bundle == "" || bundle == "." {
		return nil
	}
	publicName := strings.TrimSuffix(
		strings.ToLower(bundle),
		"bundle",
	)
	if publicName == "" {
		return nil
	}
	return []Package{{
		Name:     "@" + bundle,
		BasePath: "bundles/" + publicName,
		File:     path,
		Inferred: true,
	}}
}

func scalarContentRange(node *yamlsyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	if len(text) >= 2 &&
		(text[0] == '\'' || text[0] == '"') &&
		text[len(text)-1] == text[0] {
		rng.Start++
		rng.End--
	}
	return rng
}

func phpStringContentRange(node *phpsyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	if len(text) >= 2 &&
		(text[0] == '\'' || text[0] == '"') &&
		text[len(text)-1] == text[0] {
		rng.Start++
		rng.End--
	}
	return rng
}

func deduplicatePackages(packages []Package) []Package {
	packages = append([]Package(nil), packages...)
	sort.Slice(packages, func(left, right int) bool {
		if !strings.EqualFold(packages[left].Name, packages[right].Name) {
			return strings.ToLower(packages[left].Name) <
				strings.ToLower(packages[right].Name)
		}
		if packages[left].Inferred != packages[right].Inferred {
			return !packages[left].Inferred
		}
		if packages[left].File != packages[right].File {
			return packages[left].File < packages[right].File
		}
		return packages[left].Range.Start < packages[right].Range.Start
	})
	result := make([]Package, 0, len(packages))
	seen := make(map[string]struct{}, len(packages))
	for _, current := range packages {
		current.Name = strings.TrimSpace(current.Name)
		current.BasePath = normalizeName(current.BasePath)
		if current.Name == "" {
			continue
		}
		key := strings.ToLower(current.Name) + "\x00" +
			strings.ToLower(current.BasePath)
		if !current.Inferred {
			key += "\x00" + filepath.Clean(current.File) + "\x00" +
				current.Range.String()
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, current)
	}
	sort.Slice(result, func(left, right int) bool {
		if !strings.EqualFold(result[left].Name, result[right].Name) {
			return strings.ToLower(result[left].Name) <
				strings.ToLower(result[right].Name)
		}
		if result[left].Inferred != result[right].Inferred {
			return !result[left].Inferred
		}
		return result[left].File < result[right].File
	})
	return result
}
