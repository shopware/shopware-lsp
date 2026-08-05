package lexer

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
	"github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

type Token = parsekit.Token

func Lex(source string) []Token {
	return LexInto(source, nil)
}

// LexInto tokenizes source into the provided reusable destination.
func LexInto(source string, tokens []Token) []Token {
	// Production PHP averages about one token per five source bytes. Reserve
	// one per four bytes to cover dense generated code without making ordinary
	// files carry the older one-per-three overestimate.
	capacity := len(source)/4 + 1
	if cap(tokens) < capacity {
		tokens = make([]Token, 0, capacity)
	} else {
		tokens = tokens[:0]
	}
	sourceRef := &source
	for position := 0; position < len(source); {
		kind, length := next(source, position)
		if length <= 0 {
			length = 1
		}
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
	rest := source[position:]
	switch {
	case strings.HasPrefix(rest, "<?php"):
		return syntax.TkOpenTag, 5
	case strings.HasPrefix(rest, "<?="):
		return syntax.TkOpenTag, 3
	case strings.HasPrefix(rest, "<?"):
		return syntax.TkOpenTag, 2
	case strings.HasPrefix(rest, "?>"):
		return syntax.TkCloseTag, 2
	case strings.HasPrefix(rest, "?->"):
		return syntax.TkNullsafeObjectOperator, 3
	case strings.HasPrefix(rest, "->"):
		return syntax.TkObjectOperator, 2
	case strings.HasPrefix(rest, "::"):
		return syntax.TkScopeResolution, 2
	case strings.HasPrefix(rest, "#["):
		return syntax.TkAttributeOpen, 2
	case strings.HasPrefix(rest, "..."):
		return syntax.TkEllipsis, 3
	case strings.HasPrefix(rest, "=>"):
		return syntax.TkArrow, 2
	case strings.HasPrefix(rest, "<<<"):
		return syntax.TkString, scanHeredoc(source, position)
	}
	if length := compoundOperatorLength(rest); length != 0 {
		return syntax.TkOperator, length
	}

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
			return syntax.TkLineComment, scanLineComment(source, position, 2)
		}
		if position+1 < len(source) && source[position+1] == '*' {
			return syntax.TkBlockComment, scanBlockComment(source, position)
		}
		return syntax.TkOperator, scanOperator(source, position)
	case '#':
		return syntax.TkLineComment, scanLineComment(source, position, 1)
	case '\'', '"', '`':
		return syntax.TkString, scanQuoted(source, position, source[position])
	case '$':
		return syntax.TkVariable, scanVariable(source, position)
	case '\\':
		return syntax.TkBackslash, 1
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
	case ',':
		return syntax.TkComma, 1
	case ':':
		return syntax.TkColon, 1
	case ';':
		return syntax.TkSemicolon, 1
	case '=':
		return syntax.TkEquals, 1
	case '?':
		return syntax.TkQuestion, 1
	case '|':
		return syntax.TkPipe, 1
	case '&':
		return syntax.TkAmpersand, 1
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

func scanLineComment(source string, position, prefix int) int {
	end := position + prefix
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

func scanHeredoc(source string, position int) int {
	headerEnd := position
	for headerEnd < len(source) &&
		source[headerEnd] != '\r' && source[headerEnd] != '\n' {
		headerEnd++
	}
	header := strings.TrimSpace(source[position+3 : headerEnd])
	if len(header) >= 2 &&
		(header[0] == '\'' || header[0] == '"') &&
		header[len(header)-1] == header[0] {
		header = header[1 : len(header)-1]
	}
	if header == "" {
		return 3
	}
	lineStart := headerEnd
	if lineStart < len(source) && source[lineStart] == '\r' {
		lineStart++
	}
	if lineStart < len(source) && source[lineStart] == '\n' {
		lineStart++
	}
	for lineStart < len(source) {
		lineEnd := lineStart
		for lineEnd < len(source) &&
			source[lineEnd] != '\r' && source[lineEnd] != '\n' {
			lineEnd++
		}
		line := source[lineStart:lineEnd]
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, header) {
			after := trimmed[len(header):]
			if after == "" ||
				after[0] != ' ' && after[0] != '\t' &&
					!isIdentifierContinue(after[0]) {
				labelStart := lineStart + len(line) - len(trimmed)
				return labelStart + len(header) - position
			}
		}
		lineStart = lineEnd
		if lineStart < len(source) && source[lineStart] == '\r' {
			lineStart++
		}
		if lineStart < len(source) && source[lineStart] == '\n' {
			lineStart++
		}
	}
	return len(source) - position
}

func scanVariable(source string, position int) int {
	end := position + 1
	for end < len(source) && isIdentifierContinue(source[end]) {
		end++
	}
	return end - position
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

func compoundOperatorLength(source string) int {
	if len(source) >= 3 {
		switch source[:3] {
		case "??=", "===", "!==", "<=>", "**=", "<<=", ">>=":
			return 3
		}
	}
	if len(source) >= 2 {
		switch source[:2] {
		case "??", "==", "!=", "<=", ">=", "++", "--", "**", "<<", ">>",
			"&&", "||", "+=", "-=", "*=", "/=", ".=", "%=", "&=", "|=", "^=":
			return 2
		}
	}
	return 0
}

func scanOperator(source string, position int) int {
	end := position + 1
	for end < len(source) && end-position < 3 && isOperatorByte(source[end]) {
		end++
	}
	return end - position
}

func isIdentifierStart(value byte) bool {
	return value == '_' ||
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
	case '+', '-', '*', '%', '!', '<', '>', '^', '~', '/', '.', '@':
		return true
	}
	return false
}

func isKeyword(text string) bool {
	switch text {
	case "abstract", "and", "array", "as", "break", "callable", "case",
		"catch", "class", "clone", "const", "continue", "declare", "default",
		"do", "echo", "else", "elseif", "empty", "enddeclare", "endfor",
		"endforeach", "endif", "endswitch", "endwhile", "enum", "eval",
		"exit", "extends", "false", "final", "finally", "fn", "for",
		"foreach", "from", "function", "global", "goto", "if", "implements",
		"include", "include_once", "instanceof", "insteadof", "interface",
		"isset", "list", "match", "namespace", "new", "null", "or", "parent",
		"print", "private", "protected", "public", "readonly", "require",
		"require_once", "return", "self", "static", "switch", "throw", "trait",
		"true", "try", "unset", "use", "var", "while", "xor", "yield":
		return true
	}
	needsFold := false
	for index := 0; index < len(text); index++ {
		value := text[index]
		switch {
		case value >= 'A' && value <= 'Z':
			needsFold = true
		case value >= 'a' && value <= 'z', value == '_':
		default:
			return false
		}
	}
	if !needsFold {
		return false
	}
	switch len(text) {
	case 2:
		return equalKeyword(text, "as", "do", "fn", "if", "or")
	case 3:
		return equalKeyword(text, "and", "for", "new", "try", "use", "var", "xor")
	case 4:
		return equalKeyword(
			text,
			"case", "echo", "else", "enum", "eval", "exit", "from", "goto",
			"list", "null", "self", "true",
		)
	case 5:
		return equalKeyword(
			text,
			"array", "break", "catch", "class", "clone", "const", "empty",
			"endif", "false", "final", "isset", "match", "print", "throw",
			"trait", "unset", "while", "yield",
		)
	case 6:
		return equalKeyword(
			text,
			"elseif", "endfor", "global", "parent", "public", "return",
			"static", "switch",
		)
	case 7:
		return equalKeyword(
			text,
			"declare", "default", "extends", "finally", "foreach", "include",
			"private", "require",
		)
	case 8:
		return equalKeyword(
			text,
			"abstract", "callable", "continue", "endwhile", "function",
			"readonly",
		)
	case 9:
		return equalKeyword(
			text,
			"endswitch", "insteadof", "interface", "namespace", "protected",
		)
	case 10:
		return equalKeyword(
			text,
			"enddeclare", "endforeach", "implements", "instanceof",
		)
	case 12:
		return equalKeyword(text, "include_once", "require_once")
	default:
		return false
	}
}

func equalKeyword(text string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.EqualFold(text, candidate) {
			return true
		}
	}
	return false
}
