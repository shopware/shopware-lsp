// Package query contains semantic Twig CST queries shared by LSP consumers and
// indexers. Queries use syntax kinds and typed AST accessors instead of relying
// on grammar-specific parent depths.
package query

import (
	"iter"
	"slices"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// Nodes returns every node in root's subtree whose kind is in kinds.
func Nodes(root *syntax.Node, kinds ...syntax.Kind) []*syntax.Node {
	if root == nil {
		return nil
	}

	var nodes []*syntax.Node
	appendNodes(&nodes, root, kinds)
	return nodes
}

// IterateNodes yields every node in root's subtree whose kind is in kinds.
// It preserves source order without materializing an intermediate slice and
// stops walking as soon as the caller breaks iteration.
func IterateNodes(
	root *syntax.Node,
	kinds ...syntax.Kind,
) iter.Seq[*syntax.Node] {
	return func(yield func(*syntax.Node) bool) {
		iterateNodes(root, kinds, yield)
	}
}

func iterateNodes(
	node *syntax.Node,
	kinds []syntax.Kind,
	yield func(*syntax.Node) bool,
) bool {
	if node == nil {
		return true
	}
	if slices.Contains(kinds, node.Kind()) && !yield(node) {
		return false
	}
	cursor := node.ChildNodeCursor()
	for cursor.Next() {
		if !iterateNodes(cursor.Node(), kinds, yield) {
			return false
		}
	}
	return true
}

func appendNodes(
	result *[]*syntax.Node,
	node *syntax.Node,
	kinds []syntax.Kind,
) {
	if slices.Contains(kinds, node.Kind()) {
		*result = append(*result, node)
	}
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*syntax.Node)
		if ok {
			appendNodes(result, child, kinds)
		}
	}
}

// AncestorOfKind returns the closest ancestor (including node) with kind.
func AncestorOfKind(node *syntax.Node, kind syntax.Kind) *syntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == kind {
			return current
		}
	}
	return nil
}

// ClosestNodeOfKind returns the closest node (including node) whose kind is in
// kinds.
func ClosestNodeOfKind(node *syntax.Node, kinds ...syntax.Kind) *syntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if slices.Contains(kinds, current.Kind()) {
			return current
		}
	}
	return nil
}

// LiteralStringAt returns the string literal containing node.
func LiteralStringAt(node *syntax.Node) *syntax.Node {
	return ClosestNodeOfKind(node, syntax.TwigLiteralString)
}

// FunctionName returns the name of a Twig function call.
func FunctionName(node *syntax.Node) string {
	call, ok := ast.CastTwigFunctionCall(node)
	if !ok {
		return ""
	}

	nameOperand, ok := call.NameOperand()
	if !ok {
		return ""
	}
	if name := firstLiteralName(nameOperand.Syntax()); name != "" {
		return name
	}
	return ""
}

// StringValue returns the unquoted contents of a Twig string literal.
func StringValue(node *syntax.Node) string {
	literal, ok := ast.CastTwigLiteralString(node)
	if !ok {
		return ""
	}
	if inner, ok := literal.GetInner(); ok {
		return inner.Syntax().Text()
	}
	return strings.Trim(strings.TrimSpace(node.Text()), "\"'`")
}

func StringIsStatic(node *syntax.Node) bool {
	literal := LiteralStringAt(node)
	if literal == nil {
		return false
	}
	for range IterateNodes(literal, syntax.TwigLiteralStringInterpolation) {
		return false
	}
	return true
}

// StringArgumentsInFunctions finds direct string arguments inside calls to any
// named function. Strings nested in hashes, arrays, or nested calls are not
// treated as arguments of the outer call.
func StringArgumentsInFunctions(root *syntax.Node, functionNames ...string) []*syntax.Node {
	var stringsInCalls []*syntax.Node
	for node := range IterateNodes(root, syntax.TwigFunctionCall) {
		if !slices.Contains(functionNames, FunctionName(node)) {
			continue
		}

		call, _ := ast.CastTwigFunctionCall(node)
		arguments, ok := call.Arguments()
		if !ok {
			continue
		}
		for literal := range IterateNodes(arguments.Syntax(), syntax.TwigLiteralString) {
			if isDirectStringValue(literal, arguments.Syntax()) {
				stringsInCalls = append(stringsInCalls, literal)
			}
		}
	}
	return stringsInCalls
}

