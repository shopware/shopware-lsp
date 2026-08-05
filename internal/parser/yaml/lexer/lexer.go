package lexer

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
	"github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
)

type Token = parsekit.Token

type scanner struct {
	source           string
	position         int
	lineStart        int
	currentIndent    int
	flowDepth        int
	lineContentStart bool
}

func Lex(source string) []Token {
	return LexInto(source, nil)
}

func LexInto(source string, tokens []Token) []Token {
	state := scanner{
		source:           source,
		lineContentStart: true,
	}
	capacity := len(source)/4 + 1
	if cap(tokens) < capacity {
		tokens = make([]Token, 0, capacity)
	} else {
		tokens = tokens[:0]
	}
	sourceRef := &source
	for state.position < len(source) {
		start := state.position
		kind, end := state.next()
		if end <= start {
			panic("yaml lexer did not make progress")
		}
		tokens = append(tokens, parsekit.NewToken(
			kind,
			sourceRef,
			cst.TextRange{
				Start: uint32(start),
				End:   uint32(end),
			},
		))
		state.position = end
	}
	return tokens
}

func (s *scanner) next() (syntax.Kind, int) {
	if s.position == s.lineStart {
		s.currentIndent = 0
		s.lineContentStart = true
		end := s.position
		for end < len(s.source) && (s.source[end] == ' ' || s.source[end] == '\t') {
			if s.source[end] == '\t' {
				s.currentIndent += 2
			} else {
				s.currentIndent++
			}
			end++
		}
		if end > s.position {
			return syntax.TkIndent, end
		}
	}

	value := s.source[s.position]
	switch value {
	case '\r', '\n':
		end := scanLineBreak(s.source, s.position)
		s.lineStart = end
		s.currentIndent = 0
		s.lineContentStart = true
		return syntax.TkLineBreak, end
	case ' ', '\t':
		end := s.position + 1
		for end < len(s.source) && (s.source[end] == ' ' || s.source[end] == '\t') {
			end++
		}
		return syntax.TkWhitespace, end
	case '#':
		end := lineEnd(s.source, s.position)
		return syntax.TkComment, end
	case '%':
		if s.lineContentStart && s.flowDepth == 0 {
			s.lineContentStart = false
			return syntax.TkDirective, lineEnd(s.source, s.position)
		}
	case '-':
		if s.lineContentStart && hasMarker(s.source, s.position, "---") {
			s.lineContentStart = false
			return syntax.TkDocumentStart, s.position + 3
		}
		if s.lineContentStart && followedBySeparation(s.source, s.position+1) {
			s.lineContentStart = false
			return syntax.TkDash, s.position + 1
		}
	case '.':
		if s.lineContentStart && hasMarker(s.source, s.position, "...") {
			s.lineContentStart = false
			return syntax.TkDocumentEnd, s.position + 3
		}
	case ':':
		if colonIsIndicator(s.source, s.position, s.flowDepth) {
			s.lineContentStart = false
			return syntax.TkColon, s.position + 1
		}
	case ',':
		if s.flowDepth > 0 {
			s.lineContentStart = false
			return syntax.TkComma, s.position + 1
		}
	case '[':
		s.flowDepth++
		s.lineContentStart = false
		return syntax.TkOpenBracket, s.position + 1
	case ']':
		if s.flowDepth > 0 {
			s.flowDepth--
		}
		s.lineContentStart = false
		return syntax.TkCloseBracket, s.position + 1
	case '{':
		s.flowDepth++
		s.lineContentStart = false
		return syntax.TkOpenBrace, s.position + 1
	case '}':
		if s.flowDepth > 0 {
			s.flowDepth--
		}
		s.lineContentStart = false
		return syntax.TkCloseBrace, s.position + 1
	case '\'':
		s.lineContentStart = false
		return syntax.TkSingleQuotedScalar, scanSingleQuoted(s.source, s.position)
	case '"':
		s.lineContentStart = false
		return syntax.TkDoubleQuotedScalar, scanDoubleQuoted(s.source, s.position)
	case '|', '>':
		if s.flowDepth == 0 && isBlockScalarHeader(s.source, s.position) {
			end := scanBlockScalar(s.source, s.position, s.currentIndent)
			if newline := strings.LastIndexByte(s.source[s.position:end], '\n'); newline >= 0 {
				s.lineStart = s.position + newline + 1
				if end == s.lineStart {
					s.lineContentStart = true
					s.currentIndent = 0
				}
			}
			return syntax.TkBlockScalar, end
		}
	}

	end := scanPlain(s.source, s.position, s.flowDepth)
	if end == s.position {
		s.lineContentStart = false
		return syntax.TkUnknown, s.position + 1
	}
	s.lineContentStart = false
	return syntax.TkPlainScalar, end
}

