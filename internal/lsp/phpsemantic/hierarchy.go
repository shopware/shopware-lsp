package phpsemantic

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

func (p *Provider) GetImplementation(
	ctx context.Context,
	request *lsp.ImplementationRequest,
) []protocol.Location {
	if request == nil || request.ImplementationParams == nil ||
		!isPHP(request.TextDocument.URI) || request.LineIndex == nil {
		return nil
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil || phpContext.Snapshot == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	symbol, found := php.SymbolAt(phpContext.Document, phpContext.Snapshot, offset)
	if !found {
		return nil
	}
	implementations := implementationSymbols(phpContext.Snapshot, symbol)
	return locationsForSymbols(implementations, request.Document)
}

func implementationSymbols(
	snapshot *semantic.Snapshot,
	symbol semantic.Symbol,
) []semantic.Symbol {
	if snapshot == nil {
		return nil
	}
	if symbol.Kind == semantic.MethodSymbol {
		return snapshot.MethodOverrides(symbol.ID)
	}
	if !symbol.IsClassLike() {
		return nil
	}
	if target, found := snapshot.ClassAliasTarget(symbol.ID); found {
		symbol = target
	}
	seen := make(map[semantic.SymbolID]struct{})
	var result []semantic.Symbol
	appendSymbol := func(candidate semantic.Symbol) bool {
		if candidate.ID == symbol.ID {
			return false
		}
		if _, exists := seen[candidate.ID]; exists {
			return false
		}
		seen[candidate.ID] = struct{}{}
		result = append(result, candidate)
		return true
	}
	if symbol.Kind == semantic.TraitSymbol {
		for _, consumer := range snapshot.TraitConsumers(symbol.FullyQualified) {
			appendSymbol(consumer)
		}
		return result
	}
	queue := []string{symbol.FullyQualified}
	visitedNames := make(map[string]struct{})
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		key := strings.ToLower(strings.TrimPrefix(name, "\\"))
		if _, visited := visitedNames[key]; visited {
			continue
		}
		visitedNames[key] = struct{}{}
		for _, subtype := range snapshot.DirectSubtypes(name) {
			appendSymbol(subtype)
			queue = append(queue, subtype.FullyQualified)
		}
		for _, alias := range snapshot.ClassAliases(name) {
			appendSymbol(alias)
		}
	}
	return result
}

func (p *Provider) PrepareTypeHierarchy(
	ctx context.Context,
	request *lsp.TypeHierarchyPrepareRequest,
) []protocol.TypeHierarchyItem {
	if request == nil || request.PrepareTypeHierarchyParams == nil ||
		!isPHP(request.TextDocument.URI) || request.LineIndex == nil {
		return nil
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil || phpContext.Snapshot == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	symbol, found := php.SymbolAt(phpContext.Document, phpContext.Snapshot, offset)
	if !found || !symbol.IsClassLike() {
		return nil
	}
	return []protocol.TypeHierarchyItem{
		typeHierarchyItem(newLocationCache(request.Document), symbol),
	}
}

func (p *Provider) TypeHierarchySupertypes(
	_ context.Context,
	item protocol.TypeHierarchyItem,
) []protocol.TypeHierarchyItem {
	if p == nil || p.index == nil {
		return nil
	}
	snapshot := p.index.SemanticSnapshot()
	symbol, found := hierarchyItemSymbol(snapshot, item)
	if !found {
		return nil
	}
	return typeHierarchyItems(
		newLocationCache(nil),
		snapshot.DirectSupertypes(symbol.ID),
	)
}

func (p *Provider) TypeHierarchySubtypes(
	_ context.Context,
	item protocol.TypeHierarchyItem,
) []protocol.TypeHierarchyItem {
	if p == nil || p.index == nil {
		return nil
	}
	snapshot := p.index.SemanticSnapshot()
	symbol, found := hierarchyItemSymbol(snapshot, item)
	if !found {
		return nil
	}
	var symbols []semantic.Symbol
	if symbol.Kind == semantic.TraitSymbol {
		symbols = snapshot.TraitConsumers(symbol.FullyQualified)
	} else {
		symbols = snapshot.DirectSubtypes(symbol.FullyQualified)
	}
	return typeHierarchyItems(newLocationCache(nil), symbols)
}

func typeHierarchyItems(
	cache *locationCache,
	symbols []semantic.Symbol,
) []protocol.TypeHierarchyItem {
	result := make([]protocol.TypeHierarchyItem, 0, len(symbols))
	for _, symbol := range symbols {
		result = append(result, typeHierarchyItem(cache, symbol))
	}
	return result
}

func typeHierarchyItem(
	cache *locationCache,
	symbol semantic.Symbol,
) protocol.TypeHierarchyItem {
	full := cache.textRange(symbol.Path, symbol.Range)
	selection := cache.textRange(symbol.Path, symbol.SelectionRange)
	return protocol.TypeHierarchyItem{
		Name:           symbol.Name,
		Kind:           hierarchySymbolKind(symbol.Kind),
		Detail:         symbol.FullyQualified,
		URI:            full.URI,
		Range:          full.Range,
		SelectionRange: selection.Range,
		Data: map[string]any{
			"symbol": string(symbol.ID),
			"name":   symbol.FullyQualified,
		},
	}
}

func hierarchyItemSymbol(
	snapshot *semantic.Snapshot,
	item protocol.TypeHierarchyItem,
) (semantic.Symbol, bool) {
	if snapshot == nil {
		return semantic.Symbol{}, false
	}
	data, _ := item.Data.(map[string]any)
	if id, ok := data["symbol"].(string); ok && id != "" {
		if symbol, found := snapshot.Symbol(semantic.SymbolID(id)); found {
			return symbol, true
		}
	}
	name := item.Detail
	if value, ok := data["name"].(string); ok && value != "" {
		name = value
	}
	classes := snapshot.Classes(name)
	if len(classes) == 0 {
		return semantic.Symbol{}, false
	}
	return classes[0], true
}

func hierarchySymbolKind(kind semantic.SymbolKind) protocol.SymbolKind {
	switch kind {
	case semantic.InterfaceSymbol:
		return protocol.SymbolInterface
	case semantic.EnumSymbol:
		return protocol.SymbolEnum
	case semantic.TraitSymbol:
		return protocol.SymbolStruct
	default:
		return protocol.SymbolClass
	}
}

var _ lsp.ImplementationProvider = (*Provider)(nil)
var _ lsp.TypeHierarchyProvider = (*Provider)(nil)
