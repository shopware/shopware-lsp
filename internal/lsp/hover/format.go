package hover

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
)

func hoverProtocolRange(rng cst.TextRange, lineIndex *cst.LineIndex) *protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return &protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
}

// requestByteRange converts a CST byte range to a protocol range against the
// request's line index. It returns nil without a range or a line index.
func requestByteRange(params *lsp.HoverRequest, rng cst.TextRange, ok bool) *protocol.Range {
	if !ok || params.LineIndex == nil {
		return nil
	}
	return hoverProtocolRange(rng, params.LineIndex)
}

// unquotedRange returns the content range of a string whose trivia-trimmed
// range spells text: one leading quote and a matching trailing quote are
// excluded.
func unquotedRange(rng cst.TextRange, text string) cst.TextRange {
	trimmed := strings.TrimRight(text, " \t\r\n\f\v")
	rng.End -= uint32(len(text) - len(trimmed))
	if trimmed == "" {
		return rng
	}
	quote := trimmed[0]
	if quote != '\'' && quote != '"' && quote != '`' {
		return rng
	}
	rng.Start++
	if len(trimmed) >= 2 && trimmed[len(trimmed)-1] == quote {
		rng.End--
	}
	return rng
}

// twigStringContentRange returns the byte range of a Twig string literal's
// unquoted contents.
func twigStringContentRange(literal *cst.Node) (cst.TextRange, bool) {
	stringLiteral, ok := twigast.CastTwigLiteralString(literal)
	if !ok {
		return cst.TextRange{}, false
	}
	if inner, ok := stringLiteral.GetInner(); ok {
		return inner.Syntax().Range(), true
	}
	rng := literal.RangeTrimmedTrivia()
	text := literal.Text()[rng.Start-literal.Range().Start:]
	return unquotedRange(rng, text), true
}

// jsStringContentRange returns the byte range of the unquoted contents of the
// JavaScript string at node, matching jsquery.StringValue.
func jsStringContentRange(node *cst.Node) (cst.TextRange, bool) {
	stringNode := jsquery.StringAt(node)
	if stringNode == nil {
		return cst.TextRange{}, false
	}
	for element := range stringNode.Descendants() {
		token, ok := element.(*cst.Token)
		if ok && (token.Kind() == jssyntax.TkString || token.Kind() == jssyntax.TkTemplate) {
			return unquotedRange(token.Range(), token.Text()), true
		}
	}
	return cst.TextRange{}, false
}

func escapeHoverMarkdown(value string) string {
	return strings.ReplaceAll(value, "`", "\\`")
}
