package symfony

import (
	"context"
	"net/url"
	"strings"
	"unicode"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

var (
	phpRouteGeneratorMethods  = []string{"redirectToRoute", "generateUrl", "generate"}
	twigRouteGeneratorMethods = []string{"seoUrl", "url", "path"}
)

const (
	symfonyAbstractControllerClass = "Symfony\\Bundle\\FrameworkBundle\\Controller\\AbstractController"
	symfonyControllerClass         = "Symfony\\Bundle\\FrameworkBundle\\Controller\\Controller"
	symfonyControllerTrait         = "Symfony\\Bundle\\FrameworkBundle\\Controller\\ControllerTrait"
	symfonyURLGeneratorInterface   = "Symfony\\Component\\Routing\\Generator\\UrlGeneratorInterface"
)

func IsPHPRouteName(node *phpsyntax.Node) bool {
	literal := phpquery.StringAt(node)
	call := phpquery.CallAt(literal)
	return literal != nil && call != nil &&
		phpquery.ArgumentExpression(call, 0) == literal &&
		isPHPRouteGeneratorMethod(phpquery.CallMethodName(call))
}

// IsPHPRouteNameInContext applies the reference plugin's typed route-generator
// signatures whenever the PHP semantic document is available. The syntactic
// fallback keeps index-time parsing useful without accepting arbitrary method
// names in ordinary LSP requests.
func IsPHPRouteNameInContext(
	ctx context.Context,
	node *phpsyntax.Node,
) bool {
	if !IsPHPRouteName(node) {
		return false
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil ||
		phpContext.Snapshot == nil {
		return true
	}
	literal := phpquery.StringAt(node)
	call := phpquery.CallAt(literal)
	receiver := phpquery.CallReceiver(call)
	if receiver == nil {
		return false
	}
	receiverType := phpContext.Document.TypeOf(receiver).Type
	if receiverType.IsUnknown() {
		method := strings.ToLower(phpquery.CallMethodName(call))
		return strings.TrimSpace(receiver.Text()) == "$this" &&
			(method == "generateurl" || method == "redirecttoroute")
	}
	relations := phpContext.Snapshot.Relations()
	switch strings.ToLower(phpquery.CallMethodName(call)) {
	case "generate":
		return relations.IsSubtype(
			receiverType,
			types.Named(symfonyURLGeneratorInterface),
		)
	case "generateurl", "redirecttoroute":
		for _, className := range []string{
			symfonyAbstractControllerClass,
			symfonyControllerClass,
			symfonyControllerTrait,
		} {
			if relations.IsSubtype(
				receiverType,
				types.Named(className),
			) {
				return true
			}
		}
	}
	return false
}

func IsTwigRouteName(node *twigsyntax.Node) bool {
	if twigquery.StringInFunction(node, twigRouteGeneratorMethods...) {
		return true
	}
	_, found := TwigRouteComparisonReferenceAt(node)
	return found
}

// TwigRouteComparisonReference is a static route name compared with
// app.request.attributes.get('_route'). The range covers only string
// contents, which keeps diagnostics and editor replacements quote-safe.
type TwigRouteComparisonReference struct {
	Value string
	Node  *twigsyntax.Node
	Range cst.TextRange
}

// TwigRouteComparisonReferenceAt recognizes the exact route-comparison
// contexts supported by the Symfony plugin: equality/inequality, same-as
// tests, and membership arrays. Prefix/pattern checks intentionally remain
// ordinary strings because they do not identify one complete route name.
func TwigRouteComparisonReferenceAt(
	node *twigsyntax.Node,
) (TwigRouteComparisonReference, bool) {
	literal := twigquery.LiteralStringAt(node)
	if literal == nil || !twigquery.StringIsStatic(literal) {
		return TwigRouteComparisonReference{}, false
	}
	binary := twigquery.ClosestNodeOfKind(
		literal,
		twigsyntax.TwigBinaryExpression,
	)
	typed, ok := twigast.CastTwigBinaryExpression(binary)
	if !ok || typed.Operator() == nil {
		return TwigRouteComparisonReference{}, false
	}
	left, leftOK := typed.LhsExpression()
	right, rightOK := typed.RhsExpression()
	if !leftOK || !rightOK {
		return TwigRouteComparisonReference{}, false
	}
	operator := typed.Operator().Kind()
	leftNode := left.Syntax()
	rightNode := right.Syntax()
	matches := false
	switch operator {
	case twigsyntax.TkDoubleEqual,
		twigsyntax.TkTripleEqual,
		twigsyntax.TkExclamationMarkEquals,
		twigsyntax.TkExclamationMarkDoubleEquals:
		matches = twigRouteGetterExpression(leftNode) &&
			twigExpressionValue(rightNode) == literal
		if !matches {
			matches = twigRouteGetterExpression(rightNode) &&
				twigExpressionValue(leftNode) == literal
		}
	case twigsyntax.TkIs, twigsyntax.TkIsNot:
		call := twigExpressionValue(rightNode)
		matches = twigRouteGetterExpression(leftNode) &&
			call != nil &&
			call.Kind() == twigsyntax.TwigFunctionCall &&
			strings.EqualFold(twigquery.FunctionName(call), "same as") &&
			twigquery.StringArgument(call, 0) == literal
	case twigsyntax.TkIn, twigsyntax.TkNotIn:
		array := twigExpressionValue(rightNode)
		matches = twigRouteGetterExpression(leftNode) &&
			array != nil &&
			array.Kind() == twigsyntax.TwigLiteralArray &&
			twigquery.ClosestNodeOfKind(
				literal,
				twigsyntax.TwigLiteralArray,
			) == array
	}
	if !matches {
		return TwigRouteComparisonReference{}, false
	}
	value := twigquery.StringValue(literal)
	if !IsStaticRouteName(value) {
		return TwigRouteComparisonReference{}, false
	}
	return TwigRouteComparisonReference{
		Value: value,
		Node:  literal,
		Range: twigStringContentRange(literal),
	}, true
}

func TwigRouteComparisonReferences(
	root *twigsyntax.Node,
) []TwigRouteComparisonReference {
	var result []TwigRouteComparisonReference
	for _, literal := range twigquery.Nodes(
		root,
		twigsyntax.TwigLiteralString,
	) {
		reference, found := TwigRouteComparisonReferenceAt(literal)
		if found {
			result = append(result, reference)
		}
	}
	return result
}

func twigRouteGetterExpression(node *twigsyntax.Node) bool {
	value := twigExpressionValue(node)
	if value == nil || value.Kind() != twigsyntax.TwigFunctionCall {
		return false
	}
	argument := twigquery.StringArgument(value, 0)
	if argument == nil || twigquery.StringValue(argument) != "_route" {
		return false
	}
	compact := strings.Map(func(value rune) rune {
		if unicode.IsSpace(value) {
			return -1
		}
		return value
	}, value.Text())
	return compact == "app.request.attributes.get('_route')" ||
		compact == `app.request.attributes.get("_route")`
}

func twigExpressionValue(node *twigsyntax.Node) *twigsyntax.Node {
	for node != nil {
		switch node.Kind() {
		case twigsyntax.TwigExpression,
			twigsyntax.TwigOperand,
			twigsyntax.TwigParenthesesExpression:
			var child *twigsyntax.Node
			for candidate := range node.ChildNodes() {
				child = candidate
				break
			}
			node = child
		default:
			return node
		}
	}
	return nil
}

func twigStringContentRange(literal *twigsyntax.Node) cst.TextRange {
	if literal == nil {
		return cst.TextRange{}
	}
	for child := range literal.ChildNodes() {
		if child.Kind() == twigsyntax.TwigLiteralStringInner {
			return child.RangeTrimmedTrivia()
		}
	}
	return literal.RangeTrimmedTrivia()
}

type TwigHTMLRouteReference struct {
	Value     string
	Node      *cst.Node
	Container *cst.Node
}

type PHPRoutePathReference struct {
	Value string
	Range cst.TextRange
}

// JSRouteURLReference is a static request URL in one of the JavaScript
// contexts supported by the Symfony plugin. Range covers only string contents
// so completion can replace the URL without disturbing its quote style.
type JSRouteURLReference struct {
	Value string
	Node  *jssyntax.Node
	Range cst.TextRange
}

// JSRouteURLReferenceAt recognizes common browser and Axios request URL
// contexts: fetch()/axios()/axios.create()/Request() first arguments, `url`
// properties, and axios.create({baseURL: ...}). It deliberately excludes
// arbitrary strings and dynamic template literals.
func JSRouteURLReferenceAt(
	node *jssyntax.Node,
) (JSRouteURLReference, bool) {
	literal := jsquery.StringAt(node)
	if literal == nil {
		return JSRouteURLReference{}, false
	}
	text := literal.Text()
	if strings.HasPrefix(strings.TrimSpace(text), "`") &&
		strings.Contains(text, "${") {
		return JSRouteURLReference{}, false
	}

	accepted := false
	if call := jsquery.CallAt(literal); call != nil &&
		jsquery.StringArgumentIndex(literal) == 0 &&
		jsquery.ArgumentExpression(call, 0) == literal {
		switch strings.ToLower(jsquery.CallName(call)) {
		case "axios", "axios.create", "fetch", "request":
			accepted = true
		}
	}
	if !accepted {
		property := jsRoutePropertyAt(literal)
		if property != nil && jsquery.PropertyValue(property) == literal {
			switch strings.ToLower(jsquery.PropertyName(property)) {
			case "url":
				accepted = true
			case "baseurl":
				object := jsRouteAncestorOfKind(
					property,
					jssyntax.JsObject,
				)
				call := jsquery.CallAt(object)
				accepted = object != nil &&
					call != nil &&
					strings.EqualFold(
						jsquery.CallName(call),
						"axios.create",
					) &&
					jsquery.ArgumentExpression(call, 0) == object
			}
		}
	}
	if !accepted {
		return JSRouteURLReference{}, false
	}

	return JSRouteURLReference{
		Value: jsquery.StringValue(literal),
		Node:  literal,
		Range: jsStringContentRange(literal),
	}, true
}

func jsRoutePropertyAt(node *jssyntax.Node) *jssyntax.Node {
	return jsRouteAncestorOfKind(node, jssyntax.JsProperty)
}

func jsRouteAncestorOfKind(
	node *jssyntax.Node,
	kind jssyntax.Kind,
) *jssyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == kind {
			return current
		}
	}
	return nil
}

