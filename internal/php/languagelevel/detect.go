package languagelevel

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

// Occurrence ties a registered language feature to the syntax that uses it.
type Occurrence struct {
	Feature Feature
	Range   cst.TextRange
}

// Detect returns all version-gated syntax occurrences in source order. It
// intentionally performs no version filtering so diagnostics, completion,
// code actions, and tests can share the same syntax classification.
func Detect(root *phpsyntax.Node) []Occurrence {
	if root == nil {
		return nil
	}
	var result []Occurrence
	for element := range root.Descendants() {
		node, isNode := element.(*phpsyntax.Node)
		if !isNode {
			token := element.(*phpsyntax.Token)
			if token.Kind() == phpsyntax.TkNullsafeObjectOperator {
				result = append(result, Occurrence{
					Feature: NullsafeOperator,
					Range:   token.Range(),
				})
			}
			continue
		}

		switch node.Kind() {
		case phpsyntax.PhpAttributeGroup:
			result = appendOccurrence(result, Attributes, syntaxRange(node, phpsyntax.TkAttributeOpen))
		case phpsyntax.PhpNamedArgument:
			result = appendOccurrence(result, NamedArguments, syntaxRange(node, phpsyntax.TkColon))
		case phpsyntax.PhpMatchExpression:
			result = appendOccurrence(result, MatchExpressions, keywordRange(node, "match"))
		case phpsyntax.PhpThrowExpression:
			result = appendOccurrence(result, ThrowExpressions, keywordRange(node, "throw"))
		case phpsyntax.PhpEnumDeclaration:
			result = appendOccurrence(result, Enums, keywordRange(node, "enum"))
		case phpsyntax.PhpUnionType:
			if containsNode(node, phpsyntax.PhpIntersectionType) {
				result = appendOccurrence(result, DNFTypes, node.RangeTrimmedTrivia())
			} else {
				result = appendOccurrence(result, UnionTypes, node.RangeTrimmedTrivia())
			}
		case phpsyntax.PhpIntersectionType:
			if !hasAncestor(node, phpsyntax.PhpUnionType) {
				result = appendOccurrence(result, IntersectionTypes, node.RangeTrimmedTrivia())
			}
		case phpsyntax.PhpClassDeclaration:
			if textRange, found := directKeywordRange(node, "readonly"); found {
				result = appendOccurrence(result, ReadonlyClasses, textRange)
			}
		case phpsyntax.PhpPropertyDeclaration:
			if textRange, found := directKeywordRange(node, "readonly"); found {
				result = appendOccurrence(result, ReadonlyProperties, textRange)
			}
			if textRange, found := asymmetricVisibilityRange(node); found {
				result = appendOccurrence(result, AsymmetricVisibility, textRange)
			}
		case phpsyntax.PhpParameter:
			if promotedParameter(node) {
				result = appendOccurrence(result, PropertyPromotion, modifierRange(node))
			}
			if textRange, found := directKeywordRange(node, "readonly"); found {
				result = appendOccurrence(result, ReadonlyProperties, textRange)
			}
		case phpsyntax.PhpClassConstDeclaration:
			if hasDirectType(node) {
				result = appendOccurrence(result, TypedClassConstants, node.RangeTrimmedTrivia())
			}
		case phpsyntax.PhpPropertyHookList:
			result = appendOccurrence(result, PropertyHooks, syntaxRange(node, phpsyntax.TkOpenBrace))
		}
	}
	return result
}

func appendOccurrence(
	result []Occurrence,
	feature Feature,
	textRange cst.TextRange,
) []Occurrence {
	if textRange.End <= textRange.Start {
		return result
	}
	return append(result, Occurrence{Feature: feature, Range: textRange})
}

func syntaxRange(node *phpsyntax.Node, kind phpsyntax.Kind) cst.TextRange {
	for element := range node.Descendants() {
		if element.Kind() == kind {
			return element.Range()
		}
	}
	return node.RangeTrimmedTrivia()
}

