package entityschema

import (
	"strings"
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestRewriteDefinitionPreservesCustomMembers(t *testing.T) {
	spec := exampleSpec()
	original, err := RenderDefinition(spec)
	require.NoError(t, err)
	original = original[:len(original)-2] + "\n    public function custom(): string { return 'keep'; }\n}\n"
	spec.Fields = append(spec.Fields, FieldSpec{ID: "description", Kind: FieldLongText, PropertyName: "description", StorageName: "description", Editable: true})
	result, err := RewriteDefinition(original, spec)
	require.NoError(t, err)
	require.Contains(t, result, "function custom")
	require.Contains(t, result, "new LongTextField('description', 'description')")
	require.Empty(t, phpparser.Parse(result).Errors)
}

func TestRewriteDefinitionFromRemovesOnlyUnusedPreviousSchemaImports(t *testing.T) {
	previous := exampleSpec()
	previous.Indexes = nil
	previous.Translation = &TranslationSpec{
		Enabled:             true,
		DefinitionClass:     `Acme\Example\Entity\Catalog\Aggregate\CatalogItemTranslation\CatalogItemTranslationDefinition`,
		EntityClass:         `Acme\Example\Entity\Catalog\Aggregate\CatalogItemTranslation\CatalogItemTranslationEntity`,
		CollectionClass:     `Acme\Example\Entity\Catalog\Aggregate\CatalogItemTranslation\CatalogItemTranslationCollection`,
		AssociationProperty: "translations",
		ParentPropertyName:  "catalogItem",
		ParentStorageName:   "acme_catalog_item_id",
	}
	previous.Fields[1].Translated = true
	source, err := RenderDefinition(previous)
	require.NoError(t, err)
	source = strings.Replace(source, "use Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityDefinition;", "use Vendor\\CustomHelper;\nuse Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityDefinition;", 1)
	source = source[:len(source)-2] + "\n    public function custom(): string { return CustomHelper::class; }\n}\n"

	next := previous
	next.Fields = append([]FieldSpec(nil), previous.Fields...)
	next.Translation = nil
	next.Fields[1].Translated = false
	result, err := RewriteDefinitionFrom(source, previous, next)
	require.NoError(t, err)
	require.NotContains(t, result, "TranslationsAssociationField")
	require.NotContains(t, result, "TranslatedField")
	require.NotContains(t, result, "CatalogItemTranslationDefinition")
	require.Contains(t, result, "use Vendor\\CustomHelper;")
	require.Contains(t, result, "return CustomHelper::class")
	require.Empty(t, phpparser.Parse(result).Errors)
}

func TestRewriteDefinitionFromRenamesLiteralEntityIdentity(t *testing.T) {
	previous := exampleSpec()
	previous.Mode = "edit"
	source, err := RenderDefinition(previous)
	require.NoError(t, err)
	next := previous
	next.EntityName = "acme_catalog_entry"

	rewritten, err := RewriteDefinitionFrom(source, previous, next)
	require.NoError(t, err)
	require.Contains(t, rewritten, "const ENTITY_NAME = 'acme_catalog_entry';")
	require.NotContains(t, rewritten, "const ENTITY_NAME = 'acme_catalog_item';")
	require.Empty(t, phpparser.Parse(rewritten).Errors)

	custom := strings.Replace(source, "final public const ENTITY_NAME", "#[CustomIdentity]\n    final public const ENTITY_NAME", 1)
	_, err = RewriteDefinitionFrom(custom, previous, next)
	require.ErrorContains(t, err, "cannot overwrite customized ENTITY_NAME constant")
}

func TestRewriteTranslationDefinitionFromRenamesLiteralEntityIdentity(t *testing.T) {
	previous := CompleteSpec(EntitySpec{
		Mode: "edit", Namespace: `Acme\Example`, ClassName: "Article", EntityName: "acme_article",
		Fields: []FieldSpec{
			{ID: "id", Kind: FieldID, Editable: true},
			{ID: "name", Kind: FieldString, PropertyName: "name", StorageName: "name", Translated: true, Editable: true},
		},
	})
	source, err := RenderTranslationDefinition(previous)
	require.NoError(t, err)
	next := previous
	next.Translation = cloneTranslation(previous.Translation)
	next.Translation.EntityName = "acme_article_locale"

	rewritten, err := RewriteTranslationDefinitionFrom(source, previous, next)
	require.NoError(t, err)
	require.Contains(t, rewritten, "const ENTITY_NAME = 'acme_article_locale';")
	require.NotContains(t, rewritten, "const ENTITY_NAME = 'acme_article_translation';")
	require.Empty(t, phpparser.Parse(rewritten).Errors)
}

func TestRewriteCollectionsRenameOnlySafeAPIAliases(t *testing.T) {
	previous := exampleSpec()
	next := previous
	next.EntityName = "acme_catalog_entry"
	source, err := RenderCollection(previous)
	require.NoError(t, err)
	source = strings.TrimSuffix(source, "}\n") + "    public function custom(): bool { return true; }\n}\n"

	rewritten, err := RewriteCollection(source, previous, next)
	require.NoError(t, err)
	require.Contains(t, rewritten, "return 'acme_catalog_entry_collection';")
	require.Contains(t, rewritten, "function custom")

	custom := strings.Replace(source, "return 'acme_catalog_item_collection';", "return $this->customAlias();", 1)
	_, err = RewriteCollection(custom, previous, next)
	require.ErrorContains(t, err, "cannot overwrite customized collection getApiAlias")

	previous = CompleteSpec(EntitySpec{
		Mode: "edit", Namespace: `Acme\Example`, ClassName: "Article", EntityName: "acme_article",
		Fields: []FieldSpec{{ID: "id", Kind: FieldID, Editable: true}, {ID: "name", Kind: FieldString, PropertyName: "name", StorageName: "name", Translated: true, Editable: true}},
	})
	next = previous
	next.Translation = cloneTranslation(previous.Translation)
	next.Translation.EntityName = "acme_article_locale"
	translationSource, err := RenderTranslationCollection(previous)
	require.NoError(t, err)
	translationRewritten, err := RewriteTranslationCollection(translationSource, previous, next)
	require.NoError(t, err)
	require.Contains(t, translationRewritten, "return 'acme_article_locale_collection';")
}

func cloneTranslation(value *TranslationSpec) *TranslationSpec {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func TestRewriteEntityPreservesCustomMembers(t *testing.T) {
	before := exampleSpec()
	original, err := RenderEntity(before)
	require.NoError(t, err)
	original = original[:len(original)-2] + "\n    public function custom(): string { return 'keep'; }\n}\n"
	after := before
	after.Fields = append(after.Fields, FieldSpec{ID: "description", Kind: FieldLongText, PropertyName: "description", StorageName: "description", Editable: true})
	result, err := RewriteEntity(original, before, after)
	require.NoError(t, err)
	require.Contains(t, result, "function custom")
	require.Contains(t, result, "$description")
	require.Empty(t, phpparser.Parse(result).Errors)
}

func TestRewriteDefinitionFromSupportsEveryClassBasedKindTransition(t *testing.T) {
	kinds := []DefinitionKind{DefinitionEntity, DefinitionMapping, DefinitionExtension, DefinitionBulkExtension}
	for _, from := range kinds {
		for _, to := range kinds {
			if from == to {
				continue
			}
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				previous := transitionSpec(from)
				source, err := RenderDefinition(previous)
				require.NoError(t, err)
				source = source[:len(source)-2] + "\n    public function custom(): string { return 'keep'; }\n}\n"
				next := transitionSpec(to)
				next.DefinitionClass = previous.DefinitionClass

				result, err := RewriteDefinitionFrom(source, previous, next)
				require.NoError(t, err)
				require.Empty(t, phpparser.Parse(result).Errors, result)
				require.Contains(t, result, "function custom")
				require.Contains(t, result, "extends "+ShortClass(definitionParentClass(to)))
				switch to {
				case DefinitionBulkExtension:
					require.Contains(t, result, "function collect")
					require.NotContains(t, result, "function defineFields")
					require.NotContains(t, result, "function extendFields")
					require.NotContains(t, result, "const ENTITY_NAME")
				case DefinitionExtension:
					require.Contains(t, result, "function extendFields")
					require.NotContains(t, result, "function defineFields")
					require.NotContains(t, result, "const ENTITY_NAME")
				default:
					require.Contains(t, result, "function defineFields")
					require.NotContains(t, result, "function extendFields")
					require.Contains(t, result, "const ENTITY_NAME")
					if to == DefinitionMapping && next.EntityClass == "" && next.CollectionClass == "" {
						require.NotContains(t, result, "function getEntityClass")
						require.NotContains(t, result, "function getCollectionClass")
					}
				}
			})
		}
	}
}