func jsStringContentRange(literal *jssyntax.Node) cst.TextRange {
	if literal == nil {
		return cst.TextRange{}
	}
	for element := range literal.Descendants() {
		token, ok := element.(*jssyntax.Token)
		if !ok || token.Kind() != jssyntax.TkString &&
			token.Kind() != jssyntax.TkTemplate {
			continue
		}
		rng := token.Range()
		text := token.Text()
		if len(text) != 0 &&
			(text[0] == '\'' || text[0] == '"' || text[0] == '`') {
			rng.Start++
			if len(text) > 1 && text[len(text)-1] == text[0] {
				rng.End--
			}
		}
		return rng
	}
	return literal.RangeTrimmedTrivia()
}

// PHPRoutePathReferenceAt recognizes the path/default argument of a PHP Route
// attribute. Other string arguments such as name, methods, requirements, and
// defaults are intentionally excluded.
func PHPRoutePathReferenceAt(
	node *phpsyntax.Node,
) (PHPRoutePathReference, bool) {
	literal := phpquery.StringAt(node)
	attribute := phpquery.AttributeAt(literal)
	if literal == nil || attribute == nil || !isRouteAttribute(attribute) {
		return PHPRoutePathReference{}, false
	}
	index := phpquery.ArgumentIndex(attribute, literal)
	if index < 0 || phpquery.ArgumentExpression(attribute, index) != literal {
		return PHPRoutePathReference{}, false
	}
	name := strings.ToLower(phpquery.ArgumentName(literal))
	if name != "path" && (name != "" || index != 0) {
		return PHPRoutePathReference{}, false
	}
	return PHPRoutePathReference{
		Value: phpquery.StringValue(literal),
		Range: phpquery.StringContentRange(literal),
	}, true
}

