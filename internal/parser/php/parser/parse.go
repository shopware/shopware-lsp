package parser

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
	"github.com/shopware/shopware-lsp/internal/parser/php/lexer"
	"github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

type Error = parsekit.Error

type Result struct {
	Tree   *syntax.Tree
	Errors []Error
}

func Parse(source string) Result {
	return parse(source, nil)
}

func parse(
	source string,
	observeBuffers func(parsekit.BufferStats),
) Result {
	tokens := lexer.LexInto(
		source,
		parsekit.AcquireTokenBuffer(len(source)/4+1),
	)
	parser := parsekit.NewOwned(tokens, parsekit.Config{
		GeneralRecoverySet: []syntax.Kind{
			syntax.TkSemicolon,
			syntax.TkCloseBrace,
		},
		ErrorKind: syntax.Error,
	})
	root := parser.Start()
	for !parser.AtEnd() {
		position := parser.GetPos()
		parseStatement(parser, false)
		if parser.GetPos() == position {
			parser.AddError(parsekit.NewErrorBuilder("PHP statement"))
			parser.Bump()
		}
	}
	parser.Complete(root, syntax.PhpProgram)
	if observeBuffers != nil {
		observeBuffers(parser.BufferStats())
	}
	tree, errors := parser.Finish(source)
	return Result{Tree: tree, Errors: errors}
}

func parseStatement(parser *parsekit.Parser, inClass bool) {
	switch {
	case parser.At(syntax.TkOpenTag), parser.At(syntax.TkCloseTag):
		parser.Bump()
	case atKeyword(parser, "namespace"):
		parseNamespace(parser)
	case atKeyword(parser, "use") && !inClass:
		parseUse(parser)
	case startsClassDeclaration(parser):
		parseClassDeclaration(parser)
	case inClass && startsMethodDeclaration(parser):
		parseMethod(parser)
	case !inClass && startsFunctionDeclaration(parser):
		parseFunction(parser)
	case inClass && atKeyword(parser, "use"):
		parseTraitUse(parser)
	case inClass && atKeyword(parser, "case"):
		parseEnumCase(parser)
	case atKeyword(parser, "const") || inClass && startsClassConstDeclaration(parser):
		parseConst(parser, inClass)
	case inClass && startsPropertyDeclaration(parser):
		parseProperty(parser)
	case atKeyword(parser, "return"):
		parseReturn(parser)
	case atKeyword(parser, "if"):
		parseIf(parser)
	case atKeyword(parser, "switch"):
		parseSwitch(parser)
	case atKeyword(parser, "while"):
		parseWhile(parser)
	case atKeyword(parser, "do"):
		parseDoWhile(parser)
	case atKeyword(parser, "for"):
		parseFor(parser)
	case atKeyword(parser, "foreach"):
		parseForeach(parser)
	case atKeyword(parser, "try"):
		parseTry(parser)
	case atKeyword(parser, "throw"):
		parseKeywordExpressionStatement(parser, syntax.PhpThrowStatement)
	case atKeyword(parser, "break"):
		parseKeywordExpressionStatement(parser, syntax.PhpBreakStatement)
	case atKeyword(parser, "continue"):
		parseKeywordExpressionStatement(parser, syntax.PhpContinueStatement)
	case atKeyword(parser, "echo"):
		parseCommaExpressionStatement(parser, syntax.PhpEchoStatement)
	case atKeyword(parser, "global"):
		parseCommaExpressionStatement(parser, syntax.PhpGlobalStatement)
	case atKeyword(parser, "static") && !staticStartsExpression(parser):
		parseCommaExpressionStatement(parser, syntax.PhpStaticStatement)
	case parser.At(syntax.TkOpenBrace):
		parseBlock(parser)
	case parser.At(syntax.TkCloseBrace):
		parser.Bump()
	default:
		parseExpressionStatement(parser)
	}
}

func parseNamespace(parser *parsekit.Parser) {
	node := parser.Start()
	parser.Bump()
	if isNameStart(parser) {
		parseName(parser)
	}
	if parser.At(syntax.TkSemicolon) {
		parser.Bump()
	} else if parser.At(syntax.TkOpenBrace) {
		parseBlock(parser)
	}
	parser.Complete(node, syntax.PhpNamespace)
}

func parseUse(parser *parsekit.Parser) {
	node := parser.Start()
	parser.Bump()
	depth := 0
	for !parser.AtEnd() {
		if parser.At(syntax.TkSemicolon) && depth == 0 {
			parser.Bump()
			break
		}
		switch {
		case parser.At(syntax.TkOpenBrace):
			depth++
		case parser.At(syntax.TkCloseBrace):
			if depth > 0 {
				depth--
			}
		}
		parser.Bump()
	}
	parser.Complete(node, syntax.PhpUseDeclaration)
}

func parseClassDeclaration(parser *parsekit.Parser) {
	declaration := parser.Start()
	parseAttributeGroups(parser)
	consumeModifiers(parser)

	kind := syntax.PhpClassDeclaration
	switch {
	case atKeyword(parser, "interface"):
		kind = syntax.PhpInterfaceDeclaration
	case atKeyword(parser, "trait"):
		kind = syntax.PhpTraitDeclaration
	case atKeyword(parser, "enum"):
		kind = syntax.PhpEnumDeclaration
	}
	if atKeyword(parser, "class", "interface", "trait", "enum") {
		parser.Bump()
	}
	if isNameStart(parser) {
		parseName(parser)
	}

	for !parser.AtEnd() && !parser.At(syntax.TkOpenBrace) && !parser.At(syntax.TkSemicolon) {
		switch {
		case atKeyword(parser, "extends"):
			parseNameClause(parser, syntax.PhpExtendsClause)
		case atKeyword(parser, "implements"):
			parseNameClause(parser, syntax.PhpImplementsClause)
		default:
			parser.Bump()
		}
	}

	if parser.At(syntax.TkOpenBrace) {
		parseClassBody(parser)
	} else if parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
	parser.Complete(declaration, kind)
}

func parseNameClause(parser *parsekit.Parser, kind syntax.Kind) {
	clause := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkOpenBrace) &&
		!atKeyword(parser, "extends", "implements") {
		if isNameStart(parser) {
			parseName(parser)
		} else if parser.At(syntax.TkComma) {
			parser.Bump()
		} else {
			break
		}
	}
	parser.Complete(clause, kind)
}

func parseClassBody(parser *parsekit.Parser) {
	body := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkCloseBrace) {
		position := parser.GetPos()
		parseStatement(parser, true)
		if parser.GetPos() == position {
			parser.Bump()
		}
	}
	parser.Expect(syntax.TkCloseBrace, nil)
	parser.Complete(body, syntax.PhpClassBody)
}

func parseMethod(parser *parsekit.Parser) {
	parseFunctionLike(parser, syntax.PhpMethodDeclaration, true)
}

func parseFunction(parser *parsekit.Parser) {
	parseFunctionLike(parser, syntax.PhpFunctionDeclaration, false)
}

func parseFunctionLike(parser *parsekit.Parser, kind syntax.Kind, allowModifiers bool) {
	method := parser.Start()
	parseAttributeGroups(parser)
	if allowModifiers {
		consumeModifiers(parser)
	}
	if atKeyword(parser, "function") {
		parser.Bump()
	}
	if parser.At(syntax.TkAmpersand) {
		parser.Bump()
	}
	if isNameStart(parser) {
		parseName(parser)
	}
	if parser.At(syntax.TkOpenParen) {
		parseParameters(parser)
	}
	if parser.At(syntax.TkColon) {
		parser.Bump()
		parseTypeExpression(parser, map[syntax.Kind]struct{}{
			syntax.TkOpenBrace: {},
			syntax.TkSemicolon: {},
		})
	}
	if parser.At(syntax.TkOpenBrace) {
		parseBlock(parser)
	} else if parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
	parser.Complete(method, kind)
}

