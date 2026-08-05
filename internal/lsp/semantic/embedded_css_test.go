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

func TestEmbeddedCSSSemanticTokensColorSelectorParts(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/Crawler.php",
		[]byte(`<?php
namespace Symfony\Component\DomCrawler;
class Crawler {
    public function filter(string $selector): static {}
}
`),
	)))
	source := `<?php
use Symfony\Component\DomCrawler\Crawler;
function find(Crawler $crawler): void {
    $crawler->filter('.product > a[data-active="true"]:nth-child(2), #main');
}
`
	document := lsp.NewTextDocument(
		"file:///project/src/CrawlerTest.php",
		source,
		1,
	)
	tokens, err := NewEmbeddedCSSProvider(phpIndex).GetSemanticTokens(
		context.Background(),
		&lsp.SemanticTokensRequest{Document: document},
	)
	require.NoError(t, err)
	expected := []struct {
		text      string
		tokenType uint32
	}{
		{"product", protocol.SemanticTokenClass},
		{">", protocol.SemanticTokenOperator},
		{"a", protocol.SemanticTokenType},
		{"data-active", protocol.SemanticTokenProperty},
		{"=", protocol.SemanticTokenOperator},
		{"true", protocol.SemanticTokenString},
		{"nth-child", protocol.SemanticTokenFunction},
		{"2", protocol.SemanticTokenNumber},
		{"main", protocol.SemanticTokenVariable},
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
