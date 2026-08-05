package lexer

import (
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
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
			cst.TextRange{Start: uint32(position), End: uint32(end)},
		))
		position = end
	}
	return tokens
}

func next(source string, position int) (syntax.Kind, int) {
	switch source[position] {
	case ' ', '\t', '\f':
		end := position + 1
		for end < len(source) && (source[end] == ' ' || source[end] == '\t' || source[end] == '\f') {
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
		if position+1 < len(source) && source[position+1] == '/' {
			return syntax.TkLineComment, scanLineComment(source, position)
		}
		if position+1 < len(source) && source[position+1] == '*' {
			return syntax.TkBlockComment, scanBlockComment(source, position)
		}
		return syntax.TkOperator, scanOperator(source, position)
	case '\'', '"':
		return syntax.TkString, scanQuoted(source, position, source[position])
	case '`':
		return syntax.TkTemplate, scanTemplate(source, position)
	case '(':
		return syntax.TkOpenParen, 1
	case ')':
		return syntax.TkCloseParen, 1
	case '{':
		return syntax.TkOpenBrace, 1
	case '}':
		return syntax.TkCloseBrace, 1
	case '[':
		return syntax.TkOpenBracket, 1
	case ']':
		return syntax.TkCloseBracket, 1
	case '.':
		if position+2 < len(source) && source[position+1] == '.' && source[position+2] == '.' {
			return syntax.TkOperator, 3
		}
		return syntax.TkDot, 1
	case ',':
		return syntax.TkComma, 1
	case ':':
		return syntax.TkColon, 1
	case ';':
		return syntax.TkSemicolon, 1
	case '?':
		if position+1 < len(source) && source[position+1] == '.' {
			return syntax.TkOptionalChain, 2
		}
		if position+1 < len(source) && source[position+1] == '?' {
			if position+2 < len(source) && source[position+2] == '=' {
				return syntax.TkOperator, 3
			}
			return syntax.TkOperator, 2
		}
		return syntax.TkQuestion, 1
	case '=':
		if position+1 < len(source) && source[position+1] == '>' {
			return syntax.TkArrow, 2
		}
		return syntax.TkOperator, scanOperator(source, position)
	default:
		if isIdentifierStart(source[position]) {
			end := scanIdentifier(source, position)
			text := source[position:end]
			if isKeyword(text) {
				return syntax.TkKeyword, end - position
			}
			return syntax.TkIdentifier, end - position
		}
		if isDigit(source[position]) {
			return syntax.TkNumber, scanNumber(source, position) - position
		}
		if isOperatorByte(source[position]) {
			return syntax.TkOperator, scanOperator(source, position)
		}
		return syntax.TkUnknown, 1
	}
}

func scanLineComment(source string, position int) int {
	end := position + 2
	for end < len(source) && source[end] != '\r' && source[end] != '\n' {
		end++
	}
	return end - position
}

func scanBlockComment(source string, position int) int {
	for end := position + 2; end+1 < len(source); end++ {
		if source[end] == '*' && source[end+1] == '/' {
			return end - position + 2
		}
	}
	return len(source) - position
}

func scanQuoted(source string, position int, quote byte) int {
	escaped := false
	for end := position + 1; end < len(source); end++ {
		if escaped {
			escaped = false
			continue
		}
		if source[end] == '\\' {
			escaped = true
			continue
		}
		if source[end] == quote {
			return end - position + 1
		}
	}
	return len(source) - position
}

func scanTemplate(source string, position int) int {
	escaped := false
	for end := position + 1; end < len(source); end++ {
		if escaped {
			escaped = false
			continue
		}
		if source[end] == '\\' {
			escaped = true
			continue
		}
		if source[end] == '`' {
			return end - position + 1
		}
	}
	return len(source) - position
}

func scanIdentifier(source string, position int) int {
	position++
	for position < len(source) && isIdentifierContinue(source[position]) {
		position++
	}
	return position
}

func scanNumber(source string, position int) int {
	position++
	for position < len(source) && (isIdentifierContinue(source[position]) ||
		source[position] == '.' || source[position] == '_') {
		position++
	}
	return position
}

func scanOperator(source string, position int) int {
	end := position + 1
	for end < len(source) && end-position < 4 && isOperatorByte(source[end]) {
		if source[end] == '/' {
			break
		}
		end++
	}
	return end - position
}

func isIdentifierStart(value byte) bool {
	return value == '_' || value == '$' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= 0x80
}

func isIdentifierContinue(value byte) bool {
	return isIdentifierStart(value) || isDigit(value)
}

func isDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

func isOperatorByte(value byte) bool {
	switch value {
	case '+', '-', '*', '%', '=', '!', '<', '>', '&', '|', '^', '~', '/':
		return true
	}
	return false
}

func isKeyword(text string) bool {
	switch text {
	case "as", "async", "await", "break", "case", "catch", "class", "const",
		"continue", "debugger", "default", "delete", "do", "else", "export",
		"extends", "false", "finally", "for", "from", "function", "get", "if",
		"import", "in", "instanceof", "let", "new", "null", "of", "return",
		"satisfies", "set", "static", "super", "switch", "this", "throw", "true", "try",
		"typeof", "undefined", "var", "void", "while", "with", "yield":
		return true
	}
	return false
}
