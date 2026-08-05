package lexer

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
	"github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
)

type Token = parsekit.Token

type mode uint8

const (
	dataMode mode = iota
	tagMode
)

// Lex tokenizes XML without discarding any input. It deliberately keeps XML
// declarations, comments, CDATA, doctypes, entities, and malformed fragments
// in the token stream so editor-time parses remain lossless.
func Lex(source string) []Token {
	return LexInto(source, nil)
}

// LexInto tokenizes source into the provided reusable destination.
func LexInto(source string, tokens []Token) []Token {
	capacity := len(source)/4 + 1
	if cap(tokens) < capacity {
		tokens = make([]Token, 0, capacity)
	} else {
		tokens = tokens[:0]
	}
	currentMode := dataMode
	sourceRef := &source

	emit := func(kind syntax.Kind, start, end int) {
		tokens = append(tokens, parsekit.NewToken(
			kind,
			sourceRef,
			cst.TextRange{
				Start: uint32(start),
				End:   uint32(end),
			},
		))
	}

	for position := 0; position < len(source); {
		start := position
		var kind syntax.Kind

		if currentMode == dataMode {
			switch {
			case strings.HasPrefix(source[position:], "<!--"):
				kind = syntax.TkComment
				position = scanUntil(source, position+4, "-->")
			case strings.HasPrefix(source[position:], "<![CDATA["):
				kind = syntax.TkCdata
				position = scanUntil(source, position+9, "]]>")
			case strings.HasPrefix(source[position:], "<?"):
				kind = syntax.TkProcessingInstruction
				position = scanUntil(source, position+2, "?>")
			case hasPrefixFold(source[position:], "<!DOCTYPE"):
				kind = syntax.TkDoctype
				position = scanDoctype(source, position+9)
			case strings.HasPrefix(source[position:], "</"):
				kind = syntax.TkOpenEndTag
				position += 2
				currentMode = tagMode
			case source[position] == '<':
				kind = syntax.TkOpenTag
				position++
				currentMode = tagMode
			case source[position] == '&':
				kind = syntax.TkEntityReference
				position = scanEntity(source, position)
			default:
				kind = syntax.TkText
				position = scanText(source, position)
			}
		} else {
			switch {
			case strings.HasPrefix(source[position:], "/>"):
				kind = syntax.TkCloseEmptyTag
				position += 2
				currentMode = dataMode
			case source[position] == '>':
				kind = syntax.TkCloseTag
				position++
				currentMode = dataMode
			case source[position] == '=':
				kind = syntax.TkEquals
				position++
			case source[position] == '"' || source[position] == '\'':
				kind = syntax.TkAttributeValue
				position = scanQuoted(source, position, source[position])
			case source[position] == '\r' || source[position] == '\n':
				kind = syntax.TkLineBreak
				position++
				if source[start] == '\r' && position < len(source) && source[position] == '\n' {
					position++
				}
			case isHorizontalWhitespace(source[position]):
				kind = syntax.TkWhitespace
				position++
				for position < len(source) && isHorizontalWhitespace(source[position]) {
					position++
				}
			case isNameStart(source[position]):
				kind = syntax.TkName
				position = scanName(source, position)
			case source[position] == '<':
				// A second opening delimiter is malformed, but switching back to
				// data mode lets the parser recover at the next tag.
				kind = syntax.TkOpenTag
				position++
			default:
				kind = syntax.TkUnknown
				position++
			}
		}

		emit(kind, start, position)
	}

	return tokens
}

func scanUntil(source string, position int, closing string) int {
	if offset := strings.Index(source[position:], closing); offset >= 0 {
		return position + offset + len(closing)
	}
	return len(source)
}

func scanDoctype(source string, position int) int {
	brackets := 0
	var quote byte

	for position < len(source) {
		value := source[position]
		if quote != 0 {
			if value == quote {
				quote = 0
			}
			position++
			continue
		}

		switch value {
		case '\'', '"':
			quote = value
		case '[':
			brackets++
		case ']':
			if brackets > 0 {
				brackets--
			}
		case '>':
			position++
			if brackets == 0 {
				return position
			}
			continue
		}
		position++
	}
	return position
}

func scanEntity(source string, position int) int {
	position++
	for position < len(source) && source[position] != ';' &&
		source[position] != '<' && source[position] != '&' &&
		source[position] != '\r' && source[position] != '\n' {
		position++
	}
	if position < len(source) && source[position] == ';' {
		position++
	}
	return position
}

func scanText(source string, position int) int {
	position++
	for position < len(source) && source[position] != '<' && source[position] != '&' {
		position++
	}
	return position
}

func scanQuoted(source string, position int, quote byte) int {
	position++
	for position < len(source) {
		if source[position] == quote {
			return position + 1
		}
		position++
	}
	return position
}

func scanName(source string, position int) int {
	position++
	for position < len(source) && isNameContinue(source[position]) {
		position++
	}
	return position
}

func hasPrefixFold(source, prefix string) bool {
	return len(source) >= len(prefix) && strings.EqualFold(source[:len(prefix)], prefix)
}

func isHorizontalWhitespace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\f'
}

func isNameStart(value byte) bool {
	return value == ':' || value == '_' ||
		value >= 'A' && value <= 'Z' ||
		value >= 'a' && value <= 'z' ||
		value >= 0x80
}

func isNameContinue(value byte) bool {
	return isNameStart(value) ||
		value == '-' || value == '.' ||
		value >= '0' && value <= '9'
}
