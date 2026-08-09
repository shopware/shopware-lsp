package entityschema

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

var (
	classNamePattern  = regexp.MustCompile(`^[A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*$`)
	entityNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	propertyPattern   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	storagePattern    = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	indexNamePattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	namespacePattern  = regexp.MustCompile(`^\\?[A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*(?:\\[A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*)*$`)
)

type ValidationIssue struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	FieldID  string `json:"fieldId,omitempty"`
	Severity string `json:"severity"`
}

type existingColumnReference struct {
	name    string
	fieldID string
}

type specValidator struct {
	spec EntitySpec

	issues                   []ValidationIssue
	properties               map[string]string
	storageNames             map[string]string
	existingColumnReferences []existingColumnReference
	referenceVersionTargets  map[string]FieldSpec
	requiredRelationTargets  map[string]string
	idCount                  int
	autoIncrementCount       int
}

func newSpecValidator(spec EntitySpec) *specValidator {
	return &specValidator{
		spec:                    spec,
		properties:              make(map[string]string),
		storageNames:            make(map[string]string),
		referenceVersionTargets: make(map[string]FieldSpec),
		requiredRelationTargets: make(map[string]string),
	}
}

func ValidateSpec(spec EntitySpec) []ValidationIssue {
	validator := newSpecValidator(spec)
	validator.validateIdentity()
	for _, field := range spec.Fields {
		validator.validateField(field)
	}
	validator.validateFieldSet()
	validator.validateIndexes()
	return validator.issues
}

func (v *specValidator) add(code, message, fieldID string) {
	v.issues = append(v.issues, ValidationIssue{
		Code: code, Message: message, FieldID: fieldID, Severity: "error",
	})
}

