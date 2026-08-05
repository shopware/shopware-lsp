package codeaction

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandInvokeParameterCodeActionOffersAvailableTypes(t *testing.T) {
	source := `<?php
namespace App\Command;
use Symfony\Component\Console\Attribute\AsCommand as ConsoleCommand;
use Symfony\Component\Console\Style\SymfonyStyle as Style;

#[ConsoleCommand(name: 'app:test')]
class TestCommand
{
    public function __invoke(Style $io): int
    {
        return 0;
    }
}`
	actions := commandInvokeParameterFixture(t).GetCodeActions(
		context.Background(),
		commandInvokeParameterRequest(source, "TestCommand"),
	)

	require.Len(t, actions, 4)
	titles := make([]string, 0, len(actions))
	for _, action := range actions {
		titles = append(titles, action.Title)
		assert.Equal(t, protocol.CodeActionRefactorRewrite, action.Kind)
	}
	assert.Contains(
		t,
		titles,
		"Symfony: Add InputInterface parameter to __invoke",
	)
	assert.Contains(
		t,
		titles,
		"Symfony: Add OutputInterface parameter to __invoke",
	)
	assert.NotContains(
		t,
		titles,
		"Symfony: Add SymfonyStyle parameter to __invoke",
	)
}

func TestCommandInvokeParameterCodeActionRejectsInvalidClasses(t *testing.T) {
	allParameters := `InputInterface $input,
        OutputInterface $output,
        Cursor $cursor,
        SymfonyStyle $io,
        Application $application`
	for _, fixture := range []struct {
		name   string
		source string
	}{
		{
			name: "without AsCommand",
			source: `<?php
class TestCommand
{
    public function __invoke(): int {}
}`,
		},
		{
			name: "without invoke",
			source: `<?php
use Symfony\Component\Console\Attribute\AsCommand;
#[AsCommand]
class TestCommand
{
    public function execute(): int {}
}`,
		},
		{
			name: "direct legacy Command subclass",
			source: `<?php
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
#[AsCommand]
class TestCommand extends Command
{
    public function __invoke(): int {}
}`,
		},
		{
			name: "indirect legacy Command subclass",
			source: `<?php
namespace App;
use Symfony\Component\Console\Attribute\AsCommand;
#[AsCommand]
class TestCommand extends BaseLegacyCommand
{
    public function __invoke(): int {}
}`,
		},
		{
			name: "all parameters exist",
			source: `<?php
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Cursor;
use Symfony\Component\Console\Style\SymfonyStyle;
use Symfony\Component\Console\Application;
#[AsCommand]
class TestCommand
{
    public function __invoke(
        ` + allParameters + `
    ): int {}
}`,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			actions := commandInvokeParameterFixture(t).
				GetCodeActions(
					context.Background(),
					commandInvokeParameterRequest(
						fixture.source,
						"TestCommand",
					),
				)
			assert.Empty(t, actions)
		})
	}
}

func TestCommandInvokeParameterCodeActionInsertsBeforeOptionalParameter(
	t *testing.T,
) {
	source := `<?php
namespace App\Command;
use Symfony\Component\Console\Attribute\AsCommand;
#[AsCommand]
class TestCommand
{
    public function __invoke(?string $format = null): int {}
}`
	request := commandInvokeParameterRequest(source, "__invoke")
	action := commandInvokeParameterAction(
		t,
		commandInvokeParameterFixture(t).GetCodeActions(
			context.Background(),
			request,
		),
		"SymfonyStyle",
	)
	edits := action.Edit.Changes[request.TextDocument.URI]

	require.Len(t, edits, 2)
	assert.Equal(t, "SymfonyStyle $io, ", edits[0].NewText)
	assert.Contains(
		t,
		edits[1].NewText,
		"use Symfony\\Component\\Console\\Style\\SymfonyStyle;",
	)
}

