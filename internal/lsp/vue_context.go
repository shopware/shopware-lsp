package lsp

import (
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	vuesyntax "github.com/shopware/shopware-lsp/internal/parser/vue/syntax"
)

// EffectiveSyntaxLanguage maps a cursor in a mixed Vue SFC tree back to its
// embedded language. Other documents retain their registered language. This
// keeps feature providers simple while preserving Vue as a first-class file
// type for document-wide operations.
func EffectiveSyntaxLanguage(
	documentLanguage language.ID,
	node *cst.Node,
) language.ID {
	if documentLanguage != language.Vue {
		return documentLanguage
	}
	for current := node; current != nil; current = current.Parent() {
		switch current.Kind() {
		case vuesyntax.VueTemplateSection:
			return language.Twig
		case vuesyntax.VueScriptSection:
			return language.JavaScript
		case vuesyntax.VueStyleSection:
			return language.SCSS
		}
	}
	return language.Vue
}
