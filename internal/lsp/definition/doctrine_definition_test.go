package definition

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestDoctrineDefinitionNavigatesTypeRegistrationClass(t *testing.T) {
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	typePath := "/project/src/MoneyType.php"
	for path, source := range map[string]string{
		"/project/vendor/Type.php": `<?php
namespace Doctrine\DBAL\Types;
abstract class Type {}`,
		typePath: `<?php
namespace App\Doctrine;
class MoneyType extends \Doctrine\DBAL\Types\Type {}`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	source := `<?php
use App\Doctrine\MoneyType;
use Doctrine\DBAL\Types\Type;

Type::addType('money', MoneyType::class);`
	document := lsp.NewTextDocument(
		"file:///project/bootstrap.php",
		source,
		1,
	)
	for _, needle := range []string{"money", "MoneyType::class"} {
		offset := uint32(strings.Index(source, needle) + 1)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		locations := NewDoctrineDefinitionProvider(
			doctrineIndex,
			phpIndex,
		).GetDefinition(
			context.Background(),
			securityDefinitionRequest(document, node, offset),
		)
		require.Len(t, locations, 1)
		require.Equal(t, uriutil.FileURI(typePath), locations[0].URI)
	}
}
