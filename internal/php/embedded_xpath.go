package php

import "github.com/shopware/shopware-lsp/internal/parser/cst"

// EmbeddedXPathExpression names a decoded static PHP string recognized as an
// XPath expression by a typed Symfony DomCrawler call.
type EmbeddedXPathExpression = EmbeddedPHPString

// EmbeddedXPathExpressions recognizes the reference plugin's Crawler
// filterXPath() and evaluate() injection signatures.
func EmbeddedXPathExpressions(
	index *PHPIndex,
	path string,
	version int,
	source string,
	root *cst.Node,
) []EmbeddedXPathExpression {
	var result []EmbeddedXPathExpression
	for _, expression := range EmbeddedLanguageStrings(
		index,
		path,
		version,
		source,
		root,
	) {
		if expression.Language == EmbeddedLanguageXPath {
			result = append(result, expression.EmbeddedPHPString)
		}
	}
	return result
}
