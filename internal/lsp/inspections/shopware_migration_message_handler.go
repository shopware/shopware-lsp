package inspections

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	phprewrite "github.com/shopware/shopware-lsp/internal/php/rewrite"
	"github.com/shopware/shopware-lsp/internal/rewrite"
)

const messageHandlerSubscriberFixID lsp.FixID = "shopware-rector-message-handler-subscriber"

const (
	messageSubscriberInterface = "Symfony\\Component\\Messenger\\Handler\\MessageSubscriberInterface"
	asMessageHandlerAttribute  = "Symfony\\Component\\Messenger\\Attribute\\AsMessageHandler"
)

type messageHandlerSubscriberFix struct{}

func (messageHandlerSubscriberFix) ID() lsp.FixID {
	return messageHandlerSubscriberFixID
}

func (messageHandlerSubscriberFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	return lsp.FixPresentation{
		Title:      "Shopware 6.5: Migrate message handler to subscriber",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Safe && payload.Rule == "message-handler-subscriber", err
}

func (messageHandlerSubscriberFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !payload.Safe || payload.Rule != "message-handler-subscriber" {
		return rewrite.WorkspacePlan{}, fmt.Errorf("message handler migration is no longer safe")
	}
	class, root, err := resolveMigrationClass(fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	handle := phpOwnMethodForMigrationFix(class, "handle")
	invoke := phpOwnMethodForMigrationFix(class, "__invoke")
	if handle != nil && invoke != nil {
		return rewrite.WorkspacePlan{}, fmt.Errorf("message handler method changed")
	}
	editor := phprewrite.NewEditor(fixContext.Document.Source, root)
	subscriber, referenceErr := editor.ClassReference(messageSubscriberInterface)
	if referenceErr != nil {
		return rewrite.WorkspacePlan{}, referenceErr
	}
	extends := phpquery.DirectChild(class, phpsyntax.PhpExtendsClause)
	if extends == nil {
		return rewrite.WorkspacePlan{}, fmt.Errorf("message handler parent changed")
	}
	if phpquery.DirectChild(class, phpsyntax.PhpImplementsClause) == nil {
		if err := editor.ReplaceRange(
			extends.RangeTrimmedTrivia(),
			"implements "+subscriber,
		); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	} else {
		removed, removeErr := editor.RemoveExtends(class)
		if removeErr != nil {
			return rewrite.WorkspacePlan{}, removeErr
		}
		if !removed {
			return rewrite.WorkspacePlan{}, fmt.Errorf("message handler parent changed")
		}
		if err := editor.AddImplements(class, subscriber); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	}
	resolver := php.NewNameResolver(root)
	if !classHasMigrationAttribute(class, resolver, asMessageHandlerAttribute) {
		attribute, attributeErr := editor.ClassReference(asMessageHandlerAttribute)
		if attributeErr != nil {
			return rewrite.WorkspacePlan{}, attributeErr
		}
		if err := editor.AddAttribute(class, attribute); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	}
	if handle != nil {
		name := phpquery.DirectChild(handle, phpsyntax.PhpName)
		if name == nil {
			return rewrite.WorkspacePlan{}, fmt.Errorf("message handler method name changed")
		}
		if err := editor.ReplaceRange(name.RangeTrimmedTrivia(), "__invoke"); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	}
	if handledMessages := phpOwnMethodForMigrationFix(class, "getHandledMessages"); handledMessages != nil {
		if err := editor.RemoveClassMember(handledMessages); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	}
	return finishPHPRewrite(fixContext, editor)
}

func classHasMigrationAttribute(
	class *phpsyntax.Node,
	resolver *php.NameResolver,
	target string,
) bool {
	for _, attribute := range phpquery.Attributes(class) {
		name := phpquery.AttributeName(attribute)
		if strings.EqualFold(strings.Trim(resolver.Resolve(name), "\\"), target) {
			return true
		}
	}
	return false
}
