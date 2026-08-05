package semantic

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
)

func TestEmbeddedJSONSemanticTokensColorKeysAndScalarValues(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/JsonResponse.php",
		[]byte(`<?php
namespace Symfony\Component\HttpFoundation;
class JsonResponse {
    public static function fromJsonString(string $data): static {}
    public function setJson(string $data): static {}
}
`),
	)))
	source := `<?php
use Symfony\Component\HttpFoundation\JsonResponse;
function send(JsonResponse $response): void {
    $response->setJson('{"name":"Shopware","count":2,"active":true,"empty":null}');
    JsonResponse::fromJsonString("{\"second\":\"value\"}");
}
`
	document := lsp.NewTextDocument(
		"file:///project/src/Controller.php",
		source,
		1,
	)
	tokens, err := NewEmbeddedJSONProvider(phpIndex).GetSemanticTokens(
		context.Background(),
		&lsp.SemanticTokensRequest{Document: document},
	)
	require.NoError(t, err)

	expected := []struct {
		text      string
		tokenType uint32
	}{
		{"name", protocol.SemanticTokenProperty},
		{"Shopware", protocol.SemanticTokenString},
		{"count", protocol.SemanticTokenProperty},
		{"2", protocol.SemanticTokenNumber},
		{"active", protocol.SemanticTokenProperty},
		{"true", protocol.SemanticTokenKeyword},
		{"empty", protocol.SemanticTokenProperty},
		{"null", protocol.SemanticTokenKeyword},
		{"second", protocol.SemanticTokenProperty},
		{"value", protocol.SemanticTokenString},
	}
	require.Len(t, tokens, len(expected))
	for index, want := range expected {
		token := tokens[index]
		assert.Equal(t, want.tokenType, token.Type)
		assert.Equal(
			t,
			want.text,
			source[token.Range.Start:token.Range.End],
		)
	}
}