// PHPRouteNameReferenceAt recognizes only the named name argument of a native
// PHP Route attribute.
func PHPRouteNameReferenceAt(
	node *phpsyntax.Node,
) (PHPRoutePathReference, bool) {
	literal := phpquery.StringAt(node)
	attribute := phpquery.AttributeAt(literal)
	if literal == nil || attribute == nil || !isRouteAttribute(attribute) {
		return PHPRoutePathReference{}, false
	}
	index := phpquery.ArgumentIndex(attribute, literal)
	if index < 0 || phpquery.ArgumentExpression(attribute, index) != literal ||
		!strings.EqualFold(phpquery.ArgumentName(literal), "name") {
		return PHPRoutePathReference{}, false
	}
	return PHPRoutePathReference{
		Value: phpquery.StringValue(literal),
		Range: phpquery.StringContentRange(literal),
	}, true
}

// PHPRouteAnnotationPathReferenceAt recognizes the default or named path
// string of a legacy @Route docblock annotation. The parser is deliberately
// scoped to the open PHPDoc comment containing the cursor and does not accept
// other annotation arguments such as name, methods, or requirements.
func PHPRouteAnnotationPathReferenceAt(
	source string,
	offset uint32,
) (PHPRoutePathReference, bool) {
	if int(offset) > len(source) {
		return PHPRoutePathReference{}, false
	}
	cursor := int(offset)
	commentStart := strings.LastIndex(source[:cursor], "/**")
	if commentStart < 0 {
		return PHPRoutePathReference{}, false
	}
	if lastClose := strings.LastIndex(source[:cursor], "*/"); lastClose >
		commentStart {
		return PHPRoutePathReference{}, false
	}
	closeOffset := strings.Index(source[cursor:], "*/")
	commentEnd := len(source)
	if closeOffset >= 0 {
		commentEnd = cursor + closeOffset
	}
	annotationOpen := phpRouteAnnotationOpen(
		source,
		commentStart,
		cursor,
	)
	if annotationOpen < 0 {
		return PHPRoutePathReference{}, false
	}
	return phpRouteAnnotationStringAt(
		source,
		annotationOpen,
		commentEnd,
		cursor,
		"path",
	)
}

