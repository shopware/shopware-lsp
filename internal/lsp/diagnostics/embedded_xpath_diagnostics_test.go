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

func TestEmbeddedXPathDiagnosticsValidateTypedCrawlerExpressions(
	t *testing.T,
) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/Crawler.php",
		[]byte(`<?php
namespace Symfony\Component\DomCrawler;
class Crawler {
    public function filterXPath(string $expression): static {}
    public function evaluate(string $expression): mixed {}
}
`),
	)))
	source := `<?php
use Symfony\Component\DomCrawler\Crawler;
final class Other { public function evaluate(string $expression): void {} }
function find(Crawler $crawler, Other $other, string $id): void {
    $crawler->filterXPath('.//article[@id="valid"]');
    $crawler->filterXPath('.//article[@id="missing"');
    $crawler->evaluate('count(.//article');
    $other->evaluate('.//ignored[');
    $crawler->evaluate(".//article[@id=\"$id\"");
}
`
	document := lsp.NewTextDocument(
		"file:///project/src/CrawlerTest.php",
		source,
		1,
	)
	result, err := NewEmbeddedXPathAnalyzer(
		phpIndex,
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 2)
	for _, diagnostic := range result {
		assert.Equal(t, invalidEmbeddedXPathCode, diagnostic.ID)
		assert.Contains(t, diagnostic.Message, "Invalid XPath expression:")
		line, _ := document.LineIndex.Position(diagnostic.Range.Start)
		assert.Contains(t, []uint32{5, 6}, line)
	}
}
