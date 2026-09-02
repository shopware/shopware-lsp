package completion

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/theme"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigCompletionProvider struct {
	projectRoot               string
	twigIndexer               *twig.TwigIndexer
	iconProvider              *theme.IconProvider
	phpIndex                  *php.PHPIndex
	includeBuiltinCompletions bool
}

func NewTwigCompletionProvider(
	projectRoot string,
	twigIndexer *twig.TwigIndexer,
	extensionIndexer *extension.ExtensionIndexer,
	phpIndex *php.PHPIndex,
) *TwigCompletionProvider {
	return &TwigCompletionProvider{
		projectRoot:               projectRoot,
		twigIndexer:               twigIndexer,
		iconProvider:              theme.NewIconProvider(projectRoot, extensionIndexer),
		phpIndex:                  phpIndex,
		includeBuiltinCompletions: true,
	}
}

// WithoutBuiltinTwigCompletions keeps project and framework-aware Twig
// completions while leaving built-in language suggestions to the host IDE.
func (p *TwigCompletionProvider) WithoutBuiltinTwigCompletions() *TwigCompletionProvider {
	p.includeBuiltinCompletions = false
	return p
}

func (p *TwigCompletionProvider) GetCompletions(ctx context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	switch strings.ToLower(filepath.Ext(params.TextDocument.URI)) {
	case ".php":
		if params.Node == nil {
			return []protocol.CompletionItem{}
		}
		return p.phpCompletions(ctx, params)
	case ".twig":
		if items := p.twigTypesTagCompletions(params); items != nil {
			return items
		}
		if items := p.twigSeeCompletions(params); items != nil {
			return items
		}
		if items := p.twigGuardCompletions(params); items != nil {
			return items
		}
		if items := p.twigTestCompletions(params); items != nil {
			return items
		}
		if items := p.twigStatementCompletions(params); items != nil {
			return items
		}
		if items := p.twigTagCompletions(params); items != nil {
			return items
		}
		if params.Node == nil {
			return []protocol.CompletionItem{}
		}
		return p.twigCompletions(ctx, params)
	default:
		return []protocol.CompletionItem{}
	}
}