func parseParameters(parser *parsekit.Parser) {
	list := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkCloseParen) {
		position := parser.GetPos()
		parameter := parser.Start()
		parseAttributeGroups(parser)
		consumeModifiers(parser)
		if !parser.At(syntax.TkVariable) && !parser.At(syntax.TkEllipsis) &&
			!startsByReferenceParameter(parser) {
			parseTypeExpression(parser, map[syntax.Kind]struct{}{
				syntax.TkVariable: {},
			})
		}
		if parser.At(syntax.TkAmpersand) {
			parser.Bump()
		}
		if parser.At(syntax.TkEllipsis) {
			parser.Bump()
		}
		if parser.At(syntax.TkVariable) {
			parseLeaf(parser, syntax.PhpVariable)
		}
		if parser.At(syntax.TkEquals) {
			parser.Bump()
			parseExpression(parser, stopSet(syntax.TkComma, syntax.TkCloseParen))
		}
		parser.Complete(parameter, syntax.PhpParameter)
		if parser.At(syntax.TkComma) {
			parser.Bump()
		}
		if parser.GetPos() == position {
			parser.Bump()
		}
	}
	parser.Expect(syntax.TkCloseParen, []syntax.Kind{syntax.TkColon, syntax.TkOpenBrace, syntax.TkSemicolon})
	parser.Complete(list, syntax.PhpParameterList)
}

func startsByReferenceParameter(parser *parsekit.Parser) bool {
	if !parser.At(syntax.TkAmpersand) {
		return false
	}
	next := nextNonTriviaKind(parser)
	return next == syntax.TkVariable || next == syntax.TkEllipsis
}

func parseProperty(parser *parsekit.Parser) {
	property := parser.Start()
	parseAttributeGroups(parser)
	consumeModifiers(parser)
	if !parser.At(syntax.TkVariable) {
		parseTypeExpression(parser, map[syntax.Kind]struct{}{
			syntax.TkVariable: {},
		})
	}
	for !parser.AtEnd() && !parser.At(syntax.TkSemicolon) && !parser.At(syntax.TkCloseBrace) {
		switch {
		case parser.At(syntax.TkVariable):
			parseLeaf(parser, syntax.PhpVariable)
		case parser.At(syntax.TkEquals):
			parser.Bump()
			parseExpression(parser, stopSet(syntax.TkComma, syntax.TkSemicolon))
		case parser.At(syntax.TkComma):
			parser.Bump()
		case parser.At(syntax.TkString):
			parseString(parser)
		case parser.At(syntax.TkOpenBracket):
			parseArray(parser)
		case parser.At(syntax.TkOpenBrace):
			parsePropertyHooks(parser)
			parser.Complete(property, syntax.PhpPropertyDeclaration)
			return
		default:
			parser.Bump()
		}
	}
	if parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
	parser.Complete(property, syntax.PhpPropertyDeclaration)
}

func parsePropertyHooks(parser *parsekit.Parser) {
	list := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkCloseBrace) {
		position := parser.GetPos()
		hook := parser.Start()
		parseAttributeGroups(parser)
		consumeModifiers(parser)
		if parser.At(syntax.TkAmpersand) {
			parser.Bump()
		}
		if atWord(parser, "get", "set") {
			parser.Bump()
		}
		if parser.At(syntax.TkOpenParen) {
			parseParameters(parser)
		}
		switch {
		case parser.At(syntax.TkArrow):
			parser.Bump()
			parseExpression(parser, stopSet(syntax.TkSemicolon, syntax.TkCloseBrace))
			if parser.At(syntax.TkSemicolon) {
				parser.Bump()
			}
		case parser.At(syntax.TkOpenBrace):
			parseBlock(parser)
		case parser.At(syntax.TkSemicolon):
			parser.Bump()
		}
		parser.Complete(hook, syntax.PhpPropertyHook)
		if parser.GetPos() == position {
			parser.Bump()
		}
	}
	parser.Expect(syntax.TkCloseBrace, nil)
	parser.Complete(list, syntax.PhpPropertyHookList)
}

func parseConst(parser *parsekit.Parser, inClass bool) {
	node := parser.Start()
	parseAttributeGroups(parser)
	if inClass {
		consumeModifiers(parser)
	}
	if atKeyword(parser, "const") {
		parser.Bump()
	}
	if typedConstStarts(parser) {
		parseTypeExpression(parser, stopSet())
	}
	for !parser.AtEnd() && !parser.At(syntax.TkSemicolon) && !parser.At(syntax.TkCloseBrace) {
		if isNameStart(parser) {
			parseName(parser)
			if parser.At(syntax.TkEquals) {
				parser.Bump()
				parseExpression(parser, stopSet(syntax.TkComma, syntax.TkSemicolon))
			}
		} else {
			parser.Bump()
		}
		if parser.At(syntax.TkComma) {
			parser.Bump()
		}
	}
	if parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
	kind := syntax.PhpConstDeclaration
	if inClass {
		kind = syntax.PhpClassConstDeclaration
	}
	parser.Complete(node, kind)
}

func parseEnumCase(parser *parsekit.Parser) {
	node := parser.Start()
	parseAttributeGroups(parser)
	parser.Bump()
	if isNameStart(parser) {
		parseName(parser)
	}
	if parser.At(syntax.TkEquals) {
		parser.Bump()
		parseExpression(parser, stopSet(syntax.TkSemicolon))
	}
	if parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
	parser.Complete(node, syntax.PhpEnumCaseDeclaration)
}

func parseTraitUse(parser *parsekit.Parser) {
	node := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkSemicolon) && !parser.At(syntax.TkOpenBrace) &&
		!parser.At(syntax.TkCloseBrace) {
		if isNameStart(parser) {
			parseName(parser)
		} else {
			parser.Bump()
		}
	}
	if parser.At(syntax.TkOpenBrace) {
		depth := 0
		for !parser.AtEnd() {
			switch {
			case parser.At(syntax.TkOpenBrace):
				depth++
			case parser.At(syntax.TkCloseBrace):
				depth--
			}
			parser.Bump()
			if depth == 0 {
				break
			}
		}
	} else if parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
	parser.Complete(node, syntax.PhpTraitUseDeclaration)
}

func parseReturn(parser *parsekit.Parser) {
	node := parser.Start()
	parser.Bump()
	parseExpression(parser, stopSet(syntax.TkSemicolon, syntax.TkCloseBrace))
	if parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
	parser.Complete(node, syntax.PhpReturnStatement)
}