// FunctionCallAt returns the function call containing node.
func FunctionCallAt(node *syntax.Node) *syntax.Node {
	return ClosestNodeOfKind(node, syntax.TwigFunctionCall)
}

// FunctionArgumentIndex returns the zero-based direct argument containing node.
func FunctionArgumentIndex(node *syntax.Node) int {
	call := FunctionCallAt(node)
	if call == nil {
		return -1
	}
	typedCall, _ := ast.CastTwigFunctionCall(call)
	arguments, ok := typedCall.Arguments()
	if !ok {
		return -1
	}

	var argument *syntax.Node
	for current := node; current != nil && current != arguments.Syntax(); current = current.Parent() {
		if current.Parent() == arguments.Syntax() {
			argument = current
			break
		}
	}
	if argument == nil {
		return -1
	}
	index := 0
	for child := range arguments.Syntax().ChildNodes() {
		if child == argument {
			return index
		}
		index++
	}
	return -1
}

// FunctionArgument returns a direct positional or named argument at index.
// The returned node is the argument expression itself, or the named-argument
// wrapper when the call uses name:/name= syntax.
func FunctionArgument(call *syntax.Node, index int) *syntax.Node {
	arguments := functionArguments(call)
	if arguments == nil || index < 0 {
		return nil
	}
	position := 0
	for child := range arguments.ChildNodes() {
		if position == index {
			return child
		}
		position++
	}
	return nil
}

// StringArgument returns a direct string argument at index.
func StringArgument(call *syntax.Node, index int) *syntax.Node {
	if call == nil || call.Kind() != syntax.TwigFunctionCall {
		return nil
	}
	for literal := range IterateNodes(call, syntax.TwigLiteralString) {
		if FunctionCallAt(literal) == call && FunctionArgumentIndex(literal) == index &&
			isDirectStringValue(literal, functionArguments(call)) {
			return literal
		}
	}
	return nil
}

func functionArguments(call *syntax.Node) *syntax.Node {
	typedCall, ok := ast.CastTwigFunctionCall(call)
	if !ok {
		return nil
	}
	arguments, ok := typedCall.Arguments()
	if !ok {
		return nil
	}
	return arguments.Syntax()
}

// StringInFunction reports whether node is inside a string argument to one of
// functionNames.
func StringInFunction(node *syntax.Node, functionNames ...string) bool {
	literal := LiteralStringAt(node)
	if literal == nil {
		return false
	}
	call := FunctionCallAt(literal)
	if call == nil || !slices.Contains(functionNames, FunctionName(call)) {
		return false
	}

	typedCall, _ := ast.CastTwigFunctionCall(call)
	arguments, ok := typedCall.Arguments()
	return ok &&
		isDescendantOf(literal, arguments.Syntax()) &&
		isDirectStringValue(literal, arguments.Syntax())
}

// FunctionNameAt reports the name of the function whose name contains node.
func FunctionNameAt(node *syntax.Node) string {
	call := FunctionCallAt(node)
	if call == nil {
		return ""
	}

	typedCall, _ := ast.CastTwigFunctionCall(call)
	name, ok := typedCall.NameOperand()
	if !ok || !isDescendantOf(node, name.Syntax()) {
		return ""
	}
	return FunctionName(call)
}

// FilterName returns the right-hand name of a Twig filter expression.
func FilterName(node *syntax.Node) string {
	filter, ok := ast.CastTwigFilter(node)
	if !ok {
		return ""
	}
	operand, ok := filter.Filter()
	if !ok {
		return ""
	}
	if name := firstLiteralName(operand.Syntax()); name != "" {
		return name
	}
	return ""
}

