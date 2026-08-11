// Package entityschema models Shopware DAL entities independently from their
// PHP representation. The same normalized model drives previews, migrations,
// committed snapshots, and drift detection.
package entityschema

import (
	"fmt"
	"sort"
	"strings"
)

type FieldKind string

const (
	FieldID               FieldKind = "id"
	FieldString           FieldKind = "string"
	FieldLongText         FieldKind = "long-text"
	FieldInt              FieldKind = "int"
	FieldFloat            FieldKind = "float"
	FieldBool             FieldKind = "bool"
	FieldDate             FieldKind = "date"
	FieldDateTime         FieldKind = "datetime"
	FieldJSON             FieldKind = "json"
	FieldList             FieldKind = "list"
	FieldObject           FieldKind = "object"
	FieldBlob             FieldKind = "blob"
	FieldAutoIncrement    FieldKind = "auto-increment"
	FieldCreatedAt        FieldKind = "created-at"
	FieldUpdatedAt        FieldKind = "updated-at"
	FieldVersion          FieldKind = "version"
	FieldReferenceVersion FieldKind = "reference-version"
	FieldManyToOne        FieldKind = "many-to-one"
	FieldOneToOne         FieldKind = "one-to-one"
	FieldOneToMany        FieldKind = "one-to-many"
	FieldManyToMany       FieldKind = "many-to-many"
	FieldLocked           FieldKind = "locked"
)

type DeleteBehavior string

const (
	DeleteRestrict DeleteBehavior = "restrict"
	DeleteCascade  DeleteBehavior = "cascade"
	DeleteSetNull  DeleteBehavior = "set-null"
)

type IndexKind string

const (
	IndexNormal IndexKind = "index"
	IndexUnique IndexKind = "unique"
)

// FieldSpec is the editable designer representation. Relation rows are
// logical: a many-to-one row renders both an FkField and its association.
type FieldSpec struct {
	ID                     string         `json:"id"`
	Kind                   FieldKind      `json:"kind"`
	PropertyName           string         `json:"propertyName"`
	ForeignKeyPropertyName string         `json:"foreignKeyPropertyName,omitempty"`
	StorageName            string         `json:"storageName,omitempty"`
	Required               bool           `json:"required,omitempty"`
	Primary                bool           `json:"primary,omitempty"`
	APIAware               bool           `json:"apiAware,omitempty"`
	SearchRanking          float64        `json:"searchRanking,omitempty"`
	PreservedFlags         []string       `json:"preservedFlags,omitempty"`
	ModifiersBeforeFlags   []string       `json:"modifiersBeforeFlags,omitempty"`
	ModifiersAfterFlags    []string       `json:"modifiersAfterFlags,omitempty"`
	AssociationFlags       []string       `json:"associationFlags,omitempty"`
	AssociationBeforeFlags []string       `json:"associationModifiersBeforeFlags,omitempty"`
	AssociationAfterFlags  []string       `json:"associationModifiersAfterFlags,omitempty"`
	AssociationAPIAware    bool           `json:"associationApiAware,omitempty"`
	AssociationSearchRank  float64        `json:"associationSearchRanking,omitempty"`
	MaxLength              int            `json:"maxLength,omitempty"`
	Min                    *int           `json:"min,omitempty"`
	Max                    *int           `json:"max,omitempty"`
	ElementTypeClass       string         `json:"elementTypeClass,omitempty"`
	TargetDefinitionClass  string         `json:"targetDefinitionClass,omitempty"`
	TargetEntityClass      string         `json:"targetEntityClass,omitempty"`
	TargetCollectionClass  string         `json:"targetCollectionClass,omitempty"`
	TargetEntityName       string         `json:"targetEntityName,omitempty"`
	ReferenceField         string         `json:"referenceField,omitempty"`
	ReferenceStorageName   string         `json:"referenceStorageName,omitempty"`
	MappingDefinitionClass string         `json:"mappingDefinitionClass,omitempty"`
	MappingLocalColumn     string         `json:"mappingLocalColumn,omitempty"`
	MappingReferenceColumn string         `json:"mappingReferenceColumn,omitempty"`
	SourceColumn           string         `json:"sourceColumn,omitempty"`
	UsesExistingColumn     bool           `json:"usesExistingColumn,omitempty"`
	DeleteBehavior         DeleteBehavior `json:"deleteBehavior,omitempty"`
	MigrationDefault       string         `json:"migrationDefault,omitempty"`
	Editable               bool           `json:"editable"`
	Raw                    string         `json:"raw,omitempty"`
}

