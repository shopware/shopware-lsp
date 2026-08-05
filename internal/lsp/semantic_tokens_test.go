package lsp

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/stretchr/testify/require"
)

func TestEncodeSemanticTokensUsesUTF16AndRelativePositions(t *testing.T) {
	source := "😀 @prop name\n@block content"
	document := NewTextDocument("file:///component.html.twig", source, 1)
	token := func(value string, tokenType uint32) SemanticToken {
		start := strings.Index(source, value)
		require.NotEqual(t, -1, start)
		return SemanticToken{
			Range: cst.TextRange{
				Start: uint32(start),
				End:   uint32(start + len(value)),
			},
			Type: tokenType,
		}
	}
	tokens := []SemanticToken{
		token("@block", protocol.SemanticTokenKeyword),
		token("name", protocol.SemanticTokenProperty),
		token("@prop", protocol.SemanticTokenKeyword),
		{
			// Invalid multiline ranges are conservatively excluded.
			Range: cst.TextRange{Start: 0, End: uint32(len(source))},
			Type:  protocol.SemanticTokenType,
		},
	}

	require.Equal(t, []uint32{
		0, 3, 5, protocol.SemanticTokenKeyword, 0,
		0, 6, 4, protocol.SemanticTokenProperty, 0,
		1, 0, 6, protocol.SemanticTokenKeyword, 0,
	}, encodeSemanticTokens(document, tokens))
}

func TestSemanticTokensCollectProvidersForOpenDocument(t *testing.T) {
	source := "{# @prop open boolean Description #}"
	start := strings.Index(source, "@prop")
	server := NewServer(nil, "", "test")
	server.RegisterSemanticTokensProvider(staticSemanticTokensProvider{
		{
			Range: cst.TextRange{
				Start: uint32(start),
				End:   uint32(start + len("@prop")),
			},
			Type: protocol.SemanticTokenKeyword,
		},
	})
	server.documentManager.OpenDocument(
		"file:///component.html.twig",
		source,
		1,
	)
	params := &protocol.SemanticTokensParams{}
	params.TextDocument.URI = "file:///component.html.twig"
	result, err := server.semanticTokens(context.Background(), params)
	require.NoError(t, err)
	require.Equal(t, []uint32{
		0, 3, 5, protocol.SemanticTokenKeyword, 0,
	}, result.Data)

	missing := &protocol.SemanticTokensParams{}
	missing.TextDocument.URI = "file:///missing.html.twig"
	result, err = server.semanticTokens(context.Background(), missing)
	require.NoError(t, err)
	require.NotNil(t, result.Data)
	require.Empty(t, result.Data)
	require.NoError(t, server.CloseAll())
}

type staticSemanticTokensProvider []SemanticToken

func (p staticSemanticTokensProvider) GetSemanticTokens(
	context.Context,
	*SemanticTokensRequest,
) ([]SemanticToken, error) {
	return append([]SemanticToken(nil), p...), nil
}