func firstLiteralName(node *syntax.Node) string {
	if node == nil {
		return ""
	}
	if literal, ok := ast.CastTwigLiteralName(node); ok &&
		literal.GetName() != nil {
		return literal.GetName().Text()
	}
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*syntax.Node)
		if !ok {
			continue
		}
		if name := firstLiteralName(child); name != "" {
			return name
		}
	}
	return ""
}

// StringInFilter reports whether node is a string literal on the left side of
// a filter with one of filterNames.
func StringInFilter(node *syntax.Node, filterNames ...string) bool {
	literal := LiteralStringAt(node)
	if literal == nil {
		return false
	}
	filterNode := ClosestNodeOfKind(literal, syntax.TwigFilter)
	if filterNode == nil || !slices.Contains(filterNames, FilterName(filterNode)) {
		return false
	}
	filter, _ := ast.CastTwigFilter(filterNode)
	operand, ok := filter.Operand()
	return ok &&
		isDescendantOf(literal, operand.Syntax()) &&
		isDirectStringValue(literal, operand.Syntax())
}

// IsFilterPosition reports whether node is in the right-hand filter operand,
// including an incomplete filter after the pipe.
func IsFilterPosition(node *syntax.Node) bool {
	filterNode := ClosestNodeOfKind(node, syntax.TwigFilter)
	if filterNode == nil {
		return false
	}
	filter, _ := ast.CastTwigFilter(filterNode)
	operand, ok := filter.Filter()
	if !ok {
		return false
	}
	if operand.Syntax().Range().Len() == 0 {
		return true
	}
	return isDescendantOf(node, operand.Syntax())
}

// TagAt returns the Twig tag-like node containing node.
func TagAt(node *syntax.Node) *syntax.Node {
	return ClosestNodeOfKind(node,
		syntax.TwigBlock,
		syntax.TwigExtends,
		syntax.TwigInclude,
		syntax.TwigUse,
		syntax.TwigEmbed,
		syntax.TwigFrom,
		syntax.TwigImport,
		syntax.TwigFormTheme,
		syntax.TwigAsseticStartingBlock,
		syntax.ShopwareTwigSwExtends,
		syntax.ShopwareTwigSwInclude,
		syntax.ShopwareIcon,
		syntax.ShopwareThumbnails,
	)
}

// TagName returns the source-level name of a supported Twig tag node.
func TagName(node *syntax.Node) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case syntax.TwigBlock:
		return "block"
	case syntax.TwigExtends:
		return "extends"
	case syntax.TwigInclude:
		return "include"
	case syntax.TwigUse:
		return "use"
	case syntax.TwigEmbed:
		return "embed"
	case syntax.TwigFrom:
		return "from"
	case syntax.TwigImport:
		return "import"
	case syntax.TwigFormTheme:
		return "form_theme"
	case syntax.TwigAsseticStartingBlock:
		cursor := node.ChildTokenCursor()
		for cursor.Next() {
			token := cursor.Token()
			if token.Kind() == syntax.TkWord {
				return token.Text()
			}
		}
		return ""
	case syntax.ShopwareTwigSwExtends:
		return "sw_extends"
	case syntax.ShopwareTwigSwInclude:
		return "sw_include"
	case syntax.ShopwareIcon:
		return "sw_icon"
	case syntax.ShopwareThumbnails:
		return "sw_thumbnails"
	default:
		return ""
	}
}

// StringInTag reports whether node is a direct string argument of one of
// tagNames. Strings nested in an option hash are excluded, so the icon name in
// `{% sw_icon 'home' {'pack': 'custom'} %}` matches while `pack` and `custom`
// do not.
func StringInTag(node *syntax.Node, tagNames ...string) bool {
	literal := LiteralStringAt(node)
	if literal == nil {
		return false
	}
	tag := TagAt(literal)
	if tag == nil || !slices.Contains(tagNames, TagName(tag)) {
		return false
	}
	if TagName(tag) == "form_theme" {
		return true
	}
	for current := literal.Parent(); current != nil && current != tag; current = current.Parent() {
		switch current.Kind() {
		case syntax.TwigLiteralHash,
			syntax.TwigLiteralArray,
			syntax.TwigFunctionCall,
			syntax.TwigBinaryExpression,
			syntax.TwigNamedArgument:
			return false
		}
	}
	return true
}