// PHPRouteAnnotationNameReferenceAt recognizes only a name= string from a
// legacy @Route docblock annotation.
func PHPRouteAnnotationNameReferenceAt(
	source string,
	offset uint32,
) (PHPRoutePathReference, bool) {
	if int(offset) > len(source) {
		return PHPRoutePathReference{}, false
	}
	cursor := int(offset)
	commentStart := strings.LastIndex(source[:cursor], "/**")
	if commentStart < 0 {
		return PHPRoutePathReference{}, false
	}
	if lastClose := strings.LastIndex(source[:cursor], "*/"); lastClose >
		commentStart {
		return PHPRoutePathReference{}, false
	}
	closeOffset := strings.Index(source[cursor:], "*/")
	commentEnd := len(source)
	if closeOffset >= 0 {
		commentEnd = cursor + closeOffset
	}
	annotationOpen := phpRouteAnnotationOpen(
		source,
		commentStart,
		cursor,
	)
	if annotationOpen < 0 {
		return PHPRoutePathReference{}, false
	}
	return phpRouteAnnotationStringAt(
		source,
		annotationOpen,
		commentEnd,
		cursor,
		"name",
	)
}

func phpRouteAnnotationOpen(source string, start, cursor int) int {
	result := -1
	for position := start; position < cursor; position++ {
		if source[position] != '(' {
			continue
		}
		tokenEnd := position
		for tokenEnd > start &&
			(source[tokenEnd-1] == ' ' || source[tokenEnd-1] == '\t') {
			tokenEnd--
		}
		tokenStart := tokenEnd
		for tokenStart > start &&
			phpRouteAnnotationNameCharacter(source[tokenStart-1]) {
			tokenStart--
		}
		token := source[tokenStart:tokenEnd]
		if token == "@Route" ||
			strings.HasSuffix(token, `\Route`) ||
			strings.HasSuffix(token, ".Route") {
			result = position
		}
	}
	return result
}

func phpRouteAnnotationNameCharacter(value byte) bool {
	return value == '@' || value == '\\' || value == '.' ||
		value == '_' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9'
}

