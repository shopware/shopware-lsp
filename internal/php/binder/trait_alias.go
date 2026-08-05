package binder

import (
	"strings"

	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

func bindTraitAliases(
	node *phpsyntax.Node,
	context resolver.NameContext,
	usedTraits []string,
) []semantic.TraitAlias {
	if node == nil {
		return nil
	}
	insideAdaptations := false
	var statement []string
	var result []semantic.TraitAlias
	flush := func() {
		if alias, ok := parseTraitAlias(statement, context, usedTraits); ok {
			result = append(result, alias)
		}
		statement = statement[:0]
	}
	for index := 0; index < node.ChildCount(); index++ {
		element := node.Child(index)
		token, ok := element.(*phpsyntax.Token)
		if !ok || token.Kind().IsTrivia() {
			continue
		}
		switch token.Kind() {
		case phpsyntax.TkOpenBrace:
			insideAdaptations = true
			continue
		case phpsyntax.TkCloseBrace:
			flush()
			insideAdaptations = false
			continue
		case phpsyntax.TkSemicolon:
			if insideAdaptations {
				flush()
			}
			continue
		}
		if insideAdaptations {
			statement = append(statement, token.Text())
		}
	}
	return result
}

func parseTraitAlias(
	parts []string,
	context resolver.NameContext,
	usedTraits []string,
) (semantic.TraitAlias, bool) {
	asIndex := -1
	for index, part := range parts {
		if strings.EqualFold(part, "as") {
			asIndex = index
			break
		}
	}
	if asIndex <= 0 || asIndex+1 >= len(parts) {
		return semantic.TraitAlias{}, false
	}

	before := strings.Join(parts[:asIndex], "")
	method := before
	trait := ""
	if separator := strings.LastIndex(before, "::"); separator >= 0 {
		trait = context.ResolveClass(before[:separator])
		method = before[separator+2:]
	} else if len(usedTraits) == 1 {
		trait = usedTraits[0]
	}
	method = strings.TrimSpace(method)
	if trait == "" || method == "" {
		return semantic.TraitAlias{}, false
	}

	alias := semantic.TraitAlias{
		Trait:  trait,
		Method: method,
		Alias:  method,
	}
	for _, part := range parts[asIndex+1:] {
		switch strings.ToLower(part) {
		case "public":
			alias.Visibility = semantic.Public
			alias.HasVisibility = true
		case "protected":
			alias.Visibility = semantic.Protected
			alias.HasVisibility = true
		case "private":
			alias.Visibility = semantic.Private
			alias.HasVisibility = true
		default:
			alias.Alias = strings.TrimSpace(part)
		}
	}
	if alias.Alias == "" {
		alias.Alias = method
	}
	return alias, true
}
