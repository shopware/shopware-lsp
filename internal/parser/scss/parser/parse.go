package parser

import (
	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
	"github.com/shopware/shopware-lsp/internal/parser/scss/lexer"
	"github.com/shopware/shopware-lsp/internal/parser/scss/syntax"
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
		if parser.At(syntax.TkCloseBrace) {
			parser.AddError(parsekit.NewErrorBuilder("SCSS statement"))
			parser.Bump()
			continue
		}
		parseStatement(parser)
	}

	parser.Complete(root, syntax.ScssStylesheet)
	tree, errors := parser.Finish(source)
	return Result{Tree: tree, Errors: errors}
}

func parseStatement(parser *parsekit.Parser) {
	marker := parser.Start()
	kind := classifyStatement(parser)

	for !parser.AtEnd() && !parser.At(syntax.TkCloseBrace) {
		switch {
		case parser.At(syntax.TkOpenBrace):
			parseBlock(parser)
			if parser.At(syntax.TkSemicolon) {
				parser.Bump()
			}
			parser.Complete(marker, kind)
			return
		case parser.At(syntax.TkSemicolon):
			parser.Bump()
			parser.Complete(marker, kind)
			return
		default:
			if !parseComponent(parser, nil) {
				parser.AddError(parsekit.NewErrorBuilder("SCSS expression"))
				parser.Bump()
			}
		}
	}

	parser.Complete(marker, kind)
}

func classifyStatement(parser *parsekit.Parser) syntax.Kind {
	first, hasColon, hasBlock := statementShape(parser)
	switch {
	case first == syntax.TkVariable && hasColon:
		return syntax.ScssVariableDeclaration
	case first == syntax.TkAtKeyword:
		return syntax.ScssAtRule
	case hasBlock:
		return syntax.ScssRule
	case hasColon:
		return syntax.ScssDeclaration
	default:
		return syntax.ScssRule
	}
}

func statementShape(parser *parsekit.Parser) (syntax.Kind, bool, bool) {
	first := syntax.KindNone
	parenDepth := 0
	bracketDepth := 0
	interpolationDepth := 0
	hasColon := false

	for offset := 0; ; offset++ {
		token := parser.PeekNthToken(offset)
		if token == nil {
			return first, hasColon, false
		}
		if token.Kind.IsTrivia() {
			continue
		}
		if first == syntax.KindNone {
			first = token.Kind
		}
		switch token.Kind {
		case syntax.TkOpenParen:
			parenDepth++
		case syntax.TkCloseParen:
			if parenDepth > 0 {
				parenDepth--
			}
		case syntax.TkOpenBracket:
			bracketDepth++
		case syntax.TkCloseBracket:
			if bracketDepth > 0 {
				bracketDepth--
			}
		case syntax.TkInterpolationOpen:
			interpolationDepth++
		case syntax.TkCloseBrace:
			if interpolationDepth > 0 {
				interpolationDepth--
				continue
			}
			if parenDepth == 0 && bracketDepth == 0 {
				return first, hasColon, false
			}
		case syntax.TkColon:
			if parenDepth == 0 && bracketDepth == 0 && interpolationDepth == 0 {
				hasColon = true
			}
		case syntax.TkOpenBrace:
			if parenDepth == 0 && bracketDepth == 0 && interpolationDepth == 0 {
				return first, hasColon, true
			}
		case syntax.TkSemicolon:
			if parenDepth == 0 && bracketDepth == 0 && interpolationDepth == 0 {
				return first, hasColon, false
			}
		}
	}
}

func parseBlock(parser *parsekit.Parser) {
	marker := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkCloseBrace) {
		parseStatement(parser)
	}
	parser.Expect(syntax.TkCloseBrace, []syntax.Kind{syntax.TkSemicolon})
	parser.Complete(marker, syntax.ScssBlock)
}

func parseComponent(parser *parsekit.Parser, stop map[syntax.Kind]struct{}) bool {
	if atStop(parser, stop) || parser.AtEnd() {
		return false
	}

	switch {
	case parser.At(syntax.TkVariable):
		parseLeaf(parser, syntax.ScssVariable, 0)
	case parser.AtSet([]syntax.Kind{syntax.TkSingleQuotedString, syntax.TkDoubleQuotedString}):
		parseString(parser)
	case parser.At(syntax.TkIdentifier) && nextNonTriviaKind(parser) == syntax.TkOpenParen:
		parseFunctionCall(parser)
	case parser.At(syntax.TkOpenParen):
		parseDelimited(parser, syntax.TkCloseParen, syntax.ScssParenthesized)
	case parser.At(syntax.TkOpenBracket):
		parseDelimited(parser, syntax.TkCloseBracket, syntax.ScssBracketList)
	case parser.At(syntax.TkInterpolationOpen):
		parseInterpolation(parser)
	case parser.AtSet([]syntax.Kind{syntax.TkCloseParen, syntax.TkCloseBracket}):
		parser.AddError(parsekit.NewErrorBuilder("SCSS expression"))
		parser.Bump()
	default:
		parser.Bump()
	}
	return true
}