type IndexSpec struct {
	Name    string    `json:"name"`
	Kind    IndexKind `json:"kind"`
	Columns []string  `json:"columns"`
}

type EntitySpec struct {
	Mode               string      `json:"mode"`
	PluginRootURI      string      `json:"pluginRootUri"`
	DirectoryURI       string      `json:"directoryUri"`
	Namespace          string      `json:"namespace"`
	ClassName          string      `json:"className"`
	EntityName         string      `json:"entityName"`
	DefinitionClass    string      `json:"definitionClass,omitempty"`
	EntityClass        string      `json:"entityClass,omitempty"`
	CollectionClass    string      `json:"collectionClass,omitempty"`
	DefinitionURI      string      `json:"definitionUri,omitempty"`
	EntityURI          string      `json:"entityUri,omitempty"`
	CollectionURI      string      `json:"collectionUri,omitempty"`
	Fields             []FieldSpec `json:"fields"`
	Indexes            []IndexSpec `json:"indexes,omitempty"`
	ServiceURI         string      `json:"serviceUri,omitempty"`
	CreateMigration    bool        `json:"createMigration"`
	MigrationName      string      `json:"migrationName,omitempty"`
	MigrationTimestamp int64       `json:"migrationTimestamp,omitempty"`
	BaseSnapshotIDs    []string    `json:"baseSnapshotIds,omitempty"`
}

// Schema is the committed database-facing representation. Entity keys are
// technical entity/table names and column keys are physical storage names.
// PHP identities and presentation metadata belong to the live EntitySpec and
// indexes so moving a class or renaming a property cannot create schema drift.
type Schema struct {
	Entities map[string]Entity `json:"entities"`
}

type Entity struct {
	Name        string                `json:"name"`
	Columns     map[string]Column     `json:"columns"`
	Indexes     map[string]Index      `json:"indexes"`
	ForeignKeys map[string]ForeignKey `json:"foreignKeys"`
}

type Column struct {
	Name          string `json:"name"`
	SQLType       string `json:"sqlType"`
	NotNull       bool   `json:"notNull"`
	PrimaryKey    bool   `json:"primaryKey,omitempty"`
	AutoIncrement bool   `json:"autoIncrement,omitempty"`
	BackfillSQL   string `json:"-"`
}

type Index struct {
	Name    string   `json:"name"`
	Unique  bool     `json:"unique"`
	Columns []string `json:"columns"`
}

type ForeignKey struct {
	Name             string         `json:"name"`
	Column           string         `json:"column"`
	ReferenceEntity  string         `json:"referenceEntity"`
	ReferenceColumn  string         `json:"referenceColumn"`
	Columns          []string       `json:"columns,omitempty"`
	ReferenceColumns []string       `json:"referenceColumns,omitempty"`
	OnDelete         DeleteBehavior `json:"onDelete"`
	OnUpdate         string         `json:"onUpdate"`
}

func EmptySchema() Schema {
	return Schema{Entities: make(map[string]Entity)}
}

// Clone isolates mutable schema maps and slices before a designer mutates one
// entity. Snapshot baselines must never alias the proposed target schema.
func (s Schema) Clone() Schema {
	s = s.Normalize()
	result := EmptySchema()
	for name, entity := range s.Entities {
		copyEntity := entity
		copyEntity.Columns = make(map[string]Column, len(entity.Columns))
		for key, column := range entity.Columns {
			copyEntity.Columns[key] = column
		}
		copyEntity.Indexes = make(map[string]Index, len(entity.Indexes))
		for key, index := range entity.Indexes {
			index.Columns = append([]string(nil), index.Columns...)
			copyEntity.Indexes[key] = index
		}
		copyEntity.ForeignKeys = make(map[string]ForeignKey, len(entity.ForeignKeys))
		for key, foreignKey := range entity.ForeignKeys {
			foreignKey.Columns = append([]string(nil), foreignKey.Columns...)
			foreignKey.ReferenceColumns = append([]string(nil), foreignKey.ReferenceColumns...)
			copyEntity.ForeignKeys[key] = foreignKey
		}
		result.Entities[name] = copyEntity
	}
	return result
}

