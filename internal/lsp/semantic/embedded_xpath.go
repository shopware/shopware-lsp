package semantic

import (
	"context"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	xpathlexer "github.com/shopware/shopware-lsp/internal/parser/xpath/lexer"
	xpathsyntax "github.com/shopware/shopware-lsp/internal/parser/xpath/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// EmbeddedXPathProvider colors typed DomCrawler XPath expressions with
// standard semantic token kinds from the native XPath lexer.
type EmbeddedXPathProvider struct {
	phpIndex *php.PHPIndex
}

func NewEmbeddedXPathProvider(phpIndex *php.PHPIndex) *EmbeddedXPathProvider {
	return &EmbeddedXPathProvider{phpIndex: phpIndex}
}

func (p *EmbeddedXPathProvider) GetSemanticTokens(
	ctx context.Context,
	request *lsp.SemanticTokensRequest,
) ([]lsp.SemanticToken, error) {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.Document == nil ||
		request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil ||
		!strings.HasSuffix(strings.ToLower(request.Document.URI), ".php") {
		return nil, nil
	}
	document := request.Document
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, nil
	}
	var result []lsp.SemanticToken
	for _, expression := range php.EmbeddedXPathExpressions(
		p.phpIndex,
		path,
		document.Version,
		document.SourceString(),
		document.SyntaxTree.Root,
	) {
		if ctx.Err() != nil {
			return nil, nil
		}
		result = append(
			result,
			embeddedXPathSemanticTokens(expression)...,
		)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Range.Start != result[right].Range.Start {
			return result[left].Range.Start < result[right].Range.Start
		}
		return result[left].Range.End < result[right].Range.End
	})
	return result, nil
}

func embeddedXPathSemanticTokens(
	expression php.EmbeddedXPathExpression,
) []lsp.SemanticToken {
	tokens := xpathlexer.Lex(expression.Value)
	var result []lsp.SemanticToken
	for index, token := range tokens {
		if token.Kind.IsTrivia() {
			continue
		}
		text := token.Text()
		previous := previousXPathToken(tokens, index)
		next := nextXPathToken(tokens, index)
		var (
			tokenType uint32
			rng       = token.Range()
			include   bool
		)
		switch token.Kind {
		case xpathsyntax.TkName:
			include = true
			switch {
			case previous.Kind == xpathsyntax.TkDollar:
				tokenType = protocol.SemanticTokenVariable
			case previous.Kind == xpathsyntax.TkAt:
				tokenType = protocol.SemanticTokenProperty
			case next.Kind == xpathsyntax.TkDoubleColon:
				tokenType = protocol.SemanticTokenKeyword
			case next.Kind == xpathsyntax.TkOpenParen:
				tokenType = protocol.SemanticTokenFunction
			case xpathOperatorName(text) &&
				xpathWordOperatorContext(previous.Kind, next.Kind):
				tokenType = protocol.SemanticTokenOperator
			default:
				tokenType = protocol.SemanticTokenType
			}
		case xpathsyntax.TkString:
			include = true
			tokenType = protocol.SemanticTokenString
			if len(text) >= 2 &&
				text[0] == text[len(text)-1] {
				rng.Start++
				rng.End--
			}
		case xpathsyntax.TkNumber:
			include = true
			tokenType = protocol.SemanticTokenNumber
		default:
			if xpathOperatorToken(token.Kind) {
				include = true
				tokenType = protocol.SemanticTokenOperator
			}
		}
		if !include || rng.Start >= rng.End {
			continue
		}
		result = append(result, lsp.SemanticToken{
			Range: expression.SourceRange(rng),
			Type:  tokenType,
		})
	}
	return result
}

func xpathWordOperatorContext(
	previous xpathsyntax.Kind,
	next xpathsyntax.Kind,
) bool {
	var previousOperand bool
	switch previous {
	case xpathsyntax.TkName,
		xpathsyntax.TkNumber,
		xpathsyntax.TkString,
		xpathsyntax.TkCloseParen,
		xpathsyntax.TkCloseBracket,
		xpathsyntax.TkDot,
		xpathsyntax.TkDoubleDot,
		xpathsyntax.TkStar:
		previousOperand = true
	}
	if !previousOperand {
		return false
	}
	switch next {
	case xpathsyntax.TkName,
		xpathsyntax.TkNumber,
		xpathsyntax.TkString,
		xpathsyntax.TkOpenParen,
		xpathsyntax.TkDot,
		xpathsyntax.TkDoubleDot,
		xpathsyntax.TkAt,
		xpathsyntax.TkDollar,
		xpathsyntax.TkMinus,
		xpathsyntax.TkStar:
		return true
	default:
		return false
	}
}

func xpathOperatorName(value string) bool {
	switch strings.ToLower(value) {
	case "and", "or", "mod", "div":
		return true
	default:
		return false
	}
}

func xpathOperatorToken(kind xpathsyntax.Kind) bool {
	switch kind {
	case xpathsyntax.TkSlash,
		xpathsyntax.TkDoubleSlash,
		xpathsyntax.TkDot,
		xpathsyntax.TkDoubleDot,
		xpathsyntax.TkAt,
		xpathsyntax.TkColon,
		xpathsyntax.TkDoubleColon,
		xpathsyntax.TkPipe,
		xpathsyntax.TkPlus,
		xpathsyntax.TkMinus,
		xpathsyntax.TkStar,
		xpathsyntax.TkEquals,
		xpathsyntax.TkNotEquals,
		xpathsyntax.TkLess,
		xpathsyntax.TkLessEquals,
		xpathsyntax.TkGreater,
		xpathsyntax.TkGreaterEquals:
		return true
	default:
		return false
	}
}

func previousXPathToken(
	tokens []xpathlexer.Token,
	index int,
) xpathlexer.Token {
	for position := index - 1; position >= 0; position-- {
		if !tokens[position].Kind.IsTrivia() {
			return tokens[position]
		}
	}
	return xpathlexer.Token{Kind: xpathsyntax.KindNone}
}

func nextXPathToken(
	tokens []xpathlexer.Token,
	index int,
) xpathlexer.Token {
	for position := index + 1; position < len(tokens); position++ {
		if !tokens[position].Kind.IsTrivia() {
			return tokens[position]
		}
	}
	return xpathlexer.Token{Kind: xpathsyntax.KindNone}
}

var _ lsp.SemanticTokensProvider = (*EmbeddedXPathProvider)(nil)