func parseIf(parser *parsekit.Parser) {
	node := parser.Start()
	parser.Bump()
	parseCondition(parser)
	if parser.At(syntax.TkColon) {
		parseAlternativeBody(parser, "elseif", "else", "endif")
		for atKeyword(parser, "elseif") {
			clause := parser.Start()
			parser.Bump()
			parseCondition(parser)
			parseAlternativeBody(parser, "elseif", "else", "endif")
			parser.Complete(clause, syntax.PhpElseIfClause)
		}
		if atKeyword(parser, "else") {
			clause := parser.Start()
			parser.Bump()
			parseAlternativeBody(parser, "endif")
			parser.Complete(clause, syntax.PhpElseClause)
		}
		consumeAlternativeEnd(parser, "endif")
		parser.Complete(node, syntax.PhpIfStatement)
		return
	}
	parseControlledStatement(parser)
	for atKeyword(parser, "elseif") {
		clause := parser.Start()
		parser.Bump()
		parseCondition(parser)
		parseControlledStatement(parser)
		parser.Complete(clause, syntax.PhpElseIfClause)
	}
	if atKeyword(parser, "else") {
		clause := parser.Start()
		parser.Bump()
		parseControlledStatement(parser)
		parser.Complete(clause, syntax.PhpElseClause)
	}
	parser.Complete(node, syntax.PhpIfStatement)
}

func parseSwitch(parser *parsekit.Parser) {
	node := parser.Start()
	parser.Bump()
	parseCondition(parser)
	alternative := parser.At(syntax.TkColon)
	if !parser.At(syntax.TkOpenBrace) && !alternative {
		parser.Complete(node, syntax.PhpSwitchStatement)
		return
	}
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkCloseBrace) &&
		!atKeyword(parser, "endswitch") {
		if atKeyword(parser, "case", "default") {
			parseCase(parser)
			continue
		}
		parseStatement(parser, false)
	}
	if alternative {
		consumeAlternativeEnd(parser, "endswitch")
	} else {
		parser.Expect(syntax.TkCloseBrace, nil)
	}
	parser.Complete(node, syntax.PhpSwitchStatement)
}

func parseCase(parser *parsekit.Parser) {
	clause := parser.Start()
	isDefault := atKeyword(parser, "default")
	parser.Bump()
	if !isDefault {
		parseExpression(parser, stopSet(syntax.TkColon, syntax.TkSemicolon))
	}
	if parser.At(syntax.TkColon) || parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
	for !parser.AtEnd() && !parser.At(syntax.TkCloseBrace) &&
		!atKeyword(parser, "case", "default", "endswitch") {
		parseStatement(parser, false)
	}
	parser.Complete(clause, syntax.PhpCaseClause)
}

func parseWhile(parser *parsekit.Parser) {
	node := parser.Start()
	parser.Bump()
	parseCondition(parser)
	if parser.At(syntax.TkColon) {
		parseAlternativeBody(parser, "endwhile")
		consumeAlternativeEnd(parser, "endwhile")
	} else {
		parseControlledStatement(parser)
	}
	parser.Complete(node, syntax.PhpWhileStatement)
}

func parseDoWhile(parser *parsekit.Parser) {
	node := parser.Start()
	parser.Bump()
	parseControlledStatement(parser)
	if atKeyword(parser, "while") {
		parser.Bump()
		parseCondition(parser)
	}
	if parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
	parser.Complete(node, syntax.PhpDoWhileStatement)
}

func parseFor(parser *parsekit.Parser) {
	node := parser.Start()
	parser.Bump()
	if parser.At(syntax.TkOpenParen) {
		parser.Bump()
		parseExpressionList(parser, syntax.TkSemicolon)
		parser.Expect(syntax.TkSemicolon, []syntax.Kind{syntax.TkCloseParen})
		parseExpressionList(parser, syntax.TkSemicolon)
		parser.Expect(syntax.TkSemicolon, []syntax.Kind{syntax.TkCloseParen})
		parseExpressionList(parser, syntax.TkCloseParen)
		parser.Expect(syntax.TkCloseParen, []syntax.Kind{syntax.TkOpenBrace, syntax.TkSemicolon})
	}
	if parser.At(syntax.TkColon) {
		parseAlternativeBody(parser, "endfor")
		consumeAlternativeEnd(parser, "endfor")
	} else {
		parseControlledStatement(parser)
	}
	parser.Complete(node, syntax.PhpForStatement)
}

func parseForeach(parser *parsekit.Parser) {
	node := parser.Start()
	parser.Bump()
	if parser.At(syntax.TkOpenParen) {
		parser.Bump()
		parseExpression(parser, stopSet())
		if atKeyword(parser, "as") {
			parser.Bump()
		}
		if parser.At(syntax.TkAmpersand) {
			parser.Bump()
		}
		parseExpression(parser, stopSet(syntax.TkArrow, syntax.TkCloseParen))
		if parser.At(syntax.TkArrow) {
			parser.Bump()
			if parser.At(syntax.TkAmpersand) {
				parser.Bump()
			}
			parseExpression(parser, stopSet(syntax.TkCloseParen))
		}
		parser.Expect(syntax.TkCloseParen, []syntax.Kind{syntax.TkOpenBrace, syntax.TkSemicolon})
	}
	if parser.At(syntax.TkColon) {
		parseAlternativeBody(parser, "endforeach")
		consumeAlternativeEnd(parser, "endforeach")
	} else {
		parseControlledStatement(parser)
	}
	parser.Complete(node, syntax.PhpForeachStatement)
}

func parseTry(parser *parsekit.Parser) {
	node := parser.Start()
	parser.Bump()
	if parser.At(syntax.TkOpenBrace) {
		parseBlock(parser)
	}
	for atKeyword(parser, "catch") {
		clause := parser.Start()
		parser.Bump()
		if parser.At(syntax.TkOpenParen) {
			parser.Bump()
			parseTypeExpression(parser, stopSet(syntax.TkVariable, syntax.TkCloseParen))
			if parser.At(syntax.TkVariable) {
				parseLeaf(parser, syntax.PhpVariable)
			}
			parser.Expect(syntax.TkCloseParen, []syntax.Kind{syntax.TkOpenBrace})
		}
		if parser.At(syntax.TkOpenBrace) {
			parseBlock(parser)
		}
		parser.Complete(clause, syntax.PhpCatchClause)
	}
	if atKeyword(parser, "finally") {
		clause := parser.Start()
		parser.Bump()
		if parser.At(syntax.TkOpenBrace) {
			parseBlock(parser)
		}
		parser.Complete(clause, syntax.PhpFinallyClause)
	}
	parser.Complete(node, syntax.PhpTryStatement)
}

func parseKeywordExpressionStatement(parser *parsekit.Parser, kind syntax.Kind) {
	node := parser.Start()
	parser.Bump()
	parseExpression(parser, stopSet(syntax.TkSemicolon, syntax.TkCloseBrace))
	if parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
	parser.Complete(node, kind)
}

func parseCommaExpressionStatement(parser *parsekit.Parser, kind syntax.Kind) {
	node := parser.Start()
	parser.Bump()
	parseExpressionList(parser, syntax.TkSemicolon)
	if parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
	parser.Complete(node, kind)
}

func parseExpressionList(parser *parsekit.Parser, end syntax.Kind) {
	for !parser.AtEnd() && !parser.At(end) {
		position := parser.GetPos()
		parseExpression(parser, stopSet(syntax.TkComma, end))
		if parser.At(syntax.TkComma) {
			parser.Bump()
		}
		if parser.GetPos() == position {
			parser.Bump()
		}
	}
}

func parseCondition(parser *parsekit.Parser) {
	if parser.At(syntax.TkOpenParen) {
		parseParenthesized(parser)
	}
}

func parseControlledStatement(parser *parsekit.Parser) {
	if parser.At(syntax.TkOpenBrace) {
		parseBlock(parser)
		return
	}
	parseStatement(parser, false)
}

