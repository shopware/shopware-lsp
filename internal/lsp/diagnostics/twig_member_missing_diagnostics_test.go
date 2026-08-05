package diagnostics

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
)

func TestTwigMemberMissingDiagnosticsReportsOnlyDefiniteObjects(
	t *testing.T,
) {
	cache := t.TempDir()
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	sourcePath := filepath.Join(
		t.TempDir(),
		"src",
		"Model.php",
	)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		sourcePath,
		[]byte(`<?php
namespace App;
class Category {
    public string $title;
}
class Product {
    public const FOO = 'foo';
    public string $name;
    private string $secret;
    public function getCategory(): Category {}
    public function isActive(): bool {}
}
class MagicModel {
    public function __get(string $name): mixed {}
}
class ProductCollection implements \IteratorAggregate {
    public function getIterator(): \Traversable {}
}
`),
	)))
	document := lsp.NewTextDocument(
		"file:///project/templates/product.html.twig",
		`{# @var product \App\Product #}
{# @var nullable \App\Product|null #}
{# @var magic \App\MagicModel #}
{# @var collection \App\ProductCollection #}
{# @var list list<\App\Product> #}
{# @var unknown \App\NotIndexed #}
{# @var broad object #}
{# @var scalar string #}
{{ product.name }}
{{ product.FOO }}
{{ product.category.title }}
{{ product.active }}
{{ product.missing }}
{{ product.category.missingNested }}
{{ product.missing.deep }}
{{ product.secret }}
{{ nullable.missingNullable }}
{{ magic.anything }}
{{ collection.anything }}
{{ list.anything }}
{{ unknown.anything }}
{{ broad.anything }}
{{ scalar.anything }}
`,
		1,
	)
	provider := NewTwigMemberMissingAnalyzer(nil, phpIndex)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	assertTwigMissingMemberDiagnostics(t, document, result)
	require.NoError(t, phpIndex.Close())

	reopened, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	restored, err := NewTwigMemberMissingAnalyzer(
		nil,
		reopened,
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Equal(t, result, restored)
}

func TestTwigMemberMissingDiagnosticsSupportsKnownUnionMembers(
	t *testing.T,
) {
	index, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/src/Union.php",
		[]byte(`<?php
namespace App;
class First { public string $shared; }
class Second { public function getShared(): string {} }
class DynamicSecond { public function __call(string $name, array $args): mixed {} }
`),
	)))
	document := lsp.NewTextDocument(
		"file:///project/templates/union.html.twig",
		`{# @var known \App\First|\App\Second #}
{# @var dynamic \App\First|\App\DynamicSecond #}
{{ known.shared }}
{{ known.missing }}
{{ dynamic.missing }}
`,
		1,
	)
	result, err := NewTwigMemberMissingAnalyzer(
		nil,
		index,
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "missing", problemRangeText(document, result[0].Range))
	assert.Contains(t, result[0].Message, "App\\First|App\\Second")
}

func TestTwigMemberMissingDiagnosticsUsesTwigCallableReturnTypes(
	t *testing.T,
) {
	phpIndex, twigIndex := twigMemberDeprecationIndexes(t)
	document := lsp.NewTextDocument(
		"file:///project/templates/callable.html.twig",
		`{{ ustring('value').deprecated }}
{{ ustring(text: 'value').unknownFunctionMember }}
{{ 'value'|u.deprecated }}
{{ 'value'|u.unknownFilterMember }}
`,
		1,
	)
	result, err := NewTwigMemberMissingAnalyzer(
		twigIndex,
		phpIndex,
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.ElementsMatch(t, []string{
		"unknownFunctionMember",
		"unknownFilterMember",
	}, []string{
		problemRangeText(document, result[0].Range),
		problemRangeText(document, result[1].Range),
	})
}

func assertTwigMissingMemberDiagnostics(
	t *testing.T,
	document *lsp.TextDocument,
	diagnostics []lsp.Problem,
) {
	t.Helper()
	require.Len(t, diagnostics, 5)
	var ranges []string
	for _, diagnostic := range diagnostics {
		assert.Equal(t, missingTwigMemberCode, diagnostic.ID)
		assert.Equal(t, "twig", diagnostic.Source)
		assert.Equal(
			t,
			protocol.DiagnosticSeverityWarning,
			diagnostic.Severity,
		)
		ranges = append(
			ranges,
			problemRangeText(document, diagnostic.Range),
		)
	}
	assert.ElementsMatch(t, []string{
		"missing",
		"missingNested",
		"missing",
		"secret",
		"missingNullable",
	}, ranges)
}

func problemRangeText(document *lsp.TextDocument, rng cst.TextRange) string {
	return document.Source[rng.Start:rng.End]
}
