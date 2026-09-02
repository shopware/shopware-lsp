package folding

import (
	"context"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// AdminFoldingProvider derives collapsible Administration source regions from
// the open document's native CST. It keeps no workspace state: didChange text
// is immediately authoritative and folding has no indexing-memory cost.
type AdminFoldingProvider struct{}

func NewAdminFoldingProvider() *AdminFoldingProvider {
	return &AdminFoldingProvider{}
}

func (p *AdminFoldingProvider) GetFoldingRanges(
	ctx context.Context,
	request *lsp.FoldingRangeRequest,
) ([]protocol.FoldingRange, error) {
	if ctx.Err() != nil || p == nil || request == nil || request.Document == nil ||
		request.Document.SyntaxTree == nil || request.Document.SyntaxTree.Root == nil ||
		request.Document.LineIndex == nil {
		return nil, nil
	}
	switch request.Document.SyntaxLanguage {
	case language.JavaScript:
		return adminJavaScriptFoldingRanges(
			request.Document.SyntaxTree.Root, request.Document.LineIndex,
		), nil
	case language.Twig:
		return adminTwigFoldingRanges(
			request.Document.SyntaxTree.Root, request.Document.LineIndex,
		), nil
	case language.Vue:
		result := adminJavaScriptFoldingRanges(
			request.Document.SyntaxTree.Root, request.Document.LineIndex,
		)
		result = append(
			result,
			adminTwigFoldingRanges(
				request.Document.SyntaxTree.Root, request.Document.LineIndex,
			)...,
		)
		return result, nil
	default:
		return nil, nil
	}
}

func adminJavaScriptFoldingRanges(
	root *jssyntax.Node,
	lineIndex *cst.LineIndex,
) []protocol.FoldingRange {
	var result []protocol.FoldingRange
	for _, node := range jsquery.Nodes(
		root, jssyntax.JsObject, jssyntax.JsArray, jssyntax.JsBlock,
	) {
		if rangeValue, found := adminNodeFoldingRange(
			node.RangeTrimmedTrivia(), lineIndex, true, "",
		); found {
			result = append(result, rangeValue)
		}
	}
	imports := jsquery.Nodes(root, jssyntax.JsImportStatement)
	if len(imports) > 1 {
		start := imports[0].RangeTrimmedTrivia().Start
		end := imports[len(imports)-1].RangeTrimmedTrivia().End
		if rangeValue, found := adminNodeFoldingRange(
			cst.TextRange{Start: start, End: end}, lineIndex, false,
			protocol.FoldingRangeKindImports,
		); found {
			result = append(result, rangeValue)
		}
	}
	for element := range root.Descendants() {
		token, ok := element.(*jssyntax.Token)
		if !ok || token.Kind() != jssyntax.TkBlockComment {
			continue
		}
		if rangeValue, found := adminNodeFoldingRange(
			token.Range(), lineIndex, false, protocol.FoldingRangeKindComment,
		); found {
			result = append(result, rangeValue)
		}
	}
	return result
}

var adminTwigCompoundFoldingKinds = []twigsyntax.Kind{
	twigsyntax.TwigBlock,
	twigsyntax.TwigIf,
	twigsyntax.TwigSet,
	twigsyntax.TwigFor,
	twigsyntax.TwigApply,
	twigsyntax.TwigAutoescape,
	twigsyntax.TwigEmbed,
	twigsyntax.TwigSandbox,
	twigsyntax.TwigVerbatim,
	twigsyntax.TwigMacro,
	twigsyntax.TwigWith,
	twigsyntax.TwigCache,
	twigsyntax.TwigComponent,
	twigsyntax.TwigAssetic,
	twigsyntax.TwigTrans,
	twigsyntax.TwigLiteralArray,
	twigsyntax.TwigLiteralHash,
}

func adminTwigFoldingRanges(
	root *twigsyntax.Node,
	lineIndex *cst.LineIndex,
) []protocol.FoldingRange {
	var result []protocol.FoldingRange
	for node := range twigquery.IterateNodes(root, adminTwigCompoundFoldingKinds...) {
		if rangeValue, found := adminNodeFoldingRange(
			node.RangeTrimmedTrivia(), lineIndex, true, "",
		); found {
			result = append(result, rangeValue)
		}
	}
	for node := range twigquery.IterateNodes(root, twigsyntax.HtmlTag) {
		tag, ok := twigast.CastHtmlTag(node)
		if !ok {
			continue
		}
		_, hasEndingTag := tag.EndingTag()
		if rangeValue, found := adminNodeFoldingRange(
			node.RangeTrimmedTrivia(), lineIndex, hasEndingTag, "",
		); found {
			result = append(result, rangeValue)
		}
	}
	for node := range twigquery.IterateNodes(
		root, twigsyntax.TwigComment, twigsyntax.HtmlComment,
	) {
		if rangeValue, found := adminNodeFoldingRange(
			node.RangeTrimmedTrivia(), lineIndex, false,
			protocol.FoldingRangeKindComment,
		); found {
			result = append(result, rangeValue)
		}
	}
	return result
}

func adminNodeFoldingRange(
	rangeValue cst.TextRange,
	lineIndex *cst.LineIndex,
	preserveClosingLine bool,
	kind string,
) (protocol.FoldingRange, bool) {
	if lineIndex == nil || rangeValue.End <= rangeValue.Start {
		return protocol.FoldingRange{}, false
	}
	startLine, _ := lineIndex.PositionUTF16(rangeValue.Start)
	endLine, _ := lineIndex.PositionUTF16(rangeValue.End)
	if preserveClosingLine && endLine > startLine {
		endLine--
	}
	if endLine <= startLine {
		return protocol.FoldingRange{}, false
	}
	return protocol.FoldingRange{
		StartLine: int(startLine), EndLine: int(endLine), Kind: kind,
	}, true
}

var _ lsp.FoldingRangeProvider = (*AdminFoldingProvider)(nil)
