package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/require"
)

func TestDoctrineTypeRegistrationClassCompletion(t *testing.T) {
	doctrineIndex, phpIndex := doctrineCompletionFixture(t)
	for path, source := range map[string]string{
		"/project/vendor/dbal.php": `<?php
namespace Doctrine\DBAL\Types;
abstract class Type {}`,
		"/project/src/Types.php": `<?php
namespace App;
class Product {}
class MoneyType extends \Doctrine\DBAL\Types\Type {}`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	provider := NewDoctrineCompletionProvider(doctrineIndex, phpIndex)
	tests := []struct {
		path       string
		source     string
		offset     uint32
		label      string
		insertText string
	}{
		{
			path: "/project/config/packages/doctrine.yaml",
			source: `doctrine:
  dbal:
    types:
      money:
        class:
`,
			offset: uint32(len(`doctrine:
  dbal:
    types:
      money:
        class:`)),
			label: "App\\MoneyType",
		},
		{
			path:   "/project/config/packages/doctrine.xml",
			source: `<container xmlns:doctrine="urn:doctrine"><doctrine:config><doctrine:dbal><doctrine:type name="money" class=""/></doctrine:dbal></doctrine:config></container>`,
			offset: uint32(strings.Index(`<container xmlns:doctrine="urn:doctrine"><doctrine:config><doctrine:dbal><doctrine:type name="money" class=""/></doctrine:dbal></doctrine:config></container>`, `class=""`) + len(`class="`)),
			label:  "App\\MoneyType",
		},
		{
			path: "/project/config/packages/doctrine.php",
			source: `<?php
return static function ($containerConfigurator): void {
    $containerConfigurator->extension('doctrine', [
        'dbal' => ['types' => ['money' => '']],
    ]);
};`,
			offset: uint32(strings.Index(`<?php
return static function ($containerConfigurator): void {
    $containerConfigurator->extension('doctrine', [
        'dbal' => ['types' => ['money' => '']],
    ]);
};`, `=> '']`) + len(`=> '`)),
			label: "App\\MoneyType",
		},
		{
			path: "/project/config/packages/doctrine.php",
			source: `<?php
return static function ($containerConfigurator): void {
    $containerConfigurator->extension('doctrine', [
        'dbal' => ['types' => ['money' => ]],
    ]);
};`,
			offset: uint32(strings.Index(`<?php
return static function ($containerConfigurator): void {
    $containerConfigurator->extension('doctrine', [
        'dbal' => ['types' => ['money' => ]],
    ]);
};`, `=> ]]`) + len(`=> `)),
			label:      "MoneyType",
			insertText: `\App\MoneyType::class`,
		},
		{
			path: "/project/bootstrap.php",
			source: `<?php
use Doctrine\DBAL\Types\Type;
Type::addType('money', );`,
			offset: uint32(strings.LastIndex(`<?php
use Doctrine\DBAL\Types\Type;
Type::addType('money', );`, ")")),
			label:      "MoneyType",
			insertText: `\App\MoneyType::class`,
		},
		{
			path: "/project/bootstrap.php",
			source: `<?php
use Doctrine\DBAL\Types\Type;
Type::getTypeRegistry()->register('money', );`,
			offset: uint32(strings.LastIndex(`<?php
use Doctrine\DBAL\Types\Type;
Type::getTypeRegistry()->register('money', );`, ")")),
			label:      "MoneyType",
			insertText: `new \App\MoneyType()`,
		},
		{
			path: "/project/bootstrap.php",
			source: `<?php
use Doctrine\DBAL\Types\Type;
Type::getTypeRegistry()->register('money', new );`,
			offset: uint32(strings.LastIndex(`<?php
use Doctrine\DBAL\Types\Type;
Type::getTypeRegistry()->register('money', new );`, ")")),
			label:      "MoneyType",
			insertText: `\App\MoneyType()`,
		},
	}
	for _, test := range tests {
		document := lsp.NewTextDocument(
			"file://"+test.path,
			test.source,
			1,
		)
		node := document.SyntaxTree.Root.NodeAtOffset(test.offset)
		items := provider.GetCompletions(
			context.Background(),
			completionRequestAt(document, node, test.offset),
		)
		item := requireCompletion(t, items, test.label)
		require.Equal(t, "Doctrine DBAL type class", item.Detail)
		if test.insertText != "" {
			require.Equal(t, test.insertText, item.InsertText)
		}
		for _, candidate := range items {
			require.NotEqual(t, "App\\Product", candidate.Label)
		}
	}
}

func doctrineCompletionFixture(
	t *testing.T,
) (*doctrine.Index, *php.PHPIndex) {
	t.Helper()
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	return doctrineIndex, phpIndex
}
