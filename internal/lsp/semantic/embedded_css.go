package semantic

import (
	"context"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	scsslexer "github.com/shopware/shopware-lsp/internal/parser/scss/lexer"
	scsssyntax "github.com/shopware/shopware-lsp/internal/parser/scss/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// EmbeddedCSSProvider colors typed DomCrawler selector arguments. The native
// SCSS lexer supplies CSS-compatible selector tokens without coupling the
// language server to an editor-specific injected-language API.
type EmbeddedCSSProvider struct {
	phpIndex *php.PHPIndex
}

func NewEmbeddedCSSProvider(phpIndex *php.PHPIndex) *EmbeddedCSSProvider {
	return &EmbeddedCSSProvider{phpIndex: phpIndex}
}

func (p *EmbeddedCSSProvider) GetSemanticTokens(
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
	for _, selector := range php.EmbeddedCSSSelectors(
		p.phpIndex,
		path,
		document.Version,
		string(document.Text),
		document.SyntaxTree.Root,
	) {
		if ctx.Err() != nil {
			return nil, nil
		}
		result = append(
			result,
			embeddedCSSSemanticTokens(selector)...,
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

func embeddedCSSSemanticTokens(
	selector php.EmbeddedCSSSelector,
) []lsp.SemanticToken {
	tokens := scsslexer.Lex(selector.Value)
	var result []lsp.SemanticToken
	bracketDepth := 0
	for index, token := range tokens {
		if token.Kind.IsTrivia() {
			continue
		}
		text := token.Text()
		previous := previousEmbeddedCSSToken(tokens, index)
		next := nextEmbeddedCSSToken(tokens, index)
		var (
			tokenType uint32
			rng       = token.Range()
			include   bool
		)
		switch token.Kind {
		case scsssyntax.TkOpenBracket:
			bracketDepth++
		case scsssyntax.TkCloseBracket:
			if bracketDepth > 0 {
				bracketDepth--
			}
		case scsssyntax.TkIdentifier:
			include = true
			switch {
			case previous.Text() == ".":
				tokenType = protocol.SemanticTokenClass
			case previous.Kind == scsssyntax.TkHash:
				tokenType = protocol.SemanticTokenVariable
			case previous.Kind == scsssyntax.TkColon &&
				next.Kind == scsssyntax.TkOpenParen:
				tokenType = protocol.SemanticTokenFunction
			case previous.Kind == scsssyntax.TkColon:
				tokenType = protocol.SemanticTokenKeyword
			case bracketDepth > 0 &&
				previous.Kind == scsssyntax.TkOperator:
				tokenType = protocol.SemanticTokenString
			case bracketDepth > 0:
				tokenType = protocol.SemanticTokenProperty
			default:
				tokenType = protocol.SemanticTokenType
			}
		case scsssyntax.TkSingleQuotedString,
			scsssyntax.TkDoubleQuotedString:
			include = true
			tokenType = protocol.SemanticTokenString
			if len(text) >= 2 &&
				text[0] == text[len(text)-1] {
				rng.Start++
				rng.End--
			}
		case scsssyntax.TkNumber:
			include = true
			tokenType = protocol.SemanticTokenNumber
		case scsssyntax.TkOperator:
			if text != "." {
				include = true
				tokenType = protocol.SemanticTokenOperator
			}
		}
		if !include || rng.Start >= rng.End {
			continue
		}
		result = append(result, lsp.SemanticToken{
			Range: selector.SourceRange(rng),
			Type:  tokenType,
		})
	}
	return result
}

func previousEmbeddedCSSToken(
	tokens []scsslexer.Token,
	index int,
) scsslexer.Token {
	for position := index - 1; position >= 0; position-- {
		if !tokens[position].Kind.IsTrivia() {
			return tokens[position]
		}
	}
	return scsslexer.Token{Kind: cst.KindNone}
}

func nextEmbeddedCSSToken(
	tokens []scsslexer.Token,
	index int,
) scsslexer.Token {
	for position := index + 1; position < len(tokens); position++ {
		if !tokens[position].Kind.IsTrivia() {
			return tokens[position]
		}
	}
	return scsslexer.Token{Kind: cst.KindNone}
}

var _ lsp.SemanticTokensProvider = (*EmbeddedCSSProvider)(nil)
