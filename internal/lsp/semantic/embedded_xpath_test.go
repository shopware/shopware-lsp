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

func TestEmbeddedXPathSemanticTokensColorExpressionParts(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/Crawler.php",
		[]byte(`<?php
namespace Symfony\Component\DomCrawler;
class Crawler {
    public function evaluate(string $expression): mixed {}
}
`),
	)))
	source := `<?php
use Symfony\Component\DomCrawler\Crawler;
function find(Crawler $crawler): void {
    $crawler->evaluate('count(.//article[@data-id=$id and contains(@class, "active")]) > 0');
}
`
	document := lsp.NewTextDocument(
		"file:///project/src/CrawlerTest.php",
		source,
		1,
	)
	tokens, err := NewEmbeddedXPathProvider(phpIndex).GetSemanticTokens(
		context.Background(),
		&lsp.SemanticTokensRequest{Document: document},
	)
	require.NoError(t, err)
	expected := []struct {
		text      string
		tokenType uint32
	}{
		{"count", protocol.SemanticTokenFunction},
		{".", protocol.SemanticTokenOperator},
		{"//", protocol.SemanticTokenOperator},
		{"article", protocol.SemanticTokenType},
		{"@", protocol.SemanticTokenOperator},
		{"data-id", protocol.SemanticTokenProperty},
		{"=", protocol.SemanticTokenOperator},
		{"id", protocol.SemanticTokenVariable},
		{"and", protocol.SemanticTokenOperator},
		{"contains", protocol.SemanticTokenFunction},
		{"@", protocol.SemanticTokenOperator},
		{"class", protocol.SemanticTokenProperty},
		{"active", protocol.SemanticTokenString},
		{">", protocol.SemanticTokenOperator},
		{"0", protocol.SemanticTokenNumber},
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