func (p *TwigCompletionProvider) twigCompletions(ctx context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {

	if twig.IsTwigTemplateString(params.Node) {
		files, _ := p.twigIndexer.GetAllTemplateFiles()

		var completionItems []protocol.CompletionItem
		for _, file := range files {
			completionItems = append(completionItems, protocol.CompletionItem{
				Label: file,
			})
		}

		return completionItems
	}

	if twigquery.IsFilterPosition(params.Node) {
		filters, _ := p.twigIndexer.GetAllTwigFilters()
		uniqueFilters := make(map[string]struct{})
		deprecations := make(map[string]twigCallableCompletionDeprecation)
		for _, filter := range filters {
			status, exists := deprecations[filter.Name]
			if !exists {
				status.all = true
			}
			status.all = status.all && filter.Deprecated
			if status.message == "" {
				status.message = filter.Deprecation
			}
			deprecations[filter.Name] = status
		}

		var completionItems []protocol.CompletionItem
		for _, filter := range filters {
			if strings.Contains(filter.Name, "*") {
				continue
			}

			if _, ok := uniqueFilters[filter.Name]; ok {
				continue
			}
			uniqueFilters[filter.Name] = struct{}{}

			item := protocol.CompletionItem{
				Label:            filter.Usage,
				InsertText:       filter.Name + "($0)",
				InsertTextFormat: int(protocol.SnippetTextFormat),
			}
			applyTwigCallableCompletionDeprecation(
				&item,
				"filter",
				deprecations[filter.Name],
			)
			completionItems = append(completionItems, item)
		}
		return completionItems
	}

	if twigquery.StringInTag(params.Node, "sw_icon") {
		cfg := twigquery.HashStringMap(params.Node)

		pack, ok := cfg["pack"]
		if !ok {
			pack = "default"
		}

		icons := p.iconProvider.GetIcons(pack)

		var completionItems []protocol.CompletionItem
		for _, icon := range icons {
			completionItems = append(completionItems, protocol.CompletionItem{
				Label: icon,
			})
		}
		return completionItems
	}

	if twigquery.StringIsHashValueForKey(params.Node, "pack") &&
		twigquery.TagName(twigquery.TagAt(params.Node)) == "sw_icon" {
		packs := p.iconProvider.GetIconPacks()

		var completionItems []protocol.CompletionItem
		for _, pack := range packs {
			completionItems = append(completionItems, protocol.CompletionItem{
				Label: pack,
			})
		}
		return completionItems
	}

	if items := p.twigOperatorCompletions(params); items != nil {
		return items
	}

	if items := p.twigMemberCompletions(params); items != nil {
		return items
	}

	if twigquery.ClosestNodeOfKind(params.Node, twigsyntax.TwigVar) != nil {
		functions, _ := p.twigIndexer.GetAllTwigFunctions()
		uniqueFunctions := make(map[string]struct{})
		deprecations := make(map[string]twigCallableCompletionDeprecation)
		for _, function := range functions {
			status, exists := deprecations[function.Name]
			if !exists {
				status.all = true
			}
			status.all = status.all && function.Deprecated
			if status.message == "" {
				status.message = function.Deprecation
			}
			deprecations[function.Name] = status
		}

		completionItems := p.twigVariableCompletions(
			params.TextDocument.URI,
			params.Root,
		)
		for _, function := range functions {
			if strings.Contains(function.Name, "*") {
				continue
			}

			if _, ok := uniqueFunctions[function.Name]; ok {
				continue
			}
			uniqueFunctions[function.Name] = struct{}{}

			item := protocol.CompletionItem{
				Label:            function.Usage,
				InsertText:       function.Name + "($0)",
				InsertTextFormat: int(protocol.SnippetTextFormat),
			}
			applyTwigCallableCompletionDeprecation(
				&item,
				"function",
				deprecations[function.Name],
			)
			completionItems = append(completionItems, item)
		}

		return completionItems
	}

	return []protocol.CompletionItem{}
}

var twigBuiltinOperators = []string{
	"or",
	"||",
	"and",
	"&&",
	"b-or",
	"b-xor",
	"b-and",
	"==",
	"!=",
	"<=>",
	"<",
	">",
	">=",
	"<=",
	"in",
	"not in",
	"matches",
	"starts with",
	"ends with",
	"===",
	"!==",
	"..",
	"+",
	"-",
	"~",
	"*",
	"/",
	"//",
	"%",
	"is",
	"is not",
	"**",
	"??",
	"?:",
	"not",
}

func (p *TwigCompletionProvider) twigOperatorCompletions(
	params *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if !twigOperatorCompletionPosition(params) {
		return nil
	}

	itemsByName := make(map[string]protocol.CompletionItem)
	if p.includeBuiltinCompletions {
		for _, name := range twigBuiltinOperators {
			itemsByName[name] = protocol.CompletionItem{
				Label:  name,
				Kind:   int(protocol.OperatorCompletion),
				Detail: "Built-in Twig operator",
			}
		}
	}
	if p.twigIndexer != nil {
		operators, _ := p.twigIndexer.GetAllTwigOperators()
		for _, operator := range operators {
			if strings.TrimSpace(operator.Name) == "" {
				continue
			}
			detail := "Custom Twig binary operator"
			if operator.Unary {
				detail = "Custom Twig unary operator"
			}
			if operator.Alias {
				detail += " alias"
			}
			item := protocol.CompletionItem{
				Label:  operator.Name,
				Kind:   int(protocol.OperatorCompletion),
				Detail: detail,
			}
			item.Documentation.Kind = string(protocol.Markdown)
			item.Documentation.Value = fmt.Sprintf(
				"Declared in `%s`.",
				filepath.Base(operator.FilePath),
			)
			itemsByName[operator.Name] = item
		}
	}

	items := make([]protocol.CompletionItem, 0, len(itemsByName))
	for _, item := range itemsByName {
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool {
		leftLabel := strings.ToLower(items[left].Label)
		rightLabel := strings.ToLower(items[right].Label)
		if leftLabel != rightLabel {
			return leftLabel < rightLabel
		}
		return items[left].Label < items[right].Label
	})
	return items
}

func twigOperatorCompletionPosition(params *lsp.CompletionRequest) bool {
	if params == nil || params.CompletionParams == nil ||
		params.LineIndex == nil || len(params.DocumentContent) == 0 {
		return false
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line),
		uint32(params.Position.Character),
	)
	if offset > uint32(len(params.DocumentContent)) {
		return false
	}
	prefix := string(params.DocumentContent[:offset])
	open := strings.LastIndex(prefix, "{%")
	if open < 0 || strings.LastIndex(prefix, "%}") > open {
		return false
	}
	if strings.LastIndex(prefix, "{{") > open ||
		strings.LastIndex(prefix, "{#") > open {
		return false
	}

	statement := strings.TrimLeftFunc(
		prefix[open+2:],
		func(value rune) bool {
			return unicode.IsSpace(value) || value == '-' || value == '~'
		},
	)
	if len(statement) < 2 ||
		!strings.EqualFold(statement[:2], "if") ||
		len(statement) > 2 && isTwigOperatorWordRune(rune(statement[2])) {
		return false
	}
	expression := statement[2:]
	if strings.TrimSpace(expression) == "" {
		return false
	}

	// When completion is requested while an operator fragment is being typed,
	// evaluate the expression before that fragment. A request directly inside
	// the first operand therefore remains excluded.
	lastRune, _ := utf8.DecodeLastRuneInString(expression)
	if !unicode.IsSpace(lastRune) {
		index := len(expression) - 1
		for index >= 0 && !unicode.IsSpace(rune(expression[index])) {
			index--
		}
		if index < 0 {
			return false
		}
		expression = expression[:index+1]
	}
	return twigExpressionCanAcceptOperator(expression)
}

