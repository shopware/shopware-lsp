package symfony

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

const maxRouteResourceFiles = 200

// RouteResourceReference is a statically resolvable routing import. Loader is
// Symfony's optional import type (for example "attribute" or "yaml").
type RouteResourceReference struct {
	Path      string
	Loader    string
	Namespace string
	Node      *cst.Node
	Range     cst.TextRange
	Nested    bool
}

// RouteResourceImport is the persisted form of RouteResourceReference.
type RouteResourceImport struct {
	Path      string
	Loader    string
	Namespace string
	FilePath  string
	Range     cst.TextRange
	Nested    bool
}

func (resource RouteResourceImport) Reference() RouteResourceReference {
	return RouteResourceReference{
		Path:      resource.Path,
		Loader:    resource.Loader,
		Namespace: resource.Namespace,
		Range:     resource.Range,
		Nested:    resource.Nested,
	}
}

// RouteResourceReferenceAt recognizes routing imports in YAML, XML, and PHP
// RoutingConfigurator files.
func RouteResourceReferenceAt(
	node *cst.Node,
) (RouteResourceReference, bool) {
	if node == nil {
		return RouteResourceReference{}, false
	}
	switch node.Kind() {
	case phpsyntax.PhpString:
		return phpRouteResourceReferenceAt(node)
	case yamlsyntax.YamlScalar:
		return yamlRouteResourceReferenceAt(node)
	case xmlsyntax.XmlAttribute, xmlsyntax.XmlAttributeValue:
		return xmlRouteResourceReferenceAt(node)
	}
	for current := node.Parent(); current != nil; current = current.Parent() {
		switch current.Kind() {
		case phpsyntax.PhpString:
			return phpRouteResourceReferenceAt(current)
		case yamlsyntax.YamlScalar:
			return yamlRouteResourceReferenceAt(current)
		case xmlsyntax.XmlAttribute, xmlsyntax.XmlAttributeValue:
			return xmlRouteResourceReferenceAt(current)
		}
	}
	return RouteResourceReference{}, false
}

// RouteResourceReferences returns each statically resolvable routing import
// once in source order.
func RouteResourceReferences(root *cst.Node) []RouteResourceReference {
	if root == nil {
		return nil
	}
	var result []RouteResourceReference
	seen := make(map[string]struct{})
	for element := range root.Descendants() {
		node, ok := element.(*cst.Node)
		if !ok {
			continue
		}
		switch node.Kind() {
		case phpsyntax.PhpString, yamlsyntax.YamlScalar,
			xmlsyntax.XmlAttributeValue:
		default:
			continue
		}
		reference, found := RouteResourceReferenceAt(node)
		if !found {
			continue
		}
		key := reference.Range.String()
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, reference)
	}
	return result
}

func yamlRouteResourceReferenceAt(
	node *yamlsyntax.Node,
) (RouteResourceReference, bool) {
	scalar := yamlRouteScalarAt(node)
	if scalar == nil {
		return RouteResourceReference{}, false
	}
	pair := yamlquery.AncestorPair(scalar)
	if pair == nil ||
		!sameRouteNode(yamlquery.PairValue(pair), scalar) {
		return RouteResourceReference{}, false
	}

	var resourcePair *yamlsyntax.Node
	nested := false
	switch yamlquery.ScalarValue(yamlquery.PairKey(pair)) {
	case "resource":
		resourcePair = pair
	case "path":
		mapping := yamlRouteParentMapping(pair)
		owner := yamlRouteParentPair(mapping)
		if owner == nil ||
			yamlquery.ScalarValue(yamlquery.PairKey(owner)) != "resource" ||
			!sameRouteNode(yamlquery.PairValue(owner), mapping) {
			return RouteResourceReference{}, false
		}
		resourcePair = owner
		nested = true
	default:
		return RouteResourceReference{}, false
	}

	definition := yamlRouteParentMapping(resourcePair)
	if definition == nil {
		return RouteResourceReference{}, false
	}
	path := yamlquery.ScalarValue(scalar)
	loader := yamlquery.ScalarValue(yamlquery.Property(definition, "type"))
	namespace := ""
	if nested {
		resource := yamlquery.PairValue(resourcePair)
		namespace = yamlquery.ScalarValue(
			yamlquery.Property(resource, "namespace"),
		)
	}
	if path == "" || strings.ContainsAny(path, "\r\n") ||
		!nested && loader == "" && !routeConfigResourcePath(path) {
		return RouteResourceReference{}, false
	}
	return RouteResourceReference{
		Path:      path,
		Loader:    loader,
		Namespace: namespace,
		Node:      scalar,
		Range:     yamlScalarContentRange(scalar),
		Nested:    nested,
	}, true
}

