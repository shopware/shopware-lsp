package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsoleDefinitionNavigatesCommandsAndInputs(t *testing.T) {
	root := t.TempDir()
	commandPath := filepath.Join(root, "src", "CreateUserCommand.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(commandPath), 0o755))
	source := `<?php
namespace App\Command;
use Symfony\Component\Console\Application;
use Symfony\Component\Console\Input\InputInterface;

#[AsCommand('app:user:create', aliases: [self::CREATE_ALIAS])]
class CreateUserCommand {
    private const CREATE_ALIAS = 'app:user:add';

    protected function configure(): void {
        $this->addOption('admin', 'a');
    }
    public function execute(InputInterface $input): void {
        $input->getOption('admin');
    }
}

function run(Application $application): void {
    $application->find('app:user:create');
    $application->find('app:user:add');
}`
	require.NoError(t, os.WriteFile(commandPath, []byte(source), 0o644))

	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/Console.php",
		[]byte(`<?php
namespace Symfony\Component\Console\Input;
interface InputInterface { public function getOption(string $name): mixed; }
namespace Symfony\Component\Console;
class Application { public function find(string $name): object {} }`),
	)))
	consoleIndex, err := console.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, consoleIndex.Close()) })
	parsed := indexer.NewParsedFile(commandPath, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, consoleIndex.Index(parsed))

	document := lsp.NewTextDocument(uriutil.FileURI(commandPath), source, 1)
	provider := NewConsoleDefinitionProvider(consoleIndex)
	for _, test := range []struct {
		needle    string
		line      int
		character int
	}{
		{"getOption('admin')", 10, -1},
		{"find('app:user:create')", 5, -1},
		{
			"find('app:user:add')",
			5,
			strings.Index(
				strings.Split(source, "\n")[5],
				"self::CREATE_ALIAS",
			),
		},
	} {
		stringOffset := strings.Index(source, test.needle) +
			strings.Index(test.needle, "'") + 1
		node := document.SyntaxTree.Root.NodeAtOffset(uint32(stringOffset))
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			commandPath,
			1,
			node,
			document.SyntaxTree.Root,
		)
		locations := provider.GetDefinition(
			ctx,
			consoleDefinitionRequest(document, node),
		)
		require.Len(t, locations, 1, test.needle)
		assert.Equal(t, uriutil.FileURI(commandPath), locations[0].URI)
		assert.Equal(t, test.line, locations[0].Range.Start.Line)
		if test.character >= 0 {
			assert.Equal(
				t,
				test.character,
				locations[0].Range.Start.Character,
			)
		}
	}
}

func consoleDefinitionRequest(
	document *lsp.TextDocument,
	node *cst.Node,
) *lsp.DefinitionRequest {
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	return &lsp.DefinitionRequest{
		DefinitionParams: params,
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