func parseAlternativeBody(
	parser *parsekit.Parser,
	terminators ...string,
) {
	block := parser.Start()
	parser.Expect(syntax.TkColon, nil)
	for !parser.AtEnd() && !atKeyword(parser, terminators...) {
		position := parser.GetPos()
		parseStatement(parser, false)
		if parser.GetPos() == position {
			parser.Bump()
		}
	}
	parser.Complete(block, syntax.PhpBlock)
}

func consumeAlternativeEnd(parser *parsekit.Parser, keyword string) {
	if atKeyword(parser, keyword) {
		parser.Bump()
	} else {
		parser.AddError(parsekit.NewErrorBuilder(keyword))
	}
	if parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
}

func parseExpressionStatement(parser *parsekit.Parser) {
	statement := parser.Start()
	parseExpression(parser, stopSet(syntax.TkSemicolon, syntax.TkCloseBrace))
	if parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
	parser.Complete(statement, syntax.PhpExpressionStatement)
}

func parseExpression(parser *parsekit.Parser, stop map[syntax.Kind]struct{}) (parsekit.CompletedMarker, bool) {
	return parseExpressionPrecedence(parser, stop, 0)
}

func parseExpressionPrecedence(
	parser *parsekit.Parser,
	stop map[syntax.Kind]struct{},
	minimumPrecedence int,
) (parsekit.CompletedMarker, bool) {
	if parser.AtEnd() || atStop(parser, stop) {
		return parsekit.CompletedMarker{}, false
	}
	left, ok := parsePrefix(parser, stop)
	if !ok {
		errorNode := parser.Start()
		parser.Bump()
		left = parser.Complete(errorNode, syntax.Error)
	}
	left = parseSuffixes(parser, left)

	for !parser.AtEnd() && !atStop(parser, stop) {
		if parser.At(syntax.TkQuestion) {
			if ternaryPrecedence < minimumPrecedence {
				return left, true
			}
			ternary := parser.Precede(left)
			parser.Bump()
			if !parser.At(syntax.TkColon) {
				parseExpressionPrecedence(parser, stopSet(syntax.TkColon), 0)
			}
			parser.Expect(syntax.TkColon, nil)
			parseExpressionPrecedence(parser, stop, ternaryPrecedence)
			left = parser.Complete(ternary, syntax.PhpTernaryExpression)
			continue
		}

		precedence, rightAssociative, assignment := binaryOperator(parser)
		if precedence < minimumPrecedence {
			return left, true
		}
		expression := parser.Precede(left)
		parser.Bump()
		nextPrecedence := precedence + 1
		if rightAssociative {
			nextPrecedence = precedence
		}
		right, parsed := parseExpressionPrecedence(parser, stop, nextPrecedence)
		if !parsed {
			parser.AddError(parsekit.NewErrorBuilder("expression"))
		} else if precedence > assignmentPrecedence {
			// Assignment is syntactically permitted as the right operand of
			// a tighter binary operator. For example, PHP groups
			// `$ready && $value = load()` as
			// `$ready && ($value = load())`.
			rightPrecedence, _, rightAssignment := binaryOperator(parser)
			if rightAssignment && rightPrecedence == assignmentPrecedence {
				assignmentNode := parser.Precede(right)
				parser.Bump()
				if _, ok := parseExpressionPrecedence(
					parser,
					stop,
					assignmentPrecedence,
				); !ok {
					parser.AddError(parsekit.NewErrorBuilder("expression"))
				}
				parser.Complete(
					assignmentNode,
					syntax.PhpAssignmentExpression,
				)
			}
		}
		kind := syntax.PhpBinaryExpression
		if assignment {
			kind = syntax.PhpAssignmentExpression
		}
		left = parser.Complete(expression, kind)
	}
	return left, true
}

func parsePrefix(
	parser *parsekit.Parser,
	stop map[syntax.Kind]struct{},
) (parsekit.CompletedMarker, bool) {
	switch {
	case isPrefixOperator(parser):
		node := parser.Start()
		operator := strings.ToLower(tokenText(parser.PeekToken()))
		parser.Bump()
		minimumPrecedence := tightPrefixPrecedence
		switch operator {
		case "!":
			minimumPrecedence = logicalNotPrecedence
		case "print":
			minimumPrecedence = printPrecedence
		case "include", "include_once", "require", "require_once":
			minimumPrecedence = throwPrecedence
		}
		operand, parsed := parseExpressionPrecedence(
			parser,
			stop,
			minimumPrecedence,
		)
		if !parsed {
			parser.AddError(parsekit.NewErrorBuilder("expression"))
		} else if minimumPrecedence > assignmentPrecedence {
			// PHP groups an assignment with the variable beneath a prefix
			// operator. For example, !$value = load() means
			// !($value = load()), not (!$value) = load().
			precedence, _, assignment := binaryOperator(parser)
			if assignment && precedence == assignmentPrecedence {
				assignmentNode := parser.Precede(operand)
				parser.Bump()
				if _, ok := parseExpressionPrecedence(
					parser,
					stop,
					assignmentPrecedence,
				); !ok {
					parser.AddError(parsekit.NewErrorBuilder("expression"))
				}
				parser.Complete(
					assignmentNode,
					syntax.PhpAssignmentExpression,
				)
			}
		}
		return parser.Complete(node, syntax.PhpUnaryExpression), true
	case atKeyword(parser, "throw"):
		node := parser.Start()
		parser.Bump()
		parseExpressionPrecedence(parser, stop, throwPrecedence)
		return parser.Complete(node, syntax.PhpThrowExpression), true
	case atKeyword(parser, "yield"):
		return parseYield(parser, stop), true
	case atKeyword(parser, "clone"):
		node := parser.Start()
		parser.Bump()
		parseExpressionPrecedence(parser, stop, 19)
		return parser.Complete(node, syntax.PhpCloneExpression), true
	case isCast(parser):
		return parseCast(parser, stop), true
	default:
		return parsePrimary(parser, stop)
	}
}

func parsePrimary(
	parser *parsekit.Parser,
	stop map[syntax.Kind]struct{},
) (parsekit.CompletedMarker, bool) {
	switch {
	case parser.At(syntax.TkString):
		return parseString(parser), true
	case parser.At(syntax.TkNumber):
		return parseLeaf(parser, syntax.PhpNumber), true
	case parser.At(syntax.TkVariable):
		return parseLeaf(parser, syntax.PhpVariable), true
	case atKeyword(parser, "true", "false"):
		return parseLeaf(parser, syntax.PhpBoolean), true
	case atKeyword(parser, "null"):
		return parseLeaf(parser, syntax.PhpNull), true
	case atKeyword(parser, "static") &&
		(strings.EqualFold(nextNonTriviaText(parser), "function") ||
			strings.EqualFold(nextNonTriviaText(parser), "fn")):
		if strings.EqualFold(nextNonTriviaText(parser), "function") {
			return parseClosure(parser, true), true
		}
		return parseArrowFunction(parser, true, stop), true
	case atKeyword(parser, "function"):
		return parseClosure(parser, false), true
	case atKeyword(parser, "fn"):
		return parseArrowFunction(parser, false, stop), true
	case atKeyword(parser, "match"):
		return parseMatch(parser, stop), true
	case atKeyword(parser, "new"):
		return parseObjectCreation(parser), true
	case atKeyword(parser, "array") && nextNonTriviaKind(parser) == syntax.TkOpenParen:
		return parseOldArray(parser), true
	case isNameStart(parser):
		return parseName(parser), true
	case parser.At(syntax.TkOpenBracket):
		return parseArray(parser), true
	case parser.At(syntax.TkOpenParen):
		return parseParenthesized(parser), true
	case parser.At(syntax.TkOpenBrace):
		return parseBlock(parser), true
	}
	return parsekit.CompletedMarker{}, false
}

