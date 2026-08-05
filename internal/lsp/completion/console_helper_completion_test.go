package completion

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestConsoleHelperCompletionUsesTypedHelperSetAndCommand(t *testing.T) {
	phpIndex, root := consoleHelperCompletionPHPIndex(t)
	provider := NewConsoleHelperCompletionProvider(phpIndex)
	for _, test := range []struct {
		name   string
		source string
		needle string
	}{
		{
			name: "helper set",
			source: `<?php
use Symfony\Component\Console\Helper\HelperSet;
function inspect(HelperSet $helpers): void { $helpers->get('que'); }`,
			needle: "que",
		},
		{
			name: "command",
			source: `<?php
use Symfony\Component\Console\Command\Command;
function inspect(Command $command): void { $command->getHelper('que'); }`,
			needle: "que",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(root, "src", test.name+".php")
			document := lsp.NewTextDocument(uriutil.FileURI(path), test.source, 1)
			offset := uint32(strings.LastIndex(test.source, test.needle) + 1)
			node := document.SyntaxTree.Root.NodeAtOffset(offset)
			ctx := phpIndex.AddDocumentContext(
				context.Background(),
				path,
				1,
				node,
				document.SyntaxTree.Root,
			)
			item := requireCompletion(
				t,
				provider.GetCompletions(
					ctx,
					consoleCompletionRequest(document, node),
				),
				"question",
			)
			assert.Equal(t, int(protocol.ReferenceCompletion), item.Kind)
			assert.Equal(t, "App\\QuestionHelper", item.Detail)
			edit, ok := item.TextEdit.(protocol.TextEdit)
			require.True(t, ok)
			assert.Equal(t, "que", completionRangeText(document, edit.Range))
			assert.Equal(t, "question", edit.NewText)
		})
	}
}

func TestConsoleHelperCompletionRejectsUnrelatedGet(t *testing.T) {
	phpIndex, root := consoleHelperCompletionPHPIndex(t)
	source := `<?php
class Repository { public function get(string $name): object {} }
function inspect(Repository $repository): void { $repository->get('que'); }`
	path := filepath.Join(root, "src", "Repository.php")
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.LastIndex(source, "que") + 1)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		document.SyntaxTree.Root,
	)
	assert.Empty(t, NewConsoleHelperCompletionProvider(phpIndex).GetCompletions(
		ctx,
		consoleCompletionRequest(document, node),
	))
}

func consoleHelperCompletionPHPIndex(
	t *testing.T,
) (*php.PHPIndex, string) {
	t.Helper()
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "vendor", "Console.php"),
		[]byte(`<?php
namespace Symfony\Component\Console\Helper;
interface HelperInterface { public function getName(): string; }
abstract class Helper implements HelperInterface {}
class HelperSet {
    public function get(string $name): HelperInterface {}
    public function has(string $name): bool {}
}
namespace Symfony\Component\Console\Command;
class Command { public function getHelper(string $name): object {} }
`),
	)))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "src", "QuestionHelper.php"),
		[]byte(`<?php
namespace App;
use Symfony\Component\Console\Helper\Helper;
/** Helps ask interactive questions. */
class QuestionHelper extends Helper {
    public function getName(): string { return 'question'; }
}`),
	)))
	return phpIndex, root
}
