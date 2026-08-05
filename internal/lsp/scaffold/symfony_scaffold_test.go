package scaffold

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestSymfonyScaffoldCommandUsesComposerNamespaceAndModernTemplate(
	t *testing.T,
) {
	provider, phpIndex, root := newSymfonyScaffoldFixture(t)
	indexPHPScaffoldSource(
		t,
		phpIndex,
		filepath.Join(root, "vendor/InvokableCommand.php"),
		`<?php
namespace Symfony\Component\Console\Command;
class InvokableCommand {}
`,
	)
	indexPHPScaffoldSource(
		t,
		phpIndex,
		filepath.Join(root, "vendor/AsCommand.php"),
		`<?php
namespace Symfony\Component\Console\Attribute;
class AsCommand {}
`,
	)

	result, err := createSymfonyScaffold(t, provider, Request{
		Kind:         "command",
		DirectoryURI: uriutil.FileURI(filepath.Join(root, "src", "Command")),
		Name:         "HTTPServer",
	})
	require.NoError(t, err)

	assert.Equal(t, "App\\Command", result.Namespace)
	assert.Equal(t, "HTTPServerCommand", result.ClassName)
	assert.Equal(
		t,
		uriutil.FileURI(filepath.Join(
			root,
			"src",
			"Command",
			"HTTPServerCommand.php",
		)),
		result.FileURI,
	)
	assert.Contains(t, result.Content, "#[AsCommand(")
	assert.Contains(t, result.Content, "name: 'app:http_server'")
	assert.Contains(t, result.Content, "public function __invoke(")
	assert.Empty(t, phpparser.Parse(result.Content).Errors)
}

func TestSymfonyScaffoldSelectsAttributeTemplates(t *testing.T) {
	provider, phpIndex, root := newSymfonyScaffoldFixture(t)
	indexPHPScaffoldSource(
		t,
		phpIndex,
		filepath.Join(root, "vendor/Route.php"),
		`<?php
namespace Symfony\Component\Routing\Attribute;
class Route {}
`,
	)
	indexPHPScaffoldSource(
		t,
		phpIndex,
		filepath.Join(root, "vendor/AsTwigFunction.php"),
		`<?php
namespace Twig\Attribute;
class AsTwigFunction {}
class AsTwigFilter {}
`,
	)

	controller, err := createSymfonyScaffold(t, provider, Request{
		Kind: "controller",
		DirectoryURI: uriutil.FileURI(
			filepath.Join(root, "src", "Controller"),
		),
		Name: "Product",
	})
	require.NoError(t, err)
	assert.Equal(t, "ProductController", controller.ClassName)
	assert.Contains(
		t,
		controller.Content,
		"use Symfony\\Component\\Routing\\Attribute\\Route;",
	)
	assert.Contains(t, controller.Content, "#[Route('/product')]")

	extension, err := createSymfonyScaffold(t, provider, Request{
		Kind: "twig-extension",
		DirectoryURI: uriutil.FileURI(
			filepath.Join(root, "src", "Twig"),
		),
		Name: "ProductExtension",
	})
	require.NoError(t, err)
	assert.Contains(t, extension.Content, "#[AsTwigFunction('my_function')]")
	assert.Contains(t, extension.Content, "#[AsTwigFilter('my_filter')]")
	assert.NotContains(t, extension.Content, "extends AbstractExtension")
}

