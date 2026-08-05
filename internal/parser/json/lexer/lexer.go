package lexer

import (
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/parser/json/syntax"
	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
)

type Token = parsekit.Token

func Lex(source string) []Token {
	return LexInto(source, nil)
}

func LexInto(source string, tokens []Token) []Token {
	capacity := len(source)/3 + 1
	if cap(tokens) < capacity {
		tokens = make([]Token, 0, capacity)
	} else {
		tokens = tokens[:0]
	}
	sourceRef := &source
	for position := 0; position < len(source); {
		kind, length := next(source, position)
		end := position + length
		tokens = append(tokens, parsekit.NewToken(
			kind,
			sourceRef,
			cst.TextRange{
				Start: uint32(position),
				End:   uint32(end),
			},
		))
		position = end
	}
	return tokens
}

func next(source string, position int) (syntax.Kind, int) {
	switch source[position] {
	case ' ', '\t':
		end := position + 1
		for end < len(source) && (source[end] == ' ' || source[end] == '\t') {
			end++
		}
		return syntax.TkWhitespace, end - position
	case '\r', '\n':
		end := position + 1
		for end < len(source) && (source[end] == '\r' || source[end] == '\n') {
			end++
		}
		return syntax.TkLineBreak, end - position
	case '{':
		return syntax.TkOpenBrace, 1
	case '}':
		return syntax.TkCloseBrace, 1
	case '[':
		return syntax.TkOpenBracket, 1
	case ']':
		return syntax.TkCloseBracket, 1
	case ':':
		return syntax.TkColon, 1
	case ',':
		return syntax.TkComma, 1
	case '"':
		return syntax.TkString, scanString(source, position)
	case '-':
		if position+1 < len(source) && isDigit(source[position+1]) {
			return syntax.TkNumber, scanNumber(source, position)
		}
		return syntax.TkUnknown, 1
	default:
		if isDigit(source[position]) {
			return syntax.TkNumber, scanNumber(source, position)
		}
		if isLetter(source[position]) {
			end := position + 1
			for end < len(source) && isLetter(source[end]) {
				end++
			}
			switch source[position:end] {
			case "true":
				return syntax.TkTrue, end - position
			case "false":
				return syntax.TkFalse, end - position
			case "null":
				return syntax.TkNull, end - position
			default:
				return syntax.TkUnknown, end - position
			}
		}
		return syntax.TkUnknown, 1
	}
}

func scanString(source string, position int) int {
	for end := position + 1; end < len(source); {
		switch source[end] {
		case '"':
			return end - position + 1
		case '\\':
			if end+1 < len(source) {
				end += 2
			} else {
				end++
			}
		case '\r', '\n':
			return end - position
		default:
			end++
		}
	}
	return len(source) - position
}

func scanNumber(source string, position int) int {
	end := position
	if source[end] == '-' {
		end++
	}

	if source[end] == '0' {
		end++
	} else {
		for end < len(source) && isDigit(source[end]) {
			end++
		}
	}

	if end+1 < len(source) && source[end] == '.' && isDigit(source[end+1]) {
		end += 2
		for end < len(source) && isDigit(source[end]) {
			end++
		}
	}

	if end < len(source) && (source[end] == 'e' || source[end] == 'E') {
		exponent := end + 1
		if exponent < len(source) && (source[exponent] == '+' || source[exponent] == '-') {
			exponent++
		}
		if exponent < len(source) && isDigit(source[exponent]) {
			end = exponent + 1
			for end < len(source) && isDigit(source[end]) {
				end++
			}
		}
	}

	return end - position
}

func isDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

func isLetter(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z')
}
