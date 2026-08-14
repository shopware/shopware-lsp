package entityschema

import (
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

func importSimpleDefinitionField(
	itemIndex int,
	id, raw, kindName string,
	creation *phpsyntax.Node,
	flags importedFlagSet,
	modifiers importedModifierSet,
	resolve func(string) string,
	lookup RelationLookup,
) (FieldSpec, *TranslationSpec, bool) {
	base := FieldSpec{
		ID: id, Required: flags.required, Primary: flags.primary, APIAware: flags.apiAware,
		APIAwareSources: append([]string(nil), flags.apiAwareSources...),
		SearchRanking:   flags.ranking, SearchRankingTokenize: flags.rankingTokenize,
		Behavior: flags.behavior, Metadata: flags.metadata,
		Inherited: flags.inherited, InheritedForeignKey: flags.inheritedForeignKey,
		PreservedFlags: flags.preserved, Editable: true, Raw: raw,
	}
	switch kindName {
	case "TranslatedField":
		apiAware := flags.apiAware
		ranking := flags.ranking
		return FieldSpec{
			ID: id, Kind: FieldLocked, PropertyName: importedStringArgument(creation, 0),
			Translated: true, TranslationUseForSort: importedBoolArgument(creation, 1),
			TranslationAPIAware: &apiAware, TranslationSearchRank: &ranking,
			TranslationAPIAwareSources: append([]string(nil), flags.apiAwareSources...),
			TranslationSearchTokenize:  flags.rankingTokenize, TranslationBehavior: flags.behavior, TranslationMetadata: flags.metadata,
			TranslationInherited: flags.inherited, TranslationInheritedFK: flags.inheritedForeignKey,
			TranslationFlags: flags.preserved, TranslationBeforeFlags: modifiers.beforeFlags,
			TranslationAfterFlags: modifiers.afterFlags, Editable: false, Raw: raw,
		}, nil, true
	case "TranslationsAssociationField":
		return FieldSpec{}, &TranslationSpec{
			Enabled: true, DefinitionClass: resolve(importedClassArgument(creation, 0)),
			ParentStorageName:     defaultString(importedStringArgument(creation, 1), "id"),
			AssociationProperty:   defaultString(importedStringArgument(creation, 2), "translations"),
			AssociationLocalField: defaultString(importedStringArgument(creation, 3), "id"),
			AssociationRequired:   flags.required, AssociationAPIAware: flags.apiAware,
			AssociationAPIAwareSources: append([]string(nil), flags.apiAwareSources...),
			AssociationBehavior:        flags.behavior, AssociationMetadata: flags.metadata,
			AssociationInherited: flags.inherited, AssociationInheritedFK: flags.inheritedForeignKey,
			ReverseInheritedProperty: flags.reverseInheritedProperty,
			AssociationFlags:         flags.preserved, AssociationBeforeFlags: modifiers.beforeFlags,
			AssociationAfterFlags: modifiers.afterFlags,
		}, true
	case "IdField":
		base.StorageName = importedStringArgument(creation, 0)
		base.PropertyName = importedStringArgument(creation, 1)
		base.Kind = FieldBinaryID
		if base.StorageName == "id" && base.PropertyName == "id" {
			base.Kind = FieldID
		}
	case "StringField", "LongTextField", "IntField", "FloatField", "BoolField", "DateField", "DateTimeField", "JsonField", "ListField", "ObjectField", "BlobField":
		base.Kind = importedScalarKind(kindName)
		base.StorageName = importedStringArgument(creation, 0)
		base.PropertyName = importedStringArgument(creation, 1)
		if base.Kind == FieldString {
			base.MaxLength = importedIntArgument(creation, 2, 255)
		}
		if base.Kind == FieldInt {
			base.Min = importedOptionalIntArgument(creation, 2)
			base.Max = importedOptionalIntArgument(creation, 3)
		}
		if base.Kind == FieldList {
			base.ElementTypeClass = resolve(importedClassArgument(creation, 2))
		}
		if base.Kind == FieldJSON {
			arguments := phpquery.Arguments(creation)
			if len(arguments) > 2 {
				base.JSONPropertyMappingExpression = normalizeImportedPHPExpression(phpquery.ArgumentValueText(creation, 2), resolve)
			}
			if len(arguments) > 3 {
				base.JSONDefaultExpression = normalizeImportedPHPExpression(phpquery.ArgumentValueText(creation, 3), resolve)
			}
		}
	case "EnumField":
		base.EnumClass, base.EnumCase = importedEnumCase(creation, resolve)
		if base.EnumClass == "" || base.EnumCase == "" {
			return lockedField(itemIndex, raw), nil, true
		}
		base.Kind = FieldEnum
		base.StorageName = importedStringArgument(creation, 0)
		base.PropertyName = importedStringArgument(creation, 1)
	case "AutoIncrementField":
		base.Kind = FieldAutoIncrement
		base.PropertyName = "autoIncrement"
		base.StorageName = "auto_increment"
		base.Required = true
	case "CreatedAtField":
		base.Kind = FieldCreatedAt
		base.PropertyName = "createdAt"
		base.StorageName = "created_at"
		base.Required = true
	case "UpdatedAtField":
		base.Kind = FieldUpdatedAt
		base.PropertyName = "updatedAt"
		base.StorageName = "updated_at"
	case "VersionField":
		base.Kind = FieldVersion
		base.PropertyName = "versionId"
		base.StorageName = "version_id"
		base.Required = true
	case "ReferenceVersionField":
		return importReferenceVersionField(id, raw, creation, flags, modifiers, resolve, lookup), nil, true
	default:
		return FieldSpec{}, nil, false
	}
	return withFieldModifiers(base, modifiers), nil, true
}
