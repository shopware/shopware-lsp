package parser

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/javascript/lexer"
	"github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
)

type Error = parsekit.Error

type Result struct {
	Tree   *syntax.Tree
	Errors []Error
}

func Parse(source string) Result {
	tokens := lexer.LexInto(
		source,
		parsekit.AcquireTokenBuffer(len(source)/3+1),
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
		parseStatement(parser)
		if parser.GetPos() == position {
			parser.AddError(parsekit.NewErrorBuilder("JavaScript statement"))
			parser.Bump()
		}
	}

	parser.Complete(root, syntax.JsProgram)
	tree, errors := parser.Finish(source)
	return Result{Tree: tree, Errors: errors}
}

func parseStatement(parser *parsekit.Parser) {
	switch {
	case atKeyword(parser, "import"):
		parseImport(parser)
	case atKeyword(parser, "export") && nextNonTriviaText(parser) == "default":
		parseExportDefault(parser)
	case atKeyword(parser, "const", "let", "var"):
		parseVariableDeclaration(parser)
	case atKeyword(parser, "if"):
		parseIfStatement(parser)
	case atKeyword(parser, "for", "while", "switch", "with"):
		parseConditionalBlock(parser)
	case atKeyword(parser, "try"):
		parseTryStatement(parser)
	case atKeyword(parser, "do"):
		parseDoStatement(parser)
	case atKeyword(parser, "return"):
		parseReturnStatement(parser)
	case parser.At(syntax.TkCloseBrace):
		parser.AddError(parsekit.NewErrorBuilder("JavaScript statement"))
		parser.Bump()
	default:
		statement := parser.Start()
		parseExpression(parser, stopSet(syntax.TkSemicolon, syntax.TkCloseBrace))
		if parser.At(syntax.TkSemicolon) {
			parser.Bump()
		}
		parser.Complete(statement, syntax.JsExpressionStatement)
	}
}

func parseReturnStatement(parser *parsekit.Parser) {
	statement := parser.Start()
	parser.Bump()
	if !parser.AtEnd() && !parser.At(syntax.TkSemicolon) &&
		!parser.At(syntax.TkCloseBrace) {
		parseExpression(parser, stopSet(syntax.TkSemicolon, syntax.TkCloseBrace))
	}
	if parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
	parser.Complete(statement, syntax.JsReturnStatement)
}

// Component definitions contain real method bodies, not only flat fixture
// expressions. Parse the brace-owning control statements explicitly so a
// nested if/for/try block cannot be mistaken for the end of the surrounding
// object method. The expressions inside remain regular CST expressions and
// therefore keep this.* and registry calls queryable.
func parseIfStatement(parser *parsekit.Parser) {
	statement := parser.Start()
	parser.Bump()
	parseControlCondition(parser)
	parseStatementBody(parser)
	if atKeyword(parser, "else") {
		parser.Bump()
		if atKeyword(parser, "if") {
			parseIfStatement(parser)
		} else {
			parseStatementBody(parser)
		}
	}
	parser.Complete(statement, syntax.JsExpressionStatement)
}

func parseConditionalBlock(parser *parsekit.Parser) {
	statement := parser.Start()
	parser.Bump()
	parseControlCondition(parser)
	parseStatementBody(parser)
	parser.Complete(statement, syntax.JsExpressionStatement)
}

func parseTryStatement(parser *parsekit.Parser) {
	statement := parser.Start()
	parser.Bump()
	parseStatementBody(parser)
	if atKeyword(parser, "catch") {
		parser.Bump()
		parseControlCondition(parser)
		parseStatementBody(parser)
	}
	if atKeyword(parser, "finally") {
		parser.Bump()
		parseStatementBody(parser)
	}
	parser.Complete(statement, syntax.JsExpressionStatement)
}

func parseDoStatement(parser *parsekit.Parser) {
	statement := parser.Start()
	parser.Bump()
	parseStatementBody(parser)
	if atKeyword(parser, "while") {
		parser.Bump()
		parseControlCondition(parser)
	}
	if parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
	parser.Complete(statement, syntax.JsExpressionStatement)
}