func TestSymfonyScaffoldSelectsHistoricalCommandTemplates(t *testing.T) {
	t.Run("configure", func(t *testing.T) {
		provider, _, root := newSymfonyScaffoldFixture(t)
		result, err := createSymfonyScaffold(t, provider, Request{
			Kind: "command",
			DirectoryURI: uriutil.FileURI(
				filepath.Join(root, "src", "Command"),
			),
			Name: "Legacy",
		})
		require.NoError(t, err)
		assert.Contains(t, result.Content, "protected function configure()")
		assert.Contains(t, result.Content, "->setName('app:legacy')")
	})

	t.Run("property", func(t *testing.T) {
		provider, phpIndex, root := newSymfonyScaffoldFixture(t)
		indexPHPScaffoldSource(
			t,
			phpIndex,
			filepath.Join(root, "vendor/Command.php"),
			`<?php
namespace Symfony\Component\Console\Command;
class Command {
    protected static $defaultName;
}
`,
		)
		result, err := createSymfonyScaffold(t, provider, Request{
			Kind: "command",
			DirectoryURI: uriutil.FileURI(
				filepath.Join(root, "src", "Command"),
			),
			Name: "Property",
		})
		require.NoError(t, err)
		assert.Contains(
			t,
			result.Content,
			"protected static $defaultName = 'app:property';",
		)
		assert.NotContains(t, result.Content, "#[AsCommand(")
	})

	t.Run("attribute", func(t *testing.T) {
		provider, phpIndex, root := newSymfonyScaffoldFixture(t)
		indexPHPScaffoldSource(
			t,
			phpIndex,
			filepath.Join(root, "vendor/Command.php"),
			`<?php
namespace Symfony\Component\Console\Command;
class Command {}
`,
		)
		indexPHPScaffoldSource(
			t,
			phpIndex,
			filepath.Join(root, "vendor/AsCommand.php"),
			`<?php
namespace Symfony\Component\Console\Attribute;
class AsCommand {}
`,
		)
		result, err := createSymfonyScaffold(t, provider, Request{
			Kind: "command",
			DirectoryURI: uriutil.FileURI(
				filepath.Join(root, "src", "Command"),
			),
			Name: "Attribute",
		})
		require.NoError(t, err)
		assert.Contains(t, result.Content, "#[AsCommand(")
		assert.Contains(t, result.Content, " extends Command")
		assert.Contains(t, result.Content, "protected function execute(")
	})

	t.Run("invokable project preserving execute convention", func(t *testing.T) {
		provider, phpIndex, root := newSymfonyScaffoldFixture(t)
		for path, source := range map[string]string{
			"vendor/Command.php": `<?php
namespace Symfony\Component\Console\Command;
class Command {}
class InvokableCommand {}
`,
			"vendor/AsCommand.php": `<?php
namespace Symfony\Component\Console\Attribute;
class AsCommand {}
`,
			"src/Command/ExistingCommand.php": `<?php
namespace App\Command;
class ExistingCommand extends \Symfony\Component\Console\Command\Command
{
    protected function execute(): int { return 0; }
}
`,
		} {
			indexPHPScaffoldSource(
				t,
				phpIndex,
				filepath.Join(root, filepath.FromSlash(path)),
				source,
			)
		}
		result, err := createSymfonyScaffold(t, provider, Request{
			Kind: "command",
			DirectoryURI: uriutil.FileURI(
				filepath.Join(root, "src", "Command"),
			),
			Name: "Convention",
		})
		require.NoError(t, err)
		assert.Contains(t, result.Content, "protected function execute(")
		assert.NotContains(t, result.Content, "public function __invoke(")
	})
}

func TestSymfonyScaffoldReusesIndexedCommandPrefix(t *testing.T) {
	provider, _, root := newSymfonyScaffoldFixture(t)
	consoleIndex, err := console.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, consoleIndex.Close()) })
	require.NoError(t, consoleIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "src", "Command", "ExistingCommand.php"),
		[]byte(`<?php
namespace App\Command;
#[\Symfony\Component\Console\Attribute\AsCommand(name: 'shopware:existing')]
class ExistingCommand {}
`),
	)))
	provider.console = consoleIndex

	result, err := createSymfonyScaffold(t, provider, Request{
		Kind: "command",
		DirectoryURI: uriutil.FileURI(
			filepath.Join(root, "src", "Command"),
		),
		Name: "CatalogSync",
	})
	require.NoError(t, err)
	assert.Contains(t, result.Content, "->setName('shopware:catalog_sync')")
}

func TestSymfonyScaffoldGeneratesParseablePHPKinds(t *testing.T) {
	provider, _, root := newSymfonyScaffoldFixture(t)
	cases := []struct {
		kind      string
		directory string
		name      string
		className string
	}{
		{"command", "src/Command", "CacheClear", "CacheClearCommand"},
		{"controller", "src/Controller", "Storefront", "StorefrontController"},
		{"form", "src/Form", "ProductType", "ProductType"},
		{"twig-extension", "src/Twig", "PriceExtension", "PriceExtension"},
		{"compiler-pass", "src/DependencyInjection", "CollectServicesPass", "CollectServicesPass"},
		{"kernel-test", "tests/Integration", "Container", "ContainerTest"},
		{"web-test", "tests/Functional", "Storefront", "StorefrontTest"},
	}
	for _, testCase := range cases {
		t.Run(testCase.kind, func(t *testing.T) {
			result, err := createSymfonyScaffold(t, provider, Request{
				Kind: testCase.kind,
				DirectoryURI: uriutil.FileURI(filepath.Join(
					root,
					filepath.FromSlash(testCase.directory),
				)),
				Name: testCase.name,
			})
			require.NoError(t, err)
			assert.Equal(t, testCase.className, result.ClassName)
			assert.Equal(t, "php", result.Language)
			assert.Empty(t, phpparser.Parse(result.Content).Errors)
		})
	}
}

