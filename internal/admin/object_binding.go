package admin

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// VueObjectBindingField is one statically named property forwarded by a
// component-level v-bind object. Shorthand retains its dual meaning: the name
// is both the public component prop and the source expression.
type VueObjectBindingField struct {
	Name            string
	Expression      string
	NameRange       cst.TextRange
	ExpressionRange cst.TextRange
	Shorthand       bool
}

// VueObjectBindingFields extracts source-aware fields from an object literal.
// Complete is false for spreads, computed keys, methods, malformed entries, or
// any trailing runtime expression. Statically named siblings are still
// returned so navigation and completion do not disappear around one spread.
func VueObjectBindingFields(
	value string,
	base uint32,
) ([]VueObjectBindingField, bool) {
	open := skipObjectBindingSpace(value, 0)
	if open >= len(value) || value[open] != '{' {
		return nil, false
	}
	close := matchingSlotDelimiter(value, open, '{', '}')
	if close < 0 {
		return nil, false
	}
	complete := strings.TrimSpace(value[close+1:]) == ""
	segments := vueObjectBindingSegments(value, open+1, close)
	result := make([]VueObjectBindingField, 0, len(segments))
	for _, segment := range segments {
		field, found := vueObjectBindingField(value, base, segment)
		if !found {
			if strings.TrimSpace(value[segment.start:segment.end]) != "" {
				complete = false
			}
			continue
		}
		result = append(result, field)
	}
	return result, complete
}

type vueObjectBindingSegment struct {
	start int
	end   int
}

func vueObjectBindingSegments(
	value string,
	start,
	end int,
) []vueObjectBindingSegment {
	var result []vueObjectBindingSegment
	segmentStart := start
	state := slotScanState{}
	for index := start; index < end; index++ {
		if value[index] == ',' && state.topLevel() {
			result = append(result, vueObjectBindingSegment{
				start: segmentStart, end: index,
			})
			segmentStart = index + 1
			continue
		}
		state.consume(value[index])
	}
	result = append(result, vueObjectBindingSegment{
		start: segmentStart, end: end,
	})
	return result
}

func vueObjectBindingField(
	value string,
	base uint32,
	segment vueObjectBindingSegment,
) (VueObjectBindingField, bool) {
	start := skipObjectBindingSpace(value, segment.start)
	end := trimObjectBindingSpace(value, start, segment.end)
	if start >= end || strings.HasPrefix(value[start:end], "...") {
		return VueObjectBindingField{}, false
	}
	colon := objectBindingTopLevelColon(value, start, end)
	keyEnd := end
	expressionStart := start
	shorthand := colon < 0
	if colon >= 0 {
		keyEnd = trimObjectBindingSpace(value, start, colon)
		expressionStart = skipObjectBindingSpace(value, colon+1)
		if expressionStart >= end {
			return VueObjectBindingField{}, false
		}
	}
	keyStart := start
	keyEnd = trimObjectBindingSpace(value, keyStart, keyEnd)
	if keyStart >= keyEnd {
		return VueObjectBindingField{}, false
	}
	name := value[keyStart:keyEnd]
	if len(name) >= 2 && (name[0] == '\'' || name[0] == '"') &&
		name[len(name)-1] == name[0] {
		keyStart++
		keyEnd--
		name = value[keyStart:keyEnd]
	}
	if !isSlotPropertyName(name) {
		return VueObjectBindingField{}, false
	}
	if shorthand && !isSlotIdentifier(name) {
		return VueObjectBindingField{}, false
	}
	expressionEnd := end
	if shorthand {
		expressionStart = keyStart
		expressionEnd = keyEnd
	}
	return VueObjectBindingField{
		Name:       name,
		Expression: strings.TrimSpace(value[expressionStart:expressionEnd]),
		NameRange: cst.TextRange{
			Start: base + uint32(keyStart), End: base + uint32(keyEnd),
		},
		ExpressionRange: cst.TextRange{
			Start: base + uint32(expressionStart),
			End:   base + uint32(expressionEnd),
		},
		Shorthand: shorthand,
	}, true
}

func objectBindingTopLevelColon(value string, start, end int) int {
	state := slotScanState{}
	for index := start; index < end; index++ {
		if value[index] == ':' && state.topLevel() {
			return index
		}
		state.consume(value[index])
	}
	return -1
}

func skipObjectBindingSpace(value string, start int) int {
	for start < len(value) && isSlotSpace(value[start]) {
		start++
	}
	return start
}

func trimObjectBindingSpace(value string, start, end int) int {
	for end > start && isSlotSpace(value[end-1]) {
		end--
	}
	return end
}

