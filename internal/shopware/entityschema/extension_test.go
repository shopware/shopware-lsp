package entityschema

import (
	"reflect"
	"strings"
	"testing"

	php "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestEntityExtensionRendersImportsAndMigratesExternalTable(t *testing.T) {
	target := RelationTarget{
		DefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
		EntityClass:     `Shopware\Core\Content\Product\ProductEntity`,
		CollectionClass: `Shopware\Core\Content\Product\ProductCollection`,
		EntityName:      "product",
		Fields:          []RelationTargetField{{PropertyName: "id", StorageName: "id", Primary: true}},
	}
	spec := CompleteSpec(EntitySpec{
		Mode:                    "new",
		DefinitionKind:          DefinitionExtension,
		Namespace:               `Acme\Example\Extension\Content\Product`,
		ClassName:               "ProductCustom",
		EntityName:              target.EntityName,
		ExtendedDefinitionClass: target.DefinitionClass,
		ShopwareVersion:         "~6.7.0",
		Fields: []FieldSpec{
			extensionToOne("category", "acme_category_id"),
			{ID: "related", Kind: FieldOneToMany, PropertyName: "relatedProducts", ReferenceStorageName: "parent_id", SourceColumn: "id", TargetDefinitionClass: target.DefinitionClass, TargetEntityClass: target.EntityClass, TargetCollectionClass: target.CollectionClass, TargetEntityName: target.EntityName, Editable: true},
		},
	})

	require.Equal(t, `Acme\Example\Extension\Content\Product\ProductCustomExtension`, spec.DefinitionClass)
	require.Empty(t, spec.EntityClass)
	require.Empty(t, spec.CollectionClass)
	require.Empty(t, ValidateSpec(spec))

	source, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(source).Errors, source)
	require.Contains(t, source, "extends EntityExtension")
	require.Contains(t, source, "public function extendFields(FieldCollection $collection): void")
	require.Contains(t, source, "return ProductDefinition::ENTITY_NAME;")
	require.NotContains(t, source, "getDefinitionClass")
	require.Equal(t, 3, strings.Count(source, "new Extension()"), "the paired foreign key and both associations need the extension flag")
	require.NotContains(t, source, "CreatedAtField")
	require.NotContains(t, source, "UpdatedAtField")

	lookup := func(class string) (RelationTarget, bool) {
		if class == target.DefinitionClass {
			return target, true
		}
		return RelationTarget{}, false
	}
	imported, err := ImportExtension(source, lookup)
	require.NoError(t, err)
	require.Empty(t, imported.ProtectionMethodRaw)
	require.Equal(t, DefinitionExtension, imported.DefinitionKind)
	require.Equal(t, target.DefinitionClass, imported.ExtendedDefinitionClass)
	require.Equal(t, target.EntityName, imported.EntityName)
	require.Len(t, imported.Fields, 2)
	for _, field := range imported.Fields {
		require.NotEqual(t, FieldLocked, field.Kind, field.Raw)
	}

	before, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	after, err := SchemaFromSpec(imported)
	require.NoError(t, err)
	before.BackfillFree()
	after.BackfillFree()
	require.True(t, reflect.DeepEqual(before, after), "schema mismatch\nbefore: %#v\nafter: %#v", before, after)
	require.True(t, before.External)
	require.Equal(t, "product", before.Name)
	require.Contains(t, before.Columns, "acme_category_id")
	require.NotContains(t, before.Columns, "id")
	require.NotContains(t, before.Columns, "created_at")

	next := EmptySchema()
	require.NoError(t, MergeSpecSchema(&next, spec))
	statements, diff, err := MigrationStatements(EmptySchema(), next, nil)
	require.NoError(t, err)
	require.Empty(t, diff.CreatedEntities)
	require.Len(t, diff.AddedColumns, 1)
	joined := strings.Join(statements, "\n")
	require.Contains(t, joined, "ALTER TABLE `product` ADD COLUMN `acme_category_id` BINARY(16) NULL")
	require.Contains(t, joined, "REFERENCES `category` (`id`) ON DELETE SET NULL")
	require.NotContains(t, joined, "CREATE TABLE")
}

