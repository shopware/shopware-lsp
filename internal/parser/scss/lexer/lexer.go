package lexer

import (
	"github.com/shopware/shopware-lsp/internal/parser/bytescan"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
	"github.com/shopware/shopware-lsp/internal/parser/scss/syntax"
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
			(source[end] == ' ' || source[end] == '\t' || source[end] == '\f') {
			end++
		}
		return syntax.TkWhitespace, end - position
	case '\r', '\n':
		end := position + 1
		if source[position] == '\r' && end < len(source) && source[end] == '\n' {
			end++
		}
		return syntax.TkLineBreak, end - position
	case '/':
		if position+1 < len(source) && source[position+1] == '*' {
			return syntax.TkBlockComment, scanBlockComment(source, position)
		}
		if position+1 < len(source) &&
			source[position+1] == '/' &&
			isLineCommentStart(source, position) {
			return syntax.TkLineComment, scanLineComment(source, position)
		}
		return syntax.TkOperator, 1
	case '{':
		return syntax.TkOpenBrace, 1
	case '}':
		return syntax.TkCloseBrace, 1
	case '(':
		return syntax.TkOpenParen, 1
	case ')':
		return syntax.TkCloseParen, 1
	case '[':
		return syntax.TkOpenBracket, 1
	case ']':
		return syntax.TkCloseBracket, 1
	case ':':
		return syntax.TkColon, 1
	case ';':
		return syntax.TkSemicolon, 1
	case ',':
		return syntax.TkComma, 1
	case '\'':
		return syntax.TkSingleQuotedString, scanQuoted(source, position, '\'')
	case '"':
		return syntax.TkDoubleQuotedString, scanQuoted(source, position, '"')
	case '#':
		if position+1 < len(source) && source[position+1] == '{' {
			return syntax.TkInterpolationOpen, 2
		}
		return syntax.TkHash, 1
	case '$':
		if position+1 < len(source) && isNameStart(source[position+1]) {
			return syntax.TkVariable, scanName(source, position+1) - position
		}
		return syntax.TkOperator, 1
	case '@':
		if position+1 < len(source) && isNameStart(source[position+1]) {
			return syntax.TkAtKeyword, scanName(source, position+1) - position
		}
		return syntax.TkOperator, 1
	case '+', '-', '*', '%', '=', '!', '<', '>', '&', '|', '~', '^':
		if (source[position] == '-' && position+1 < len(source) && isNameStart(source[position+1])) ||
			(source[position] == '-' && position+2 < len(source) && source[position+1] == '-' && isNameStart(source[position+2])) {
			return syntax.TkIdentifier, scanName(source, position) - position
		}
		end := position + 1
		if end < len(source) && isOperatorPair(source[position], source[end]) {
			end++
		}
		return syntax.TkOperator, end - position
	default:
		if isDigit(source[position]) ||
			source[position] == '.' && position+1 < len(source) && isDigit(source[position+1]) {
			return syntax.TkNumber, scanNumber(source, position) - position
		}
		if isNameStart(source[position]) || source[position] == '\\' {
			return syntax.TkIdentifier, scanName(source, position) - position
		}
		return syntax.TkUnknown, 1
	}
}

func scanBlockComment(source string, position int) int {
	for end := position + 2; end+1 < len(source); end++ {
		end = bytescan.IndexByte(source, end, '*')
		if end+1 < len(source) && source[end+1] == '/' {
			return end - position + 2
		}
	}
	return len(source) - position
}

func scanLineComment(source string, position int) int {
	end := bytescan.IndexAny2(source, position+2, '\r', '\n')
	return end - position
}

func scanQuoted(source string, position int, quote byte) int {
	for end := position + 1; end < len(source); {
		end = bytescan.IndexAny2(source, end, quote, '\\')
		if end >= len(source) {
			break
		}
		if source[end] == quote {
			return end - position + 1
		}
		end += 2
	}
	return len(source) - position
}

func scanName(source string, position int) int {
	end := position
	for end < len(source) {
		if isNameContinue(source[end]) {
			end++
			continue
		}
		if source[end] == '\\' && end+1 < len(source) {
			end += 2
			continue
		}
		break
	}
	return end
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
	for end < len(source) && (isNameContinue(source[end]) || source[end] == '%') {
		end++
	}
	return end
}

func isNameStart(value byte) bool {
	return value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value == '_' ||
		value >= 0x80
}

func isNameContinue(value byte) bool {
	return isNameStart(value) || isDigit(value) || value == '-'
}

func isDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

func isOperatorPair(first, second byte) bool {
	switch first {
	case '=', '!', '<', '>':
		return second == '='
	case '&':
		return second == '&'
	case '|':
		return second == '|'
	default:
		return false
	}
}

func isLineCommentStart(source string, position int) bool {
	if position == 0 {
		return true
	}
	switch source[position-1] {
	case ' ', '\t', '\r', '\n', '\f', ';', '{', '}':
		return true
	default:
		return false
	}
}
