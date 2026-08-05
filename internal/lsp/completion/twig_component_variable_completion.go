package completion

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigComponentVariableCompletionProvider struct {
	index    *twigcomponent.Index
	phpIndex *php.PHPIndex
}

func NewTwigComponentVariableCompletionProvider(
	index *twigcomponent.Index,
	phpIndex *php.PHPIndex,
) *TwigComponentVariableCompletionProvider {
	return &TwigComponentVariableCompletionProvider{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *TwigComponentVariableCompletionProvider) GetCompletions(
	_ context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil || request.Root == nil ||
		!strings.EqualFold(
			filepath.Ext(request.TextDocument.URI),
			".twig",
		) ||
		twigquery.ClosestNodeOfKind(
			request.Node,
			twigsyntax.TwigVar,
		) == nil {
		return nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil
	}
	components, props, err := p.index.ContextForTemplate(
		path,
		request.Root,
	)
	if err != nil || len(components) == 0 {
		return nil
	}
	if items := p.memberCompletions(request, components, props); items != nil {
		return items
	}
	items := make(map[string]protocol.CompletionItem)
	for _, prop := range props {
		items[strings.ToLower(prop.Name)] = protocol.CompletionItem{
			Label:  prop.Name,
			Kind:   int(protocol.VariableCompletion),
			Detail: componentPropDetail(prop),
		}
	}
	computed, _ := p.index.ComputedForTemplate(path)
	if len(computed) != 0 {
		items["computed"] = protocol.CompletionItem{
			Label:  "computed",
			Kind:   int(protocol.VariableCompletion),
			Detail: "cached component getters",
		}
	}
	for _, component := range components {
		if component.Class == "" {
			continue
		}
		items["this"] = protocol.CompletionItem{
			Label:  "this",
			Kind:   int(protocol.VariableCompletion),
			Detail: component.Class,
		}
		break
	}
	result := make([]protocol.CompletionItem, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	sortCompletionItems(result)
	return result
}

func (p *TwigComponentVariableCompletionProvider) memberCompletions(
	request *lsp.CompletionRequest,
	components []twigcomponent.Component,
	props []twigcomponent.Prop,
) []protocol.CompletionItem {
	if p.phpIndex == nil {
		return nil
	}
	accessor := twigquery.ClosestNodeOfKind(
		request.Node,
		twigsyntax.TwigAccessor,
	)
	if accessor == nil {
		return nil
	}
	parts := strings.Split(compactTwigAccessor(accessor.Text()), ".")
	if len(parts) < 2 {
		return nil
	}
	for _, part := range parts[:len(parts)-1] {
		if !isTwigIdentifier(part) {
			return nil
		}
	}
	if parts[0] == "computed" {
		path, err := uriutil.Path(request.TextDocument.URI)
		if err != nil {
			return nil
		}
		computed, err := p.index.ComputedForTemplate(path)
		if err != nil {
			return nil
		}
		items := make([]protocol.CompletionItem, 0, len(computed))
		for _, prop := range computed {
			items = append(items, protocol.CompletionItem{
				Label:  prop.Name,
				Kind:   int(protocol.PropertyCompletion),
				Detail: prop.Type,
			})
		}
		sortCompletionItems(items)
		return items
	}
	var receiver types.Type
	if parts[0] == "this" {
		for _, component := range components {
			if component.Class != "" {
				receiver = types.Named(component.Class)
				break
			}
		}
	} else {
		for _, prop := range props {
			if prop.Name != parts[0] || prop.Type == "" {
				continue
			}
			parsed, err := types.Parse(prop.Type)
			if err == nil {
				receiver = parsed
			}
			break
		}
	}
	if receiver.IsUnknown() {
		return nil
	}
	snapshot := p.phpIndex.SemanticSnapshot()
	for _, segment := range parts[1 : len(parts)-1] {
		receiver = twigMemberType(snapshot, receiver, segment)
		if receiver.IsUnknown() {
			return []protocol.CompletionItem{}
		}
	}
	return twigPHPMemberCompletions(snapshot, receiver)
}

func (p *TwigComponentVariableCompletionProvider) GetTriggerCharacters() []string {
	return []string{"."}
}

func componentPropDetail(prop twigcomponent.Prop) string {
	var suffix string
	switch {
	case prop.Live && prop.Writable:
		suffix = "writable live prop"
	case prop.Live:
		suffix = "live prop"
	}
	if prop.Type == "" {
		return suffix
	}
	if suffix == "" {
		return prop.Type
	}
	return prop.Type + " • " + suffix
}