func binaryOperator(parser *parsekit.Parser) (precedence int, rightAssociative bool, assignment bool) {
	token := parser.PeekToken()
	if token == nil {
		return -1, false, false
	}
	text := strings.ToLower(token.Text())
	if token.Kind == syntax.TkEquals ||
		token.Kind == syntax.TkOperator && strings.HasSuffix(text, "=") &&
			text != "==" && text != "===" && text != "!=" && text != "!==" &&
			text != "<=" && text != ">=" && text != "<=>" {
		return assignmentPrecedence, true, true
	}
	switch text {
	case "??":
		return nullCoalescingPrecedence, true, false
	case "or":
		return logicalOrWordPrecedence, false, false
	case "xor":
		return logicalXorWordPrecedence, false, false
	case "and":
		return logicalAndWordPrecedence, false, false
	case "||":
		return logicalOrPrecedence, false, false
	case "&&":
		return logicalAndPrecedence, false, false
	case "|":
		return bitwiseOrPrecedence, false, false
	case "^":
		return bitwiseXorPrecedence, false, false
	case "&":
		return bitwiseAndPrecedence, false, false
	case "==", "!=", "===", "!==", "<>", "<=>":
		return equalityPrecedence, false, false
	case "<", ">", "<=", ">=":
		return comparisonPrecedence, false, false
	case "instanceof":
		return instanceofPrecedence, false, false
	case "<<", ">>":
		return shiftPrecedence, false, false
	case ".":
		return concatenationPrecedence, false, false
	case "+", "-":
		return additivePrecedence, false, false
	case "*", "/", "%":
		return multiplicativePrecedence, false, false
	case "**":
		return exponentiationPrecedence, true, false
	default:
		return -1, false, false
	}
}

// PHP's operator table is intentionally not a simple C-style precedence
// ladder. In particular, instanceof binds more tightly than !, while the
// word operators bind less tightly than assignment.
const (
	throwPrecedence = iota + 1
	logicalOrWordPrecedence
	logicalXorWordPrecedence
	logicalAndWordPrecedence
	printPrecedence
	yieldPrecedence
	yieldFromPrecedence
	assignmentPrecedence
	ternaryPrecedence
	nullCoalescingPrecedence
	logicalOrPrecedence
	logicalAndPrecedence
	bitwiseOrPrecedence
	bitwiseXorPrecedence
	bitwiseAndPrecedence
	equalityPrecedence
	comparisonPrecedence
	concatenationPrecedence
	shiftPrecedence
	additivePrecedence
	multiplicativePrecedence
	logicalNotPrecedence
	instanceofPrecedence
	tightPrefixPrecedence
	exponentiationPrecedence
)

func isPrefixOperator(parser *parsekit.Parser) bool {
	token := parser.PeekToken()
	if token == nil {
		return false
	}
	switch token.Text() {
	case "!", "~", "+", "-", "@", "++", "--":
		return true
	default:
		return atKeyword(parser, "include", "include_once", "require", "require_once", "print")
	}
}

func isCast(parser *parsekit.Parser) bool {
	if !parser.At(syntax.TkOpenParen) {
		return false
	}
	typeToken := nthNonTrivia(parser, 1)
	closeToken := nthNonTrivia(parser, 2)
	if typeToken == nil || closeToken == nil || closeToken.Kind != syntax.TkCloseParen {
		return false
	}
	switch strings.ToLower(typeToken.Text()) {
	case "array", "bool", "boolean", "double", "float", "int", "integer",
		"object", "real", "string", "unset", "binary":
		return true
	default:
		return false
	}
}

func parseCast(
	parser *parsekit.Parser,
	stop map[syntax.Kind]struct{},
) parsekit.CompletedMarker {
	node := parser.Start()
	parser.Bump()
	parser.Bump()
	parser.Expect(syntax.TkCloseParen, nil)
	parseExpressionPrecedence(parser, stop, tightPrefixPrecedence)
	return parser.Complete(node, syntax.PhpCastExpression)
}

func parseYield(
	parser *parsekit.Parser,
	stop map[syntax.Kind]struct{},
) parsekit.CompletedMarker {
	node := parser.Start()
	parser.Bump()
	if atKeyword(parser, "from") {
		parser.Bump()
		parseExpressionPrecedence(parser, stop, yieldFromPrecedence)
		return parser.Complete(node, syntax.PhpYieldExpression)
	}
	if parser.AtEnd() || atStop(parser, stop) || parser.At(syntax.TkSemicolon) {
		return parser.Complete(node, syntax.PhpYieldExpression)
	}
	parseExpression(parser, stopSet(syntax.TkArrow, syntax.TkSemicolon))
	if parser.At(syntax.TkArrow) {
		parser.Bump()
		parseExpression(parser, stop)
	}
	return parser.Complete(node, syntax.PhpYieldExpression)
}

func parseSuffixes(parser *parsekit.Parser, expression parsekit.CompletedMarker) parsekit.CompletedMarker {
	for {
		switch {
		case parser.At(syntax.TkObjectOperator), parser.At(syntax.TkNullsafeObjectOperator):
			member := parser.Precede(expression)
			parser.Bump()
			parseMemberName(parser)
			if parser.At(syntax.TkOpenParen) {
				parseArguments(parser)
				expression = parser.Complete(member, syntax.PhpMemberCall)
			} else {
				expression = parser.Complete(member, syntax.PhpMemberAccess)
			}
		case parser.At(syntax.TkScopeResolution):
			call := parser.Precede(expression)
			parser.Bump()
			parseMemberName(parser)
			if parser.At(syntax.TkOpenParen) {
				parseArguments(parser)
				expression = parser.Complete(call, syntax.PhpScopedCall)
			} else {
				// Keep the established member-access kind for compatibility;
				// the scope-resolution token distinguishes static access.
				expression = parser.Complete(call, syntax.PhpMemberAccess)
			}
		case parser.At(syntax.TkOpenParen):
			call := parser.Precede(expression)
			parseArguments(parser)
			expression = parser.Complete(call, syntax.PhpFunctionCall)
		case parser.At(syntax.TkOpenBracket):
			member := parser.Precede(expression)
			parseArrayAccess(parser)
			expression = parser.Complete(member, syntax.PhpArrayAccess)
		case parser.At(syntax.TkOperator) &&
			(tokenText(parser.PeekToken()) == "++" ||
				tokenText(parser.PeekToken()) == "--"):
			postfix := parser.Precede(expression)
			parser.Bump()
			expression = parser.Complete(postfix, syntax.PhpUnaryExpression)
		default:
			return expression
		}
	}
}

func parseMemberName(parser *parsekit.Parser) {
	if parser.At(syntax.TkIdentifier) ||
		parser.At(syntax.TkKeyword) ||
		parser.At(syntax.TkVariable) {
		parseName(parser)
		return
	}
	if !parser.At(syntax.TkOpenBrace) {
		return
	}
	parser.Bump()
	parseExpression(parser, stopSet(syntax.TkCloseBrace))
	parser.Expect(syntax.TkCloseBrace, nil)
}

func parseArrayAccess(parser *parsekit.Parser) {
	parser.Bump()
	if !parser.At(syntax.TkCloseBracket) {
		parseExpression(parser, stopSet(syntax.TkCloseBracket))
	}
	parser.Expect(syntax.TkCloseBracket, nil)
}