func phpRouteAnnotationStringAt(
	source string,
	open,
	limit,
	cursor int,
	property string,
) (PHPRoutePathReference, bool) {
	depth := 0
	argumentIndex := 0
	segmentStart := open + 1
	for position := open + 1; position < limit; position++ {
		switch source[position] {
		case '\'', '"':
			quote := source[position]
			contentStart := position + 1
			contentEnd := contentStart
			for contentEnd < limit {
				if source[contentEnd] == quote &&
					(contentEnd == contentStart ||
						source[contentEnd-1] != '\\') {
					break
				}
				contentEnd++
			}
			if depth == 0 &&
				cursor >= contentStart && cursor <= contentEnd &&
				phpRouteAnnotationPathArgument(
					source[segmentStart:position],
					argumentIndex,
					property,
				) {
				return PHPRoutePathReference{
					Value: source[contentStart:contentEnd],
					Range: cst.TextRange{
						Start: uint32(contentStart),
						End:   uint32(contentEnd),
					},
				}, true
			}
			position = contentEnd
		case '(', '{', '[':
			depth++
		case ')':
			if depth == 0 {
				return PHPRoutePathReference{}, false
			}
			depth--
		case '}', ']':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				argumentIndex++
				segmentStart = position + 1
			}
		}
	}
	return PHPRoutePathReference{}, false
}

func phpRouteAnnotationPathArgument(
	prefix string,
	argumentIndex int,
	property string,
) bool {
	prefix = strings.ToLower(strings.ReplaceAll(
		strings.ReplaceAll(strings.TrimSpace(prefix), " ", ""),
		"\t",
		"",
	))
	if property == "name" {
		return prefix == "name="
	}
	return prefix == "path=" || prefix == "" && argumentIndex == 0
}

