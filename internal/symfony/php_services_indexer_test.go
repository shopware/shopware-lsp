package symfony

import (
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceIndexIndexesAndClearsPHPConfig(t *testing.T) {
	configDir := t.TempDir()
	index, err := NewServiceIndex(t.TempDir(), configDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })

	path := filepath.Join(t.TempDir(), "DependencyInjection", "messenger.php")
	config := []byte(`<?php
use App\Message\MessageHandler;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $container->services()->set(MessageHandler::class)->tag('messenger.message_handler');
    $container->parameters()->set('app.transport', 'async');
};
`)
	require.NoError(t, index.Index(indexer.NewParsedFile(path, config)))

	service, found, err := index.GetServiceByID("App\\Message\\MessageHandler")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, path, service.Path)
	assert.Contains(t, service.Tags, "messenger.message_handler")

	parameter, found, err := index.GetParameterByName("app.transport")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "async", parameter.Value)

	// Replacing a former configurator with normal PHP must remove its stale
	// service and parameter records without writing for every unrelated file.
	require.NoError(t, index.Index(indexer.NewParsedFile(path, []byte("<?php return 1;"))))
	_, found, err = index.GetServiceByID("App\\Message\\MessageHandler")
	require.NoError(t, err)
	assert.False(t, found)
	_, found, err = index.GetParameterByName("app.transport")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestServiceIndexIgnoresOrdinaryPHP(t *testing.T) {
	index, err := NewServiceIndex(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })

	require.NoError(t, index.Index(indexer.NewParsedFile("src/Service.php", []byte(`<?php
final class Service {}
`))))
	services, err := index.GetAllServices()
	require.NoError(t, err)
	assert.Empty(t, services)
}

func TestServiceIndexPersistsPHPArrayConfig(t *testing.T) {
	projectRoot := t.TempDir()
	configDir := t.TempDir()
	path := filepath.Join(projectRoot, "config", "services.php")
	source := []byte(`<?php
return [
    'parameters' => ['app.parameter' => 'value'],
    'services' => [
        'app.target' => [
            'class' => 'App\\Target',
            'tags' => ['app.tag'],
        ],
        'app.alias' => '@app.target',
    ],
];
`)
	serviceIndex, err := NewServiceIndex(projectRoot, configDir)
	require.NoError(t, err)
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(path, source)))
	require.NoError(t, serviceIndex.Close())

	restored, err := NewServiceIndex(projectRoot, configDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	target, found, err := restored.GetServiceByID("app.target")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "App\\Target", target.Class)
	assert.Contains(t, target.Tags, "app.tag")
	alias, found, err := restored.GetServiceByID("app.alias")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "app.target", alias.AliasTarget)
	parameter, found, err := restored.GetParameterByName("app.parameter")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "value", parameter.Value)

	require.NoError(t, restored.Index(indexer.NewParsedFile(
		path,
		[]byte("<?php return 1;"),
	)))
	_, found, err = restored.GetServiceByID("app.target")
	require.NoError(t, err)
	require.False(t, found)
	_, found, err = restored.GetParameterByName("app.parameter")
	require.NoError(t, err)
	require.False(t, found)
}

func TestServiceIndexResolvesInstanceofTags(t *testing.T) {
	configDir := t.TempDir()
	phpIndex, err := php.NewPHPIndex(configDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	serviceIndex, err := NewServiceIndex(t.TempDir(), configDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	serviceIndex.SetPHPIndex(phpIndex)

	types := []byte(`<?php
namespace App\Listing;
interface FilterHandler {}
abstract class AbstractHandler implements FilterHandler {}
final class ProductHandler extends AbstractHandler {}
final class OtherHandler {}
`)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile("src/Listing.php", types)))

	config := []byte(`<?php
use App\Listing\FilterHandler;
use App\Listing\OtherHandler;
use App\Listing\ProductHandler;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->instanceof(FilterHandler::class)->tag('app.filter_handler');
    $services->set(ProductHandler::class);
    $services->set(OtherHandler::class);
};
`)
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile("config/listing.php", config)))

	services, err := serviceIndex.GetServicesByTag("app.filter_handler")
	require.NoError(t, err)
	assert.Equal(t, []string{"App\\Listing\\ProductHandler"}, services)

	service, found, err := serviceIndex.GetServiceByID("App\\Listing\\ProductHandler")
	require.NoError(t, err)
	require.True(t, found)
	assert.Contains(t, service.Tags, "app.filter_handler")
	other, found, err := serviceIndex.GetServiceByID("App\\Listing\\OtherHandler")
	require.NoError(t, err)
	require.True(t, found)
	assert.NotContains(t, other.Tags, "app.filter_handler")
}

