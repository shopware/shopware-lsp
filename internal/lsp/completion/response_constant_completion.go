package completion

import (
	"context"
	"fmt"
	"sort"
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

const symfonyResponseClass = "Symfony\\Component\\HttpFoundation\\Response"

type ResponseConstantCompletionProvider struct {
	phpIndex *php.PHPIndex
}

func NewResponseConstantCompletionProvider(
	phpIndex *php.PHPIndex,
) *ResponseConstantCompletionProvider {
	return &ResponseConstantCompletionProvider{phpIndex: phpIndex}
}

func (p *ResponseConstantCompletionProvider) GetCompletions(
	ctx context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.CompletionParams == nil || request.LineIndex == nil ||
		request.Node == nil {
		return nil
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil ||
		phpContext.Snapshot == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	if !p.isStatusCodeContext(ctx, request, offset) {
		return nil
	}

	qualifier := responseClassQualifier(
		phpContext.Document,
		request.Root,
		offset,
	)
	members := (resolver.MemberResolver{
		Snapshot: phpContext.Snapshot,
	}).All(types.Named(symfonyResponseClass))
	var items []protocol.CompletionItem
	for _, member := range members {
		symbol := member.Symbol
		if symbol.Kind != semantic.ClassConstantSymbol ||
			symbol.Visibility != semantic.Public ||
			!strings.HasPrefix(symbol.Name, "HTTP_") {
			continue
		}
		insertText := qualifier + "::" + symbol.Name
		item := protocol.CompletionItem{
			Label:      insertText,
			FilterText: symbol.Name + " " + insertText,
			InsertText: insertText,
			Kind:       int(protocol.ConstantCompletion),
			Detail: fmt.Sprintf(
				"Symfony HTTP status constant · %s",
				symfonyResponseClass,
			),
		}
		if symbol.DocSummary != "" {
			item.Documentation.Kind = string(protocol.Markdown)
			item.Documentation.Value = symbol.DocSummary
		}
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool {
		return items[left].Label < items[right].Label
	})
	return items
}

func (p *ResponseConstantCompletionProvider) isStatusCodeContext(
	ctx context.Context,
	request *lsp.CompletionRequest,
	offset uint32,
) bool {
	if call := phpquery.CallAt(request.Node); call != nil &&
		strings.EqualFold(
			phpquery.CallMethodName(call),
			"setStatusCode",
		) &&
		responseActiveArgument(call, offset) == 0 &&
		p.phpIndex.IsMethodCalledOnClass(
			ctx,
			call,
			request.DocumentContent,
			symfonyResponseClass,
		) {
		return true
	}

	for current := request.Node; current != nil; current = current.Parent() {
		if current.Kind() != phpsyntax.PhpBinaryExpression {
			continue
		}
		var left *phpsyntax.Node
		for child := range current.ChildNodes() {
			left = child
			break
		}
		if left == nil || left.Range().End > offset ||
			!strings.EqualFold(
				phpquery.CallMethodName(left),
				"getStatusCode",
			) {
			return false
		}
		return p.phpIndex.IsMethodCalledOnClass(
			ctx,
			left,
			request.DocumentContent,
			symfonyResponseClass,
		)
	}
	return false
}

func responseActiveArgument(call *phpsyntax.Node, offset uint32) int {
	arguments := phpquery.Arguments(call)
	if len(arguments) == 0 {
		return 0
	}
	for index, argument := range arguments {
		if offset <= argument.Range().End {
			return index
		}
	}
	return len(arguments)
}

func responseClassQualifier(
	document *semantic.Document,
	root *phpsyntax.Node,
	offset uint32,
) string {
	for _, declaration := range phpquery.UseDeclarations(root) {
		for _, imported := range resolver.ParseUseDeclaration(
			declaration.Text(),
		) {
			if imported.Kind == resolver.ClassImport &&
				strings.EqualFold(
					strings.TrimPrefix(imported.Target, "\\"),
					symfonyResponseClass,
				) {
				return imported.Alias
			}
		}
	}
	if document != nil {
		if scope, found := document.ScopeAt(offset); found {
			if strings.EqualFold(
				scope.Namespace,
				"Symfony\\Component\\HttpFoundation",
			) {
				return "Response"
			}
		}
	}
	return "\\" + symfonyResponseClass
}

func (p *ResponseConstantCompletionProvider) GetTriggerCharacters() []string {
	return nil
}