// PHPRouteNameSuggestion mirrors Symfony plugin route-name generation from a
// controller method. When the cursor is in a leading docblock rather than the
// method AST, the first following method is selected.
func PHPRouteNameSuggestion(
	root,
	node *phpsyntax.Node,
	offset uint32,
) string {
	method := phpquery.MethodAt(node)
	if method == nil {
		for _, class := range phpquery.Classes(root) {
			for _, candidate := range phpquery.Methods(class) {
				if candidate.Range().End < offset {
					continue
				}
				if method == nil ||
					candidate.Range().Start < method.Range().Start {
					method = candidate
				}
			}
		}
	}
	if method == nil {
		return ""
	}
	class := phpquery.ClassAt(method)
	className := phpquery.ClassName(class)
	methodName := phpquery.MethodName(method)
	if class == nil || className == "" || methodName == "" {
		return ""
	}
	methodName = strings.TrimSuffix(methodName, "Action")
	qualified := phpquery.Namespace(root)
	if qualified != "" {
		qualified += `\`
	}
	qualified += className
	rawParts := strings.Split(strings.Trim(qualified, `\`), `\`)
	parts := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		if part == "" || strings.EqualFold(part, "controller") {
			continue
		}
		part = strings.ToLower(part)
		if strings.HasSuffix(part, "bundle") && part != "bundle" {
			part = strings.TrimSuffix(part, "bundle")
		}
		if strings.HasSuffix(part, "controller") &&
			part != "controller" {
			part = strings.TrimSuffix(part, "controller")
		}
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 || methodName == "" {
		return ""
	}
	prefix := strings.Join(parts, "_")
	if !strings.HasPrefix(methodName, "_") {
		prefix += "_"
	}
	return prefix + strings.ToLower(methodName)
}

// PrefixAt returns the complete path prefix ending at the next slash after
// the cursor. This mirrors the Symfony plugin's segment navigation: placing
// the cursor inside an intermediate segment finds routes rooted at that path,
// while the final segment keeps ordinary symbol navigation quiet.
func (reference PHPRoutePathReference) PrefixAt(
	offset uint32,
) (string, bool) {
	if !reference.Range.Contains(offset) && offset != reference.Range.End {
		return "", false
	}
	relative := int(offset - reference.Range.Start)
	if relative < 0 {
		relative = 0
	}
	if relative > len(reference.Value) {
		relative = len(reference.Value)
	}
	slash := strings.IndexByte(reference.Value[relative:], '/')
	if slash < 0 {
		return "", false
	}
	prefix := reference.Value[:relative+slash]
	if len(prefix) < 3 ||
		strings.EqualFold(
			strings.Trim(prefix, "/"),
			strings.Trim(reference.Value, "/"),
		) {
		return "", false
	}
	return "/" + strings.Trim(prefix, "/"), true
}

// TwigHTMLRouteReferenceAt recognizes the two route-aware HTML contexts from
// the Symfony plugin: anchor href values and form action values.
func TwigHTMLRouteReferenceAt(
	node *twigsyntax.Node,
) (TwigHTMLRouteReference, bool) {
	htmlString := twigquery.ClosestNodeOfKind(
		node,
		twigsyntax.HtmlString,
	)
	if htmlString == nil {
		return TwigHTMLRouteReference{}, false
	}
	attribute := twigquery.HTMLAttributeAt(htmlString)
	startTag := twigquery.StartingHTMLTagAt(attribute)
	if attribute == nil || startTag == nil {
		return TwigHTMLRouteReference{}, false
	}
	tag := strings.ToLower(twigquery.HTMLTagName(startTag))
	name := strings.ToLower(twigquery.HTMLAttributeName(attribute))
	if tag != "a" || name != "href" {
		if tag != "form" || name != "action" {
			return TwigHTMLRouteReference{}, false
		}
	}
	valueNode := htmlString
	typed, _ := twigast.CastHtmlString(htmlString)
	if inner, found := typed.GetInner(); found {
		valueNode = inner.Syntax()
	}
	value := strings.TrimSpace(valueNode.Text())
	if strings.Contains(value, "{{") ||
		strings.Contains(value, "{%") ||
		strings.Contains(value, "{#") {
		return TwigHTMLRouteReference{}, false
	}
	return TwigHTMLRouteReference{
		Value:     value,
		Node:      valueNode,
		Container: htmlString,
	}, true
}

func TwigHTMLRouteReferences(root *twigsyntax.Node) []TwigHTMLRouteReference {
	var result []TwigHTMLRouteReference
	for _, htmlString := range twigquery.Nodes(root, twigsyntax.HtmlString) {
		if reference, found := TwigHTMLRouteReferenceAt(htmlString); found &&
			RouteURLPath(reference.Value) != "" {
			result = append(result, reference)
		}
	}
	return result
}

// NormalizeRouteSearchPath normalizes route lookup input without forcing a
// leading slash. Absolute and protocol-relative URLs are reduced to their raw
// path while query strings and fragments are discarded. Opaque values such as
// "foo:bar" remain unchanged so callers can decide whether they are meaningful
// in their own context.
func NormalizeRouteSearchPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	hasAuthorityPrefix := strings.Contains(value, "://") ||
		strings.HasPrefix(value, "//")

	// Go accepts braces in URL paths and EscapedPath would encode them. Route
	// placeholders must remain intact, so those inputs deliberately use the
	// same string fallback as syntactically invalid URLs.
	if !strings.ContainsAny(value, "{}") {
		if parsed, err := url.Parse(value); err == nil {
			path := parsed.EscapedPath()
			if hasAuthorityPrefix || parsed.Host != "" {
				if strings.TrimSpace(path) == "" {
					return "/"
				}
				return path
			}
			if !parsed.IsAbs() && strings.TrimSpace(path) != "" {
				return path
			}
		}
	}

	return normalizeRouteSearchPathFallback(value)
}

func normalizeRouteSearchPathFallback(value string) string {
	pathStart := -1
	schemeSeparator := strings.Index(value, "://")
	switch {
	case schemeSeparator >= 0:
		if offset := strings.IndexByte(
			value[schemeSeparator+3:],
			'/',
		); offset >= 0 {
			pathStart = schemeSeparator + 3 + offset
		}
	case strings.HasPrefix(value, "//"):
		if offset := strings.IndexByte(value[2:], '/'); offset >= 0 {
			pathStart = 2 + offset
		}
	}
	switch {
	case pathStart >= 0:
		value = value[pathStart:]
	case schemeSeparator >= 0 || strings.HasPrefix(value, "//"):
		value = "/"
	}

	pathEnd := len(value)
	if query := strings.IndexByte(value, '?'); query >= 0 {
		pathEnd = min(pathEnd, query)
	}
	if fragment := strings.IndexByte(value, '#'); fragment >= 0 {
		pathEnd = min(pathEnd, fragment)
	}
	return value[:pathEnd]
}

// RouteURLPath normalizes a static HTML URL for route matching. Query strings
// and fragments do not participate in Symfony route selection. Hierarchical
// absolute URLs are supported, while fragment-only links and opaque schemes
// such as mailto:, tel:, and javascript: remain external.
func RouteURLPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "#") {
		return ""
	}
	parsed, err := url.Parse(value)
	if err == nil && parsed.Scheme != "" && parsed.Opaque != "" {
		return ""
	}
	path := NormalizeRouteSearchPath(value)
	if path == "" {
		return ""
	}
	return "/" + strings.TrimLeft(path, "/")
}

func PHPRouteParameterContext(node *phpsyntax.Node) (string, bool) {
	key := phpquery.StringAt(node)
	array := phpquery.ArrayAt(node)
	item := phpquery.ArrayItemAt(node)
	if key == nil || array == nil || item == nil || phpquery.ArrayItemKey(item) != key {
		return "", false
	}
	call := phpquery.CallAt(array)
	if call == nil || !isPHPRouteGeneratorMethod(phpquery.CallMethodName(call)) {
		return "", false
	}
	if phpquery.ArgumentExpression(call, 1) != array {
		return "", false
	}
	routeName := phpquery.StringValue(phpquery.StringArgument(call, 0))
	return routeName, routeName != ""
}

// PHPRouteParameterReferenceAt returns the static route and parameter names
// for a quoted key in the second argument of a PHP route-generator call.
func PHPRouteParameterReferenceAt(
	node *phpsyntax.Node,
) (string, string, bool) {
	routeName, ok := PHPRouteParameterContext(node)
	if !ok {
		return "", "", false
	}
	key := phpquery.StringAt(node)
	parameter := phpquery.StringValue(key)
	if parameter == "" {
		return "", "", false
	}
	return routeName, parameter, true
}

func TwigRouteParameterContext(node *twigsyntax.Node) (string, bool) {
	hash := twigquery.HashAt(node)
	if hash == nil {
		return "", false
	}
	call := twigquery.FunctionCallAt(hash)
	if call == nil || !isTwigRouteGeneratorMethod(twigquery.FunctionName(call)) ||
		twigquery.FunctionArgumentIndex(hash) != 1 {
		return "", false
	}
	if pair := twigquery.ClosestNodeOfKind(node, twigsyntax.TwigLiteralHashPair); pair != nil &&
		twigquery.HashKeyAt(node) == nil {
		return "", false
	}
	routeName := twigquery.StringValue(twigquery.StringArgument(call, 0))
	return routeName, routeName != ""
}

// TwigRouteParameterReferenceAt returns quoted, unquoted, and shorthand hash
// keys from the second argument of path()/url() without mistaking their value
// expressions for route parameters.
func TwigRouteParameterReferenceAt(
	node *twigsyntax.Node,
) (string, string, bool) {
	routeName, ok := TwigRouteParameterContext(node)
	if !ok {
		return "", "", false
	}
	key := twigquery.HashKeyAt(node)
	if key == nil {
		return "", "", false
	}
	parameter := ""
	if literal := twigquery.LiteralStringAt(node); literal != nil &&
		twigNodeInside(literal, key) {
		parameter = twigquery.StringValue(literal)
	} else {
		parameter = strings.TrimSpace(key.Text())
		parameter = strings.TrimSpace(strings.TrimSuffix(parameter, ":"))
		parameter = strings.Trim(parameter, "'\"`")
	}
	if !routeParameterName(parameter) {
		return "", "", false
	}
	return routeName, parameter, true
}

func twigNodeInside(node, ancestor *twigsyntax.Node) bool {
	for current := node; current != nil; current = current.Parent() {
		if current == ancestor {
			return true
		}
	}
	return false
}

func routeParameterName(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		if !isRouteParameterCharacter(value[index], index == 0) {
			return false
		}
	}
	return true
}

// IsStaticRouteName excludes dynamic/interpolated values and controller FQCN
// shortcuts, which require PHP receiver/class resolution instead of an index
// lookup and must not produce false missing-route diagnostics.
func IsStaticRouteName(name string) bool {
	return name != "" &&
		!strings.ContainsAny(name, "$\\") &&
		!strings.Contains(name, "#{") &&
		!strings.Contains(name, "{{")
}

func isPHPRouteGeneratorMethod(name string) bool {
	for _, candidate := range phpRouteGeneratorMethods {
		if name == candidate {
			return true
		}
	}
	return false
}

func isTwigRouteGeneratorMethod(name string) bool {
	for _, candidate := range twigRouteGeneratorMethods {
		if name == candidate {
			return true
		}
	}
	return false
}