func xmlRouteResourceReferenceAt(
	node *xmlsyntax.Node,
) (RouteResourceReference, bool) {
	attribute := xmlquery.AttributeAt(node)
	if attribute == nil ||
		xmlquery.AttributeName(attribute) != "resource" {
		return RouteResourceReference{}, false
	}
	element := xmlquery.ElementAt(attribute)
	if element == nil || xmlquery.ElementName(element) != "import" {
		return RouteResourceReference{}, false
	}
	parent := xmlquery.ParentElement(element)
	if parent == nil || xmlquery.ElementName(parent) != "routes" {
		return RouteResourceReference{}, false
	}
	path := xmlquery.AttributeValue(attribute)
	if path == "" {
		return RouteResourceReference{}, false
	}
	return RouteResourceReference{
		Path:   path,
		Loader: xmlquery.AttributeValue(xmlquery.Attribute(element, "type")),
		Node:   attribute,
		Range:  xmlAttributeContentRange(attribute),
	}, true
}

func phpRouteResourceReferenceAt(
	node *phpsyntax.Node,
) (RouteResourceReference, bool) {
	literal, call, found := phpRouteResourceCallAt(node)
	if !found {
		return RouteResourceReference{}, false
	}
	path, found := phpRouteLiteralArgument(call, 0)
	if !found || path == "" {
		return RouteResourceReference{}, false
	}
	loader, _ := phpRouteLiteralArgument(call, 1)
	return RouteResourceReference{
		Path:   path,
		Loader: loader,
		Node:   literal,
		Range:  phpquery.StringContentRange(literal),
	}, true
}

// PHPRouteResourceCompletionRangeAt recognizes the first string argument of a
// typed RoutingConfigurator import call, including an empty literal.
func PHPRouteResourceCompletionRangeAt(
	node *phpsyntax.Node,
) (cst.TextRange, bool) {
	literal, _, found := phpRouteResourceCallAt(node)
	if !found {
		return cst.TextRange{}, false
	}
	return phpquery.StringContentRange(literal), true
}

func phpRouteResourceCallAt(
	node *phpsyntax.Node,
) (*phpsyntax.Node, *phpsyntax.Node, bool) {
	literal := phpquery.StringAt(node)
	call := phpquery.CallAt(literal)
	if literal == nil || call == nil ||
		!strings.EqualFold(phpquery.CallMethodName(call), "import") ||
		phpquery.ArgumentIndex(call, literal) != 0 {
		return nil, nil, false
	}
	function := phpquery.FunctionLikeAt(call)
	if function == nil {
		return nil, nil, false
	}
	root := routeDocumentRoot(call)
	resolver := php.NewNameResolver(root)
	configurators := routingConfiguratorParameters(function, resolver)
	receiver := phpquery.CallReceiver(call)
	if receiver == nil || receiver.Kind() != phpsyntax.PhpVariable {
		return nil, nil, false
	}
	if _, found := configurators["$"+phpquery.VariableName(receiver)]; !found {
		return nil, nil, false
	}
	return literal, call, true
}

func routeDocumentRoot(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	for node.Parent() != nil {
		node = node.Parent()
	}
	return node
}

func yamlRouteScalarAt(node *yamlsyntax.Node) *yamlsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == yamlsyntax.YamlScalar {
			return current
		}
		switch current.Kind() {
		case yamlsyntax.YamlPair, yamlsyntax.YamlMapping,
			yamlsyntax.YamlFlowMapping, yamlsyntax.YamlSequence,
			yamlsyntax.YamlFlowSequence:
			return nil
		}
	}
	return nil
}

