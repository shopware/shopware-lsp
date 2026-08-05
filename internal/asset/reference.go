package asset

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

type ReferenceKind uint8

const (
	AssetReference ReferenceKind = iota
	EncoreEntryReference
	AssetPackageReference
	ImportmapReference
	ViteEntryReference
	AsseticNamedReference
)

type Reference struct {
	Name      string
	Package   string
	AssetName string
	Assetic   bool
	Kind      ReferenceKind
	Node      *cst.Node
	Container *cst.Node
	HTMLType  HTMLAssetType
}

type HTMLAssetType uint8

const (
	HTMLAssetNone HTMLAssetType = iota
	HTMLAssetCSS
	HTMLAssetJavaScript
	HTMLAssetImage
)

func ReferenceAt(path string, node *cst.Node) (Reference, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		return phpReferenceAt(node)
	case ".twig":
		if reference, found := twigReferenceAt(node); found {
			return reference, true
		}
		return twigHTMLReferenceAt(node)
	default:
		return Reference{}, false
	}
}

func References(path string, root *cst.Node) []Reference {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		var result []Reference
		for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
			if reference, found := phpReferenceAt(literal); found {
				result = append(result, reference)
			}
		}
		return result
	case ".twig":
		var result []Reference
		for _, literal := range twigquery.Nodes(
			root,
			twigsyntax.TwigLiteralString,
		) {
			if reference, found := twigReferenceAt(literal); found {
				result = append(result, reference)
			}
		}
		for _, htmlString := range twigquery.Nodes(
			root,
			twigsyntax.HtmlString,
		) {
			if reference, found := twigHTMLReferenceAt(htmlString); found {
				result = append(result, reference)
			}
		}
		return result
	default:
		return nil
	}
}

func twigHTMLReferenceAt(node *twigsyntax.Node) (Reference, bool) {
	htmlString := twigquery.ClosestNodeOfKind(
		node,
		twigsyntax.HtmlString,
	)
	if htmlString == nil {
		return Reference{}, false
	}
	attribute := twigquery.HTMLAttributeAt(htmlString)
	startTag := twigquery.StartingHTMLTagAt(attribute)
	if attribute == nil || startTag == nil {
		return Reference{}, false
	}
	tagName := strings.ToLower(twigquery.HTMLTagName(startTag))
	attributeName := strings.ToLower(
		twigquery.HTMLAttributeName(attribute),
	)
	var htmlType HTMLAssetType
	switch {
	case tagName == "link" && attributeName == "href" &&
		htmlAttributeEquals(startTag, "rel", "stylesheet"):
		htmlType = HTMLAssetCSS
	case tagName == "script" && attributeName == "src":
		htmlType = HTMLAssetJavaScript
	case tagName == "img" && attributeName == "src":
		htmlType = HTMLAssetImage
	default:
		return Reference{}, false
	}
	valueNode := htmlString
	typed, _ := twigast.CastHtmlString(htmlString)
	if inner, found := typed.GetInner(); found {
		valueNode = inner.Syntax()
	}
	value := strings.TrimSpace(valueNode.Text())
	if !isLocalStaticHTMLAsset(value) {
		return Reference{}, false
	}
	return Reference{
		Name:      normalizeHTMLAssetName(value),
		Kind:      AssetReference,
		Node:      valueNode,
		Container: htmlString,
		HTMLType:  htmlType,
	}, true
}

func htmlAttributeEquals(
	startTag *twigsyntax.Node,
	name,
	value string,
) bool {
	typed, found := twigast.CastHtmlStartingTag(startTag)
	if !found {
		return false
	}
	for _, attribute := range typed.Attributes() {
		if !strings.EqualFold(
			twigquery.HTMLAttributeName(attribute.Syntax()),
			name,
		) {
			continue
		}
		htmlString, found := attribute.Value()
		if !found {
			return false
		}
		inner, found := htmlString.GetInner()
		return found &&
			strings.EqualFold(strings.TrimSpace(inner.Syntax().Text()), value)
	}
	return false
}

func isLocalStaticHTMLAsset(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return !strings.Contains(value, "{{") &&
		!strings.Contains(value, "{%") &&
		!strings.Contains(value, "{#") &&
		!strings.Contains(value, "://") &&
		!strings.ContainsAny(value, "?#") &&
		!strings.HasPrefix(value, "//") &&
		!strings.HasPrefix(lower, "data:") &&
		!strings.HasPrefix(lower, "mailto:") &&
		!strings.HasPrefix(lower, "tel:")
}

