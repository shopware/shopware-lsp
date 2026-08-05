package syntax

import "github.com/shopware/shopware-lsp/internal/parser/cst"

const phpBase Kind = 24576

const (
	TkWhitespace Kind = phpBase + iota
	TkLineBreak
	TkLineComment
	TkBlockComment
	TkOpenTag
	TkCloseTag
	TkIdentifier
	TkKeyword
	TkVariable
	TkNumber
	TkString
	TkBackslash
	TkObjectOperator
	TkNullsafeObjectOperator
	TkScopeResolution
	TkAttributeOpen
	TkOpenParen
	TkCloseParen
	TkOpenBrace
	TkCloseBrace
	TkOpenBracket
	TkCloseBracket
	TkComma
	TkColon
	TkSemicolon
	TkEquals
	TkQuestion
	TkPipe
	TkAmpersand
	TkEllipsis
	TkArrow
	TkOperator
	TkUnknown

	PhpProgram
	PhpNamespace
	PhpUseDeclaration
	PhpClassDeclaration
	PhpInterfaceDeclaration
	PhpTraitDeclaration
	PhpEnumDeclaration
	PhpClassBody
	PhpExtendsClause
	PhpImplementsClause
	PhpMethodDeclaration
	PhpFunctionDeclaration
	PhpPropertyDeclaration
	PhpPropertyHookList
	PhpPropertyHook
	PhpConstDeclaration
	PhpClassConstDeclaration
	PhpEnumCaseDeclaration
	PhpTraitUseDeclaration
	PhpParameterList
	PhpParameter
	PhpType
	PhpNullableType
	PhpUnionType
	PhpIntersectionType
	PhpAttributeGroup
	PhpAttribute
	PhpArgumentList
	PhpArgument
	PhpNamedArgument
	PhpAssignmentExpression
	PhpBinaryExpression
	PhpUnaryExpression
	PhpTernaryExpression
	PhpMemberCall
	PhpScopedCall
	PhpFunctionCall
	PhpMemberAccess
	PhpScopedAccess
	PhpArrayAccess
	PhpObjectCreation
	PhpAnonymousClass
	PhpClosure
	PhpArrowFunction
	PhpMatchExpression
	PhpMatchArm
	PhpThrowExpression
	PhpYieldExpression
	PhpCloneExpression
	PhpCastExpression
	PhpArray
	PhpArrayItem
	PhpString
	PhpName
	PhpVariable
	PhpNumber
	PhpBoolean
	PhpNull
	PhpBlock
	PhpReturnStatement
	PhpIfStatement
	PhpElseIfClause
	PhpElseClause
	PhpSwitchStatement
	PhpCaseClause
	PhpWhileStatement
	PhpDoWhileStatement
	PhpForStatement
	PhpForeachStatement
	PhpTryStatement
	PhpCatchClause
	PhpFinallyClause
	PhpThrowStatement
	PhpBreakStatement
	PhpContinueStatement
	PhpEchoStatement
	PhpGlobalStatement
	PhpStaticStatement
	PhpExpressionStatement
	PhpParenthesized
	Error

	phpKindCount
)