func TestSymfonyScaffoldGeneratesProjectAwareServiceConfigurations(
	t *testing.T,
) {
	provider, _, root := newSymfonyScaffoldFixture(t)
	configDirectory := filepath.Join(root, "config")

	yamlResult, err := createSymfonyScaffold(t, provider, Request{
		Kind:         "services-yaml",
		DirectoryURI: uriutil.FileURI(configDirectory),
		Name:         "services",
	})
	require.NoError(t, err)
	assert.Equal(t, "yaml", yamlResult.Language)
	assert.Equal(
		t,
		uriutil.FileURI(filepath.Join(configDirectory, "services.yaml")),
		yamlResult.FileURI,
	)
	assert.Contains(t, yamlResult.Content, "  App\\:")
	assert.Contains(t, yamlResult.Content, "resource: '../src/'")
	assert.Empty(t, yamlparser.Parse(yamlResult.Content).Errors)

	xmlResult, err := createSymfonyScaffold(t, provider, Request{
		Kind:         "services-xml",
		DirectoryURI: uriutil.FileURI(configDirectory),
		Name:         "services",
	})
	require.NoError(t, err)
	assert.Contains(t, xmlResult.Content, `namespace="App\"`)
	assert.Contains(t, xmlResult.Content, `resource="../src/"`)
	assert.Empty(t, xmlparser.Parse(xmlResult.Content).Errors)

	phpResult, err := createSymfonyScaffold(t, provider, Request{
		Kind:         "services-php",
		DirectoryURI: uriutil.FileURI(configDirectory),
		Name:         "services",
	})
	require.NoError(t, err)
	assert.Contains(t, phpResult.Content, "$services->load('App\\\\', '../src/')")
	assert.Empty(t, phpparser.Parse(phpResult.Content).Errors)
}

func TestSymfonyScaffoldUsesMostSpecificComposerMapping(t *testing.T) {
	provider, _, root := newSymfonyScaffoldFixture(t)
	result, err := createSymfonyScaffold(t, provider, Request{
		Kind: "command",
		DirectoryURI: uriutil.FileURI(
			filepath.Join(root, "src", "Feature", "Command"),
		),
		Name: "Refresh",
	})
	require.NoError(t, err)
	assert.Equal(t, "App\\Feature\\Command", result.Namespace)
}

func TestSymfonyScaffoldUsesCustomPluginComposerMapping(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "composer.json"),
		[]byte(`{"autoload":{"psr-4":{"Shopware\\":"src/"}}}`),
		0o600,
	))
	pluginRoot := filepath.Join(root, "custom", "plugins", "FroshTools")
	commandDirectory := filepath.Join(pluginRoot, "src", "Command")
	configDirectory := filepath.Join(pluginRoot, "src", "Resources", "config")
	for _, directory := range []string{commandDirectory, configDirectory} {
		require.NoError(t, os.MkdirAll(directory, 0o755))
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(pluginRoot, "composer.json"),
		[]byte(`{
  "name": "frosh/tools",
  "autoload": {"psr-4": {"Frosh\\Tools\\": "src/"}},
  "autoload-dev": {"psr-4": {"Frosh\\Tools\\Tests\\": "tests/"}}
}`),
		0o600,
	))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.ConfigureProject(root))
	provider := NewProvider(root, phpIndex, nil)

	command, err := createSymfonyScaffold(t, provider, Request{
		Kind:         "command",
		DirectoryURI: uriutil.FileURI(commandDirectory),
		Name:         "Refresh",
	})
	require.NoError(t, err)
	assert.Equal(t, "Frosh\\Tools\\Command", command.Namespace)
	assert.Contains(t, command.Content, "namespace Frosh\\Tools\\Command;")
	assert.Empty(t, phpparser.Parse(command.Content).Errors)

	services, err := createSymfonyScaffold(t, provider, Request{
		Kind:         "services-yaml",
		DirectoryURI: uriutil.FileURI(configDirectory),
		Name:         "services",
	})
	require.NoError(t, err)
	assert.Contains(t, services.Content, "  Frosh\\Tools\\:")
	assert.Contains(t, services.Content, "resource: '../../'")
	assert.Empty(t, yamlparser.Parse(services.Content).Errors)
}