func normalizeHTMLAssetName(value string) string {
	value = strings.TrimSpace(value)
	for strings.HasPrefix(value, "./") {
		value = strings.TrimPrefix(value, "./")
	}
	return normalizeName(value)
}

func MatchesHTMLAssetType(name string, assetType HTMLAssetType) bool {
	extension := strings.ToLower(filepath.Ext(name))
	switch assetType {
	case HTMLAssetCSS:
		switch extension {
		case ".css", ".less", ".sass", ".scss":
			return true
		}
	case HTMLAssetJavaScript:
		switch extension {
		case ".js", ".mjs", ".coffee", ".dart":
			return true
		}
	case HTMLAssetImage:
		switch extension {
		case ".avif", ".bmp", ".gif", ".ico", ".jpeg", ".jpg",
			".png", ".svg", ".webp":
			return true
		}
	default:
		return true
	}
	return false
}

func phpReferenceAt(node *phpsyntax.Node) (Reference, bool) {
	literal := phpquery.StringAt(node)
	if literal == nil {
		return Reference{}, false
	}
	call := phpquery.CallAt(literal)
	if call == nil {
		return Reference{}, false
	}
	switch strings.ToLower(phpquery.CallMethodName(call)) {
	case "geturl", "getversion":
		argument := phpquery.ArgumentIndex(call, literal)
		argumentName := strings.ToLower(
			phpquery.ArgumentName(literal),
		)
		if argument < 0 ||
			phpquery.ArgumentExpression(call, argument) != literal {
			return Reference{}, false
		}
		switch {
		case argumentName == "path" ||
			argumentName == "" && argument == 0:
			packageNode := phpStringCallArgument(
				call,
				1,
				"packagename",
				"package",
			)
			return Reference{
				Name: normalizeName(
					phpquery.StringValue(literal),
				),
				Package:   phpStaticStringValue(packageNode),
				Kind:      AssetReference,
				Node:      literal,
				Container: call,
			}, true
		case argumentName == "packagename" ||
			argumentName == "package" ||
			argumentName == "" && argument == 1:
			assetNode := phpStringCallArgument(call, 0, "path")
			return Reference{
				Name:      phpStaticStringValue(literal),
				Package:   phpStaticStringValue(literal),
				AssetName: normalizeName(phpStaticStringValue(assetNode)),
				Kind:      AssetPackageReference,
				Node:      literal,
				Container: call,
			}, true
		default:
			return Reference{}, false
		}
	default:
		return Reference{}, false
	}
}

func twigReferenceAt(node *twigsyntax.Node) (Reference, bool) {
	literal := twigquery.LiteralStringAt(node)
	if literal == nil || !twigquery.StringIsStatic(literal) {
		return Reference{}, false
	}
	if reference, found := twigAsseticReferenceAt(literal); found {
		return reference, true
	}
	call := twigquery.FunctionCallAt(literal)
	if call == nil {
		return Reference{}, false
	}
	kind := AssetReference
	switch twigquery.FunctionName(call) {
	case "asset":
	case "importmap":
		kind = ImportmapReference
	case "encore_entry_link_tags",
		"encore_entry_script_tags",
		"encore_entry_exists",
		"encore_entry_js_files",
		"encore_entry_css_files":
		kind = EncoreEntryReference
	case "vite_entry_link_tags",
		"vite_entry_script_tags":
		kind = ViteEntryReference
	default:
		return Reference{}, false
	}
	argument := twigquery.FunctionArgumentIndex(literal)
	if argument < 0 ||
		twigquery.StringArgument(call, argument) != literal {
		return Reference{}, false
	}
	if (kind == EncoreEntryReference || kind == ImportmapReference ||
		kind == ViteEntryReference) &&
		argument != 0 {
		return Reference{}, false
	}
	if kind == AssetReference && argument == 1 {
		pathNode := twigquery.StringArgument(call, 0)
		return Reference{
			Name:      twigquery.StringValue(literal),
			Package:   twigquery.StringValue(literal),
			AssetName: normalizeName(twigStaticStringValue(pathNode)),
			Kind:      AssetPackageReference,
			Node:      literal,
			Container: call,
		}, true
	}
	if argument != 0 {
		return Reference{}, false
	}
	packageName := ""
	if kind == AssetReference {
		packageName = twigStaticStringValue(
			twigquery.StringArgument(call, 1),
		)
	}
	return Reference{
		Name:      normalizeName(twigquery.StringValue(literal)),
		Package:   packageName,
		Kind:      kind,
		Node:      literal,
		Container: call,
	}, true
}