func SchemaFromSpec(spec EntitySpec) (Entity, error) {
	entity := Entity{
		Name:        spec.EntityName,
		Columns:     make(map[string]Column),
		Indexes:     make(map[string]Index),
		ForeignKeys: make(map[string]ForeignKey),
	}
	referenceVersions := make(map[string]FieldSpec)
	for _, field := range spec.Fields {
		if field.Kind == FieldReferenceVersion && field.TargetDefinitionClass != "" {
			referenceVersions[field.TargetDefinitionClass] = field
		}
	}
	for _, field := range spec.Fields {
		if !field.Editable && field.Kind == FieldLocked {
			continue
		}
		if field.Kind == FieldOneToMany || field.Kind == FieldManyToMany || (field.Kind == FieldOneToOne && field.UsesExistingColumn) {
			continue
		}
		if field.StorageName == "" {
			return Entity{}, fmt.Errorf("field %q has no storage name", field.PropertyName)
		}
		sqlType, err := SQLType(field)
		if err != nil {
			return Entity{}, err
		}
		column := Column{
			Name: field.StorageName, SQLType: sqlType, NotNull: field.Required,
			BackfillSQL: strings.TrimSpace(field.MigrationDefault),
		}
		if field.Primary {
			column.PrimaryKey = true
			column.NotNull = true
		}
		if field.Kind == FieldID {
			column.PrimaryKey = true
			column.NotNull = true
		}
		if field.Kind == FieldVersion {
			column.PrimaryKey = true
			column.NotNull = true
		}
		if field.Kind == FieldAutoIncrement {
			column.AutoIncrement = true
			column.NotNull = true
		}
		entity.Columns[column.Name] = column
		if field.Kind == FieldManyToOne || field.Kind == FieldOneToOne {
			referenceColumn := field.ReferenceStorageName
			if referenceColumn == "" {
				referenceColumn = "id"
			}
			onDelete := field.DeleteBehavior
			if onDelete == "" {
				onDelete = DeleteSetNull
			}
			fkName := "fk." + spec.EntityName + "." + field.StorageName
			columns := []string{field.StorageName}
			referenceColumns := []string{referenceColumn}
			var storedColumns, storedReferenceColumns []string
			if versionField, found := referenceVersions[field.TargetDefinitionClass]; found {
				columns = append(columns, versionField.StorageName)
				referenceColumns = append(referenceColumns, "version_id")
				storedColumns = append([]string(nil), columns...)
				storedReferenceColumns = append([]string(nil), referenceColumns...)
			}
			entity.ForeignKeys[fkName] = ForeignKey{
				Name: fkName, Column: field.StorageName,
				ReferenceEntity:  field.TargetEntityName,
				ReferenceColumn:  referenceColumn,
				Columns:          storedColumns,
				ReferenceColumns: storedReferenceColumns,
				OnDelete:         onDelete, OnUpdate: "cascade",
			}
			indexName := "idx." + spec.EntityName + "." + field.StorageName
			entity.Indexes[indexName] = Index{
				Name: indexName, Columns: append([]string(nil), columns...),
			}
		}
	}
	for _, index := range spec.Indexes {
		entity.Indexes[index.Name] = Index{
			Name: index.Name, Unique: index.Kind == IndexUnique,
			Columns: append([]string(nil), index.Columns...),
		}
	}
	return entity, nil
}

