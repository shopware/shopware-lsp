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

func TestEmbeddedLanguageSemanticTokensDispatchAllLanguages(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	for path, source := range map[string]string{
		"/project/vendor/JsonResponse.php": `<?php
namespace Symfony\Component\HttpFoundation;
class JsonResponse { public function setJson(string $data): static {} }
`,
		"/project/vendor/Crawler.php": `<?php
namespace Symfony\Component\DomCrawler;
class Crawler {
    public function filter(string $selector): static {}
    public function evaluate(string $expression): mixed {}
}
`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	source := `<?php
use Symfony\Component\HttpFoundation\JsonResponse;
use Symfony\Component\DomCrawler\Crawler;
function inspect(JsonResponse $response, Crawler $crawler): void {
    $response->setJson('{"payload":"ready"}');
    $crawler->filter('.product[data-active="yes"]');
    $crawler->evaluate('.//article[@data-id=$identifier]');
}
`
	document := lsp.NewTextDocument(
		"file:///project/src/Inspector.php",
		source,
		1,
	)
	tokens, err := NewEmbeddedLanguageProvider(phpIndex).GetSemanticTokens(
		context.Background(),
		&lsp.SemanticTokensRequest{Document: document},
	)
	require.NoError(t, err)

	typesByText := make(map[string]uint32)
	for _, token := range tokens {
		typesByText[source[token.Range.Start:token.Range.End]] = token.Type
	}
	assert.Equal(
		t,
		protocol.SemanticTokenProperty,
		typesByText["payload"],
	)
	assert.Equal(
		t,
		protocol.SemanticTokenString,
		typesByText["ready"],
	)
	assert.Equal(
		t,
		protocol.SemanticTokenClass,
		typesByText["product"],
	)
	assert.Equal(
		t,
		protocol.SemanticTokenType,
		typesByText["article"],
	)
	assert.Equal(
		t,
		protocol.SemanticTokenVariable,
		typesByText["identifier"],
	)
}

func TestEmbeddedXPathOperatorWordsRemainNamesInPathContexts(t *testing.T) {
	expression := php.EmbeddedPHPString{
		Value: ".//and/@or | .//item[price div 2]",
		SourceOffsets: []uint32{
			0, 1, 2, 3, 4, 5, 6, 7, 8, 9,
			10, 11, 12, 13, 14, 15, 16, 17, 18, 19,
			20, 21, 22, 23, 24, 25, 26, 27, 28, 29,
			30, 31, 32, 33, 34,
		},
	}
	tokens := embeddedXPathSemanticTokens(expression)
	var andType, orType, divType uint32
	for _, token := range tokens {
		switch expression.Value[token.Range.Start:token.Range.End] {
		case "and":
			andType = token.Type
		case "or":
			orType = token.Type
		case "div":
			divType = token.Type
		}
	}
	assert.Equal(t, protocol.SemanticTokenType, andType)
	assert.Equal(t, protocol.SemanticTokenProperty, orType)
	assert.Equal(t, protocol.SemanticTokenOperator, divType)
}