func twigExpressionCanAcceptOperator(expression string) bool {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return false
	}
	fields := strings.Fields(strings.ToLower(expression))
	if len(fields) == 0 {
		return false
	}
	switch fields[len(fields)-1] {
	case "and", "or", "not", "is", "in", "matches", "starts", "ends",
		"with", "b-and", "b-or", "b-xor":
		return false
	}

	last, _ := utf8.DecodeLastRuneInString(expression)
	return unicode.IsLetter(last) ||
		unicode.IsDigit(last) ||
		last == '_' ||
		last == '\'' ||
		last == '"' ||
		last == ')' ||
		last == ']' ||
		last == '}'
}

func isTwigOperatorWordRune(value rune) bool {
	return unicode.IsLetter(value) ||
		unicode.IsDigit(value) ||
		value == '_'
}

type twigCallableCompletionDeprecation struct {
	all     bool
	message string
}

func applyTwigCallableCompletionDeprecation(
	item *protocol.CompletionItem,
	kind string,
	deprecation twigCallableCompletionDeprecation,
) {
	if item == nil || !deprecation.all {
		return
	}
	item.Deprecated = true
	item.Detail = "Deprecated Twig " + kind
	item.Documentation.Kind = string(protocol.Markdown)
	item.Documentation.Value = "**Deprecated Twig " + kind + "**"
	if deprecation.message != "" {
		item.Documentation.Value += "\n\n" + deprecation.message
	}
}