func transitionSpec(kind DefinitionKind) EntitySpec {
	spec := EntitySpec{
		Mode: "edit", DefinitionKind: kind,
		Namespace: `Acme\Example`, ClassName: "Demo", EntityName: "acme_demo",
		DefinitionClass: `Acme\Example\DemoDefinition`,
		ShopwareVersion: "6.7.1.0", CreateMigration: true,
	}
	switch kind {
	case DefinitionEntity:
		spec.Fields = []FieldSpec{{ID: "id", Kind: FieldID, PropertyName: "id", StorageName: "id", Required: true, Primary: true, Editable: true}}
	case DefinitionMapping:
		spec.Fields = []FieldSpec{{ID: "left", Kind: FieldBinaryID, PropertyName: "leftId", StorageName: "left_id", Required: true, Primary: true, Editable: true}}
	case DefinitionExtension:
		spec.EntityName = "product"
		spec.ExtendedDefinitionClass = `Shopware\Core\Content\Product\ProductDefinition`
		spec.Fields = []FieldSpec{{ID: "note", Kind: FieldString, PropertyName: "note", StorageName: "acme_note", Behavior: &FieldBehavior{Runtime: true}, Editable: true}}
	case DefinitionBulkExtension:
		spec.EntityName = ""
		spec.BulkExtensions = []BulkExtensionTargetSpec{{
			ID: "product", EntityName: "product",
			ExtendedDefinitionClass: `Shopware\Core\Content\Product\ProductDefinition`,
			Fields:                  []FieldSpec{{ID: "note", Kind: FieldString, PropertyName: "note", StorageName: "acme_note", Behavior: &FieldBehavior{Runtime: true}, Editable: true}},
		}}
	}
	return CompleteSpec(spec)
}
