package admin

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// VueDynamicComponentCandidate is one statically visible component name in a
// Vue <component is="..."> selector. Range covers only the name itself, not
// the JavaScript or HTML quotes surrounding it.
type VueDynamicComponentCandidate struct {
	Name  string
	Range cst.TextRange
}

// VueDynamicComponentSelector describes the selector of a Vue dynamic
// component. Complete is true only when every possible branch is a static
// string; incomplete selectors may still expose useful literal candidates for
// navigation without being strong enough for missing-component diagnostics.
type VueDynamicComponentSelector struct {
	AttributeName   string
	Expression      string
	ExpressionRange cst.TextRange
	Candidates      []VueDynamicComponentCandidate
	Complete        bool
}

// TwigDynamicComponentSelector recognizes static is attributes and bound
// :is/v-bind:is selectors on Vue's built-in <component> element.
func TwigDynamicComponentSelector(
	startTag *twigsyntax.Node,
) (VueDynamicComponentSelector, bool) {
	if startTag == nil || twigquery.HTMLTagName(startTag) != "component" {
		return VueDynamicComponentSelector{}, false
	}
	tag, ok := twigast.CastHtmlStartingTag(startTag)
	if !ok {
		return VueDynamicComponentSelector{}, false
	}
	for attribute := range tag.Attributes() {
		attributeName := twigquery.HTMLAttributeName(attribute.Syntax())
		if attributeName != "is" && attributeName != ":is" &&
			attributeName != "v-bind:is" {
			continue
		}
		value, valueOK := attribute.Value()
		if !valueOK {
			return VueDynamicComponentSelector{
				AttributeName: attributeName,
			}, true
		}
		inner, innerOK := value.GetInner()
		if !innerOK {
			return VueDynamicComponentSelector{
				AttributeName: attributeName,
			}, true
		}
		rangeValue := inner.Syntax().RangeTrimmedTrivia()
		expression := strings.TrimSpace(inner.Syntax().Text())
		selector := VueDynamicComponentSelector{
			AttributeName:   attributeName,
			Expression:      expression,
			ExpressionRange: rangeValue,
		}
		if expression == "" {
			return selector, true
		}
		if attributeName == "is" {
			selector.Candidates = []VueDynamicComponentCandidate{{
				Name: expression, Range: rangeValue,
			}}
			selector.Complete = true
			return selector, true
		}
		selector.Candidates, selector.Complete =
			staticVueDynamicComponentCandidates(expression, rangeValue.Start)
		return selector, true
	}
	return VueDynamicComponentSelector{}, false
}

// StaticComponentNameForTag resolves a normal component tag or a dynamic
// component selector whose complete candidate set contains one name.
func StaticComponentNameForTag(startTag *twigsyntax.Node) (string, bool) {
	name := twigquery.HTMLTagName(startTag)
	if IsComponentTag(name) {
		return name, true
	}
	selector, found := TwigDynamicComponentSelector(startTag)
	if !found || !selector.Complete {
		return "", false
	}
	names := selector.Names()
	if len(names) != 1 || !IsComponentTag(names[0]) {
		return "", false
	}
	return names[0], true
}

// CandidateAt returns the static selector candidate under offset.
func (selector VueDynamicComponentSelector) CandidateAt(
	offset uint32,
) (VueDynamicComponentCandidate, bool) {
	for _, candidate := range selector.Candidates {
		if offset >= candidate.Range.Start && offset < candidate.Range.End {
			return candidate, true
		}
	}
	return VueDynamicComponentCandidate{}, false
}

// Names returns selector candidates once each in source order.
func (selector VueDynamicComponentSelector) Names() []string {
	seen := make(map[string]bool)
	var result []string
	for _, candidate := range selector.Candidates {
		if candidate.Name == "" || seen[candidate.Name] {
			continue
		}
		seen[candidate.Name] = true
		result = append(result, candidate.Name)
	}
	return result
}

func staticVueDynamicComponentCandidates(
	expression string,
	base uint32,
) ([]VueDynamicComponentCandidate, bool) {
	expression, base = trimDynamicComponentExpression(expression, base)
	if expression == "" {
		return nil, false
	}
	for len(expression) >= 2 && expression[0] == '(' &&
		matchingSlotDelimiter(expression, 0, '(', ')') == len(expression)-1 {
		expression, base = trimDynamicComponentExpression(
			expression[1:len(expression)-1], base+1,
		)
	}
	if name, start, end, literal := staticDynamicComponentString(expression); literal {
		return []VueDynamicComponentCandidate{{
			Name: name,
			Range: cst.TextRange{
				Start: base + uint32(start),
				End:   base + uint32(end),
			},
		}}, true
	}
	trueStart, trueEnd, falseStart, falseEnd, conditional :=
		splitDynamicComponentConditional(expression)
	if !conditional {
		return nil, false
	}
	whenTrue, trueComplete := staticVueDynamicComponentCandidates(
		expression[trueStart:trueEnd], base+uint32(trueStart),
	)
	whenFalse, falseComplete := staticVueDynamicComponentCandidates(
		expression[falseStart:falseEnd], base+uint32(falseStart),
	)
	return append(whenTrue, whenFalse...), trueComplete && falseComplete
}

func trimDynamicComponentExpression(
	value string,
	base uint32,
) (string, uint32) {
	left := len(value) - len(strings.TrimLeft(value, " \t\r\n"))
	value = strings.TrimSpace(value)
	return value, base + uint32(left)
}

func staticDynamicComponentString(
	value string,
) (name string, start, end int, found bool) {
	if len(value) < 2 || !strings.ContainsRune("'\"`", rune(value[0])) ||
		value[len(value)-1] != value[0] {
		return "", 0, 0, false
	}
	quote := value[0]
	escaped := false
	for index := 1; index < len(value)-1; index++ {
		if escaped {
			escaped = false
			continue
		}
		if value[index] == '\\' {
			escaped = true
			continue
		}
		if value[index] == quote || quote == '`' && value[index] == '$' &&
			index+1 < len(value)-1 && value[index+1] == '{' {
			return "", 0, 0, false
		}
	}
	name = value[1 : len(value)-1]
	if name == "" || strings.ContainsRune(name, '\\') {
		return "", 0, 0, false
	}
	return name, 1, len(value) - 1, true
}

func splitDynamicComponentConditional(
	value string,
) (trueStart, trueEnd, falseStart, falseEnd int, found bool) {
	state := slotScanState{}
	question := -1
	nested := 0
	for index := 0; index < len(value); index++ {
		if state.topLevel() {
			switch value[index] {
			case '?':
				if index+1 < len(value) && (value[index+1] == '?' ||
					value[index+1] == '.') || index > 0 && value[index-1] == '?' {
					break
				}
				if question < 0 {
					question = index
				} else {
					nested++
				}
			case ':':
				if question < 0 {
					break
				}
				if nested > 0 {
					nested--
					break
				}
				return question + 1, index, index + 1, len(value), true
			}
		}
		state.consume(value[index])
	}
	return 0, 0, 0, 0, false
}
