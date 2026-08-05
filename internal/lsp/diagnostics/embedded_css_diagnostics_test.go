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

func TestEmbeddedCSSDiagnosticsValidateTypedDomCrawlerSelectors(
	t *testing.T,
) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	for path, source := range map[string]string{
		"/project/vendor/Crawler.php": `<?php
namespace Symfony\Component\DomCrawler;
class Crawler {
    public function filter(string $selector): static {}
    public function children(string $selector): static {}
}
`,
		"/project/vendor/CssSelectorConverter.php": `<?php
namespace Symfony\Component\CssSelector;
class CssSelectorConverter {
    public function toXPath(string $selector): string {}
}
`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	source := `<?php
use Symfony\Component\DomCrawler\Crawler;
use Symfony\Component\CssSelector\CssSelectorConverter;
final class Other { public function filter(string $selector): void {} }
function find(
    Crawler $crawler,
    CssSelectorConverter $converter,
    Other $other,
    string $name,
): void {
    $crawler->filter('.product > a[data-active="true"]');
    $crawler->children('.broken[');
    $converter->toXPath('button:not(');
    $other->filter('.ignored[');
    $crawler->filter(".dynamic-$name[");
}
`
	document := lsp.NewTextDocument(
		"file:///project/src/CrawlerTest.php",
		source,
		1,
	)
	result, err := NewEmbeddedCSSAnalyzer(
		phpIndex,
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 2)
	for _, diagnostic := range result {
		assert.Equal(t, invalidEmbeddedCSSCode, diagnostic.ID)
		assert.Contains(t, diagnostic.Message, "Invalid CSS selector:")
		line, _ := document.LineIndex.Position(diagnostic.Range.Start)
		assert.Contains(t, []uint32{11, 12}, line)
	}
}
