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

func ValidateSpec(spec EntitySpec) []ValidationIssue {
	var issues []ValidationIssue
	add := func(code, message, fieldID string) {
		issues = append(issues, ValidationIssue{
			Code: code, Message: message, FieldID: fieldID, Severity: "error",
		})
	}
	if !classNamePattern.MatchString(spec.ClassName) {
		add("entity.class.invalid", "Enter a valid PHP base class name", "")
	}
	if !entityNamePattern.MatchString(spec.EntityName) {
		add("entity.name.invalid", "Entity name must use lowercase snake_case", "")
	} else if len(spec.EntityName) > 64 {
		add("entity.name.tooLong", "Entity/table names cannot exceed 64 bytes", "")
	}
	if strings.Trim(spec.Namespace, `\ `) == "" {
		add("entity.namespace.missing", "A PSR-4 namespace is required", "")
	} else if !namespacePattern.MatchString(spec.Namespace) {
		add("entity.namespace.invalid", "Enter a valid PHP namespace", "")
	}
	if spec.Mode != "edit" && classNamePattern.MatchString(spec.ClassName) && namespacePattern.MatchString(spec.Namespace) {
		namespace := strings.Trim(spec.Namespace, `\`)
		if spec.DefinitionClass != "" && strings.Trim(spec.DefinitionClass, `\`) != namespace+`\`+spec.ClassName+"Definition" {
			add("entity.definition.identity", "Definition class must match the selected namespace and base class", "")
		}
		if spec.EntityClass != "" && strings.Trim(spec.EntityClass, `\`) != namespace+`\`+spec.ClassName+"Entity" {
			add("entity.class.identity", "Entity class must match the selected namespace and base class", "")
		}
		if spec.CollectionClass != "" && strings.Trim(spec.CollectionClass, `\`) != namespace+`\`+spec.ClassName+"Collection" {
			add("entity.collection.identity", "Collection class must match the selected namespace and base class", "")
		}
	}
	properties := make(map[string]string)
	storageNames := make(map[string]string)
	type existingColumnReference struct{ name, fieldID string }
	var existingColumnReferences []existingColumnReference
	idCount := 0
	autoIncrementCount := 0
	referenceVersionTargets := make(map[string]FieldSpec)
	requiredRelationTargets := make(map[string]string)
	for _, field := range spec.Fields {
		if field.Kind == FieldLocked {
			continue
		}
		if !supportedFieldKind(field.Kind) {
			add("entity.field.kind.unsupported", fmt.Sprintf("Unsupported field kind %q", field.Kind), field.ID)
			continue
		}
		if field.Kind == FieldID {
			idCount++
		}
		if field.Kind == FieldAutoIncrement {
			autoIncrementCount++
		}
		if field.Primary && !field.Required {
			add("entity.field.primary.required", "Primary-key fields must also be required", field.ID)
		}
		if field.Kind == FieldString && (field.MaxLength < 0 || field.MaxLength > 16383) {
			add("entity.field.length.invalid", "String length must be between 1 and 16383 when specified", field.ID)
		}
		if field.Kind == FieldInt && field.Min != nil && field.Max != nil && *field.Min > *field.Max {
			add("entity.field.range.invalid", "Integer minimum cannot be greater than its maximum", field.ID)
		}
		if field.SearchRanking < 0 || math.IsNaN(field.SearchRanking) || math.IsInf(field.SearchRanking, 0) {
			add("entity.field.searchRanking.invalid", "Search ranking must be a finite non-negative number", field.ID)
		}
		if field.AssociationSearchRank < 0 || math.IsNaN(field.AssociationSearchRank) || math.IsInf(field.AssociationSearchRank, 0) {
			add("entity.association.searchRanking.invalid", "Association search ranking must be a finite non-negative number", field.ID)
		}
		if field.DeleteBehavior != "" && field.DeleteBehavior != DeleteRestrict && field.DeleteBehavior != DeleteCascade && field.DeleteBehavior != DeleteSetNull {
			add("entity.relation.delete.invalid", "Unsupported delete behavior", field.ID)
		}
		if field.DeleteBehavior == DeleteSetNull && field.Required {
			add("entity.relation.delete.required", "SET NULL cannot be used by a required foreign key", field.ID)
		}
		if field.MigrationDefault != "" && !validBackfillExpression(field.MigrationDefault) {
			add("entity.migration.backfill.invalid", "Backfill must be one SQL expression without comments or statement separators", field.ID)
		}
		if !propertyPattern.MatchString(field.PropertyName) {
			add("entity.field.property.invalid", fmt.Sprintf("Invalid property name %q", field.PropertyName), field.ID)
		} else if previous, duplicate := properties[field.PropertyName]; duplicate {
			add("entity.field.property.duplicate", fmt.Sprintf("Property %q is already used by field %s", field.PropertyName, previous), field.ID)
		} else {
			properties[field.PropertyName] = field.ID
		}
		if field.UsesExistingColumn && field.Kind != FieldOneToOne {
			add("entity.relation.existingColumn.unsupported", "Only one-to-one associations can reuse an existing local column", field.ID)
		}
		if field.Kind == FieldReferenceVersion {
			if field.TargetDefinitionClass == "" || field.TargetEntityName == "" {
				add("entity.referenceVersion.target.missing", "Select an indexed version-aware target entity", field.ID)
			} else if previous, duplicate := referenceVersionTargets[field.TargetDefinitionClass]; duplicate {
				add("entity.referenceVersion.target.duplicate", fmt.Sprintf("A reference-version field for this target already exists as field %s", previous.ID), field.ID)
			} else {
				referenceVersionTargets[field.TargetDefinitionClass] = field
			}
		}
		if isForeignKeyKind(field.Kind) {
			if field.Required && field.TargetDefinitionClass != "" {
				requiredRelationTargets[field.TargetDefinitionClass] = field.ID
			}
			if !field.UsesExistingColumn && !propertyPattern.MatchString(field.ForeignKeyPropertyName) {
				add("entity.field.foreignKeyProperty.invalid", "To-one relations require a valid foreign-key property", field.ID)
			} else if !field.UsesExistingColumn {
				if previous, duplicate := properties[field.ForeignKeyPropertyName]; duplicate {
					add("entity.field.property.duplicate", fmt.Sprintf("Property %q is already used by field %s", field.ForeignKeyPropertyName, previous), field.ID)
				} else {
					properties[field.ForeignKeyPropertyName] = field.ID
				}
			}
			if field.TargetDefinitionClass == "" || field.TargetEntityClass == "" || field.TargetEntityName == "" {
				add("entity.relation.target.missing", "Select an indexed target entity", field.ID)
			}
			if !storagePattern.MatchString(defaultString(field.ReferenceStorageName, "id")) {
				add("entity.relation.reference.invalid", "Enter a valid referenced storage column", field.ID)
			}
			if field.UsesExistingColumn {
				if !storagePattern.MatchString(field.StorageName) {
					add("entity.field.storage.invalid", fmt.Sprintf("Invalid existing storage name %q", field.StorageName), field.ID)
				} else {
					existingColumnReferences = append(existingColumnReferences, existingColumnReference{name: field.StorageName, fieldID: field.ID})
				}
				continue
			}
		}
		if field.Kind == FieldOneToMany {
			if field.TargetDefinitionClass == "" || field.TargetCollectionClass == "" || field.ReferenceStorageName == "" {
				add("entity.relation.target.missing", "Select a target definition, collection, and foreign-key column", field.ID)
			}
			if field.ReferenceStorageName != "" && !storagePattern.MatchString(field.ReferenceStorageName) {
				add("entity.relation.reference.invalid", "Enter a valid target foreign-key column", field.ID)
			}
			if !storagePattern.MatchString(defaultString(field.SourceColumn, "id")) {
				add("entity.relation.source.invalid", "Enter a valid local source column", field.ID)
			} else {
				existingColumnReferences = append(existingColumnReferences, existingColumnReference{name: defaultString(field.SourceColumn, "id"), fieldID: field.ID})
			}
			continue
		}
		if field.Kind == FieldManyToMany {
			if field.TargetDefinitionClass == "" || field.TargetCollectionClass == "" {
				add("entity.relation.target.missing", "Select an indexed target entity", field.ID)
			}
			if field.MappingDefinitionClass == "" || !namespacePattern.MatchString(field.MappingDefinitionClass) {
				add("entity.relation.mapping.missing", "Select a valid mapping entity definition", field.ID)
			}
			mappingColumns := []struct{ name, value string }{
				{name: "mapping local column", value: field.MappingLocalColumn},
				{name: "mapping reference column", value: field.MappingReferenceColumn},
				{name: "source column", value: field.SourceColumn},
				{name: "reference field", value: field.ReferenceField},
			}
			for _, mappingColumn := range mappingColumns {
				name, value := mappingColumn.name, mappingColumn.value
				if !storagePattern.MatchString(value) {
					add("entity.relation.mappingColumn.invalid", fmt.Sprintf("Enter a valid %s", name), field.ID)
				}
			}
			if storagePattern.MatchString(defaultString(field.SourceColumn, "id")) {
				existingColumnReferences = append(existingColumnReferences, existingColumnReference{name: defaultString(field.SourceColumn, "id"), fieldID: field.ID})
			}
			continue
		}
		if !storagePattern.MatchString(field.StorageName) {
			add("entity.field.storage.invalid", fmt.Sprintf("Invalid storage name %q", field.StorageName), field.ID)
		} else if len(field.StorageName) > 64 {
			add("entity.field.storage.tooLong", "Storage column names cannot exceed 64 bytes", field.ID)
		} else if previous, duplicate := storageNames[field.StorageName]; duplicate {
			add("entity.field.storage.duplicate", fmt.Sprintf("Storage column %q is already used by field %s", field.StorageName, previous), field.ID)
		} else {
			storageNames[field.StorageName] = field.ID
		}
		if (isForeignKeyKind(field.Kind) || isJSONKind(field.Kind)) && len(spec.EntityName)+len(field.StorageName)+6 > 64 {
			add("entity.databaseObject.name.tooLong", "Generated index, foreign-key, or JSON constraint name exceeds 64 bytes; shorten the entity or storage name", field.ID)
		}
	}
	if idCount != 1 {
		add("entity.id.required", "Exactly one primary ID field is required", "")
	}
	if autoIncrementCount > 1 {
		add("entity.autoIncrement.duplicate", "At most one auto-increment field is allowed", "")
	}
	for target, relationID := range requiredRelationTargets {
		if version, found := referenceVersionTargets[target]; found && !version.Required {
			add("entity.referenceVersion.required", fmt.Sprintf("Reference-version field must be required because relation field %s is required", relationID), version.ID)
		}
	}
	for _, reference := range existingColumnReferences {
		if _, found := storageNames[reference.name]; !found {
			add("entity.relation.existingColumn.missing", fmt.Sprintf("Association references unknown local column %q", reference.name), reference.fieldID)
		}
	}
	indexNames := make(map[string]struct{})
	for _, index := range spec.Indexes {
		if !indexNamePattern.MatchString(index.Name) {
			add("entity.index.name.invalid", fmt.Sprintf("Invalid index name %q", index.Name), "")
		} else if len(index.Name) > 64 {
			add("entity.index.name.tooLong", fmt.Sprintf("Index name %q exceeds 64 bytes", index.Name), "")
		}
		if _, duplicate := indexNames[index.Name]; duplicate {
			add("entity.index.name.duplicate", fmt.Sprintf("Index %q is duplicated", index.Name), "")
		}
		indexNames[index.Name] = struct{}{}
		if index.Kind != IndexNormal && index.Kind != IndexUnique {
			add("entity.index.kind.invalid", fmt.Sprintf("Unsupported index kind %q", index.Kind), "")
		}
		if len(index.Columns) == 0 {
			add("entity.index.columns.empty", fmt.Sprintf("Index %q has no columns", index.Name), "")
		}
		seenColumns := make(map[string]struct{})
		for _, column := range index.Columns {
			if _, found := storageNames[column]; !found {
				add("entity.index.column.unknown", fmt.Sprintf("Index %q references unknown column %q", index.Name, column), "")
			}
			if _, duplicate := seenColumns[column]; duplicate {
				add("entity.index.column.duplicate", fmt.Sprintf("Index %q repeats column %q", index.Name, column), "")
			}
			seenColumns[column] = struct{}{}
		}
	}
	return issues
}

func ValidateMigration(previous, next Schema, diff SchemaDiff, decisions []Decision) []ValidationIssue {
	decisionByTarget := make(map[string]Decision)
	for _, decision := range decisions {
		decisionByTarget[decision.Entity+"\x00"+decision.To] = decision
	}
	var issues []ValidationIssue
	missing := func(entity string, column Column) {
		issues = append(issues, ValidationIssue{
			Code:     "entity.migration.backfill.required",
			Message:  fmt.Sprintf("Adding NOT NULL column %s.%s to existing rows requires a backfill SQL expression", entity, column.Name),
			Severity: "error",
		})
	}
	for _, change := range diff.AddedColumns {
		if change.After == nil || !change.After.NotNull || change.After.AutoIncrement || strings.TrimSpace(change.After.BackfillSQL) != "" {
			continue
		}
		if decision := decisionByTarget[change.Entity+"\x00"+change.After.Name]; decision.Kind == "columnRename" {
			if old, found := previous.Entities[change.Entity].Columns[decision.From]; found && old.NotNull {
				continue
			}
		}
		missing(change.Entity, *change.After)
	}
	for _, change := range diff.ChangedColumns {
		if change.Before == nil || change.After == nil || change.Before.NotNull || !change.After.NotNull || change.After.AutoIncrement || strings.TrimSpace(change.After.BackfillSQL) != "" {
			continue
		}
		missing(change.Entity, *change.After)
	}
	return issues
}

func supportedFieldKind(kind FieldKind) bool {
	switch kind {
	case FieldID, FieldString, FieldLongText, FieldInt, FieldFloat, FieldBool,
		FieldDate, FieldDateTime, FieldJSON, FieldList, FieldObject, FieldBlob,
		FieldAutoIncrement, FieldCreatedAt, FieldUpdatedAt, FieldVersion, FieldReferenceVersion,
		FieldManyToOne, FieldOneToOne, FieldOneToMany, FieldManyToMany:
		return true
	default:
		return false
	}
}

func validBackfillExpression(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !strings.Contains(value, ";") && !strings.Contains(value, "--") &&
		!strings.Contains(value, "/*") && !strings.Contains(value, "*/") && !strings.ContainsRune(value, '\x00')
}

func ValidSpec(spec EntitySpec) error {
	issues := ValidateSpec(spec)
	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf("%s: %s", issues[0].Code, issues[0].Message)
}