func parseControlCondition(parser *parsekit.Parser) {
	if parser.At(syntax.TkOpenParen) {
		parseParenthesized(parser)
	}
}

func parseStatementBody(parser *parsekit.Parser) {
	if parser.At(syntax.TkOpenBrace) {
		parseBlock(parser)
		return
	}
	if !parser.AtEnd() && !parser.At(syntax.TkCloseBrace) {
		parseStatement(parser)
	}
}

func parseImport(parser *parsekit.Parser) {
	statement := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkSemicolon) {
		if parser.At(syntax.TkString) || parser.At(syntax.TkTemplate) {
			parseString(parser)
		} else {
			parser.Bump()
		}
	}
	if parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
	parser.Complete(statement, syntax.JsImportStatement)
}

func parseExportDefault(parser *parsekit.Parser) {
	statement := parser.Start()
	parser.Bump()
	if atKeyword(parser, "default") {
		parser.Bump()
	}
	parseExpression(parser, stopSet(syntax.TkSemicolon))
	if parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
	parser.Complete(statement, syntax.JsExportDefault)
}

func parseVariableDeclaration(parser *parsekit.Parser) {
	declaration := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkSemicolon) && !parser.At(syntax.TkCloseBrace) {
		position := parser.GetPos()
		parseExpression(parser, stopSet(syntax.TkComma, syntax.TkSemicolon, syntax.TkCloseBrace))
		if parser.At(syntax.TkComma) {
			parser.Bump()
		}
		if parser.GetPos() == position {
			parser.Bump()
		}
	}
	if parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
	parser.Complete(declaration, syntax.JsVariableDeclaration)
}

func parseExpression(parser *parsekit.Parser, stop map[syntax.Kind]struct{}) (parsekit.CompletedMarker, bool) {
	if parser.AtEnd() || atStop(parser, stop) {
		return parsekit.CompletedMarker{}, false
	}

	left, ok := parsePrimary(parser)
	if !ok {
		errorMarker := parser.Start()
		parser.Bump()
		left = parser.Complete(errorMarker, syntax.Error)
	}
	left = parseSuffixes(parser, left)

	for !parser.AtEnd() && !atStop(parser, stop) {
		switch {
		case parser.At(syntax.TkArrow):
			arrow := parser.Precede(left)
			parser.Bump()
			if parser.At(syntax.TkOpenBrace) {
				parseBlock(parser)
			} else {
				parseExpression(parser, stop)
			}
			left = parser.Complete(arrow, syntax.JsArrowFunction)
			return left, true
		case parser.At(syntax.TkColon):
			// TypeScript arrow functions place the return annotation between
			// their parameter list and =>, for example
			// `state: (): State => ({ ... })`. Parameter annotations reach this
			// branch as well; consuming their type up to the enclosing `)` keeps
			// the lightweight CST lossless without requiring a separate TS AST.
			arrow := parser.Precede(left)
			parser.Bump()
			parseTypeAnnotation(parser)
			if !parser.At(syntax.TkArrow) {
				left = parser.Complete(arrow, syntax.JsIdentifier)
				continue
			}
			parser.Bump()
			if parser.At(syntax.TkOpenBrace) {
				parseBlock(parser)
			} else {
				parseExpression(parser, stop)
			}
			left = parser.Complete(arrow, syntax.JsArrowFunction)
			return left, true
		case parser.At(syntax.TkQuestion):
			parser.Bump()
			parseExpression(parser, stopSet(syntax.TkColon))
			if parser.At(syntax.TkColon) {
				parser.Bump()
				parseExpression(parser, stop)
			}
		case parser.At(syntax.TkOperator):
			parser.Bump()
			if right, parsed := parsePrimary(parser); parsed {
				left = parseSuffixes(parser, right)
			}
		case atKeyword(parser, "as", "satisfies"):
			// TypeScript assertions are part of real Administration component
			// values (for example [] as Product[]). Keep their tokens in the
			// surrounding lossless expression while consuming nested type
			// syntax so the outer object parser does not mistake it for fields.
			parser.Bump()
			parseTypeExpression(parser, stop)
		default:
			return left, true
		}
	}
	return left, true
}