func (p *TwigCompletionProvider) twigVariableCompletions(
	uri string,
	root *twigsyntax.Node,
) []protocol.CompletionItem {
	variables := p.templateVariables(uri)
	declarations := twig.TwigTypeDeclarations(root)
	items := make(
		[]protocol.CompletionItem,
		0,
		len(variables)+len(declarations),
	)
	seen := make(map[string]struct{}, len(variables)+len(declarations))
	annotationNames := make([]string, 0, len(declarations))
	for name := range declarations {
		annotationNames = append(annotationNames, name)
	}
	sort.Slice(annotationNames, func(left, right int) bool {
		return strings.ToLower(annotationNames[left]) <
			strings.ToLower(annotationNames[right])
	})
	for _, name := range annotationNames {
		declaration := declarations[name]
		seen[name] = struct{}{}
		item := protocol.CompletionItem{
			Label:  name,
			Kind:   int(protocol.VariableCompletion),
			Detail: declaration.Type.String(),
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = "Declared by a Twig type annotation."
		if declaration.FromTypesTag {
			status := "Required"
			if declaration.Optional {
				status = "Optional"
			}
			item.Documentation.Value = status +
				" variable declared by the Twig `types` tag."
		}
		if declaration.Documentation != "" {
			item.Documentation.Value += "\n\n" +
				declaration.Documentation
		}
		items = append(items, item)
	}
	for _, variable := range variables {
		if _, exists := seen[variable.Name]; exists {
			continue
		}
		seen[variable.Name] = struct{}{}
		item := protocol.CompletionItem{
			Label:  variable.Name,
			Kind:   int(protocol.VariableCompletion),
			Detail: variable.Type,
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = fmt.Sprintf(
			"Provided by `%s` for `%s`.",
			filepath.Base(variable.File),
			variable.Template,
		)
		items = append(items, item)
	}
	if p.twigIndexer == nil {
		return items
	}
	globals, _ := p.twigIndexer.GetAllGlobals()
	for _, global := range effectiveTwigGlobals(globals) {
		if _, exists := seen[global.Name]; exists {
			continue
		}
		seen[global.Name] = struct{}{}
		detail := global.Type
		if detail == "" {
			detail = "mixed"
		}
		item := protocol.CompletionItem{
			Label:  global.Name,
			Kind:   int(protocol.VariableCompletion),
			Detail: detail,
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = fmt.Sprintf(
			"Workspace-wide Twig global from %s.",
			global.Source.String(),
		)
		if display := p.globalDisplayPath(global.File); display != "" {
			item.Documentation.Value += fmt.Sprintf(
				"\n\nDeclared in `%s`.",
				display,
			)
		}
		items = append(items, item)
	}
	return items
}

func (p *TwigCompletionProvider) twigMemberCompletions(
	params *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if params == nil || params.Node == nil {
		return nil
	}
	accessor := twigquery.ClosestNodeOfKind(
		params.Node,
		twigsyntax.TwigAccessor,
	)
	if accessor == nil {
		return nil
	}
	offset := params.LineIndex.OffsetUTF16(
		uint32(params.Position.Line),
		uint32(params.Position.Character),
	)
	if offset < accessor.Range().Start {
		return nil
	}
	localOffset := int(offset - accessor.Range().Start)
	source := accessor.Text()
	lastDot := strings.LastIndex(source, ".")
	if lastDot < 0 || localOffset <= lastDot {
		return nil
	}
	templatePath, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return nil
	}
	if twig.LoopAccessorInScope(accessor) {
		if loopType, found := twig.LoopContextType(p.phpIndex); found {
			return twigPHPMemberCompletions(
				p.phpIndex.SemanticSnapshot(),
				loopType,
			)
		}
		return twig3LoopCompletions()
	}
	if p.phpIndex == nil {
		return nil
	}
	receiver := (twig.PHPAccessResolver{
		PHP:  p.phpIndex,
		Twig: p.twigIndexer,
	}).AccessorReceiverType(templatePath, params.Root, accessor)
	if receiver.IsUnknown() {
		return nil
	}
	snapshot := p.phpIndex.SemanticSnapshot()
	return twigPHPMemberCompletions(snapshot, receiver)
}

func twig3LoopCompletions() []protocol.CompletionItem {
	names := twig.Twig3LoopVariables()
	items := make([]protocol.CompletionItem, 0, len(names))
	for _, name := range names {
		items = append(items, protocol.CompletionItem{
			Label:  name,
			Kind:   int(protocol.PropertyCompletion),
			Detail: "Twig 3 loop variable",
		})
	}
	return items
}

func (p *TwigCompletionProvider) globalDisplayPath(path string) string {
	if path == "" {
		return ""
	}
	display, err := filepath.Rel(p.projectRoot, path)
	if err != nil {
		display = path
	}
	return filepath.ToSlash(display)
}

func effectiveTwigGlobals(globals []twig.Global) []twig.Global {
	byName := make(map[string]twig.Global, len(globals))
	var order []string
	for _, global := range globals {
		if global.Name == "" {
			continue
		}
		existing, exists := byName[global.Name]
		if !exists {
			order = append(order, global.Name)
			byName[global.Name] = global
			continue
		}
		existing.Type = mergeTwigGlobalTypes(existing.Type, global.Type)
		byName[global.Name] = existing
	}
	result := make([]twig.Global, 0, len(order))
	for _, name := range order {
		result = append(result, byName[name])
	}
	return result
}

func mergeTwigGlobalTypes(left, right string) string {
	if left == "" {
		return right
	}
	if right == "" || left == right {
		return left
	}
	for _, value := range strings.Split(left, "|") {
		if value == right {
			return left
		}
	}
	return left + "|" + right
}

func (p *TwigCompletionProvider) templateVariables(
	uri string,
) []php.TwigTemplateVariable {
	if p.phpIndex == nil {
		return nil
	}
	path, err := uriutil.Path(uri)
	if err != nil {
		return nil
	}
	variables, err := p.phpIndex.TwigTemplateVariables(
		twig.TemplateNames(path)...,
	)
	if err != nil {
		return nil
	}
	return variables
}

func twigPHPMemberCompletions(
	snapshot *semantic.Snapshot,
	receiver types.Type,
) []protocol.CompletionItem {
	resolved := (resolver.MemberResolver{Snapshot: snapshot}).All(receiver)
	items := make(map[string]protocol.CompletionItem)
	for _, member := range resolved {
		symbol := member.Symbol
		if symbol.Visibility != semantic.Public ||
			symbol.Flags.Has(semantic.StaticFlag) {
			continue
		}
		switch symbol.Kind {
		case semantic.PropertySymbol:
			name := strings.TrimPrefix(symbol.Name, "$")
			item := protocol.CompletionItem{
				Label:      name,
				Kind:       int(protocol.PropertyCompletion),
				Detail:     member.Type.String(),
				Deprecated: symbol.Flags.Has(semantic.DeprecatedFlag),
			}
			applyTwigPHPMemberDocumentation(&item, symbol)
			items[strings.ToLower(name)] = item
		case semantic.MethodSymbol:
			lowerName := strings.ToLower(symbol.Name)
			if strings.HasPrefix(lowerName, "set") ||
				strings.HasPrefix(lowerName, "__") {
				continue
			}
			attribute := twig.TwigAttributeName(symbol.Name)
			if attribute != "" {
				key := strings.ToLower(attribute)
				if _, exists := items[key]; !exists {
					item := protocol.CompletionItem{
						Label:      attribute,
						Kind:       int(protocol.PropertyCompletion),
						Deprecated: symbol.Flags.Has(semantic.DeprecatedFlag),
						Detail: fmt.Sprintf(
							"%s via %s()",
							member.Type.String(),
							symbol.Name,
						),
					}
					applyTwigPHPMemberDocumentation(&item, symbol)
					items[key] = item
				}
				continue
			}
			key := strings.ToLower(symbol.Name)
			if _, exists := items[key]; exists {
				continue
			}
			items[key] = protocol.CompletionItem{
				Label:            symbol.Name,
				Kind:             int(protocol.MethodCompletion),
				Detail:           member.Type.String(),
				InsertText:       symbol.Name + "($0)",
				InsertTextFormat: int(protocol.SnippetTextFormat),
				Deprecated:       symbol.Flags.Has(semantic.DeprecatedFlag),
			}
			item := items[key]
			applyTwigPHPMemberDocumentation(&item, symbol)
			items[key] = item
		case semantic.ClassConstantSymbol, semantic.EnumCaseSymbol:
			kind := protocol.ConstantCompletion
			if symbol.Kind == semantic.EnumCaseSymbol {
				kind = protocol.EnumMemberCompletion
			}
			key := strings.ToLower(symbol.Name)
			if _, exists := items[key]; exists {
				continue
			}
			item := protocol.CompletionItem{
				Label:      symbol.Name,
				Kind:       int(kind),
				Detail:     member.Type.String(),
				Deprecated: symbol.Flags.Has(semantic.DeprecatedFlag),
			}
			applyTwigPHPMemberDocumentation(&item, symbol)
			items[key] = item
		}
	}
	result := make([]protocol.CompletionItem, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Label) <
			strings.ToLower(result[right].Label)
	})
	return result
}

