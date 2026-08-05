package diagnostics

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
)

func TestEmbeddedLanguageDiagnosticsDispatchAllLanguages(t *testing.T) {
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
    $response->setJson('{"broken":}');
    $crawler->filter('.broken[');
    $crawler->evaluate('count(.//article');
}
`
	document := lsp.NewTextDocument(
		"file:///project/src/Inspector.php",
		source,
		1,
	)
	result, err := NewEmbeddedLanguageAnalyzer(
		phpIndex,
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 3)
	assert.ElementsMatch(t, []any{
		invalidEmbeddedJSONCode,
		invalidEmbeddedCSSCode,
		invalidEmbeddedXPathCode,
	}, []any{
		result[0].ID,
		result[1].ID,
		result[2].ID,
	})
}