func TestServiceIndexExpandsPHPPrototypes(t *testing.T) {
	projectRoot := t.TempDir()
	configDir := t.TempDir()
	phpIndex, err := php.NewPHPIndex(configDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	serviceIndex, err := NewServiceIndex(projectRoot, configDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	serviceIndex.SetPHPIndex(phpIndex)

	indexPHP := func(relativePath, source string) {
		t.Helper()
		path := filepath.Join(projectRoot, relativePath)
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(path, []byte(source))))
	}
	indexPHP("src/Marker.php", "<?php namespace App; interface Marker {}")
	indexPHP("src/Included.php", "<?php namespace App; final class Included implements Marker {}")
	indexPHP("src/Explicit.php", "<?php namespace App; final class Explicit {}")
	indexPHP("src/AbstractService.php", "<?php namespace App; abstract class AbstractService implements Marker {}")
	indexPHP("src/Reusable.php", "<?php namespace App; trait Reusable {}")
	indexPHP("src/Status.php", "<?php namespace App; enum Status {}")
	indexPHP("src/Excluded/Hidden.php", "<?php namespace App\\Excluded; final class Hidden {}")

	configPath := filepath.Join(projectRoot, "config", "services.php")
	config := []byte(`<?php
use App\Marker;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->instanceof(Marker::class)->tag('app.marker');
    $services->load('App\\', __DIR__ . '/../src/')
        ->exclude(__DIR__ . '/../src/Excluded')
        ->tag('app.prototype');
    $services->set(\App\Explicit::class);
};
`)
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(configPath, config)))

	services, err := serviceIndex.GetAllServices()
	require.NoError(t, err)
	assert.Equal(t, []string{"App\\Explicit", "App\\Included"}, services)
	service, found, err := serviceIndex.GetServiceByID("App\\Included")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, configPath, service.Path)
	assert.Contains(t, service.Tags, "app.prototype")
	assert.Contains(t, service.Tags, "app.marker")

	byPrototypeTag, err := serviceIndex.GetServicesByTag("app.prototype")
	require.NoError(t, err)
	assert.Equal(t, []string{"App\\Included"}, byPrototypeTag)
	byConditionalTag, err := serviceIndex.GetServicesByTag("app.marker")
	require.NoError(t, err)
	assert.Equal(t, []string{"App\\Included"}, byConditionalTag)

	// Prototype expansion follows the PHP index revision without requiring the
	// service config file to be reindexed.
	indexPHP("src/Added.php", "<?php namespace App; final class Added {}")
	services, err = serviceIndex.GetAllServices()
	require.NoError(t, err)
	assert.Equal(t, []string{"App\\Added", "App\\Explicit", "App\\Included"}, services)
	require.NoError(t, phpIndex.RemovedFiles([]string{filepath.Join(projectRoot, "src", "Added.php")}))
	services, err = serviceIndex.GetAllServices()
	require.NoError(t, err)
	assert.Equal(t, []string{"App\\Explicit", "App\\Included"}, services)

	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(configPath, []byte("<?php return 1;"))))
	services, err = serviceIndex.GetAllServices()
	require.NoError(t, err)
	assert.Empty(t, services)
}

func TestPHPPrototypePathPatterns(t *testing.T) {
	assert.True(t, pathMatchesConfigPattern(
		filepath.FromSlash("/project/src/Entity/Product.php"),
		filepath.FromSlash("/project/src/{Entity,Repository}"),
	))
	assert.True(t, pathMatchesConfigPattern(
		filepath.FromSlash("/project/src/Kernel.php"),
		filepath.FromSlash("/project/src/{Entity,Kernel.php}"),
	))
	assert.True(t, pathMatchesConfigPattern(
		filepath.FromSlash("/project/src/Domain/Tests/ProductTest.php"),
		filepath.FromSlash("/project/src/**/Tests/*Test.php"),
	))
	assert.False(t, pathMatchesConfigPattern(
		filepath.FromSlash("/project/src/Domain/Product.php"),
		filepath.FromSlash("/project/src/**/Tests/*Test.php"),
	))
}