// parseTypeAnnotation consumes a TypeScript value/parameter/arrow return
// annotation. Unlike an `as` expression it must stop before a variable's `=`
// initializer, the enclosing parameter delimiter, or the arrow token.
func parseTypeAnnotation(parser *parsekit.Parser) {
	var round, square, curly, angle int
	for !parser.AtEnd() {
		kind := parser.PeekToken().Kind
		text := parser.PeekToken().Text()
		if round == 0 && square == 0 && curly == 0 && angle == 0 {
			if kind == syntax.TkArrow || kind == syntax.TkComma ||
				kind == syntax.TkCloseParen || kind == syntax.TkSemicolon ||
				kind == syntax.TkCloseBrace ||
				kind == syntax.TkOperator && text == "=" {
				return
			}
		}
		switch kind {
		case syntax.TkOpenParen:
			round++
		case syntax.TkCloseParen:
			if round == 0 {
				return
			}
			round--
		case syntax.TkOpenBracket:
			square++
		case syntax.TkCloseBracket:
			if square == 0 {
				return
			}
			square--
		case syntax.TkOpenBrace:
			curly++
		case syntax.TkCloseBrace:
			if curly == 0 {
				return
			}
			curly--
		case syntax.TkOperator:
			angle += strings.Count(text, "<")
			angle -= strings.Count(text, ">")
			if angle < 0 {
				angle = 0
			}
		}
		parser.Bump()
	}
}

func parseTypeExpression(
	parser *parsekit.Parser,
	stop map[syntax.Kind]struct{},
) {
	var round, square, curly, angle int
	for !parser.AtEnd() {
		kind := parser.PeekToken().Kind
		text := parser.PeekToken().Text()
		if round == 0 && square == 0 && curly == 0 && angle == 0 &&
			atStop(parser, stop) {
			return
		}
		switch kind {
		case syntax.TkOpenParen:
			round++
		case syntax.TkCloseParen:
			if round == 0 {
				return
			}
			round--
		case syntax.TkOpenBracket:
			square++
		case syntax.TkCloseBracket:
			if square == 0 {
				return
			}
			square--
		case syntax.TkOpenBrace:
			curly++
		case syntax.TkCloseBrace:
			if curly == 0 {
				return
			}
			curly--
		case syntax.TkOperator:
			angle += strings.Count(text, "<")
			angle -= strings.Count(text, ">")
			if angle < 0 {
				angle = 0
			}
		}
		parser.Bump()
	}
}

func parsePrimary(parser *parsekit.Parser) (parsekit.CompletedMarker, bool) {
	switch {
	case parser.At(syntax.TkString), parser.At(syntax.TkTemplate):
		return parseString(parser), true
	case parser.At(syntax.TkNumber):
		return parseLeaf(parser, syntax.JsNumber), true
	case parser.At(syntax.TkIdentifier):
		return parseLeaf(parser, syntax.JsIdentifier), true
	case atKeyword(parser, "true", "false"):
		return parseLeaf(parser, syntax.JsBoolean), true
	case atKeyword(parser, "null", "undefined"):
		return parseLeaf(parser, syntax.JsNull), true
	case atKeyword(parser, "function"):
		return parseFunction(parser), true
	case atKeyword(parser, "class"):
		return parseClassLike(parser), true
	case atKeyword(parser, "await", "new", "typeof", "void", "delete", "throw", "yield"):
		return parseUnary(parser), true
	case parser.At(syntax.TkOperator) && isJavaScriptPrefixOperator(parser.PeekToken().Text()):
		return parseUnary(parser), true
	case atKeyword(parser, "async", "return", "this", "super", "import"):
		return parseLeaf(parser, syntax.JsIdentifier), true
	case parser.At(syntax.TkOpenBrace):
		return parseObject(parser), true
	case parser.At(syntax.TkOpenBracket):
		return parseArray(parser), true
	case parser.At(syntax.TkOpenParen):
		return parseParenthesized(parser), true
	}
	return parsekit.CompletedMarker{}, false
}