func keywordRange(node *phpsyntax.Node, keyword string) cst.TextRange {
	for element := range node.Descendants() {
		if element.Kind() == phpsyntax.TkKeyword &&
			strings.EqualFold(element.Text(), keyword) {
			return element.Range()
		}
	}
	return node.RangeTrimmedTrivia()
}

func directKeywordRange(node *phpsyntax.Node, keyword string) (cst.TextRange, bool) {
	for token := range node.ChildTokens() {
		if token.Kind() == phpsyntax.TkKeyword &&
			strings.EqualFold(token.Text(), keyword) {
			return token.Range(), true
		}
	}
	return cst.TextRange{}, false
}

func containsNode(node *phpsyntax.Node, kind phpsyntax.Kind) bool {
	for element := range node.Descendants() {
		if descendant, ok := element.(*phpsyntax.Node); ok &&
			descendant != node && descendant.Kind() == kind {
			return true
		}
	}
	return false
}

func hasAncestor(node *phpsyntax.Node, kind phpsyntax.Kind) bool {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if parent.Kind() == kind {
			return true
		}
	}
	return false
}

func hasDirectType(node *phpsyntax.Node) bool {
	for child := range node.ChildNodes() {
		switch child.Kind() {
		case phpsyntax.PhpType, phpsyntax.PhpNullableType,
			phpsyntax.PhpUnionType, phpsyntax.PhpIntersectionType:
			return true
		}
	}
	return false
}

func promotedParameter(node *phpsyntax.Node) bool {
	if node == nil || node.Parent() == nil || node.Parent().Kind() != phpsyntax.PhpParameterList {
		return false
	}
	method := node.Parent().Parent()
	if method == nil || method.Kind() != phpsyntax.PhpMethodDeclaration ||
		!directNameEquals(method, "__construct") {
		return false
	}
	for _, modifier := range []string{"public", "protected", "private", "readonly"} {
		if _, found := directKeywordRange(node, modifier); found {
			return true
		}
	}
	return false
}

func directNameEquals(node *phpsyntax.Node, name string) bool {
	for child := range node.ChildNodes() {
		if child.Kind() == phpsyntax.PhpName {
			return strings.EqualFold(strings.TrimSpace(child.Text()), name)
		}
	}
	return false
}

func modifierRange(node *phpsyntax.Node) cst.TextRange {
	for token := range node.ChildTokens() {
		if token.Kind() != phpsyntax.TkKeyword {
			continue
		}
		switch strings.ToLower(token.Text()) {
		case "public", "protected", "private", "readonly":
			return token.Range()
		}
	}
	return node.RangeTrimmedTrivia()
}

func asymmetricVisibilityRange(node *phpsyntax.Node) (cst.TextRange, bool) {
	state := 0
	var start uint32
	for token := range node.ChildTokens() {
		if isTrivia(token.Kind()) {
			continue
		}
		switch state {
		case 0:
			if visibilityKeyword(token) {
				start = token.Range().Start
				state = 1
			}
		case 1:
			if token.Kind() == phpsyntax.TkOpenParen {
				state = 2
			} else if visibilityKeyword(token) {
				start = token.Range().Start
			} else {
				state = 0
			}
		case 2:
			if (token.Kind() == phpsyntax.TkIdentifier || token.Kind() == phpsyntax.TkKeyword) &&
				strings.EqualFold(token.Text(), "set") {
				state = 3
			} else {
				state = 0
			}
		case 3:
			if token.Kind() == phpsyntax.TkCloseParen {
				return cst.TextRange{Start: start, End: token.Range().End}, true
			}
			state = 0
		}
	}
	return cst.TextRange{}, false
}

func visibilityKeyword(token *phpsyntax.Token) bool {
	if token == nil || token.Kind() != phpsyntax.TkKeyword {
		return false
	}
	switch strings.ToLower(token.Text()) {
	case "public", "protected", "private":
		return true
	default:
		return false
	}
}

func isTrivia(kind phpsyntax.Kind) bool {
	switch kind {
	case phpsyntax.TkWhitespace, phpsyntax.TkLineBreak,
		phpsyntax.TkLineComment, phpsyntax.TkBlockComment:
		return true
	default:
		return false
	}
}
