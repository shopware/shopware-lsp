package semantic

import (
	"context"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsonparser "github.com/shopware/shopware-lsp/internal/parser/json"
	jsonquery "github.com/shopware/shopware-lsp/internal/parser/json/query"
	jsonsyntax "github.com/shopware/shopware-lsp/internal/parser/json/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// EmbeddedJSONProvider colors decoded JsonResponse JSON arguments with
// standard semantic token kinds. It is the portable equivalent of the
// reference plugin's JSON language injection.
type EmbeddedJSONProvider struct {
	phpIndex *php.PHPIndex
}

func NewEmbeddedJSONProvider(phpIndex *php.PHPIndex) *EmbeddedJSONProvider {
	return &EmbeddedJSONProvider{phpIndex: phpIndex}
}

func (p *EmbeddedJSONProvider) GetSemanticTokens(
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
	for _, literal := range php.EmbeddedJSONLiterals(
		p.phpIndex,
		path,
		document.Version,
		string(document.Text),
		document.SyntaxTree.Root,
	) {
		if ctx.Err() != nil {
			return nil, nil
		}
		result = append(result, embeddedJSONSemanticTokens(literal)...)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Range.Start != result[right].Range.Start {
			return result[left].Range.Start < result[right].Range.Start
		}
		return result[left].Range.End < result[right].Range.End
	})
	return result, nil
}

func embeddedJSONSemanticTokens(
	literal php.EmbeddedPHPString,
) []lsp.SemanticToken {
	parsed := jsonparser.Parse(literal.Value)
	if parsed.Tree == nil || parsed.Tree.Root == nil {
		return nil
	}
	var result []lsp.SemanticToken
	keys := make(map[cst.TextRange]struct{})
	for _, pair := range jsonquery.Nodes(
		parsed.Tree.Root,
		jsonsyntax.JsonPair,
	) {
		key := jsonquery.PairKey(pair)
		if key == nil {
			continue
		}
		keys[key.Range()] = struct{}{}
		result = appendEmbeddedJSONToken(
			result,
			literal,
			jsonStringContentRange(key),
			protocol.SemanticTokenProperty,
		)
	}
	for _, node := range jsonquery.Nodes(
		parsed.Tree.Root,
		jsonsyntax.JsonString,
		jsonsyntax.JsonNumber,
		jsonsyntax.JsonBoolean,
		jsonsyntax.JsonNull,
	) {
		if _, key := keys[node.Range()]; key {
			continue
		}
		tokenType := protocol.SemanticTokenKeyword
		rng := node.Range()
		switch node.Kind() {
		case jsonsyntax.JsonString:
			tokenType = protocol.SemanticTokenString
			rng = jsonStringContentRange(node)
		case jsonsyntax.JsonNumber:
			tokenType = protocol.SemanticTokenNumber
		}
		result = appendEmbeddedJSONToken(
			result,
			literal,
			rng,
			tokenType,
		)
	}
	return result
}

func appendEmbeddedJSONToken(
	result []lsp.SemanticToken,
	literal php.EmbeddedPHPString,
	rng cst.TextRange,
	tokenType uint32,
) []lsp.SemanticToken {
	hostRange := literal.SourceRange(rng)
	if hostRange.Start >= hostRange.End {
		return result
	}
	return append(result, lsp.SemanticToken{
		Range: hostRange,
		Type:  tokenType,
	})
}

func jsonStringContentRange(node *jsonsyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
		rng.Start++
		rng.End--
	}
	return rng
}

var _ lsp.SemanticTokensProvider = (*EmbeddedJSONProvider)(nil)