// TwigComponentObjectBindingFieldAtOffset resolves a public prop key inside a
// component-level v-bind object. Expression values are deliberately excluded.
func TwigComponentObjectBindingFieldAtOffset(
	root *twigsyntax.Node,
	offset uint32,
) (*twigsyntax.Node, VueObjectBindingField, bool) {
	if root == nil {
		return nil, VueObjectBindingField{}, false
	}
	for startTag := range twigquery.IterateNodes(root, twigsyntax.HtmlStartingTag) {
		if offset < startTag.Range().Start || offset > startTag.Range().End {
			continue
		}
		tag, ok := twigast.CastHtmlStartingTag(startTag)
		if !ok {
			continue
		}
		for attribute := range tag.Attributes() {
			if twigquery.HTMLAttributeName(attribute.Syntax()) != "v-bind" {
				continue
			}
			value, ok := attribute.Value()
			if !ok {
				continue
			}
			inner, ok := value.GetInner()
			if !ok || offset < inner.Syntax().Range().Start ||
				offset > inner.Syntax().Range().End {
				continue
			}
			fields, _ := VueObjectBindingFields(
				inner.Syntax().Text(), inner.Syntax().Range().Start,
			)
			for _, field := range fields {
				if offset >= field.NameRange.Start && offset <= field.NameRange.End {
					return startTag, field, true
				}
			}
		}
	}
	return nil, VueObjectBindingField{}, false
}

// TwigComponentObjectBindingValueAtOffset resolves a direct string-literal
// expression inside a component-level v-bind object. The returned range covers
// only the literal content, so completion can replace a value while preserving
// its quotes and the surrounding Vue expression.
func TwigComponentObjectBindingValueAtOffset(
	root *twigsyntax.Node,
	offset uint32,
) (*twigsyntax.Node, VueObjectBindingField, cst.TextRange, bool) {
	if root == nil {
		return nil, VueObjectBindingField{}, cst.TextRange{}, false
	}
	for startTag := range twigquery.IterateNodes(root, twigsyntax.HtmlStartingTag) {
		if offset < startTag.Range().Start || offset > startTag.Range().End {
			continue
		}
		tag, ok := twigast.CastHtmlStartingTag(startTag)
		if !ok {
			continue
		}
		for attribute := range tag.Attributes() {
			if twigquery.HTMLAttributeName(attribute.Syntax()) != "v-bind" {
				continue
			}
			value, ok := attribute.Value()
			if !ok {
				continue
			}
			inner, ok := value.GetInner()
			if !ok || offset < inner.Syntax().Range().Start ||
				offset > inner.Syntax().Range().End {
				continue
			}
			fields, _ := VueObjectBindingFields(
				inner.Syntax().Text(), inner.Syntax().Range().Start,
			)
			for _, field := range fields {
				if field.Shorthand {
					continue
				}
				_, contentStart, contentEnd, literal :=
					VueStaticStringLiteral(field.Expression)
				if !literal {
					continue
				}
				valueRange := cst.TextRange{
					Start: field.ExpressionRange.Start + contentStart,
					End:   field.ExpressionRange.Start + contentEnd,
				}
				if offset >= valueRange.Start && offset <= valueRange.End {
					return startTag, field, valueRange, true
				}
			}
		}
	}
	return nil, VueObjectBindingField{}, cst.TextRange{}, false
}

// TwigComponentObjectBindingKeyContextAtOffset reports a top-level key-editing
// position inside a component v-bind object. It remains useful for incomplete
// literals, which is essential while completion is being requested.
func TwigComponentObjectBindingKeyContextAtOffset(
	root *twigsyntax.Node,
	offset uint32,
) (*twigsyntax.Node, []VueObjectBindingField, bool) {
	if root == nil {
		return nil, nil, false
	}
	for startTag := range twigquery.IterateNodes(root, twigsyntax.HtmlStartingTag) {
		if offset < startTag.Range().Start || offset > startTag.Range().End {
			continue
		}
		tag, ok := twigast.CastHtmlStartingTag(startTag)
		if !ok {
			continue
		}
		for attribute := range tag.Attributes() {
			if twigquery.HTMLAttributeName(attribute.Syntax()) != "v-bind" {
				continue
			}
			value, ok := attribute.Value()
			if !ok {
				continue
			}
			inner, ok := value.GetInner()
			if !ok {
				continue
			}
			text := inner.Syntax().Text()
			base := inner.Syntax().Range().Start
			if offset < base || offset > base+uint32(len(text)) {
				continue
			}
			if !vueObjectBindingKeyPosition(text, int(offset-base)) {
				continue
			}
			fields, _ := VueObjectBindingFields(text, base)
			return startTag, fields, true
		}
	}
	return nil, nil, false
}

func vueObjectBindingKeyPosition(value string, cursor int) bool {
	open := skipObjectBindingSpace(value, 0)
	if open >= len(value) || value[open] != '{' || cursor <= open {
		return false
	}
	close := matchingSlotDelimiter(value, open, '{', '}')
	end := len(value)
	if close >= 0 {
		end = close
		if cursor > close {
			return false
		}
	}
	if cursor > end {
		return false
	}
	segments := vueObjectBindingSegments(value, open+1, end)
	for _, segment := range segments {
		if cursor < segment.start || cursor > segment.end {
			continue
		}
		start := skipObjectBindingSpace(value, segment.start)
		if start < segment.end && strings.HasPrefix(value[start:segment.end], "...") {
			return false
		}
		colon := objectBindingTopLevelColon(value, start, segment.end)
		return colon < 0 || cursor <= colon
	}
	return false
}