func (v *specValidator) validateIdentity() {
	spec := v.spec
	if !classNamePattern.MatchString(spec.ClassName) {
		v.add("entity.class.invalid", "Enter a valid PHP base class name", "")
	}
	if !entityNamePattern.MatchString(spec.EntityName) {
		v.add("entity.name.invalid", "Entity name must use lowercase snake_case", "")
	} else if len(spec.EntityName) > 64 {
		v.add("entity.name.tooLong", "Entity/table names cannot exceed 64 bytes", "")
	}
	if strings.Trim(spec.Namespace, `\ `) == "" {
		v.add("entity.namespace.missing", "A PSR-4 namespace is required", "")
	} else if !namespacePattern.MatchString(spec.Namespace) {
		v.add("entity.namespace.invalid", "Enter a valid PHP namespace", "")
	}
	if spec.Mode == "edit" || !classNamePattern.MatchString(spec.ClassName) ||
		!namespacePattern.MatchString(spec.Namespace) {
		return
	}
	namespace := strings.Trim(spec.Namespace, `\`)
	identities := []struct {
		actual  string
		suffix  string
		code    string
		message string
	}{
		{spec.DefinitionClass, "Definition", "entity.definition.identity", "Definition class must match the selected namespace and base class"},
		{spec.EntityClass, "Entity", "entity.class.identity", "Entity class must match the selected namespace and base class"},
		{spec.CollectionClass, "Collection", "entity.collection.identity", "Collection class must match the selected namespace and base class"},
	}
	for _, identity := range identities {
		if identity.actual != "" && strings.Trim(identity.actual, `\`) !=
			namespace+`\`+spec.ClassName+identity.suffix {
			v.add(identity.code, identity.message, "")
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
	if field.Kind == FieldID {
		v.idCount++
	}
	if field.Kind == FieldAutoIncrement {
		v.autoIncrementCount++
	}
	v.validateFieldSettings(field)
	v.validateProperty(field)
	if field.UsesExistingColumn && field.Kind != FieldOneToOne {
		v.add("entity.relation.existingColumn.unsupported", "Only one-to-one associations can reuse an existing local column", field.ID)
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
	if (isForeignKeyKind(field.Kind) || isJSONKind(field.Kind)) &&
		len(v.spec.EntityName)+len(field.StorageName)+6 > 64 {
		v.add("entity.databaseObject.name.tooLong", "Generated index, foreign-key, or JSON constraint name exceeds 64 bytes; shorten the entity or storage name", field.ID)
	}
}

func (v *specValidator) validateFieldSettings(field FieldSpec) {
	if field.Primary && !field.Required {
		v.add("entity.field.primary.required", "Primary-key fields must also be required", field.ID)
	}
	if field.Kind == FieldString && (field.MaxLength < 0 || field.MaxLength > 16383) {
		v.add("entity.field.length.invalid", "String length must be between 1 and 16383 when specified", field.ID)
	}
	if field.Kind == FieldInt && field.Min != nil && field.Max != nil && *field.Min > *field.Max {
		v.add("entity.field.range.invalid", "Integer minimum cannot be greater than its maximum", field.ID)
	}
	if field.SearchRanking < 0 || math.IsNaN(field.SearchRanking) || math.IsInf(field.SearchRanking, 0) {
		v.add("entity.field.searchRanking.invalid", "Search ranking must be a finite non-negative number", field.ID)
	}
	if field.AssociationSearchRank < 0 || math.IsNaN(field.AssociationSearchRank) || math.IsInf(field.AssociationSearchRank, 0) {
		v.add("entity.association.searchRanking.invalid", "Association search ranking must be a finite non-negative number", field.ID)
	}
	if field.DeleteBehavior != "" && field.DeleteBehavior != DeleteRestrict &&
		field.DeleteBehavior != DeleteCascade && field.DeleteBehavior != DeleteSetNull {
		v.add("entity.relation.delete.invalid", "Unsupported delete behavior", field.ID)
	}
	if field.DeleteBehavior == DeleteSetNull && field.Required {
		v.add("entity.relation.delete.required", "SET NULL cannot be used by a required foreign key", field.ID)
	}
	if field.MigrationDefault != "" && !validBackfillExpression(field.MigrationDefault) {
		v.add("entity.migration.backfill.invalid", "Backfill must be one SQL expression without comments or statement separators", field.ID)
	}
}

func (v *specValidator) validateProperty(field FieldSpec) {
	if !propertyPattern.MatchString(field.PropertyName) {
		v.add("entity.field.property.invalid", fmt.Sprintf("Invalid property name %q", field.PropertyName), field.ID)
		return
	}
	if previous, duplicate := v.properties[field.PropertyName]; duplicate {
		v.add("entity.field.property.duplicate", fmt.Sprintf("Property %q is already used by field %s", field.PropertyName, previous), field.ID)
		return
	}
	v.properties[field.PropertyName] = field.ID
}

func (v *specValidator) validateReferenceVersion(field FieldSpec) {
	if field.Kind != FieldReferenceVersion {
		return
	}
	if field.TargetDefinitionClass == "" || field.TargetEntityName == "" {
		v.add("entity.referenceVersion.target.missing", "Select an indexed version-aware target entity", field.ID)
		return
	}
	if previous, duplicate := v.referenceVersionTargets[field.TargetDefinitionClass]; duplicate {
		v.add("entity.referenceVersion.target.duplicate", fmt.Sprintf("A reference-version field for this target already exists as field %s", previous.ID), field.ID)
		return
	}
	v.referenceVersionTargets[field.TargetDefinitionClass] = field
}

// validateForeignKey returns whether storage validation must be skipped because
// the association intentionally reuses a local column.
func (v *specValidator) validateForeignKey(field FieldSpec) bool {
	if field.Required && field.TargetDefinitionClass != "" {
		v.requiredRelationTargets[field.TargetDefinitionClass] = field.ID
	}
	if !field.UsesExistingColumn {
		v.validateForeignKeyProperty(field)
	}
	if field.TargetDefinitionClass == "" || field.TargetEntityClass == "" || field.TargetEntityName == "" {
		v.add("entity.relation.target.missing", "Select an indexed target entity", field.ID)
	}
	if !storagePattern.MatchString(defaultString(field.ReferenceStorageName, "id")) {
		v.add("entity.relation.reference.invalid", "Enter a valid referenced storage column", field.ID)
	}
	if !field.UsesExistingColumn {
		return false
	}
	if !storagePattern.MatchString(field.StorageName) {
		v.add("entity.field.storage.invalid", fmt.Sprintf("Invalid existing storage name %q", field.StorageName), field.ID)
	} else {
		v.existingColumnReferences = append(v.existingColumnReferences, existingColumnReference{
			name: field.StorageName, fieldID: field.ID,
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
		v.add("entity.field.property.duplicate", fmt.Sprintf("Property %q is already used by field %s", field.ForeignKeyPropertyName, previous), field.ID)
		return
	}
	v.properties[field.ForeignKeyPropertyName] = field.ID
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
	if !storagePattern.MatchString(field.StorageName) {
		v.add("entity.field.storage.invalid", fmt.Sprintf("Invalid storage name %q", field.StorageName), field.ID)
		return
	}
	if len(field.StorageName) > 64 {
		v.add("entity.field.storage.tooLong", "Storage column names cannot exceed 64 bytes", field.ID)
		return
	}
	if previous, duplicate := v.storageNames[field.StorageName]; duplicate {
		v.add("entity.field.storage.duplicate", fmt.Sprintf("Storage column %q is already used by field %s", field.StorageName, previous), field.ID)
		return
	}
	v.storageNames[field.StorageName] = field.ID
}

func (v *specValidator) validateFieldSet() {
	if v.idCount != 1 {
		v.add("entity.id.required", "Exactly one primary ID field is required", "")
	}
	if v.autoIncrementCount > 1 {
		v.add("entity.autoIncrement.duplicate", "At most one auto-increment field is allowed", "")
	}
	for target, relationID := range v.requiredRelationTargets {
		if version, found := v.referenceVersionTargets[target]; found && !version.Required {
			v.add("entity.referenceVersion.required", fmt.Sprintf("Reference-version field must be required because relation field %s is required", relationID), version.ID)
		}
	}
	for _, reference := range v.existingColumnReferences {
		if _, found := v.storageNames[reference.name]; !found {
			v.add("entity.relation.existingColumn.missing", fmt.Sprintf("Association references unknown local column %q", reference.name), reference.fieldID)
		}
	}
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
	if _, duplicate := indexNames[index.Name]; duplicate {
		v.add("entity.index.name.duplicate", fmt.Sprintf("Index %q is duplicated", index.Name), "")
	}
	indexNames[index.Name] = struct{}{}
	if index.Kind != IndexNormal && index.Kind != IndexUnique {
		v.add("entity.index.kind.invalid", fmt.Sprintf("Unsupported index kind %q", index.Kind), "")
	}
	if len(index.Columns) == 0 {
		v.add("entity.index.columns.empty", fmt.Sprintf("Index %q has no columns", index.Name), "")
	}
	seenColumns := make(map[string]struct{})
	for _, column := range index.Columns {
		if _, found := v.storageNames[column]; !found {
			v.add("entity.index.column.unknown", fmt.Sprintf("Index %q references unknown column %q", index.Name, column), "")
		}
		if _, duplicate := seenColumns[column]; duplicate {
			v.add("entity.index.column.duplicate", fmt.Sprintf("Index %q repeats column %q", index.Name, column), "")
		}
		seenColumns[column] = struct{}{}
	}
}
