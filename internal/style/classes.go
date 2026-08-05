package style

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	scsssyntax "github.com/shopware/shopware-lsp/internal/parser/scss/syntax"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// ClassDeclarations returns statically named class selectors from SCSS trees.
// Vue SFC trees are supported because their style section embeds SCSS nodes.
func ClassDeclarations(path string, root *cst.Node) []ClassOccurrence {
	extension := strings.ToLower(filepath.Ext(path))
	if root == nil || extension != ".scss" && extension != ".vue" {
		return nil
	}
	var result []ClassOccurrence
	walkSCSSRules(path, root, nil, &result)
	sortOccurrences(result)
	return result
}

func walkSCSSRules(
	path string,
	node *cst.Node,
	parentClasses []string,
	result *[]ClassOccurrence,
) {
	for child := range node.ChildNodes() {
		if child.Kind() != scsssyntax.ScssRule {
			walkSCSSRules(path, child, parentClasses, result)
			continue
		}
		declarations, contexts, block := classesInRule(
			path,
			child,
			parentClasses,
		)
		*result = append(*result, declarations...)
		if block != nil {
			walkSCSSRules(path, block, contexts, result)
		}
	}
}

func classesInRule(
	path string,
	rule *cst.Node,
	parentClasses []string,
) ([]ClassOccurrence, []string, *cst.Node) {
	var block *cst.Node
	for child := range rule.ChildNodes() {
		if child.Kind() == scsssyntax.ScssBlock {
			block = child
			break
		}
	}
	if block == nil {
		return nil, nil, nil
	}

	var tokens []*cst.Token
	for element := range rule.Descendants() {
		token, ok := element.(*cst.Token)
		if !ok || token.Range().Start >= block.Range().Start ||
			token.Kind().IsTrivia() {
			continue
		}
		tokens = append(tokens, token)
	}

	var declarations []ClassOccurrence
	var contexts []string
	segmentStart := 0
	parenDepth := 0
	bracketDepth := 0
	interpolationDepth := 0
	for position, token := range tokens {
		switch token.Kind() {
		case scsssyntax.TkOpenParen:
			parenDepth++
		case scsssyntax.TkCloseParen:
			parenDepth = max(0, parenDepth-1)
		case scsssyntax.TkOpenBracket:
			bracketDepth++
		case scsssyntax.TkCloseBracket:
			bracketDepth = max(0, bracketDepth-1)
		case scsssyntax.TkInterpolationOpen:
			interpolationDepth++
		case scsssyntax.TkCloseBrace:
			interpolationDepth = max(0, interpolationDepth-1)
		case scsssyntax.TkComma:
			if parenDepth == 0 && bracketDepth == 0 &&
				interpolationDepth == 0 {
				current, context := classesInSelectorSegment(
					path,
					tokens[segmentStart:position],
					parentClasses,
				)
				declarations = append(declarations, current...)
				contexts = append(contexts, context...)
				segmentStart = position + 1
			}
		}
	}
	current, context := classesInSelectorSegment(
		path,
		tokens[segmentStart:],
		parentClasses,
	)
	declarations = append(declarations, current...)
	contexts = append(contexts, context...)
	return uniqueOccurrences(declarations), uniqueStrings(contexts), block
}

func classesInSelectorSegment(
	path string,
	tokens []*cst.Token,
	parentClasses []string,
) ([]ClassOccurrence, []string) {
	var declarations []ClassOccurrence
	var directContexts []string
	var expandedContexts []string
	usesParent := false
	for position := 0; position < len(tokens); position++ {
		token := tokens[position]
		if token.Text() == "." && position+1 < len(tokens) {
			identifier := tokens[position+1]
			if identifier.Kind() != scsssyntax.TkIdentifier ||
				token.Range().End != identifier.Range().Start ||
				hasAdjacentInterpolation(tokens, position+1) {
				continue
			}
			name := decodeCSSIdentifier(identifier.Text())
			if name == "" {
				continue
			}
			declarations = append(declarations, ClassOccurrence{
				Name: name, File: path, Range: identifier.Range(),
				Kind: ClassDeclaration,
			})
			directContexts = []string{name}
			position++
			continue
		}
		if token.Text() != "&" {
			continue
		}
		usesParent = true
		if position+1 >= len(tokens) {
			continue
		}
		suffix := tokens[position+1]
		if suffix.Kind() != scsssyntax.TkIdentifier ||
			token.Range().End != suffix.Range().Start ||
			hasAdjacentInterpolation(tokens, position+1) {
			continue
		}
		decodedSuffix := decodeCSSIdentifier(suffix.Text())
		if decodedSuffix == "" {
			continue
		}
		for _, parent := range parentClasses {
			name := parent + decodedSuffix
			expandedContexts = append(expandedContexts, name)
			declarations = append(declarations, ClassOccurrence{
				Name: name, File: path,
				Range: cst.TextRange{
					Start: token.Range().Start,
					End:   suffix.Range().End,
				},
				Kind: ClassDeclaration,
			})
		}
		position++
	}
	if len(directContexts) != 0 {
		return declarations, directContexts
	}
	if len(expandedContexts) != 0 {
		return declarations, expandedContexts
	}
	if usesParent {
		return declarations, append([]string(nil), parentClasses...)
	}
	return declarations, nil
}

func hasAdjacentInterpolation(tokens []*cst.Token, identifierPosition int) bool {
	return identifierPosition+1 < len(tokens) &&
		tokens[identifierPosition].Range().End ==
			tokens[identifierPosition+1].Range().Start &&
		tokens[identifierPosition+1].Kind() == scsssyntax.TkInterpolationOpen
}