func scanLineBreak(source string, position int) int {
	if source[position] == '\r' && position+1 < len(source) && source[position+1] == '\n' {
		return position + 2
	}
	return position + 1
}

func lineEnd(source string, position int) int {
	end := position
	for end < len(source) && source[end] != '\r' && source[end] != '\n' {
		end++
	}
	return end
}

func hasMarker(source string, position int, marker string) bool {
	if !strings.HasPrefix(source[position:], marker) {
		return false
	}
	return followedBySeparation(source, position+len(marker))
}

func followedBySeparation(source string, position int) bool {
	if position >= len(source) {
		return true
	}
	switch source[position] {
	case ' ', '\t', '\r', '\n', '#', ',', ']', '}':
		return true
	default:
		return false
	}
}

func colonIsIndicator(source string, position, flowDepth int) bool {
	if position+1 >= len(source) {
		return true
	}
	switch source[position+1] {
	case ' ', '\t', '\r', '\n', '#':
		return true
	case ',', ']', '}':
		return flowDepth > 0
	default:
		return false
	}
}

func scanSingleQuoted(source string, position int) int {
	for end := position + 1; end < len(source); end++ {
		if source[end] != '\'' {
			continue
		}
		if end+1 < len(source) && source[end+1] == '\'' {
			end++
			continue
		}
		return end + 1
	}
	return len(source)
}

func scanDoubleQuoted(source string, position int) int {
	escaped := false
	for end := position + 1; end < len(source); end++ {
		if escaped {
			escaped = false
			continue
		}
		switch source[end] {
		case '\\':
			escaped = true
		case '"':
			return end + 1
		}
	}
	return len(source)
}

func isBlockScalarHeader(source string, position int) bool {
	end := lineEnd(source, position)
	header := strings.TrimSpace(source[position+1 : end])
	if comment := strings.IndexByte(header, '#'); comment >= 0 {
		header = strings.TrimSpace(header[:comment])
	}
	if len(header) > 2 {
		return false
	}
	for _, value := range header {
		if value != '+' && value != '-' && (value < '1' || value > '9') {
			return false
		}
	}
	return true
}

func scanBlockScalar(source string, position, baseIndent int) int {
	headerEnd := lineEnd(source, position)
	if headerEnd == len(source) {
		return headerEnd
	}
	cursor := scanLineBreak(source, headerEnd)

	for cursor < len(source) {
		end := lineEnd(source, cursor)
		indent, content := indentation(source, cursor, end)
		if content == end {
			if end == len(source) {
				return end
			}
			cursor = scanLineBreak(source, end)
			continue
		}
		if indent <= baseIndent {
			return cursor
		}
		if end == len(source) {
			return end
		}
		cursor = scanLineBreak(source, end)
	}
	return cursor
}

func indentation(source string, start, end int) (int, int) {
	width := 0
	position := start
	for position < end {
		switch source[position] {
		case ' ':
			width++
		case '\t':
			width += 2
		default:
			return width, position
		}
		position++
	}
	return width, position
}

func scanPlain(source string, position, flowDepth int) int {
	end := position
	for end < len(source) {
		value := source[end]
		if value == '\r' || value == '\n' {
			break
		}
		if value == ':' && colonIsIndicator(source, end, flowDepth) {
			break
		}
		if value == '#' && (end == position || source[end-1] == ' ' || source[end-1] == '\t') {
			break
		}
		if flowDepth > 0 {
			switch value {
			case ',', '[', ']', '{', '}':
				return end
			}
		}
		end++
	}
	return end
}