func parseArguments(parser *parsekit.Parser) parsekit.CompletedMarker {
	arguments := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkCloseParen) {
		position := parser.GetPos()
		argument := parser.Start()
		kind := syntax.PhpArgument
		if (parser.At(syntax.TkIdentifier) || parser.At(syntax.TkKeyword)) &&
			nextNonTriviaKind(parser) == syntax.TkColon {
			parseName(parser)
			parser.Expect(syntax.TkColon, nil)
			kind = syntax.PhpNamedArgument
		}
		if parser.At(syntax.TkEllipsis) || parser.At(syntax.TkAmpersand) {
			parser.Bump()
		}
		parseExpression(parser, stopSet(syntax.TkComma, syntax.TkCloseParen))
		parser.Complete(argument, kind)
		if parser.At(syntax.TkComma) {
			parser.Bump()
		}
		if parser.GetPos() == position {
			parser.Bump()
		}
	}
	parser.Expect(syntax.TkCloseParen, []syntax.Kind{syntax.TkSemicolon, syntax.TkCloseBrace})
	return parser.Complete(arguments, syntax.PhpArgumentList)
}

func parseArray(parser *parsekit.Parser) parsekit.CompletedMarker {
	array := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkCloseBracket) {
		position := parser.GetPos()
		item := parser.Start()
		if parser.At(syntax.TkEllipsis) {
			parser.Bump()
		}
		parseExpression(parser, stopSet(syntax.TkComma, syntax.TkCloseBracket, syntax.TkArrow))
		if parser.At(syntax.TkArrow) {
			parser.Bump()
			parseExpression(parser, stopSet(syntax.TkComma, syntax.TkCloseBracket))
		}
		parser.Complete(item, syntax.PhpArrayItem)
		if parser.At(syntax.TkComma) {
			parser.Bump()
		}
		if parser.GetPos() == position {
			parser.Bump()
		}
	}
	parser.Expect(syntax.TkCloseBracket, []syntax.Kind{syntax.TkSemicolon, syntax.TkCloseBrace})
	return parser.Complete(array, syntax.PhpArray)
}

func parseOldArray(parser *parsekit.Parser) parsekit.CompletedMarker {
	array := parser.Start()
	parser.Bump()
	if parser.At(syntax.TkOpenParen) {
		parser.Bump()
	}
	for !parser.AtEnd() && !parser.At(syntax.TkCloseParen) {
		position := parser.GetPos()
		item := parser.Start()
		if parser.At(syntax.TkEllipsis) {
			parser.Bump()
		}
		parseExpression(parser, stopSet(syntax.TkComma, syntax.TkCloseParen, syntax.TkArrow))
		if parser.At(syntax.TkArrow) {
			parser.Bump()
			parseExpression(parser, stopSet(syntax.TkComma, syntax.TkCloseParen))
		}
		parser.Complete(item, syntax.PhpArrayItem)
		if parser.At(syntax.TkComma) {
			parser.Bump()
		}
		if parser.GetPos() == position {
			parser.Bump()
		}
	}
	parser.Expect(syntax.TkCloseParen, []syntax.Kind{syntax.TkSemicolon, syntax.TkCloseBrace})
	return parser.Complete(array, syntax.PhpArray)
}

func parseObjectCreation(parser *parsekit.Parser) parsekit.CompletedMarker {
	object := parser.Start()
	parser.Bump()
	if atKeyword(parser, "class") {
		anonymous := parser.Start()
		parser.Bump()
		if parser.At(syntax.TkOpenParen) {
			parseArguments(parser)
		}
		for !parser.AtEnd() && !parser.At(syntax.TkOpenBrace) {
			switch {
			case atKeyword(parser, "extends"):
				parseNameClause(parser, syntax.PhpExtendsClause)
			case atKeyword(parser, "implements"):
				parseNameClause(parser, syntax.PhpImplementsClause)
			default:
				parser.Bump()
			}
		}
		if parser.At(syntax.TkOpenBrace) {
			parseClassBody(parser)
		}
		parser.Complete(anonymous, syntax.PhpAnonymousClass)
		return parser.Complete(object, syntax.PhpObjectCreation)
	}
	if isNameStart(parser) {
		parseName(parser)
	}
	if parser.At(syntax.TkOpenParen) {
		parseArguments(parser)
	}
	return parser.Complete(object, syntax.PhpObjectCreation)
}

func parseClosure(parser *parsekit.Parser, hasStatic bool) parsekit.CompletedMarker {
	node := parser.Start()
	if hasStatic {
		parser.Bump()
	}
	parser.Bump()
	if parser.At(syntax.TkAmpersand) {
		parser.Bump()
	}
	if parser.At(syntax.TkOpenParen) {
		parseParameters(parser)
	}
	if atKeyword(parser, "use") {
		parser.Bump()
		if parser.At(syntax.TkOpenParen) {
			parser.Bump()
			for !parser.AtEnd() && !parser.At(syntax.TkCloseParen) {
				if parser.At(syntax.TkAmpersand) {
					parser.Bump()
				}
				if parser.At(syntax.TkVariable) {
					parseLeaf(parser, syntax.PhpVariable)
				} else {
					parser.Bump()
				}
				if parser.At(syntax.TkComma) {
					parser.Bump()
				}
			}
			parser.Expect(syntax.TkCloseParen, []syntax.Kind{syntax.TkColon, syntax.TkOpenBrace})
		}
	}
	if parser.At(syntax.TkColon) {
		parser.Bump()
		parseTypeExpression(parser, stopSet(syntax.TkOpenBrace))
	}
	if parser.At(syntax.TkOpenBrace) {
		parseBlock(parser)
	}
	return parser.Complete(node, syntax.PhpClosure)
}

func parseArrowFunction(
	parser *parsekit.Parser,
	hasStatic bool,
	stop map[syntax.Kind]struct{},
) parsekit.CompletedMarker {
	node := parser.Start()
	if hasStatic {
		parser.Bump()
	}
	parser.Bump()
	if parser.At(syntax.TkAmpersand) {
		parser.Bump()
	}
	if parser.At(syntax.TkOpenParen) {
		parseParameters(parser)
	}
	if parser.At(syntax.TkColon) {
		parser.Bump()
		parseTypeExpression(parser, stopSet(syntax.TkArrow))
	}
	parser.Expect(syntax.TkArrow, nil)
	parseExpression(parser, stop)
	return parser.Complete(node, syntax.PhpArrowFunction)
}

func parseMatch(
	parser *parsekit.Parser,
	stop map[syntax.Kind]struct{},
) parsekit.CompletedMarker {
	node := parser.Start()
	parser.Bump()
	if parser.At(syntax.TkOpenParen) {
		parseParenthesized(parser)
	}
	if !parser.At(syntax.TkOpenBrace) {
		return parser.Complete(node, syntax.PhpMatchExpression)
	}
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkCloseBrace) {
		position := parser.GetPos()
		arm := parser.Start()
		if atKeyword(parser, "default") {
			parser.Bump()
		} else {
			parseExpressionList(parser, syntax.TkArrow)
		}
		parser.Expect(syntax.TkArrow, []syntax.Kind{syntax.TkComma, syntax.TkCloseBrace})
		parseExpression(parser, stopSet(syntax.TkComma, syntax.TkCloseBrace))
		parser.Complete(arm, syntax.PhpMatchArm)
		if parser.At(syntax.TkComma) {
			parser.Bump()
		}
		if parser.GetPos() == position {
			parser.Bump()
		}
	}
	parser.Expect(syntax.TkCloseBrace, nil)
	_ = stop
	return parser.Complete(node, syntax.PhpMatchExpression)
}

