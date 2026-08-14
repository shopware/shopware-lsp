package entityschema

import (
	"sort"
	"strings"
	"unicode"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/project"
)

const (
	constructorStorageProperty = "storage-property"
	constructorFixed           = "fixed"
)

type specializedFieldDescriptor struct {
	kind               FieldKind
	mode               string
	defaultStorage     string
	defaultProperty    string
	entityType         string
	templateEntityType string
	booleanGetter      bool
	entityTrait        string
	manageEntity       bool
	implicitComputed   bool
	targetDefinition   string
	targetEntity       string
	targetName         string
	maxLength          int
	maxLengthArgument  bool
	minimumAdditional  int
	minimumVersion     project.Version
}

var specializedFieldDescriptors = map[string]specializedFieldDescriptor{
	"CustomFields":              {kind: FieldJSON, mode: constructorStorageProperty, defaultStorage: "custom_fields", defaultProperty: "customFields", templateEntityType: "array", entityTrait: `Shopware\Core\Framework\DataAbstractionLayer\EntityCustomFieldsTrait`},
	"CalculatedPriceField":      {kind: FieldJSON, mode: constructorStorageProperty, templateEntityType: `Shopware\Core\Checkout\Cart\Price\Struct\CalculatedPrice`},
	"CashRoundingConfigField":   {kind: FieldJSON, mode: constructorStorageProperty, templateEntityType: `Shopware\Core\Framework\DataAbstractionLayer\Pricing\CashRoundingConfig`, minimumVersion: project.Version{Major: 6, Minor: 4}},
	"PriceField":                {kind: FieldJSON, mode: constructorStorageProperty, templateEntityType: `Shopware\Core\Framework\DataAbstractionLayer\Pricing\PriceCollection`},
	"TaxFreeConfigField":        {kind: FieldJSON, mode: constructorStorageProperty, templateEntityType: `Shopware\Core\Framework\DataAbstractionLayer\TaxFreeConfig`, minimumVersion: project.Version{Major: 6, Minor: 4}},
	"VersionDataPayloadField":   {kind: FieldJSON, mode: constructorStorageProperty, templateEntityType: "array"},
	"VariantListingConfigField": {kind: FieldJSON, mode: constructorStorageProperty, templateEntityType: `Shopware\Core\Content\Product\DataAbstractionLayer\VariantListingConfig`},
	"SlotConfigField":           {kind: FieldJSON, mode: constructorStorageProperty, templateEntityType: "array"},
	"PriceDefinitionField":      {kind: FieldJSON, mode: constructorStorageProperty, templateEntityType: `Shopware\Core\Checkout\Cart\Price\Struct\PriceDefinitionInterface`},
	"FlowTemplateConfigField":   {kind: FieldJSON, mode: constructorStorageProperty, templateEntityType: "array"},
	"CheapestPriceField":        {kind: FieldJSON, mode: constructorStorageProperty, templateEntityType: `Shopware\Core\Content\Product\DataAbstractionLayer\CheapestPrice\CheapestPrice|Shopware\Core\Content\Product\DataAbstractionLayer\CheapestPrice\CheapestPriceContainer`},
	"CartPriceField":            {kind: FieldJSON, mode: constructorStorageProperty, templateEntityType: `Shopware\Core\Checkout\Cart\Price\Struct\CartPrice`},
	"BreadcrumbField":           {kind: FieldJSON, mode: constructorStorageProperty, defaultStorage: "breadcrumb", defaultProperty: "breadcrumb", templateEntityType: "array", minimumVersion: project.Version{Major: 6, Minor: 4, Patch: 17}},
	"TreeBreadcrumbField":       {kind: FieldJSON, mode: constructorStorageProperty, defaultStorage: "breadcrumb", defaultProperty: "breadcrumb", templateEntityType: "array"},
	"ConfigJsonField":           {kind: FieldJSON, mode: constructorStorageProperty, templateEntityType: "array|bool|float|int|string"},
	"MeasurementUnitsField":     {kind: FieldObject, mode: constructorStorageProperty, templateEntityType: `Shopware\Core\Content\MeasurementSystem\MeasurementUnits`, minimumVersion: project.Version{Major: 6, Minor: 7, Patch: 1}},
	"ManyToManyIdField":         {kind: FieldList, mode: constructorStorageProperty, minimumAdditional: 1, templateEntityType: "array"},
	"NumberRangeField":          {kind: FieldString, mode: constructorStorageProperty, entityType: "string", manageEntity: true, maxLength: 64, maxLengthArgument: true},
	"PasswordField":             {kind: FieldString, mode: constructorStorageProperty, entityType: "string", manageEntity: true},
	"EmailField":                {kind: FieldString, mode: constructorStorageProperty, entityType: "string", manageEntity: true},
	"TimeZoneField":             {kind: FieldString, mode: constructorStorageProperty, entityType: "string", manageEntity: true},
	"RemoteAddressField":        {kind: FieldString, mode: constructorStorageProperty, entityType: "string", manageEntity: true},
	"CronIntervalField":         {kind: FieldString, mode: constructorStorageProperty, entityType: "string", manageEntity: true},
	"DateIntervalField":         {kind: FieldString, mode: constructorStorageProperty, entityType: "string", manageEntity: true},
	"TreePathField":             {kind: FieldLongText, mode: constructorStorageProperty, entityType: "string", manageEntity: true},
	"TreeLevelField":            {kind: FieldInt, mode: constructorStorageProperty, entityType: "int", manageEntity: true},
	"ChildCountField":           {kind: FieldInt, mode: constructorFixed, defaultStorage: "child_count", defaultProperty: "childCount", entityType: "int", manageEntity: true},
	"WasModifiedByUserField":    {kind: FieldBool, mode: constructorStorageProperty, defaultStorage: "was_modified_by_user", defaultProperty: "wasModifiedByUser", entityType: "bool", booleanGetter: true, manageEntity: true},
	"LockedField":               {kind: FieldBool, mode: constructorFixed, defaultStorage: "locked", defaultProperty: "locked", entityType: "bool", booleanGetter: true, manageEntity: true, implicitComputed: true},
	"CreatedByField": {
		kind: FieldForeignKey, mode: constructorFixed, defaultStorage: "created_by_id", defaultProperty: "createdById", entityType: "string", manageEntity: true,
		minimumVersion:   project.Version{Major: 6, Minor: 3, Patch: 5},
		targetDefinition: `Shopware\Core\System\User\UserDefinition`, targetEntity: `Shopware\Core\System\User\UserEntity`, targetName: "user",
	},
	"UpdatedByField": {
		kind: FieldForeignKey, mode: constructorFixed, defaultStorage: "updated_by_id", defaultProperty: "updatedById", entityType: "string", manageEntity: true,
		minimumVersion:   project.Version{Major: 6, Minor: 3, Patch: 5},
		targetDefinition: `Shopware\Core\System\User\UserDefinition`, targetEntity: `Shopware\Core\System\User\UserEntity`, targetName: "user",
	},
	"StateMachineStateField": {
		kind: FieldForeignKey, mode: constructorStorageProperty, entityType: "string", manageEntity: true, minimumAdditional: 1,
		targetDefinition: `Shopware\Core\System\StateMachine\Aggregation\StateMachineState\StateMachineStateDefinition`,
		targetEntity:     `Shopware\Core\System\StateMachine\Aggregation\StateMachineState\StateMachineStateEntity`, targetName: "state_machine_state",
	},
}

