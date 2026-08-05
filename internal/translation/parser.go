package translation

import (
	"path/filepath"
	"strings"
	"unicode"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
)

type resourceMetadata struct {
	domain   string
	locale   string
	format   string
	compiled bool
}

func catalogueMetadata(path string) (resourceMetadata, bool) {
	extension := strings.ToLower(filepath.Ext(path))
	switch extension {
	case ".php", ".yaml", ".yml", ".xml", ".xlf", ".xliff":
	default:
		return resourceMetadata{}, false
	}
	filename := filepath.Base(path)
	parts := strings.Split(filename, ".")
	if len(parts) < 3 {
		return resourceMetadata{}, false
	}
	domain := strings.Join(parts[:len(parts)-2], ".")
	locale := parts[len(parts)-2]
	if domain == "" || locale == "" {
		return resourceMetadata{}, false
	}
	metadata := resourceMetadata{
		domain: normalizeDomain(domain),
		locale: locale,
		format: strings.TrimPrefix(extension, "."),
	}
	if strings.EqualFold(domain, "catalogue") && extension == ".php" {
		metadata.compiled = true
		return metadata, true
	}

	normalized := filepath.ToSlash(path)
	inTranslationDirectory := strings.Contains(
		strings.ToLower(normalized),
		"/translations/",
	)
	domainLower := strings.ToLower(metadata.domain)
	isConventionalDomain := domainLower == "messages" ||
		domainLower == "validators"
	isXLIFF := extension == ".xlf" || extension == ".xliff"
	if !inTranslationDirectory && !isConventionalDomain &&
		!isXLIFF && !looksLikeLocale(locale) {
		return resourceMetadata{}, false
	}
	return metadata, true
}

func looksLikeLocale(value string) bool {
	if len(value) < 2 {
		return false
	}
	return unicode.IsLower(rune(value[0])) &&
		unicode.IsLower(rune(value[1]))
}

func parseYAMLResource(
	file *indexer.ParsedFile,
	metadata resourceMetadata,
) []Message {
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		return nil
	}
	root := yamlquery.RootValue(tree.Root)
	if !yamlquery.IsMapping(root) {
		return nil
	}
	var result []Message
	collectYAMLMessages(
		root,
		nil,
		file,
		metadata,
		&result,
	)
	return result
}

func collectYAMLMessages(
	mapping *cst.Node,
	path []string,
	file *indexer.ParsedFile,
	metadata resourceMetadata,
	result *[]Message,
) {
	for _, pair := range yamlquery.Pairs(mapping) {
		keyNode := yamlquery.PairKey(pair)
		value := yamlquery.PairValue(pair)
		key := strings.TrimSpace(yamlquery.ScalarValue(keyNode))
		if key == "" {
			continue
		}
		nextPath := append(append([]string(nil), path...), key)
		if yamlquery.IsMapping(value) {
			collectYAMLMessages(value, nextPath, file, metadata, result)
			continue
		}
		text := yamlquery.ScalarValue(value)
		if text == "" {
			text = yamlquery.RawText(value)
		}
		*result = append(*result, newMessage(
			metadata.domain,
			strings.Join(nextPath, "."),
			text,
			metadata.locale,
			file.Path,
			metadata.format,
			keyNode,
			file.LineIndex(),
		))
	}
}

func parseXMLResource(
	file *indexer.ParsedFile,
	metadata resourceMetadata,
) []Message {
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		return nil
	}
	var result []Message
	for _, unit := range xmlquery.Elements(tree.Root, "trans-unit", "unit") {
		source := translationSourceElement(unit)
		keyNode := source
		key := strings.TrimSpace(xmlquery.TextContent(source))
		if resname := xmlquery.Attribute(unit, "resname"); resname != nil {
			key = strings.TrimSpace(xmlquery.AttributeValue(resname))
			keyNode = resname
		}
		if key == "" {
			continue
		}
		target := translationTargetElement(unit)
		text := strings.TrimSpace(xmlquery.TextContent(target))
		result = append(result, newMessage(
			metadata.domain,
			key,
			text,
			metadata.locale,
			file.Path,
			metadata.format,
			keyNode,
			file.LineIndex(),
		))
	}
	return result
}