func applyTwigPHPMemberDocumentation(
	item *protocol.CompletionItem,
	symbol semantic.Symbol,
) {
	if item == nil {
		return
	}
	var sections []string
	if symbol.Flags.Has(semantic.DeprecatedFlag) {
		sections = append(sections, "**Deprecated PHP member**")
	}
	if symbol.DocSummary() != "" {
		sections = append(sections, symbol.DocSummary())
	}
	if len(sections) == 0 {
		return
	}
	item.Documentation.Kind = string(protocol.Markdown)
	item.Documentation.Value = strings.Join(sections, "\n\n")
}

func twigMemberType(
	snapshot *semantic.Snapshot,
	receiver types.Type,
	name string,
) types.Type {
	for _, member := range (resolver.MemberResolver{
		Snapshot: snapshot,
	}).All(receiver) {
		symbol := member.Symbol
		if symbol.Visibility != semantic.Public ||
			symbol.Flags.Has(semantic.StaticFlag) {
			continue
		}
		switch symbol.Kind {
		case semantic.PropertySymbol:
			if strings.EqualFold(strings.TrimPrefix(symbol.Name, "$"), name) {
				return member.Type
			}
		case semantic.MethodSymbol:
			if strings.EqualFold(symbol.Name, name) ||
				strings.EqualFold(twig.TwigAttributeName(symbol.Name), name) {
				return member.Type
			}
		}
	}
	return types.Unknown()
}

