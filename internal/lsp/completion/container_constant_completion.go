package completion

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

type ContainerConstantCompletionProvider struct {
	phpIndex *php.PHPIndex
}

func NewContainerConstantCompletionProvider(
	phpIndex *php.PHPIndex,
) *ContainerConstantCompletionProvider {
	return &ContainerConstantCompletionProvider{phpIndex: phpIndex}
}

func (p *ContainerConstantCompletionProvider) GetCompletions(
	_ context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.CompletionParams == nil || request.LineIndex == nil ||
		!isYAMLFile(request.TextDocument.URI) {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	reference, found := symfony.YAMLContainerConstantCompletionAt(
		request.DocumentContent,
		offset,
	)
	if !found {
		return nil
	}
	if scope := strings.Index(reference.Name, "::"); scope >= 0 {
		className := reference.Name[:scope]
		partial := reference.Name[scope+2:]
		replace := reference.Range
		replace.Start += uint32(scope + 2)
		return p.classConstantItems(
			className,
			partial,
			replace,
			request.LineIndex,
		)
	}
	return p.allConstantItems(
		reference.Name,
		reference.Range,
		request.LineIndex,
	)
}

func (p *ContainerConstantCompletionProvider) classConstantItems(
	className,
	partial string,
	replace cst.TextRange,
	lineIndex *cst.LineIndex,
) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	seen := make(map[string]struct{})
	for _, symbol := range p.phpIndex.Constants(className) {
		if symbol.Visibility != semantic.Public ||
			!strings.HasPrefix(
				strings.ToLower(symbol.Name),
				strings.ToLower(partial),
			) {
			continue
		}
		if _, duplicate := seen[symbol.Name]; duplicate {
			continue
		}
		seen[symbol.Name] = struct{}{}
		items = append(items, containerConstantCompletionItem(
			symbol.Name,
			className+"::"+symbol.Name,
			symbol,
			replace,
			lineIndex,
		))
	}
	sortConstantCompletionItems(items)
	return items
}

func (p *ContainerConstantCompletionProvider) allConstantItems(
	prefix string,
	replace cst.TextRange,
	lineIndex *cst.LineIndex,
) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	seen := make(map[string]struct{})
	for _, symbol := range p.phpIndex.ConstantSymbols() {
		if symbol.Kind != semantic.GlobalConstantSymbol &&
			symbol.Visibility != semantic.Public {
			continue
		}
		label := p.phpIndex.ConstantSymbolName(symbol)
		switch strings.ToLower(label) {
		case "true", "false", "null":
			continue
		}
		if !strings.HasPrefix(
			strings.ToLower(label),
			strings.ToLower(prefix),
		) {
			continue
		}
		if _, duplicate := seen[label]; duplicate {
			continue
		}
		seen[label] = struct{}{}
		items = append(items, containerConstantCompletionItem(
			label,
			label,
			symbol,
			replace,
			lineIndex,
		))
	}
	sortConstantCompletionItems(items)
	return items
}

func containerConstantCompletionItem(
	label,
	detail string,
	symbol semantic.Symbol,
	replace cst.TextRange,
	lineIndex *cst.LineIndex,
) protocol.CompletionItem {
	item := protocol.CompletionItem{
		Label:  label,
		Kind:   int(protocol.ConstantCompletion),
		Detail: detail,
		TextEdit: protocol.TextEdit{
			Range:   containerConstantCompletionRange(replace, lineIndex),
			NewText: label,
		},
	}
	if symbol.DocSummary != "" {
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = symbol.DocSummary
	}
	if symbol.Flags.Has(semantic.DeprecatedFlag) {
		item.Deprecated = true
	}
	return item
}

func containerConstantCompletionRange(
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
}

func sortConstantCompletionItems(items []protocol.CompletionItem) {
	sort.Slice(items, func(left, right int) bool {
		return strings.ToLower(items[left].Label) <
			strings.ToLower(items[right].Label)
	})
}

func isYAMLFile(uri string) bool {
	extension := strings.ToLower(filepath.Ext(uri))
	return extension == ".yaml" || extension == ".yml"
}

func (p *ContainerConstantCompletionProvider) GetTriggerCharacters() []string {
	return []string{":", "\\"}
}
