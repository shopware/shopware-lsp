package phpsemantic

import (
	"context"
	"fmt"
	"sort"
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
)

func (p *Provider) GetCompletions(
	ctx context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if !isPHP(request.TextDocument.URI) || request.Root == nil {
		return nil
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil || phpContext.Snapshot == nil {
		return nil
	}
	if mode, reference, found := assistantClassReference(
		ctx,
		request.Node,
	); found {
		return p.assistantClassCompletions(
			mode,
			reference,
			request.LineIndex,
		)
	}
	if items := expectedArgumentCompletions(phpContext, request); len(items) > 0 {
		return items
	}
	if completionSuppressed(request) {
		return nil
	}

	if expression, static := memberExpression(request.Node); expression != nil {
		nodes := directNodes(expression)
		if len(nodes) > 0 {
			receiver := phpContext.Document.TypeOf(nodes[0]).Type
			if receiver.IsUnknown() && static && nodes[0].Kind() == phpsyntax.PhpName {
				receiver = staticReceiverType(
					phpContext,
					compactName(nodes[0].Text()),
					nodes[0].Range().Start,
				)
			}
			if !receiver.IsUnknown() {
				return p.memberCompletions(phpContext, receiver, static)
			}
		}
	}
	return p.scopeCompletions(
		phpContext,
		request,
		!completionIsVariableOnly(request),
	)
}

func expectedArgumentCompletions(
	phpContext *php.PHPContext,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if phpContext == nil || phpContext.Document == nil ||
		phpContext.Snapshot == nil || request == nil || request.LineIndex == nil {
		return nil
	}
	call := phpquery.CallAt(request.Node)
	constructor := false
	if call == nil {
		call = objectCreationAt(request.Node)
		constructor = call != nil
	}
	if call == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	argumentIndex, argumentName := activeArgument(call, offset)
	var values []semantic.CallValue
	visit := func(contract semantic.CallContract) bool {
		for _, expected := range contract.ExpectedArguments {
			if int(expected.Argument) == argumentIndex {
				values = append(values, expected.Values...)
			}
		}
		return true
	}
	visitSymbol := func(symbol semantic.Symbol) {
		parameter, ok := callableParameterAt(
			symbol.Parameters,
			argumentIndex,
			argumentName,
		)
		if !ok {
			return
		}
		attribute, ok := semantic.AttributeNamed(
			parameter.Attributes,
			"ExpectedValues",
		)
		if !ok {
			return
		}
		values = appendExpectedValueAttributeValues(
			values,
			attribute,
			phpContext.Snapshot,
		)
	}
	if constructor {
		nameNode := firstDirectKind(call, phpsyntax.PhpName)
		if nameNode == nil {
			return nil
		}
		className := nameContextAt(
			phpContext.Document,
			nameNode.Range().Start,
		).ResolveClass(compactName(nameNode.Text()))
		phpContext.Snapshot.VisitMethodCallContracts(
			types.Named(className),
			"__construct",
			visit,
		)
		for _, member := range (resolver.MemberResolver{
			Snapshot: phpContext.Snapshot,
		}).Methods(types.Named(className), "__construct") {
			visitSymbol(member.Symbol)
		}
	} else {
		nodes := directNodes(call)
		switch call.Kind() {
		case phpsyntax.PhpFunctionCall:
			if len(nodes) == 0 || nodes[0].Kind() != phpsyntax.PhpName {
				return nil
			}
			nameContextAt(
				phpContext.Document,
				call.Range().Start,
			).VisitFunctionNames(compactName(nodes[0].Text()), func(name string) bool {
				phpContext.Snapshot.VisitFunctionCallContracts(name, visit)
				phpContext.Snapshot.VisitFunctionViews(
					name,
					func(view semantic.SymbolView) bool {
						visitSymbol(view.Materialize())
						return true
					},
				)
				return true
			})
		case phpsyntax.PhpMemberCall, phpsyntax.PhpScopedCall:
			if len(nodes) < 2 {
				return nil
			}
			static := call.Kind() == phpsyntax.PhpScopedCall
			receiver := phpContext.Document.TypeOf(nodes[0]).Type
			if receiver.IsUnknown() && static && nodes[0].Kind() == phpsyntax.PhpName {
				receiver = staticReceiverType(
					phpContext,
					compactName(nodes[0].Text()),
					nodes[0].Range().Start,
				)
			}
			phpContext.Snapshot.VisitMethodCallContracts(
				receiver,
				compactName(nodes[1].Text()),
				visit,
			)
			for _, member := range (resolver.MemberResolver{
				Snapshot: phpContext.Snapshot,
			}).Methods(receiver, compactName(nodes[1].Text())) {
				visitSymbol(member.Symbol)
			}
		}
	}
	if len(values) == 0 {
		return nil
	}
	replacement := cst.TextRange{Start: offset, End: offset}
	if expression := phpquery.ArgumentExpression(call, argumentIndex); expression != nil &&
		offset >= expression.Range().Start && offset <= expression.Range().End {
		replacement = expression.RangeTrimmedTrivia()
	}
	textRange := *rangeFromText(request.LineIndex, replacement)
	seen := make(map[string]struct{}, len(values))
	items := make([]protocol.CompletionItem, 0, len(values))
	for _, value := range values {
		if value.Expression == "" {
			continue
		}
		key := strings.ToLower(value.Expression)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		kind := protocol.ConstantCompletion
		if value.Kind == semantic.CallValueString ||
			value.Kind == semantic.CallValueNumber {
			kind = protocol.ValueCompletion
		}
		items = append(items, protocol.CompletionItem{
			Label:    value.Label(),
			Kind:     int(kind),
			Detail:   "Expected by PHP metadata",
			SortText: "00-" + value.Label(),
			TextEdit: protocol.TextEdit{
				Range:   textRange,
				NewText: value.Expression,
			},
		})
	}
	return items
}

func callableParameterAt(
	parameters []semantic.Parameter,
	argumentIndex int,
	argumentName string,
) (semantic.Parameter, bool) {
	if argumentName != "" {
		name := strings.TrimPrefix(argumentName, "$")
		for _, parameter := range parameters {
			if strings.EqualFold(strings.TrimPrefix(parameter.Name, "$"), name) {
				return parameter, true
			}
		}
		return semantic.Parameter{}, false
	}
	if argumentIndex >= 0 && argumentIndex < len(parameters) {
		return parameters[argumentIndex], true
	}
	if len(parameters) != 0 &&
		parameters[len(parameters)-1].Flags.Has(semantic.VariadicFlag) {
		return parameters[len(parameters)-1], true
	}
	return semantic.Parameter{}, false
}

func appendExpectedValueAttributeValues(
	result []semantic.CallValue,
	attribute *semantic.Attribute,
	snapshot *semantic.Snapshot,
) []semantic.CallValue {
	for _, argument := range []struct {
		name  string
		index int
	}{
		{name: "values", index: 0},
		{name: "flags", index: 1},
	} {
		value, ok := attribute.Argument(argument.name, argument.index)
		if !ok || value.Kind != semantic.AttributeValueArray {
			continue
		}
		for _, item := range value.Items {
			if converted, ok := expectedAttributeCallValue(item.Value); ok {
				result = append(result, converted)
			}
		}
	}
	for _, argument := range []struct {
		name  string
		index int
	}{
		{name: "valuesFromClass", index: 2},
		{name: "flagsFromClass", index: 3},
	} {
		value, ok := attribute.Argument(argument.name, argument.index)
		if !ok {
			continue
		}
		result = appendExpectedValuesFromClass(result, value, snapshot)
	}
	return result
}

func expectedAttributeCallValue(
	value semantic.AttributeValue,
) (semantic.CallValue, bool) {
	if value.Expression == "" {
		return semantic.CallValue{}, false
	}
	result := semantic.CallValue{
		Kind:       semantic.CallValueExpression,
		Value:      value.Value,
		Expression: value.Expression,
	}
	switch value.Kind {
	case semantic.AttributeValueString:
		result.Kind = semantic.CallValueString
	case semantic.AttributeValueInteger, semantic.AttributeValueFloat:
		result.Kind = semantic.CallValueNumber
	case semantic.AttributeValueConstant:
		result.Kind = semantic.CallValueConstant
	case semantic.AttributeValueClassConstant:
		result.Kind = semantic.CallValueClassConstant
	case semantic.AttributeValueBool, semantic.AttributeValueNull,
		semantic.AttributeValueExpression:
	default:
		return semantic.CallValue{}, false
	}
	return result, true
}

func appendExpectedValuesFromClass(
	result []semantic.CallValue,
	value semantic.AttributeValue,
	snapshot *semantic.Snapshot,
) []semantic.CallValue {
	if snapshot == nil {
		return result
	}
	className := value.Value
	if value.Kind == semantic.AttributeValueClassConstant {
		className = strings.TrimSuffix(className, "::class")
	} else if value.Kind != semantic.AttributeValueString &&
		value.Kind != semantic.AttributeValueConstant {
		return result
	}
	className = strings.Trim(className, "\\")
	if className == "" {
		return result
	}
	snapshot.VisitClassViews(className, func(view semantic.SymbolView) bool {
		class := view.Materialize()
		for _, member := range snapshot.MembersOf(class.ID) {
			if member.Kind != semantic.ClassConstantSymbol &&
				member.Kind != semantic.EnumCaseSymbol {
				continue
			}
			expression := "\\" + class.FullyQualified + "::" + member.Name
			result = append(result, semantic.CallValue{
				Kind:       semantic.CallValueClassConstant,
				Value:      strings.TrimPrefix(expression, "\\"),
				Expression: expression,
			})
		}
		return true
	})
	return result
}

type assistantClassMode uint8

const (
	assistantClasses assistantClassMode = iota
	assistantInterfaces
	assistantClassesAndInterfaces
)

func assistantClassReference(
	ctx context.Context,
	node *phpsyntax.Node,
) (assistantClassMode, cst.TextRange, bool) {
	for _, candidate := range []struct {
		tag  string
		mode assistantClassMode
	}{
		{tag: "ClassInterface", mode: assistantClassesAndInterfaces},
		{tag: "Interface", mode: assistantInterfaces},
		{tag: "Class", mode: assistantClasses},
	} {
		if reference, found := php.AssistantArgumentReference(
			ctx,
			node,
			candidate.tag,
		); found {
			return candidate.mode, reference, true
		}
	}
	return assistantClasses, cst.TextRange{}, false
}

func (p *Provider) assistantClassCompletions(
	mode assistantClassMode,
	reference cst.TextRange,
	lineIndex *cst.LineIndex,
) []protocol.CompletionItem {
	if p == nil || p.index == nil || lineIndex == nil {
		return nil
	}
	replacement := *rangeFromText(lineIndex, reference)
	seen := make(map[string]struct{})
	var items []protocol.CompletionItem
	for _, symbol := range p.index.ClassSymbols() {
		if !assistantClassKindAllowed(mode, symbol.Kind) {
			continue
		}
		label := strings.TrimPrefix(symbol.FullyQualified, "\\")
		key := strings.ToLower(label)
		if label == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		item := protocol.CompletionItem{
			Label:      label,
			Kind:       completionKind(symbol),
			Detail:     formatSymbol(symbol),
			Deprecated: symbol.Flags.Has(semantic.DeprecatedFlag),
			TextEdit: protocol.TextEdit{
				Range:   replacement,
				NewText: label,
			},
		}
		if symbol.DocSummary() != "" {
			item.Documentation.Kind = string(protocol.Markdown)
			item.Documentation.Value = symbol.DocSummary()
		}
		items = append(items, item)
	}
	return items
}

func assistantClassKindAllowed(
	mode assistantClassMode,
	kind semantic.SymbolKind,
) bool {
	switch mode {
	case assistantInterfaces:
		return kind == semantic.InterfaceSymbol
	case assistantClassesAndInterfaces:
		return kind == semantic.ClassSymbol ||
			kind == semantic.InterfaceSymbol
	default:
		return kind == semantic.ClassSymbol
	}
}

func (p *Provider) memberCompletions(
	phpContext *php.PHPContext,
	receiver types.Type,
	static bool,
) []protocol.CompletionItem {
	resolved := resolver.MemberResolver{Snapshot: phpContext.Snapshot}.All(receiver)
	seen := make(map[string]struct{}, len(resolved))
	items := make([]protocol.CompletionItem, 0, len(resolved))
	for _, member := range resolved {
		symbol := member.Symbol
		if !memberVisible(phpContext, symbol) {
			continue
		}
		if static {
			switch symbol.Kind {
			case semantic.MethodSymbol, semantic.PropertySymbol:
				if !symbol.Flags.Has(semantic.StaticFlag) {
					continue
				}
			}
		} else if symbol.Kind == semantic.ClassConstantSymbol ||
			symbol.Kind == semantic.EnumCaseSymbol {
			continue
		}
		key := fmt.Sprintf("%d:%s", symbol.Kind, strings.ToLower(symbol.Name))
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		item := protocol.CompletionItem{
			Label:      completionLabel(symbol),
			Kind:       completionKind(symbol),
			Detail:     formatSymbol(symbol),
			Deprecated: symbol.Flags.Has(semantic.DeprecatedFlag),
			SortText:   fmt.Sprintf("%02d-%s", completionRank(symbol), symbol.Name),
		}
		if symbol.IsFunctionLike() {
			item.InsertText = completionSnippet(symbol)
			item.InsertTextFormat = int(protocol.SnippetTextFormat)
		} else if static && symbol.Kind == semantic.PropertySymbol {
			item.InsertText = "$" + strings.TrimPrefix(symbol.Name, "$")
		} else {
			item.InsertText = symbol.Name
		}
		if symbol.DocSummary() != "" {
			item.Documentation.Kind = string(protocol.Markdown)
			item.Documentation.Value = symbol.DocSummary()
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(left, right int) bool {
		return items[left].SortText < items[right].SortText
	})
	return items
}

func (p *Provider) scopeCompletions(
	phpContext *php.PHPContext,
	request *lsp.CompletionRequest,
	includeGlobals bool,
) []protocol.CompletionItem {
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	seen := make(map[string]struct{})
	var items []protocol.CompletionItem

	if scope, ok := phpContext.Document.ScopeAt(offset); ok {
		for {
			for id := range scope.AllSymbolIDs(phpContext.Document.Symbols) {
				symbol, exists := phpContext.Snapshot.Symbol(id)
				if !exists || (symbol.Kind != semantic.LocalSymbol &&
					symbol.Kind != semantic.ParameterSymbol) {
					continue
				}
				if _, exists := seen[symbol.Name]; exists {
					continue
				}
				seen[symbol.Name] = struct{}{}
				items = append(items, protocol.CompletionItem{
					Label:    symbol.Name,
					Kind:     int(protocol.VariableCompletion),
					Detail:   typeDetail(symbol.Type),
					SortText: "00-" + strings.ToLower(symbol.Name),
				})
			}
			if scope.ID == scope.Parent || int(scope.Parent) >= len(phpContext.Document.Scopes) {
				break
			}
			scope = phpContext.Document.Scopes[scope.Parent]
		}
	}

	if !includeGlobals {
		return items
	}
	completionContext := newPHPGlobalCompletionContext(
		request,
		phpContext.Document,
		p.index.Project(),
		offset,
	)
	for _, symbol := range phpContext.Snapshot.GlobalSymbols() {
		if symbol.Container != "" {
			continue
		}
		switch symbol.Kind {
		case semantic.ClassSymbol, semantic.InterfaceSymbol,
			semantic.TraitSymbol, semantic.EnumSymbol, semantic.FunctionSymbol:
		default:
			continue
		}
		key := fmt.Sprintf("%d:%s", symbol.Kind, strings.ToLower(symbol.FullyQualified))
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		label := symbol.FullyQualified
		insertText := label
		imported := false
		var classPlan phpClassCompletionPlan
		if symbol.IsClassLike() {
			classPlan = completionContext.classPlan(symbol)
			label = symbol.Name
			insertText = classPlan.qualifier
			imported = classPlan.imported
		}
		if label == "" {
			label = symbol.Name
		}
		item := protocol.CompletionItem{
			Label:      label,
			FilterText: symbol.Name + " " + symbol.FullyQualified,
			Kind:       completionKind(symbol),
			Detail:     formatSymbol(symbol),
			Deprecated: symbol.Flags.Has(semantic.DeprecatedFlag),
			SortText:   completionContext.sortText(symbol, imported),
		}
		if symbol.IsClassLike() {
			item.TextEdit = protocol.TextEdit{
				Range: *rangeFromText(
					request.LineIndex,
					completionContext.replacement,
				),
				NewText: insertText,
			}
			if classPlan.edit != nil {
				item.AdditionalTextEdits = []interface{}{*classPlan.edit}
			}
		} else if symbol.Kind == semantic.FunctionSymbol {
			item.InsertText = completionSnippet(symbol)
			item.InsertTextFormat = int(protocol.SnippetTextFormat)
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(left, right int) bool {
		return items[left].SortText < items[right].SortText
	})
	return items
}

func completionSuppressed(request *lsp.CompletionRequest) bool {
	if request == nil {
		return true
	}
	if request.Token != nil {
		switch request.Token.Kind() {
		case phpsyntax.TkLineComment, phpsyntax.TkBlockComment,
			phpsyntax.TkString:
			return true
		}
	}
	for node := request.Node; node != nil; node = node.Parent() {
		if node.Kind() == phpsyntax.PhpString {
			return true
		}
	}
	return false
}

func completionIsVariableOnly(request *lsp.CompletionRequest) bool {
	if request == nil {
		return false
	}
	if request.Token != nil && request.Token.Kind() == phpsyntax.TkVariable {
		return true
	}
	return request.Node != nil && request.Node.Kind() == phpsyntax.PhpVariable
}
