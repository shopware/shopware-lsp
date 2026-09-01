package admin

import (
	"strings"

	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

func TwigSymbolAtOffset(
	root *twigsyntax.Node,
	offset uint32,
) (AdminSymbolTarget, bool) {
	if root == nil {
		return AdminSymbolTarget{}, false
	}
	if reference, found := TwigRegistryReferenceAtOffset(root, offset); found &&
		reference.Name != "" {
		return AdminSymbolTarget{Kind: reference.Kind, Name: reference.Name}, true
	}
	for _, node := range twigquery.Nodes(root, twigsyntax.HtmlStartingTag) {
		if target, found := twigStartingTagSymbolAtOffset(node, offset); found {
			return target, true
		}
	}
	return twigEndingTagSymbolAtOffset(root, offset)
}

func twigStartingTagSymbolAtOffset(
	node *twigsyntax.Node,
	offset uint32,
) (AdminSymbolTarget, bool) {
	tag, ok := twigast.CastHtmlStartingTag(node)
	if !ok || tag.Name() == nil {
		return AdminSymbolTarget{}, false
	}
	tagName := tag.Name().Text()
	selector, dynamic := TwigDynamicComponentSelector(node)
	if dynamic {
		if candidate, found := selector.CandidateAt(offset); found {
			return AdminSymbolTarget{
				Kind: AdminSymbolComponent, Name: candidate.Name,
			}, true
		}
	}
	contractName := tagName
	if resolvedName, found := StaticComponentNameForTag(node); found {
		contractName = resolvedName
	}
	if IsComponentTag(tagName) && rangeContains(tag.Name().Range(), offset) {
		return AdminSymbolTarget{
			Kind: AdminSymbolComponent, Name: tagName,
		}, true
	}
	for attribute := range tag.Attributes() {
		if target, found := twigAttributeSymbolAtOffset(
			node,
			attribute,
			contractName,
			selector,
			dynamic,
			offset,
		); found {
			return target, true
		}
	}
	if tagName == "slot" {
		return twigSlotDeclarationAtOffset(tag, offset)
	}
	return AdminSymbolTarget{}, false
}

func twigAttributeSymbolAtOffset(
	node *twigsyntax.Node,
	attribute twigast.HtmlAttribute,
	contractName string,
	selector VueDynamicComponentSelector,
	dynamic bool,
	offset uint32,
) (AdminSymbolTarget, bool) {
	nameToken := attribute.Name()
	if nameToken == nil {
		return AdminSymbolTarget{}, false
	}
	attributeName := twigquery.HTMLAttributeName(attribute.Syntax())
	if directive, found := VueDirectiveReferenceForAttribute(
		attributeName,
		nameToken.Range(),
	); found && rangeContains(directive.Range, offset) {
		return AdminSymbolTarget{
			Kind: AdminSymbolDirective, Name: directive.Name,
		}, true
	}
	if attributeName == "v-bind" && IsComponentTag(contractName) {
		if target, found := twigObjectBindingSymbolAtOffset(
			attribute,
			contractName,
			offset,
		); found {
			return target, true
		}
	}
	if !rangeContains(nameToken.Range(), offset) {
		return AdminSymbolTarget{}, false
	}
	componentAttribute := IsComponentTag(contractName) &&
		(!dynamic || attributeName != selector.AttributeName)
	if componentAttribute {
		if target, found := twigModelOrEventTarget(attributeName, contractName); found {
			return target, true
		}
	}
	if slotName := NormalizeSlotName(attributeName); slotName != "" &&
		attributeName != "v-slot" {
		owner := contractName
		if !IsComponentTag(owner) {
			owner = parentComponentName(node)
		}
		if owner != "" {
			return AdminSymbolTarget{
				Kind: AdminSymbolComponentSlot, Owner: owner, Name: slotName,
			}, true
		}
	}
	if componentAttribute {
		if propName := NormalizePropName(attributeName); propName != "" {
			return AdminSymbolTarget{
				Kind: AdminSymbolComponentProp, Owner: contractName, Name: propName,
			}, true
		}
	}
	return AdminSymbolTarget{}, false
}

func twigObjectBindingSymbolAtOffset(
	attribute twigast.HtmlAttribute,
	contractName string,
	offset uint32,
) (AdminSymbolTarget, bool) {
	value, ok := attribute.Value()
	if !ok {
		return AdminSymbolTarget{}, false
	}
	inner, ok := value.GetInner()
	if !ok {
		return AdminSymbolTarget{}, false
	}
	fields, _ := VueObjectBindingFields(
		inner.Syntax().Text(),
		inner.Syntax().Range().Start,
	)
	for _, field := range fields {
		if rangeContains(field.NameRange, offset) {
			return AdminSymbolTarget{
				Kind:  AdminSymbolComponentProp,
				Owner: contractName,
				Name:  NormalizePropName(field.Name),
			}, true
		}
	}
	return AdminSymbolTarget{}, false
}

func twigModelOrEventTarget(
	attributeName,
	contractName string,
) (AdminSymbolTarget, bool) {
	if argument, model := NormalizeModelArgument(attributeName); model {
		name := "v-model"
		if argument != "" {
			name += ":" + CamelToKebab(argument)
		}
		return AdminSymbolTarget{
			Kind: AdminSymbolComponentModel, Owner: contractName, Name: name,
		}, true
	}
	if eventName := NormalizeEventName(attributeName); eventName != "" {
		return AdminSymbolTarget{
			Kind: AdminSymbolComponentEvent, Owner: contractName, Name: eventName,
		}, true
	}
	return AdminSymbolTarget{}, false
}

func twigSlotDeclarationAtOffset(
	tag twigast.HtmlStartingTag,
	offset uint32,
) (AdminSymbolTarget, bool) {
	for attribute := range tag.Attributes() {
		if twigquery.HTMLAttributeName(attribute.Syntax()) != "name" {
			continue
		}
		value, ok := attribute.Value()
		if !ok {
			continue
		}
		inner, ok := value.GetInner()
		if !ok || !rangeContains(inner.Syntax().Range(), offset) {
			continue
		}
		if name := strings.TrimSpace(inner.Syntax().Text()); name != "" {
			return AdminSymbolTarget{
				Kind: AdminSymbolComponentSlot, Name: name,
			}, true
		}
	}
	return AdminSymbolTarget{}, false
}

func twigEndingTagSymbolAtOffset(
	root *twigsyntax.Node,
	offset uint32,
) (AdminSymbolTarget, bool) {
	for _, node := range twigquery.Nodes(root, twigsyntax.HtmlEndingTag) {
		tag, ok := twigast.CastHtmlEndingTag(node)
		if !ok || tag.Name() == nil || !IsComponentTag(tag.Name().Text()) ||
			!rangeContains(tag.Name().Range(), offset) {
			continue
		}
		return AdminSymbolTarget{
			Kind: AdminSymbolComponent, Name: tag.Name().Text(),
		}, true
	}
	return AdminSymbolTarget{}, false
}