func compactTwigAccessor(source string) string {
	return strings.Map(func(value rune) rune {
		if unicode.IsSpace(value) {
			return -1
		}
		return value
	}, source)
}

func isTwigIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for index, current := range value {
		if index == 0 {
			if current != '_' && !unicode.IsLetter(current) {
				return false
			}
		} else if current != '_' && !unicode.IsLetter(current) &&
			!unicode.IsDigit(current) {
			return false
		}
	}
	return true
}

func (p *TwigCompletionProvider) phpCompletions(ctx context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	if reference, found := php.AssistantArgumentReference(
		ctx,
		params.Node,
		"Template",
	); found {
		return p.assistantTemplateCompletions(params, reference)
	}
	if twig.IsPHPTemplateString(params.Node) {
		files, _ := p.twigIndexer.GetAllTemplateFiles()

		var completionItems []protocol.CompletionItem
		for _, file := range files {
			completionItems = append(completionItems, protocol.CompletionItem{
				Label: file,
			})
		}

		return completionItems
	}

	return []protocol.CompletionItem{}
}

func (p *TwigCompletionProvider) assistantTemplateCompletions(
	params *lsp.CompletionRequest,
	reference cst.TextRange,
) []protocol.CompletionItem {
	if p == nil || p.twigIndexer == nil {
		return nil
	}
	files, err := p.twigIndexer.GetAllTemplateFiles()
	if err != nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(files))
	var replacement protocol.Range
	hasReplacement := params != nil && params.LineIndex != nil
	if hasReplacement {
		replacement = namedArgumentCompletionRange(
			reference,
			params.LineIndex,
		)
	}
	for _, file := range files {
		item := protocol.CompletionItem{
			Label:  file,
			Kind:   int(protocol.FileCompletion),
			Detail: "Twig template",
		}
		if hasReplacement {
			item.TextEdit = protocol.TextEdit{
				Range:   replacement,
				NewText: file,
			}
		}
		items = append(items, item)
	}
	return items
}

func (p *TwigCompletionProvider) GetTriggerCharacters() []string {
	return []string{"\"", "'", "|"}
}