func parseLeaf(parser *parsekit.Parser, kind syntax.Kind, expectedClosing byte) {
	marker := parser.Start()
	token := parser.Bump()
	text := token.Text()
	if expectedClosing != 0 &&
		(len(text) < 2 || text[len(text)-1] != expectedClosing) {
		parser.AddError(parsekit.NewErrorBuilder("closing quote").AtToken(token))
	}
	parser.Complete(marker, kind)
}

func parseString(parser *parsekit.Parser) {
	closing := byte('\'')
	if parser.At(syntax.TkDoubleQuotedString) {
		closing = '"'
	}
	parseLeaf(parser, syntax.ScssString, closing)
}

func parseFunctionCall(parser *parsekit.Parser) {
	marker := parser.Start()
	parser.Bump()

	arguments := parser.Start()
	parser.Expect(syntax.TkOpenParen, []syntax.Kind{syntax.TkCloseParen, syntax.TkSemicolon})
	for !parser.AtEnd() && !parser.At(syntax.TkCloseParen) {
		position := parser.GetPos()
		argument := parser.Start()
		for !parser.AtEnd() &&
			!parser.At(syntax.TkComma) &&
			!parser.At(syntax.TkCloseParen) {
			if !parseComponent(parser, map[syntax.Kind]struct{}{
				syntax.TkComma:      {},
				syntax.TkCloseParen: {},
			}) {
				break
			}
		}
		parser.Complete(argument, syntax.ScssArgument)

		if parser.At(syntax.TkComma) {
			parser.Bump()
		} else if !parser.At(syntax.TkCloseParen) {
			parser.AddError(parsekit.NewErrorBuilder("\",\" or \")\""))
			parser.Recover([]syntax.Kind{syntax.TkComma, syntax.TkCloseParen})
			if parser.At(syntax.TkComma) {
				parser.Bump()
			}
		}
		if parser.GetPos() == position {
			break
		}
	}
	parser.Expect(syntax.TkCloseParen, []syntax.Kind{
		syntax.TkSemicolon,
		syntax.TkCloseBrace,
	})
	parser.Complete(arguments, syntax.ScssArgumentList)
	parser.Complete(marker, syntax.ScssFunctionCall)
}

func parseDelimited(parser *parsekit.Parser, closing, kind syntax.Kind) {
	marker := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(closing) {
		if !parseComponent(parser, map[syntax.Kind]struct{}{closing: {}}) {
			break
		}
	}
	parser.Expect(closing, []syntax.Kind{
		syntax.TkSemicolon,
		syntax.TkCloseBrace,
	})
	parser.Complete(marker, kind)
}

func parseInterpolation(parser *parsekit.Parser) {
	marker := parser.Start()
	parser.Bump()
	for !parser.AtEnd() && !parser.At(syntax.TkCloseBrace) {
		if !parseComponent(parser, map[syntax.Kind]struct{}{syntax.TkCloseBrace: {}}) {
			break
		}
	}
	parser.Expect(syntax.TkCloseBrace, []syntax.Kind{
		syntax.TkSemicolon,
		syntax.TkCloseParen,
		syntax.TkCloseBracket,
	})
	parser.Complete(marker, syntax.ScssInterpolation)
}

func atStop(parser *parsekit.Parser, stop map[syntax.Kind]struct{}) bool {
	if len(stop) == 0 {
		return false
	}
	kind, ok := parser.Peek()
	if !ok {
		return true
	}
	_, ok = stop[kind]
	return ok
}

func nextNonTriviaKind(parser *parsekit.Parser) syntax.Kind {
	currentSeen := false
	for offset := 0; ; offset++ {
		token := parser.PeekNthToken(offset)
		if token == nil {
			return syntax.KindNone
		}
		if token.Kind.IsTrivia() {
			continue
		}
		if !currentSeen {
			currentSeen = true
			continue
		}
		return token.Kind
	}
}
