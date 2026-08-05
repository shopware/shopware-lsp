package console

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndexPHPConsoleCommandsAndInputs(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	source := `<?php
namespace App\Command;

#[AsCommand(
    name: 'app:user:create',
    description: 'Creates a user',
    aliases: ['app:user:add'],
)]
class CreateUserCommand
{
    protected function configure(): void
    {
        $this
            ->addArgument('role', InputArgument::OPTIONAL, 'Initial role', 'user')
            ->addOption(
                name: 'admin',
                shortcut: 'a',
                mode: InputOption::VALUE_NONE,
                description: 'Grant admin access',
            )
            ->setDefinition([
                new InputArgument('tenant', InputArgument::REQUIRED, 'Tenant'),
                new InputOption('dry-run', 'd', InputOption::VALUE_NONE, 'Dry run'),
            ]);
    }

    public function __invoke(
        #[Argument(description: 'User name')] string $username,
        #[Option(name: 'notify', shortcut: 'n')] bool $sendNotification = true,
    ): void {}
}

class UserCommands
{
    #[AsCommand('app:user:delete')]
    public function delete(
        #[Argument(name: 'user-id')] string $id,
        #[Option(name: 'force')] bool $force = false,
    ): void {}
}`
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/src/Command/CreateUserCommand.php",
		[]byte(source),
	)))

	commands, err := idx.GetCommand("app:user:create")
	require.NoError(t, err)
	require.Len(t, commands, 1)
	command := commands[0]
	assert.Equal(t, "App\\Command\\CreateUserCommand", command.Class)
	assert.Equal(t, "Creates a user", command.Description)
	assertInputNames(t, command.Arguments, "role", "tenant", "username")
	assertInputNames(t, command.Options, "admin", "dry-run", "notify")
	assert.Equal(t, "a", findInput(t, command.Options, "admin").Shortcut)

	aliases, err := idx.GetCommand("app:user:add")
	require.NoError(t, err)
	require.Len(t, aliases, 1)
	assert.Equal(t, "app:user:create", aliases[0].Canonical)

	methodCommands, err := idx.GetCommand("app:user:delete")
	require.NoError(t, err)
	require.Len(t, methodCommands, 1)
	assert.Equal(t, "delete", methodCommands[0].Method)
	assertInputNames(t, methodCommands[0].Arguments, "user-id")
	assertInputNames(t, methodCommands[0].Options, "force")
}

func TestIndexLegacyAndCompiledConsoleCommands(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/src/LegacyCommand.php",
		[]byte(`<?php
namespace App;
class LegacyCommand extends Command {
    protected static $defaultName = 'app:legacy|app:old';
}
class ConfiguredCommand extends Command {
    protected function configure(): void {
        $this->setName('app:configured');
    }
}`),
	)))
	for _, name := range []string{"app:legacy", "app:old", "app:configured"} {
		commands, commandErr := idx.GetCommand(name)
		require.NoError(t, commandErr)
		require.Len(t, commands, 1, name)
	}

	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/var/cache/container.xml",
		[]byte(`<container><services>
  <service id="App\Command\CompiledCommand" class="App\Command\CompiledCommand">
    <tag name="console.command" command="app:compiled"/>
  </service>
</services></container>`),
	)))
	compiled, err := idx.GetCommand("app:compiled")
	require.NoError(t, err)
	require.Len(t, compiled, 1)
	assert.Equal(t, "App\\Command\\CompiledCommand", compiled[0].Class)
}

