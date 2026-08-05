package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestConsoleHelperDefinitionNavigatesToHelperClass(t *testing.T) {
	root := t.TempDir()
	helperPath := filepath.Join(root, "src", "QuestionHelper.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(helperPath), 0o755))
	helperSource := `<?php
namespace App;
use Symfony\Component\Console\Helper\Helper;
class QuestionHelper extends Helper {
    public function getName(): string { return 'question'; }
}`
	require.NoError(t, os.WriteFile(helperPath, []byte(helperSource), 0o600))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "vendor", "Console.php"),
		[]byte(`<?php
namespace Symfony\Component\Console\Helper;
interface HelperInterface { public function getName(): string; }
abstract class Helper implements HelperInterface {}
class HelperSet { public function get(string $name): HelperInterface {} }
`),
	)))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		helperPath,
		[]byte(helperSource),
	)))

	path := filepath.Join(root, "src", "Consumer.php")
	source := `<?php
use Symfony\Component\Console\Helper\HelperSet;
function inspect(HelperSet $helpers): void {
    $helpers->get('question');
}`
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.LastIndex(source, "question") + 1)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		document.SyntaxTree.Root,
	)
	locations := NewConsoleHelperDefinitionProvider(phpIndex).GetDefinition(
		ctx,
		consoleDefinitionRequest(document, node),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(helperPath), locations[0].URI)
	assert.Equal(t, 3, locations[0].Range.Start.Line)
	assert.Equal(t, 6, locations[0].Range.Start.Character)
	assert.Equal(t, 20, locations[0].Range.End.Character)
}
