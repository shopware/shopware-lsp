package syntax

import "github.com/shopware/shopware-lsp/internal/parser/cst"

const scssBase Kind = 12288

const (
	TkWhitespace Kind = scssBase + iota
	TkLineBreak
	TkBlockComment
	TkLineComment
	TkOpenBrace
	TkCloseBrace
	TkOpenParen
	TkCloseParen
	TkOpenBracket
	TkCloseBracket
	TkInterpolationOpen
	TkColon
	TkSemicolon
	TkComma
	TkVariable
	TkAtKeyword
	TkIdentifier
	TkNumber
	TkSingleQuotedString
	TkDoubleQuotedString
	TkHash
	TkOperator
	TkUnknown

	ScssStylesheet
	ScssRule
	ScssDeclaration
	ScssVariableDeclaration
	ScssAtRule
	ScssBlock
	ScssFunctionCall
	ScssArgumentList
	ScssArgument
	ScssVariable
	ScssString
	ScssParenthesized
	ScssBracketList
	ScssInterpolation
	Error

	scssKindCount
)

func init() {
	names := make([]string, int(scssKindCount-scssBase))
	texts := make([]string, len(names))

	set := func(kind Kind, name, text string) {
		index := int(kind - scssBase)
		names[index] = name
		texts[index] = text
	}

	set(TkWhitespace, "SCSS_WHITESPACE", "whitespace")
	set(TkLineBreak, "SCSS_LINE_BREAK", "line break")
	set(TkBlockComment, "SCSS_BLOCK_COMMENT", "block comment")
	set(TkLineComment, "SCSS_LINE_COMMENT", "line comment")
	set(TkOpenBrace, "SCSS_OPEN_BRACE", "{")
	set(TkCloseBrace, "SCSS_CLOSE_BRACE", "}")
	set(TkOpenParen, "SCSS_OPEN_PAREN", "(")
	set(TkCloseParen, "SCSS_CLOSE_PAREN", ")")
	set(TkOpenBracket, "SCSS_OPEN_BRACKET", "[")
	set(TkCloseBracket, "SCSS_CLOSE_BRACKET", "]")
	set(TkInterpolationOpen, "SCSS_INTERPOLATION_OPEN", "#{")
	set(TkColon, "SCSS_COLON", ":")
	set(TkSemicolon, "SCSS_SEMICOLON", ";")
	set(TkComma, "SCSS_COMMA", ",")
	set(TkVariable, "SCSS_VARIABLE_TOKEN", "variable")
	set(TkAtKeyword, "SCSS_AT_KEYWORD", "at-keyword")
	set(TkIdentifier, "SCSS_IDENTIFIER", "identifier")
	set(TkNumber, "SCSS_NUMBER", "number")
	set(TkSingleQuotedString, "SCSS_SINGLE_QUOTED_STRING", "single-quoted string")
	set(TkDoubleQuotedString, "SCSS_DOUBLE_QUOTED_STRING", "double-quoted string")
	set(TkHash, "SCSS_HASH", "#")
	set(TkOperator, "SCSS_OPERATOR", "operator")
	set(TkUnknown, "SCSS_UNKNOWN", "unknown token")

	set(ScssStylesheet, "SCSS_STYLESHEET", "")
	set(ScssRule, "SCSS_RULE", "")
	set(ScssDeclaration, "SCSS_DECLARATION", "")
	set(ScssVariableDeclaration, "SCSS_VARIABLE_DECLARATION", "")
	set(ScssAtRule, "SCSS_AT_RULE", "")
	set(ScssBlock, "SCSS_BLOCK", "")
	set(ScssFunctionCall, "SCSS_FUNCTION_CALL", "")
	set(ScssArgumentList, "SCSS_ARGUMENT_LIST", "")
	set(ScssArgument, "SCSS_ARGUMENT", "")
	set(ScssVariable, "SCSS_VARIABLE", "")
	set(ScssString, "SCSS_STRING", "")
	set(ScssParenthesized, "SCSS_PARENTHESIZED", "")
	set(ScssBracketList, "SCSS_BRACKET_LIST", "")
	set(ScssInterpolation, "SCSS_INTERPOLATION", "")
	set(Error, "SCSS_ERROR", "")

	cst.RegisterLanguage(cst.LanguageSpec{
		Name:       "scss",
		Base:       scssBase,
		KindNames:  names,
		TokenTexts: texts,
		FirstNode:  ScssStylesheet,
		TriviaKinds: []Kind{
			TkWhitespace,
			TkLineBreak,
			TkBlockComment,
			TkLineComment,
		},
	})
}
