package completion

import (
	"context"
	"path/filepath"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

func (p *AdminCompletionProvider) twigCompletions(_ context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	node := params.Node
	token := params.Token
	content := params.DocumentContent
	offset, hasOffset := adminCompletionOffset(params)
	templatePath := adminTemplatePath(params.TextDocument.URI)
	liveOwner, _ := p.adminIndexer.GetComponentForDocument(
		templatePath, params.Root, string(content), params.LineIndex,
	)
	if hasOffset && admin.IsTwigBlockNameCompletionAt(node, offset) {
		return p.getParentBlockCompletions(templatePath)
	}

	if reference, found := adminTwigRegistryReferenceAtCompletion(params); found {
		switch reference.Kind {
		case admin.AdminSymbolPrivilege:
			return p.getPrivilegeCompletions()
		case admin.AdminSymbolModuleRoute:
			return p.getModuleRouteCompletions()
		}
	}
	if hasOffset {
		if items, handled := p.componentPropValueCompletions(
			params, offset, templatePath, liveOwner,
		); handled {
			return items
		}
	}
	if selector, dynamicComponent := adminDynamicComponentSelectorAtCompletion(
		params, offset, hasOffset,
	); dynamicComponent {
		return p.getDynamicComponentCompletions(
			templatePath, selector, offset, liveOwner,
		)
	}
	if hasOffset {
		if startTag, fields, objectBinding :=
			admin.TwigComponentObjectBindingKeyContextAtOffset(
				params.Root, offset,
			); objectBinding {
			return p.objectBindingPropCompletions(
				startTag, fields, templatePath, liveOwner,
			)
		}
	}

	resolvedSlots, _ := p.scopedSlotsAtCompletion(params)
	var resolvedSlot *admin.ResolvedTwigScopedSlot
	if len(resolvedSlots) > 0 {
		resolvedSlot = &resolvedSlots[len(resolvedSlots)-1]
	}
	vueBindings := p.twigVueBindingsAtCompletion(params, offset, hasOffset)
	if hasOffset && admin.IsTwigVueExpressionAt(node, offset) {
		if members, handled := p.twigVueMemberCompletionsAt(
			params, resolvedSlot, vueBindings, offset,
		); handled {
			return members
		}
	}
	if resolved := resolvedSlot; resolved != nil {
		if resolved.Scope.IsBindingOffset(offset) {
			return scopedSlotContractCompletions(*resolved)
		}
		if admin.IsTwigVueExpressionAt(node, offset) {
			return mergeAdminLexicalCompletions(
				p.getTemplateMemberCompletionsAt(
					params.TextDocument.URI, params.Node, params.Document,
				),
				resolvedSlots,
				vueBindings,
			)
		}
	}

	if twigquery.ClosestNodeOfKind(node, twigsyntax.TwigVar) != nil ||
		adminTwigVueExpressionAtCompletion(params) {
		return mergeAdminLexicalCompletions(
			p.getTemplateMemberCompletionsAt(
				params.TextDocument.URI, params.Node, params.Document,
			),
			resolvedSlots,
			vueBindings,
		)
	}

	// Check if we're in an HTML tag name position
	if p.isInHTMLTagName(node, token, content) {
		return p.getComponentTagCompletionsForOwner(templatePath, liveOwner)
	}

	// Check if we're in a slot name position (# or v-slot:)
	if items, handled := p.getResolvedSlotCompletions(
		node, content, templatePath, liveOwner,
	); handled {
		return items
	}
	if items, handled := p.dynamicComponentAttributeCompletions(
		node, templatePath, liveOwner,
	); handled {
		return items
	}

	// Custom directives are valid on both native elements and components. On a
	// component, merge them with its public prop/event/model contract.
	if p.isInHTMLAttributeCompletionPosition(node) {
		directives := p.getDirectiveCompletions(true, templatePath)
		if componentName := p.getComponentNameForAttributeCompletion(
			node, content,
		); componentName != "" {
			return mergeAdminCompletions(
				p.getComponentPropCompletionsForOwner(
					componentName, templatePath, liveOwner,
				),
				directives,
			)
		}
		return directives
	}

	return []protocol.CompletionItem{}
}

func (p *AdminCompletionProvider) getParentBlockCompletions(
	templatePath string,
) []protocol.CompletionItem {
	if p == nil || p.adminIndexer == nil || templatePath == "" {
		return nil
	}
	parent, err := p.adminIndexer.GetParentComponentForTemplate(templatePath)
	if err != nil || parent == nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(parent.Blocks))
	for _, block := range parent.Blocks {
		if block.Name == "" {
			continue
		}
		item := protocol.CompletionItem{
			Label: block.Name, Kind: int(protocol.MethodCompletion),
			Detail: "Administration Twig block · " + parent.Name,
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = "Extensibility block declared by Administration component `" +
			parent.Name + "`."
		if block.FilePath != "" {
			item.Documentation.Value += "\n\n**Source:** `" +
				filepath.ToSlash(block.FilePath) + "`"
		}
		markAdminCompletionDeprecated(&item, block.Deprecated)
		items = append(items, item)
	}
	sortCompletionItems(items)
	return items
}

// getResolvedSlotCompletions resolves the component contract owning a slot
// directive. Closed dynamic component selectors contribute only slots shared
// by every possible component, so accepting a completion stays type-safe.
