package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsoleInputArgumentAndOptionCompletion(t *testing.T) {
	provider, phpIndex, source, path := consoleCompletionFixture(t)
	document := lsp.NewTextDocument("file://"+path, source, 1)

	for _, test := range []struct {
		needle string
		label  string
	}{
		{"getArgument('')", "username"},
		{"getOption('')", "admin"},
	} {
		offset := strings.Index(source, test.needle) +
			strings.Index(test.needle, "''") + 1
		node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			path,
			1,
			node,
			document.SyntaxTree.Root,
		)
		items := provider.GetCompletions(
			ctx,
			consoleCompletionRequest(document, node),
		)
		item := requireCompletion(t, items, test.label)
		assert.Equal(t, int(protocol.FieldCompletion), item.Kind)
	}
}

func TestConsoleCommandNameCompletionRequiresApplicationType(t *testing.T) {
	provider, phpIndex, _, _ := consoleCompletionFixture(t)
	source := `<?php
namespace App;
use Symfony\Component\Console\Application;
function findCommand(Application $application) {
    return $application->find('');
}`
	path := "/project/src/Runner.php"
	document := lsp.NewTextDocument("file://"+path, source, 1)
	offset := strings.Index(source, "''") + 1
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		document.SyntaxTree.Root,
	)
	items := provider.GetCompletions(
		ctx,
		consoleCompletionRequest(document, node),
	)
	command := requireCompletion(t, items, "app:user:create")
	assert.Equal(t, int(protocol.ReferenceCompletion), command.Kind)

	untyped := `<?php $repository->find('');`
	untypedDocument := lsp.NewTextDocument(
		"file:///project/src/Repository.php",
		untyped,
		1,
	)
	untypedNode := untypedDocument.SyntaxTree.Root.NodeAtOffset(
		uint32(strings.Index(untyped, "''") + 1),
	)
	untypedContext := phpIndex.AddDocumentContext(
		context.Background(),
		"/project/src/Repository.php",
		1,
		untypedNode,
		untypedDocument.SyntaxTree.Root,
	)
	assert.Empty(t, provider.GetCompletions(
		untypedContext,
		consoleCompletionRequest(untypedDocument, untypedNode),
	))
}

func consoleCompletionFixture(
	t *testing.T,
) (*ConsoleCompletionProvider, *php.PHPIndex, string, string) {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/Console.php",
		[]byte(`<?php
namespace Symfony\Component\Console\Input;
interface InputInterface {
    public function getArgument(string $name): mixed;
    public function getOption(string $name): mixed;
}
namespace Symfony\Component\Console;
class Application {
    public function find(string $name): object {}
}`),
	)))
	consoleIndex, err := console.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, consoleIndex.Close()) })

	source := `<?php
namespace App\Command;
use Symfony\Component\Console\Input\InputInterface;

#[AsCommand(name: 'app:user:create', description: 'Create user')]
class CreateUserCommand
{
    protected function configure(): void
    {
        $this->addArgument('username', description: 'User name');
        $this->addOption('admin', 'a', description: 'Grant admin');
    }

    public function execute(InputInterface $input): void
    {
        $input->getArgument('');
        $input->getOption('');
    }
}`
	path := "/project/src/Command/CreateUserCommand.php"
	parsed := indexer.NewParsedFile(path, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, consoleIndex.Index(parsed))
	return NewConsoleCompletionProvider(consoleIndex), phpIndex, source, path
}

func consoleCompletionRequest(
	document *lsp.TextDocument,
	node *cst.Node,
) *lsp.CompletionRequest {
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	return &lsp.CompletionRequest{
		CompletionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            node,
		},
	}
}