func init() {
	names := make([]string, int(phpKindCount-phpBase))
	texts := make([]string, len(names))
	set := func(kind Kind, name, text string) {
		index := int(kind - phpBase)
		names[index] = name
		texts[index] = text
	}

	set(TkWhitespace, "PHP_WHITESPACE", "whitespace")
	set(TkLineBreak, "PHP_LINE_BREAK", "line break")
	set(TkLineComment, "PHP_LINE_COMMENT", "line comment")
	set(TkBlockComment, "PHP_BLOCK_COMMENT", "block comment")
	set(TkOpenTag, "PHP_OPEN_TAG", "<?php")
	set(TkCloseTag, "PHP_CLOSE_TAG", "?>")
	set(TkIdentifier, "PHP_IDENTIFIER_TOKEN", "identifier")
	set(TkKeyword, "PHP_KEYWORD", "keyword")
	set(TkVariable, "PHP_VARIABLE_TOKEN", "variable")
	set(TkNumber, "PHP_NUMBER_TOKEN", "number")
	set(TkString, "PHP_STRING_TOKEN", "string")
	set(TkBackslash, "PHP_BACKSLASH", "\\")
	set(TkObjectOperator, "PHP_OBJECT_OPERATOR", "->")
	set(TkNullsafeObjectOperator, "PHP_NULLSAFE_OBJECT_OPERATOR", "?->")
	set(TkScopeResolution, "PHP_SCOPE_RESOLUTION", "::")
	set(TkAttributeOpen, "PHP_ATTRIBUTE_OPEN", "#[")
	set(TkOpenParen, "PHP_OPEN_PAREN", "(")
	set(TkCloseParen, "PHP_CLOSE_PAREN", ")")
	set(TkOpenBrace, "PHP_OPEN_BRACE", "{")
	set(TkCloseBrace, "PHP_CLOSE_BRACE", "}")
	set(TkOpenBracket, "PHP_OPEN_BRACKET", "[")
	set(TkCloseBracket, "PHP_CLOSE_BRACKET", "]")
	set(TkComma, "PHP_COMMA", ",")
	set(TkColon, "PHP_COLON", ":")
	set(TkSemicolon, "PHP_SEMICOLON", ";")
	set(TkEquals, "PHP_EQUALS", "=")
	set(TkQuestion, "PHP_QUESTION", "?")
	set(TkPipe, "PHP_PIPE", "|")
	set(TkAmpersand, "PHP_AMPERSAND", "&")
	set(TkEllipsis, "PHP_ELLIPSIS", "...")
	set(TkArrow, "PHP_ARROW", "=>")
	set(TkOperator, "PHP_OPERATOR", "operator")
	set(TkUnknown, "PHP_UNKNOWN", "unknown token")

	set(PhpProgram, "PHP_PROGRAM", "")
	set(PhpNamespace, "PHP_NAMESPACE", "")
	set(PhpUseDeclaration, "PHP_USE_DECLARATION", "")
	set(PhpClassDeclaration, "PHP_CLASS_DECLARATION", "")
	set(PhpInterfaceDeclaration, "PHP_INTERFACE_DECLARATION", "")
	set(PhpTraitDeclaration, "PHP_TRAIT_DECLARATION", "")
	set(PhpEnumDeclaration, "PHP_ENUM_DECLARATION", "")
	set(PhpClassBody, "PHP_CLASS_BODY", "")
	set(PhpExtendsClause, "PHP_EXTENDS_CLAUSE", "")
	set(PhpImplementsClause, "PHP_IMPLEMENTS_CLAUSE", "")
	set(PhpMethodDeclaration, "PHP_METHOD_DECLARATION", "")
	set(PhpFunctionDeclaration, "PHP_FUNCTION_DECLARATION", "")
	set(PhpPropertyDeclaration, "PHP_PROPERTY_DECLARATION", "")
	set(PhpPropertyHookList, "PHP_PROPERTY_HOOK_LIST", "")
	set(PhpPropertyHook, "PHP_PROPERTY_HOOK", "")
	set(PhpConstDeclaration, "PHP_CONST_DECLARATION", "")
	set(PhpClassConstDeclaration, "PHP_CLASS_CONST_DECLARATION", "")
	set(PhpEnumCaseDeclaration, "PHP_ENUM_CASE_DECLARATION", "")
	set(PhpTraitUseDeclaration, "PHP_TRAIT_USE_DECLARATION", "")
	set(PhpParameterList, "PHP_PARAMETER_LIST", "")
	set(PhpParameter, "PHP_PARAMETER", "")
	set(PhpType, "PHP_TYPE", "")
	set(PhpNullableType, "PHP_NULLABLE_TYPE", "")
	set(PhpUnionType, "PHP_UNION_TYPE", "")
	set(PhpIntersectionType, "PHP_INTERSECTION_TYPE", "")
	set(PhpAttributeGroup, "PHP_ATTRIBUTE_GROUP", "")
	set(PhpAttribute, "PHP_ATTRIBUTE", "")
	set(PhpArgumentList, "PHP_ARGUMENT_LIST", "")
	set(PhpArgument, "PHP_ARGUMENT", "")
	set(PhpNamedArgument, "PHP_NAMED_ARGUMENT", "")
	set(PhpAssignmentExpression, "PHP_ASSIGNMENT_EXPRESSION", "")
	set(PhpBinaryExpression, "PHP_BINARY_EXPRESSION", "")
	set(PhpUnaryExpression, "PHP_UNARY_EXPRESSION", "")
	set(PhpTernaryExpression, "PHP_TERNARY_EXPRESSION", "")
	set(PhpMemberCall, "PHP_MEMBER_CALL", "")
	set(PhpScopedCall, "PHP_SCOPED_CALL", "")
	set(PhpFunctionCall, "PHP_FUNCTION_CALL", "")
	set(PhpMemberAccess, "PHP_MEMBER_ACCESS", "")
	set(PhpScopedAccess, "PHP_SCOPED_ACCESS", "")
	set(PhpArrayAccess, "PHP_ARRAY_ACCESS", "")
	set(PhpObjectCreation, "PHP_OBJECT_CREATION", "")
	set(PhpAnonymousClass, "PHP_ANONYMOUS_CLASS", "")
	set(PhpClosure, "PHP_CLOSURE", "")
	set(PhpArrowFunction, "PHP_ARROW_FUNCTION", "")
	set(PhpMatchExpression, "PHP_MATCH_EXPRESSION", "")
	set(PhpMatchArm, "PHP_MATCH_ARM", "")
	set(PhpThrowExpression, "PHP_THROW_EXPRESSION", "")
	set(PhpYieldExpression, "PHP_YIELD_EXPRESSION", "")
	set(PhpCloneExpression, "PHP_CLONE_EXPRESSION", "")
	set(PhpCastExpression, "PHP_CAST_EXPRESSION", "")
	set(PhpArray, "PHP_ARRAY", "")
	set(PhpArrayItem, "PHP_ARRAY_ITEM", "")
	set(PhpString, "PHP_STRING", "")
	set(PhpName, "PHP_NAME", "")
	set(PhpVariable, "PHP_VARIABLE", "")
	set(PhpNumber, "PHP_NUMBER", "")
	set(PhpBoolean, "PHP_BOOLEAN", "")
	set(PhpNull, "PHP_NULL", "")
	set(PhpBlock, "PHP_BLOCK", "")
	set(PhpReturnStatement, "PHP_RETURN_STATEMENT", "")
	set(PhpIfStatement, "PHP_IF_STATEMENT", "")
	set(PhpElseIfClause, "PHP_ELSEIF_CLAUSE", "")
	set(PhpElseClause, "PHP_ELSE_CLAUSE", "")
	set(PhpSwitchStatement, "PHP_SWITCH_STATEMENT", "")
	set(PhpCaseClause, "PHP_CASE_CLAUSE", "")
	set(PhpWhileStatement, "PHP_WHILE_STATEMENT", "")
	set(PhpDoWhileStatement, "PHP_DO_WHILE_STATEMENT", "")
	set(PhpForStatement, "PHP_FOR_STATEMENT", "")
	set(PhpForeachStatement, "PHP_FOREACH_STATEMENT", "")
	set(PhpTryStatement, "PHP_TRY_STATEMENT", "")
	set(PhpCatchClause, "PHP_CATCH_CLAUSE", "")
	set(PhpFinallyClause, "PHP_FINALLY_CLAUSE", "")
	set(PhpThrowStatement, "PHP_THROW_STATEMENT", "")
	set(PhpBreakStatement, "PHP_BREAK_STATEMENT", "")
	set(PhpContinueStatement, "PHP_CONTINUE_STATEMENT", "")
	set(PhpEchoStatement, "PHP_ECHO_STATEMENT", "")
	set(PhpGlobalStatement, "PHP_GLOBAL_STATEMENT", "")
	set(PhpStaticStatement, "PHP_STATIC_STATEMENT", "")
	set(PhpExpressionStatement, "PHP_EXPRESSION_STATEMENT", "")
	set(PhpParenthesized, "PHP_PARENTHESIZED", "")
	set(Error, "PHP_ERROR", "")

	cst.RegisterLanguage(cst.LanguageSpec{
		Name:       "php",
		Base:       phpBase,
		KindNames:  names,
		TokenTexts: texts,
		FirstNode:  PhpProgram,
		TriviaKinds: []Kind{
			TkWhitespace,
			TkLineBreak,
			TkLineComment,
			TkBlockComment,
		},
	})
}