func twigAsseticReferenceAt(
	literal *twigsyntax.Node,
) (Reference, bool) {
	tag := twigquery.TagAt(literal)
	tagName := twigquery.TagName(tag)
	if (tagName != "stylesheets" && tagName != "javascripts") ||
		!twigquery.StringInTag(literal, tagName) {
		return Reference{}, false
	}
	value := twigquery.StringValue(literal)
	// A leading @ without a bundle Resources/public path is an Assetic named
	// formula resolved from the compiled container.
	if strings.HasPrefix(value, "@") &&
		!strings.Contains(value, "/Resources/public/") {
		name := strings.TrimPrefix(strings.TrimSpace(value), "@")
		if name == "" {
			return Reference{}, false
		}
		assetType := HTMLAssetJavaScript
		if tagName == "stylesheets" {
			assetType = HTMLAssetCSS
		}
		return Reference{
			Name:      name,
			Assetic:   true,
			Kind:      AsseticNamedReference,
			Node:      literal,
			Container: tag,
			HTMLType:  assetType,
		}, true
	}
	name, packageName := asseticLogicalName(value)
	if name == "" {
		return Reference{}, false
	}
	assetType := HTMLAssetJavaScript
	if tagName == "stylesheets" {
		assetType = HTMLAssetCSS
	}
	return Reference{
		Name:      name,
		Package:   packageName,
		Assetic:   true,
		Kind:      AssetReference,
		Node:      literal,
		Container: tag,
		HTMLType:  assetType,
	}, true
}

func asseticLogicalName(value string) (string, string) {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	const publicMarker = "/Resources/public/"
	if strings.HasPrefix(value, "@") {
		if marker := strings.Index(value, publicMarker); marker > 1 {
			return normalizeName(value[marker+len(publicMarker):]),
				value[:marker]
		}
	}
	return normalizeName(value), ""
}

func ValidatePHPReference(
	ctx context.Context,
	reference Reference,
	index *php.PHPIndex,
	content []byte,
) bool {
	if reference.Container == nil ||
		index == nil {
		return false
	}
	if reference.Kind == AssetPackageReference {
		return index.IsMethodCalledOnClass(
			ctx,
			reference.Container,
			content,
			"Symfony\\Component\\Asset\\Packages",
		)
	}
	if reference.Kind != AssetReference {
		return false
	}
	for _, className := range []string{
		"Symfony\\Component\\Asset\\Packages",
		"Symfony\\Component\\Asset\\Package",
		"Symfony\\Component\\Asset\\PackageInterface",
		"Symfony\\Component\\Asset\\Context\\ContextualPackage",
		"Symfony\\Component\\Asset\\PathPackage",
		"Symfony\\Component\\Asset\\UrlPackage",
	} {
		if index.IsMethodCalledOnClass(
			ctx,
			reference.Container,
			content,
			className,
		) {
			return true
		}
	}
	return false
}

func phpStringCallArgument(
	call *phpsyntax.Node,
	fallback int,
	names ...string,
) *phpsyntax.Node {
	for index, argument := range phpquery.Arguments(call) {
		name := strings.ToLower(phpquery.ArgumentName(argument))
		for _, candidate := range names {
			if name == strings.ToLower(candidate) {
				return phpquery.ArgumentExpression(call, index)
			}
		}
	}
	argument := phpquery.Argument(call, fallback)
	if argument == nil || phpquery.ArgumentName(argument) != "" {
		return nil
	}
	return phpquery.ArgumentExpression(call, fallback)
}

func phpStaticStringValue(node *phpsyntax.Node) string {
	if node == nil || node.Kind() != phpsyntax.PhpString {
		return ""
	}
	text := strings.TrimSpace(node.Text())
	if len(text) < 2 ||
		(text[0] != '\'' && text[0] != '"') ||
		text[len(text)-1] != text[0] ||
		text[0] == '"' && strings.Contains(text[1:len(text)-1], "$") {
		return ""
	}
	return phpquery.StringValue(node)
}

func twigStaticStringValue(node *twigsyntax.Node) string {
	if node == nil || !twigquery.StringIsStatic(node) {
		return ""
	}
	return twigquery.StringValue(node)
}

func ReferenceRange(reference Reference) cst.TextRange {
	if reference.Node == nil {
		return cst.TextRange{}
	}
	rng := reference.Node.RangeTrimmedTrivia()
	text := strings.TrimSpace(reference.Node.Text())
	if len(text) >= 2 &&
		(text[0] == '\'' || text[0] == '"' || text[0] == '`') &&
		text[len(text)-1] == text[0] {
		rng.Start++
		rng.End--
	}
	return rng
}
