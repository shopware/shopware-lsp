// Package phpsemantic exposes the PHP semantic graph through LSP features.
package phpsemantic

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/languagelevel"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/stubs"
	"github.com/shopware/shopware-lsp/internal/php/suppression"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type Provider struct {
	index *php.PHPIndex
}

func New(index *php.PHPIndex) *Provider {
	return &Provider{index: index}
}

func (p *Provider) GetTriggerCharacters() []string {
	return []string{">", ":", "$", "\\"}
}

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
		if symbol.DocSummary != "" {
			item.Documentation.Kind = string(protocol.Markdown)
			item.Documentation.Value = symbol.DocSummary
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
		if symbol.DocSummary != "" {
			item.Documentation.Kind = string(protocol.Markdown)
			item.Documentation.Value = symbol.DocSummary
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
			for id := range scope.AllSymbolIDs() {
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

func (p *Provider) GetHover(
	ctx context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if !isPHP(request.TextDocument.URI) || request.LineIndex == nil {
		return nil, nil
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil || phpContext.Snapshot == nil {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	if symbol, ok := php.SymbolAt(phpContext.Document, phpContext.Snapshot, offset); ok {
		value := "```php\n" + formatSymbol(symbol) + "\n```"
		if symbol.DocSummary != "" {
			value += "\n\n" + symbol.DocSummary
		}
		if symbol.Flags.Has(semantic.DeprecatedFlag) {
			value += formatDeprecationHover(symbol)
		}
		rng := symbolRangeAt(phpContext.Document, offset)
		return &protocol.Hover{
			Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: value},
			Range:    rangeFromText(request.LineIndex, rng),
		}, nil
	}

	for current := request.Node; current != nil; current = current.Parent() {
		fact := phpContext.Document.TypeOf(current)
		if fact.Type.IsUnknown() {
			continue
		}
		value := "```php\n" + fact.Type.String() + "\n```"
		if fact.Reason != "" {
			value += "\n\n" + fact.Reason
		}
		return &protocol.Hover{
			Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: value},
			Range:    rangeFromText(request.LineIndex, current.RangeTrimmedTrivia()),
		}, nil
	}
	return nil, nil
}

func (p *Provider) GetDefinition(
	ctx context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if !isPHP(request.TextDocument.URI) || request.LineIndex == nil {
		return nil
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil || phpContext.Snapshot == nil {
		return nil
	}
	if mode, _, found := assistantClassReference(
		ctx,
		request.Node,
	); found {
		name := strings.TrimPrefix(
			strings.TrimSpace(phpquery.StringValue(request.Node)),
			"\\",
		)
		var symbols []semantic.Symbol
		for _, symbol := range phpContext.Snapshot.Classes(name) {
			if assistantClassKindAllowed(mode, symbol.Kind) &&
				!symbol.Flags.Has(semantic.InternalFlag) {
				symbols = append(symbols, symbol)
			}
		}
		return locationsForSymbols(symbols, request.Document)
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	var symbols []semantic.Symbol
	if reference, ok := php.ReferenceAt(phpContext.Document, offset); ok {
		symbols = append(
			symbols,
			referenceCandidates(phpContext.Snapshot, reference)...,
		)
	}
	if len(symbols) == 0 {
		if symbol, ok := php.SymbolAt(
			phpContext.Document,
			phpContext.Snapshot,
			offset,
		); ok {
			symbols = append(symbols, symbol)
		}
	}
	return locationsForSymbols(symbols, request.Document)
}

func (p *Provider) GetReferences(
	ctx context.Context,
	request *lsp.ReferenceRequest,
) ([]protocol.Location, error) {
	if !isPHP(request.TextDocument.URI) || request.LineIndex == nil {
		return nil, nil
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil || phpContext.Snapshot == nil {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	symbol, ok := php.SymbolAt(phpContext.Document, phpContext.Snapshot, offset)
	if !ok {
		return nil, nil
	}
	cache := newLocationCache(request.Document)
	var locations []protocol.Location
	if request.Context.IncludeDeclaration {
		locations = append(locations, cache.symbol(symbol))
	}
	for _, reference := range phpContext.Snapshot.ReferencesTo(symbol.ID) {
		locations = append(locations, cache.textRange(
			reference.Path,
			cst.TextRange{Start: reference.RangeStart, End: reference.RangeEnd},
		))
	}
	return locations, nil
}

func (p *Provider) GetSignatureHelp(
	ctx context.Context,
	request *lsp.SignatureHelpRequest,
) (*protocol.SignatureHelp, error) {
	if !isPHP(request.TextDocument.URI) || request.LineIndex == nil {
		return nil, nil
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil || phpContext.Snapshot == nil {
		return nil, nil
	}
	call := phpquery.CallAt(request.Node)
	var candidates []semantic.Symbol
	if call != nil {
		nodes := directNodes(call)
		switch call.Kind() {
		case phpsyntax.PhpFunctionCall:
			if len(nodes) > 0 && nodes[0].Kind() == phpsyntax.PhpName {
				context := nameContextAt(phpContext.Document, call.Range().Start)
				context.VisitFunctionNames(
					compactName(nodes[0].Text()),
					func(name string) bool {
						candidates = append(
							candidates,
							phpContext.Snapshot.Functions(name)...,
						)
						return len(candidates) == 0
					},
				)
			}
		case phpsyntax.PhpMemberCall, phpsyntax.PhpScopedCall:
			if len(nodes) >= 2 {
				static := call.Kind() == phpsyntax.PhpScopedCall
				receiver := phpContext.Document.TypeOf(nodes[0]).Type
				if receiver.IsUnknown() && static && nodes[0].Kind() == phpsyntax.PhpName {
					receiver = staticReceiverType(
						phpContext,
						compactName(nodes[0].Text()),
						nodes[0].Range().Start,
					)
				}
				name := compactName(nodes[1].Text())
				for _, member := range (resolver.MemberResolver{
					Snapshot: phpContext.Snapshot,
				}).Methods(receiver, name) {
					candidates = append(candidates, member.Symbol)
				}
			}
		}
	} else if object := objectCreationAt(request.Node); object != nil {
		nameNode := firstDirectKind(object, phpsyntax.PhpName)
		if nameNode != nil {
			name := nameContextAt(phpContext.Document, nameNode.Range().Start).
				ResolveClass(compactName(nameNode.Text()))
			for _, constructor := range (resolver.MemberResolver{
				Snapshot: phpContext.Snapshot,
			}).Methods(types.Named(name), "__construct") {
				candidates = append(candidates, constructor.Symbol)
			}
		}
		call = object
	}
	if len(candidates) == 0 || call == nil {
		return nil, nil
	}

	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	activeArgument, activeName := activeArgument(call, offset)
	result := &protocol.SignatureHelp{
		Signatures:      make([]protocol.SignatureInformation, 0, len(candidates)),
		ActiveParameter: activeArgument,
	}
	seen := make(map[semantic.SymbolID]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, exists := seen[candidate.ID]; exists {
			continue
		}
		seen[candidate.ID] = struct{}{}
		activeParameter := activeArgument
		if activeName != "" {
			for index, parameter := range candidate.Parameters {
				if strings.EqualFold(
					strings.TrimPrefix(parameter.Name, "$"),
					activeName,
				) {
					activeParameter = index
					break
				}
			}
		}
		signature := protocol.SignatureInformation{
			Label:           formatSymbol(candidate),
			ActiveParameter: activeParameter,
		}
		if candidate.DocSummary != "" {
			signature.Documentation = &protocol.MarkupContent{
				Kind:  protocol.Markdown,
				Value: candidate.DocSummary,
			}
		}
		for _, parameter := range candidate.Parameters {
			signature.Parameters = append(
				signature.Parameters,
				protocol.ParameterInformation{Label: formatParameter(parameter)},
			)
		}
		result.Signatures = append(result.Signatures, signature)
	}
	return result, nil
}

func (p *Provider) Rename(
	ctx context.Context,
	request *lsp.RenameRequest,
) (*protocol.WorkspaceEdit, error) {
	if !isPHP(request.TextDocument.URI) || request.LineIndex == nil {
		return nil, nil
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil || phpContext.Snapshot == nil {
		return nil, nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	symbol, ok := php.SymbolAt(phpContext.Document, phpContext.Snapshot, offset)
	if !ok {
		return nil, nil
	}
	if symbol.Flags.Has(semantic.InternalFlag) || strings.HasPrefix(symbol.Path, "phpstub://") {
		return nil, fmt.Errorf("cannot rename internal PHP symbol %s", symbol.Name)
	}
	newName := strings.TrimPrefix(strings.TrimSpace(request.NewName), "$")
	if !validPHPIdentifier(newName) {
		return nil, fmt.Errorf("%q is not a valid PHP identifier", request.NewName)
	}

	cache := newLocationCache(request.Document)
	edit := &protocol.WorkspaceEdit{Changes: make(map[string][]protocol.TextEdit)}
	seen := make(map[string]struct{})
	add := func(path string, textRange cst.TextRange) {
		key := fmt.Sprintf("%s:%d:%d", path, textRange.Start, textRange.End)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		location := cache.textRange(path, textRange)
		replacement := cache.renameText(path, textRange, newName)
		edit.Changes[location.URI] = append(edit.Changes[location.URI], protocol.TextEdit{
			Range:   location.Range,
			NewText: replacement,
		})
	}
	add(symbol.Path, symbol.SelectionRange)
	for _, reference := range phpContext.Snapshot.ReferencesTo(symbol.ID) {
		add(
			reference.Path,
			cst.TextRange{Start: reference.RangeStart, End: reference.RangeEnd},
		)
	}
	return edit, nil
}

func (p *Provider) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if document == nil || !isPHP(document.URI) || document.LineIndex == nil {
		return nil, nil
	}
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, err
	}
	semanticDocument := p.index.AnalyzeDocument(
		path,
		document.Version,
		document.SyntaxTree.Root,
	)
	snapshot := p.index.SemanticSnapshot().WithDocument(semanticDocument)
	suppressions := suppression.Parse(document.Source)
	var diagnostics []lsp.Problem
	for errorIndex := range document.ParseErrors {
		parseError := &document.ParseErrors[errorIndex]
		if suppressions.Suppresses(parseError.Range.Start, "php.parse") {
			continue
		}
		diagnostics = append(diagnostics, lsp.Problem{
			Range:    parseError.Range,
			Severity: protocol.DiagnosticSeverityError,
			ID:       "php.parse",
			Source:   "shopware-php",
			Message:  parseError.Message(),
		})
	}
	if model := p.index.Project(); model != nil {
		for _, occurrence := range languagelevel.Detect(document.SyntaxTree.Root) {
			definition, found := languagelevel.Lookup(occurrence.Feature)
			if !found || languagelevel.Supports(model.PHPVersion, occurrence.Feature) ||
				suppressions.Suppresses(occurrence.Range.Start, "php.version") {
				continue
			}
			diagnostics = append(diagnostics, lsp.Problem{
				Range:    occurrence.Range,
				Severity: protocol.DiagnosticSeverityError,
				ID:       "php.version",
				Source:   "shopware-php",
				Message:  languagelevel.UnsupportedMessage(definition, model.PHPVersion),
				Payload: map[string]any{
					"feature":       occurrence.Feature,
					"minimumPHP":    fmt.Sprintf("%d.%d", definition.Major, definition.Minor),
					"configuredPHP": fmt.Sprintf("%d.%d", model.PHPVersion.Major, model.PHPVersion.Minor),
				},
			})
		}
	}
	for _, reference := range semanticDocument.References {
		candidates := referenceCandidates(snapshot, reference)
		if len(candidates) > 0 {
			for _, symbol := range candidates {
				if !symbol.Flags.Has(semantic.DeprecatedFlag) ||
					suppressions.Suppresses(
						reference.Range.Start,
						"php.deprecated",
					) ||
					isDeprecationSuppressedAtReference(
						semanticDocument,
						snapshot,
						reference,
						symbol,
					) {
					continue
				}
				diagnostics = append(diagnostics, lsp.Problem{
					Range:    reference.Range,
					Severity: protocol.DiagnosticSeverityHint,
					ID:       "php.deprecated",
					Source:   "shopware-php",
					Message:  formatDeprecationDiagnostic(symbol),
					Tags:     []protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
				})
				break
			}
			if reference.Kind == semantic.MemberName &&
				!anyMemberAccessible(
					semanticDocument,
					snapshot,
					document.SyntaxTree.Root,
					reference,
					candidates,
				) {
				if suppressions.Suppresses(
					reference.Range.Start,
					"php.visibility",
				) {
					continue
				}
				diagnostics = append(diagnostics, lsp.Problem{
					Range:    reference.Range,
					Severity: protocol.DiagnosticSeverityError,
					ID:       "php.visibility",
					Source:   "shopware-php",
					Message:  inaccessibleMemberMessage(reference, candidates[0]),
				})
			}
			continue
		}
		var message string
		switch reference.Kind {
		case semantic.ClassName:
			if classExistenceGuarded(
				document.SyntaxTree.Root,
				semanticDocument,
				reference,
			) {
				continue
			}
			if diagnostic, handled := p.unavailableExtensionDiagnostic(
				reference,
			); handled {
				if diagnostic != nil && !suppressions.Suppresses(
					reference.Range.Start,
					"php.extension",
				) {
					diagnostics = append(diagnostics, *diagnostic)
				}
				continue
			}
			message = "Undefined class " + reference.Name
		case semantic.FunctionName, semantic.ConstantName:
			if diagnostic, handled := p.unavailableExtensionDiagnostic(
				reference,
			); handled {
				if diagnostic != nil && !suppressions.Suppresses(
					reference.Range.Start,
					"php.extension",
				) {
					diagnostics = append(diagnostics, *diagnostic)
				}
			}
			continue
		case semantic.MemberName:
			if !diagnosableMemberReference(snapshot, reference) {
				continue
			}
			if lateStaticMemberMayBeDeclaredBySubclass(
				document.SyntaxTree.Root,
				snapshot,
				reference,
			) {
				continue
			}
			if reference.TargetKind == semantic.MethodSymbol &&
				methodExistsGuarded(
					document.SyntaxTree.Root,
					reference,
				) {
				continue
			}
			if isImplicitTraitRequirement(
				semanticDocument,
				snapshot,
				reference,
			) {
				continue
			}
			message = undefinedMemberMessage(reference)
		case semantic.VariableName:
			if isImplicitVariable(reference.Name) {
				continue
			}
			message = "Undefined variable " + reference.Name
		default:
			continue
		}
		if suppressions.Suppresses(reference.Range.Start, "php.undefined") {
			continue
		}
		diagnostics = append(diagnostics, lsp.Problem{
			Range:    reference.Range,
			Severity: protocol.DiagnosticSeverityWarning,
			ID:       "php.undefined",
			Source:   "shopware-php",
			Message:  message,
		})
	}
	for _, issue := range semanticDocument.Issues {
		diagnostics = append(diagnostics, lsp.Problem{
			Range:    issue.Range,
			Severity: protocol.DiagnosticSeverityError,
			ID:       lsp.DiagnosticID(issue.Code),
			Source:   "shopware-php",
			Message:  issue.Message,
		})
	}
	for _, symbol := range semanticDocument.Symbols {
		if !symbol.IsClassLike() || len(snapshot.Classes(symbol.FullyQualified)) < 2 {
			continue
		}
		diagnostics = append(diagnostics, lsp.Problem{
			Range:    symbol.SelectionRange,
			Severity: protocol.DiagnosticSeverityError,
			ID:       "php.duplicate",
			Source:   "shopware-php",
			Message:  "Duplicate declaration of " + symbol.FullyQualified,
		})
	}
	return diagnostics, ctx.Err()
}

func (p *Provider) unavailableExtensionDiagnostic(
	reference semantic.Reference,
) (*lsp.Problem, bool) {
	if p == nil || p.index == nil {
		return nil, false
	}
	var extension string
	for index := 0; index < reference.QualifiedNameCount(); index++ {
		if candidate, found := stubs.ExtensionForSymbol(
			reference.QualifiedNameAt(index),
		); found {
			extension = candidate
			break
		}
	}
	if extension == "" {
		var found bool
		extension, found = stubs.ExtensionForSymbol(reference.Name)
		if !found {
			return nil, false
		}
	}
	enabled, known := p.index.Project().ExtensionAvailability(extension)
	if enabled {
		return nil, false
	}
	if !known {
		// The symbol belongs to a real optional runtime extension, but Composer's
		// positive requirements do not prove that the local runtime lacks it.
		return nil, true
	}
	diagnostic := &lsp.Problem{
		Range:    reference.Range,
		Severity: protocol.DiagnosticSeverityWarning,
		ID:       "php.extension",
		Source:   "shopware-php",
		Message: fmt.Sprintf(
			"%s requires disabled PHP extension ext-%s",
			reference.Name,
			strings.ReplaceAll(extension, "_", "-"),
		),
		Payload: map[string]interface{}{
			"extension": extension,
		},
	}
	return diagnostic, true
}

func formatDeprecationHover(symbol semantic.Symbol) string {
	result := "\n\n**Deprecated**"
	details, found := semantic.DeprecationOf(symbol.Attributes)
	if !found {
		return result
	}
	if details.Since != "" {
		result += " since " + details.Since
	}
	if details.Reason != "" {
		result += "\n\n" + details.Reason
	}
	if details.Replacement != "" {
		result += "\n\nReplacement: `" +
			strings.ReplaceAll(details.Replacement, "`", "\\`") + "`"
	}
	return result
}

func formatDeprecationDiagnostic(symbol semantic.Symbol) string {
	message := symbol.Name + " is deprecated"
	details, found := semantic.DeprecationOf(symbol.Attributes)
	if !found {
		return message
	}
	if details.Since != "" {
		message += " since " + details.Since
	}
	if details.Reason != "" {
		message += ": " + details.Reason
	}
	if details.Replacement != "" {
		message += "; use " + details.Replacement
	}
	return message
}

func lateStaticMemberMayBeDeclaredBySubclass(
	root *phpsyntax.Node,
	snapshot *semantic.Snapshot,
	reference semantic.Reference,
) bool {
	if root == nil || snapshot == nil || !reference.Static {
		return false
	}
	node := root.NodeAtOffset(reference.Range.Start)
	lateStatic := false
	for current := node; current != nil; current = current.Parent() {
		switch current.Kind() {
		case phpsyntax.PhpMemberAccess, phpsyntax.PhpScopedAccess,
			phpsyntax.PhpMemberCall, phpsyntax.PhpScopedCall:
			nodes := directNodes(current)
			lateStatic = len(nodes) > 0 && strings.EqualFold(
				strings.TrimSpace(nodes[0].Text()),
				"static",
			)
		}
		if lateStatic {
			break
		}
	}
	if !lateStatic {
		return false
	}

	extensible := false
	found := false
	visitDiagnosticObjectAlternatives(reference.Receiver, func(receiver types.Type) {
		snapshot.VisitClassViews(receiver.Name(), func(classView semantic.SymbolView) bool {
			found = true
			if !classView.Materialize().Flags.Has(semantic.FinalFlag) {
				extensible = true
			}
			return !extensible
		})
	})
	return !found || extensible
}

func visitDiagnosticObjectAlternatives(
	value types.Type,
	visit func(types.Type),
) {
	if value.Kind() == types.ObjectKind {
		visit(value)
		return
	}
	if value.Kind() != types.UnionKind && value.Kind() != types.IntersectionKind {
		return
	}
	for _, alternative := range value.Arguments() {
		visitDiagnosticObjectAlternatives(alternative, visit)
	}
}

func methodExistsGuarded(
	root *phpsyntax.Node,
	reference semantic.Reference,
) bool {
	if root == nil {
		return false
	}
	call := phpquery.CallAt(root.NodeAtOffset(reference.Range.Start))
	if call == nil || call.Kind() != phpsyntax.PhpMemberCall {
		return false
	}
	receiver := phpquery.CallReceiver(call)
	if receiver == nil {
		return false
	}
	receiverKey := normalizedGuardExpression(receiver.Text())
	if receiverKey == "" {
		return false
	}

	for current := call; current != nil && current.Parent() != nil; current = current.Parent() {
		parent := current.Parent()
		if parent.Kind() == phpsyntax.PhpTernaryExpression {
			nodes := directNodes(parent)
			if len(nodes) >= 3 {
				truth := nodes[len(nodes)-2].Range().Contains(reference.Range.Start)
				falsity := nodes[len(nodes)-1].Range().Contains(reference.Range.Start)
				if (truth || falsity) && conditionGuaranteesMethod(
					nodes[0],
					receiverKey,
					reference.Name,
					truth,
				) {
					return true
				}
			}
		}
		if parent.Kind() != phpsyntax.PhpIfStatement {
			continue
		}
		nodes := directNodes(parent)
		if len(nodes) < 2 ||
			!nodes[1].Range().Contains(reference.Range.Start) {
			continue
		}
		if conditionGuaranteesMethod(
			nodes[0],
			receiverKey,
			reference.Name,
			true,
		) {
			return true
		}
	}

	for current := call; current != nil; {
		statement := current
		for statement.Parent() != nil &&
			statement.Parent().Kind() != phpsyntax.PhpBlock {
			statement = statement.Parent()
		}
		block := statement.Parent()
		if block == nil {
			break
		}
		statements := directNodes(block)
		for index, candidate := range statements {
			if candidate != statement {
				continue
			}
			for previous := index - 1; previous >= 0; previous-- {
				guard := statements[previous]
				if statementAssignsExpression(guard, receiverKey) {
					return false
				}
				if guard.Kind() != phpsyntax.PhpIfStatement {
					continue
				}
				nodes := directNodes(guard)
				if len(nodes) == 2 && statementTerminates(nodes[1]) &&
					conditionGuaranteesMethod(
						nodes[0],
						receiverKey,
						reference.Name,
						false,
					) {
					return true
				}
			}
			break
		}
		current = block
	}
	return false
}

func classExistenceGuarded(
	root *phpsyntax.Node,
	document *semantic.Document,
	reference semantic.Reference,
) bool {
	if root == nil || document == nil {
		return false
	}
	names := diagnosticReferenceClassNames(document, reference)
	if len(names) == 0 {
		return false
	}
	node := root.NodeAtOffset(reference.Range.Start)
	call := phpquery.CallAt(node)
	if call != nil && classExistencePredicate(call) {
		argument := phpquery.ArgumentExpression(call, 0)
		if argument != nil && argument.Range().Contains(reference.Range.Start) &&
			guardExpressionMatchesClass(document, argument, names) {
			return true
		}
	}

	for current := node; current != nil && current.Parent() != nil; current = current.Parent() {
		parent := current.Parent()
		if parent.Kind() == phpsyntax.PhpTernaryExpression {
			nodes := directNodes(parent)
			if len(nodes) >= 3 {
				truth := nodes[len(nodes)-2].Range().Contains(reference.Range.Start)
				falsity := nodes[len(nodes)-1].Range().Contains(reference.Range.Start)
				if (truth || falsity) && conditionGuaranteesClass(
					nodes[0],
					document,
					names,
					truth,
				) {
					return true
				}
			}
		}
		if parent.Kind() != phpsyntax.PhpIfStatement {
			continue
		}
		nodes := directNodes(parent)
		if len(nodes) < 2 ||
			!nodes[1].Range().Contains(reference.Range.Start) {
			continue
		}
		if conditionGuaranteesClass(nodes[0], document, names, true) {
			return true
		}
	}

	for current := node; current != nil; {
		statement := current
		for statement.Parent() != nil &&
			statement.Parent().Kind() != phpsyntax.PhpBlock {
			statement = statement.Parent()
		}
		block := statement.Parent()
		if block == nil {
			break
		}
		statements := directNodes(block)
		for index, candidate := range statements {
			if candidate != statement {
				continue
			}
			for previous := index - 1; previous >= 0; previous-- {
				guard := statements[previous]
				if guard.Kind() != phpsyntax.PhpIfStatement {
					continue
				}
				nodes := directNodes(guard)
				if len(nodes) == 2 && statementTerminates(nodes[1]) &&
					conditionGuaranteesClass(
						nodes[0],
						document,
						names,
						false,
					) {
					return true
				}
			}
			break
		}
		current = block
	}
	return false
}

func diagnosticReferenceClassNames(
	document *semantic.Document,
	reference semantic.Reference,
) []string {
	names := make([]string, 0, reference.QualifiedNameCount()+1)
	for index := 0; index < reference.QualifiedNameCount(); index++ {
		if name := normalizeDiagnosticClassName(reference.QualifiedNameAt(index)); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 && reference.Name != "" {
		resolved := nameContextAt(document, reference.Range.Start).ResolveClass(reference.Name)
		if name := normalizeDiagnosticClassName(resolved); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func conditionGuaranteesClass(
	node *phpsyntax.Node,
	document *semantic.Document,
	names []string,
	truth bool,
) bool {
	if node == nil {
		return false
	}
	if node.Kind() == phpsyntax.PhpParenthesized {
		nodes := directNodes(node)
		return len(nodes) > 0 && conditionGuaranteesClass(
			nodes[0], document, names, truth,
		)
	}
	if node.Kind() == phpsyntax.PhpUnaryExpression &&
		diagnosticDirectOperator(node) == "!" {
		nodes := directNodes(node)
		return len(nodes) > 0 && conditionGuaranteesClass(
			nodes[len(nodes)-1], document, names, !truth,
		)
	}
	if node.Kind() == phpsyntax.PhpBinaryExpression {
		nodes := directNodes(node)
		if len(nodes) >= 2 {
			left := conditionGuaranteesClass(nodes[0], document, names, truth)
			right := conditionGuaranteesClass(
				nodes[len(nodes)-1], document, names, truth,
			)
			switch strings.ToLower(diagnosticDirectOperator(node)) {
			case "&&", "and":
				if truth {
					return left || right
				}
				return left && right
			case "||", "or":
				if truth {
					return left && right
				}
				return left || right
			}
		}
	}
	if !truth || !classExistencePredicate(node) {
		return false
	}
	return guardExpressionMatchesClass(
		document,
		phpquery.ArgumentExpression(node, 0),
		names,
	)
}

func classExistencePredicate(node *phpsyntax.Node) bool {
	if node == nil || node.Kind() != phpsyntax.PhpFunctionCall {
		return false
	}
	switch strings.ToLower(strings.TrimPrefix(phpquery.CallMethodName(node), "\\")) {
	case "class_exists", "interface_exists", "trait_exists", "enum_exists":
		return true
	default:
		return false
	}
}

func guardExpressionMatchesClass(
	document *semantic.Document,
	expression *phpsyntax.Node,
	names []string,
) bool {
	guarded := guardedClassName(document, expression)
	if guarded == "" {
		return false
	}
	for _, name := range names {
		if guarded == name {
			return true
		}
	}
	return false
}

func guardedClassName(
	document *semantic.Document,
	expression *phpsyntax.Node,
) string {
	if expression == nil || document == nil {
		return ""
	}
	if expression.Kind() == phpsyntax.PhpString {
		return normalizeDiagnosticClassName(
			diagnosticPHPStringValue(expression),
		)
	}
	if expression.Kind() != phpsyntax.PhpMemberAccess &&
		expression.Kind() != phpsyntax.PhpScopedAccess {
		return ""
	}
	nodes := directNodes(expression)
	if len(nodes) < 2 ||
		!strings.EqualFold(compactName(nodes[len(nodes)-1].Text()), "class") {
		return ""
	}
	receiver := compactName(nodes[0].Text())
	if receiver == "" {
		return ""
	}
	resolved := nameContextAt(document, nodes[0].Range().Start).ResolveClass(receiver)
	return normalizeDiagnosticClassName(resolved)
}

func normalizeDiagnosticClassName(name string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(name), "\\"))
}

func diagnosticPHPStringValue(node *phpsyntax.Node) string {
	value := phpquery.StringValue(node)
	text := strings.TrimSpace(node.Text())
	if len(text) < 2 {
		return value
	}
	switch text[0] {
	case '\'':
		return strings.NewReplacer(`\\`, `\`, `\'`, `'`).Replace(value)
	case '"':
		return strings.NewReplacer(`\\`, `\`, `\"`, `"`).Replace(value)
	default:
		return value
	}
}

func statementAssignsExpression(node *phpsyntax.Node, expression string) bool {
	if node == nil || expression == "" {
		return false
	}
	assigned := func(candidate *phpsyntax.Node) bool {
		if candidate == nil ||
			candidate.Kind() != phpsyntax.PhpAssignmentExpression {
			return false
		}
		nodes := directNodes(candidate)
		return len(nodes) > 0 &&
			normalizedGuardExpression(nodes[0].Text()) == expression
	}
	if assigned(node) {
		return true
	}
	for element := range node.Descendants() {
		candidate, ok := element.(*phpsyntax.Node)
		if ok && assigned(candidate) {
			return true
		}
	}
	return false
}

func conditionGuaranteesMethod(
	node *phpsyntax.Node,
	receiver,
	method string,
	truth bool,
) bool {
	if node == nil {
		return false
	}
	if node.Kind() == phpsyntax.PhpParenthesized {
		nodes := directNodes(node)
		if len(nodes) == 0 {
			return false
		}
		return conditionGuaranteesMethod(nodes[0], receiver, method, truth)
	}
	if node.Kind() == phpsyntax.PhpUnaryExpression &&
		diagnosticDirectOperator(node) == "!" {
		nodes := directNodes(node)
		if len(nodes) == 0 {
			return false
		}
		return conditionGuaranteesMethod(nodes[len(nodes)-1], receiver, method, !truth)
	}
	if node.Kind() == phpsyntax.PhpBinaryExpression {
		nodes := directNodes(node)
		if len(nodes) >= 2 {
			left := conditionGuaranteesMethod(
				nodes[0], receiver, method, truth,
			)
			right := conditionGuaranteesMethod(
				nodes[len(nodes)-1], receiver, method, truth,
			)
			switch strings.ToLower(diagnosticDirectOperator(node)) {
			case "&&", "and":
				if truth {
					return left || right
				}
				return left && right
			case "||", "or":
				if truth {
					return left && right
				}
				return left || right
			}
		}
	}
	if !truth || node.Kind() != phpsyntax.PhpFunctionCall ||
		!strings.EqualFold(
			strings.TrimPrefix(phpquery.CallMethodName(node), "\\"),
			"method_exists",
		) {
		return false
	}
	target := phpquery.ArgumentExpression(node, 0)
	name := phpquery.ArgumentExpression(node, 1)
	return target != nil && name != nil &&
		normalizedGuardExpression(target.Text()) == receiver &&
		strings.EqualFold(phpquery.StringValue(name), method)
}

func statementTerminates(node *phpsyntax.Node) bool {
	if node == nil {
		return false
	}
	if node.Kind() == phpsyntax.PhpBlock {
		nodes := directNodes(node)
		if len(nodes) == 0 {
			return false
		}
		return statementTerminates(nodes[len(nodes)-1])
	}
	switch node.Kind() {
	case phpsyntax.PhpReturnStatement,
		phpsyntax.PhpThrowStatement,
		phpsyntax.PhpBreakStatement,
		phpsyntax.PhpContinueStatement:
		return true
	default:
		return false
	}
}

func diagnosticDirectOperator(node *phpsyntax.Node) string {
	if node == nil {
		return ""
	}
	for index := 0; index < node.ChildCount(); index++ {
		token, ok := node.Child(index).(*phpsyntax.Token)
		if !ok || token.Kind().IsTrivia() {
			continue
		}
		switch token.Kind() {
		case phpsyntax.TkOperator, phpsyntax.TkKeyword:
			return token.Text()
		}
	}
	return ""
}

func normalizedGuardExpression(value string) string {
	value = strings.Map(func(character rune) rune {
		switch character {
		case ' ', '\t', '\r', '\n':
			return -1
		default:
			return character
		}
	}, value)
	return strings.ReplaceAll(value, "?->", "->")
}

func isDeprecationSuppressedAtReference(
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	reference semantic.Reference,
	symbol semantic.Symbol,
) bool {
	for _, declaration := range document.Symbols {
		if declaration.Flags.Has(semantic.DeprecatedFlag) &&
			declaration.Range.Contains(reference.Range.Start) {
			return true
		}
	}
	scope, found := document.ScopeAt(reference.Range.Start)
	if !found {
		return false
	}
	for {
		if owner, exists := snapshot.Symbol(scope.Owner); exists {
			if owner.Flags.Has(semantic.DeprecatedFlag) ||
				symbol.ID == owner.ID || symbol.Container == owner.ID {
				return true
			}
			if owner.Container != "" {
				if container, exists := snapshot.Symbol(owner.Container); exists &&
					container.Flags.Has(semantic.DeprecatedFlag) {
					return true
				}
			}
		}
		if scope.ID == scope.Parent || int(scope.Parent) >= len(document.Scopes) {
			return false
		}
		scope = document.Scopes[scope.Parent]
	}
}

func referenceCandidates(
	snapshot *semantic.Snapshot,
	reference semantic.Reference,
) []semantic.Symbol {
	if snapshot == nil {
		return nil
	}
	candidates := reference.CandidateIDs()
	capacity := len(candidates)
	if reference.Resolved != "" {
		capacity++
	}
	result := make([]semantic.Symbol, 0, capacity)
	appendSymbol := func(id semantic.SymbolID) {
		for _, existing := range result {
			if existing.ID == id {
				return
			}
		}
		symbol, exists := snapshot.Symbol(id)
		if !exists {
			return
		}
		result = append(result, symbol)
	}
	if reference.Resolved != "" {
		appendSymbol(reference.Resolved)
	}
	for _, id := range candidates {
		appendSymbol(id)
	}
	return result
}

func diagnosableMemberReference(
	snapshot *semantic.Snapshot,
	reference semantic.Reference,
) bool {
	if snapshot == nil || reference.Receiver.IsUnknown() {
		return false
	}
	var check func(types.Type) bool
	check = func(receiver types.Type) bool {
		switch receiver.Kind() {
		case types.UnionKind, types.IntersectionKind:
			members := receiver.Arguments()
			if len(members) == 0 {
				return false
			}
			for _, member := range members {
				if !check(member) {
					return false
				}
			}
			return true
		case types.ObjectKind:
			if receiver.Name() == "" {
				return false
			}
			classes := snapshot.Classes(receiver.Name())
			if len(classes) == 0 {
				return false
			}
			for _, class := range classes {
				if classHasDynamicMemberFallback(snapshot, class, reference) {
					return false
				}
			}
			return true
		default:
			return false
		}
	}
	return check(reference.Receiver)
}

func classHasDynamicMemberFallback(
	snapshot *semantic.Snapshot,
	class semantic.Symbol,
	reference semantic.Reference,
) bool {
	if reference.TargetKind == semantic.PropertySymbol &&
		classAllowsDynamicProperties(snapshot, class, nil) {
		return true
	}
	resolver := resolver.MemberResolver{Snapshot: snapshot}
	receiver := types.Named(class.FullyQualified)
	var names []string
	switch reference.TargetKind {
	case semantic.MethodSymbol:
		if reference.Static {
			names = []string{"__callStatic"}
		} else {
			names = []string{"__call"}
		}
	case semantic.PropertySymbol:
		if reference.Static {
			return false
		}
		if reference.Write {
			names = []string{"__set"}
		} else {
			names = []string{"__get", "__isset"}
		}
	default:
		return false
	}
	for _, name := range names {
		if len(resolver.Methods(receiver, name)) > 0 {
			return true
		}
	}
	return false
}

func classAllowsDynamicProperties(
	snapshot *semantic.Snapshot,
	class semantic.Symbol,
	visited map[semantic.SymbolID]struct{},
) bool {
	if strings.EqualFold(class.FullyQualified, "stdClass") ||
		hasAllowDynamicProperties(class) {
		return true
	}
	if visited == nil {
		visited = make(map[semantic.SymbolID]struct{})
	}
	if _, duplicate := visited[class.ID]; duplicate {
		return false
	}
	visited[class.ID] = struct{}{}
	for _, parent := range class.Extends {
		allowed := false
		snapshot.VisitClassViews(
			parent,
			func(parentView semantic.SymbolView) bool {
				allowed = classAllowsDynamicProperties(
					snapshot,
					parentView.Materialize(),
					visited,
				)
				return !allowed
			},
		)
		if allowed {
			return true
		}
	}
	return false
}

func hasAllowDynamicProperties(class semantic.Symbol) bool {
	for _, attribute := range class.Attributes {
		name := strings.TrimPrefix(attribute.Name, "\\")
		if strings.EqualFold(name, "AllowDynamicProperties") ||
			strings.HasSuffix(
				strings.ToLower(name),
				"\\allowdynamicproperties",
			) {
			return true
		}
	}
	return false
}

func undefinedMemberMessage(reference semantic.Reference) string {
	name := strings.TrimPrefix(reference.Name, "$")
	switch reference.TargetKind {
	case semantic.MethodSymbol:
		return "Undefined method " + name + " on " + reference.Receiver.String()
	case semantic.ClassConstantSymbol:
		return "Undefined class constant " + name + " on " + reference.Receiver.String()
	default:
		return "Undefined property $" + name + " on " + reference.Receiver.String()
	}
}

func isImplicitTraitRequirement(
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	reference semantic.Reference,
) bool {
	current, ok := enclosingClass(
		document,
		snapshot,
		reference.Range.Start,
	)
	if !ok || current.Kind != semantic.TraitSymbol {
		return false
	}
	var containsTrait func(types.Type) bool
	containsTrait = func(value types.Type) bool {
		switch value.Kind() {
		case types.ObjectKind:
			return strings.EqualFold(
				strings.TrimPrefix(value.Name(), "\\"),
				strings.TrimPrefix(current.FullyQualified, "\\"),
			)
		case types.UnionKind, types.IntersectionKind:
			for _, member := range value.Arguments() {
				if containsTrait(member) {
					return true
				}
			}
		}
		return false
	}
	return containsTrait(reference.Receiver)
}

func anyMemberAccessible(
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	root *phpsyntax.Node,
	reference semantic.Reference,
	candidates []semantic.Symbol,
) bool {
	current, hasCurrent := enclosingClass(
		document,
		snapshot,
		reference.Range.Start,
	)
	boundClasses := closureBindingClasses(
		document,
		snapshot,
		root,
		reference.Range.Start,
	)
	for _, candidate := range candidates {
		visibility := candidate.Visibility
		if reference.Write && candidate.Kind == semantic.PropertySymbol {
			if candidate.Flags.Has(semantic.ReadonlyFlag) &&
				(!hasCurrent || candidate.Container != current.ID) &&
				!containsClassID(boundClasses, candidate.Container) {
				continue
			}
			if candidate.HasWriteVisibility {
				visibility = candidate.WriteVisibility
			}
		}
		if visibility == semantic.Public {
			return true
		}
		if hasCurrent && memberAccessibleFromClass(
			snapshot,
			current,
			candidate,
			visibility,
		) {
			return true
		}
		for _, boundClass := range boundClasses {
			if memberAccessibleFromClass(
				snapshot,
				boundClass,
				candidate,
				visibility,
			) {
				return true
			}
		}
	}
	for _, class := range receiverClasses(snapshot, reference.Receiver) {
		if classHasDynamicMemberFallback(snapshot, class, reference) {
			return true
		}
	}
	return false
}

func memberAccessibleFromClass(
	snapshot *semantic.Snapshot,
	current,
	candidate semantic.Symbol,
	visibility semantic.Visibility,
) bool {
	if candidate.Container == current.ID {
		return true
	}
	container, exists := snapshot.Symbol(candidate.Container)
	if !exists {
		return false
	}
	if container.Kind == semantic.TraitSymbol {
		if visibility == semantic.Private {
			return classDirectlyUsesTrait(
				snapshot,
				current.FullyQualified,
				container.FullyQualified,
				make(map[string]struct{}),
			)
		}
		if classUsesTrait(
			snapshot,
			current.FullyQualified,
			container.FullyQualified,
			make(map[string]struct{}),
		) {
			return true
		}
	}
	return visibility != semantic.Private &&
		snapshot.IsSubtypeOf(
			current.FullyQualified,
			container.FullyQualified,
		)
}

func containsClassID(classes []semantic.Symbol, id semantic.SymbolID) bool {
	for _, class := range classes {
		if class.ID == id {
			return true
		}
	}
	return false
}

func closureBindingClasses(
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	root *phpsyntax.Node,
	offset uint32,
) []semantic.Symbol {
	if document == nil || snapshot == nil || root == nil {
		return nil
	}
	node := root.NodeAtOffset(offset)
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() != phpsyntax.PhpClosure &&
			current.Kind() != phpsyntax.PhpArrowFunction {
			continue
		}
		call := phpquery.CallAt(current.Parent())
		if call == nil ||
			call.Kind() != phpsyntax.PhpScopedCall ||
			phpquery.ArgumentIndex(call, current) != 0 ||
			!strings.EqualFold(phpquery.CallMethodName(call), "bind") {
			return nil
		}
		receiver := phpquery.CallReceiver(call)
		if receiver == nil || receiver.Kind() != phpsyntax.PhpName ||
			!strings.EqualFold(
				nameContextAt(
					document,
					receiver.Range().Start,
				).ResolveClass(phpquery.NameValue(receiver)),
				"Closure",
			) {
			return nil
		}

		scopeExpression := phpquery.ArgumentExpression(call, 2)
		for index, argument := range phpquery.Arguments(call) {
			if strings.EqualFold(
				phpquery.ArgumentName(argument),
				"newScope",
			) {
				scopeExpression = phpquery.ArgumentExpression(call, index)
				break
			}
		}
		if scopeExpression == nil {
			return nil
		}
		var result []semantic.Symbol
		appendBindingClasses(
			snapshot,
			document.TypeOf(scopeExpression).Type,
			&result,
		)
		return result
	}
	return nil
}

func appendBindingClasses(
	snapshot *semantic.Snapshot,
	value types.Type,
	result *[]semantic.Symbol,
) {
	switch value.Kind() {
	case types.ClassStringKind:
		if value.ArgumentCount() > 0 {
			appendBindingClasses(snapshot, value.Argument(0), result)
		}
	case types.ObjectKind:
		if value.Name() == "" {
			return
		}
		snapshot.VisitClassViews(
			value.Name(),
			func(class semantic.SymbolView) bool {
				id := class.ID()
				for _, existing := range *result {
					if existing.ID == id {
						return true
					}
				}
				*result = append(*result, class.Materialize())
				return true
			},
		)
	case types.UnionKind, types.IntersectionKind:
		for _, member := range value.Arguments() {
			appendBindingClasses(snapshot, member, result)
		}
	}
}

func receiverClasses(
	snapshot *semantic.Snapshot,
	receiver types.Type,
) []semantic.Symbol {
	switch receiver.Kind() {
	case types.ObjectKind:
		return snapshot.Classes(receiver.Name())
	case types.UnionKind, types.IntersectionKind:
		var result []semantic.Symbol
		for _, member := range receiver.Arguments() {
			result = append(result, receiverClasses(snapshot, member)...)
		}
		return result
	default:
		return nil
	}
}

func classUsesTrait(
	snapshot *semantic.Snapshot,
	className,
	traitName string,
	visited map[string]struct{},
) bool {
	if snapshot == nil || className == "" || traitName == "" {
		return false
	}
	key := strings.ToLower(strings.TrimPrefix(className, "\\"))
	if _, seen := visited[key]; seen {
		return false
	}
	visited[key] = struct{}{}
	found := false
	snapshot.VisitClassViews(className, func(view semantic.SymbolView) bool {
		class := view.Materialize()
		for _, used := range class.Traits {
			if strings.EqualFold(
				strings.TrimPrefix(used, "\\"),
				strings.TrimPrefix(traitName, "\\"),
			) || classUsesTrait(snapshot, used, traitName, visited) {
				found = true
				return false
			}
		}
		for _, parent := range class.Extends {
			if classUsesTrait(snapshot, parent, traitName, visited) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func classDirectlyUsesTrait(
	snapshot *semantic.Snapshot,
	className,
	traitName string,
	visited map[string]struct{},
) bool {
	if snapshot == nil || className == "" || traitName == "" {
		return false
	}
	key := strings.ToLower(strings.TrimPrefix(className, "\\"))
	if _, seen := visited[key]; seen {
		return false
	}
	visited[key] = struct{}{}
	found := false
	snapshot.VisitClassViews(className, func(view semantic.SymbolView) bool {
		class := view.Materialize()
		for _, used := range class.Traits {
			if strings.EqualFold(
				strings.TrimPrefix(used, "\\"),
				strings.TrimPrefix(traitName, "\\"),
			) || classDirectlyUsesTrait(
				snapshot,
				used,
				traitName,
				visited,
			) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func enclosingClass(
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	offset uint32,
) (semantic.Symbol, bool) {
	scope, ok := document.ScopeAt(offset)
	if !ok {
		return semantic.Symbol{}, false
	}
	for {
		if owner, exists := snapshot.Symbol(scope.Owner); exists {
			if owner.IsClassLike() {
				return owner, true
			}
			if owner.Container != "" {
				if container, found := snapshot.Symbol(owner.Container); found &&
					container.IsClassLike() {
					return container, true
				}
			}
		}
		if scope.ID == scope.Parent || int(scope.Parent) >= len(document.Scopes) {
			return semantic.Symbol{}, false
		}
		scope = document.Scopes[scope.Parent]
	}
}

func inaccessibleMemberMessage(
	reference semantic.Reference,
	symbol semantic.Symbol,
) string {
	visibility := symbol.Visibility
	if reference.Write && symbol.Kind == semantic.PropertySymbol &&
		symbol.HasWriteVisibility {
		visibility = symbol.WriteVisibility
	}
	return "Cannot access " + visibilityName(visibility) + " member " +
		strings.TrimPrefix(reference.Name, "$")
}

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