func parseUnary(parser *parsekit.Parser) parsekit.CompletedMarker {
	unary := parser.Start()
	parser.Bump()
	operand, parsed := parsePrimary(parser)
	if parsed {
		parseSuffixes(parser, operand)
	} else {
		parser.AddError(parsekit.NewErrorBuilder("unary expression operand"))
	}
	return parser.Complete(unary, syntax.JsUnaryExpression)
}

func isJavaScriptPrefixOperator(value string) bool {
	switch value {
	case "!", "~", "+", "-", "++", "--":
		return true
	default:
		return false
	}
}

func parseSuffixes(parser *parsekit.Parser, expression parsekit.CompletedMarker) parsekit.CompletedMarker {
	for {
		switch {
		case parser.At(syntax.TkDot) || parser.At(syntax.TkOptionalChain):
			member := parser.Precede(expression)
			parser.Bump()
			if parser.At(syntax.TkIdentifier) || parser.At(syntax.TkKeyword) {
				parseLeaf(parser, syntax.JsIdentifier)
			} else {
				parser.AddError(parsekit.NewErrorBuilder("property name"))
			}
			expression = parser.Complete(member, syntax.JsMemberExpression)
		case parser.At(syntax.TkOpenBracket):
			member := parser.Precede(expression)
			computed := parser.Start()
			parser.Bump()
			parseExpression(parser, stopSet(syntax.TkCloseBracket))
			parser.Expect(syntax.TkCloseBracket, nil)
			parser.Complete(computed, syntax.JsComputed)
			expression = parser.Complete(member, syntax.JsMemberExpression)
		case parser.At(syntax.TkOpenParen):
			call := parser.Precede(expression)
			parseArguments(parser)
			expression = parser.Complete(call, syntax.JsCallExpression)
		default:
			return expression
		}
	}
}

func parseArguments(parser *parsekit.Parser) parsekit.CompletedMarker {
	arguments := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkCloseParen) {
		position := parser.GetPos()
		argument := parser.Start()
		parseExpression(parser, stopSet(syntax.TkComma, syntax.TkCloseParen))
		parser.Complete(argument, syntax.JsArgument)
		if parser.At(syntax.TkComma) {
			parser.Bump()
		}
		if parser.GetPos() == position {
			parser.Bump()
		}
	}
	parser.Expect(syntax.TkCloseParen, []syntax.Kind{
		syntax.TkSemicolon,
		syntax.TkCloseBrace,
	})
	return parser.Complete(arguments, syntax.JsArgumentList)
}

func parseObject(parser *parsekit.Parser) parsekit.CompletedMarker {
	object := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkCloseBrace) {
		if parser.At(syntax.TkComma) {
			parser.Bump()
			continue
		}
		position := parser.GetPos()
		parseObjectMember(parser)
		if parser.At(syntax.TkComma) || parser.At(syntax.TkSemicolon) {
			parser.Bump()
		}
		if parser.GetPos() == position {
			parser.Bump()
		}
	}
	parser.Expect(syntax.TkCloseBrace, []syntax.Kind{syntax.TkSemicolon})
	return parser.Complete(object, syntax.JsObject)
}