// BlockAt returns the Twig block containing node.
func BlockAt(node *syntax.Node) *syntax.Node {
	return ClosestNodeOfKind(node, syntax.TwigBlock)
}

// BlockName returns the declared name of a Twig block.
func BlockName(node *syntax.Node) string {
	block, ok := ast.CastTwigBlock(node)
	if !ok || block.Name() == nil {
		return ""
	}
	return block.Name().Text()
}

// HashStringMap extracts string-to-string entries from the closest Twig hash.
func HashStringMap(node *syntax.Node) map[string]string {
	hash := ClosestNodeOfKind(node, syntax.TwigLiteralHash)
	result := make(map[string]string)
	if hash == nil {
		scope := TagAt(node)
		if scope == nil {
			scope = node
		}
		for candidate := range IterateNodes(scope, syntax.TwigLiteralHash) {
			hash = candidate
			break
		}
		if hash == nil {
			return result
		}
	}

	for pair := range IterateNodes(hash, syntax.TwigLiteralHashPair) {
		var values []string
		for literal := range IterateNodes(pair, syntax.TwigLiteralString) {
			values = append(values, StringValue(literal))
		}
		if len(values) >= 2 {
			result[values[0]] = values[1]
		}
	}
	return result
}

func HashAt(node *syntax.Node) *syntax.Node {
	return ClosestNodeOfKind(node, syntax.TwigLiteralHash)
}

func HashKeyAt(node *syntax.Node) *syntax.Node {
	return ClosestNodeOfKind(node, syntax.TwigLiteralHashKey)
}

// StringIsHashValueForKey reports whether node is a string literal in a hash
// pair whose string key equals key.
func StringIsHashValueForKey(node *syntax.Node, key string) bool {
	literal := LiteralStringAt(node)
	pair := ClosestNodeOfKind(literal, syntax.TwigLiteralHashPair)
	if literal == nil || pair == nil {
		return false
	}
	var values [2]*syntax.Node
	count := 0
	for candidate := range IterateNodes(pair, syntax.TwigLiteralString) {
		values[count] = candidate
		count++
		if count == len(values) {
			break
		}
	}
	return count == len(values) && values[1] == literal && StringValue(values[0]) == key
}

// StartingHTMLTagAt returns the HTML start tag containing node.
func StartingHTMLTagAt(node *syntax.Node) *syntax.Node {
	return ClosestNodeOfKind(node, syntax.HtmlStartingTag)
}

// HTMLAttributeAt returns the HTML attribute containing node.
func HTMLAttributeAt(node *syntax.Node) *syntax.Node {
	return ClosestNodeOfKind(node, syntax.HtmlAttribute)
}

// HTMLTagName returns the name of an HTML start tag.
func HTMLTagName(node *syntax.Node) string {
	tag, ok := ast.CastHtmlStartingTag(node)
	if !ok || tag.Name() == nil {
		return ""
	}
	return tag.Name().Text()
}

// HTMLAttributeName returns the source-level attribute name, including common
// Vue binding prefixes.
func HTMLAttributeName(node *syntax.Node) string {
	attribute, ok := ast.CastHtmlAttribute(node)
	if !ok || attribute.Name() == nil {
		return ""
	}
	text := strings.TrimSpace(node.Text())
	if equal := strings.IndexByte(text, '='); equal >= 0 {
		text = text[:equal]
	}
	return strings.TrimSpace(text)
}

func isDescendantOf(node, ancestor *syntax.Node) bool {
	for current := node; current != nil; current = current.Parent() {
		if current == ancestor {
			return true
		}
	}
	return false
}

func isDirectStringValue(literal, scope *syntax.Node) bool {
	for current := literal.Parent(); current != nil && current != scope; current = current.Parent() {
		switch current.Kind() {
		case syntax.TwigLiteralHash, syntax.TwigLiteralArray, syntax.TwigFunctionCall:
			return false
		}
	}
	return isDescendantOf(literal, scope)
}