func TestSymfonyScaffoldSupportsGlobalComposerMapping(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "config"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "composer.json"),
		[]byte(`{"autoload":{"psr-4":{"":"src/"}}}`),
		0o600,
	))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.ConfigureProject(root))
	provider := NewProvider(root, phpIndex, nil)

	classResult, err := createSymfonyScaffold(t, provider, Request{
		Kind:         "form",
		DirectoryURI: uriutil.FileURI(filepath.Join(root, "src")),
		Name:         "GlobalType",
	})
	require.NoError(t, err)
	assert.Empty(t, classResult.Namespace)
	assert.NotContains(t, classResult.Content, "namespace ;")
	assert.Empty(t, phpparser.Parse(classResult.Content).Errors)

	serviceResult, err := createSymfonyScaffold(t, provider, Request{
		Kind:         "services-yaml",
		DirectoryURI: uriutil.FileURI(filepath.Join(root, "config")),
		Name:         "services",
	})
	require.NoError(t, err)
	assert.Contains(t, serviceResult.Content, "  '':")
	assert.Empty(t, yamlparser.Parse(serviceResult.Content).Errors)
}

func TestSymfonyScaffoldRejectsUnsafeOrConflictingTargets(t *testing.T) {
	provider, _, root := newSymfonyScaffoldFixture(t)
	commandDirectory := filepath.Join(root, "src", "Command")
	require.NoError(t, os.WriteFile(
		filepath.Join(commandDirectory, "ExistingCommand.php"),
		[]byte("<?php"),
		0o600,
	))

	_, err := createSymfonyScaffold(t, provider, Request{
		Kind:         "command",
		DirectoryURI: uriutil.FileURI(commandDirectory),
		Name:         "Existing",
	})
	assert.ErrorContains(t, err, "already exists")

	_, err = createSymfonyScaffold(t, provider, Request{
		Kind:         "command",
		DirectoryURI: uriutil.FileURI(commandDirectory),
		Name:         "../Escape",
	})
	assert.ErrorContains(t, err, "invalid PHP class name")

	_, err = createSymfonyScaffold(t, provider, Request{
		Kind:         "not-a-scaffold",
		DirectoryURI: uriutil.FileURI(commandDirectory),
		Name:         "Unknown",
	})
	assert.ErrorContains(t, err, "unsupported Symfony scaffold kind")

	outside := t.TempDir()
	_, err = createSymfonyScaffold(t, provider, Request{
		Kind:         "command",
		DirectoryURI: uriutil.FileURI(outside),
		Name:         "Outside",
	})
	assert.ErrorContains(t, err, "outside the workspace")

	link := filepath.Join(root, "linked-outside")
	require.NoError(t, os.Symlink(outside, link))
	_, err = createSymfonyScaffold(t, provider, Request{
		Kind:         "command",
		DirectoryURI: uriutil.FileURI(link),
		Name:         "Outside",
	})
	assert.ErrorContains(t, err, "outside the workspace")
}

func newSymfonyScaffoldFixture(
	t *testing.T,
) (*Provider, *php.PHPIndex, string) {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{
		"config",
		"src/Command",
		"src/Controller",
		"src/DependencyInjection",
		"src/Feature/Command",
		"src/Form",
		"src/Twig",
		"tests/Functional",
		"tests/Integration",
	} {
		require.NoError(t, os.MkdirAll(
			filepath.Join(root, filepath.FromSlash(directory)),
			0o755,
		))
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "composer.json"),
		[]byte(`{
  "autoload": {
    "psr-4": {
      "App\\": "src/",
      "App\\Feature\\": "src/Feature/"
    }
  },
  "autoload-dev": {
    "psr-4": {
      "App\\Tests\\": "tests/"
    }
  }
}`),
		0o600,
	))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.ConfigureProject(root))
	return NewProvider(root, phpIndex, nil), phpIndex, root
}

func indexPHPScaffoldSource(
	t *testing.T,
	phpIndex *php.PHPIndex,
	path,
	source string,
) {
	t.Helper()
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
}

func createSymfonyScaffold(
	t *testing.T,
	provider *Provider,
	request Request,
) (Response, error) {
	t.Helper()
	content, err := json.Marshal(request)
	require.NoError(t, err)
	raw := json.RawMessage(content)
	value, err := provider.create(context.Background(), &raw)
	if err != nil {
		return Response{}, err
	}
	result, ok := value.(Response)
	require.True(t, ok)
	return result, nil
}