func parseParenthesized(parser *parsekit.Parser) parsekit.CompletedMarker {
	node := parser.Start()
	parser.Bump()
	parseExpression(parser, stopSet(syntax.TkCloseParen))
	parser.Expect(syntax.TkCloseParen, []syntax.Kind{syntax.TkSemicolon, syntax.TkOpenBrace})
	return parser.Complete(node, syntax.PhpParenthesized)
}

func parseBlock(parser *parsekit.Parser) parsekit.CompletedMarker {
	block := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkCloseBrace) {
		position := parser.GetPos()
		parseStatement(parser, false)
		if parser.GetPos() == position {
			parser.Bump()
		}
	}
	parser.Expect(syntax.TkCloseBrace, nil)
	return parser.Complete(block, syntax.PhpBlock)
}

func parseAttributeGroups(parser *parsekit.Parser) {
	for parser.At(syntax.TkAttributeOpen) {
		group := parser.Start()
		parser.Bump()
		for !parser.AtEnd() && !parser.At(syntax.TkCloseBracket) {
			if parser.At(syntax.TkComma) {
				parser.Bump()
				continue
			}
			attribute := parser.Start()
			if isNameStart(parser) {
				parseName(parser)
			}
			if parser.At(syntax.TkOpenParen) {
				parseArguments(parser)
			}
			parser.Complete(attribute, syntax.PhpAttribute)
			if parser.At(syntax.TkComma) {
				parser.Bump()
			}
		}
		parser.Expect(syntax.TkCloseBracket, nil)
		parser.Complete(group, syntax.PhpAttributeGroup)
	}
}

func parseName(parser *parsekit.Parser) parsekit.CompletedMarker {
	name := parser.Start()
	if parser.At(syntax.TkBackslash) {
		parser.Bump()
	}
	for {
		if parser.At(syntax.TkIdentifier) || parser.At(syntax.TkKeyword) || parser.At(syntax.TkVariable) {
			parser.Bump()
		} else {
			break
		}
		if parser.At(syntax.TkBackslash) {
			parser.Bump()
			continue
		}
		break
	}
	return parser.Complete(name, syntax.PhpName)
}

func parseString(parser *parsekit.Parser) parsekit.CompletedMarker {
	node := parser.Start()
	token := parser.Bump()
	if !closedString(token.Text()) {
		parser.AddError(parsekit.NewErrorBuilder("closing quote").AtToken(token))
	}
	return parser.Complete(node, syntax.PhpString)
}

func parseLeaf(parser *parsekit.Parser, kind syntax.Kind) parsekit.CompletedMarker {
	node := parser.Start()
	parser.Bump()
	return parser.Complete(node, kind)
}

func parseTypeExpression(
	parser *parsekit.Parser,
	stop map[syntax.Kind]struct{},
) (parsekit.CompletedMarker, bool) {
	left, ok := parseIntersectionType(parser, stop)
	if !ok {
		return parsekit.CompletedMarker{}, false
	}
	for parser.At(syntax.TkPipe) && !atStop(parser, stop) {
		union := parser.Precede(left)
		parser.Bump()
		if _, parsed := parseIntersectionType(parser, stop); !parsed {
			parser.AddError(parsekit.NewErrorBuilder("type"))
		}
		left = parser.Complete(union, syntax.PhpUnionType)
	}
	return left, true
}

func parseIntersectionType(
	parser *parsekit.Parser,
	stop map[syntax.Kind]struct{},
) (parsekit.CompletedMarker, bool) {
	left, ok := parseTypePrimary(parser, stop)
	if !ok {
		return parsekit.CompletedMarker{}, false
	}
	for parser.At(syntax.TkAmpersand) && nextNonTriviaKind(parser) != syntax.TkVariable &&
		nextNonTriviaKind(parser) != syntax.TkEllipsis &&
		!atStop(parser, stop) {
		intersection := parser.Precede(left)
		parser.Bump()
		if _, parsed := parseTypePrimary(parser, stop); !parsed {
			parser.AddError(parsekit.NewErrorBuilder("type"))
		}
		left = parser.Complete(intersection, syntax.PhpIntersectionType)
	}
	return left, true
}

func parseTypePrimary(
	parser *parsekit.Parser,
	stop map[syntax.Kind]struct{},
) (parsekit.CompletedMarker, bool) {
	if parser.AtEnd() || atStop(parser, stop) {
		return parsekit.CompletedMarker{}, false
	}
	if parser.At(syntax.TkQuestion) {
		nullable := parser.Start()
		parser.Bump()
		if _, ok := parseTypePrimary(parser, stop); !ok {
			parser.AddError(parsekit.NewErrorBuilder("type"))
		}
		return parser.Complete(nullable, syntax.PhpNullableType), true
	}
	if parser.At(syntax.TkOpenParen) {
		group := parser.Start()
		parser.Bump()
		parseTypeExpression(parser, stopSet(syntax.TkCloseParen))
		parser.Expect(syntax.TkCloseParen, nil)
		return parser.Complete(group, syntax.PhpType), true
	}
	if isNameStart(parser) && !parser.At(syntax.TkVariable) {
		node := parser.Start()
		parseName(parser)
		return parser.Complete(node, syntax.PhpType), true
	}
	return parsekit.CompletedMarker{}, false
}

func consumeModifiers(parser *parsekit.Parser) {
	for atKeyword(parser, "abstract", "final", "public", "protected", "private",
		"static", "readonly", "var") {
		modifier := strings.ToLower(parser.PeekToken().Text())
		parser.Bump()
		if (modifier == "public" || modifier == "protected" || modifier == "private") &&
			parser.At(syntax.TkOpenParen) &&
			strings.EqualFold(tokenText(nthNonTrivia(parser, 1)), "set") &&
			nthNonTrivia(parser, 2) != nil &&
			nthNonTrivia(parser, 2).Kind == syntax.TkCloseParen {
			parser.Bump()
			parser.Bump()
			parser.Bump()
		}
	}
}

func startsClassDeclaration(parser *parsekit.Parser) bool {
	return tokenAfterPrefix(parser, true, "class", "interface", "trait", "enum")
}

func startsMethodDeclaration(parser *parsekit.Parser) bool {
	return tokenAfterPrefix(parser, true, "function")
}

func startsFunctionDeclaration(parser *parsekit.Parser) bool {
	for offset := 0; ; offset++ {
		token := nthNonTrivia(parser, offset)
		if token == nil {
			return false
		}
		if token.Kind == syntax.TkAttributeOpen {
			depth := 1
			for depth > 0 {
				offset++
				token = nthNonTrivia(parser, offset)
				if token == nil {
					return false
				}
				switch token.Kind {
				case syntax.TkOpenBracket, syntax.TkAttributeOpen:
					depth++
				case syntax.TkCloseBracket:
					depth--
				}
			}
			continue
		}
		if !strings.EqualFold(token.Text(), "function") {
			return false
		}
		offset++
		token = nthNonTrivia(parser, offset)
		if token != nil && token.Kind == syntax.TkAmpersand {
			offset++
			token = nthNonTrivia(parser, offset)
		}
		return token != nil &&
			(token.Kind == syntax.TkIdentifier || token.Kind == syntax.TkKeyword)
	}
}