func translationSourceElement(unit *cst.Node) *cst.Node {
	if source := xmlquery.ChildElement(unit, "source"); source != nil {
		return source
	}
	if segment := xmlquery.ChildElement(unit, "segment"); segment != nil {
		return xmlquery.ChildElement(segment, "source")
	}
	for _, segment := range xmlquery.Elements(unit, "segment") {
		if source := xmlquery.ChildElement(segment, "source"); source != nil {
			return source
		}
	}
	return nil
}

func translationTargetElement(unit *cst.Node) *cst.Node {
	if target := xmlquery.ChildElement(unit, "target"); target != nil {
		return target
	}
	if segment := xmlquery.ChildElement(unit, "segment"); segment != nil {
		return xmlquery.ChildElement(segment, "target")
	}
	for _, segment := range xmlquery.Elements(unit, "segment") {
		if target := xmlquery.ChildElement(segment, "target"); target != nil {
			return target
		}
	}
	return nil
}

func parsePHPResource(
	file *indexer.ParsedFile,
	metadata resourceMetadata,
) []Message {
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		return nil
	}
	var result []Message
	for _, statement := range phpquery.Nodes(
		tree.Root,
		phpsyntax.PhpReturnStatement,
	) {
		array := phpquery.DirectChild(statement, phpsyntax.PhpArray)
		if array == nil {
			continue
		}
		result = appendPHPArrayMessages(
			result,
			array,
			metadata.domain,
			file,
			metadata,
		)
	}
	return result
}

func parseCompiledPHPCatalogue(
	file *indexer.ParsedFile,
	metadata resourceMetadata,
) []Message {
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		return nil
	}
	var result []Message
	for _, object := range phpquery.ObjectCreations(tree.Root) {
		if !strings.EqualFold(
			lastNamePart(phpquery.ObjectClassName(object)),
			"MessageCatalogue",
		) {
			continue
		}
		locale := metadata.locale
		if localeNode := phpquery.StringArgument(object, 0); localeNode != nil {
			locale = phpquery.StringValue(localeNode)
		}
		domains := phpquery.ArgumentExpression(object, 1)
		if domains == nil || domains.Kind() != phpsyntax.PhpArray {
			continue
		}
		for _, domainItem := range phpquery.ArrayItems(domains) {
			domainNode := phpquery.ArrayItemKey(domainItem)
			messages := phpquery.ArrayItemValue(domainItem)
			if domainNode == nil || messages == nil ||
				messages.Kind() != phpsyntax.PhpArray {
				continue
			}
			domain := normalizeDomain(phpquery.StringValue(domainNode))
			if domain == "" {
				continue
			}
			localMetadata := metadata
			localMetadata.domain = domain
			localMetadata.locale = locale
			result = appendPHPArrayMessages(
				result,
				messages,
				domain,
				file,
				localMetadata,
			)
		}
	}
	return result
}

func appendPHPArrayMessages(
	result []Message,
	array *cst.Node,
	domain string,
	file *indexer.ParsedFile,
	metadata resourceMetadata,
) []Message {
	for _, item := range phpquery.ArrayItems(array) {
		keyNode := phpquery.ArrayItemKey(item)
		valueNode := phpquery.ArrayItemValue(item)
		if keyNode == nil || valueNode == nil {
			continue
		}
		key := phpquery.StringValue(keyNode)
		if key == "" {
			continue
		}
		text := phpquery.StringValue(valueNode)
		if text == "" {
			text = strings.TrimSpace(valueNode.Text())
		}
		result = append(result, newMessage(
			domain,
			key,
			text,
			metadata.locale,
			file.Path,
			metadata.format,
			keyNode,
			file.LineIndex(),
		))
	}
	return result
}

func lastNamePart(value string) string {
	value = strings.TrimPrefix(value, "\\")
	if index := strings.LastIndex(value, "\\"); index >= 0 {
		return value[index+1:]
	}
	return value
}