func TestIndexAsCommandPositionalAndLocalConstantAliases(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	source := `<?php
namespace App\Command;

class UserCommands
{
    private const CREATE = 'app:user:create';
    private const CREATE_ALIAS = 'app:user:add';
    private const DELETE_ALIAS = 'app:user:remove';
    private const DYNAMIC = self::CREATE . ':dynamic';

    #[AsCommand(self::CREATE, 'Creates a user', [
        static::CREATE_ALIAS,
        'app:user:new',
        self::DYNAMIC,
        External::ALIAS,
    ])]
    public function create(): int { return 0; }

    #[AsCommand(name: 'app:user:delete', aliases: [self::DELETE_ALIAS])]
    public function delete(): int { return 0; }
}`
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/src/Command/UserCommands.php",
		[]byte(source),
	)))

	for _, name := range []string{
		"app:user:create",
		"app:user:add",
		"app:user:new",
	} {
		commands, commandErr := idx.GetCommand(name)
		require.NoError(t, commandErr)
		require.NotEmpty(t, commands, name)
	}
	create, err := idx.GetCommand("app:user:create")
	require.NoError(t, err)
	require.Len(t, create, 1)
	assert.Equal(t, "app:user:create", create[0].Canonical)
	assert.Equal(t, "Creates a user", create[0].Description)
	assert.Equal(t, "create", create[0].Method)

	deleteAlias, err := idx.GetCommand("app:user:remove")
	require.NoError(t, err)
	require.Len(t, deleteAlias, 1)
	assert.Equal(t, "app:user:delete", deleteAlias[0].Canonical)

	for _, unsupported := range []string{
		"app:user:create:dynamic",
		"External::ALIAS",
	} {
		commands, commandErr := idx.GetCommand(unsupported)
		require.NoError(t, commandErr)
		assert.Empty(t, commands, unsupported)
	}
}

func TestAsCommandWithoutStaticNameFallsBackToTraditionalName(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/src/Command/LegacyCommand.php",
		[]byte(`<?php
#[AsCommand(name: CommandNames::DYNAMIC)]
class LegacyCommand {
    protected static $defaultName = 'app:legacy|app:old';
}`),
	)))
	for _, name := range []string{"app:legacy", "app:old"} {
		commands, commandErr := idx.GetCommand(name)
		require.NoError(t, commandErr)
		require.Len(t, commands, 1, name)
	}
}

func TestRestoredCommandPathClearsStaleCandidate(t *testing.T) {
	cache := t.TempDir()
	path := "/project/src/Command/RestoredCommand.php"
	first, err := NewIndex(cache)
	require.NoError(t, err)
	require.NoError(t, first.Index(indexer.NewParsedFile(
		path,
		[]byte(`<?php
#[AsCommand(name: 'app:restored')]
class RestoredCommand {}
`),
	)))
	require.NoError(t, first.Close())

	restored, err := NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	require.NoError(t, restored.Index(indexer.NewParsedFile(
		path,
		[]byte("<?php return 1;"),
	)))

	commands, err := restored.GetCommand("app:restored")
	require.NoError(t, err)
	require.Empty(t, commands)
}

func TestLegacyCommandInputsPreserveSourceOrder(t *testing.T) {
	file := indexer.NewParsedFile(
		"/project/src/OrderedCommand.php",
		[]byte(`<?php
class OrderedCommand {
    protected function configure(): void {
        $this->addArgument('zebra', InputArgument::REQUIRED);
        $this->addArgument('alpha', InputArgument::OPTIONAL);
        $this->addOption('verbose', 'v', InputOption::VALUE_NONE);
    }
}
`),
	)
	classes := phpquery.Classes(file.SyntaxTree().Root)
	require.Len(t, classes, 1)

	arguments, options := LegacyCommandInputs(classes[0], file.Path)

	require.Len(t, arguments, 2)
	assert.Equal(t, "zebra", arguments[0].Name)
	assert.Equal(t, "alpha", arguments[1].Name)
	require.Len(t, options, 1)
	assert.Equal(t, "verbose", options[0].Name)
}

func assertInputNames(t *testing.T, inputs []Input, names ...string) {
	t.Helper()
	actual := make([]string, 0, len(inputs))
	for _, input := range inputs {
		actual = append(actual, input.Name)
	}
	assert.ElementsMatch(t, names, actual)
}

func findInput(t *testing.T, inputs []Input, name string) Input {
	t.Helper()
	for _, input := range inputs {
		if input.Name == name {
			return input
		}
	}
	t.Fatalf("input %q missing in %#v", name, inputs)
	return Input{}
}
