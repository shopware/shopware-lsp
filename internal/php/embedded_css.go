package php

import "github.com/shopware/shopware-lsp/internal/parser/cst"

// EmbeddedCSSSelector names a decoded static PHP string recognized as a CSS
// selector by a typed Symfony DomCrawler or CssSelector call.
type EmbeddedCSSSelector = EmbeddedPHPString

// EmbeddedCSSSelectors recognizes the CSS selector signatures injected by the
// reference plugin: Crawler::filter(), Crawler::children(), and
// CssSelectorConverter::toXPath().
func EmbeddedCSSSelectors(
	index *PHPIndex,
	path string,
	version int,
	source string,
	root *cst.Node,
) []EmbeddedCSSSelector {
	var result []EmbeddedCSSSelector
	for _, selector := range EmbeddedLanguageStrings(
		index,
		path,
		version,
		source,
		root,
	) {
		if selector.Language == EmbeddedLanguageCSS {
			result = append(result, selector.EmbeddedPHPString)
		}
	}
	return result
}