func importSpecializedField(
	id, raw string,
	creation *phpsyntax.Node,
	flags importedFlagSet,
	modifiers importedModifierSet,
	resolve func(string) string,
) (FieldSpec, bool) {
	shortName := ShortClass(phpquery.ObjectClassName(creation))
	descriptor, found := specializedFieldDescriptors[shortName]
	if !found {
		return FieldSpec{}, false
	}
	implementation := &FieldImplementation{
		Class: resolve(phpquery.ObjectClassName(creation)), ConstructorMode: descriptor.mode,
		FixedStorageName: descriptor.defaultStorage, FixedPropertyName: descriptor.defaultProperty,
		EntityType: descriptor.entityType, EntityBooleanGetter: descriptor.booleanGetter,
		EntityTrait:  descriptor.entityTrait,
		ManageEntity: descriptor.manageEntity, ImplicitComputed: descriptor.implicitComputed,
		MaxLengthArgument:          descriptor.maxLengthArgument,
		MinimumAdditionalArguments: descriptor.minimumAdditional,
	}
	arguments := phpquery.Arguments(creation)
	field := FieldSpec{
		ID: id, Kind: descriptor.kind, StorageName: descriptor.defaultStorage, PropertyName: descriptor.defaultProperty,
		Required: flags.required, Primary: flags.primary, APIAware: flags.apiAware,
		APIAwareSources: append([]string(nil), flags.apiAwareSources...),
		SearchRanking:   flags.ranking, SearchRankingTokenize: flags.rankingTokenize, Behavior: flags.behavior,
		Metadata:  flags.metadata,
		Inherited: flags.inherited, InheritedForeignKey: flags.inheritedForeignKey,
		PreservedFlags: flags.preserved, Implementation: implementation, Editable: true, Raw: raw,
		TargetDefinitionClass: descriptor.targetDefinition, TargetEntityClass: descriptor.targetEntity,
		TargetEntityName: descriptor.targetName, ReferenceField: "id", ReferenceStorageName: "id",
		MaxLength: descriptor.maxLength,
	}
	if descriptor.mode == constructorStorageProperty {
		if len(arguments) > 0 {
			field.StorageName = importedStringArgument(creation, 0)
		}
		if len(arguments) > 1 {
			field.PropertyName = importedStringArgument(creation, 1)
		}
		for index := 2; index < len(arguments); index++ {
			implementation.AdditionalArguments = append(implementation.AdditionalArguments, normalizeImportedPHPExpression(phpquery.ArgumentValueText(creation, index), resolve))
		}
	} else {
		for index := range arguments {
			implementation.FixedArguments = append(implementation.FixedArguments, normalizeImportedPHPExpression(phpquery.ArgumentValueText(creation, index), resolve))
		}
	}
	if shortName == "NumberRangeField" && len(arguments) > 2 {
		field.MaxLength = importedIntArgument(creation, 2, descriptor.maxLength)
	}
	return withFieldModifiers(field, modifiers), field.StorageName != "" && field.PropertyName != ""
}