// IndexSpecsFromEntity restores only designer-owned indexes. Relation indexes
// are structural and are regenerated from their logical relation rows.
func IndexSpecsFromEntity(spec EntitySpec, entity Entity) []IndexSpec {
	automatic := make(map[string]Index)
	referenceVersions := make(map[string]string)
	for _, field := range spec.Fields {
		if field.Kind == FieldReferenceVersion && field.TargetDefinitionClass != "" {
			referenceVersions[field.TargetDefinitionClass] = field.StorageName
		}
	}
	for _, field := range spec.Fields {
		if (field.Kind != FieldManyToOne && field.Kind != FieldOneToOne) || field.StorageName == "" || field.UsesExistingColumn {
			continue
		}
		name := "idx." + spec.EntityName + "." + field.StorageName
		columns := []string{field.StorageName}
		if versionColumn := referenceVersions[field.TargetDefinitionClass]; versionColumn != "" {
			columns = append(columns, versionColumn)
		}
		automatic[name] = Index{Name: name, Columns: columns}
	}
	var result []IndexSpec
	for name, index := range entity.Indexes {
		if generated, found := automatic[name]; found && sameIndexShape(generated, index) {
			continue
		}
		kind := IndexNormal
		if index.Unique {
			kind = IndexUnique
		}
		result = append(result, IndexSpec{Name: name, Kind: kind, Columns: append([]string(nil), index.Columns...)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// RestoreSnapshotOnlyIndexes overlays indexes that cannot be represented by a
// DAL EntityDefinition. Foreign-key indexes remain derived from the current
// PHP relation fields, so an obsolete relation index is never resurrected.
func RestoreSnapshotOnlyIndexes(scanned, snapshot Schema) Schema {
	scanned = scanned.Clone()
	snapshot = snapshot.Normalize()
	for entityName, current := range scanned.Entities {
		committed, found := snapshot.Entities[entityName]
		if !found {
			continue
		}
		for name, index := range committed.Indexes {
			if relationIndex(committed, index) {
				continue
			}
			index.Columns = append([]string(nil), index.Columns...)
			current.Indexes[name] = index
		}
		scanned.Entities[entityName] = current
	}
	return scanned
}

func relationIndex(entity Entity, index Index) bool {
	for _, foreignKey := range entity.ForeignKeys {
		if index.Name == "idx."+entity.Name+"."+foreignKey.Column && !index.Unique && sameStrings(index.Columns, foreignKeyColumns(foreignKey)) {
			return true
		}
	}
	return false
}

func sameIndexShape(left, right Index) bool {
	if left.Name != right.Name || left.Unique != right.Unique || len(left.Columns) != len(right.Columns) {
		return false
	}
	for index := range left.Columns {
		if left.Columns[index] != right.Columns[index] {
			return false
		}
	}
	return true
}

func SQLType(field FieldSpec) (string, error) {
	switch field.Kind {
	case FieldID, FieldVersion, FieldReferenceVersion, FieldManyToOne, FieldOneToOne:
		return "BINARY(16)", nil
	case FieldString:
		length := field.MaxLength
		if length <= 0 {
			length = 255
		}
		return fmt.Sprintf("VARCHAR(%d)", length), nil
	case FieldLongText:
		return "LONGTEXT", nil
	case FieldInt:
		return "INT", nil
	case FieldFloat:
		return "DOUBLE", nil
	case FieldBool:
		return "TINYINT(1)", nil
	case FieldDate:
		return "DATE", nil
	case FieldDateTime, FieldCreatedAt, FieldUpdatedAt:
		return "DATETIME(3)", nil
	case FieldJSON, FieldList, FieldObject:
		return "JSON", nil
	case FieldBlob:
		return "LONGBLOB", nil
	case FieldAutoIncrement:
		return "BIGINT UNSIGNED", nil
	default:
		return "", fmt.Errorf("unsupported stored field kind %q", field.Kind)
	}
}

func isForeignKeyKind(kind FieldKind) bool {
	return kind == FieldManyToOne || kind == FieldOneToOne
}

func isJSONKind(kind FieldKind) bool {
	return kind == FieldJSON || kind == FieldList || kind == FieldObject
}

func (s Schema) Normalize() Schema {
	if s.Entities == nil {
		s.Entities = make(map[string]Entity)
	}
	for key, entity := range s.Entities {
		if entity.Columns == nil {
			entity.Columns = make(map[string]Column)
		}
		if entity.Indexes == nil {
			entity.Indexes = make(map[string]Index)
		}
		if entity.ForeignKeys == nil {
			entity.ForeignKeys = make(map[string]ForeignKey)
		}
		for name, foreignKey := range entity.ForeignKeys {
			foreignKey.Columns = append([]string(nil), foreignKey.Columns...)
			foreignKey.ReferenceColumns = append([]string(nil), foreignKey.ReferenceColumns...)
			entity.ForeignKeys[name] = foreignKey
		}
		for name, index := range entity.Indexes {
			index.Columns = append([]string(nil), index.Columns...)
			entity.Indexes[name] = index
		}
		s.Entities[key] = entity
	}
	return s
}

func ShortClass(class string) string {
	class = strings.Trim(class, "\\")
	if index := strings.LastIndex(class, "\\"); index >= 0 {
		return class[index+1:]
	}
	return class
}
