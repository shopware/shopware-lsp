package parser

import (
	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
	"github.com/shopware/shopware-lsp/internal/parser/xpath/lexer"
	"github.com/shopware/shopware-lsp/internal/parser/xpath/syntax"
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
		ErrorKind: syntax.Error,
	})
	root := parser.Start()
	if parser.AtEnd() {
		parser.AddError(parsekit.NewErrorBuilder("XPath expression"))
	} else {
		parseSequence(parser, syntax.KindNone)
	}
	parser.Complete(root, syntax.XPathDocument)
	tree, errors := parser.Finish(source)
	return Result{Tree: tree, Errors: errors}
}

func parseSequence(parser *parsekit.Parser, closing syntax.Kind) {
	for !parser.AtEnd() && (closing == syntax.KindNone ||
		!parser.At(closing)) {
		switch {
		case parser.At(syntax.TkOpenParen):
			parseDelimited(
				parser,
				syntax.TkCloseParen,
				syntax.XPathGroup,
			)
		case parser.At(syntax.TkOpenBracket):
			parseDelimited(
				parser,
				syntax.TkCloseBracket,
				syntax.XPathPredicate,
			)
		case parser.AtSet([]syntax.Kind{
			syntax.TkCloseParen,
			syntax.TkCloseBracket,
		}):
			parser.AddError(parsekit.NewErrorBuilder("XPath expression"))
			parser.Bump()
		case parser.At(syntax.TkString):
			token := parser.Bump()
			text := token.Text()
			if len(text) < 2 ||
				text[0] != text[len(text)-1] {
				parser.AddError(
					parsekit.NewErrorBuilder("closing quote").AtToken(token),
				)
			}
		case parser.At(syntax.TkUnknown):
			token := parser.Bump()
			parser.AddError(
				parsekit.NewErrorBuilder("XPath token").AtToken(token),
			)
		case parser.At(syntax.TkDollar):
			parser.Bump()
			if !parser.At(syntax.TkName) {
				parser.AddError(parsekit.NewErrorBuilder("variable name"))
			}
		case parser.At(syntax.TkAt):
			parser.Bump()
			if !parser.AtSet([]syntax.Kind{
				syntax.TkName,
				syntax.TkStar,
			}) {
				parser.AddError(parsekit.NewErrorBuilder("attribute name"))
			}
		default:
			parser.Bump()
		}
	}
}

func parseDelimited(
	parser *parsekit.Parser,
	closing,
	kind syntax.Kind,
) {
	marker := parser.Start()
	parser.Bump()
	if kind == syntax.XPathPredicate && parser.At(closing) {
		parser.AddError(parsekit.NewErrorBuilder("XPath predicate"))
	}
	parseSequence(parser, closing)
	parser.Expect(closing, nil)
	parser.Complete(marker, kind)
}