func yamlRouteParentMapping(node *yamlsyntax.Node) *yamlsyntax.Node {
	if node == nil {
		return nil
	}
	for current := node.Parent(); current != nil; current = current.Parent() {
		if yamlquery.IsMapping(current) {
			return current
		}
	}
	return nil
}

func yamlRouteParentPair(node *yamlsyntax.Node) *yamlsyntax.Node {
	if node == nil {
		return nil
	}
	for current := node.Parent(); current != nil; current = current.Parent() {
		if current.Kind() == yamlsyntax.YamlPair {
			return current
		}
	}
	return nil
}

func sameRouteNode(left, right *cst.Node) bool {
	return left != nil && right != nil &&
		(left == right || left.Range() == right.Range())
}

func routeConfigResourcePath(path string) bool {
	lower := strings.ToLower(path)
	for _, extension := range []string{".yaml", ".yml", ".xml", ".php"} {
		if strings.Contains(lower, extension) {
			return true
		}
	}
	return false
}

// RouteResourceFiles resolves an import to deterministic file targets. Route
// directories are recursive and bounded to keep code-lens responses useful.
func RouteResourceFiles(
	currentPath string,
	reference RouteResourceReference,
) []string {
	if currentPath == "" || reference.Path == "" ||
		strings.ContainsRune(reference.Path, '\x00') ||
		strings.HasPrefix(reference.Path, "@") {
		return nil
	}
	target := filepath.FromSlash(
		strings.ReplaceAll(reference.Path, "\\", "/"),
	)
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(currentPath), target)
	}
	target = filepath.Clean(target)
	return routeResourceFilesAtTarget(target, reference)
}

func routeResourceFilesAtTarget(
	target string,
	reference RouteResourceReference,
) []string {
	if routeResourceHasGlob(target) {
		return routeResourceGlobFiles(target, reference)
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		if routeResourceFileAccepted(target, reference) {
			return []string{target}
		}
		return nil
	}
	return routeResourceDirectoryFiles(target, reference)
}

// RouteResourceMatches reports whether candidate is selected by an import
// without enumerating the whole imported directory. It powers reverse
// resource-to-import code lenses.
func RouteResourceMatches(
	currentPath,
	candidate string,
	reference RouteResourceReference,
) bool {
	if currentPath == "" || candidate == "" || reference.Path == "" ||
		strings.ContainsRune(reference.Path, '\x00') ||
		strings.HasPrefix(reference.Path, "@") {
		return false
	}
	target := filepath.FromSlash(
		strings.ReplaceAll(reference.Path, "\\", "/"),
	)
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(currentPath), target)
	}
	target = filepath.Clean(target)
	return routeResourceMatchesTarget(target, candidate, reference)
}

