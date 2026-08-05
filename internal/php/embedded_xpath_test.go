package php

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
)

func TestEmbeddedXPathExpressionsResolveSymfonyCrawler(t *testing.T) {
	index, err := NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
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
final class CustomCrawler extends Crawler {}
final class Other { public function evaluate(string $expression): void {} }
function find(
    Crawler $crawler,
    CustomCrawler $custom,
    Other $other,
    string $id,
): void {
    $crawler->filterXPath('.//article[@data-id="42"]');
    $custom->evaluate("count(.//article[@id=\"main\"])");
    $other->evaluate('.//ignored[');
    $crawler->evaluate(".//article[@id=\"$id\"]");
}
`
	parsed := phpparser.Parse(source)
	require.Empty(t, parsed.Errors)
	expressions := EmbeddedXPathExpressions(
		index,
		"/project/src/CrawlerTest.php",
		1,
		source,
		parsed.Tree.Root,
	)
	require.Len(t, expressions, 2)
	assert.Equal(t, `.//article[@data-id="42"]`, expressions[0].Value)
	assert.Equal(
		t,
		`count(.//article[@id="main"])`,
		expressions[1].Value,
	)
}