func TestEntityExtensionImportsShopware67LiteralEntityNameTarget(t *testing.T) {
	const source = `<?php declare(strict_types=1);

namespace Acme\Example;

use Shopware\Core\Framework\DataAbstractionLayer\EntityExtension;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;

class ProductExtension extends EntityExtension
{
    public function extendFields(FieldCollection $collection): void
    {
    }

    public function getEntityName(): string
    {
        return 'product';
    }
}
`
	target := RelationTarget{
		DefinitionClass:  `Shopware\Core\Content\Product\ProductDefinition`,
		EntityClass:      `Shopware\Core\Content\Product\ProductEntity`,
		CollectionClass:  `Shopware\Core\Content\Product\ProductCollection`,
		EntityName:       "product",
		Fields:           []RelationTargetField{{PropertyName: "id", StorageName: "id", Primary: true}},
		VersionAware:     true,
		InheritanceAware: true,
	}
	lookup := func(classOrEntityName string) (RelationTarget, bool) {
		if classOrEntityName == target.EntityName {
			return target, true
		}
		return RelationTarget{}, false
	}

	spec, err := ImportExtension(source, lookup)
	require.NoError(t, err)
	require.Equal(t, target.DefinitionClass, spec.ExtendedDefinitionClass)
	require.Equal(t, target.EntityName, spec.EntityName)
	require.Equal(t, target.Fields, spec.ExtendedFields)
}

