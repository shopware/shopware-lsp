package codelens

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

func TestServiceRelatedCodeLensesAcrossPHPXMLAndYAML(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	classPath := filepath.Join(root, "src", "Services.php")
	xmlPath := filepath.Join(root, "config", "services.xml")
	yamlPath := filepath.Join(root, "config", "services.yaml")
	phpPath := filepath.Join(root, "config", "services.php")
	classSource := `<?php
namespace App;
class Base {}
class AbstractService {}
class Decorator {}
class PHPDecorator {}
`
	xmlSource := `<container>
  <services>
    <service id="base" class="App\Base"/>
    <service id="abstract" class="App\AbstractService"/>
  </services>
</container>
`
	yamlSource := `services:
  yaml.decorator:
    class: App\Decorator
    decorates: base
    parent: abstract
  'App\':
    resource: '../src'
`
	phpSource := `<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->set('php.decorator', App\PHPDecorator::class)
        ->decorate('base')
        ->parent('abstract');
};
`
	for path, source := range map[string]string{
		classPath: classSource,
		xmlPath:   xmlSource,
		yamlPath:  yamlSource,
		phpPath:   phpSource,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}

	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		classPath,
		[]byte(classSource),
	)))
	serviceIndex, err := symfony.NewServiceIndex(root, cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	serviceIndex.SetPHPIndex(phpIndex)
	for path, source := range map[string]string{
		xmlPath:  xmlSource,
		yamlPath: yamlSource,
		phpPath:  phpSource,
	} {
		require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	provider := NewServiceRelatedCodeLensProvider(
		serviceIndex,
		phpIndex,
	)

	xmlLenses := relatedCodeLensesFor(
		t,
		provider,
		xmlPath,
		xmlSource,
	)
	require.Len(t, xmlLenses, 2)
	assert.ElementsMatch(t, []string{
		relatedTarget(yamlPath, 2),
		relatedTarget(phpPath, 6),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(
			t,
			xmlLenses,
			"Open 2 decorating services",
		),
	))
	assert.ElementsMatch(t, []string{
		relatedTarget(yamlPath, 2),
		relatedTarget(phpPath, 6),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, xmlLenses, "Open 2 child services"),
	))

	yamlLenses := relatedCodeLensesFor(
		t,
		provider,
		yamlPath,
		yamlSource,
	)
	require.Len(t, yamlLenses, 3)
	assert.Equal(t, []string{
		relatedTarget(xmlPath, 3),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, yamlLenses, "Open decorated service"),
	))
	assert.Equal(t, []string{
		relatedTarget(xmlPath, 4),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, yamlLenses, "Open parent service"),
	))
	assert.ElementsMatch(t, []string{
		relatedTarget(classPath, 3),
		relatedTarget(classPath, 4),
		relatedTarget(classPath, 5),
		relatedTarget(classPath, 6),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, yamlLenses, "Open 4 prototype classes"),
	))

	phpLenses := relatedCodeLensesFor(
		t,
		provider,
		phpPath,
		phpSource,
	)
	require.Len(t, phpLenses, 2)
	assert.Equal(t, []string{
		relatedTarget(xmlPath, 3),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, phpLenses, "Open decorated service"),
	))
	assert.Equal(t, []string{
		relatedTarget(xmlPath, 4),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, phpLenses, "Open parent service"),
	))
}
