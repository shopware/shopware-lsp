package hover

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsoleCommandHover(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/Application.php",
		[]byte(`<?php
namespace Symfony\Component\Console;
class Application { public function find(string $name): object {} }`),
	)))
	consoleIndex, err := console.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, consoleIndex.Close()) })
	require.NoError(t, consoleIndex.Index(indexer.NewParsedFile(
		"/project/src/CreateUserCommand.php",
		[]byte(`<?php
namespace App;
#[AsCommand(name: 'app:user:create', description: 'Creates a user')]
class CreateUserCommand {
    protected function configure(): void {
        $this->addArgument('username');
        $this->addOption('admin');
    }
}`),
	)))

	source := `<?php
use Symfony\Component\Console\Application;
function run(Application $application): void {
    $application->find('app:user:create');
}`
	path := "/project/src/Runner.php"
	document := lsp.NewTextDocument("file://"+path, source, 1)
	offset := strings.Index(source, "app:user:create") + 1
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		document.SyntaxTree.Root,
	)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	result, err := NewConsoleHoverProvider(
		"/project",
		consoleIndex,
	).GetHover(ctx, &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            node,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Symfony command")
	assert.Contains(t, result.Contents.Value, "Creates a user")
	assert.Contains(t, result.Contents.Value, "1 arguments · 1 options")
}
