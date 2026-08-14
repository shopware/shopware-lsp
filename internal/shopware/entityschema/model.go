// Package entityschema models Shopware DAL entities independently from their
// PHP representation. The same normalized model drives previews, migrations,
// committed snapshots, and drift detection.
package entityschema

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

type FieldKind string

type DefinitionKind string

const (
	DefinitionEntity        DefinitionKind = "entity"
	DefinitionMapping       DefinitionKind = "mapping"
	DefinitionExtension     DefinitionKind = "extension"
	DefinitionBulkExtension DefinitionKind = "bulk-extension"
)

type BulkExtensionTargetSpec struct {
	ID                      string                `json:"id"`
	EntityName              string                `json:"entityName"`
	ExtendedDefinitionClass string                `json:"extendedDefinitionClass,omitempty"`
	ExtendedFields          []RelationTargetField `json:"extendedFields,omitempty"`
	Fields                  []FieldSpec           `json:"fields"`
	Indexes                 []IndexSpec           `json:"indexes,omitempty"`
}

const (
	FieldID               FieldKind = "id"
	FieldBinaryID         FieldKind = "binary-id"
	FieldString           FieldKind = "string"
	FieldEnum             FieldKind = "enum"
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
	FieldForeignKey       FieldKind = "foreign-key"
	FieldManyToOne        FieldKind = "many-to-one"
	FieldOneToOne         FieldKind = "one-to-one"
	FieldOneToMany        FieldKind = "one-to-many"
	FieldManyToMany       FieldKind = "many-to-many"
	FieldHierarchy        FieldKind = "hierarchy"
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

// FieldBehavior describes DAL flags which change how a field participates in
// persistence. It is shared by stored fields and association/facade fields so
// those flags do not have to fall back to opaque PHP snippets.
type FieldBehavior struct {
	Runtime                       bool     `json:"runtime,omitempty"`
	RuntimeDependencies           []string `json:"runtimeDependencies,omitempty"`
	RuntimeDependenciesExpression string   `json:"runtimeDependenciesExpression,omitempty" jsonschema:"opaque PHP expression used only when imported Runtime dependencies are not a literal string list"`
	Computed                      bool     `json:"computed,omitempty"`
	NoConstraint                  bool     `json:"noConstraint,omitempty" jsonschema:"the FkField has no physical database constraint; valid only for stored foreign keys"`
}

type FieldImplementation struct {
	Class                      string   `json:"class" jsonschema:"fully-qualified DAL Field subclass"`
	ConstructorMode            string   `json:"constructorMode" jsonschema:"storage-property or fixed"`
	AdditionalArguments        []string `json:"additionalArguments,omitempty" jsonschema:"losslessly imported PHP constructor expressions after storage and property"`
	FixedArguments             []string `json:"fixedArguments,omitempty" jsonschema:"losslessly imported constructor expressions for a field with fixed storage and property names"`
	FixedStorageName           string   `json:"fixedStorageName,omitempty"`
	FixedPropertyName          string   `json:"fixedPropertyName,omitempty"`
	EntityType                 string   `json:"entityType,omitempty" jsonschema:"fully-qualified PHP entity value type or a builtin scalar"`
	EntityBooleanGetter        bool     `json:"entityBooleanGetter,omitempty"`
	EntityTrait                string   `json:"entityTrait,omitempty" jsonschema:"fully-qualified trait that owns the entity property and accessors"`
	ManageEntity               bool     `json:"manageEntity,omitempty" jsonschema:"whether entity property and accessors can be generated safely"`
	ImplicitComputed           bool     `json:"implicitComputed,omitempty"`
	MaxLengthArgument          bool     `json:"maxLengthArgument,omitempty"`
	MinimumAdditionalArguments int      `json:"minimumAdditionalArguments,omitempty"`
}

type ConditionalAssociation struct {
	ConditionExpression string    `json:"conditionExpression" jsonschema:"safe inline PHP condition choosing the primary association shape"`
	AlternativeKind     FieldKind `json:"alternativeKind" jsonschema:"many-to-one or one-to-one"`
	AlternativeAutoload bool      `json:"alternativeAutoload,omitempty"`
}

type FieldMetadata struct {
	AllowHTML                 *bool        `json:"allowHtmlSanitized,omitempty" jsonschema:"presence adds AllowHtml; the value controls sanitization"`
	AllowEmptyString          bool         `json:"allowEmptyString,omitempty"`
	AsArray                   bool         `json:"asArray,omitempty"`
	Immutable                 bool         `json:"immutable,omitempty"`
	Since                     string       `json:"since,omitempty"`
	Deprecated                *Deprecation `json:"deprecated,omitempty"`
	IgnoreInOpenAPISchema     bool         `json:"ignoreInOpenapiSchema,omitempty"`
	IgnoreInUnusedMediaSearch bool         `json:"ignoreInUnusedMediaSearch,omitempty"`
	APICriteriaAware          bool         `json:"apiCriteriaAware,omitempty"`
	RuleAreas                 []string     `json:"ruleAreas,omitempty"`
	Choice                    *ChoiceSpec  `json:"choice,omitempty"`
	DoNotUseContext           bool         `json:"doNotUseContext,omitempty"`
	Extension                 bool         `json:"extension,omitempty"`
}

type Deprecation struct {
	DeprecatedSince string `json:"deprecatedSince"`
	WillBeRemovedIn string `json:"willBeRemovedIn"`
	ReplacedBy      string `json:"replacedBy,omitempty"`
}

type ChoiceSpec struct {
	Values []string `json:"values" jsonschema:"literal PHP scalar or constant expressions imported from the Choice array"`
	Strict *bool    `json:"strict,omitempty"`
}

type FieldFlagKind string

const (
	FlagRequired                  FieldFlagKind = "required"
	FlagPrimaryKey                FieldFlagKind = "primary-key"
	FlagAPIAware                  FieldFlagKind = "api-aware"
	FlagSearchRanking             FieldFlagKind = "search-ranking"
	FlagCascadeDelete             FieldFlagKind = "cascade-delete"
	FlagSetNullOnDelete           FieldFlagKind = "set-null-on-delete"
	FlagRestrictDelete            FieldFlagKind = "restrict-delete"
	FlagRuntime                   FieldFlagKind = "runtime"
	FlagComputed                  FieldFlagKind = "computed"
	FlagNoConstraint              FieldFlagKind = "no-constraint"
	FlagInherited                 FieldFlagKind = "inherited"
	FlagReverseInherited          FieldFlagKind = "reverse-inherited"
	FlagWriteProtected            FieldFlagKind = "write-protected"
	FlagAllowHTML                 FieldFlagKind = "allow-html"
	FlagAllowEmptyString          FieldFlagKind = "allow-empty-string"
	FlagAsArray                   FieldFlagKind = "as-array"
	FlagImmutable                 FieldFlagKind = "immutable"
	FlagSince                     FieldFlagKind = "since"
	FlagDeprecated                FieldFlagKind = "deprecated"
	FlagIgnoreInOpenAPISchema     FieldFlagKind = "ignore-in-openapi-schema"
	FlagIgnoreInUnusedMediaSearch FieldFlagKind = "ignore-in-unused-media-search"
	FlagAPICriteriaAware          FieldFlagKind = "api-criteria-aware"
	FlagRuleAreas                 FieldFlagKind = "rule-areas"
	FlagChoice                    FieldFlagKind = "choice"
	FlagDoNotUseContext           FieldFlagKind = "do-not-use-context"
	FlagExtension                 FieldFlagKind = "extension"
)

// FieldFlagSpec is a discriminated, serializable representation of every DAL
// flag that the class-based field model understands. Only arguments belonging
// to Kind are accepted by validation.
type FieldFlagSpec struct {
	Kind                          FieldFlagKind `json:"kind"`
	APISources                    []string      `json:"apiSources,omitempty"`
	SearchRanking                 float64       `json:"searchRanking,omitempty"`
	SearchTokenize                *bool         `json:"searchTokenize,omitempty"`
	RuntimeDependencies           []string      `json:"runtimeDependencies,omitempty"`
	RuntimeDependenciesExpression string        `json:"runtimeDependenciesExpression,omitempty"`
	InheritedForeignKey           string        `json:"inheritedForeignKey,omitempty"`
	ReverseProperty               string        `json:"reverseProperty,omitempty"`
	WriteScopes                   []string      `json:"writeScopes,omitempty"`
	AllowHTMLSanitized            *bool         `json:"allowHtmlSanitized,omitempty"`
	CloneRelevant                 *bool         `json:"cloneRelevant,omitempty"`
	EnforcedByConstraint          *bool         `json:"enforcedByConstraint,omitempty"`
	Since                         string        `json:"since,omitempty"`
	Deprecated                    *Deprecation  `json:"deprecated,omitempty"`
	RuleAreas                     []string      `json:"ruleAreas,omitempty"`
	Choice                        *ChoiceSpec   `json:"choice,omitempty"`
}

type FieldModificationSpec struct {
	ID           string          `json:"id"`
	PropertyName string          `json:"propertyName"`
	AddFlags     []FieldFlagSpec `json:"addFlags,omitempty"`
	RemoveFlags  []FieldFlagKind `json:"removeFlags,omitempty"`
}

// FieldSpec is the editable designer representation. Relation rows are
// logical: a many-to-one row renders both an FkField and its association.
type FieldSpec struct {
	ID                            string                  `json:"id"`
	Kind                          FieldKind               `json:"kind"`
	PropertyName                  string                  `json:"propertyName"`
	ForeignKeyPropertyName        string                  `json:"foreignKeyPropertyName,omitempty"`
	StorageName                   string                  `json:"storageName,omitempty"`
	Required                      bool                    `json:"required,omitempty"`
	Primary                       bool                    `json:"primary,omitempty"`
	APIAware                      bool                    `json:"apiAware,omitempty"`
	APIAwareSources               []string                `json:"apiAwareSources,omitempty" jsonschema:"optional fully-qualified API source classes; empty permits both Admin and Store API"`
	SearchRanking                 float64                 `json:"searchRanking,omitempty"`
	SearchRankingTokenize         *bool                   `json:"searchRankingTokenize,omitempty"`
	Behavior                      *FieldBehavior          `json:"behavior,omitempty"`
	Implementation                *FieldImplementation    `json:"implementation,omitempty"`
	Metadata                      *FieldMetadata          `json:"metadata,omitempty"`
	WriteProtected                bool                    `json:"writeProtected,omitempty"`
	WriteProtectedScopes          []string                `json:"writeProtectedScopes,omitempty" jsonschema:"optional literal DAL write scopes such as system, user, or crud"`
	Inherited                     bool                    `json:"inherited,omitempty" jsonschema:"whether the stored DAL field carries the Inherited flag"`
	InheritedForeignKey           string                  `json:"inheritedForeignKey,omitempty" jsonschema:"optional foreign-key override passed to the stored field Inherited flag"`
	PreservedFlags                []string                `json:"preservedFlags,omitempty"`
	ModifiersBeforeFlags          []string                `json:"modifiersBeforeFlags,omitempty"`
	ModifiersAfterFlags           []string                `json:"modifiersAfterFlags,omitempty"`
	AssociationFlags              []string                `json:"associationFlags,omitempty"`
	AssociationBeforeFlags        []string                `json:"associationModifiersBeforeFlags,omitempty"`
	AssociationAfterFlags         []string                `json:"associationModifiersAfterFlags,omitempty"`
	AssociationAPIAware           bool                    `json:"associationApiAware,omitempty"`
	AssociationAPIAwareSources    []string                `json:"associationApiAwareSources,omitempty"`
	AssociationSearchRank         float64                 `json:"associationSearchRanking,omitempty"`
	AssociationSearchTokenize     *bool                   `json:"associationSearchRankingTokenize,omitempty"`
	AssociationBehavior           *FieldBehavior          `json:"associationBehavior,omitempty"`
	AssociationMetadata           *FieldMetadata          `json:"associationMetadata,omitempty"`
	AssociationAutoload           bool                    `json:"associationAutoload,omitempty" jsonschema:"whether a many-to-one or one-to-one association is autoloaded by Shopware DAL"`
	ConditionalAssociation        *ConditionalAssociation `json:"conditionalAssociation,omitempty"`
	AssociationWriteProtected     bool                    `json:"associationWriteProtected,omitempty"`
	AssociationWriteScopes        []string                `json:"associationWriteProtectedScopes,omitempty"`
	AssociationInherited          bool                    `json:"associationInherited,omitempty" jsonschema:"whether the association field carries the Inherited flag"`
	AssociationInheritedFK        string                  `json:"associationInheritedForeignKey,omitempty" jsonschema:"optional foreign-key override passed to the association Inherited flag"`
	ReverseInheritedProperty      string                  `json:"reverseInheritedProperty,omitempty" jsonschema:"property on the target inheritance-aware entity passed to ReverseInherited"`
	HierarchyParentProperty       string                  `json:"hierarchyParentProperty,omitempty"`
	HierarchyChildrenFlags        []string                `json:"hierarchyChildrenFlags,omitempty"`
	HierarchyChildrenBefore       []string                `json:"hierarchyChildrenModifiersBeforeFlags,omitempty"`
	HierarchyChildrenAfter        []string                `json:"hierarchyChildrenModifiersAfterFlags,omitempty"`
	HierarchyChildrenAPIAware     bool                    `json:"hierarchyChildrenApiAware,omitempty"`
	HierarchyChildrenAPISources   []string                `json:"hierarchyChildrenApiAwareSources,omitempty"`
	HierarchyChildrenRank         float64                 `json:"hierarchyChildrenSearchRanking,omitempty"`
	HierarchyChildrenTokenize     *bool                   `json:"hierarchyChildrenSearchRankingTokenize,omitempty"`
	HierarchyChildrenBehavior     *FieldBehavior          `json:"hierarchyChildrenBehavior,omitempty"`
	HierarchyChildrenMetadata     *FieldMetadata          `json:"hierarchyChildrenMetadata,omitempty"`
	HierarchyChildrenProtected    bool                    `json:"hierarchyChildrenWriteProtected,omitempty"`
	HierarchyChildrenWriteScopes  []string                `json:"hierarchyChildrenWriteProtectedScopes,omitempty"`
	HierarchyChildrenInherited    bool                    `json:"hierarchyChildrenInherited,omitempty"`
	HierarchyChildrenInheritedFK  string                  `json:"hierarchyChildrenInheritedForeignKey,omitempty"`
	HierarchyChildrenReverse      string                  `json:"hierarchyChildrenReverseInheritedProperty,omitempty"`
	HierarchyVersionAware         bool                    `json:"hierarchyVersionAware,omitempty"`
	HierarchyVersionAPIAware      bool                    `json:"hierarchyVersionApiAware,omitempty"`
	HierarchyVersionAPISources    []string                `json:"hierarchyVersionApiAwareSources,omitempty"`
	HierarchyVersionProtected     bool                    `json:"hierarchyVersionWriteProtected,omitempty"`
	HierarchyVersionWriteScopes   []string                `json:"hierarchyVersionWriteProtectedScopes,omitempty"`
	HierarchyVersionInherited     bool                    `json:"hierarchyVersionInherited,omitempty"`
	HierarchyVersionInheritedFK   string                  `json:"hierarchyVersionInheritedForeignKey,omitempty"`
	HierarchyVersionFlags         []string                `json:"hierarchyVersionFlags,omitempty"`
	HierarchyVersionBehavior      *FieldBehavior          `json:"hierarchyVersionBehavior,omitempty"`
	HierarchyVersionMetadata      *FieldMetadata          `json:"hierarchyVersionMetadata,omitempty"`
	HierarchyVersionBefore        []string                `json:"hierarchyVersionModifiersBeforeFlags,omitempty"`
	HierarchyVersionAfter         []string                `json:"hierarchyVersionModifiersAfterFlags,omitempty"`
	MaxLength                     int                     `json:"maxLength,omitempty"`
	Min                           *int                    `json:"min,omitempty"`
	Max                           *int                    `json:"max,omitempty"`
	ElementTypeClass              string                  `json:"elementTypeClass,omitempty"`
	JSONPropertyMappingExpression string                  `json:"jsonPropertyMappingExpression,omitempty" jsonschema:"safe inline PHP array expression passed as JsonField property mapping"`
	JSONDefaultExpression         string                  `json:"jsonDefaultExpression,omitempty" jsonschema:"safe inline PHP array or null expression passed as JsonField default"`
	EnumClass                     string                  `json:"enumClass,omitempty" jsonschema:"fully-qualified backed enum class stored by EnumField"`
	EnumCase                      string                  `json:"enumCase,omitempty" jsonschema:"enum case name passed to the EnumField constructor"`
	EnumBackingType               string                  `json:"enumBackingType,omitempty" jsonschema:"database backing type: string or int"`
	TargetDefinitionClass         string                  `json:"targetDefinitionClass,omitempty"`
	TargetEntityClass             string                  `json:"targetEntityClass,omitempty"`
	TargetCollectionClass         string                  `json:"targetCollectionClass,omitempty"`
	TargetEntityName              string                  `json:"targetEntityName,omitempty"`
	ReferenceField                string                  `json:"referenceField,omitempty"`
	ReferenceStorageName          string                  `json:"referenceStorageName,omitempty"`
	MappingDefinitionClass        string                  `json:"mappingDefinitionClass,omitempty"`
	MappingLocalColumn            string                  `json:"mappingLocalColumn,omitempty"`
	MappingReferenceColumn        string                  `json:"mappingReferenceColumn,omitempty"`
	SourceColumn                  string                  `json:"sourceColumn,omitempty"`
	UsesExistingColumn            bool                    `json:"usesExistingColumn,omitempty"`
	DeleteBehavior                DeleteBehavior          `json:"deleteBehavior,omitempty"`
	DeleteCloneRelevant           *bool                   `json:"deleteCloneRelevant,omitempty" jsonschema:"CascadeDelete clone relevance; omitted means the Shopware default true"`
	DeleteEnforcedByConstraint    *bool                   `json:"deleteEnforcedByConstraint,omitempty" jsonschema:"SetNullOnDelete database-constraint enforcement; omitted means the Shopware default true"`
	MigrationDefault              string                  `json:"migrationDefault,omitempty"`
	Translated                    bool                    `json:"translated,omitempty"`
	TranslationDefinitionOnly     bool                    `json:"translationDefinitionOnly,omitempty"`
	TranslationUseForSort         bool                    `json:"translationUseForSorting,omitempty"`
	TranslationAPIAware           *bool                   `json:"translationApiAware,omitempty"`
	TranslationAPIAwareSources    []string                `json:"translationApiAwareSources,omitempty"`
	TranslationSearchRank         *float64                `json:"translationSearchRanking,omitempty"`
	TranslationSearchTokenize     *bool                   `json:"translationSearchRankingTokenize,omitempty"`
	TranslationBehavior           *FieldBehavior          `json:"translationBehavior,omitempty"`
	TranslationMetadata           *FieldMetadata          `json:"translationMetadata,omitempty"`
	TranslationWriteProtected     bool                    `json:"translationWriteProtected,omitempty"`
	TranslationWriteScopes        []string                `json:"translationWriteProtectedScopes,omitempty"`
	TranslationInherited          bool                    `json:"translationInherited,omitempty" jsonschema:"whether the parent TranslatedField facade carries the Inherited flag"`
	TranslationInheritedFK        string                  `json:"translationInheritedForeignKey,omitempty" jsonschema:"optional foreign-key override passed to the translated facade Inherited flag"`
	TranslationFlags              []string                `json:"translationFlags,omitempty"`
	TranslationBeforeFlags        []string                `json:"translationModifiersBeforeFlags,omitempty"`
	TranslationAfterFlags         []string                `json:"translationModifiersAfterFlags,omitempty"`
	Editable                      bool                    `json:"editable"`
	Raw                           string                  `json:"raw,omitempty"`
}

type IndexSpec struct {
	Name        string    `json:"name"`
	Kind        IndexKind `json:"kind"`
	Columns     []string  `json:"columns"`
	Translation bool      `json:"translation,omitempty" jsonschema:"index belongs to the generated translation table rather than the parent table"`
}

// TranslationSpec describes the companion DAL translation bundle owned by a
// parent entity. Its PHP identities are live presentation metadata; the
// resulting translation table is persisted in Schema like any other entity.
type TranslationSpec struct {
	Enabled                    bool                    `json:"enabled"`
	EntityName                 string                  `json:"entityName,omitempty"`
	DefinitionClass            string                  `json:"definitionClass,omitempty"`
	EntityClass                string                  `json:"entityClass,omitempty"`
	CollectionClass            string                  `json:"collectionClass,omitempty"`
	DefinitionURI              string                  `json:"definitionUri,omitempty"`
	EntityURI                  string                  `json:"entityUri,omitempty"`
	CollectionURI              string                  `json:"collectionUri,omitempty"`
	ParentDefinitionClass      string                  `json:"parentDefinitionClass,omitempty"`
	ParentStorageName          string                  `json:"parentStorageName,omitempty"`
	ParentPropertyName         string                  `json:"parentPropertyName,omitempty"`
	AssociationProperty        string                  `json:"associationProperty,omitempty"`
	AssociationLocalField      string                  `json:"associationLocalField,omitempty"`
	AssociationRequired        bool                    `json:"associationRequired,omitempty"`
	AssociationAPIAware        bool                    `json:"associationApiAware,omitempty"`
	AssociationAPIAwareSources []string                `json:"associationApiAwareSources,omitempty"`
	AssociationBehavior        *FieldBehavior          `json:"associationBehavior,omitempty"`
	AssociationMetadata        *FieldMetadata          `json:"associationMetadata,omitempty"`
	AssociationWriteProtected  bool                    `json:"associationWriteProtected,omitempty"`
	AssociationWriteScopes     []string                `json:"associationWriteProtectedScopes,omitempty"`
	AssociationInherited       bool                    `json:"associationInherited,omitempty"`
	AssociationInheritedFK     string                  `json:"associationInheritedForeignKey,omitempty"`
	ReverseInheritedProperty   string                  `json:"reverseInheritedProperty,omitempty"`
	AssociationFlags           []string                `json:"associationFlags,omitempty"`
	AssociationBeforeFlags     []string                `json:"associationModifiersBeforeFlags,omitempty"`
	AssociationAfterFlags      []string                `json:"associationModifiersAfterFlags,omitempty"`
	DefinitionBehavior         *DefinitionBehaviorSpec `json:"definitionBehavior,omitempty" jsonschema:"translation-definition explicit awareness and default-field behavior"`
	DefinitionMetadata         *DefinitionMetadataSpec `json:"definitionMetadata,omitempty" jsonschema:"translation-definition availability and literal write defaults"`
}

type DefinitionDefaultSpec struct {
	PropertyName    string `json:"propertyName" jsonschema:"DAL property name receiving the default"`
	ValueExpression string `json:"valueExpression" jsonschema:"PHP expression returned for the default value"`
}

// DefinitionMetadataSpec represents the common class-based EntityDefinition
// hooks that are safe and useful to author structurally. Arbitrary method
// bodies remain immutable raw methods when imported.
type DefinitionMetadataSpec struct {
	Since                  string                  `json:"since,omitempty" jsonschema:"Shopware version in which this definition became available"`
	Defaults               []DefinitionDefaultSpec `json:"defaults,omitempty" jsonschema:"literal getDefaults property-to-PHP-expression entries"`
	ChildDefaults          []DefinitionDefaultSpec `json:"childDefaults,omitempty" jsonschema:"literal getChildDefaults entries for inheritance-aware child records"`
	HydratorClass          string                  `json:"hydratorClass,omitempty" jsonschema:"optional custom EntityHydrator subclass"`
	SinceMethodRaw         string                  `json:"sinceMethodRaw,omitempty" jsonschema:"lossless non-editable custom since method"`
	DefaultsMethodRaw      string                  `json:"defaultsMethodRaw,omitempty" jsonschema:"lossless non-editable custom getDefaults method"`
	ChildDefaultsMethodRaw string                  `json:"childDefaultsMethodRaw,omitempty" jsonschema:"lossless non-editable custom getChildDefaults method"`
	HydratorMethodRaw      string                  `json:"hydratorMethodRaw,omitempty" jsonschema:"lossless non-editable custom getHydratorClass method"`
}

// DefinitionBehaviorSpec represents class-level EntityDefinition behavior
// which is not expressed by defineFields itself. Literal behavior remains
// editable; non-literal implementations are retained as immutable source so
// editing an existing definition never drops custom framework behavior.
type DefinitionBehaviorSpec struct {
	ParentDefinitionClass        string      `json:"parentDefinitionClass,omitempty" jsonschema:"optional aggregate parent EntityDefinition class"`
	VersionAware                 *bool       `json:"versionAware,omitempty" jsonschema:"explicit isVersionAware return; omitted uses the framework field-derived behavior"`
	OverrideDefaultFields        bool        `json:"overrideDefaultFields,omitempty" jsonschema:"override EntityDefinition.defaultFields; an empty defaultFields list disables implicit timestamps"`
	DefaultFields                []FieldSpec `json:"defaultFields,omitempty" jsonschema:"literal fields returned by an overridden defaultFields method"`
	OverrideBaseFields           bool        `json:"overrideBaseFields,omitempty" jsonschema:"override EntityDefinition.getBaseFields; an empty baseFields list disables inherited base fields"`
	BaseFields                   []FieldSpec `json:"baseFields,omitempty" jsonschema:"literal fields returned by an overridden getBaseFields method"`
	RestrictDeleteMetaProperties []string    `json:"restrictDeleteMetaProperties,omitempty" jsonschema:"properties retained as metadata when this entity participates in restricted deletes"`
	ParentDefinitionMethodRaw    string      `json:"parentDefinitionMethodRaw,omitempty" jsonschema:"lossless non-editable custom getParentDefinitionClass method"`
	VersionAwareMethodRaw        string      `json:"versionAwareMethodRaw,omitempty" jsonschema:"lossless non-editable custom isVersionAware method"`
	InheritanceAwareMethodRaw    string      `json:"inheritanceAwareMethodRaw,omitempty" jsonschema:"lossless non-editable custom isInheritanceAware method"`
	DefaultFieldsMethodRaw       string      `json:"defaultFieldsMethodRaw,omitempty" jsonschema:"lossless non-editable custom defaultFields method"`
	BaseFieldsMethodRaw          string      `json:"baseFieldsMethodRaw,omitempty" jsonschema:"lossless non-editable custom getBaseFields method"`
	RestrictDeleteMetaMethodRaw  string      `json:"restrictDeleteMetaMethodRaw,omitempty" jsonschema:"lossless non-editable custom getRestrictDeleteMetaFields method"`
}

type EntitySpec struct {
	Mode                    string                    `json:"mode"`
	DefinitionKind          DefinitionKind            `json:"definitionKind,omitempty" jsonschema:"entity generates an EntityDefinition; mapping generates a MappingEntityDefinition; extension generates an EntityExtension; bulk-extension generates a multi-target BulkEntityExtension"`
	PluginRootURI           string                    `json:"pluginRootUri"`
	DirectoryURI            string                    `json:"directoryUri"`
	Namespace               string                    `json:"namespace"`
	ClassName               string                    `json:"className"`
	EntityName              string                    `json:"entityName"`
	ExtendedDefinitionClass string                    `json:"extendedDefinitionClass,omitempty"`
	ExtendedFields          []RelationTargetField     `json:"extendedFields,omitempty" jsonschema:"indexed physical fields of the EntityExtension target; presentation metadata revalidated by the server"`
	InheritanceAware        bool                      `json:"inheritanceAware,omitempty" jsonschema:"render isInheritanceAware true; requires one native hierarchy bundle on entity definitions"`
	DefinitionBehavior      *DefinitionBehaviorSpec   `json:"definitionBehavior,omitempty" jsonschema:"aggregate parent, explicit awareness, default fields, and restrict-delete metadata behavior"`
	DefinitionMetadata      *DefinitionMetadataSpec   `json:"definitionMetadata,omitempty" jsonschema:"class-based definition availability, defaults, child defaults, and custom hydrator"`
	ReadProtected           bool                      `json:"readProtected,omitempty"`
	ReadProtectionScopes    []string                  `json:"readProtectionScopes,omitempty"`
	WriteProtected          bool                      `json:"writeProtected,omitempty"`
	WriteProtectionScopes   []string                  `json:"writeProtectionScopes,omitempty"`
	PreservedProtections    []string                  `json:"preservedProtections,omitempty"`
	ProtectionMethodRaw     string                    `json:"protectionMethodRaw,omitempty" jsonschema:"lossless non-editable defineProtections method imported when its body is not a literal collection"`
	FieldModifications      []FieldModificationSpec   `json:"fieldModifications,omitempty" jsonschema:"typed flag changes applied to existing target fields by EntityExtension.modifyFields"`
	ModifyFieldsMethodRaw   string                    `json:"modifyFieldsMethodRaw,omitempty" jsonschema:"lossless non-editable modifyFields method when its body is not a literal supported flag mutation"`
	CollectMethodRaw        string                    `json:"collectMethodRaw,omitempty" jsonschema:"lossless non-editable BulkEntityExtension collect method when its targets cannot be represented as literal yields"`
	BulkExtensions          []BulkExtensionTargetSpec `json:"bulkExtensions,omitempty" jsonschema:"targets and fields yielded by a BulkEntityExtension collect method"`
	ShopwareVersion         string                    `json:"shopwareVersion,omitempty"`
	DefinitionClass         string                    `json:"definitionClass,omitempty"`
	EntityClass             string                    `json:"entityClass,omitempty"`
	CollectionClass         string                    `json:"collectionClass,omitempty"`
	DefinitionURI           string                    `json:"definitionUri,omitempty"`
	EntityURI               string                    `json:"entityUri,omitempty"`
	CollectionURI           string                    `json:"collectionUri,omitempty"`
	Fields                  []FieldSpec               `json:"fields"`
	Indexes                 []IndexSpec               `json:"indexes,omitempty"`
	Translation             *TranslationSpec          `json:"translation,omitempty"`
	ServiceURI              string                    `json:"serviceUri,omitempty"`
	CreateMigration         bool                      `json:"createMigration"`
	MigrationName           string                    `json:"migrationName,omitempty"`
	MigrationTimestamp      int64                     `json:"migrationTimestamp,omitempty"`
	BaseSnapshotIDs         []string                  `json:"baseSnapshotIds,omitempty"`
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
	External    bool                  `json:"external,omitempty"`
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
	spec = CompleteSpec(spec)
	if spec.DefinitionKind == DefinitionBulkExtension {
		return Entity{}, fmt.Errorf("BulkEntityExtension owns multiple partial entities; use SchemaEntitiesFromSpec")
	}
	columns := make(map[string]Column)
	if spec.DefinitionKind == DefinitionEntity && (spec.DefinitionBehavior == nil || !spec.DefinitionBehavior.OverrideDefaultFields) {
		columns = defaultTimestampColumns()
	}
	entity := Entity{
		Name:        spec.EntityName,
		External:    spec.DefinitionKind == DefinitionExtension,
		Columns:     columns,
		Indexes:     make(map[string]Index),
		ForeignKeys: make(map[string]ForeignKey),
	}
	fields := schemaDefinitionFields(spec)
	referenceVersions := make(map[string]FieldSpec)
	for _, field := range fields {
		if field.Kind == FieldReferenceVersion && field.TargetDefinitionClass != "" {
			referenceVersions[field.TargetDefinitionClass] = field
		}
	}
	for _, field := range fields {
		if isImplicitTimestampField(spec, field.Kind) {
			continue
		}
		if !field.Editable && field.Kind == FieldLocked {
			continue
		}
		if field.Behavior != nil && field.Behavior.Runtime {
			continue
		}
		if field.Translated || field.Kind == FieldOneToMany || field.Kind == FieldManyToMany ||
			((field.Kind == FieldOneToOne || field.Kind == FieldManyToOne) && field.UsesExistingColumn) {
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
		if isForeignKeyKind(field.Kind) && (field.Behavior == nil || !field.Behavior.NoConstraint) {
			referenceColumn := field.ReferenceStorageName
			if referenceColumn == "" {
				referenceColumn = "id"
			}
			onDelete := field.DeleteBehavior
			if onDelete == "" {
				onDelete = DeleteSetNull
				if field.Required {
					onDelete = DeleteRestrict
				}
			}
			fkName := generatedDatabaseObjectName("fk", spec.EntityName, field.StorageName)
			columns := []string{field.StorageName}
			referenceColumns := []string{referenceColumn}
			var storedColumns, storedReferenceColumns []string
			if field.Kind == FieldHierarchy && field.HierarchyVersionAware {
				entity.Columns["parent_version_id"] = Column{Name: "parent_version_id", SQLType: "BINARY(16)"}
				columns = append(columns, "parent_version_id")
				referenceColumns = append(referenceColumns, "version_id")
				storedColumns = append([]string(nil), columns...)
				storedReferenceColumns = append([]string(nil), referenceColumns...)
			} else if versionField, found := referenceVersions[field.TargetDefinitionClass]; found {
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
			indexName := generatedDatabaseObjectName("idx", spec.EntityName, field.StorageName)
			entity.Indexes[indexName] = Index{
				Name: indexName, Columns: append([]string(nil), columns...),
			}
		}
	}
	for _, index := range spec.Indexes {
		if index.Translation {
			continue
		}
		entity.Indexes[index.Name] = Index{
			Name: index.Name, Unique: index.Kind == IndexUnique,
			Columns: append([]string(nil), index.Columns...),
		}
	}
	return entity, nil
}

func schemaDefinitionFields(spec EntitySpec) []FieldSpec {
	if spec.DefinitionBehavior == nil {
		return spec.Fields
	}
	behavior := spec.DefinitionBehavior
	capacity := len(spec.Fields)
	if behavior.OverrideDefaultFields {
		capacity += len(behavior.DefaultFields)
	}
	if behavior.OverrideBaseFields {
		capacity += len(behavior.BaseFields)
	}
	if capacity == len(spec.Fields) {
		return spec.Fields
	}
	fields := make([]FieldSpec, 0, capacity)
	if behavior.OverrideDefaultFields {
		fields = append(fields, behavior.DefaultFields...)
	}
	fields = append(fields, spec.Fields...)
	if behavior.OverrideBaseFields {
		fields = append(fields, behavior.BaseFields...)
	}
	return fields
}

func defaultTimestampColumns() map[string]Column {
	return map[string]Column{
		"created_at": {Name: "created_at", SQLType: "DATETIME(3)", NotNull: true, BackfillSQL: "CURRENT_TIMESTAMP(3)"},
		"updated_at": {Name: "updated_at", SQLType: "DATETIME(3)"},
	}
}

// SchemaEntitiesFromSpec returns the parent table and, when enabled, its
// translation table. Callers that mutate a complete schema should use this
// helper so the two DAL definitions cannot drift apart.
func SchemaEntitiesFromSpec(spec EntitySpec) ([]Entity, error) {
	spec = CompleteSpec(spec)
	if spec.DefinitionKind == DefinitionBulkExtension {
		result := make([]Entity, 0, len(spec.BulkExtensions))
		for _, target := range spec.BulkExtensions {
			entity, err := SchemaFromSpec(bulkTargetEntitySpec(spec, target))
			if err != nil {
				return nil, err
			}
			result = append(result, entity)
		}
		return result, nil
	}
	if spec.DefinitionKind != DefinitionEntity && spec.Translation != nil && spec.Translation.Enabled {
		return nil, fmt.Errorf("%s definitions cannot own translation bundles", spec.DefinitionKind)
	}
	parent, err := SchemaFromSpec(spec)
	if err != nil {
		return nil, err
	}
	result := []Entity{parent}
	if spec.Translation == nil || !spec.Translation.Enabled {
		return result, nil
	}
	translation, err := translationSchemaFromSpec(spec)
	if err != nil {
		return nil, err
	}
	return append(result, translation), nil
}

func bulkTargetEntitySpec(owner EntitySpec, target BulkExtensionTargetSpec) EntitySpec {
	return CompleteSpec(EntitySpec{
		Mode: "edit", DefinitionKind: DefinitionExtension,
		Namespace: owner.Namespace, ClassName: owner.ClassName,
		DefinitionClass: owner.DefinitionClass, ShopwareVersion: owner.ShopwareVersion,
		EntityName: target.EntityName, ExtendedDefinitionClass: target.ExtendedDefinitionClass,
		ExtendedFields: target.ExtendedFields, Fields: target.Fields, Indexes: target.Indexes,
		CreateMigration: owner.CreateMigration,
	})
}

// MergeSpecSchema adds the database objects owned by spec to schema. Multiple
// EntityExtension classes may contribute disjoint columns and associations to
// the same external table, so those partial entities are merged by object name.
func MergeSpecSchema(schema *Schema, spec EntitySpec) error {
	entities, err := SchemaEntitiesFromSpec(spec)
	if err != nil {
		return err
	}
	for _, entity := range entities {
		if err := mergeSchemaEntity(schema, entity); err != nil {
			return err
		}
	}
	return nil
}

// ReplaceSpecSchema swaps one class's owned database contribution without
// removing fields contributed by another EntityExtension for the same table.
func ReplaceSpecSchema(schema *Schema, previous *EntitySpec, next EntitySpec) error {
	if previous != nil {
		entities, err := SchemaEntitiesFromSpec(*previous)
		if err != nil {
			return err
		}
		for _, entity := range entities {
			removeSchemaEntityContribution(schema, entity)
		}
	}
	return MergeSpecSchema(schema, next)
}

func mergeSchemaEntity(schema *Schema, incoming Entity) error {
	if schema.Entities == nil {
		schema.Entities = make(map[string]Entity)
	}
	existing, found := schema.Entities[incoming.Name]
	if !found {
		schema.Entities[incoming.Name] = incoming
		return nil
	}
	if !existing.External && !incoming.External {
		if sameSchemaEntity(existing, incoming) {
			return nil
		}
		return fmt.Errorf("entity %s is defined more than once", incoming.Name)
	}
	if existing.External && !incoming.External {
		incoming, existing = existing, incoming
	}
	existing.External = existing.External && incoming.External
	for name, column := range incoming.Columns {
		if current, duplicate := existing.Columns[name]; duplicate && current != column {
			return fmt.Errorf("entity %s column %s is extended with conflicting definitions", incoming.Name, name)
		}
		existing.Columns[name] = column
	}
	for name, index := range incoming.Indexes {
		if current, duplicate := existing.Indexes[name]; duplicate && !sameIndex(current, index) {
			return fmt.Errorf("entity %s index %s is extended with conflicting definitions", incoming.Name, name)
		}
		existing.Indexes[name] = index
	}
	for name, foreignKey := range incoming.ForeignKeys {
		if current, duplicate := existing.ForeignKeys[name]; duplicate && !sameForeignKey(current, foreignKey) {
			return fmt.Errorf("entity %s foreign key %s is extended with conflicting definitions", incoming.Name, name)
		}
		existing.ForeignKeys[name] = foreignKey
	}
	schema.Entities[incoming.Name] = existing
	return nil
}

func sameSchemaEntity(left, right Entity) bool {
	if left.Name != right.Name || len(left.Columns) != len(right.Columns) ||
		len(left.Indexes) != len(right.Indexes) || len(left.ForeignKeys) != len(right.ForeignKeys) {
		return false
	}
	for name, column := range left.Columns {
		other, found := right.Columns[name]
		if !found || !sameDatabaseColumn(column, other) {
			return false
		}
	}
	for name, index := range left.Indexes {
		other, found := right.Indexes[name]
		if !found || !sameIndex(index, other) {
			return false
		}
	}
	for name, foreignKey := range left.ForeignKeys {
		other, found := right.ForeignKeys[name]
		if !found || !sameForeignKey(foreignKey, other) {
			return false
		}
	}
	return true
}

func removeSchemaEntityContribution(schema *Schema, contribution Entity) {
	existing, found := schema.Entities[contribution.Name]
	if !found {
		return
	}
	if !contribution.External {
		delete(schema.Entities, contribution.Name)
		return
	}
	for name := range contribution.Columns {
		delete(existing.Columns, name)
	}
	for name := range contribution.Indexes {
		delete(existing.Indexes, name)
	}
	for name := range contribution.ForeignKeys {
		delete(existing.ForeignKeys, name)
	}
	if len(existing.Columns) == 0 && len(existing.Indexes) == 0 && len(existing.ForeignKeys) == 0 {
		delete(schema.Entities, contribution.Name)
		return
	}
	schema.Entities[contribution.Name] = existing
}

func translationSchemaFromSpec(spec EntitySpec) (Entity, error) {
	translation := spec.Translation
	if translation == nil {
		return Entity{}, fmt.Errorf("translation metadata is missing")
	}
	columns := make(map[string]Column)
	if translation.DefinitionBehavior == nil || !translation.DefinitionBehavior.OverrideDefaultFields {
		columns = defaultTimestampColumns()
	}
	entity := Entity{
		Name:        translation.EntityName,
		Columns:     columns,
		Indexes:     make(map[string]Index),
		ForeignKeys: make(map[string]ForeignKey),
	}
	var behaviorFields []FieldSpec
	if translation.DefinitionBehavior != nil {
		if translation.DefinitionBehavior.OverrideDefaultFields {
			behaviorFields = append(behaviorFields, translation.DefinitionBehavior.DefaultFields...)
		}
		if translation.DefinitionBehavior.OverrideBaseFields {
			behaviorFields = append(behaviorFields, translation.DefinitionBehavior.BaseFields...)
		}
	}
	if len(behaviorFields) != 0 {
		contribution, contributionErr := SchemaFromSpec(EntitySpec{
			DefinitionKind: DefinitionMapping,
			EntityName:     translation.EntityName,
			Fields:         behaviorFields,
		})
		if contributionErr != nil {
			return Entity{}, fmt.Errorf("translation behavior fields: %w", contributionErr)
		}
		for name, column := range contribution.Columns {
			entity.Columns[name] = column
		}
		for name, index := range contribution.Indexes {
			entity.Indexes[name] = index
		}
		for name, foreignKey := range contribution.ForeignKeys {
			entity.ForeignKeys[name] = foreignKey
		}
	}
	parentColumn := translation.ParentStorageName
	versionColumn := ""
	overridesBaseFields := translation.DefinitionBehavior != nil && translation.DefinitionBehavior.OverrideBaseFields
	if !overridesBaseFields {
		for _, field := range schemaDefinitionFields(spec) {
			if field.Kind == FieldVersion {
				versionColumn = strings.TrimSuffix(parentColumn, "_id") + "_version_id"
				break
			}
		}
		entity.Columns[parentColumn] = Column{Name: parentColumn, SQLType: "BINARY(16)", NotNull: true, PrimaryKey: true}
		if versionColumn != "" {
			entity.Columns[versionColumn] = Column{Name: versionColumn, SQLType: "BINARY(16)", NotNull: true, PrimaryKey: true}
		}
		entity.Columns["language_id"] = Column{Name: "language_id", SQLType: "BINARY(16)", NotNull: true, PrimaryKey: true}
	}
	for _, field := range spec.Fields {
		if !field.Translated || field.Kind == FieldLocked {
			continue
		}
		if field.Behavior != nil && field.Behavior.Runtime {
			continue
		}
		if field.StorageName == "" {
			return Entity{}, fmt.Errorf("translated field %q has no storage name", field.PropertyName)
		}
		sqlType, typeErr := SQLType(field)
		if typeErr != nil {
			return Entity{}, typeErr
		}
		entity.Columns[field.StorageName] = Column{
			Name:        field.StorageName,
			SQLType:     sqlType,
			NotNull:     field.Required,
			BackfillSQL: strings.TrimSpace(field.MigrationDefault),
		}
	}
	for _, index := range spec.Indexes {
		if !index.Translation {
			continue
		}
		entity.Indexes[index.Name] = Index{
			Name: index.Name, Unique: index.Kind == IndexUnique,
			Columns: append([]string(nil), index.Columns...),
		}
	}
	if !overridesBaseFields {
		parentFKName := generatedDatabaseObjectName("fk", entity.Name, parentColumn)
		parentColumns := []string{parentColumn}
		parentReferenceColumns := []string{"id"}
		storedParentColumns := []string(nil)
		storedParentReferenceColumns := []string(nil)
		if versionColumn != "" {
			parentColumns = append(parentColumns, versionColumn)
			parentReferenceColumns = append(parentReferenceColumns, "version_id")
			storedParentColumns = append([]string(nil), parentColumns...)
			storedParentReferenceColumns = append([]string(nil), parentReferenceColumns...)
		}
		entity.ForeignKeys[parentFKName] = ForeignKey{
			Name: parentFKName, Column: parentColumn, ReferenceEntity: spec.EntityName, ReferenceColumn: "id",
			Columns: storedParentColumns, ReferenceColumns: storedParentReferenceColumns,
			OnDelete: DeleteCascade, OnUpdate: "cascade",
		}
		languageFKName := generatedDatabaseObjectName("fk", entity.Name, "language_id")
		entity.ForeignKeys[languageFKName] = ForeignKey{
			Name: languageFKName, Column: "language_id", ReferenceEntity: "language", ReferenceColumn: "id",
			OnDelete: DeleteCascade, OnUpdate: "cascade",
		}
	}
	return entity, nil
}

// IndexSpecsFromEntity restores only designer-owned indexes. Relation indexes
// are structural and are regenerated from their logical relation rows.
func IndexSpecsFromEntity(spec EntitySpec, entity Entity) []IndexSpec {
	automatic := make(map[string]Index)
	referenceVersions := make(map[string]string)
	fields := schemaDefinitionFields(spec)
	for _, field := range fields {
		if field.Kind == FieldReferenceVersion && field.TargetDefinitionClass != "" {
			referenceVersions[field.TargetDefinitionClass] = field.StorageName
		}
	}
	for _, field := range fields {
		if !isForeignKeyKind(field.Kind) || field.StorageName == "" || field.UsesExistingColumn {
			continue
		}
		name := generatedDatabaseObjectName("idx", spec.EntityName, field.StorageName)
		columns := []string{field.StorageName}
		if field.Kind == FieldHierarchy && field.HierarchyVersionAware {
			columns = append(columns, "parent_version_id")
		} else if versionColumn := referenceVersions[field.TargetDefinitionClass]; versionColumn != "" {
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

// IndexSpecsFromEntities restores parent- and translation-table indexes from
// a committed schema snapshot without conflating their independently scoped
// database names.
func IndexSpecsFromEntities(spec EntitySpec, schema Schema) []IndexSpec {
	var result []IndexSpec
	if parent, found := schema.Entities[spec.EntityName]; found {
		parentIndexes := IndexSpecsFromEntity(spec, parent)
		if spec.DefinitionKind == DefinitionExtension {
			ownedColumns := extensionOwnedColumns(spec)
			parentIndexes = slicesMatchingOwnedColumns(parentIndexes, ownedColumns)
		}
		result = append(result, parentIndexes...)
	}
	if spec.Translation != nil && spec.Translation.Enabled {
		if translation, found := schema.Entities[spec.Translation.EntityName]; found {
			for name, index := range translation.Indexes {
				if relationIndex(translation, index) {
					continue
				}
				kind := IndexNormal
				if index.Unique {
					kind = IndexUnique
				}
				result = append(result, IndexSpec{
					Name: name, Kind: kind, Translation: true,
					Columns: append([]string(nil), index.Columns...),
				})
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Translation != result[j].Translation {
			return !result[i].Translation
		}
		return result[i].Name < result[j].Name
	})
	return result
}

// RestoreSpecIndexesFromEntities restores snapshot-only indexes for either a
// single definition/extension or every target of a BulkEntityExtension.
func RestoreSpecIndexesFromEntities(spec *EntitySpec, schema Schema) {
	if spec == nil {
		return
	}
	if spec.DefinitionKind != DefinitionBulkExtension {
		spec.Indexes = IndexSpecsFromEntities(*spec, schema)
		return
	}
	for index := range spec.BulkExtensions {
		targetSpec := bulkTargetEntitySpec(*spec, spec.BulkExtensions[index])
		spec.BulkExtensions[index].Indexes = IndexSpecsFromEntities(targetSpec, schema)
	}
}

func extensionOwnedColumns(spec EntitySpec) map[string]struct{} {
	owned := make(map[string]struct{})
	for _, field := range spec.Fields {
		if field.Kind == FieldLocked || field.Translated || field.StorageName == "" ||
			field.Behavior != nil && field.Behavior.Runtime || field.Kind == FieldOneToMany ||
			field.Kind == FieldManyToMany ||
			(field.Kind == FieldOneToOne || field.Kind == FieldManyToOne) && field.UsesExistingColumn {
			continue
		}
		owned[field.StorageName] = struct{}{}
	}
	return owned
}

func slicesMatchingOwnedColumns(indexes []IndexSpec, owned map[string]struct{}) []IndexSpec {
	result := make([]IndexSpec, 0, len(indexes))
	for _, index := range indexes {
		for _, column := range index.Columns {
			if _, found := owned[column]; found {
				result = append(result, index)
				break
			}
		}
	}
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
		if index.Name == generatedDatabaseObjectName("idx", entity.Name, foreignKey.Column) &&
			!index.Unique && sameStrings(index.Columns, foreignKeyColumns(foreignKey)) {
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
	case FieldID, FieldBinaryID, FieldVersion, FieldReferenceVersion, FieldForeignKey, FieldManyToOne, FieldOneToOne, FieldHierarchy:
		return "BINARY(16)", nil
	case FieldString:
		length := field.MaxLength
		if length <= 0 {
			length = 255
		}
		return fmt.Sprintf("VARCHAR(%d)", length), nil
	case FieldEnum:
		if field.EnumBackingType == "int" {
			return "INT", nil
		}
		if field.EnumBackingType == "string" {
			return "VARCHAR(255)", nil
		}
		return "", fmt.Errorf("enum field %q has no backing type", field.PropertyName)
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
	return kind == FieldForeignKey || kind == FieldManyToOne || kind == FieldOneToOne || kind == FieldHierarchy
}

func generatedDatabaseObjectName(prefix, entity, column string) string {
	name := prefix + "." + entity + "." + column
	if len(name) <= 64 {
		return name
	}
	digest := sha256.Sum256([]byte(name))
	suffix := hex.EncodeToString(digest[:6])
	return name[:64-len(suffix)-1] + "." + suffix
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
