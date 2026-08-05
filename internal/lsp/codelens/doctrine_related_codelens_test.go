package codelens

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
)

func TestDoctrineRelatedCodeLensesLinkPHPXMLAndYAML(t *testing.T) {
	fixture := newDoctrineRelatedFixture(t)
	provider := NewDoctrineRelatedCodeLensProvider(
		fixture.doctrine,
		fixture.php,
	)

	phpLenses := relatedCodeLensesFor(
		t,
		provider,
		fixture.entityPath,
		fixture.entitySource,
	)
	require.Len(t, phpLenses, 1)
	assert.Equal(
		t,
		"Open 2 related Doctrine mappings",
		phpLenses[0].Command.Title,
	)
	assert.ElementsMatch(t, []string{
		relatedTarget(fixture.xmlPath, 2),
		relatedTarget(fixture.yamlPath, 1),
	}, relatedLensTargets(t, phpLenses[0]))

	for path, source := range map[string]string{
		fixture.xmlPath:  fixture.xmlSource,
		fixture.yamlPath: fixture.yamlSource,
	} {
		lenses := relatedCodeLensesFor(t, provider, path, source)
		require.Len(t, lenses, 1)
		assert.Equal(t, "Open mapped PHP class", lenses[0].Command.Title)
		assert.Equal(t, []string{
			relatedTarget(fixture.entityPath, 4),
		}, relatedLensTargets(t, lenses[0]))
	}
}

func TestDoctrineRelatedCodeLensLinksRepositoryCallsToModelDeclarations(
	t *testing.T,
) {
	fixture := newDoctrineRelatedFixture(t)
	stubPath := filepath.Join(fixture.root, "vendor", "doctrine.php")
	controllerPath := filepath.Join(
		fixture.root,
		"src",
		"Controller",
		"ProductController.php",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(stubPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(controllerPath), 0o755))
	stubSource := `<?php
namespace Doctrine\Persistence;
interface ManagerRegistry {
    public function getRepository(string $class): object;
    public function getManagerForClass(string $class): object;
}
`
	controllerSource := `<?php
namespace App\Controller;

use App\Entity\Product;
use Doctrine\Persistence\ManagerRegistry;

class OtherRegistry {
    public function getRepository(string $class): object {}
}

final class ProductController
{
    public function edit(ManagerRegistry $registry): void
    {
        $registry->getRepository(Product::class);
        $registry->getManagerForClass('App\Entity\Product');
    }

    public function unrelated(OtherRegistry $registry): void
    {
        $registry->getRepository(Product::class);
    }

    private function helper(ManagerRegistry $registry): void
    {
        $registry->getRepository(Product::class);
    }
}
`
	for path, source := range map[string]string{
		stubPath:       stubSource,
		controllerPath: controllerSource,
	} {
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
		require.NoError(t, fixture.php.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}

	lenses := relatedCodeLensesFor(
		t,
		NewDoctrineRelatedCodeLensProvider(
			fixture.doctrine,
			fixture.php,
		),
		controllerPath,
		controllerSource,
	)
	require.Len(t, lenses, 1)
	assert.Equal(
		t,
		"Open 3 related Doctrine declarations",
		lenses[0].Command.Title,
	)
	assert.Equal(t, 12, lenses[0].Range.Start.Line)
	assert.ElementsMatch(t, []string{
		relatedTarget(fixture.entityPath, 4),
		relatedTarget(fixture.xmlPath, 2),
		relatedTarget(fixture.yamlPath, 1),
	}, relatedLensTargets(t, lenses[0]))
}

func TestDoctrineRelatedCodeLensRestoresExternalMappings(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	entityPath := filepath.Join(root, "src", "Entity", "Product.php")
	mappingPath := filepath.Join(
		root,
		"config",
		"doctrine",
		"Product.orm.xml",
	)
	entitySource := "<?php\nnamespace App\\Entity;\nclass Product {}\n"
	mappingSource := `<doctrine-mapping>
  <entity name="App\Entity\Product"/>
</doctrine-mapping>
`
	require.NoError(t, os.MkdirAll(filepath.Dir(entityPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(mappingPath), 0o755))
	require.NoError(t, os.WriteFile(
		entityPath,
		[]byte(entitySource),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		mappingPath,
		[]byte(mappingSource),
		0o644,
	))

	firstPHP, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	firstDoctrine, err := doctrine.NewIndex(cache)
	require.NoError(t, err)
	require.NoError(t, firstPHP.Index(indexer.NewParsedFile(
		entityPath,
		[]byte(entitySource),
	)))
	require.NoError(t, firstDoctrine.Index(indexer.NewParsedFile(
		mappingPath,
		[]byte(mappingSource),
	)))
	require.NoError(t, firstDoctrine.Close())
	require.NoError(t, firstPHP.Close())

	restoredPHP, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restoredPHP.Close()) })
	restoredDoctrine, err := doctrine.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restoredDoctrine.Close()) })
	lenses := relatedCodeLensesFor(
		t,
		NewDoctrineRelatedCodeLensProvider(
			restoredDoctrine,
			restoredPHP,
		),
		entityPath,
		entitySource,
	)
	require.Len(t, lenses, 1)
	assert.Equal(t, []string{
		relatedTarget(mappingPath, 2),
	}, relatedLensTargets(t, lenses[0]))
}

type doctrineRelatedFixture struct {
	root         string
	entityPath   string
	entitySource string
	xmlPath      string
	xmlSource    string
	yamlPath     string
	yamlSource   string
	php          *php.PHPIndex
	doctrine     *doctrine.Index
}

func newDoctrineRelatedFixture(t *testing.T) doctrineRelatedFixture {
	t.Helper()
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	fixture := doctrineRelatedFixture{
		root:       root,
		entityPath: filepath.Join(root, "src", "Entity", "Product.php"),
		entitySource: `<?php
namespace App\Entity;

final class Product {}
`,
		xmlPath: filepath.Join(
			root,
			"config",
			"doctrine",
			"Product.orm.xml",
		),
		xmlSource: `<doctrine-mapping>
  <entity name="App\Entity\Product" table="product"/>
</doctrine-mapping>
`,
		yamlPath: filepath.Join(
			root,
			"config",
			"doctrine",
			"Product.orm.yaml",
		),
		yamlSource: `App\Entity\Product:
  type: entity
  table: product
`,
	}
	for _, path := range []string{
		fixture.entityPath,
		fixture.xmlPath,
		fixture.yamlPath,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	}
	for path, source := range map[string]string{
		fixture.entityPath: fixture.entitySource,
		fixture.xmlPath:    fixture.xmlSource,
		fixture.yamlPath:   fixture.yamlSource,
	} {
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	var err error
	fixture.php, err = php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fixture.php.Close()) })
	fixture.doctrine, err = doctrine.NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fixture.doctrine.Close()) })
	require.NoError(t, fixture.php.Index(indexer.NewParsedFile(
		fixture.entityPath,
		[]byte(fixture.entitySource),
	)))
	require.NoError(t, fixture.doctrine.Index(indexer.NewParsedFile(
		fixture.xmlPath,
		[]byte(fixture.xmlSource),
	)))
	require.NoError(t, fixture.doctrine.Index(indexer.NewParsedFile(
		fixture.yamlPath,
		[]byte(fixture.yamlSource),
	)))
	return fixture
}