func parseObjectMember(parser *parsekit.Parser) {
	member := parser.Start()

	if parser.At(syntax.TkOperator) && parser.PeekToken().Text() == "..." {
		parser.Bump()
		parseExpression(parser, stopSet(syntax.TkComma, syntax.TkCloseBrace))
		parser.Complete(member, syntax.JsProperty)
		return
	}

	if atKeyword(parser, "async", "get", "set", "static") &&
		objectModifierHasName(parser) {
		// Keep modifiers as tokens rather than identifier nodes so PropertyName
		// resolves the actual method name.
		parser.Bump()
		if parser.At(syntax.TkOperator) && parser.PeekToken().Text() == "*" {
			parser.Bump()
		}
	}

	if parser.At(syntax.TkIdentifier) || parser.At(syntax.TkKeyword) {
		parseLeaf(parser, syntax.JsIdentifier)
	} else if parser.At(syntax.TkString) || parser.At(syntax.TkTemplate) {
		parseString(parser)
	} else if parser.At(syntax.TkOpenBracket) {
		computed := parser.Start()
		parser.Bump()
		parseExpression(parser, stopSet(syntax.TkCloseBracket))
		parser.Expect(syntax.TkCloseBracket, nil)
		parser.Complete(computed, syntax.JsComputed)
	} else {
		parser.AddError(parsekit.NewErrorBuilder("object property"))
		parser.Bump()
		parser.Complete(member, syntax.Error)
		return
	}

	switch {
	case parser.At(syntax.TkColon):
		parser.Bump()
		parseExpression(parser, stopSet(syntax.TkComma, syntax.TkCloseBrace))
		parser.Complete(member, syntax.JsProperty)
	case parser.At(syntax.TkOpenParen):
		parseArguments(parser)
		parseReturnType(parser)
		if parser.At(syntax.TkOpenBrace) {
			parseBlock(parser)
		}
		parser.Complete(member, syntax.JsMethod)
	default:
		parser.Complete(member, syntax.JsProperty)
	}
}

func objectModifierHasName(parser *parsekit.Parser) bool {
	kind, ok := parser.PeekNextNonTriviaKind()
	return ok && (kind == syntax.TkIdentifier || kind == syntax.TkKeyword ||
		kind == syntax.TkString || kind == syntax.TkTemplate ||
		kind == syntax.TkOpenBracket || kind == syntax.TkOperator)
}

func parseReturnType(parser *parsekit.Parser) {
	if !parser.At(syntax.TkColon) {
		return
	}
	parser.Bump()
	// A method return annotation ends at its body. Consuming the type tokens
	// here keeps the following brace attached to the method instead of letting
	// the outer object parser interpret it as a new object literal.
	sawTypeToken := false
	angleDepth := 0
	previousOperator := ""
	for !parser.AtEnd() && !parser.At(syntax.TkComma) &&
		!parser.At(syntax.TkCloseBrace) {
		if parser.At(syntax.TkOpenBrace) {
			// A leading object literal, an intersection branch, or an object
			// nested in a generic belongs to the return type. The next top-level
			// brace is the actual method body.
			if !sawTypeToken || angleDepth > 0 || previousOperator == "|" ||
				previousOperator == "&" {
				consumeTypeObject(parser)
				sawTypeToken = true
				previousOperator = ""
				continue
			}
			return
		}
		token := parser.Bump()
		sawTypeToken = true
		previousOperator = ""
		if token.Kind == syntax.TkOperator {
			switch token.Text() {
			case "<":
				angleDepth++
			case ">":
				if angleDepth > 0 {
					angleDepth--
				}
			case "|", "&":
				previousOperator = token.Text()
			}
		}
	}
}

func consumeTypeObject(parser *parsekit.Parser) {
	depth := 0
	for !parser.AtEnd() {
		switch {
		case parser.At(syntax.TkOpenBrace):
			depth++
			parser.Bump()
		case parser.At(syntax.TkCloseBrace):
			depth--
			parser.Bump()
			if depth == 0 {
				return
			}
		default:
			parser.Bump()
		}
	}
}

func parseArray(parser *parsekit.Parser) parsekit.CompletedMarker {
	array := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkCloseBracket) {
		position := parser.GetPos()
		parseExpression(parser, stopSet(syntax.TkComma, syntax.TkCloseBracket))
		if parser.At(syntax.TkComma) {
			parser.Bump()
		}
		if parser.GetPos() == position {
			parser.Bump()
		}
	}
	parser.Expect(syntax.TkCloseBracket, []syntax.Kind{syntax.TkSemicolon, syntax.TkCloseBrace})
	return parser.Complete(array, syntax.JsArray)
}

