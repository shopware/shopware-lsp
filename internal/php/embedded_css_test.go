package php

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
)

func TestEmbeddedCSSSelectorsResolveSymfonyReceivers(t *testing.T) {
	index, err := NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
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
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}

	source := `<?php
namespace App;
use Symfony\Component\DomCrawler\Crawler;
use Symfony\Component\CssSelector\CssSelectorConverter;

final class CustomCrawler extends Crawler {}
final class Other {
    public function filter(string $selector): void {}
}

function find(
    Crawler $crawler,
    CustomCrawler $custom,
    CssSelectorConverter $converter,
    Other $other,
    string $name,
): void {
    $crawler->filter('.product[data-active="true"]');
    $crawler->children("ul > li:nth-child(2)");
    $custom->filter('#main .card');
    $converter->toXPath('[data-controller~="cart"]');
    $other->filter('.ignored[');
    $crawler->filter(".dynamic-$name");
}
`
	parsed := phpparser.Parse(source)
	require.Empty(t, parsed.Errors)
	selectors := EmbeddedCSSSelectors(
		index,
		"/project/src/CrawlerTest.php",
		1,
		source,
		parsed.Tree.Root,
	)
	require.Len(t, selectors, 4)
	assert.Equal(t, `.product[data-active="true"]`, selectors[0].Value)
	assert.Equal(t, "ul > li:nth-child(2)", selectors[1].Value)
	assert.Equal(t, "#main .card", selectors[2].Value)
	assert.Equal(t, `[data-controller~="cart"]`, selectors[3].Value)
}
