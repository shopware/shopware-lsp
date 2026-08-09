package semantic

import (
	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func (collector *adminMarkupTokenCollector) collectLexicalBindings() error {
	for _, binding := range admin.TwigVueBindings(
		collector.root,
		collector.request.Document.Text,
	) {
		if err := collector.ctx.Err(); err != nil {
			return err
		}
		for _, rangeValue := range admin.TwigVueBindingReferences(
			collector.root,
			collector.request.Document.Text,
			binding,
		) {
			if collector.seenLexical[rangeValue] {
				continue
			}
			collector.seenLexical[rangeValue] = true
			collector.appendToken(rangeValue, protocol.SemanticTokenVariable)
		}
	}
	return nil
}

func (collector *adminMarkupTokenCollector) collectMemberAccesses() error {
	for _, access := range admin.TwigVueExpressionMemberAccesses(
		collector.root,
		collector.request.Document.Text,
	) {
		if err := collector.ctx.Err(); err != nil {
			return err
		}
		matched, err := collector.memberAccessMatchesContract(access)
		if err != nil {
			return err
		}
		if !matched || collector.seenLexical[access.MemberRange] {
			continue
		}
		collector.seenLexical[access.MemberRange] = true
		collector.appendToken(access.MemberRange, protocol.SemanticTokenProperty)
	}
	return nil
}

func (collector *adminMarkupTokenCollector) memberAccessMatchesContract(
	access admin.TwigVueMemberAccess,
) (bool, error) {
	node := collector.root.NodeAtOffset(access.MemberRange.Start)
	resolvedSlot, err := collector.provider.index.ResolveTwigScopedSlotMemberForOwner(
		collector.root,
		node,
		collector.request.Document.Text,
		access.MemberRange.Start,
		collector.templatePath,
		collector.owner,
	)
	if err != nil {
		return false, err
	}
	resolvedVue, vueFound := admin.TwigVueBindingAtOffset(
		collector.root,
		collector.request.Document.Text,
		access.RootRange.Start,
	)
	lexicalMember := vueFound && resolvedVue != nil &&
		(resolvedSlot == nil || resolvedVue.ScopeRange.Len() <=
			resolvedSlot.Scope.TemplateRange.Len())
	if lexicalMember {
		return true, nil
	}
	if resolvedSlot != nil && resolvedSlot.MemberFound {
		return true, nil
	}
	resolvedInstance, err :=
		collector.provider.index.ResolveTwigVueInstanceMemberForComponent(
			collector.root,
			collector.request.Document.Text,
			access.MemberRange.Start,
			collector.templatePath,
			collector.owner,
		)
	return resolvedInstance != nil && resolvedInstance.MemberFound, err
}

func (collector *adminMarkupTokenCollector) collectRootIdentifiers() error {
	if collector.owner == nil {
		return nil
	}
	for _, identifier := range admin.TwigVueExpressionRootIdentifiers(
		collector.root,
		collector.request.Document.Text,
	) {
		if err := collector.ctx.Err(); err != nil {
			return err
		}
		if collector.seenLexical[identifier.Range] ||
			collector.isLexicalRootIdentifier(identifier) {
			continue
		}
		tokenType, found, err := collector.rootIdentifierTokenType(identifier)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		collector.seenLexical[identifier.Range] = true
		collector.appendToken(identifier.Range, tokenType)
	}
	return nil
}

func (collector *adminMarkupTokenCollector) isLexicalRootIdentifier(
	identifier admin.TwigVueMember,
) bool {
	binding, found := admin.TwigVueBindingAtOffset(
		collector.root,
		collector.request.Document.Text,
		identifier.Range.Start,
	)
	return found && binding != nil
}

func (collector *adminMarkupTokenCollector) rootIdentifierTokenType(
	identifier admin.TwigVueMember,
) (uint32, bool, error) {
	node := collector.root.NodeAtOffset(identifier.Range.Start)
	slotBinding, err := collector.provider.index.ResolveTwigScopedSlotBindingForOwner(
		collector.root,
		node,
		collector.request.Document.Text,
		identifier.Range.Start,
		collector.templatePath,
		collector.owner,
	)
	if err != nil {
		return 0, false, err
	}
	if slotBinding != nil {
		return protocol.SemanticTokenVariable, true, nil
	}
	member, found := collector.owner.TemplateMember(identifier.Name)
	if !found {
		member, found = admin.VueBuiltinMember(identifier.Name)
	}
	if !found {
		return 0, false, nil
	}
	if member.Kind == admin.ComponentMemberMethod {
		return protocol.SemanticTokenFunction, true, nil
	}
	return protocol.SemanticTokenVariable, true, nil
}
