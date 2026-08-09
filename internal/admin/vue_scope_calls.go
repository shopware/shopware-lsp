package admin

import (
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

func TwigVueCallAtOffset(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
) (TwigVueCall, bool) {
	if root == nil || offset > uint32(len(content)) {
		return TwigVueCall{}, false
	}
	nodeOffset := offset
	if nodeOffset == uint32(len(content)) && nodeOffset > 0 {
		nodeOffset--
	}
	node := root.NodeAtOffset(nodeOffset)
	if node == nil || !IsTwigVueExpressionAt(node, offset) {
		return TwigVueCall{}, false
	}
	expressionRange, found := twigVueExpressionRange(node)
	if !found || offset < expressionRange.Start || offset > expressionRange.End {
		return TwigVueCall{}, false
	}
	limit := int(offset)
	if limit > int(expressionRange.End) {
		limit = int(expressionRange.End)
	}
	scanner := twigVueCallScanner{
		content:         content,
		expressionStart: int(expressionRange.Start),
		limit:           limit,
		stack:           make([]twigVueCallFrame, 0, 8),
	}
	return scanner.activeCall()
}

type twigVueCallFrame struct {
	delimiter byte
	call      TwigVueCall
	callable  bool
}

type twigVueCallScanner struct {
	content         []byte
	expressionStart int
	limit           int
	stack           []twigVueCallFrame
	quote           byte
	escaped         bool
	lineComment     bool
	blockComment    bool
	templates       []int
}

func (s *twigVueCallScanner) activeCall() (TwigVueCall, bool) {
	for index := s.expressionStart; index < s.limit; index++ {
		index = s.consume(index)
	}
	for index := len(s.stack) - 1; index >= 0; index-- {
		if s.stack[index].callable {
			return s.stack[index].call, true
		}
	}
	return TwigVueCall{}, false
}

func (s *twigVueCallScanner) consume(index int) int {
	current := s.content[index]
	if s.lineComment {
		if current == '\n' {
			s.lineComment = false
		}
		return index
	}
	if s.blockComment {
		return s.consumeBlockComment(index, current)
	}
	if s.quote != 0 {
		s.consumeQuote(current)
		return index
	}
	if s.inTemplateText() {
		return s.consumeTemplateText(index, current)
	}
	if consumed, next := s.consumeCommentStart(index, current); consumed {
		return next
	}
	if current == '\'' || current == '"' {
		s.quote = current
		return index
	}
	if current == '`' {
		s.templates = append(s.templates, 0)
		return index
	}
	if len(s.templates) > 0 && current == '{' {
		s.templates[len(s.templates)-1]++
	}
	if s.consumeDelimiter(index, current) {
		return index
	}
	if len(s.templates) > 0 && current == '}' {
		s.templates[len(s.templates)-1]--
	}
	return index
}

func (s *twigVueCallScanner) consumeBlockComment(index int, current byte) int {
	if current == '*' && index+1 < s.limit && s.content[index+1] == '/' {
		s.blockComment = false
		return index + 1
	}
	return index
}

func (s *twigVueCallScanner) consumeQuote(current byte) {
	if s.escaped {
		s.escaped = false
		return
	}
	if current == '\\' {
		s.escaped = true
		return
	}
	if current == s.quote {
		s.quote = 0
	}
}

func (s *twigVueCallScanner) inTemplateText() bool {
	return len(s.templates) > 0 && s.templates[len(s.templates)-1] == 0
}

func (s *twigVueCallScanner) consumeTemplateText(index int, current byte) int {
	if current == '\\' {
		return index + 1
	}
	if current == '`' {
		s.templates = s.templates[:len(s.templates)-1]
		return index
	}
	if current == '$' && index+1 < s.limit && s.content[index+1] == '{' {
		s.templates[len(s.templates)-1] = 1
		s.stack = append(s.stack, twigVueCallFrame{delimiter: '{'})
		return index + 1
	}
	return index
}

func (s *twigVueCallScanner) consumeCommentStart(
	index int,
	current byte,
) (bool, int) {
	if current != '/' || index+1 >= s.limit {
		return false, index
	}
	switch s.content[index+1] {
	case '/':
		s.lineComment = true
		return true, index + 1
	case '*':
		s.blockComment = true
		return true, index + 1
	default:
		return false, index
	}
}

func (s *twigVueCallScanner) consumeDelimiter(index int, current byte) bool {
	switch current {
	case '(', '[', '{':
		entry := twigVueCallFrame{delimiter: current}
		if current == '(' {
			entry.call, entry.callable = twigVueCallBefore(
				s.content,
				s.expressionStart,
				index,
			)
		}
		s.stack = append(s.stack, entry)
	case ')', ']', '}':
		if len(s.stack) == 0 || !matchingVueCallDelimiter(
			s.stack[len(s.stack)-1].delimiter,
			current,
		) {
			return true
		}
		s.stack = s.stack[:len(s.stack)-1]
	case ',':
		if len(s.stack) > 0 && s.stack[len(s.stack)-1].delimiter == '(' &&
			s.stack[len(s.stack)-1].callable {
			s.stack[len(s.stack)-1].call.ActiveArgument++
		}
	}
	return false
}

// TwigVueCalls returns complete calls in evaluated Administration template
// expressions in source order. It shares the lexical recognizer used by
// TwigVueCallAtOffset, so grouping parentheses and call-like text inside
// strings or comments are not reported as calls.
func TwigVueCalls(
	root *twigsyntax.Node,
	content []byte,
) []TwigVueCallSite {
	if root == nil || len(content) == 0 {
		return nil
	}
	var result []TwigVueCallSite
	seen := make(map[cst.TextRange]bool)
	for open := 0; open < len(content); open++ {
		if content[open] != '(' {
			continue
		}
		node := root.NodeAtOffset(uint32(open))
		if node == nil || !IsTwigVueExpressionAt(node, uint32(open)) {
			continue
		}
		expressionRange, found := twigVueExpressionRange(node)
		if !found || uint32(open) < expressionRange.Start ||
			uint32(open) >= expressionRange.End {
			continue
		}
		candidate, callable := twigVueCallBefore(
			content, int(expressionRange.Start), open,
		)
		if !callable || seen[candidate.NameRange] {
			continue
		}
		active, activeFound := TwigVueCallAtOffset(
			root, content, uint32(open+1),
		)
		if !activeFound || active.NameRange != candidate.NameRange ||
			active.Filter != candidate.Filter {
			continue
		}
		arguments, end := twigVueCallArgumentRanges(
			content, open, int(expressionRange.End),
		)
		seen[candidate.NameRange] = true
		result = append(result, TwigVueCallSite{
			TwigVueCall: candidate,
			Range: cst.TextRange{
				Start: candidate.NameRange.Start,
				End:   uint32(end),
			},
			OpenParen: uint32(open),
			Arguments: arguments,
		})
	}
	return result
}

func twigVueCallArgumentRanges(
	content []byte,
	open,
	expressionEnd int,
) ([]cst.TextRange, int) {
	if open < 0 || open >= len(content) || content[open] != '(' {
		return nil, open
	}
	if expressionEnd > len(content) {
		expressionEnd = len(content)
	}
	if expressionEnd <= open {
		return nil, open + 1
	}
	stack := []byte{'('}
	argumentStart := open + 1
	var result []cst.TextRange
	var quote byte
	escaped := false
	lineComment := false
	blockComment := false
	appendArgument := func(end int) {
		start := argumentStart
		for start < end && isSlotSpace(content[start]) {
			start++
		}
		for end > start && isSlotSpace(content[end-1]) {
			end--
		}
		if start < end {
			result = append(result, cst.TextRange{
				Start: uint32(start), End: uint32(end),
			})
		}
	}
	for index := open + 1; index < expressionEnd; index++ {
		current := content[index]
		if lineComment {
			if current == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if current == '*' && index+1 < expressionEnd &&
				content[index+1] == '/' {
				blockComment = false
				index++
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '/' && index+1 < expressionEnd {
			switch content[index+1] {
			case '/':
				lineComment = true
				index++
				continue
			case '*':
				blockComment = true
				index++
				continue
			}
		}
		if current == '\'' || current == '"' || current == '`' {
			quote = current
			continue
		}
		switch current {
		case '(', '[', '{':
			stack = append(stack, current)
		case ')', ']', '}':
			if len(stack) == 0 || !matchingVueCallDelimiter(
				stack[len(stack)-1], current,
			) {
				continue
			}
			if len(stack) == 1 {
				appendArgument(index)
				return result, index + 1
			}
			stack = stack[:len(stack)-1]
		case ',':
			if len(stack) == 1 {
				appendArgument(index)
				argumentStart = index + 1
			}
		}
	}
	appendArgument(expressionEnd)
	return result, expressionEnd
}

func twigVueCallBefore(
	content []byte,
	expressionStart,
	open int,
) (TwigVueCall, bool) {
	cursor := open
	for cursor > expressionStart && isSlotSpace(content[cursor-1]) {
		cursor--
	}
	if cursor >= expressionStart+2 && content[cursor-2] == '?' &&
		content[cursor-1] == '.' {
		cursor -= 2
		for cursor > expressionStart && isSlotSpace(content[cursor-1]) {
			cursor--
		}
	}
	end := cursor
	for cursor > expressionStart && isSlotIdentifierContinue(content[cursor-1]) {
		cursor--
	}
	if cursor == end || !isSlotIdentifierStart(content[cursor]) {
		return TwigVueCall{}, false
	}
	previous := cursor
	for previous > expressionStart && isSlotSpace(content[previous-1]) {
		previous--
	}
	filter := previous > expressionStart && content[previous-1] == '|'
	if filter && previous >= expressionStart+2 && content[previous-2] == '|' {
		filter = false
	}
	return TwigVueCall{
		Name: string(content[cursor:end]),
		NameRange: cst.TextRange{
			Start: uint32(cursor), End: uint32(end),
		},
		Filter: filter,
	}, true
}

func matchingVueCallDelimiter(open, close byte) bool {
	return open == '(' && close == ')' || open == '[' && close == ']' ||
		open == '{' && close == '}'
}

func twigVueExpressionRange(
	node *twigsyntax.Node,
) (cst.TextRange, bool) {
	if attributeNode := twigquery.HTMLAttributeAt(node); attributeNode != nil {
		attribute, ok := twigast.CastHtmlAttribute(attributeNode)
		if !ok {
			return cst.TextRange{}, false
		}
		value, ok := attribute.Value()
		if !ok {
			return cst.TextRange{}, false
		}
		inner, ok := value.GetInner()
		if !ok {
			return cst.TextRange{}, false
		}
		return inner.Syntax().RangeTrimmedTrivia(), true
	}
	if variable := twigquery.ClosestNodeOfKind(node, twigsyntax.TwigVar); variable != nil {
		return variable.RangeTrimmedTrivia(), true
	}
	return cst.TextRange{}, false
}

func vueExpressionPositionInLiteralOrComment(prefix []byte) bool {
	var quote byte
	escaped := false
	lineComment := false
	blockComment := false
	// Each entry represents one template literal. Zero is template text;
	// positive values are the brace depth of its current ${...} expression.
	var templates []int
	for index := 0; index < len(prefix); index++ {
		current := prefix[index]
		if lineComment {
			if current == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if current == '*' && index+1 < len(prefix) && prefix[index+1] == '/' {
				blockComment = false
				index++
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		if len(templates) > 0 && templates[len(templates)-1] == 0 {
			if current == '\\' {
				index++
				continue
			}
			if current == '`' {
				templates = templates[:len(templates)-1]
				continue
			}
			if current == '$' && index+1 < len(prefix) &&
				prefix[index+1] == '{' {
				templates[len(templates)-1] = 1
				index++
			}
			continue
		}
		if current == '/' && index+1 < len(prefix) {
			switch prefix[index+1] {
			case '/':
				lineComment = true
				index++
				continue
			case '*':
				blockComment = true
				index++
				continue
			}
		}
		if current == '\'' || current == '"' {
			quote = current
			continue
		}
		if current == '`' {
			templates = append(templates, 0)
			continue
		}
		if len(templates) > 0 {
			top := len(templates) - 1
			switch current {
			case '{':
				templates[top]++
			case '}':
				templates[top]--
			}
		}
	}
	return quote != 0 || lineComment || blockComment ||
		len(templates) > 0 && templates[len(templates)-1] == 0
}
