package phpsemantic

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

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
		if candidate.DocSummary() != "" {
			signature.Documentation = &protocol.MarkupContent{
				Kind:  protocol.Markdown,
				Value: candidate.DocSummary(),
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
