package lsp

import (
	"context"
	"fmt"
	"sort"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func (s *Server) semanticTokens(
	ctx context.Context,
	params *protocol.SemanticTokensParams,
) (*protocol.SemanticTokens, error) {
	result := &protocol.SemanticTokens{Data: make([]uint32, 0)}
	if params == nil {
		return result, nil
	}
	document, ok := s.documentManager.GetDocument(params.TextDocument.URI)
	if !ok || document == nil || document.LineIndex == nil {
		return result, nil
	}
	request := &SemanticTokensRequest{
		SemanticTokensParams: params,
		Document:             document,
	}
	var tokens []SemanticToken
	for _, provider := range s.semanticTokensProviders {
		provided, err := provider.GetSemanticTokens(ctx, request)
		if err != nil {
			return nil, fmt.Errorf(
				"semantic token provider %T: %w",
				provider,
				err,
			)
		}
		tokens = append(tokens, provided...)
	}
	result.Data = encodeSemanticTokens(document, tokens)
	return result, nil
}

type positionedSemanticToken struct {
	line      uint32
	start     uint32
	length    uint32
	tokenType uint32
	modifiers uint32
}

func encodeSemanticTokens(
	document *TextDocument,
	tokens []SemanticToken,
) []uint32 {
	if document == nil || document.LineIndex == nil || len(tokens) == 0 {
		return make([]uint32, 0)
	}
	positioned := make([]positionedSemanticToken, 0, len(tokens))
	for _, token := range tokens {
		if token.Range.Start >= token.Range.End ||
			token.Range.End > uint32(len(document.Text)) ||
			token.Type >= uint32(len(protocol.SemanticTokenTypes)) {
			continue
		}
		startLine, startCharacter := document.LineIndex.PositionUTF16(
			token.Range.Start,
		)
		endLine, endCharacter := document.LineIndex.PositionUTF16(
			token.Range.End,
		)
		// Token lengths cannot cross lines unless the client explicitly
		// advertises multiline support. Providers emit lexical ranges, so
		// conservatively discard malformed multiline tokens.
		if startLine != endLine || endCharacter <= startCharacter {
			continue
		}
		positioned = append(positioned, positionedSemanticToken{
			line:      startLine,
			start:     startCharacter,
			length:    endCharacter - startCharacter,
			tokenType: token.Type,
			modifiers: token.Modifiers,
		})
	}
	sort.Slice(positioned, func(left, right int) bool {
		if positioned[left].line != positioned[right].line {
			return positioned[left].line < positioned[right].line
		}
		if positioned[left].start != positioned[right].start {
			return positioned[left].start < positioned[right].start
		}
		if positioned[left].length != positioned[right].length {
			return positioned[left].length < positioned[right].length
		}
		return positioned[left].tokenType < positioned[right].tokenType
	})

	data := make([]uint32, 0, len(positioned)*5)
	var previousLine, previousStart uint32
	var lastLine, lastEnd uint32
	havePrevious := false
	for _, token := range positioned {
		if havePrevious && token.line == lastLine && token.start < lastEnd {
			// Overlapping semantic tokens are invalid unless separately
			// negotiated by the client. Registration order gives the earlier
			// sorted token priority.
			continue
		}
		deltaLine := token.line
		deltaStart := token.start
		if havePrevious {
			deltaLine = token.line - previousLine
			if deltaLine == 0 {
				deltaStart = token.start - previousStart
			}
		}
		data = append(
			data,
			deltaLine,
			deltaStart,
			token.length,
			token.tokenType,
			token.modifiers,
		)
		previousLine = token.line
		previousStart = token.start
		lastLine = token.line
		lastEnd = token.start + token.length
		havePrevious = true
	}
	return data
}
