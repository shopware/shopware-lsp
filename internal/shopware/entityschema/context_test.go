package entityschema

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindPluginContextPrefersYAMLServiceConfiguration(t *testing.T) {
	root := t.TempDir()
	configDirectory := filepath.Join(root, "src", "Resources", "config")
	require.NoError(t, os.MkdirAll(configDirectory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name": "acme/example",
  "type": "shopware-platform-plugin",
  "autoload": {"psr-4": {"Acme\\Example\\": "src/"}},
  "extra": {"shopware-plugin-class": "Acme\\Example\\Example"}
}`), 0o644))
	for _, name := range []string{"services.xml", "services.php", "services.yaml"} {
		require.NoError(t, os.WriteFile(filepath.Join(configDirectory, name), []byte("services:\n"), 0o644))
	}

	context, err := FindPluginContext(root, filepath.Join(root, "src"))
	require.NoError(t, err)
	require.Len(t, context.ServiceURIs, 3)
	require.Equal(t, filepath.Join(configDirectory, "services.yaml"), context.ServiceURIs[0])
	require.Equal(t, filepath.Join(configDirectory, "services.xml"), context.ServiceURIs[2])
}

func TestScanPluginSchemaDiscoversArbitraryFilesAndCustomDALBases(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "src")
	require.NoError(t, os.MkdirAll(sourceRoot, 0o755))
	write := func(name, source string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(sourceRoot, name), []byte(source), 0o644))
	}
	write("Foundation.php", `<?php declare(strict_types=1);
namespace Acme\Odd;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\MappingEntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityExtension;
use Shopware\Core\Framework\DataAbstractionLayer\BulkEntityExtension;
abstract class RecordBase extends EntityDefinition {}
abstract class LinkBase extends MappingEntityDefinition {}
abstract class EnricherBase extends EntityExtension {}
abstract class BulkBase extends BulkEntityExtension {}
`)
	write("CatalogModel.php", `<?php declare(strict_types=1);
namespace Acme\Odd;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CatalogModel extends RecordBase {
    public const ENTITY_NAME = 'acme_catalog';
    protected function getBaseFields(): array { return [new StringField('base_code', 'baseCode')]; }
    protected function defineFields(): FieldCollection { return new FieldCollection([(new IdField('id', 'id'))]); }
}
`)
	write("CatalogLinks.php", `<?php declare(strict_types=1);
namespace Acme\Odd;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class CatalogLinks extends LinkBase {
    public const ENTITY_NAME = 'acme_catalog_link';
    protected function defineFields(): FieldCollection { return new FieldCollection([(new IdField('id', 'id'))]); }
}
`)
	write("CatalogEnricher.php", `<?php declare(strict_types=1);
namespace Acme\Odd;
class CatalogEnricher extends EnricherBase {
    public function getDefinitionClass(): string { return CatalogModel::class; }
}
`)
	write("CatalogBatch.php", `<?php declare(strict_types=1);
namespace Acme\Odd;
class CatalogBatch extends BulkBase {
    public function collect(): \Generator { yield CatalogModel::ENTITY_NAME => []; }
}
`)

	schema, definitions, err := ScanPluginSchema(root)
	require.NoError(t, err)
	require.Contains(t, schema.Entities, "acme_catalog")
	require.Contains(t, schema.Entities["acme_catalog"].Columns, "base_code")
	require.Contains(t, schema.Entities, "acme_catalog_link")
	kinds := make(map[DefinitionKind]int)
	files := make(map[string]bool)
	for _, definition := range definitions {
		kinds[definition.Spec.DefinitionKind]++
		files[filepath.Base(definition.Path)] = true
	}
	require.Equal(t, map[DefinitionKind]int{
		DefinitionEntity: 1, DefinitionMapping: 1, DefinitionExtension: 1, DefinitionBulkExtension: 1,
	}, kinds)
	for _, name := range []string{"CatalogModel.php", "CatalogLinks.php", "CatalogEnricher.php", "CatalogBatch.php"} {
		require.True(t, files[name], name)
	}
}