func parseParenthesized(parser *parsekit.Parser) parsekit.CompletedMarker {
	parenthesized := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkCloseParen) {
		position := parser.GetPos()
		parseExpression(parser, stopSet(syntax.TkComma, syntax.TkCloseParen))
		if parser.At(syntax.TkComma) {
			parser.Bump()
		}
		if parser.GetPos() == position {
			parser.Bump()
		}
	}
	parser.Expect(syntax.TkCloseParen, []syntax.Kind{syntax.TkArrow, syntax.TkOpenBrace})
	return parser.Complete(parenthesized, syntax.JsParenthesized)
}

func parseFunction(parser *parsekit.Parser) parsekit.CompletedMarker {
	function := parser.Start()
	parser.Bump()
	if parser.At(syntax.TkIdentifier) {
		parseLeaf(parser, syntax.JsIdentifier)
	}
	if parser.At(syntax.TkOpenParen) {
		parseArguments(parser)
	}
	parseReturnType(parser)
	if parser.At(syntax.TkOpenBrace) {
		parseBlock(parser)
	}
	return parser.Complete(function, syntax.JsFunction)
}

func parseClassLike(parser *parsekit.Parser) parsekit.CompletedMarker {
	class := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkOpenBrace) && !parser.At(syntax.TkSemicolon) {
		parser.Bump()
	}
	if parser.At(syntax.TkOpenBrace) {
		parseBlock(parser)
	}
	return parser.Complete(class, syntax.JsFunction)
}

func parseBlock(parser *parsekit.Parser) parsekit.CompletedMarker {
	block := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkCloseBrace) {
		position := parser.GetPos()
		parseStatement(parser)
		if parser.GetPos() == position {
			parser.Bump()
		}
	}
	parser.Expect(syntax.TkCloseBrace, nil)
	return parser.Complete(block, syntax.JsBlock)
}

func parseString(parser *parsekit.Parser) parsekit.CompletedMarker {
	stringNode := parser.Start()
	token := parser.Bump()
	if !closedString(token.Text()) {
		parser.AddError(parsekit.NewErrorBuilder("closing quote").AtToken(token))
	}
	return parser.Complete(stringNode, syntax.JsString)
}

func parseLeaf(parser *parsekit.Parser, kind syntax.Kind) parsekit.CompletedMarker {
	node := parser.Start()
	parser.Bump()
	return parser.Complete(node, kind)
}

func atKeyword(parser *parsekit.Parser, words ...string) bool {
	if !parser.At(syntax.TkKeyword) {
		return false
	}
	text := parser.PeekToken().Text()
	for _, word := range words {
		if text == word {
			return true
		}
	}
	return false
}

func nextNonTriviaText(parser *parsekit.Parser) string {
	currentSeen := false
	for offset := 0; ; offset++ {
		token := parser.PeekNthToken(offset)
		if token == nil {
			return ""
		}
		if token.Kind.IsTrivia() {
			continue
		}
		if !currentSeen {
			currentSeen = true
			continue
		}
		return token.Text()
	}
}

func stopSet(kinds ...syntax.Kind) map[syntax.Kind]struct{} {
	result := make(map[syntax.Kind]struct{}, len(kinds))
	for _, kind := range kinds {
		result[kind] = struct{}{}
	}
	return result
}

func atStop(parser *parsekit.Parser, stop map[syntax.Kind]struct{}) bool {
	kind, ok := parser.Peek()
	if !ok {
		return true
	}
	_, ok = stop[kind]
	return ok
}

func closedString(text string) bool {
	if len(text) < 2 {
		return false
	}
	quote := text[0]
	return (quote == '\'' || quote == '"' || quote == '`') && strings.HasSuffix(text, string(quote))
}
