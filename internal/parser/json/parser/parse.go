package parser

import (
	"unicode/utf8"

	"github.com/shopware/shopware-lsp/internal/parser/bytescan"
	"github.com/shopware/shopware-lsp/internal/parser/json/lexer"
	"github.com/shopware/shopware-lsp/internal/parser/json/syntax"
	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
)

type Error = parsekit.Error

type Result struct {
	Tree   *syntax.Tree
	Errors []Error
}

var booleanKinds = []syntax.Kind{
	syntax.TkTrue,
	syntax.TkFalse,
}

var pairRecoveryKinds = []syntax.Kind{
	syntax.TkComma,
	syntax.TkCloseBrace,
	syntax.TkOpenBrace,
	syntax.TkOpenBracket,
	syntax.TkString,
	syntax.TkNumber,
	syntax.TkTrue,
	syntax.TkFalse,
	syntax.TkNull,
}

var arrayRecoveryKinds = []syntax.Kind{
	syntax.TkCloseBracket,
	syntax.TkOpenBrace,
	syntax.TkOpenBracket,
	syntax.TkString,
	syntax.TkNumber,
	syntax.TkTrue,
	syntax.TkFalse,
	syntax.TkNull,
}

func Parse(source string) Result {
	tokens := lexer.LexInto(
		source,
		parsekit.AcquireTokenBuffer(len(source)/3+1),
	)
	parser := parsekit.NewOwned(
		tokens,
		parsekit.Config{ErrorKind: syntax.Error},
	)
	root := parser.Start()

	if parser.AtEnd() {
		parser.AddError(parsekit.NewErrorBuilder("JSON value"))
	} else {
		parseValue(parser)
	}
	if !parser.AtEnd() {
		parser.AddError(parsekit.NewErrorBuilder("end of file"))
		parser.Recover(nil)
	}

	parser.Complete(root, syntax.JsonDocument)
	tree, errors := parser.Finish(source)
	return Result{Tree: tree, Errors: errors}
}

func parseValue(parser *parsekit.Parser) bool {
	switch {
	case parser.At(syntax.TkOpenBrace):
		parseObject(parser)
	case parser.At(syntax.TkOpenBracket):
		parseArray(parser)
	case parser.At(syntax.TkString):
		parseScalar(parser, syntax.JsonString)
	case parser.At(syntax.TkNumber):
		parseScalar(parser, syntax.JsonNumber)
	case parser.AtSet(booleanKinds):
		parseScalar(parser, syntax.JsonBoolean)
	case parser.At(syntax.TkNull):
		parseScalar(parser, syntax.JsonNull)
	default:
		return false
	}
	return true
}

func parseScalar(parser *parsekit.Parser, kind syntax.Kind) {
	marker := parser.Start()
	token := parser.Bump()
	if kind == syntax.JsonString && !validJSONString(token.Text()) {
		parser.AddError(parsekit.NewErrorBuilder("valid JSON string").AtToken(token))
	}
	parser.Complete(marker, kind)
}

func validJSONString(text string) bool {
	if len(text) < 2 || text[0] != '"' || text[len(text)-1] != '"' {
		return false
	}

	content := text[1 : len(text)-1]
	if !utf8.ValidString(content) {
		return false
	}
	for position := 0; position < len(content); position++ {
		position = bytescan.IndexByteOrLessThan(content, position, '\\', 0x20)
		if position >= len(content) {
			break
		}
		if content[position] < 0x20 {
			return false
		}
		if position+1 >= len(content) {
			return false
		}

		position++
		switch content[position] {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		case 'u':
			if position+4 >= len(content) {
				return false
			}
			for _, digit := range content[position+1 : position+5] {
				if !isHexDigit(byte(digit)) {
					return false
				}
			}
			position += 4
		default:
			return false
		}
	}
	return true
}

func isHexDigit(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'f' ||
		value >= 'A' && value <= 'F'
}

func parseObject(parser *parsekit.Parser) {
	marker := parser.Start()
	parser.Bump()

	for !parser.AtEnd() && !parser.At(syntax.TkCloseBrace) {
		position := parser.GetPos()
		if parser.At(syntax.TkString) {
			parsePair(parser)
		} else {
			parser.AddError(parsekit.NewErrorBuilder("string object key"))
			parser.Recover([]syntax.Kind{syntax.TkComma, syntax.TkCloseBrace})
		}

		if parser.At(syntax.TkComma) {
			parser.Bump()
			if parser.At(syntax.TkCloseBrace) {
				parser.AddError(parsekit.NewErrorBuilder("string object key"))
			}
		} else if !parser.AtEnd() && !parser.At(syntax.TkCloseBrace) {
			parser.Expect(syntax.TkComma, []syntax.Kind{syntax.TkString, syntax.TkCloseBrace})
		}

		if parser.GetPos() == position {
			break
		}
	}

	parser.Expect(syntax.TkCloseBrace, nil)
	parser.Complete(marker, syntax.JsonObject)
}

func parsePair(parser *parsekit.Parser) {
	marker := parser.Start()
	parseScalar(parser, syntax.JsonString)

	parser.Expect(syntax.TkColon, pairRecoveryKinds)

	if !parseValue(parser) {
		parser.AddError(parsekit.NewErrorBuilder("JSON value"))
		parser.Recover([]syntax.Kind{syntax.TkComma, syntax.TkCloseBrace})
	}

	parser.Complete(marker, syntax.JsonPair)
}

func parseArray(parser *parsekit.Parser) {
	marker := parser.Start()
	parser.Bump()

	for !parser.AtEnd() && !parser.At(syntax.TkCloseBracket) {
		position := parser.GetPos()
		if !parseValue(parser) {
			parser.AddError(parsekit.NewErrorBuilder("JSON value"))
			parser.Recover([]syntax.Kind{syntax.TkComma, syntax.TkCloseBracket})
		}

		if parser.At(syntax.TkComma) {
			parser.Bump()
			if parser.At(syntax.TkCloseBracket) {
				parser.AddError(parsekit.NewErrorBuilder("JSON value"))
			}
		} else if !parser.AtEnd() && !parser.At(syntax.TkCloseBracket) {
			parser.Expect(syntax.TkComma, arrayRecoveryKinds)
		}

		if parser.GetPos() == position {
			break
		}
	}

	parser.Expect(syntax.TkCloseBracket, nil)
	parser.Complete(marker, syntax.JsonArray)
}
