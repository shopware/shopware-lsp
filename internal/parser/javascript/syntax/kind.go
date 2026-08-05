package syntax

import "github.com/shopware/shopware-lsp/internal/parser/cst"

const javascriptBase Kind = 20480

const (
	TkWhitespace Kind = javascriptBase + iota
	TkLineBreak
	TkLineComment
	TkBlockComment
	TkIdentifier
	TkKeyword
	TkNumber
	TkString
	TkTemplate
	TkOpenParen
	TkCloseParen
	TkOpenBrace
	TkCloseBrace
	TkOpenBracket
	TkCloseBracket
	TkDot
	TkOptionalChain
	TkComma
	TkColon
	TkSemicolon
	TkQuestion
	TkArrow
	TkOperator
	TkUnknown

	JsProgram
	JsImportStatement
	JsExportDefault
	JsVariableDeclaration
	JsExpressionStatement
	JsCallExpression
	JsMemberExpression
	JsArgumentList
	JsArgument
	JsObject
	JsProperty
	JsMethod
	JsArray
	JsFunction
	JsArrowFunction
	JsIdentifier
	JsString
	JsNumber
	JsBoolean
	JsNull
	JsBlock
	JsParenthesized
	JsComputed
	JsReturnStatement
	JsUnaryExpression
	Error

	javascriptKindCount
)

func init() {
	names := make([]string, int(javascriptKindCount-javascriptBase))
	texts := make([]string, len(names))
	set := func(kind Kind, name, text string) {
		index := int(kind - javascriptBase)
		names[index] = name
		texts[index] = text
	}

	set(TkWhitespace, "JS_WHITESPACE", "whitespace")
	set(TkLineBreak, "JS_LINE_BREAK", "line break")
	set(TkLineComment, "JS_LINE_COMMENT", "line comment")
	set(TkBlockComment, "JS_BLOCK_COMMENT", "block comment")
	set(TkIdentifier, "JS_IDENTIFIER_TOKEN", "identifier")
	set(TkKeyword, "JS_KEYWORD", "keyword")
	set(TkNumber, "JS_NUMBER_TOKEN", "number")
	set(TkString, "JS_STRING_TOKEN", "string")
	set(TkTemplate, "JS_TEMPLATE_TOKEN", "template string")
	set(TkOpenParen, "JS_OPEN_PAREN", "(")
	set(TkCloseParen, "JS_CLOSE_PAREN", ")")
	set(TkOpenBrace, "JS_OPEN_BRACE", "{")
	set(TkCloseBrace, "JS_CLOSE_BRACE", "}")
	set(TkOpenBracket, "JS_OPEN_BRACKET", "[")
	set(TkCloseBracket, "JS_CLOSE_BRACKET", "]")
	set(TkDot, "JS_DOT", ".")
	set(TkOptionalChain, "JS_OPTIONAL_CHAIN", "?.")
	set(TkComma, "JS_COMMA", ",")
	set(TkColon, "JS_COLON", ":")
	set(TkSemicolon, "JS_SEMICOLON", ";")
	set(TkQuestion, "JS_QUESTION", "?")
	set(TkArrow, "JS_ARROW", "=>")
	set(TkOperator, "JS_OPERATOR", "operator")
	set(TkUnknown, "JS_UNKNOWN", "unknown token")

	set(JsProgram, "JS_PROGRAM", "")
	set(JsImportStatement, "JS_IMPORT_STATEMENT", "")
	set(JsExportDefault, "JS_EXPORT_DEFAULT", "")
	set(JsVariableDeclaration, "JS_VARIABLE_DECLARATION", "")
	set(JsExpressionStatement, "JS_EXPRESSION_STATEMENT", "")
	set(JsCallExpression, "JS_CALL_EXPRESSION", "")
	set(JsMemberExpression, "JS_MEMBER_EXPRESSION", "")
	set(JsArgumentList, "JS_ARGUMENT_LIST", "")
	set(JsArgument, "JS_ARGUMENT", "")
	set(JsObject, "JS_OBJECT", "")
	set(JsProperty, "JS_PROPERTY", "")
	set(JsMethod, "JS_METHOD", "")
	set(JsArray, "JS_ARRAY", "")
	set(JsFunction, "JS_FUNCTION", "")
	set(JsArrowFunction, "JS_ARROW_FUNCTION", "")
	set(JsIdentifier, "JS_IDENTIFIER", "")
	set(JsString, "JS_STRING", "")
	set(JsNumber, "JS_NUMBER", "")
	set(JsBoolean, "JS_BOOLEAN", "")
	set(JsNull, "JS_NULL", "")
	set(JsBlock, "JS_BLOCK", "")
	set(JsParenthesized, "JS_PARENTHESIZED", "")
	set(JsComputed, "JS_COMPUTED", "")
	set(JsReturnStatement, "JS_RETURN_STATEMENT", "")
	set(JsUnaryExpression, "JS_UNARY_EXPRESSION", "")
	set(Error, "JS_ERROR", "")

	cst.RegisterLanguage(cst.LanguageSpec{
		Name:       "javascript",
		Base:       javascriptBase,
		KindNames:  names,
		TokenTexts: texts,
		FirstNode:  JsProgram,
		TriviaKinds: []Kind{
			TkWhitespace,
			TkLineBreak,
			TkLineComment,
			TkBlockComment,
		},
	})
}
