package lexer

import (
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
	"github.com/shopware/shopware-lsp/internal/parser/xpath/syntax"
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
	case ' ', '\t', '\f':
		end := position + 1
		for end < len(source) &&
			(source[end] == ' ' || source[end] == '\t' ||
				source[end] == '\f') {
			end++
		}
		return syntax.TkWhitespace, end - position
	case '\r', '\n':
		end := position + 1
		if source[position] == '\r' && end < len(source) &&
			source[end] == '\n' {
			end++
		}
		return syntax.TkLineBreak, end - position
	case '\'', '"':
		return syntax.TkString, scanQuoted(
			source,
			position,
			source[position],
		)
	case '/':
		if position+1 < len(source) && source[position+1] == '/' {
			return syntax.TkDoubleSlash, 2
		}
		return syntax.TkSlash, 1
	case '.':
		if position+1 < len(source) && source[position+1] == '.' {
			return syntax.TkDoubleDot, 2
		}
		if position+1 < len(source) && isDigit(source[position+1]) {
			return syntax.TkNumber, scanNumber(source, position) - position
		}
		return syntax.TkDot, 1
	case ':':
		if position+1 < len(source) && source[position+1] == ':' {
			return syntax.TkDoubleColon, 2
		}
		return syntax.TkColon, 1
	case '!':
		if position+1 < len(source) && source[position+1] == '=' {
			return syntax.TkNotEquals, 2
		}
		return syntax.TkUnknown, 1
	case '<':
		if position+1 < len(source) && source[position+1] == '=' {
			return syntax.TkLessEquals, 2
		}
		return syntax.TkLess, 1
	case '>':
		if position+1 < len(source) && source[position+1] == '=' {
			return syntax.TkGreaterEquals, 2
		}
		return syntax.TkGreater, 1
	case '@':
		return syntax.TkAt, 1
	case '$':
		return syntax.TkDollar, 1
	case '|':
		return syntax.TkPipe, 1
	case '+':
		return syntax.TkPlus, 1
	case '-':
		return syntax.TkMinus, 1
	case '*':
		return syntax.TkStar, 1
	case '=':
		return syntax.TkEquals, 1
	case '(':
		return syntax.TkOpenParen, 1
	case ')':
		return syntax.TkCloseParen, 1
	case '[':
		return syntax.TkOpenBracket, 1
	case ']':
		return syntax.TkCloseBracket, 1
	case ',':
		return syntax.TkComma, 1
	default:
		if isDigit(source[position]) {
			return syntax.TkNumber, scanNumber(source, position) - position
		}
		if isNameStart(source[position]) {
			return syntax.TkName, scanName(source, position) - position
		}
		return syntax.TkUnknown, 1
	}
}

func scanQuoted(source string, position int, quote byte) int {
	for end := position + 1; end < len(source); end++ {
		if source[end] == quote {
			return end - position + 1
		}
	}
	return len(source) - position
}

func scanNumber(source string, position int) int {
	end := position
	if source[end] == '.' {
		end++
	}
	for end < len(source) && isDigit(source[end]) {
		end++
	}
	if end < len(source) && source[end] == '.' {
		end++
		for end < len(source) && isDigit(source[end]) {
			end++
		}
	}
	return end
}

func scanName(source string, position int) int {
	end := position + 1
	for end < len(source) && isNameContinue(source[end]) {
		end++
	}
	return end
}

func isNameStart(value byte) bool {
	return value == '_' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= 0x80
}

func isNameContinue(value byte) bool {
	return isNameStart(value) || isDigit(value) ||
		value == '-' || value == '.'
}

func isDigit(value byte) bool {
	return value >= '0' && value <= '9'
}
