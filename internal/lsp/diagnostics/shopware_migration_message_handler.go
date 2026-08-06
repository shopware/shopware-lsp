package diagnostics

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

const (
	messageHandlerSubscriberCode lsp.DiagnosticID = "shopware.migration.message_handler.subscriber"
	abstractMessageHandlerClass  string           = "Shopware\\Core\\Framework\\MessageQueue\\Handler\\AbstractMessageHandler"
)

func (p *ShopwareMigrationAnalyzer) messageHandlerMigrationProblems(
	ctx context.Context,
	root *phpsyntax.Node,
) []lsp.Problem {
	resolver := php.NewNameResolver(root)
	var result []lsp.Problem
	for _, class := range phpquery.Classes(root) {
		if ctx.Err() != nil {
			return result
		}
		extends := phpquery.DirectChild(class, phpsyntax.PhpExtendsClause)
		parent := phpquery.DirectChild(extends, phpsyntax.PhpName)
		if parent == nil || !strings.EqualFold(
			strings.Trim(resolver.Resolve(strings.TrimSpace(parent.Text())), "\\"),
			abstractMessageHandlerClass,
		) {
			continue
		}
		handle := phpOwnMethodForMigration(class, "handle")
		invoke := phpOwnMethodForMigration(class, "__invoke")
		safe := (handle != nil) != (invoke != nil)
		name := phpquery.DirectChild(class, phpsyntax.PhpName)
		rng := class.RangeTrimmedTrivia()
		if name != nil {
			rng = name.RangeTrimmedTrivia()
		}
		result = append(result, lsp.Problem{
			ID:       messageHandlerSubscriberCode,
			Range:    rng,
			Element:  class,
			Message:  "Shopware 6.5: migrate AbstractMessageHandler to an attributed Messenger subscriber",
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "shopware-rector",
			Payload: ShopwareMigrationPayload{
				Rule: "message-handler-subscriber",
				Kind: "class",
				Safe: safe,
			},
		})
	}
	return result
}