// ClassUsages returns static class attribute values from Twig-compatible
// markup. Vue SFC template sections embed the same Twig CST nodes.
func ClassUsages(path string, root *cst.Node) []ClassOccurrence {
	extension := strings.ToLower(filepath.Ext(path))
	if root == nil || extension != ".twig" && extension != ".html" &&
		extension != ".vue" {
		return nil
	}
	var result []ClassOccurrence
	for element := range root.Descendants() {
		attributeNode, ok := element.(*cst.Node)
		if !ok || attributeNode.Kind() != twigsyntax.HtmlAttribute ||
			!strings.EqualFold(
				twigquery.HTMLAttributeName(attributeNode),
				"class",
			) {
			continue
		}
		attribute, cast := twigast.CastHtmlAttribute(attributeNode)
		if !cast {
			continue
		}
		value, found := attribute.Value()
		if !found {
			continue
		}
		inner, found := value.GetInner()
		if !found {
			continue
		}
		result = append(
			result,
			staticClassSegments(path, inner.Syntax())...,
		)
	}
	sortOccurrences(result)
	return uniqueOccurrences(result)
}

func staticClassSegments(path string, inner *cst.Node) []ClassOccurrence {
	var result []ClassOccurrence
	var text strings.Builder
	var start uint32
	var end uint32
	segmentOpen := false
	leadingDynamic := false
	previousDynamicEnd := uint32(0)
	previousDynamicTouchesRight := false

	flush := func(trailingDynamic bool) {
		if !segmentOpen {
			return
		}
		result = append(result, classWords(
			path,
			text.String(),
			start,
			leadingDynamic,
			trailingDynamic,
		)...)
		text.Reset()
		segmentOpen = false
		leadingDynamic = false
	}

	for child := range inner.ChildElements() {
		switch current := child.(type) {
		case *cst.Token:
			if !segmentOpen {
				start = current.Range().Start
				leadingDynamic = previousDynamicEnd == start &&
					previousDynamicTouchesRight
				segmentOpen = true
			}
			text.WriteString(current.Text())
			end = current.Range().End
		case *cst.Node:
			dynamicText := current.Text()
			touchesLeft := dynamicText != "" &&
				!isClassSpace(dynamicText[0])
			flush(segmentOpen && end == current.Range().Start && touchesLeft)
			previousDynamicEnd = current.Range().End
			previousDynamicTouchesRight = dynamicText != "" &&
				!isClassSpace(dynamicText[len(dynamicText)-1])
		}
	}
	flush(false)
	return result
}

func classWords(
	path,
	value string,
	base uint32,
	leadingDynamic,
	trailingDynamic bool,
) []ClassOccurrence {
	var result []ClassOccurrence
	for position := 0; position < len(value); {
		for position < len(value) && isClassSpace(value[position]) {
			position++
		}
		start := position
		for position < len(value) && !isClassSpace(value[position]) {
			position++
		}
		if start == position ||
			leadingDynamic && start == 0 ||
			trailingDynamic && position == len(value) {
			continue
		}
		name := value[start:position]
		if strings.ContainsAny(name, "{}") {
			continue
		}
		result = append(result, ClassOccurrence{
			Name: name, File: path,
			Range: cst.TextRange{
				Start: base + uint32(start),
				End:   base + uint32(position),
			},
			Kind: ClassUsage,
		})
	}
	return result
}

func ClassAt(
	path string,
	root *cst.Node,
	offset uint32,
) (ClassOccurrence, bool) {
	occurrences := append(
		ClassDeclarations(path, root),
		ClassUsages(path, root)...,
	)
	for _, occurrence := range occurrences {
		if occurrence.Range.Contains(offset) ||
			offset == occurrence.Range.End {
			return occurrence, true
		}
	}
	return ClassOccurrence{}, false
}

func isClassSpace(value byte) bool {
	switch value {
	case ' ', '\t', '\r', '\n', '\f':
		return true
	default:
		return false
	}
}

func decodeCSSIdentifier(value string) string {
	if !strings.Contains(value, "\\") {
		return value
	}
	var result strings.Builder
	for position := 0; position < len(value); {
		if value[position] != '\\' {
			result.WriteByte(value[position])
			position++
			continue
		}
		position++
		if position >= len(value) {
			break
		}
		start := position
		codepoint := rune(0)
		for position < len(value) && position-start < 6 {
			digit, ok := hexDigit(value[position])
			if !ok {
				break
			}
			codepoint = codepoint*16 + rune(digit)
			position++
		}
		if position != start {
			if position < len(value) && isClassSpace(value[position]) {
				position++
			}
			if codepoint == 0 || !utf8.ValidRune(codepoint) {
				codepoint = utf8.RuneError
			}
			result.WriteRune(codepoint)
			continue
		}
		result.WriteByte(value[position])
		position++
	}
	return result.String()
}

func hexDigit(value byte) (uint8, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}

func uniqueOccurrences(values []ClassOccurrence) []ClassOccurrence {
	seen := make(map[string]struct{}, len(values))
	result := make([]ClassOccurrence, 0, len(values))
	for _, value := range values {
		key := value.Name + "\x00" + value.Range.String()
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func sortOccurrences(values []ClassOccurrence) {
	sort.Slice(values, func(left, right int) bool {
		if values[left].Range.Start != values[right].Range.Start {
			return values[left].Range.Start < values[right].Range.Start
		}
		if values[left].Range.End != values[right].Range.End {
			return values[left].Range.End < values[right].Range.End
		}
		return values[left].Name < values[right].Name
	})
}
