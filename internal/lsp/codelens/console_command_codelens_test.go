package codelens

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsoleCommandCodeLensesCoverAttributeAndLegacyDeclarations(
	t *testing.T,
) {
	root := symfonyConsoleCodeLensRoot(t)
	path := filepath.Join(root, "src", "Commands.php")
	source := `<?php
namespace App\Command;

#[AsCommand(name: 'app:user:create', aliases: ['app:user:add'])]
final class CreateUserCommand {}

final class UserCommands
{
    #[AsCommand('app:user:delete')]
    public function delete(): int { return 0; }
}

final class LegacyCommand
{
    protected static $defaultName = 'app:legacy|app:old';
}

final class ConfiguredCommand
{
    protected function configure(): void
    {
        $this->setName('app:configured');
    }
}
`
	lenses := relatedCodeLensesFor(
		t,
		NewConsoleCommandCodeLensProvider(root),
		path,
		source,
	)
	require.Len(t, lenses, 4)
	assert.Equal(t, []string{
		"Run app:user:create",
		"Run app:user:delete",
		"Run app:legacy",
		"Run app:configured",
	}, relatedLensTitles(lenses))
	assert.Equal(t, []int{3, 8, 14, 21}, []int{
		lenses[0].Range.Start.Line,
		lenses[1].Range.Start.Line,
		lenses[2].Range.Start.Line,
		lenses[3].Range.Start.Line,
	})
	for index, name := range []string{
		"app:user:create",
		"app:user:delete",
		"app:legacy",
		"app:configured",
	} {
		require.NotNil(t, lenses[index].Command)
		assert.Equal(t, runSymfonyCommandID, lenses[index].Command.Command)
		assert.Equal(
			t,
			[]any{name, uriutil.FileURI(path)},
			lenses[index].Command.Arguments,
		)
	}
}

func TestConsoleCommandCodeLensesUseUnsavedCommandName(t *testing.T) {
	root := symfonyConsoleCodeLensRoot(t)
	path := filepath.Join(root, "src", "DraftCommand.php")
	lenses := relatedCodeLensesFor(
		t,
		NewConsoleCommandCodeLensProvider(root),
		path,
		`<?php
#[AsCommand('app:unsaved')]
final class DraftCommand {}
`,
	)
	require.Len(t, lenses, 1)
	assert.Equal(t, "Run app:unsaved", lenses[0].Command.Title)
}

func TestConsoleCommandCodeLensesRequireWorkspaceConsole(t *testing.T) {
	root := t.TempDir()
	lenses := relatedCodeLensesFor(
		t,
		NewConsoleCommandCodeLensProvider(root),
		filepath.Join(root, "src", "DraftCommand.php"),
		`<?php
#[AsCommand('app:unavailable')]
final class DraftCommand {}
`,
	)
	assert.Empty(t, lenses)
}

func symfonyConsoleCodeLensRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	consolePath := filepath.Join(root, "bin", "console")
	require.NoError(t, os.MkdirAll(filepath.Dir(consolePath), 0o755))
	require.NoError(t, os.WriteFile(consolePath, []byte("#!/usr/bin/env php\n"), 0o755))
	return root
}