func routeResourceMatchesTarget(
	target,
	candidate string,
	reference RouteResourceReference,
) bool {
	candidate = filepath.Clean(candidate)
	if routeResourceHasGlob(target) {
		for _, expression := range routeResourceGlobExpressions(
			filepath.ToSlash(target),
		) {
			if expression.MatchString(filepath.ToSlash(candidate)) &&
				routeResourceFileAccepted(candidate, reference) {
				return true
			}
		}
		return false
	}
	if target == candidate {
		info, err := os.Stat(candidate)
		return err == nil && !info.IsDir() &&
			routeResourceFileAccepted(candidate, reference)
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() ||
		!routeResourceFileAccepted(candidate, reference) {
		return false
	}
	relative, err := filepath.Rel(target, candidate)
	return err == nil && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func routeResourceDirectoryFiles(
	root string,
	reference RouteResourceReference,
) []string {
	var result []string
	_ = filepath.WalkDir(root, func(
		path string,
		entry fs.DirEntry,
		err error,
	) error {
		if err != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if routeResourceFileAccepted(path, reference) {
			result = append(result, filepath.Clean(path))
			if len(result) >= maxRouteResourceFiles {
				return fs.SkipAll
			}
		}
		return nil
	})
	sort.Strings(result)
	return result
}

func routeResourceGlobFiles(
	pattern string,
	reference RouteResourceReference,
) []string {
	root := routeResourceGlobRoot(pattern)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil
	}
	expressions := routeResourceGlobExpressions(filepath.ToSlash(pattern))
	if len(expressions) == 0 {
		return nil
	}
	var result []string
	_ = filepath.WalkDir(root, func(
		path string,
		entry fs.DirEntry,
		err error,
	) error {
		if err != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		slashed := filepath.ToSlash(path)
		matches := false
		for _, expression := range expressions {
			if expression.MatchString(slashed) {
				matches = true
				break
			}
		}
		if matches && routeResourceFileAccepted(path, reference) {
			result = append(result, filepath.Clean(path))
			if len(result) >= maxRouteResourceFiles {
				return fs.SkipAll
			}
		}
		return nil
	})
	sort.Strings(result)
	return result
}

func routeResourceFileAccepted(
	path string,
	reference RouteResourceReference,
) bool {
	extension := strings.ToLower(filepath.Ext(path))
	switch strings.ToLower(reference.Loader) {
	case "annotation", "attribute", "attributes":
		return extension == ".php"
	case "yaml", "yml":
		return extension == ".yaml" || extension == ".yml"
	case "xml":
		return extension == ".xml"
	case "php":
		return extension == ".php"
	}
	if reference.Nested || reference.Namespace != "" {
		return extension == ".php"
	}
	switch extension {
	case ".php", ".yaml", ".yml", ".xml":
		return true
	default:
		return false
	}
}

func routeResourceHasGlob(path string) bool {
	return strings.ContainsAny(path, "*?[{")
}

func routeResourceGlobRoot(pattern string) string {
	first := len(pattern)
	if index := strings.IndexAny(pattern, "*?[{"); index >= 0 {
		first = index
	}
	prefix := pattern[:first]
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix = filepath.Dir(prefix)
	}
	if prefix == "" {
		return string(filepath.Separator)
	}
	return filepath.Clean(prefix)
}

func routeResourceGlobExpressions(pattern string) []*regexp.Regexp {
	expanded := expandRouteResourceBraces(pattern, 0)
	result := make([]*regexp.Regexp, 0, len(expanded))
	for _, candidate := range expanded {
		expression, err := regexp.Compile(routeResourceGlobRegexp(candidate))
		if err == nil {
			result = append(result, expression)
		}
	}
	return result
}

func expandRouteResourceBraces(pattern string, depth int) []string {
	if depth >= 8 {
		return []string{pattern}
	}
	start := strings.IndexByte(pattern, '{')
	if start < 0 {
		return []string{pattern}
	}
	end := strings.IndexByte(pattern[start+1:], '}')
	if end < 0 {
		return []string{pattern}
	}
	end += start + 1
	parts := strings.Split(pattern[start+1:end], ",")
	if len(parts) < 2 {
		return []string{pattern}
	}
	var result []string
	for _, part := range parts {
		next := pattern[:start] + part + pattern[end+1:]
		result = append(
			result,
			expandRouteResourceBraces(next, depth+1)...,
		)
		if len(result) >= 64 {
			return result[:64]
		}
	}
	return result
}

func routeResourceGlobRegexp(pattern string) string {
	var result strings.Builder
	result.WriteByte('^')
	for index := 0; index < len(pattern); index++ {
		character := pattern[index]
		switch character {
		case '*':
			if index+1 < len(pattern) && pattern[index+1] == '*' {
				index++
				if index+1 < len(pattern) && pattern[index+1] == '/' {
					index++
					result.WriteString("(?:.*/)?")
				} else {
					result.WriteString(".*")
				}
			} else {
				result.WriteString("[^/]*")
			}
		case '?':
			result.WriteString("[^/]")
		case '[':
			end := strings.IndexByte(pattern[index+1:], ']')
			if end < 0 {
				result.WriteString(`\[`)
				continue
			}
			end += index + 1
			class := pattern[index+1 : end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			result.WriteByte('[')
			result.WriteString(class)
			result.WriteByte(']')
			index = end
		default:
			if strings.ContainsRune(`.+()|^$\\`, rune(character)) {
				result.WriteByte('\\')
			}
			result.WriteByte(character)
		}
	}
	result.WriteByte('$')
	return result.String()
}