func typedConstStarts(parser *parsekit.Parser) bool {
	previousName := false
	for offset := 0; ; offset++ {
		token := nthNonTrivia(parser, offset)
		if token == nil {
			return false
		}
		if token.Kind == syntax.TkAttributeOpen {
			depth := 1
			for depth > 0 {
				offset++
				token = nthNonTrivia(parser, offset)
				if token == nil {
					return false
				}
				switch token.Kind {
				case syntax.TkOpenBracket, syntax.TkAttributeOpen:
					depth++
				case syntax.TkCloseBracket:
					depth--
				}
			}
			continue
		}
		switch token.Kind {
		case syntax.TkEquals, syntax.TkComma, syntax.TkSemicolon, syntax.TkCloseBrace:
			return false
		case syntax.TkBackslash:
			previousName = false
		case syntax.TkIdentifier, syntax.TkKeyword:
			if previousName {
				return true
			}
			previousName = true
		default:
			previousName = false
		}
	}
}

func startsClassConstDeclaration(parser *parsekit.Parser) bool {
	return tokenAfterPrefix(parser, true, "const")
}

func startsPropertyDeclaration(parser *parsekit.Parser) bool {
	if startsMethodDeclaration(parser) || startsClassDeclaration(parser) ||
		startsClassConstDeclaration(parser) {
		return false
	}
	// Class-level declarations with a variable before the next statement
	// delimiter are properties, including typed and multi-property forms.
	for offset := 0; ; offset++ {
		token := nthNonTrivia(parser, offset)
		if token == nil {
			return false
		}
		if token.Kind == syntax.TkAttributeOpen {
			depth := 1
			for depth > 0 {
				offset++
				token = nthNonTrivia(parser, offset)
				if token == nil {
					return false
				}
				switch token.Kind {
				case syntax.TkOpenBracket, syntax.TkAttributeOpen:
					depth++
				case syntax.TkCloseBracket:
					depth--
				}
			}
			continue
		}
		switch token.Kind {
		case syntax.TkVariable:
			return true
		case syntax.TkSemicolon:
			return false
		case syntax.TkOpenParen:
			previous := nthNonTrivia(parser, offset-1)
			next := nthNonTrivia(parser, offset+1)
			close := nthNonTrivia(parser, offset+2)
			if previous != nil && isVisibility(previous.Text()) &&
				next != nil && strings.EqualFold(next.Text(), "set") &&
				close != nil && close.Kind == syntax.TkCloseParen {
				offset += 2
				continue
			}
			depth := 1
			for depth > 0 {
				offset++
				token = nthNonTrivia(parser, offset)
				if token == nil {
					return false
				}
				switch token.Kind {
				case syntax.TkOpenParen:
					depth++
				case syntax.TkCloseParen:
					depth--
				case syntax.TkSemicolon, syntax.TkOpenBrace,
					syntax.TkCloseBrace:
					return false
				}
			}
			continue
		case syntax.TkOpenBrace, syntax.TkCloseBrace:
			return false
		}
	}
}

func tokenAfterPrefix(parser *parsekit.Parser, attributes bool, words ...string) bool {
	for offset := 0; ; offset++ {
		token := nthNonTrivia(parser, offset)
		if token == nil {
			return false
		}
		if attributes && token.Kind == syntax.TkAttributeOpen {
			depth := 1
			for depth > 0 {
				offset++
				token = nthNonTrivia(parser, offset)
				if token == nil {
					return false
				}
				switch token.Kind {
				case syntax.TkOpenBracket, syntax.TkAttributeOpen:
					depth++
				case syntax.TkCloseBracket:
					depth--
				}
			}
			continue
		}
		if token.Kind == syntax.TkKeyword && isModifier(token.Text()) {
			continue
		}
		for _, word := range words {
			if strings.EqualFold(token.Text(), word) {
				return true
			}
		}
		return false
	}
}

func nthNonTrivia(parser *parsekit.Parser, ordinal int) *parsekit.Token {
	seen := 0
	for raw := 0; ; raw++ {
		token := parser.PeekNthToken(raw)
		if token == nil {
			return nil
		}
		if token.Kind.IsTrivia() {
			continue
		}
		if seen == ordinal {
			return token
		}
		seen++
	}
}

func nextNonTriviaKind(parser *parsekit.Parser) syntax.Kind {
	token := nthNonTrivia(parser, 1)
	if token == nil {
		return syntax.KindNone
	}
	return token.Kind
}

func nextNonTriviaText(parser *parsekit.Parser) string {
	token := nthNonTrivia(parser, 1)
	if token == nil {
		return ""
	}
	return token.Text()
}

func staticStartsExpression(parser *parsekit.Parser) bool {
	if !atKeyword(parser, "static") {
		return false
	}
	return nextNonTriviaKind(parser) == syntax.TkScopeResolution ||
		strings.EqualFold(nextNonTriviaText(parser), "function") ||
		strings.EqualFold(nextNonTriviaText(parser), "fn")
}

func atKeyword(parser *parsekit.Parser, words ...string) bool {
	if !parser.At(syntax.TkKeyword) {
		return false
	}
	text := parser.PeekToken().Text()
	for _, word := range words {
		if strings.EqualFold(text, word) {
			return true
		}
	}
	return false
}

func atWord(parser *parsekit.Parser, words ...string) bool {
	token := parser.PeekToken()
	if token == nil || token.Kind != syntax.TkKeyword && token.Kind != syntax.TkIdentifier {
		return false
	}
	for _, word := range words {
		if strings.EqualFold(token.Text(), word) {
			return true
		}
	}
	return false
}

func tokenText(token *parsekit.Token) string {
	if token == nil {
		return ""
	}
	return token.Text()
}

func isModifier(text string) bool {
	switch strings.ToLower(text) {
	case "abstract", "final", "public", "protected", "private", "static", "readonly", "var":
		return true
	}
	return false
}

func isVisibility(text string) bool {
	return strings.EqualFold(text, "public") ||
		strings.EqualFold(text, "protected") ||
		strings.EqualFold(text, "private")
}

func isNameStart(parser *parsekit.Parser) bool {
	return parser.At(syntax.TkIdentifier) || parser.At(syntax.TkKeyword) ||
		parser.At(syntax.TkBackslash) || parser.At(syntax.TkVariable)
}

func atStop(parser *parsekit.Parser, stop map[syntax.Kind]struct{}) bool {
	kind, ok := parser.Peek()
	if !ok {
		return true
	}
	_, found := stop[kind]
	return found
}

func stopSet(kinds ...syntax.Kind) map[syntax.Kind]struct{} {
	result := make(map[syntax.Kind]struct{}, len(kinds))
	for _, kind := range kinds {
		result[kind] = struct{}{}
	}
	return result
}

func closedString(text string) bool {
	if strings.HasPrefix(text, "<<<") {
		headerEnd := strings.IndexAny(text, "\r\n")
		if headerEnd < 0 {
			return false
		}
		label := strings.TrimSpace(text[3:headerEnd])
		if len(label) >= 2 &&
			(label[0] == '\'' || label[0] == '"') &&
			label[len(label)-1] == label[0] {
			label = label[1 : len(label)-1]
		}
		return label != "" && strings.HasSuffix(strings.TrimSpace(text), label)
	}
	if len(text) < 2 {
		return false
	}
	quote := text[0]
	return (quote == '\'' || quote == '"' || quote == '`') && text[len(text)-1] == quote
}
