package entityschema

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpindex "github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/require"
)

func TestIndexedCatalogRejectsIncompleteWorkspaceGeneration(t *testing.T) {
	store, err := indexer.NewStore(filepath.Join(t.TempDir(), "indexes.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	readiness, err := indexer.NewWorkspaceSymbolCatalog(store)
	require.NoError(t, err)
	phpIndex, err := phpindex.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	catalog := NewIndexedCatalog(phpIndex, nil, readiness)

	_, _, err = catalog.ScanContext(context.Background(), t.TempDir(), nil)
	require.ErrorIs(t, err, ErrIndexNotReady)
	require.True(t, errors.Is(err, ErrIndexNotReady))

	require.NoError(t, readiness.SetReady(context.Background(), true))
	schema, definitions, err := catalog.ScanContext(context.Background(), t.TempDir(), nil)
	require.NoError(t, err)
	require.Equal(t, EmptySchema(), schema)
	require.Empty(t, definitions)
}

func TestIndexedCatalogDiscoversCustomBasesWithoutWalkingPlugin(t *testing.T) {
	root := t.TempDir()
	phpIndex, err := phpindex.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	sourceIndex, err := NewSourceIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sourceIndex.Close()) })

	sources := map[string]string{
		filepath.Join(root, "vendor", "shopware", "DAL.php"): `<?php
namespace Shopware\Core\Framework\DataAbstractionLayer;
abstract class EntityDefinition {}
abstract class MappingEntityDefinition extends EntityDefinition {}
abstract class EntityTranslationDefinition extends EntityDefinition {}
abstract class EntityExtension {}
abstract class BulkEntityExtension {}
`,
		filepath.Join(root, "src", "Foundation.php"): `<?php
namespace Acme\Odd;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityExtension;
abstract class RecordBase extends EntityDefinition {}
abstract class EnricherBase extends EntityExtension {}
`,
		filepath.Join(root, "src", "UnexpectedName.php"): `<?php
namespace Acme\Odd;
use Shopware\Core\Framework\DataAbstractionLayer\Field\FieldCollection;
use Shopware\Core\Framework\DataAbstractionLayer\Field\EnumField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
class CatalogModel extends RecordBase {
    public const ENTITY_NAME = 'acme_catalog';
    protected function getBaseFields(): array { return [new StringField('base_code', 'baseCode'), new EnumField('status', 'status', Status::Active)]; }
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}
`,
		filepath.Join(root, "src", "Status.php"): `<?php
namespace Acme\Odd;
enum Status: string { case Active = 'active'; }
`,
		filepath.Join(root, "src", "Extension.php"): `<?php
namespace Acme\Odd;
class CatalogExtension extends EnricherBase {
    public function getDefinitionClass(): string { return CatalogModel::class; }
}
`,
		filepath.Join(root, "src", "Unrelated.php"): `<?php
namespace Acme\Odd;
class Unrelated {}
`,
	}
	for path, source := range sources {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
		file := indexer.NewParsedFile(path, []byte(source))
		require.NoError(t, phpIndex.Index(file))
		require.NoError(t, sourceIndex.Index(file))
	}
	_, found, err := sourceIndex.Source(filepath.Join(root, "src", "Unrelated.php"))
	require.NoError(t, err)
	require.False(t, found)

	// This valid but deliberately unindexed definition proves discovery uses
	// the semantic catalog instead of walking src at request time.
	unindexedPath := filepath.Join(root, "src", "NotIndexed.php")
	require.NoError(t, os.WriteFile(unindexedPath, []byte(`<?php
namespace Acme\Odd;
class NotIndexed extends RecordBase {
    public const ENTITY_NAME = 'must_not_appear';
    protected function defineFields(): \Shopware\Core\Framework\DataAbstractionLayer\Field\FieldCollection { return new \Shopware\Core\Framework\DataAbstractionLayer\Field\FieldCollection([]); }
}`), 0o644))
	// Indexed DAL source remains available after the workspace file disappears;
	// the request path must not reopen any PHP file.
	for path := range sources {
		require.NoError(t, os.Remove(path))
	}

	schema, definitions, err := NewIndexedCatalog(phpIndex, sourceIndex).Scan(root, nil)
	require.NoError(t, err)
	require.Contains(t, schema.Entities, "acme_catalog")
	require.Equal(t, "VARCHAR(255)", schema.Entities["acme_catalog"].Columns["status"].SQLType)
	require.Equal(t, "VARCHAR(255)", schema.Entities["acme_catalog"].Columns["base_code"].SQLType)
	require.NotContains(t, schema.Entities, "must_not_appear")
	require.Len(t, definitions, 2)
	kinds := make(map[DefinitionKind]int)
	for _, definition := range definitions {
		kinds[definition.Spec.DefinitionKind]++
	}
	require.Equal(t, map[DefinitionKind]int{
		DefinitionEntity: 1, DefinitionExtension: 1,
	}, kinds)
	entity := definitions[0].Spec
	if entity.DefinitionKind != DefinitionEntity {
		entity = definitions[1].Spec
	}
	require.NotNil(t, entity.DefinitionBehavior)
	status := fieldByProperty(t, entity.DefinitionBehavior.BaseFields, "status")
	require.NotNil(t, status)
	require.Equal(t, "string", status.EnumBackingType)
}
