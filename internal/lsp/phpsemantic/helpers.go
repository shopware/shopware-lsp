package phpsemantic

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func memberExpression(node *phpsyntax.Node) (*phpsyntax.Node, bool) {
	for current := node; current != nil; current = current.Parent() {
		switch current.Kind() {
		case phpsyntax.PhpScopedCall, phpsyntax.PhpScopedAccess:
			return current, true
		case phpsyntax.PhpMemberCall:
			return current, false
		case phpsyntax.PhpMemberAccess:
			static := false
			for index := 0; index < current.ChildCount(); index++ {
				child := current.Child(index)
				token, ok := child.(*phpsyntax.Token)
				if ok && token.Kind() == phpsyntax.TkScopeResolution {
					static = true
				}
			}
			return current, static
		}
	}
	return nil, false
}

func directNodes(node *phpsyntax.Node) []*phpsyntax.Node {
	var result []*phpsyntax.Node
	if node == nil {
		return result
	}
	for child := range node.ChildNodes() {
		result = append(result, child)
	}
	return result
}

func firstDirectKind(node *phpsyntax.Node, kind phpsyntax.Kind) *phpsyntax.Node {
	for _, child := range directNodes(node) {
		if child.Kind() == kind {
			return child
		}
	}
	return nil
}

func objectCreationAt(node *phpsyntax.Node) *phpsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == phpsyntax.PhpObjectCreation {
			return current
		}
	}
	return nil
}

func activeArgument(call *phpsyntax.Node, offset uint32) (int, string) {
	arguments := phpquery.Arguments(call)
	if len(arguments) == 0 {
		return 0, ""
	}
	for index, argument := range arguments {
		rng := argument.Range()
		if offset <= rng.End {
			return index, phpquery.ArgumentName(argument)
		}
	}
	last := arguments[len(arguments)-1]
	return len(arguments), phpquery.ArgumentName(last)
}

func nameContextAt(document *semantic.Document, offset uint32) resolver.NameContext {
	context := resolver.NewNameContext(document.Namespace)
	if scope, ok := document.ScopeAt(offset); ok {
		context.Namespace = scope.Namespace
		context.Imports = scope.Imports
	}
	return context
}

func staticReceiverType(
	phpContext *php.PHPContext,
	name string,
	offset uint32,
) types.Type {
	switch strings.ToLower(name) {
	case "self", "static":
		if phpContext.InsideClass != nil {
			return types.Named(phpContext.InsideClass.FullyQualified)
		}
	case "parent":
		if phpContext.InsideClass != nil && len(phpContext.InsideClass.Extends) > 0 {
			return types.Named(phpContext.InsideClass.Extends[0])
		}
	default:
		return types.Named(
			nameContextAt(phpContext.Document, offset).ResolveClass(name),
		)
	}
	return types.Unknown()
}

func memberVisible(phpContext *php.PHPContext, symbol semantic.Symbol) bool {
	if symbol.Visibility == semantic.Public {
		return true
	}
	if phpContext.InsideClass == nil {
		return false
	}
	if symbol.Container == phpContext.InsideClass.ID {
		return true
	}
	if symbol.Visibility == semantic.Private {
		return false
	}
	container, ok := phpContext.Snapshot.Symbol(symbol.Container)
	return ok && phpContext.Snapshot.IsSubtypeOf(
		phpContext.InsideClass.FullyQualified,
		container.FullyQualified,
	)
}

func completionLabel(symbol semantic.Symbol) string {
	if symbol.Kind == semantic.PropertySymbol {
		return "$" + strings.TrimPrefix(symbol.Name, "$")
	}
	return symbol.Name
}

func completionKind(symbol semantic.Symbol) int {
	switch symbol.Kind {
	case semantic.MethodSymbol:
		return int(protocol.MethodCompletion)
	case semantic.FunctionSymbol:
		return int(protocol.FunctionCompletion)
	case semantic.PropertySymbol:
		return int(protocol.PropertyCompletion)
	case semantic.ClassSymbol:
		return int(protocol.ClassCompletion)
	case semantic.InterfaceSymbol:
		return int(protocol.InterfaceCompletion)
	case semantic.EnumSymbol:
		return int(protocol.EnumCompletion)
	case semantic.ClassConstantSymbol, semantic.GlobalConstantSymbol:
		return int(protocol.ConstantCompletion)
	case semantic.EnumCaseSymbol:
		return int(protocol.EnumMemberCompletion)
	case semantic.ParameterSymbol, semantic.LocalSymbol:
		return int(protocol.VariableCompletion)
	default:
		return int(protocol.TextCompletion)
	}
}

