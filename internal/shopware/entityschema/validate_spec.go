package entityschema

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

var (
	classNamePattern          = regexp.MustCompile(`^[A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*$`)
	entityNamePattern         = regexp.MustCompile(`^_*[a-z][a-z0-9_]*$`)
	propertyPattern           = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	storagePattern            = regexp.MustCompile(`^_*[a-z][a-z0-9_]*$`)
	associationStoragePattern = regexp.MustCompile(`^_*[a-z][a-z0-9_]*(?:\._*[a-z][a-z0-9_]*)*$`)
	indexNamePattern          = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	namespacePattern          = regexp.MustCompile(`^\\?[A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*(?:\\[A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*)*$`)
)

type ValidationIssue struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	FieldID  string `json:"fieldId,omitempty"`
	Severity string `json:"severity"`
}

type existingColumnReference struct {
	name         string
	fieldID      string
	allowMissing bool
}

type identityRequirement struct {
	actual  string
	suffix  string
	code    string
	message string
}

type specValidator struct {
	spec EntitySpec

	issues                   []ValidationIssue
	properties               map[string]FieldSpec
	storageNames             map[string]FieldSpec
	extendedStorageNames     map[string]RelationTargetField
	translatedStorageNames   map[string]string
	existingColumnReferences []existingColumnReference
	referenceVersionTargets  map[string][]FieldSpec
	requiredRelationTargets  map[string]string
	idCount                  int
	autoIncrementCount       int
	primaryCount             int
	hierarchyCount           int
}

func newSpecValidator(spec EntitySpec) *specValidator {
	return &specValidator{
		spec:                    spec,
		properties:              make(map[string]FieldSpec),
		storageNames:            make(map[string]FieldSpec),
		extendedStorageNames:    make(map[string]RelationTargetField),
		translatedStorageNames:  make(map[string]string),
		referenceVersionTargets: make(map[string][]FieldSpec),
		requiredRelationTargets: make(map[string]string),
	}
}

func ValidateSpec(spec EntitySpec) []ValidationIssue {
	spec = CompleteSpec(spec)
	validator := newSpecValidator(spec)
	if spec.DefinitionKind == DefinitionExtension {
		for _, field := range spec.ExtendedFields {
			if storagePattern.MatchString(field.StorageName) {
				validator.extendedStorageNames[field.StorageName] = field
			}
		}
	}
	validator.validateIdentity()
	if spec.DefinitionKind == DefinitionBulkExtension {
		validateBulkExtensionSpec(validator)
		return validator.issues
	}
	if spec.DefinitionBehavior != nil && spec.DefinitionBehavior.OverrideDefaultFields {
		for _, field := range spec.DefinitionBehavior.DefaultFields {
			validator.validateField(field)
		}
	}
	if spec.DefinitionBehavior != nil && spec.DefinitionBehavior.OverrideBaseFields {
		for _, field := range spec.DefinitionBehavior.BaseFields {
			validator.validateField(field)
		}
	}
	for _, field := range spec.Fields {
		validator.validateField(field)
	}
	validator.validateFieldSet()
	validator.validateIndexes()
	validateFieldModifications(validator)
	return validator.issues
}

func (v *specValidator) add(code, message, fieldID string) {
	v.issues = append(v.issues, ValidationIssue{
		Code: code, Message: message, FieldID: fieldID, Severity: "error",
	})
}

