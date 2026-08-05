package completion

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/twig"
)

func (p *TwigCompletionProvider) twigSeeCompletions(
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || request == nil || request.CompletionParams == nil ||
		request.Root == nil || request.LineIndex == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	completion, found := twig.SeeCompletionAt(request.Root, offset)
	if !found {
		return nil
	}
	if className, memberPrefix, method := twigSeeMethodPrefix(
		completion.Prefix,
	); method {
		return p.twigSeeMethodCompletions(
			request,
			completion,
			className,
			memberPrefix,
		)
	}

	startLine, startCharacter := request.LineIndex.PositionUTF16(
		completion.Range.Start,
	)
	endLine, endCharacter := request.LineIndex.PositionUTF16(
		completion.Range.End,
	)
	replace := protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
	prefix := strings.ToLower(completion.Prefix)
	items := make(map[string]protocol.CompletionItem)
	if p.phpIndex != nil && !strings.ContainsAny(prefix, "@/:.") {
		classPrefix := strings.TrimLeft(prefix, `\`)
		leadingSlash := strings.HasPrefix(completion.Prefix, `\`)
		for _, symbol := range p.phpIndex.ClassSymbols() {
			name := strings.TrimLeft(symbol.FullyQualified, `\`)
			if classPrefix != "" && !strings.HasPrefix(
				strings.ToLower(name),
				classPrefix,
			) {
				continue
			}
			insert := name
			if leadingSlash {
				insert = `\` + insert
			}
			kind := protocol.ClassCompletion
			switch symbol.Kind {
			case semantic.InterfaceSymbol:
				kind = protocol.InterfaceCompletion
			case semantic.EnumSymbol:
				kind = protocol.EnumCompletion
			}
			item := protocol.CompletionItem{
				Label:      name,
				Kind:       int(kind),
				Detail:     "PHP symbol for Twig @see",
				Deprecated: symbol.Flags.Has(semantic.DeprecatedFlag),
				TextEdit: protocol.TextEdit{
					Range:   replace,
					NewText: insert,
				},
			}
			items["php\x00"+strings.ToLower(name)] = item
		}
	}
	if p.twigIndexer != nil {
		templates, _ := p.twigIndexer.GetAllTemplateFiles()
		for _, template := range templates {
			if prefix != "" && !strings.HasPrefix(
				strings.ToLower(template),
				prefix,
			) {
				continue
			}
			items["twig\x00"+strings.ToLower(template)] =
				protocol.CompletionItem{
					Label:  template,
					Kind:   int(protocol.FileCompletion),
					Detail: "Twig template for @see",
					TextEdit: protocol.TextEdit{
						Range:   replace,
						NewText: template,
					},
				}
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

func (p *TwigCompletionProvider) twigSeeMethodCompletions(
	request *lsp.CompletionRequest,
	completion twig.SeeCompletionContext,
	className,
	prefix string,
) []protocol.CompletionItem {
	if p.phpIndex == nil {
		return []protocol.CompletionItem{}
	}
	class, found := p.phpIndex.FindClass(className)
	if !found {
		return []protocol.CompletionItem{}
	}
	delimiter := strings.LastIndex(completion.Prefix, ":") + 1
	start := completion.Range.Start + uint32(delimiter)
	startLine, startCharacter := request.LineIndex.PositionUTF16(start)
	endLine, endCharacter := request.LineIndex.PositionUTF16(
		completion.Range.End,
	)
	replace := protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
	var items []protocol.CompletionItem
	for _, member := range (phpresolver.MemberResolver{
		Snapshot: p.phpIndex.SemanticSnapshot(),
	}).All(types.Named(class.FullyQualified)) {
		symbol := member.Symbol
		if symbol.Kind != semantic.MethodSymbol ||
			symbol.Visibility != semantic.Public ||
			prefix != "" && !strings.HasPrefix(
				strings.ToLower(symbol.Name),
				strings.ToLower(prefix),
			) {
			continue
		}
		items = append(items, protocol.CompletionItem{
			Label:      symbol.Name,
			Kind:       int(protocol.MethodCompletion),
			Detail:     member.Type.String() + " · Twig @see method",
			Deprecated: symbol.Flags.Has(semantic.DeprecatedFlag),
			TextEdit: protocol.TextEdit{
				Range:   replace,
				NewText: symbol.Name,
			},
		})
	}
	sort.Slice(items, func(left, right int) bool {
		return strings.ToLower(items[left].Label) <
			strings.ToLower(items[right].Label)
	})
	return items
}

func twigSeeMethodPrefix(
	prefix string,
) (string, string, bool) {
	delimiter := strings.LastIndex(prefix, "::")
	width := 2
	if delimiter < 0 && strings.Count(prefix, ":") == 1 {
		delimiter = strings.LastIndex(prefix, ":")
		width = 1
	}
	if delimiter < 0 {
		return "", "", false
	}
	className := strings.TrimLeft(prefix[:delimiter], `\`)
	if className == "" {
		return "", "", false
	}
	return className, prefix[delimiter+width:], true
}