func completionRank(symbol semantic.Symbol) int {
	switch symbol.Kind {
	case semantic.MethodSymbol:
		return 0
	case semantic.PropertySymbol:
		return 1
	default:
		return 2
	}
}

func completionSnippet(symbol semantic.Symbol) string {
	if !symbol.IsFunctionLike() {
		return symbol.Name
	}
	var arguments []string
	position := 1
	for _, parameter := range symbol.Parameters {
		if parameter.Optional {
			continue
		}
		name := strings.TrimPrefix(parameter.Name, "$")
		arguments = append(arguments, fmt.Sprintf("${%d:%s}", position, name))
		position++
	}
	return symbol.Name + "(" + strings.Join(arguments, ", ") + ")"
}

func formatSymbol(symbol semantic.Symbol) string {
	switch symbol.Kind {
	case semantic.ClassSymbol, semantic.InterfaceSymbol,
		semantic.TraitSymbol, semantic.EnumSymbol:
		var modifiers []string
		if symbol.Flags.Has(semantic.FinalFlag) {
			modifiers = append(modifiers, "final")
		}
		if symbol.Flags.Has(semantic.AbstractFlag) {
			modifiers = append(modifiers, "abstract")
		}
		if symbol.Flags.Has(semantic.ReadonlyFlag) {
			modifiers = append(modifiers, "readonly")
		}
		kind := map[semantic.SymbolKind]string{
			semantic.ClassSymbol:     "class",
			semantic.InterfaceSymbol: "interface",
			semantic.TraitSymbol:     "trait",
			semantic.EnumSymbol:      "enum",
		}[symbol.Kind]
		modifiers = append(modifiers, kind, symbol.FullyQualified)
		if len(symbol.Extends) > 0 {
			modifiers = append(modifiers, "extends", strings.Join(symbol.Extends, ", "))
		}
		if len(symbol.Implements) > 0 {
			modifiers = append(modifiers, "implements", strings.Join(symbol.Implements, ", "))
		}
		return strings.Join(modifiers, " ")
	case semantic.MethodSymbol, semantic.FunctionSymbol:
		var prefix []string
		if symbol.Kind == semantic.MethodSymbol {
			prefix = append(prefix, visibilityName(symbol.Visibility))
			if symbol.Flags.Has(semantic.StaticFlag) {
				prefix = append(prefix, "static")
			}
		}
		prefix = append(prefix, "function")
		name := symbol.Name
		if symbol.Kind == semantic.FunctionSymbol {
			name = symbol.FullyQualified
		}
		parameters := make([]string, 0, len(symbol.Parameters))
		for _, parameter := range symbol.Parameters {
			parameters = append(parameters, formatParameter(parameter))
		}
		signature := strings.Join(prefix, " ") + " " + name +
			"(" + strings.Join(parameters, ", ") + ")"
		if !symbol.ReturnType.IsUnknown() {
			signature += ": " + symbol.ReturnType.String()
		}
		return signature
	case semantic.PropertySymbol:
		result := visibilityName(symbol.Visibility) + " "
		if symbol.HasWriteVisibility {
			result += visibilityName(symbol.WriteVisibility) + "(set) "
		}
		if symbol.Flags.Has(semantic.StaticFlag) {
			result += "static "
		}
		if symbol.Flags.Has(semantic.ReadonlyFlag) {
			result += "readonly "
		}
		if !symbol.Type.IsUnknown() {
			result += symbol.Type.String() + " "
		}
		return result + "$" + strings.TrimPrefix(symbol.Name, "$")
	case semantic.ParameterSymbol, semantic.LocalSymbol:
		if symbol.Type.IsUnknown() {
			return symbol.Name
		}
		return symbol.Type.String() + " " + symbol.Name
	case semantic.ClassConstantSymbol, semantic.GlobalConstantSymbol:
		result := "const " + symbol.Name
		if !symbol.Type.IsUnknown() {
			result += ": " + symbol.Type.String()
		}
		return result
	case semantic.EnumCaseSymbol:
		return "case " + symbol.Name
	default:
		return symbol.Name
	}
}