func (v *specValidator) validateIdentity() {
	spec := v.spec
	if spec.DefinitionKind != DefinitionEntity && spec.DefinitionKind != DefinitionMapping &&
		spec.DefinitionKind != DefinitionExtension && spec.DefinitionKind != DefinitionBulkExtension {
		v.add("entity.definitionKind.invalid", "Definition kind must be entity, mapping, extension, or bulk-extension", "")
	}
	if spec.DefinitionKind == DefinitionBulkExtension && !BulkEntityExtensionSupported(spec.ShopwareVersion) {
		v.add(
			"entity.bulkExtension.version.unsupported",
			"BulkEntityExtension requires Shopware 6.6.10 or newer throughout the configured compatibility range",
			"",
		)
	}
	if strings.TrimSpace(spec.CollectMethodRaw) != "" && spec.DefinitionKind != DefinitionBulkExtension {
		v.add("entity.bulkExtension.collectRaw.owner.unsupported", "A preserved collect method belongs only to BulkEntityExtension", "")
	}
	if strings.TrimSpace(spec.CollectMethodRaw) != "" && spec.Mode != "edit" {
		v.add("entity.bulkExtension.collectRaw.creation.unsupported", "Custom collect methods can only be preserved from an existing BulkEntityExtension", "")
	}
	if !classNamePattern.MatchString(spec.ClassName) {
		v.add("entity.class.invalid", "Enter a valid PHP base class name", "")
	}
	if spec.DefinitionKind != DefinitionBulkExtension && !entityNamePattern.MatchString(spec.EntityName) {
		v.add("entity.name.invalid", "Entity name must use lowercase snake_case", "")
	} else if spec.DefinitionKind != DefinitionBulkExtension && len(spec.EntityName) > 64 {
		v.add("entity.name.tooLong", "Entity/table names cannot exceed 64 bytes", "")
	}
	if strings.Trim(spec.Namespace, `\ `) == "" {
		v.add("entity.namespace.missing", "A PSR-4 namespace is required", "")
	} else if !namespacePattern.MatchString(spec.Namespace) {
		v.add("entity.namespace.invalid", "Enter a valid PHP namespace", "")
	}
	if spec.DefinitionKind != DefinitionEntity && spec.Translation != nil && spec.Translation.Enabled {
		v.add("entity.translation.owner.unsupported", "Only entity definitions can own translation bundles", "")
	}
	validateDefinitionBehavior(v, spec.DefinitionBehavior, spec.DefinitionKind, false)
	validateDefinitionMetadata(v, spec.DefinitionMetadata, spec.DefinitionKind, false)
	if spec.InheritanceAware && spec.DefinitionKind != DefinitionEntity {
		v.add("entity.inheritance.owner.unsupported", "Only entity definitions can be inheritance-aware", "")
	}
	protectionsConfigured := spec.ReadProtected || spec.WriteProtected || len(spec.PreservedProtections) != 0 || strings.TrimSpace(spec.ProtectionMethodRaw) != ""
	if protectionsConfigured && spec.DefinitionKind != DefinitionEntity && spec.DefinitionKind != DefinitionExtension {
		v.add("entity.protection.owner.unsupported", "Only entity definitions and entity extensions can declare entity protections", "")
	}
	if strings.TrimSpace(spec.ProtectionMethodRaw) != "" && (spec.ReadProtected || spec.WriteProtected || len(spec.PreservedProtections) != 0) {
		v.add("entity.protection.raw.conflict", "A preserved custom protection method cannot be combined with editable protections", "")
	}
	v.validateWriteProtectedScopes(spec.ReadProtected, spec.ReadProtectionScopes, "entity.protection.read.invalid", "")
	v.validateWriteProtectedScopes(spec.WriteProtected, spec.WriteProtectionScopes, "entity.protection.write.invalid", "")
	if spec.DefinitionKind == DefinitionExtension {
		if spec.ExtendedDefinitionClass == "" || !namespacePattern.MatchString(spec.ExtendedDefinitionClass) {
			v.add("entity.extension.target.missing", "Select an indexed entity definition to extend", "")
		}
	}
	if spec.Translation != nil && spec.Translation.Enabled {
		translation := spec.Translation
		validateDefinitionBehavior(v, translation.DefinitionBehavior, DefinitionEntity, true)
		validateTranslationBehaviorFields(v, translation.DefinitionBehavior)
		validateDefinitionMetadata(v, translation.DefinitionMetadata, DefinitionEntity, true)
		if !entityNamePattern.MatchString(translation.EntityName) || len(translation.EntityName) > 64 {
			v.add("entity.translation.name.invalid", "Translation entity name must use lowercase snake_case and fit within 64 bytes", "")
		}
		if !namespacePattern.MatchString(translation.DefinitionClass) ||
			!namespacePattern.MatchString(translation.EntityClass) ||
			!namespacePattern.MatchString(translation.CollectionClass) {
			v.add("entity.translation.identity.invalid", "Translation definition, entity, and collection classes must be valid fully-qualified PHP classes", "")
		}
		if !storagePattern.MatchString(translation.ParentStorageName) || len(translation.ParentStorageName) > 64 {
			v.add("entity.translation.parentStorage.invalid", "Translation parent storage name must use lowercase snake_case and fit within 64 bytes", "")
		} else if translation.ParentStorageName != spec.EntityName+"_id" {
			v.add("entity.translation.parentStorage.mismatch", "Translation parent storage must match the DAL-generated <entity_name>_id field", "")
		}
		if !storagePattern.MatchString(translation.AssociationLocalField) {
			v.add("entity.translation.localField.invalid", "Translation association local field must use lowercase snake_case", "")
		}
		if !propertyPattern.MatchString(translation.ParentPropertyName) || !propertyPattern.MatchString(translation.AssociationProperty) {
			v.add("entity.translation.property.invalid", "Translation parent and association properties must be valid PHP property names", "")
		}
		if strings.Trim(translation.ParentDefinitionClass, `\`) != strings.Trim(spec.DefinitionClass, `\`) {
			v.add("entity.translation.parentDefinition.mismatch", "Translation definition must reference the owning parent definition", "")
		}
		if translation.AssociationInheritedFK != "" &&
			(!translation.AssociationInherited || !storagePattern.MatchString(translation.AssociationInheritedFK)) {
			v.add("entity.inheritance.translationAssociationForeignKey.invalid", "Translations-association inherited foreign-key overrides require a valid storage name", "")
		}
		if translation.ReverseInheritedProperty != "" && !propertyPattern.MatchString(translation.ReverseInheritedProperty) {
			v.add("entity.inheritance.translationAssociationReverse.invalid", "Translations-association reverse inheritance requires a valid target property", "")
		}
		v.validateWriteProtectedScopes(
			translation.AssociationWriteProtected,
			translation.AssociationWriteScopes,
			"entity.translation.association.writeProtection.invalid",
			"",
		)
		v.validateBehavior(FieldSpec{}, "translation association", translation.AssociationBehavior)
		v.validateMetadata(FieldSpec{}, "translation association", translation.AssociationMetadata)
		v.validateAPIAware(translation.AssociationAPIAware, translation.AssociationAPIAwareSources, "entity.translation.association.apiAware.invalid", "")
	}
	if spec.Mode == "edit" || !classNamePattern.MatchString(spec.ClassName) ||
		!namespacePattern.MatchString(spec.Namespace) {
		return
	}
	namespace := strings.Trim(spec.Namespace, `\`)
	definitionSuffix := "Definition"
	switch spec.DefinitionKind {
	case DefinitionExtension:
		definitionSuffix = "Extension"
	case DefinitionBulkExtension:
		definitionSuffix = "BulkEntityExtension"
	}
	identities := []identityRequirement{
		{spec.DefinitionClass, definitionSuffix, "entity.definition.identity", "Generated class must match the selected namespace and base class"},
	}
	if spec.DefinitionKind == DefinitionEntity {
		identities = append(identities,
			identityRequirement{spec.EntityClass, "Entity", "entity.class.identity", "Entity class must match the selected namespace and base class"},
			identityRequirement{spec.CollectionClass, "Collection", "entity.collection.identity", "Collection class must match the selected namespace and base class"},
		)
	}
	for _, identity := range identities {
		if identity.actual != "" && strings.Trim(identity.actual, `\`) !=
			namespace+`\`+spec.ClassName+identity.suffix {
			v.add(identity.code, identity.message, "")
		}
	}
}

func validateTranslationBehaviorFields(parent *specValidator, behavior *DefinitionBehaviorSpec) {
	if behavior == nil {
		return
	}
	spec := EntitySpec{Mode: parent.spec.Mode, DefinitionKind: DefinitionEntity, EntityName: parent.spec.Translation.EntityName}
	validator := newSpecValidator(spec)
	if behavior.OverrideDefaultFields {
		for _, field := range behavior.DefaultFields {
			validator.validateField(field)
		}
	}
	if behavior.OverrideBaseFields {
		for _, field := range behavior.BaseFields {
			validator.validateField(field)
		}
	}
	parent.issues = append(parent.issues, validator.issues...)
}

func validateBulkExtensionSpec(v *specValidator) {
	spec := v.spec
	if strings.TrimSpace(spec.CollectMethodRaw) != "" {
		if len(spec.BulkExtensions) != 0 || len(spec.Fields) != 0 || len(spec.Indexes) != 0 ||
			spec.ExtendedDefinitionClass != "" || len(spec.ExtendedFields) != 0 {
			v.add("entity.bulkExtension.collectRaw.conflict", "A preserved custom collect method cannot be combined with editable bulk targets, fields, or indexes", "")
		}
		return
	}
	if len(spec.BulkExtensions) == 0 {
		v.add("entity.bulkExtension.targets.empty", "BulkEntityExtension requires at least one indexed target", "")
	}
	if len(spec.Fields) != 0 || len(spec.Indexes) != 0 || spec.ExtendedDefinitionClass != "" || len(spec.ExtendedFields) != 0 {
		v.add("entity.bulkExtension.singleTarget.conflict", "BulkEntityExtension fields and indexes belong inside bulkExtensions targets", "")
	}
	seenIDs := make(map[string]struct{}, len(spec.BulkExtensions))
	seenEntities := make(map[string]struct{}, len(spec.BulkExtensions))
	seenDefinitions := make(map[string]struct{}, len(spec.BulkExtensions))
	for index, target := range spec.BulkExtensions {
		id := target.ID
		if id == "" {
			id = fmt.Sprintf("bulk-target-%d", index)
		}
		if _, duplicate := seenIDs[id]; duplicate {
			v.add("entity.bulkExtension.target.id.duplicate", fmt.Sprintf("Bulk target ID %q is duplicated", id), id)
		}
		seenIDs[id] = struct{}{}
		entityKey := strings.ToLower(target.EntityName)
		if _, duplicate := seenEntities[entityKey]; duplicate {
			v.add("entity.bulkExtension.target.entity.duplicate", fmt.Sprintf("Entity %q is extended more than once", target.EntityName), id)
		}
		seenEntities[entityKey] = struct{}{}
		definitionKey := strings.ToLower(strings.Trim(target.ExtendedDefinitionClass, `\ `))
		if definitionKey != "" {
			if _, duplicate := seenDefinitions[definitionKey]; duplicate {
				v.add("entity.bulkExtension.target.definition.duplicate", fmt.Sprintf("Definition %q is extended more than once", target.ExtendedDefinitionClass), id)
			}
			seenDefinitions[definitionKey] = struct{}{}
		}
		targetSpec := bulkTargetEntitySpec(spec, target)
		if targetSpec.ExtendedDefinitionClass == "" {
			targetSpec.ExtendedDefinitionClass = spec.DefinitionClass
		}
		for _, issue := range ValidateSpec(targetSpec) {
			if issue.FieldID == "" {
				issue.FieldID = id
			}
			issue.Message = fmt.Sprintf("Bulk target %s: %s", defaultString(target.EntityName, id), issue.Message)
			v.issues = append(v.issues, issue)
		}
	}
}

func (v *specValidator) validateField(field FieldSpec) {
	if field.Kind == FieldLocked {
		return
	}
	if !supportedFieldKind(field.Kind) {
		v.add("entity.field.kind.unsupported", fmt.Sprintf("Unsupported field kind %q", field.Kind), field.ID)
		return
	}
	v.validateImplementation(field)
	if field.Kind == FieldID {
		v.idCount++
	}
	if field.Kind == FieldAutoIncrement {
		v.autoIncrementCount++
	}
	if field.Kind == FieldHierarchy {
		v.hierarchyCount++
	}
	if field.Primary || field.Kind == FieldID || field.Kind == FieldVersion {
		v.primaryCount++
	}
	v.validateFieldSettings(field)
	v.validateProperty(field)
	if field.Translated {
		v.validateTranslatedField(field)
		return
	}
	if field.Kind == FieldHierarchy {
		v.validateHierarchy(field)
		return
	}
	if field.UsesExistingColumn && field.Kind != FieldOneToOne && field.Kind != FieldManyToOne {
		v.add("entity.relation.existingColumn.unsupported", "Only to-one associations can reuse an existing local column", field.ID)
	}
	v.validateReferenceVersion(field)

	switch {
	case isForeignKeyKind(field.Kind):
		if v.validateForeignKey(field) {
			return
		}
	case field.Kind == FieldOneToMany:
		v.validateOneToMany(field)
		return
	case field.Kind == FieldManyToMany:
		v.validateManyToMany(field)
		return
	}

	v.validateStorage(field)
}

func (v *specValidator) validateImplementation(field FieldSpec) {
	implementation := field.Implementation
	if implementation == nil {
		return
	}
	if implementation.Class == "" || !namespacePattern.MatchString(implementation.Class) {
		v.add("entity.field.implementation.class.invalid", "Specialized fields require a fully-qualified PHP class", field.ID)
	}
	if !SpecializedFieldSupported(implementation.Class, v.spec.ShopwareVersion) {
		v.add(
			"entity.field.implementation.version.unsupported",
			fmt.Sprintf("Specialized field %s is unavailable for Shopware %s", ShortClass(implementation.Class), v.spec.ShopwareVersion),
			field.ID,
		)
	}
	switch implementation.ConstructorMode {
	case constructorStorageProperty:
		if len(implementation.FixedArguments) != 0 {
			v.add("entity.field.implementation.arguments.invalid", "Storage/property constructors cannot use fixed arguments", field.ID)
		}
	case constructorFixed:
		if len(implementation.AdditionalArguments) != 0 {
			v.add("entity.field.implementation.arguments.invalid", "Fixed constructors cannot use storage/property arguments", field.ID)
		}
		if field.StorageName != implementation.FixedStorageName || field.PropertyName != implementation.FixedPropertyName {
			v.add("entity.field.implementation.fixedIdentity.invalid", "This specialized field constructor fixes its storage and property names", field.ID)
		}
	default:
		v.add("entity.field.implementation.constructor.invalid", "Unsupported specialized field constructor shape", field.ID)
	}
	if implementation.MaxLengthArgument && field.Kind != FieldString {
		v.add("entity.field.implementation.maxLength.invalid", "Only string-based specialized fields can expose a maximum-length argument", field.ID)
	}
	for _, expression := range append(append([]string(nil), implementation.AdditionalArguments...), implementation.FixedArguments...) {
		if !validInlinePHPExpression(expression) {
			v.add("entity.field.implementation.expression.invalid", "Specialized-field constructor arguments must be safe inline PHP expressions", field.ID)
			break
		}
	}
	if implementation.ConstructorMode == constructorStorageProperty && len(implementation.AdditionalArguments) < implementation.MinimumAdditionalArguments {
		v.add("entity.field.implementation.arguments.missing", fmt.Sprintf("This specialized field requires at least %d additional constructor argument(s)", implementation.MinimumAdditionalArguments), field.ID)
	}
	if implementation.ManageEntity {
		if !validEntityTypeExpression(implementation.EntityType) {
			v.add("entity.field.implementation.entityType.invalid", "Managed specialized fields require a builtin or fully-qualified entity value type", field.ID)
		}
	} else if v.spec.Mode != "edit" {
		v.add("entity.field.implementation.entity.unmanaged", "New specialized fields require a safe entity property type", field.ID)
	}
	if implementation.EntityTrait != "" && !namespacePattern.MatchString(implementation.EntityTrait) {
		v.add("entity.field.implementation.entityTrait.invalid", "Specialized entity traits require a fully-qualified PHP trait name", field.ID)
	}
}

func validEntityTypeExpression(value string) bool {
	parts := strings.Split(value, "|")
	if len(parts) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, `\ `)
		if part == "" || part == "null" || !isBuiltinEntityType(part) && !namespacePattern.MatchString(part) {
			return false
		}
		if _, duplicate := seen[strings.ToLower(part)]; duplicate {
			return false
		}
		seen[strings.ToLower(part)] = struct{}{}
	}
	return true
}

func isBuiltinEntityType(value string) bool {
	switch value {
	case "string", "int", "float", "bool", "array", "object", "mixed":
		return true
	default:
		return false
	}
}

func (v *specValidator) validateFieldSettings(field FieldSpec) {
	v.validateExtensionFieldSettings(field)
	v.validateInheritanceFieldSettings(field)
	v.validateStoredValueSettings(field)
	v.validateFieldAPIAndSearchSettings(field)
	v.validateFieldBehaviorAndMetadata(field)
	v.validateDeleteAndMigrationSettings(field)
}

func (v *specValidator) validateExtensionFieldSettings(field FieldSpec) {
	if v.spec.DefinitionKind == DefinitionExtension {
		switch field.Kind {
		case FieldID, FieldAutoIncrement, FieldCreatedAt, FieldUpdatedAt, FieldVersion, FieldHierarchy:
			v.add("entity.extension.field.unsupported", fmt.Sprintf("Field kind %q cannot be added by an EntityExtension", field.Kind), field.ID)
		case FieldForeignKey:
			if field.Behavior == nil || !field.Behavior.Runtime {
				v.add(
					"entity.extension.foreignKey.association.required",
					"A persisted EntityExtension foreign key must be added together with its association; use a to-one relation row",
					field.ID,
				)
			}
		default:
			if !validEntityExtensionField(field) {
				v.add(
					"entity.extension.field.runtime.required",
					"EntityExtension scalar fields must be Runtime; persisted columns require a to-one association with its paired foreign key",
					field.ID,
				)
			}
		}
		if field.Primary {
			v.add("entity.extension.primary.unsupported", "EntityExtension fields cannot change the target primary key", field.ID)
		}
	}
	if field.Primary && !field.Required {
		v.add("entity.field.primary.required", "Primary-key fields must also be required", field.ID)
	}
}

func (v *specValidator) validateInheritanceFieldSettings(field FieldSpec) {
	if v.spec.DefinitionKind == DefinitionMapping && (field.Inherited || field.AssociationInherited || field.TranslationInherited) {
		v.add("entity.inheritance.field.mapping", "Mapping-definition fields cannot be inherited", field.ID)
	}
	if v.spec.DefinitionKind == DefinitionEntity && !v.spec.InheritanceAware &&
		(field.Inherited || field.AssociationInherited || field.TranslationInherited) {
		v.add("entity.inheritance.field.owner", "Enable inheritance on the entity before marking fields as inherited", field.ID)
	}
	if field.InheritedForeignKey != "" && (!field.Inherited || !storagePattern.MatchString(field.InheritedForeignKey)) {
		v.add("entity.inheritance.foreignKey.invalid", "Inherited foreign-key overrides require an inherited field and a valid storage name", field.ID)
	}
	if field.AssociationInheritedFK != "" && (!field.AssociationInherited || !storagePattern.MatchString(field.AssociationInheritedFK)) {
		v.add("entity.inheritance.associationForeignKey.invalid", "Association inherited foreign-key overrides require an inherited association and a valid storage name", field.ID)
	}
	if field.TranslationInheritedFK != "" && (!field.TranslationInherited || !storagePattern.MatchString(field.TranslationInheritedFK)) {
		v.add("entity.inheritance.translationForeignKey.invalid", "Translated inherited foreign-key overrides require an inherited facade and a valid storage name", field.ID)
	}
	if field.TranslationInherited && !field.Translated {
		v.add("entity.inheritance.translation.unsupported", "Only translated fields can inherit through a TranslatedField facade", field.ID)
	}
	if (field.HierarchyChildrenInherited || field.HierarchyVersionInherited) && !v.spec.InheritanceAware {
		v.add("entity.inheritance.hierarchyField.owner", "Enable inheritance before marking hierarchy components as inherited", field.ID)
	}
	if field.HierarchyChildrenInheritedFK != "" &&
		(!field.HierarchyChildrenInherited || !storagePattern.MatchString(field.HierarchyChildrenInheritedFK)) {
		v.add("entity.inheritance.hierarchyChildrenForeignKey.invalid", "Children-association inherited foreign-key overrides require a valid storage name", field.ID)
	}
	if field.HierarchyVersionInheritedFK != "" &&
		(!field.HierarchyVersionInherited || !storagePattern.MatchString(field.HierarchyVersionInheritedFK)) {
		v.add("entity.inheritance.hierarchyVersionForeignKey.invalid", "Parent-version inherited foreign-key overrides require a valid storage name", field.ID)
	}
	if field.HierarchyVersionInherited && !field.HierarchyVersionAware {
		v.add("entity.inheritance.hierarchyVersion.unsupported", "Only version-aware hierarchies can inherit the parent version field", field.ID)
	}
	if field.HierarchyChildrenReverse != "" && !propertyPattern.MatchString(field.HierarchyChildrenReverse) {
		v.add("entity.inheritance.hierarchyChildrenReverse.invalid", "Children reverse inheritance requires a valid target property", field.ID)
	}
	if field.ReverseInheritedProperty != "" {
		if !isAssociationKind(field.Kind) || !propertyPattern.MatchString(field.ReverseInheritedProperty) {
			v.add("entity.inheritance.reverse.invalid", "Reverse inheritance requires an association and a valid target property", field.ID)
		}
	}
}

func (v *specValidator) validateStoredValueSettings(field FieldSpec) {
	if field.Kind == FieldString && (field.MaxLength < 0 || field.MaxLength > 16383) {
		v.add("entity.field.length.invalid", "String length must be between 1 and 16383 when specified", field.ID)
	}
	if field.Kind == FieldInt && field.Min != nil && field.Max != nil && *field.Min > *field.Max {
		v.add("entity.field.range.invalid", "Integer minimum cannot be greater than its maximum", field.ID)
	}
	if field.Kind == FieldEnum {
		if !namespacePattern.MatchString(field.EnumClass) {
			v.add("entity.field.enum.class.invalid", "Enum fields require a fully-qualified PHP enum class", field.ID)
		}
		if !propertyPattern.MatchString(field.EnumCase) {
			v.add("entity.field.enum.case.invalid", "Enum fields require a valid enum case name", field.ID)
		}
		if field.EnumBackingType != "string" && field.EnumBackingType != "int" {
			v.add("entity.field.enum.backing.invalid", "Enum fields require a string or int backing type", field.ID)
		}
		if !EnumFieldSupported(v.spec.ShopwareVersion) {
			v.add("entity.field.enum.version.unsupported", fmt.Sprintf("EnumField is unavailable for Shopware %s", v.spec.ShopwareVersion), field.ID)
		}
	} else if field.EnumClass != "" || field.EnumCase != "" || field.EnumBackingType != "" {
		v.add("entity.field.enum.arguments.unsupported", "Enum class, case, and backing type apply only to EnumField", field.ID)
	}
	if field.Kind != FieldJSON && (strings.TrimSpace(field.JSONPropertyMappingExpression) != "" || strings.TrimSpace(field.JSONDefaultExpression) != "") {
		v.add("entity.field.json.arguments.unsupported", "JSON property mappings and defaults apply only to JsonField", field.ID)
	}
	if strings.TrimSpace(field.JSONPropertyMappingExpression) != "" && !validInlinePHPExpression(field.JSONPropertyMappingExpression) {
		v.add("entity.field.json.propertyMapping.invalid", "JSON property mapping must be one safe inline PHP expression", field.ID)
	}
	if strings.TrimSpace(field.JSONDefaultExpression) != "" && !validInlinePHPExpression(field.JSONDefaultExpression) {
		v.add("entity.field.json.default.invalid", "JSON default must be one safe inline PHP expression", field.ID)
	}
}

func (v *specValidator) validateFieldAPIAndSearchSettings(field FieldSpec) {
	v.validateAPIAware(field.APIAware, field.APIAwareSources, "entity.field.apiAware.invalid", field.ID)
	v.validateAPIAware(field.AssociationAPIAware, field.AssociationAPIAwareSources, "entity.association.apiAware.invalid", field.ID)
	v.validateAPIAware(field.HierarchyChildrenAPIAware, field.HierarchyChildrenAPISources, "entity.hierarchy.children.apiAware.invalid", field.ID)
	v.validateAPIAware(field.HierarchyVersionAPIAware, field.HierarchyVersionAPISources, "entity.hierarchy.version.apiAware.invalid", field.ID)
	translationAPIAware := field.APIAware
	if field.TranslationAPIAware != nil {
		translationAPIAware = *field.TranslationAPIAware
	}
	v.validateAPIAware(translationAPIAware, field.TranslationAPIAwareSources, "entity.translation.apiAware.invalid", field.ID)
	if field.SearchRanking < 0 || math.IsNaN(field.SearchRanking) || math.IsInf(field.SearchRanking, 0) {
		v.add("entity.field.searchRanking.invalid", "Search ranking must be a finite non-negative number", field.ID)
	}
	if field.AssociationSearchRank < 0 || math.IsNaN(field.AssociationSearchRank) || math.IsInf(field.AssociationSearchRank, 0) {
		v.add("entity.association.searchRanking.invalid", "Association search ranking must be a finite non-negative number", field.ID)
	}
	if field.AssociationAutoload && field.Kind != FieldManyToOne && field.Kind != FieldOneToOne {
		v.add("entity.association.autoload.unsupported", "Only many-to-one and one-to-one associations can be autoloaded", field.ID)
	}
	if conditional := field.ConditionalAssociation; conditional != nil {
		if field.Kind != FieldManyToOne && field.Kind != FieldOneToOne ||
			conditional.AlternativeKind != FieldManyToOne && conditional.AlternativeKind != FieldOneToOne ||
			conditional.AlternativeKind == field.Kind {
			v.add("entity.association.conditional.kind.invalid", "A conditional association must alternate between many-to-one and one-to-one", field.ID)
		}
		if !validInlinePHPExpression(conditional.ConditionExpression) {
			v.add("entity.association.conditional.expression.invalid", "Conditional association checks must be one safe inline PHP expression", field.ID)
		}
	}
	v.validateWriteProtection(field)
	if field.HierarchyChildrenRank < 0 || math.IsNaN(field.HierarchyChildrenRank) || math.IsInf(field.HierarchyChildrenRank, 0) {
		v.add("entity.hierarchy.children.searchRanking.invalid", "Hierarchy children search ranking must be a finite non-negative number", field.ID)
	}
	if field.TranslationSearchRank != nil && (*field.TranslationSearchRank < 0 || math.IsNaN(*field.TranslationSearchRank) || math.IsInf(*field.TranslationSearchRank, 0)) {
		v.add("entity.translation.searchRanking.invalid", "Translated-field search ranking must be a finite non-negative number", field.ID)
	}
}

func (v *specValidator) validateFieldBehaviorAndMetadata(field FieldSpec) {
	v.validateBehavior(field, "stored", field.Behavior)
	v.validateBehavior(field, "association", field.AssociationBehavior)
	v.validateBehavior(field, "translation", field.TranslationBehavior)
	v.validateBehavior(field, "hierarchy children", field.HierarchyChildrenBehavior)
	v.validateBehavior(field, "hierarchy version", field.HierarchyVersionBehavior)
	v.validateMetadata(field, "stored", field.Metadata)
	v.validateMetadata(field, "association", field.AssociationMetadata)
	v.validateMetadata(field, "translation", field.TranslationMetadata)
	v.validateMetadata(field, "hierarchy children", field.HierarchyChildrenMetadata)
	v.validateMetadata(field, "hierarchy version", field.HierarchyVersionMetadata)
	if field.SearchRankingTokenize != nil && field.SearchRanking <= 0 {
		v.add("entity.field.searchRanking.tokenizeWithoutRanking", "Search tokenization requires a positive search ranking", field.ID)
	}
	if field.AssociationSearchTokenize != nil && field.AssociationSearchRank <= 0 {
		v.add("entity.association.searchRanking.tokenizeWithoutRanking", "Association search tokenization requires a positive search ranking", field.ID)
	}
	if field.HierarchyChildrenTokenize != nil && field.HierarchyChildrenRank <= 0 {
		v.add("entity.hierarchy.children.searchRanking.tokenizeWithoutRanking", "Hierarchy search tokenization requires a positive search ranking", field.ID)
	}
	if field.TranslationSearchTokenize != nil && (field.TranslationSearchRank == nil || *field.TranslationSearchRank <= 0) {
		v.add("entity.translation.searchRanking.tokenizeWithoutRanking", "Translated-field search tokenization requires a positive search ranking", field.ID)
	}
}

func (v *specValidator) validateDeleteAndMigrationSettings(field FieldSpec) {
	if field.DeleteBehavior != "" && field.DeleteBehavior != DeleteRestrict &&
		field.DeleteBehavior != DeleteCascade && field.DeleteBehavior != DeleteSetNull {
		v.add("entity.relation.delete.invalid", "Unsupported delete behavior", field.ID)
	}
	if field.DeleteBehavior == DeleteSetNull && field.Required {
		v.add("entity.relation.delete.required", "SET NULL cannot be used by a required foreign key", field.ID)
	}
	if field.DeleteCloneRelevant != nil && field.DeleteBehavior != DeleteCascade {
		v.add("entity.relation.delete.cloneRelevant.invalid", "Clone relevance applies only to cascade delete", field.ID)
	}
	if field.DeleteEnforcedByConstraint != nil && field.DeleteBehavior != DeleteSetNull {
		v.add("entity.relation.delete.constraint.invalid", "Constraint enforcement applies only to set-null delete", field.ID)
	}
	if field.MigrationDefault != "" && !validBackfillExpression(field.MigrationDefault) {
		v.add("entity.migration.backfill.invalid", "Backfill must be one SQL expression without comments or statement separators", field.ID)
	}
}

// EntityDefinition accepts only associations, runtime fields,
// ReferenceVersionField, and an FkField that has a matching association from
// the same EntityExtension. To-one rows are the designer's atomic
// representation of that FkField/association pair; a standalone foreign-key
// row must therefore be rejected.
func validEntityExtensionField(field FieldSpec) bool {
	if field.Behavior != nil && field.Behavior.Runtime {
		return true
	}
	switch field.Kind {
	case FieldReferenceVersion, FieldForeignKey, FieldManyToOne, FieldOneToOne, FieldOneToMany, FieldManyToMany:
		return true
	default:
		return false
	}
}

func (v *specValidator) validateBehavior(field FieldSpec, location string, behavior *FieldBehavior) {
	if behavior == nil {
		return
	}
	if !behavior.Runtime && (len(behavior.RuntimeDependencies) != 0 || strings.TrimSpace(behavior.RuntimeDependenciesExpression) != "") {
		v.add("entity.field.runtime.dependenciesWithoutRuntime", "Runtime dependencies require the Runtime flag", field.ID)
	}
	if len(behavior.RuntimeDependencies) != 0 && strings.TrimSpace(behavior.RuntimeDependenciesExpression) != "" {
		v.add("entity.field.runtime.dependencies.ambiguous", "Runtime dependencies must use either a literal list or an imported expression", field.ID)
	}
	if expression := strings.TrimSpace(behavior.RuntimeDependenciesExpression); expression != "" && !validInlinePHPExpression(expression) {
		v.add("entity.field.runtime.dependencies.invalid", "Runtime dependencies must be one safe inline PHP expression", field.ID)
	}
	for _, dependency := range behavior.RuntimeDependencies {
		if strings.TrimSpace(dependency) == "" {
			v.add("entity.field.runtime.dependency.empty", "Runtime dependencies cannot contain empty values", field.ID)
			break
		}
	}
	if !behavior.NoConstraint {
		return
	}
	storedForeignKey := location == "stored" && (field.Kind == FieldForeignKey || field.Kind == FieldManyToOne ||
		field.Kind == FieldOneToOne || field.Kind == FieldHierarchy) && !field.UsesExistingColumn
	if !storedForeignKey {
		v.add("entity.field.noConstraint.unsupported", "NoConstraint applies only to a stored foreign-key field", field.ID)
	}
}

func (v *specValidator) validateAPIAware(enabled bool, sources []string, code, fieldID string) {
	if len(sources) != 0 && !enabled {
		v.add(code, "API source restrictions require the ApiAware flag", fieldID)
		return
	}
	seen := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		normalized := strings.Trim(source, `\ `)
		if normalized == "" || !namespacePattern.MatchString(normalized) {
			v.add(code, "API source restrictions must be fully-qualified PHP classes", fieldID)
			return
		}
		key := strings.ToLower(normalized)
		if _, duplicate := seen[key]; duplicate {
			v.add(code, "API source restrictions cannot contain duplicates", fieldID)
			return
		}
		seen[key] = struct{}{}
	}
}

func (v *specValidator) validateMetadata(field FieldSpec, location string, metadata *FieldMetadata) {
	if metadata == nil {
		return
	}
	if metadata.Deprecated != nil && (strings.TrimSpace(metadata.Deprecated.DeprecatedSince) == "" || strings.TrimSpace(metadata.Deprecated.WillBeRemovedIn) == "") {
		v.add("entity.field.metadata.deprecated.invalid", "Deprecated metadata requires both the deprecation and removal versions", field.ID)
	}
	for _, area := range metadata.RuleAreas {
		if strings.TrimSpace(area) == "" {
			v.add("entity.field.metadata.ruleArea.invalid", "Rule areas cannot contain empty values", field.ID)
			break
		}
	}
	if metadata.Choice != nil {
		if len(metadata.Choice.Values) == 0 {
			v.add("entity.field.metadata.choice.empty", "Choice metadata requires at least one value", field.ID)
		}
		for _, value := range metadata.Choice.Values {
			if !validInlinePHPExpression(value) {
				v.add("entity.field.metadata.choice.invalid", "Choice values must be safe inline scalar or constant expressions", field.ID)
				break
			}
		}
	}
	if metadata.Extension && v.spec.DefinitionKind != DefinitionExtension {
		v.add("entity.field.metadata.extension.unsupported", "The Extension flag is valid only inside an EntityExtension", field.ID)
	}
	_ = location
}

func validInlinePHPExpression(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !strings.Contains(value, ";") && !strings.Contains(value, "//") &&
		!strings.Contains(value, "/*") && !strings.Contains(value, "*/") && !strings.ContainsRune(value, '\x00')
}

func (v *specValidator) validateWriteProtection(field FieldSpec) {
	stored := field.Kind != FieldOneToMany && field.Kind != FieldManyToMany &&
		((field.Kind != FieldOneToOne && field.Kind != FieldManyToOne) || !field.UsesExistingColumn)
	if field.WriteProtected && !stored {
		v.add("entity.field.writeProtection.unsupported", "This field has no stored DAL field to mark write-protected", field.ID)
	}
	if field.AssociationWriteProtected && !isAssociationKind(field.Kind) {
		v.add("entity.association.writeProtection.unsupported", "Only association fields can carry association write protection", field.ID)
	}
	if field.TranslationWriteProtected && !field.Translated {
		v.add("entity.translation.writeProtection.unsupported", "Only translated fields have a translated facade to mark write-protected", field.ID)
	}
	if (field.HierarchyChildrenProtected || field.HierarchyVersionProtected) && field.Kind != FieldHierarchy {
		v.add("entity.hierarchy.writeProtection.unsupported", "Hierarchy-component write protection requires a hierarchy field", field.ID)
	}
	v.validateWriteProtectedScopes(field.WriteProtected, field.WriteProtectedScopes, "entity.field.writeProtection.invalid", field.ID)
	v.validateWriteProtectedScopes(field.AssociationWriteProtected, field.AssociationWriteScopes, "entity.association.writeProtection.invalid", field.ID)
	v.validateWriteProtectedScopes(field.TranslationWriteProtected, field.TranslationWriteScopes, "entity.translation.writeProtection.invalid", field.ID)
	v.validateWriteProtectedScopes(field.HierarchyChildrenProtected, field.HierarchyChildrenWriteScopes, "entity.hierarchy.children.writeProtection.invalid", field.ID)
	v.validateWriteProtectedScopes(field.HierarchyVersionProtected, field.HierarchyVersionWriteScopes, "entity.hierarchy.version.writeProtection.invalid", field.ID)
}

func (v *specValidator) validateWriteProtectedScopes(enabled bool, scopes []string, code, fieldID string) {
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if !enabled || strings.TrimSpace(scope) == "" || strings.ContainsAny(scope, "\r\n\x00") {
			v.add(code, "Write-protection scopes require an enabled flag and non-empty literal values", fieldID)
			return
		}
		if _, duplicate := seen[scope]; duplicate {
			v.add(code, "Write-protection scopes must not contain duplicates", fieldID)
			return
		}
		seen[scope] = struct{}{}
	}
}

func (v *specValidator) validateTranslatedField(field FieldSpec) {
	if v.spec.DefinitionKind != DefinitionEntity {
		v.add("entity.translation.owner.unsupported", "Only entity-definition fields can be translated", field.ID)
		return
	}
	if v.spec.Translation == nil || !v.spec.Translation.Enabled {
		v.add("entity.translation.missing", "Translated fields require a translation bundle", field.ID)
	}
	if !isTranslatableKind(field.Kind) {
		v.add("entity.translation.kind.unsupported", fmt.Sprintf("Field kind %q cannot be translated", field.Kind), field.ID)
		return
	}
	if field.Primary {
		v.add("entity.translation.primary.unsupported", "Translated fields cannot be primary keys", field.ID)
	}
	if !storagePattern.MatchString(field.StorageName) {
		v.add("entity.field.storage.invalid", fmt.Sprintf("Invalid translated storage name %q", field.StorageName), field.ID)
		return
	}
	if len(field.StorageName) > 64 {
		v.add("entity.field.storage.tooLong", "Translated storage column names cannot exceed 64 bytes", field.ID)
		return
	}
	if previous, duplicate := v.translatedStorageNames[field.StorageName]; duplicate {
		v.add("entity.field.storage.duplicate", fmt.Sprintf("Translated storage column %q is already used by field %s", field.StorageName, previous), field.ID)
		return
	}
	v.translatedStorageNames[field.StorageName] = field.ID
}

func isTranslatableKind(kind FieldKind) bool {
	switch kind {
	case FieldBinaryID, FieldString, FieldEnum, FieldLongText, FieldInt, FieldFloat, FieldBool, FieldDate, FieldDateTime,
		FieldJSON, FieldList, FieldObject, FieldBlob:
		return true
	default:
		return false
	}
}

func (v *specValidator) validateProperty(field FieldSpec) {
	if !propertyPattern.MatchString(field.PropertyName) {
		v.add("entity.field.property.invalid", fmt.Sprintf("Invalid property name %q", field.PropertyName), field.ID)
		return
	}
	if previous, duplicate := v.properties[field.PropertyName]; duplicate {
		if importedCompatiblePropertyAlias(previous, field) {
			return
		}
		v.add("entity.field.property.duplicate", fmt.Sprintf("Property %q is already used by field %s", field.PropertyName, previous.ID), field.ID)
		return
	}
	v.properties[field.PropertyName] = field
}

func importedCompatiblePropertyAlias(left, right FieldSpec) bool {
	if strings.TrimSpace(left.Raw) == "" || strings.TrimSpace(right.Raw) == "" {
		return false
	}
	if left.Kind == right.Kind && left.StorageName == right.StorageName {
		return true
	}
	return isAssociationKind(left.Kind) && isAssociationKind(right.Kind) &&
		left.TargetDefinitionClass != "" && left.TargetDefinitionClass == right.TargetDefinitionClass
}

func importedCompatibleStorageAlias(left, right FieldSpec) bool {
	if strings.TrimSpace(left.Raw) == "" || strings.TrimSpace(right.Raw) == "" {
		return false
	}
	leftType, leftErr := SQLType(left)
	rightType, rightErr := SQLType(right)
	return leftErr == nil && rightErr == nil && leftType == rightType &&
		left.Required == right.Required && left.Primary == right.Primary
}

func (v *specValidator) validateReferenceVersion(field FieldSpec) {
	if field.Kind != FieldReferenceVersion {
		return
	}
	if field.TargetDefinitionClass == "" || field.TargetEntityName == "" {
		v.add("entity.referenceVersion.target.missing", "Select an indexed version-aware target entity", field.ID)
		return
	}
	v.referenceVersionTargets[field.TargetDefinitionClass] = append(v.referenceVersionTargets[field.TargetDefinitionClass], field)
}

// validateForeignKey returns whether storage validation must be skipped because
// the association intentionally reuses a local column.
func (v *specValidator) validateForeignKey(field FieldSpec) bool {
	if field.Required && field.TargetDefinitionClass != "" {
		v.requiredRelationTargets[field.TargetDefinitionClass] = field.ID
	}
	if !field.UsesExistingColumn && field.Kind != FieldForeignKey {
		v.validateForeignKeyProperty(field)
	}
	if field.TargetDefinitionClass == "" || field.TargetEntityName == "" ||
		(field.Kind != FieldForeignKey && field.TargetEntityClass == "") {
		v.add("entity.relation.target.missing", "Select an indexed target entity", field.ID)
	}
	if !storagePattern.MatchString(defaultString(field.ReferenceStorageName, "id")) {
		v.add("entity.relation.reference.invalid", "Enter a valid referenced storage column", field.ID)
	}
	if !field.UsesExistingColumn {
		return false
	}
	if !associationStoragePattern.MatchString(field.StorageName) {
		v.add("entity.field.storage.invalid", fmt.Sprintf("Invalid existing storage name %q", field.StorageName), field.ID)
	} else if strings.Contains(field.StorageName, ".") {
		// DAL accepts qualified local-field paths for association-only fields.
		// They are resolver expressions, not physical columns owned by this
		// definition, so they must not participate in local-column validation.
		return true
	} else {
		v.existingColumnReferences = append(v.existingColumnReferences, existingColumnReference{
			name: field.StorageName, fieldID: field.ID,
			allowMissing: field.Kind == FieldManyToOne && field.AssociationBehavior != nil && field.AssociationBehavior.Runtime,
		})
	}
	return true
}

func (v *specValidator) validateForeignKeyProperty(field FieldSpec) {
	if !propertyPattern.MatchString(field.ForeignKeyPropertyName) {
		v.add("entity.field.foreignKeyProperty.invalid", "To-one relations require a valid foreign-key property", field.ID)
		return
	}
	if previous, duplicate := v.properties[field.ForeignKeyPropertyName]; duplicate {
		v.add("entity.field.property.duplicate", fmt.Sprintf("Property %q is already used by field %s", field.ForeignKeyPropertyName, previous.ID), field.ID)
		return
	}
	foreignKeyField := field
	foreignKeyField.PropertyName = field.ForeignKeyPropertyName
	v.properties[field.ForeignKeyPropertyName] = foreignKeyField
}

func (v *specValidator) validateHierarchy(field FieldSpec) {
	if v.spec.DefinitionKind != DefinitionEntity {
		v.add("entity.hierarchy.owner.unsupported", "Only entity definitions can own a hierarchy", field.ID)
	}
	if field.TargetDefinitionClass != v.spec.DefinitionClass ||
		field.TargetEntityClass != v.spec.EntityClass ||
		field.TargetCollectionClass != v.spec.CollectionClass ||
		field.TargetEntityName != v.spec.EntityName {
		v.add("entity.hierarchy.target.invalid", "Hierarchy fields must target their owning entity definition", field.ID)
	}
	if field.StorageName != "parent_id" || field.ForeignKeyPropertyName != "parentId" ||
		field.HierarchyParentProperty != "parent" {
		v.add("entity.hierarchy.identity.invalid", "Hierarchy parent fields must use parent_id, parentId, and parent", field.ID)
	}
	if field.DeleteBehavior != DeleteCascade {
		v.add("entity.hierarchy.delete.invalid", "Hierarchy children require cascading parent deletion", field.ID)
	}
	v.validateForeignKeyProperty(field)
	v.validateProperty(FieldSpec{ID: field.ID, PropertyName: field.HierarchyParentProperty})
	if field.TargetCollectionClass == "" {
		v.add("entity.hierarchy.collection.missing", "Hierarchy requires the owning entity collection", field.ID)
	}
	v.validateStorage(field)
}

func (v *specValidator) validateOneToMany(field FieldSpec) {
	if field.TargetDefinitionClass == "" || field.TargetCollectionClass == "" || field.ReferenceStorageName == "" {
		v.add("entity.relation.target.missing", "Select a target definition, collection, and foreign-key column", field.ID)
	}
	if field.ReferenceStorageName != "" && !storagePattern.MatchString(field.ReferenceStorageName) {
		v.add("entity.relation.reference.invalid", "Enter a valid target foreign-key column", field.ID)
	}
	source := defaultString(field.SourceColumn, "id")
	if !storagePattern.MatchString(source) {
		v.add("entity.relation.source.invalid", "Enter a valid local source column", field.ID)
	} else {
		v.existingColumnReferences = append(v.existingColumnReferences, existingColumnReference{
			name: source, fieldID: field.ID,
		})
	}
}

func (v *specValidator) validateManyToMany(field FieldSpec) {
	if field.TargetDefinitionClass == "" || field.TargetCollectionClass == "" {
		v.add("entity.relation.target.missing", "Select an indexed target entity", field.ID)
	}
	if field.MappingDefinitionClass == "" || !namespacePattern.MatchString(field.MappingDefinitionClass) {
		v.add("entity.relation.mapping.missing", "Select a valid mapping entity definition", field.ID)
	}
	columns := []struct{ name, value string }{
		{name: "mapping local column", value: field.MappingLocalColumn},
		{name: "mapping reference column", value: field.MappingReferenceColumn},
		{name: "source column", value: field.SourceColumn},
		{name: "reference field", value: field.ReferenceField},
	}
	for _, column := range columns {
		if !storagePattern.MatchString(column.value) {
			v.add("entity.relation.mappingColumn.invalid", fmt.Sprintf("Enter a valid %s", column.name), field.ID)
		}
	}
	source := defaultString(field.SourceColumn, "id")
	if storagePattern.MatchString(source) {
		v.existingColumnReferences = append(v.existingColumnReferences, existingColumnReference{
			name: source, fieldID: field.ID,
		})
	}
}

func (v *specValidator) validateStorage(field FieldSpec) {
	if field.Behavior != nil && field.Behavior.Runtime {
		if !propertyPattern.MatchString(field.StorageName) {
			v.add("entity.field.storage.invalid", fmt.Sprintf("Invalid runtime field name %q", field.StorageName), field.ID)
		}
		return
	}
	if !storagePattern.MatchString(field.StorageName) {
		v.add("entity.field.storage.invalid", fmt.Sprintf("Invalid storage name %q", field.StorageName), field.ID)
		return
	}
	if len(field.StorageName) > 64 {
		v.add("entity.field.storage.tooLong", "Storage column names cannot exceed 64 bytes", field.ID)
		return
	}
	if target, duplicate := v.extendedStorageNames[field.StorageName]; duplicate {
		v.add("entity.extension.column.duplicate", fmt.Sprintf("Extension field %q conflicts with existing target field %s", field.StorageName, target.PropertyName), field.ID)
		return
	}
	if previous, duplicate := v.storageNames[field.StorageName]; duplicate {
		if importedCompatibleStorageAlias(previous, field) {
			return
		}
		v.add("entity.field.storage.duplicate", fmt.Sprintf("Storage column %q is already used by field %s", field.StorageName, previous.ID), field.ID)
		return
	}
	v.storageNames[field.StorageName] = field
}

func (v *specValidator) validateFieldSet() {
	if v.spec.DefinitionKind == DefinitionEntity && v.idCount != 1 {
		v.add("entity.id.required", "Exactly one primary ID field is required", "")
	}
	if v.spec.DefinitionKind == DefinitionMapping && v.primaryCount == 0 {
		v.add("entity.mapping.primary.required", "Mapping definitions require at least one primary-key field", "")
	}
	if v.autoIncrementCount > 1 {
		v.add("entity.autoIncrement.duplicate", "At most one auto-increment field is allowed", "")
	}
	if v.hierarchyCount > 1 {
		v.add("entity.hierarchy.duplicate", "At most one hierarchy field is allowed", "")
	}
	if v.spec.InheritanceAware && v.hierarchyCount != 1 {
		v.add("entity.inheritance.hierarchy.required", "Inheritance-aware definitions require exactly one parent/children hierarchy", "")
	}
	for target, relationID := range v.requiredRelationTargets {
		if versions, found := v.referenceVersionTargets[target]; found {
			for _, version := range versions {
				if !version.Required {
					v.add("entity.referenceVersion.required", fmt.Sprintf("Reference-version field must be required because relation field %s is required", relationID), version.ID)
				}
			}
		}
	}
	for _, reference := range v.existingColumnReferences {
		if v.spec.DefinitionKind == DefinitionExtension {
			if len(v.extendedStorageNames) == 0 {
				continue
			}
			if _, local := v.storageNames[reference.name]; local {
				continue
			}
			if _, target := v.extendedStorageNames[reference.name]; target {
				continue
			}
			if !reference.allowMissing {
				v.add("entity.relation.existingColumn.missing", fmt.Sprintf("Association references unknown local or target column %q", reference.name), reference.fieldID)
			}
			continue
		}
		if _, found := v.storageNames[reference.name]; !found && !reference.allowMissing {
			v.add("entity.relation.existingColumn.missing", fmt.Sprintf("Association references unknown local column %q", reference.name), reference.fieldID)
		}
	}
}

func isAssociationKind(kind FieldKind) bool {
	return kind == FieldManyToOne || kind == FieldOneToOne ||
		kind == FieldOneToMany || kind == FieldManyToMany || kind == FieldHierarchy
}

func (v *specValidator) validateIndexes() {
	indexNames := make(map[string]struct{})
	for _, index := range v.spec.Indexes {
		v.validateIndex(index, indexNames)
	}
}

func (v *specValidator) validateIndex(index IndexSpec, indexNames map[string]struct{}) {
	if !indexNamePattern.MatchString(index.Name) {
		v.add("entity.index.name.invalid", fmt.Sprintf("Invalid index name %q", index.Name), "")
	} else if len(index.Name) > 64 {
		v.add("entity.index.name.tooLong", fmt.Sprintf("Index name %q exceeds 64 bytes", index.Name), "")
	}
	scope := "parent"
	if index.Translation {
		scope = "translation"
	}
	indexKey := scope + "\x00" + index.Name
	if _, duplicate := indexNames[indexKey]; duplicate {
		v.add("entity.index.name.duplicate", fmt.Sprintf("Index %q is duplicated", index.Name), "")
	}
	indexNames[indexKey] = struct{}{}
	if index.Translation && (v.spec.Translation == nil || !v.spec.Translation.Enabled) {
		v.add("entity.index.translation.missing", fmt.Sprintf("Index %q targets a disabled translation table", index.Name), "")
	}
	if index.Kind != IndexNormal && index.Kind != IndexUnique {
		v.add("entity.index.kind.invalid", fmt.Sprintf("Unsupported index kind %q", index.Kind), "")
	}
	if len(index.Columns) == 0 {
		v.add("entity.index.columns.empty", fmt.Sprintf("Index %q has no columns", index.Name), "")
	}
	seenColumns := make(map[string]struct{})
	columns := v.storageNames
	if v.spec.DefinitionKind == DefinitionExtension {
		columns = make(map[string]FieldSpec, len(v.storageNames)+len(v.extendedStorageNames))
		for name, field := range v.storageNames {
			columns[name] = field
		}
		for name := range v.extendedStorageNames {
			columns[name] = FieldSpec{}
		}
	}
	if index.Translation {
		columns = make(map[string]FieldSpec, len(v.translatedStorageNames)+5)
		for name := range v.translatedStorageNames {
			columns[name] = FieldSpec{}
		}
		if v.spec.Translation != nil {
			columns[v.spec.Translation.ParentStorageName] = FieldSpec{}
			if hasVersionField(v.spec) {
				columns[strings.TrimSuffix(v.spec.Translation.ParentStorageName, "_id")+"_version_id"] = FieldSpec{}
			}
		}
		for _, name := range []string{"language_id", "created_at", "updated_at"} {
			columns[name] = FieldSpec{}
		}
	}
	for _, column := range index.Columns {
		if _, found := columns[column]; !found {
			if !index.Translation {
				if _, translated := v.translatedStorageNames[column]; translated {
					v.add("entity.index.column.translated", fmt.Sprintf("Index %q cannot reference translated column %q on the parent table", index.Name, column), "")
				} else {
					v.add("entity.index.column.unknown", fmt.Sprintf("Index %q references unknown column %q", index.Name, column), "")
				}
			} else {
				v.add("entity.index.column.unknown", fmt.Sprintf("Index %q references unknown column %q", index.Name, column), "")
			}
		}
		if _, duplicate := seenColumns[column]; duplicate {
			v.add("entity.index.column.duplicate", fmt.Sprintf("Index %q repeats column %q", index.Name, column), "")
		}
		seenColumns[column] = struct{}{}
	}
	if v.spec.DefinitionKind == DefinitionExtension && !index.Translation {
		owned := false
		for _, column := range index.Columns {
			if _, found := v.storageNames[column]; found {
				owned = true
				break
			}
		}
		if !owned {
			v.add("entity.extension.index.ownedColumn.required", fmt.Sprintf("Extension index %q must include at least one column contributed by this extension", index.Name), "")
		}
	}
}