func TestEntityExtensionRejectsImpureLiteralLookingTargetMethods(t *testing.T) {
	target := RelationTarget{DefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`, EntityName: "product"}
	lookup := func(classOrEntityName string) (RelationTarget, bool) {
		if classOrEntityName == target.EntityName || classOrEntityName == target.DefinitionClass {
			return target, true
		}
		return RelationTarget{}, false
	}
	for name, body := range map[string]string{
		"statement before return": "$this->audit(); return 'product';",
		"compound expression":     "return 'pro' . 'duct';",
	} {
		t.Run(name, func(t *testing.T) {
			source := `<?php namespace Acme\Example;
use Shopware\Core\Framework\DataAbstractionLayer\EntityExtension;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ProductExtension extends EntityExtension {
    public function getEntityName(): string { ` + body + ` }
    public function extendFields(FieldCollection $collection): void {}
}`
			_, err := ImportExtension(source, lookup)
			require.ErrorContains(t, err, "entity extension target")
		})
	}
}

func TestEntityExtensionIndexesCanCombineOwnedAndIndexedTargetColumns(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "edit", DefinitionKind: DefinitionExtension,
		Namespace: `Acme\Example\Extension`, ClassName: "Product", EntityName: "product",
		ExtendedDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
		ExtendedFields: []RelationTargetField{
			{PropertyName: "id", StorageName: "id", Primary: true},
			{PropertyName: "createdAt", StorageName: "created_at"},
		},
		Fields: []FieldSpec{extensionToOne("category", "acme_category_id")},
		Indexes: []IndexSpec{{
			Name: "uniq.product.acme_category_id_id", Kind: IndexUnique,
			Columns: []string{"acme_category_id", "id"},
		}},
	})

	require.Empty(t, ValidateSpec(spec))
	contribution, err := SchemaFromSpec(spec)
	require.NoError(t, err)
	require.Equal(t, []string{"acme_category_id", "id"}, contribution.Indexes["uniq.product.acme_category_id_id"].Columns)
	require.NotContains(t, contribution.Columns, "id", "the extension must not claim ownership of an existing target column")

	schema := EmptySchema()
	schema.Entities["product"] = contribution
	schema.Entities["product"].Indexes["idx.other_extension"] = Index{Name: "idx.other_extension", Columns: []string{"other_extension_column", "id"}}
	schema.Entities["product"].Indexes["idx.product.id"] = Index{Name: "idx.product.id", Columns: []string{"id"}}
	require.Equal(t, spec.Indexes, IndexSpecsFromEntities(spec, schema), "only indexes owned through this extension's columns are restored")

	invalid := spec
	invalid.Indexes = []IndexSpec{{Name: "idx.product.id", Kind: IndexNormal, Columns: []string{"id"}}}
	requireIssueCode(t, ValidateSpec(invalid), "entity.extension.index.ownedColumn.required")
	invalid.Indexes[0].Columns = []string{"missing"}
	requireIssueCode(t, ValidateSpec(invalid), "entity.index.column.unknown")
	invalid.Fields[0].StorageName = "id"
	invalid.Indexes = nil
	requireIssueCode(t, ValidateSpec(invalid), "entity.extension.column.duplicate")
}

func TestEntityExtensionBefore67IncludesLegacyTargetMethod(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", DefinitionKind: DefinitionExtension,
		Namespace: `Acme\Example`, ClassName: "Product", EntityName: "product",
		ExtendedDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
		ShopwareVersion:         "~6.6.0",
	})
	source, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Contains(t, source, "public function getEntityName(): string")
	require.Contains(t, source, "public function getDefinitionClass(): string")
	require.Contains(t, source, "return ProductDefinition::class;")
}

func TestAssociationOnlyEntityExtensionProducesNoDatabaseStatements(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", DefinitionKind: DefinitionExtension,
		Namespace: `Acme\Example`, ClassName: "Product", EntityName: "product",
		ExtendedDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
		Fields: []FieldSpec{{
			ID: "children", Kind: FieldOneToMany, PropertyName: "children",
			ReferenceStorageName: "parent_id", SourceColumn: "id",
			TargetDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
			TargetEntityClass:     `Shopware\Core\Content\Product\ProductEntity`,
			TargetCollectionClass: `Shopware\Core\Content\Product\ProductCollection`,
			TargetEntityName:      "product", Editable: true,
		}},
	})
	next := EmptySchema()
	require.NoError(t, MergeSpecSchema(&next, spec))
	require.True(t, next.Entities["product"].External)
	require.Empty(t, next.Entities["product"].Columns)
	statements, diff, err := MigrationStatements(EmptySchema(), next, nil)
	require.NoError(t, err)
	require.Empty(t, statements)
	require.False(t, diff.DatabaseChanged())
}

func TestEntityExtensionRewritePreservesCustomMembers(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", DefinitionKind: DefinitionExtension,
		Namespace: `Acme\Example`, ClassName: "Product", EntityName: "product",
		ExtendedDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
		ShopwareVersion:         "6.7.1.0",
		Fields:                  []FieldSpec{{ID: "note", Kind: FieldString, PropertyName: "note", StorageName: "acme_note", Behavior: &FieldBehavior{Runtime: true}, Editable: true}},
	})
	source, err := RenderDefinition(spec)
	require.NoError(t, err)
	source = strings.TrimSuffix(source, "}\n") + "    public function customBehavior(): bool { return true; }\n}\n"
	lookup := func(class string) (RelationTarget, bool) {
		return RelationTarget{DefinitionClass: class, EntityName: "product"}, true
	}
	previous, err := ImportExtension(source, lookup)
	require.NoError(t, err)
	next := previous
	next.ShopwareVersion = "6.7.1.0"
	next.Fields = append(next.Fields, FieldSpec{ID: "label", Kind: FieldString, PropertyName: "label", StorageName: "acme_label", Behavior: &FieldBehavior{Runtime: true}, Editable: true})
	rewritten, err := RewriteDefinition(source, next)
	require.NoError(t, err)
	require.Empty(t, php.Parse(rewritten).Errors, rewritten)
	require.Contains(t, rewritten, "customBehavior")
	require.Contains(t, rewritten, "new StringField('acme_label', 'label'")
	require.Equal(t, 1, strings.Count(rewritten, "function getEntityName"))
}

func TestEntityExtensionRewriteRetargetsManagedMethodsAndImports(t *testing.T) {
	previous := CompleteSpec(EntitySpec{
		Mode: "edit", DefinitionKind: DefinitionExtension,
		Namespace: `Acme\Example`, ClassName: "Catalog", EntityName: "product",
		ExtendedDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
		ShopwareVersion:         "~6.6.0",
		Fields:                  []FieldSpec{{ID: "note", Kind: FieldString, PropertyName: "note", StorageName: "acme_note", Behavior: &FieldBehavior{Runtime: true}, Editable: true}},
	})
	source, err := RenderDefinition(previous)
	require.NoError(t, err)
	source = strings.TrimSuffix(source, "}\n") + "    public function customBehavior(): bool { return true; }\n}\n"
	next := previous
	next.EntityName = "category"
	next.ExtendedDefinitionClass = `Shopware\Core\Content\Category\CategoryDefinition`

	rewritten, err := RewriteDefinitionFrom(source, previous, next)
	require.NoError(t, err)
	require.Empty(t, php.Parse(rewritten).Errors, rewritten)
	require.NotContains(t, rewritten, "ProductDefinition")
	require.Contains(t, rewritten, "use Shopware\\Core\\Content\\Category\\CategoryDefinition;")
	require.Contains(t, rewritten, "return CategoryDefinition::ENTITY_NAME;")
	require.Contains(t, rewritten, "return CategoryDefinition::class;")
	require.Contains(t, rewritten, "customBehavior")
}

func TestEntityExtensionSchemaContributionsMergeAndReplaceIndependently(t *testing.T) {
	extension := func(className, property, storage string) EntitySpec {
		return CompleteSpec(EntitySpec{
			Mode: "edit", DefinitionKind: DefinitionExtension,
			Namespace: `Acme\Example`, ClassName: className, EntityName: "product",
			ExtendedDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
			Fields:                  []FieldSpec{extensionToOne(property, storage)},
		})
	}
	first := extension("First", "first", "acme_first")
	second := extension("Second", "second", "acme_second")
	schema := EmptySchema()
	require.NoError(t, MergeSpecSchema(&schema, first))
	require.NoError(t, MergeSpecSchema(&schema, second))
	require.True(t, schema.Entities["product"].External)
	require.Contains(t, schema.Entities["product"].Columns, "acme_first")
	require.Contains(t, schema.Entities["product"].Columns, "acme_second")

	replacement := extension("First", "renamed", "acme_renamed")
	require.NoError(t, ReplaceSpecSchema(&schema, &first, replacement))
	require.NotContains(t, schema.Entities["product"].Columns, "acme_first")
	require.Contains(t, schema.Entities["product"].Columns, "acme_renamed")
	require.Contains(t, schema.Entities["product"].Columns, "acme_second")
}

func TestEntityExtensionRejectsRuntimeInvalidStoredFields(t *testing.T) {
	base := CompleteSpec(EntitySpec{
		Mode: "new", DefinitionKind: DefinitionExtension,
		Namespace: `Acme\Example`, ClassName: "Product", EntityName: "product",
		ExtendedDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
	})
	storedScalar := base
	storedScalar.Fields = []FieldSpec{{ID: "label", Kind: FieldString, PropertyName: "label", StorageName: "acme_label", Editable: true}}
	requireIssueCode(t, ValidateSpec(storedScalar), "entity.extension.field.runtime.required")

	standaloneFK := base
	standaloneFK.Fields = []FieldSpec{{
		ID: "category", Kind: FieldForeignKey, PropertyName: "categoryId", StorageName: "acme_category_id",
		TargetDefinitionClass: `Shopware\Core\Content\Category\CategoryDefinition`, TargetEntityName: "category",
		ReferenceStorageName: "id", Editable: true,
	}}
	requireIssueCode(t, ValidateSpec(standaloneFK), "entity.extension.foreignKey.association.required")

	runtimeScalar := storedScalar
	runtimeScalar.Fields[0].Behavior = &FieldBehavior{Runtime: true}
	require.Empty(t, ValidateSpec(runtimeScalar))
	entity, err := SchemaFromSpec(runtimeScalar)
	require.NoError(t, err)
	require.Empty(t, entity.Columns)
	definition, err := RenderDefinition(runtimeScalar)
	require.NoError(t, err)
	require.Contains(t, definition, "new Runtime()")
}

func TestEntityExtensionProtectionsRoundTripAndRewrite(t *testing.T) {
	spec := CompleteSpec(EntitySpec{
		Mode: "new", DefinitionKind: DefinitionExtension,
		Namespace: `Acme\Example`, ClassName: "Product", EntityName: "product",
		ExtendedDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
		ReadProtected:           true, ReadProtectionScopes: []string{"system", "user"},
		WriteProtected: true, WriteProtectionScopes: []string{"system"},
	})
	source, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(source).Errors, source)
	require.Contains(t, source, "function extendProtections(EntityProtectionCollection $protections): void")
	require.Contains(t, source, "$protections->add(new ReadProtection('system', 'user'));")
	require.Contains(t, source, "$protections->add(new WriteProtection('system'));")

	lookup := func(class string) (RelationTarget, bool) {
		return RelationTarget{DefinitionClass: class, EntityName: "product"}, true
	}
	imported, err := ImportExtension(source, lookup)
	require.NoError(t, err)
	require.True(t, imported.ReadProtected)
	require.Equal(t, []string{"system", "user"}, imported.ReadProtectionScopes)
	require.True(t, imported.WriteProtected)
	require.Equal(t, []string{"system"}, imported.WriteProtectionScopes)

	imported.WriteProtected = false
	imported.WriteProtectionScopes = nil
	_, class, parseErr := parsedPHPClass(source)
	require.NoError(t, parseErr)
	require.True(t, safeExtensionProtectionMethod(namedMethod(class, "extendProtections")))
	rewritten, err := RewriteDefinition(source, imported)
	require.NoError(t, err)
	require.Contains(t, rewritten, "new ReadProtection('system', 'user')")
	require.NotContains(t, rewritten, "new WriteProtection")

	custom := strings.Replace(source,
		"    public function extendProtections(EntityProtectionCollection $protections): void\n    {",
		"    public function extendProtections(EntityProtectionCollection $protections): void\n    {\n        // keep custom protection logic",
		1,
	)
	locked, err := ImportExtension(custom, lookup)
	require.NoError(t, err)
	require.Contains(t, locked.ProtectionMethodRaw, "keep custom protection logic")
	locked.Fields = append(locked.Fields, FieldSpec{ID: "runtime", Kind: FieldString, PropertyName: "runtime", StorageName: "runtime", Behavior: &FieldBehavior{Runtime: true}, Editable: true})
	rewritten, err = RewriteDefinition(custom, locked)
	require.NoError(t, err)
	require.Contains(t, rewritten, "keep custom protection logic")
}

func extensionToOne(property, storage string) FieldSpec {
	return FieldSpec{
		ID: property, Kind: FieldManyToOne, PropertyName: property,
		ForeignKeyPropertyName: property + "Id", StorageName: storage,
		TargetDefinitionClass: `Shopware\Core\Content\Category\CategoryDefinition`,
		TargetEntityClass:     `Shopware\Core\Content\Category\CategoryEntity`,
		TargetCollectionClass: `Shopware\Core\Content\Category\CategoryCollection`,
		TargetEntityName:      "category", ReferenceField: "id", ReferenceStorageName: "id",
		DeleteBehavior: DeleteSetNull, Editable: true,
	}
}

func TestPatchEntityExtensionServiceConfiguration(t *testing.T) {
	for _, path := range []string{"services.yaml", "services.xml"} {
		result, err := PatchTaggedServiceConfiguration(path, "", `Acme\Example\ProductExtension`, "shopware.entity.extension")
		require.NoError(t, err)
		require.Contains(t, result, `Acme\Example\ProductExtension`)
		require.Contains(t, result, "shopware.entity.extension")
	}
}