func TestCommandInvokeParameterCodeActionPreservesMultilineStyle(t *testing.T) {
	for _, fixture := range []struct {
		name       string
		parameters string
		typeName   string
		newText    string
	}{
		{
			name:       "empty",
			parameters: "\n    ",
			typeName:   "InputInterface",
			newText:    "\n        InputInterface $input",
		},
		{
			name:       "trailing comma",
			parameters: "\n        string $name,\n    ",
			typeName:   "OutputInterface",
			newText:    "\n        OutputInterface $output,",
		},
		{
			name:       "without trailing comma",
			parameters: "\n        string $name\n    ",
			typeName:   "Cursor",
			newText:    ",\n        Cursor $cursor",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source := `<?php
use Symfony\Component\Console\Attribute\AsCommand;
#[AsCommand]
class TestCommand
{
    public function __invoke(` + fixture.parameters + `): int {}
}`
			request := commandInvokeParameterRequest(source, "__invoke")
			action := commandInvokeParameterAction(
				t,
				commandInvokeParameterFixture(t).GetCodeActions(
					context.Background(),
					request,
				),
				fixture.typeName,
			)
			edits := action.Edit.Changes[request.TextDocument.URI]
			require.NotEmpty(t, edits)
			assert.Equal(t, fixture.newText, edits[0].NewText)
		})
	}
}

func TestCommandInvokeParameterCodeActionAvoidsImportConflict(t *testing.T) {
	source := `<?php
use App\Model\Cursor;
use Symfony\Component\Console\Attribute\AsCommand;
#[AsCommand]
class TestCommand
{
    public function __invoke(): int {}
}`
	request := commandInvokeParameterRequest(source, "__invoke")
	action := commandInvokeParameterAction(
		t,
		commandInvokeParameterFixture(t).GetCodeActions(
			context.Background(),
			request,
		),
		"Cursor",
	)
	edits := action.Edit.Changes[request.TextDocument.URI]

	require.Len(t, edits, 1)
	assert.Equal(
		t,
		`\Symfony\Component\Console\Cursor $cursor`,
		edits[0].NewText,
	)
}

func TestCommandInvokeParameterCodeActionInsertsBeforeVariadic(t *testing.T) {
	source := `<?php
use Symfony\Component\Console\Attribute\AsCommand;
#[AsCommand]
class TestCommand
{
    public function __invoke(string ...$values): int {}
}`
	request := commandInvokeParameterRequest(source, "__invoke")
	action := commandInvokeParameterAction(
		t,
		commandInvokeParameterFixture(t).GetCodeActions(
			context.Background(),
			request,
		),
		"InputInterface",
	)
	edits := action.Edit.Changes[request.TextDocument.URI]

	require.Len(t, edits, 2)
	assert.Equal(
		t,
		"InputInterface $input, ",
		edits[0].NewText,
	)
}

func commandInvokeParameterFixture(
	t *testing.T,
) *CommandInvokeParameterCodeActionProvider {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/vendor/ConsoleCommandStubs.php",
		[]byte(`<?php
namespace Symfony\Component\Console\Command;
abstract class Command {}
namespace App;
abstract class BaseLegacyCommand extends \Symfony\Component\Console\Command\Command {}
`),
	)))
	return NewCommandInvokeParameterCodeActionProvider(phpIndex)
}

func commandInvokeParameterRequest(
	source,
	needle string,
) *lsp.CodeActionRequest {
	document := lsp.NewTextDocument(
		"file:///project/src/TestCommand.php",
		source,
		1,
	)
	offset := strings.Index(source, needle)
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.CodeActionParams{
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      int(line),
				Character: int(character),
			},
			End: protocol.Position{
				Line:      int(line),
				Character: int(character + uint32(len(needle))),
			},
		},
	}
	params.TextDocument.URI = document.URI
	return &lsp.CodeActionRequest{
		CodeActionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node: document.SyntaxTree.Root.NodeAtOffset(
				uint32(offset),
			),
		},
	}
}

func commandInvokeParameterAction(
	t *testing.T,
	actions []protocol.CodeAction,
	typeName string,
) protocol.CodeAction {
	t.Helper()
	for _, action := range actions {
		if strings.Contains(action.Title, "Add "+typeName+" parameter") {
			return action
		}
	}
	require.FailNow(t, "missing parameter action", typeName)
	return protocol.CodeAction{}
}