type SpecializedFieldTemplate struct {
	ID                     string    `json:"id"`
	Label                  string    `json:"label"`
	MinimumShopwareVersion string    `json:"minimumShopwareVersion,omitempty"`
	Field                  FieldSpec `json:"field"`
}

// SpecializedFieldTemplates returns safe creation templates for every native
// Shopware storage-aware Field subclass known by the importer. Structured
// value fields use an array entity member for new code, while imported code
// keeps its existing entity implementation unmanaged.
func SpecializedFieldTemplates() []SpecializedFieldTemplate {
	names := make([]string, 0, len(specializedFieldDescriptors))
	for name := range specializedFieldDescriptors {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]SpecializedFieldTemplate, 0, len(names))
	for _, name := range names {
		descriptor := specializedFieldDescriptors[name]
		entityType := descriptor.templateEntityType
		manageEntity := descriptor.manageEntity
		if entityType != "" {
			manageEntity = true
		}
		if entityType == "" {
			entityType = descriptor.entityType
		}
		if entityType == "" {
			manageEntity = true
			switch descriptor.kind {
			case FieldJSON, FieldList, FieldObject:
				entityType = "array"
			default:
				continue
			}
		}
		field := FieldSpec{
			Kind: descriptor.kind, StorageName: descriptor.defaultStorage, PropertyName: descriptor.defaultProperty,
			TargetDefinitionClass: descriptor.targetDefinition, TargetEntityClass: descriptor.targetEntity,
			TargetEntityName: descriptor.targetName, ReferenceField: "id", ReferenceStorageName: "id",
			MaxLength: descriptor.maxLength, Editable: true,
			Implementation: &FieldImplementation{
				Class: specializedFieldClass(name), ConstructorMode: descriptor.mode,
				FixedStorageName: descriptor.defaultStorage, FixedPropertyName: descriptor.defaultProperty,
				EntityType: entityType, EntityBooleanGetter: descriptor.booleanGetter, EntityTrait: descriptor.entityTrait, ManageEntity: manageEntity,
				ImplicitComputed: descriptor.implicitComputed, MaxLengthArgument: descriptor.maxLengthArgument,
				MinimumAdditionalArguments: descriptor.minimumAdditional,
			},
		}
		minimumVersion := ""
		if descriptor.minimumVersion.Major != 0 {
			minimumVersion = descriptor.minimumVersion.String()
		}
		result = append(result, SpecializedFieldTemplate{
			ID: "specialized:" + name, Label: splitSpecializedLabel(name),
			MinimumShopwareVersion: minimumVersion, Field: field,
		})
	}
	return result
}

// SpecializedFieldSupported reports whether a known native field subclass is
// available at the lower bound of the configured Shopware version constraint.
// Unknown versions and third-party subclasses remain available.
func SpecializedFieldSupported(className, versionConstraint string) bool {
	descriptor, found := specializedFieldDescriptors[ShortClass(className)]
	if !found || descriptor.minimumVersion.Major == 0 {
		return true
	}
	version, known := project.ParseVersionConstraint(versionConstraint)
	return !known || version.Compare(descriptor.minimumVersion) >= 0
}

func specializedFieldClass(name string) string {
	switch name {
	case "SlotConfigField":
		return `Shopware\Core\Content\Cms\DataAbstractionLayer\Field\SlotConfigField`
	case "FlowTemplateConfigField":
		return `Shopware\Core\Content\Flow\DataAbstractionLayer\Field\FlowTemplateConfigField`
	case "CheapestPriceField":
		return `Shopware\Core\Content\Product\DataAbstractionLayer\CheapestPrice\CheapestPriceField`
	case "MeasurementUnitsField":
		return `Shopware\Core\Content\MeasurementSystem\Field\MeasurementUnitsField`
	case "NumberRangeField":
		return `Shopware\Core\System\NumberRange\DataAbstractionLayer\NumberRangeField`
	default:
		return `Shopware\Core\Framework\DataAbstractionLayer\Field\` + name
	}
}

func splitSpecializedLabel(name string) string {
	name = strings.TrimSuffix(name, "Field")
	var result []rune
	for index, char := range []rune(name) {
		if index > 0 && unicode.IsUpper(char) {
			result = append(result, ' ')
		}
		result = append(result, char)
	}
	return string(result) + " (Shopware)"
}
