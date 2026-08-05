package diagnostics

import (
	"context"
	"fmt"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvokableCommandDiagnostics(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/Command.php",
		[]byte(`<?php
namespace Symfony\Component\Console\Command;
class Command {
    public const SUCCESS = 0;
}`),
	)))

	source := `<?php
namespace App\Command;

use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;

#[AsCommand(name: 'app:invalid')]
class InvalidCommand
{
    public function __invoke()
    {
        $exitCode = 0;
        if (rand(0, 1)) {
            return $exitCode;
        }
        if (rand(0, 1)) {
            return Command::SUCCESS;
        }
        return 'error';
    }
}

#[AsCommand(name: 'app:typed')]
class TypedCommand
{
    public function __invoke(): int
    {
        return 'the PHP type checker owns this mismatch';
    }
}

#[AsCommand(name: 'app:wrong')]
class WrongTypeCommand
{
    public function __invoke(): string
    {
        return null;
    }
}

class TraditionalCommand extends Command
{
    public function __invoke(): void {}
}
`
	document := lsp.NewTextDocument(
		"file:///project/src/Command/Commands.php",
		source,
		1,
	)
	result, err := NewInvokableCommandAnalyzer(phpIndex).
		Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 4, fmt.Sprintf("%#v", result))

	var typeDiagnostics, valueDiagnostics int
	for _, diagnostic := range result {
		switch diagnostic.ID {
		case invokableCommandReturnTypeCode:
			typeDiagnostics++
			assert.Equal(t, invokableCommandReturnTypeMessage, diagnostic.Message)
		case invokableCommandReturnValueCode:
			valueDiagnostics++
			assert.Equal(t, invokableCommandReturnValueMessage, diagnostic.Message)
		}
	}
	assert.Equal(t, 2, typeDiagnostics)
	assert.Equal(t, 2, valueDiagnostics)

	assert.Equal(
		t,
		"__invoke",
		problemRangeText(document, result[0].Range),
	)
	assert.Equal(
		t,
		"'error'",
		problemRangeText(document, result[1].Range),
	)
	assert.Nil(t, result[0].Payload)
}

func TestInvokableCommandDiagnosticsReportBareReturn(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	document := lsp.NewTextDocument(
		"file:///project/BareCommand.php",
		`<?php
use Symfony\Component\Console\Attribute\AsCommand;

#[AsCommand]
class BareCommand {
    public function __invoke(): void {
        return;
    }
}`,
		1,
	)
	result, err := NewInvokableCommandAnalyzer(phpIndex).
		Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, invokableCommandReturnTypeCode, result[0].ID)
	assert.Nil(t, result[0].Payload)
	assert.Equal(t, invokableCommandReturnValueCode, result[1].ID)
	assert.Equal(
		t,
		"return;",
		problemRangeText(document, result[1].Range),
	)
}