func formatParameter(parameter semantic.Parameter) string {
	var result string
	if !parameter.Type.IsUnknown() {
		result += parameter.Type.String() + " "
	}
	if parameter.Flags.Has(semantic.ByReferenceFlag) {
		result += "&"
	}
	if parameter.Flags.Has(semantic.VariadicFlag) {
		result += "..."
	}
	result += parameter.Name
	if parameter.Optional {
		result += " = ..."
	}
	return result
}

func visibilityName(visibility semantic.Visibility) string {
	switch visibility {
	case semantic.Protected:
		return "protected"
	case semantic.Private:
		return "private"
	default:
		return "public"
	}
}

func typeDetail(value types.Type) string {
	if value.IsUnknown() {
		return ""
	}
	return value.String()
}

func symbolRangeAt(document *semantic.Document, offset uint32) cst.TextRange {
	if reference, ok := php.ReferenceAt(document, offset); ok {
		return reference.Range
	}
	for _, symbol := range document.Symbols {
		if symbol.SelectionRange.Contains(offset) || offset == symbol.SelectionRange.End {
			return symbol.SelectionRange
		}
	}
	return cst.TextRange{Start: offset, End: offset}
}

func rangeFromText(index *cst.LineIndex, textRange cst.TextRange) *protocol.Range {
	if index == nil {
		return nil
	}
	startLine, startCharacter := index.PositionUTF16(textRange.Start)
	endLine, endCharacter := index.PositionUTF16(textRange.End)
	return &protocol.Range{
		Start: protocol.Position{Line: int(startLine), Character: int(startCharacter)},
		End:   protocol.Position{Line: int(endLine), Character: int(endCharacter)},
	}
}

func locationsForSymbols(
	symbols []semantic.Symbol,
	current *lsp.TextDocument,
) []protocol.Location {
	cache := newLocationCache(current)
	seen := make(map[semantic.SymbolID]struct{}, len(symbols))
	var result []protocol.Location
	for _, symbol := range symbols {
		if _, exists := seen[symbol.ID]; exists {
			continue
		}
		seen[symbol.ID] = struct{}{}
		result = append(result, cache.symbol(symbol))
	}
	return result
}

type locationCache struct {
	current *lsp.TextDocument
	indexes map[string]*cst.LineIndex
	sources map[string][]byte
}

func newLocationCache(current *lsp.TextDocument) *locationCache {
	return &locationCache{
		current: current,
		indexes: make(map[string]*cst.LineIndex),
		sources: make(map[string][]byte),
	}
}

func (c *locationCache) symbol(symbol semantic.Symbol) protocol.Location {
	return c.textRange(symbol.Path, symbol.SelectionRange)
}

func (c *locationCache) textRange(path string, textRange cst.TextRange) protocol.Location {
	uri := uriutil.FileURI(path)
	if strings.HasPrefix(path, "phpstub://") {
		uri = path
	}
	index := c.lineIndex(path)
	rng := rangeFromText(index, textRange)
	if rng == nil {
		rng = &protocol.Range{}
	}
	return protocol.Location{URI: uri, Range: *rng}
}

func (c *locationCache) lineIndex(path string) *cst.LineIndex {
	if index, exists := c.indexes[path]; exists {
		return index
	}
	if c.current != nil {
		currentPath, _ := uriutil.Path(c.current.URI)
		if filepath.Clean(currentPath) == filepath.Clean(path) {
			c.indexes[path] = c.current.LineIndex
			c.sources[path] = c.current.Text
			return c.current.LineIndex
		}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	index := cst.NewLineIndex(string(content))
	c.indexes[path] = index
	c.sources[path] = content
	return index
}

func (c *locationCache) renameText(
	path string,
	textRange cst.TextRange,
	newName string,
) string {
	_ = c.lineIndex(path)
	source := c.sources[path]
	if int(textRange.End) <= len(source) && textRange.Start < textRange.End {
		original := source[textRange.Start:textRange.End]
		if len(original) > 0 && original[0] == '$' {
			return "$" + newName
		}
	}
	return newName
}

func compactName(source string) string {
	return strings.Join(strings.Fields(source), "")
}

func isImplicitVariable(name string) bool {
	switch name {
	case "$this", "$GLOBALS", "$_SERVER", "$_GET", "$_POST", "$_FILES",
		"$_COOKIE", "$_SESSION", "$_REQUEST", "$_ENV", "$argc", "$argv":
		return true
	default:
		return false
	}
}

func isPHP(uri string) bool {
	return strings.EqualFold(filepath.Ext(uri), ".php")
}

func validPHPIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		first := index == 0
		if character == '_' || character >= 0x80 ||
			character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			!first && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}
