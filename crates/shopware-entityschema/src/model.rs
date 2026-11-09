//! Normalized DAL entity model.
//!
//! The same model drives previews, migrations, committed snapshots and drift
//! detection. Enumerations are kept as strings so unknown client values survive
//! until validation reports them.

use std::collections::{BTreeMap, HashMap, HashSet};
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use crate::canonical_json::{
    integral_f64, integral_opt_f64, is_false, is_zero_f64, is_zero_i64, null_if_empty, nullable,
};
use crate::render::complete_spec;

pub type FieldKind = String;
pub type DefinitionKind = String;
pub type DeleteBehavior = String;
pub type IndexKind = String;
pub type FieldFlagKind = String;

pub const DEFINITION_ENTITY: &str = "entity";
pub const DEFINITION_MAPPING: &str = "mapping";
pub const DEFINITION_EXTENSION: &str = "extension";
pub const DEFINITION_BULK_EXTENSION: &str = "bulk-extension";

pub const FIELD_ID: &str = "id";
pub const FIELD_BINARY_ID: &str = "binary-id";
pub const FIELD_STRING: &str = "string";
pub const FIELD_ENUM: &str = "enum";
pub const FIELD_LONG_TEXT: &str = "long-text";
pub const FIELD_INT: &str = "int";
pub const FIELD_FLOAT: &str = "float";
pub const FIELD_BOOL: &str = "bool";
pub const FIELD_DATE: &str = "date";
pub const FIELD_DATE_TIME: &str = "datetime";
pub const FIELD_JSON: &str = "json";
pub const FIELD_LIST: &str = "list";
pub const FIELD_OBJECT: &str = "object";
pub const FIELD_BLOB: &str = "blob";
pub const FIELD_AUTO_INCREMENT: &str = "auto-increment";
pub const FIELD_CREATED_AT: &str = "created-at";
pub const FIELD_UPDATED_AT: &str = "updated-at";
pub const FIELD_VERSION: &str = "version";
pub const FIELD_REFERENCE_VERSION: &str = "reference-version";
pub const FIELD_FOREIGN_KEY: &str = "foreign-key";
pub const FIELD_MANY_TO_ONE: &str = "many-to-one";
pub const FIELD_ONE_TO_ONE: &str = "one-to-one";
pub const FIELD_ONE_TO_MANY: &str = "one-to-many";
pub const FIELD_MANY_TO_MANY: &str = "many-to-many";
pub const FIELD_HIERARCHY: &str = "hierarchy";
pub const FIELD_LOCKED: &str = "locked";

pub const DELETE_RESTRICT: &str = "restrict";
pub const DELETE_CASCADE: &str = "cascade";
pub const DELETE_SET_NULL: &str = "set-null";

pub const INDEX_NORMAL: &str = "index";
pub const INDEX_UNIQUE: &str = "unique";

pub const FLAG_REQUIRED: &str = "required";
pub const FLAG_PRIMARY_KEY: &str = "primary-key";
pub const FLAG_API_AWARE: &str = "api-aware";
pub const FLAG_SEARCH_RANKING: &str = "search-ranking";
pub const FLAG_CASCADE_DELETE: &str = "cascade-delete";
pub const FLAG_SET_NULL_ON_DELETE: &str = "set-null-on-delete";
pub const FLAG_RESTRICT_DELETE: &str = "restrict-delete";
pub const FLAG_RUNTIME: &str = "runtime";
pub const FLAG_COMPUTED: &str = "computed";
pub const FLAG_NO_CONSTRAINT: &str = "no-constraint";
pub const FLAG_INHERITED: &str = "inherited";
pub const FLAG_REVERSE_INHERITED: &str = "reverse-inherited";
pub const FLAG_WRITE_PROTECTED: &str = "write-protected";
pub const FLAG_ALLOW_HTML: &str = "allow-html";
pub const FLAG_ALLOW_EMPTY_STRING: &str = "allow-empty-string";
pub const FLAG_AS_ARRAY: &str = "as-array";
pub const FLAG_IMMUTABLE: &str = "immutable";
pub const FLAG_SINCE: &str = "since";
pub const FLAG_DEPRECATED: &str = "deprecated";
pub const FLAG_IGNORE_IN_OPEN_API_SCHEMA: &str = "ignore-in-openapi-schema";
pub const FLAG_IGNORE_IN_UNUSED_MEDIA_SEARCH: &str = "ignore-in-unused-media-search";
pub const FLAG_API_CRITERIA_AWARE: &str = "api-criteria-aware";
pub const FLAG_RULE_AREAS: &str = "rule-areas";
pub const FLAG_CHOICE: &str = "choice";
pub const FLAG_DO_NOT_USE_CONTEXT: &str = "do-not-use-context";
pub const FLAG_EXTENSION: &str = "extension";

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct BulkExtensionTargetSpec {
    pub id: String,
    pub entity_name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub extended_definition_class: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub extended_fields: Vec<RelationTargetField>,
    #[serde(deserialize_with = "nullable", serialize_with = "null_if_empty")]
    pub fields: Vec<FieldSpec>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub indexes: Vec<IndexSpec>,
}

/// DAL flags which change how a field participates in persistence.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct FieldBehavior {
    #[serde(skip_serializing_if = "is_false")]
    pub runtime: bool,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub runtime_dependencies: Vec<String>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub runtime_dependencies_expression: String,
    #[serde(skip_serializing_if = "is_false")]
    pub computed: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub no_constraint: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct FieldImplementation {
    pub class: String,
    pub constructor_mode: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub additional_arguments: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub fixed_arguments: Vec<String>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub fixed_storage_name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub fixed_property_name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub entity_type: String,
    #[serde(skip_serializing_if = "is_false")]
    pub entity_boolean_getter: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub entity_trait: String,
    #[serde(skip_serializing_if = "is_false")]
    pub manage_entity: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub implicit_computed: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub max_length_argument: bool,
    #[serde(skip_serializing_if = "is_zero_i64")]
    pub minimum_additional_arguments: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct ConditionalAssociation {
    pub condition_expression: String,
    pub alternative_kind: FieldKind,
    #[serde(skip_serializing_if = "is_false")]
    pub alternative_autoload: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct FieldMetadata {
    #[serde(rename = "allowHtmlSanitized", skip_serializing_if = "Option::is_none")]
    pub allow_html: Option<bool>,
    #[serde(skip_serializing_if = "is_false")]
    pub allow_empty_string: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub as_array: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub immutable: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub since: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub deprecated: Option<Deprecation>,
    #[serde(rename = "ignoreInOpenapiSchema", skip_serializing_if = "is_false")]
    pub ignore_in_open_api_schema: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub ignore_in_unused_media_search: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub api_criteria_aware: bool,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub rule_areas: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub choice: Option<ChoiceSpec>,
    #[serde(skip_serializing_if = "is_false")]
    pub do_not_use_context: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub extension: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct Deprecation {
    pub deprecated_since: String,
    pub will_be_removed_in: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub replaced_by: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct ChoiceSpec {
    #[serde(deserialize_with = "nullable", serialize_with = "null_if_empty")]
    pub values: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub strict: Option<bool>,
}

/// A serializable representation of every DAL flag the class-based field
/// model understands. Only arguments belonging to `kind` are valid.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct FieldFlagSpec {
    pub kind: FieldFlagKind,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub api_sources: Vec<String>,
    #[serde(skip_serializing_if = "is_zero_f64", serialize_with = "integral_f64")]
    pub search_ranking: f64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub search_tokenize: Option<bool>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub runtime_dependencies: Vec<String>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub runtime_dependencies_expression: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub inherited_foreign_key: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub reverse_property: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub write_scopes: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub allow_html_sanitized: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub clone_relevant: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub enforced_by_constraint: Option<bool>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub since: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub deprecated: Option<Deprecation>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub rule_areas: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub choice: Option<ChoiceSpec>,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct FieldModificationSpec {
    pub id: String,
    pub property_name: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub add_flags: Vec<FieldFlagSpec>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub remove_flags: Vec<FieldFlagKind>,
}

/// The editable designer representation of one field. Relation rows are
/// logical: a many-to-one row renders both an FkField and its association.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct FieldSpec {
    pub id: String,
    pub kind: FieldKind,
    pub property_name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub foreign_key_property_name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub storage_name: String,
    #[serde(skip_serializing_if = "is_false")]
    pub required: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub primary: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub api_aware: bool,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub api_aware_sources: Vec<String>,
    #[serde(skip_serializing_if = "is_zero_f64", serialize_with = "integral_f64")]
    pub search_ranking: f64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub search_ranking_tokenize: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub behavior: Option<FieldBehavior>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub implementation: Option<FieldImplementation>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub metadata: Option<FieldMetadata>,
    #[serde(skip_serializing_if = "is_false")]
    pub write_protected: bool,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub write_protected_scopes: Vec<String>,
    #[serde(skip_serializing_if = "is_false")]
    pub inherited: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub inherited_foreign_key: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub preserved_flags: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub modifiers_before_flags: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub modifiers_after_flags: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub association_flags: Vec<String>,
    #[serde(
        rename = "associationModifiersBeforeFlags",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub association_before_flags: Vec<String>,
    #[serde(
        rename = "associationModifiersAfterFlags",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub association_after_flags: Vec<String>,
    #[serde(skip_serializing_if = "is_false")]
    pub association_api_aware: bool,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub association_api_aware_sources: Vec<String>,
    #[serde(
        rename = "associationSearchRanking",
        skip_serializing_if = "is_zero_f64",
        serialize_with = "integral_f64"
    )]
    pub association_search_rank: f64,
    #[serde(
        rename = "associationSearchRankingTokenize",
        skip_serializing_if = "Option::is_none"
    )]
    pub association_search_tokenize: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub association_behavior: Option<FieldBehavior>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub association_metadata: Option<FieldMetadata>,
    #[serde(skip_serializing_if = "is_false")]
    pub association_autoload: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub conditional_association: Option<ConditionalAssociation>,
    #[serde(skip_serializing_if = "is_false")]
    pub association_write_protected: bool,
    #[serde(
        rename = "associationWriteProtectedScopes",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub association_write_scopes: Vec<String>,
    #[serde(skip_serializing_if = "is_false")]
    pub association_inherited: bool,
    #[serde(
        rename = "associationInheritedForeignKey",
        skip_serializing_if = "String::is_empty"
    )]
    pub association_inherited_fk: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub reverse_inherited_property: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub hierarchy_parent_property: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub hierarchy_children_flags: Vec<String>,
    #[serde(
        rename = "hierarchyChildrenModifiersBeforeFlags",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub hierarchy_children_before: Vec<String>,
    #[serde(
        rename = "hierarchyChildrenModifiersAfterFlags",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub hierarchy_children_after: Vec<String>,
    #[serde(skip_serializing_if = "is_false")]
    pub hierarchy_children_api_aware: bool,
    #[serde(
        rename = "hierarchyChildrenApiAwareSources",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub hierarchy_children_api_sources: Vec<String>,
    #[serde(
        rename = "hierarchyChildrenSearchRanking",
        skip_serializing_if = "is_zero_f64",
        serialize_with = "integral_f64"
    )]
    pub hierarchy_children_rank: f64,
    #[serde(
        rename = "hierarchyChildrenSearchRankingTokenize",
        skip_serializing_if = "Option::is_none"
    )]
    pub hierarchy_children_tokenize: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub hierarchy_children_behavior: Option<FieldBehavior>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub hierarchy_children_metadata: Option<FieldMetadata>,
    #[serde(
        rename = "hierarchyChildrenWriteProtected",
        skip_serializing_if = "is_false"
    )]
    pub hierarchy_children_protected: bool,
    #[serde(
        rename = "hierarchyChildrenWriteProtectedScopes",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub hierarchy_children_write_scopes: Vec<String>,
    #[serde(skip_serializing_if = "is_false")]
    pub hierarchy_children_inherited: bool,
    #[serde(
        rename = "hierarchyChildrenInheritedForeignKey",
        skip_serializing_if = "String::is_empty"
    )]
    pub hierarchy_children_inherited_fk: String,
    #[serde(
        rename = "hierarchyChildrenReverseInheritedProperty",
        skip_serializing_if = "String::is_empty"
    )]
    pub hierarchy_children_reverse: String,
    #[serde(skip_serializing_if = "is_false")]
    pub hierarchy_version_aware: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub hierarchy_version_api_aware: bool,
    #[serde(
        rename = "hierarchyVersionApiAwareSources",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub hierarchy_version_api_sources: Vec<String>,
    #[serde(
        rename = "hierarchyVersionWriteProtected",
        skip_serializing_if = "is_false"
    )]
    pub hierarchy_version_protected: bool,
    #[serde(
        rename = "hierarchyVersionWriteProtectedScopes",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub hierarchy_version_write_scopes: Vec<String>,
    #[serde(skip_serializing_if = "is_false")]
    pub hierarchy_version_inherited: bool,
    #[serde(
        rename = "hierarchyVersionInheritedForeignKey",
        skip_serializing_if = "String::is_empty"
    )]
    pub hierarchy_version_inherited_fk: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub hierarchy_version_flags: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub hierarchy_version_behavior: Option<FieldBehavior>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub hierarchy_version_metadata: Option<FieldMetadata>,
    #[serde(
        rename = "hierarchyVersionModifiersBeforeFlags",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub hierarchy_version_before: Vec<String>,
    #[serde(
        rename = "hierarchyVersionModifiersAfterFlags",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub hierarchy_version_after: Vec<String>,
    #[serde(skip_serializing_if = "is_zero_i64")]
    pub max_length: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub min: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub max: Option<i64>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub element_type_class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub json_property_mapping_expression: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub json_default_expression: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub enum_class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub enum_case: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub enum_backing_type: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub target_definition_class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub target_entity_class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub target_collection_class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub target_entity_name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub reference_field: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub reference_storage_name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub mapping_definition_class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub mapping_local_column: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub mapping_reference_column: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub source_column: String,
    #[serde(skip_serializing_if = "is_false")]
    pub uses_existing_column: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub delete_behavior: DeleteBehavior,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub delete_clone_relevant: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub delete_enforced_by_constraint: Option<bool>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub migration_default: String,
    #[serde(skip_serializing_if = "is_false")]
    pub translated: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub translation_definition_only: bool,
    #[serde(rename = "translationUseForSorting", skip_serializing_if = "is_false")]
    pub translation_use_for_sort: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub translation_api_aware: Option<bool>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub translation_api_aware_sources: Vec<String>,
    #[serde(
        rename = "translationSearchRanking",
        skip_serializing_if = "Option::is_none",
        serialize_with = "integral_opt_f64"
    )]
    pub translation_search_rank: Option<f64>,
    #[serde(
        rename = "translationSearchRankingTokenize",
        skip_serializing_if = "Option::is_none"
    )]
    pub translation_search_tokenize: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub translation_behavior: Option<FieldBehavior>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub translation_metadata: Option<FieldMetadata>,
    #[serde(skip_serializing_if = "is_false")]
    pub translation_write_protected: bool,
    #[serde(
        rename = "translationWriteProtectedScopes",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub translation_write_scopes: Vec<String>,
    #[serde(skip_serializing_if = "is_false")]
    pub translation_inherited: bool,
    #[serde(
        rename = "translationInheritedForeignKey",
        skip_serializing_if = "String::is_empty"
    )]
    pub translation_inherited_fk: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub translation_flags: Vec<String>,
    #[serde(
        rename = "translationModifiersBeforeFlags",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub translation_before_flags: Vec<String>,
    #[serde(
        rename = "translationModifiersAfterFlags",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub translation_after_flags: Vec<String>,
    pub editable: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub raw: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct IndexSpec {
    pub name: String,
    pub kind: IndexKind,
    #[serde(deserialize_with = "nullable", serialize_with = "null_if_empty")]
    pub columns: Vec<String>,
    #[serde(skip_serializing_if = "is_false")]
    pub translation: bool,
}

/// The companion DAL translation bundle owned by a parent entity.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct TranslationSpec {
    pub enabled: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub entity_name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub definition_class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub entity_class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub collection_class: String,
    #[serde(rename = "definitionUri", skip_serializing_if = "String::is_empty")]
    pub definition_uri: String,
    #[serde(rename = "entityUri", skip_serializing_if = "String::is_empty")]
    pub entity_uri: String,
    #[serde(rename = "collectionUri", skip_serializing_if = "String::is_empty")]
    pub collection_uri: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub parent_definition_class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub parent_storage_name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub parent_property_name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub association_property: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub association_local_field: String,
    #[serde(skip_serializing_if = "is_false")]
    pub association_required: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub association_api_aware: bool,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub association_api_aware_sources: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub association_behavior: Option<FieldBehavior>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub association_metadata: Option<FieldMetadata>,
    #[serde(skip_serializing_if = "is_false")]
    pub association_write_protected: bool,
    #[serde(
        rename = "associationWriteProtectedScopes",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub association_write_scopes: Vec<String>,
    #[serde(skip_serializing_if = "is_false")]
    pub association_inherited: bool,
    #[serde(
        rename = "associationInheritedForeignKey",
        skip_serializing_if = "String::is_empty"
    )]
    pub association_inherited_fk: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub reverse_inherited_property: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub association_flags: Vec<String>,
    #[serde(
        rename = "associationModifiersBeforeFlags",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub association_before_flags: Vec<String>,
    #[serde(
        rename = "associationModifiersAfterFlags",
        skip_serializing_if = "Vec::is_empty"
    )]
    pub association_after_flags: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub definition_behavior: Option<DefinitionBehaviorSpec>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub definition_metadata: Option<DefinitionMetadataSpec>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct DefinitionDefaultSpec {
    pub property_name: String,
    pub value_expression: String,
}

/// Common class-based EntityDefinition hooks that are safe to author
/// structurally. Arbitrary method bodies stay immutable raw methods.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct DefinitionMetadataSpec {
    #[serde(skip_serializing_if = "String::is_empty")]
    pub since: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub defaults: Vec<DefinitionDefaultSpec>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub child_defaults: Vec<DefinitionDefaultSpec>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub hydrator_class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub since_method_raw: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub defaults_method_raw: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub child_defaults_method_raw: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub hydrator_method_raw: String,
}

/// Class-level EntityDefinition behavior not expressed by defineFields.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct DefinitionBehaviorSpec {
    #[serde(skip_serializing_if = "String::is_empty")]
    pub parent_definition_class: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub version_aware: Option<bool>,
    #[serde(skip_serializing_if = "is_false")]
    pub override_default_fields: bool,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub default_fields: Vec<FieldSpec>,
    #[serde(skip_serializing_if = "is_false")]
    pub override_base_fields: bool,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub base_fields: Vec<FieldSpec>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub restrict_delete_meta_properties: Vec<String>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub parent_definition_method_raw: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub version_aware_method_raw: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub inheritance_aware_method_raw: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub default_fields_method_raw: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub base_fields_method_raw: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub restrict_delete_meta_method_raw: String,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct EntitySpec {
    pub mode: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub definition_kind: DefinitionKind,
    #[serde(rename = "pluginRootUri")]
    pub plugin_root_uri: String,
    #[serde(rename = "directoryUri")]
    pub directory_uri: String,
    pub namespace: String,
    pub class_name: String,
    pub entity_name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub extended_definition_class: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub extended_fields: Vec<RelationTargetField>,
    #[serde(skip_serializing_if = "is_false")]
    pub inheritance_aware: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub definition_behavior: Option<DefinitionBehaviorSpec>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub definition_metadata: Option<DefinitionMetadataSpec>,
    #[serde(skip_serializing_if = "is_false")]
    pub read_protected: bool,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub read_protection_scopes: Vec<String>,
    #[serde(skip_serializing_if = "is_false")]
    pub write_protected: bool,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub write_protection_scopes: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub preserved_protections: Vec<String>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub protection_method_raw: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub field_modifications: Vec<FieldModificationSpec>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub modify_fields_method_raw: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub collect_method_raw: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub bulk_extensions: Vec<BulkExtensionTargetSpec>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub shopware_version: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub definition_class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub entity_class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub collection_class: String,
    #[serde(rename = "definitionUri", skip_serializing_if = "String::is_empty")]
    pub definition_uri: String,
    #[serde(rename = "entityUri", skip_serializing_if = "String::is_empty")]
    pub entity_uri: String,
    #[serde(rename = "collectionUri", skip_serializing_if = "String::is_empty")]
    pub collection_uri: String,
    #[serde(deserialize_with = "nullable")]
    pub fields: Vec<FieldSpec>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub indexes: Vec<IndexSpec>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub translation: Option<TranslationSpec>,
    #[serde(rename = "serviceUri", skip_serializing_if = "String::is_empty")]
    pub service_uri: String,
    pub create_migration: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub migration_name: String,
    #[serde(skip_serializing_if = "is_zero_i64")]
    pub migration_timestamp: i64,
    #[serde(rename = "baseSnapshotIds", skip_serializing_if = "Vec::is_empty")]
    pub base_snapshot_ids: Vec<String>,
}

/// The committed database-facing representation. Entity keys are technical
/// entity/table names and column keys are physical storage names.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Schema {
    #[serde(deserialize_with = "nullable")]
    pub entities: BTreeMap<String, Entity>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct Entity {
    pub name: String,
    #[serde(skip_serializing_if = "is_false")]
    pub external: bool,
    #[serde(deserialize_with = "nullable")]
    pub columns: BTreeMap<String, Column>,
    #[serde(deserialize_with = "nullable")]
    pub indexes: BTreeMap<String, Index>,
    #[serde(deserialize_with = "nullable")]
    pub foreign_keys: BTreeMap<String, ForeignKey>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct Column {
    pub name: String,
    pub sql_type: String,
    pub not_null: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub primary_key: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub auto_increment: bool,
    #[serde(skip)]
    pub backfill_sql: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Index {
    pub name: String,
    pub unique: bool,
    #[serde(deserialize_with = "nullable", serialize_with = "null_if_empty")]
    pub columns: Vec<String>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct ForeignKey {
    pub name: String,
    pub column: String,
    pub reference_entity: String,
    pub reference_column: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub columns: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub reference_columns: Vec<String>,
    pub on_delete: DeleteBehavior,
    pub on_update: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct RelationTarget {
    pub definition_class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub definition_kind: DefinitionKind,
    pub entity_class: String,
    pub collection_class: String,
    pub entity_name: String,
    #[serde(rename = "fileUri", skip_serializing_if = "String::is_empty")]
    pub file_uri: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub fields: Vec<RelationTargetField>,
    #[serde(skip_serializing_if = "is_false")]
    pub version_aware: bool,
    #[serde(skip_serializing_if = "is_false")]
    pub inheritance_aware: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct RelationTargetField {
    pub property_name: String,
    pub storage_name: String,
    #[serde(skip_serializing_if = "is_false")]
    pub primary: bool,
}

/// Resolves either a fully-qualified definition class or a technical entity
/// name. Entity-name resolution serves Shopware 6.7+ EntityExtensions whose
/// only mandatory target method is `getEntityName()`.
pub type RelationLookup<'a> = &'a dyn Fn(&str) -> Option<RelationTarget>;

#[derive(Clone, Debug, Default, PartialEq)]
pub struct ImportedTranslation {
    pub spec: TranslationSpec,
    pub fields: Vec<FieldSpec>,
}

/// An entity schema error. Cloneable, because import results (errors
/// included) are memoized per scan.
#[derive(Clone, Debug, thiserror::Error)]
pub enum Error {
    /// The workspace index has not finished; the catalog cannot be read yet.
    #[error("entity schema index is not ready; wait for workspace indexing to finish")]
    IndexNotReady,
    /// A schema, specification or plugin layout problem.
    #[error("{0}")]
    Message(String),
    #[error(transparent)]
    Index(Arc<swlsp_indexer::Error>),
    #[error(transparent)]
    PhpRewrite(Arc<swlsp_php_rewrite::Error>),
    #[error(transparent)]
    Rewrite(Arc<swlsp_rewrite::RewriteError>),
    #[error(transparent)]
    Io(Arc<std::io::Error>),
    #[error(transparent)]
    Json(Arc<serde_json::Error>),
}

macro_rules! from_source {
    ($($source:ty => $variant:ident),* $(,)?) => {$(
        impl From<$source> for Error {
            fn from(error: $source) -> Self {
                Error::$variant(Arc::new(error))
            }
        }
    )*};
}

from_source! {
    swlsp_indexer::Error => Index,
    swlsp_php_rewrite::Error => PhpRewrite,
    swlsp_rewrite::RewriteError => Rewrite,
    std::io::Error => Io,
    serde_json::Error => Json,
}

pub type Result<T, E = Error> = std::result::Result<T, E>;

macro_rules! errorf {
    ($($arg:tt)*) => { $crate::model::Error::Message(format!($($arg)*)) };
}
pub(crate) use errorf;

pub fn empty_schema() -> Schema {
    Schema::default()
}

impl Schema {
    /// The schema in its comparable form; schemas are already normalized
    /// on construction, so this is an owned copy.
    pub fn normalize(&self) -> Schema {
        self.clone()
    }
}

pub fn schema_from_spec(spec: &EntitySpec) -> Result<Entity> {
    let spec = complete_spec(spec.clone());
    schema_from_completed_spec(&spec)
}

fn schema_from_completed_spec(spec: &EntitySpec) -> Result<Entity> {
    if spec.definition_kind == DEFINITION_BULK_EXTENSION {
        return Err(errorf!(
            "BulkEntityExtension owns multiple partial entities; use SchemaEntitiesFromSpec"
        ));
    }
    let columns = if spec.definition_kind == DEFINITION_ENTITY
        && !spec
            .definition_behavior
            .as_ref()
            .is_some_and(|behavior| behavior.override_default_fields)
    {
        default_timestamp_columns()
    } else {
        BTreeMap::new()
    };
    let mut entity = Entity {
        name: spec.entity_name.clone(),
        external: spec.definition_kind == DEFINITION_EXTENSION,
        columns,
        ..Entity::default()
    };
    let fields = schema_definition_fields(spec);
    let mut reference_versions: HashMap<&str, &FieldSpec> = HashMap::new();
    for field in fields.iter() {
        if field.kind == FIELD_REFERENCE_VERSION && !field.target_definition_class.is_empty() {
            reference_versions.insert(&field.target_definition_class, field);
        }
    }
    for field in fields.iter() {
        if is_implicit_timestamp_field(spec, &field.kind) {
            continue;
        }
        if !field.editable && field.kind == FIELD_LOCKED {
            continue;
        }
        if field
            .behavior
            .as_ref()
            .is_some_and(|behavior| behavior.runtime)
        {
            continue;
        }
        if field.translated
            || field.kind == FIELD_ONE_TO_MANY
            || field.kind == FIELD_MANY_TO_MANY
            || ((field.kind == FIELD_ONE_TO_ONE || field.kind == FIELD_MANY_TO_ONE)
                && field.uses_existing_column)
        {
            continue;
        }
        if field.storage_name.is_empty() {
            return Err(errorf!(
                "field {:?} has no storage name",
                field.property_name
            ));
        }
        let sql_type = sql_type(field)?;
        let mut column = Column {
            name: field.storage_name.clone(),
            sql_type,
            not_null: field.required,
            backfill_sql: field.migration_default.trim().to_owned(),
            ..Column::default()
        };
        if field.primary || field.kind == FIELD_ID || field.kind == FIELD_VERSION {
            column.primary_key = true;
            column.not_null = true;
        }
        if field.kind == FIELD_AUTO_INCREMENT {
            column.auto_increment = true;
            column.not_null = true;
        }
        entity.columns.insert(column.name.clone(), column);
        if is_foreign_key_kind(&field.kind)
            && !field
                .behavior
                .as_ref()
                .is_some_and(|behavior| behavior.no_constraint)
        {
            let reference_column = if field.reference_storage_name.is_empty() {
                "id".to_owned()
            } else {
                field.reference_storage_name.clone()
            };
            let mut on_delete = field.delete_behavior.clone();
            if on_delete.is_empty() {
                on_delete = if field.required {
                    DELETE_RESTRICT.to_owned()
                } else {
                    DELETE_SET_NULL.to_owned()
                };
            }
            let fk_name =
                generated_database_object_name("fk", &spec.entity_name, &field.storage_name);
            let mut columns = vec![field.storage_name.clone()];
            let mut reference_columns = vec![reference_column.clone()];
            let mut stored_columns = Vec::new();
            let mut stored_reference_columns = Vec::new();
            if field.kind == FIELD_HIERARCHY && field.hierarchy_version_aware {
                entity.columns.insert(
                    "parent_version_id".to_owned(),
                    Column {
                        name: "parent_version_id".to_owned(),
                        sql_type: "BINARY(16)".to_owned(),
                        ..Column::default()
                    },
                );
                columns.push("parent_version_id".to_owned());
                reference_columns.push("version_id".to_owned());
                stored_columns = columns.clone();
                stored_reference_columns = reference_columns.clone();
            } else if let Some(version_field) =
                reference_versions.get(field.target_definition_class.as_str())
            {
                columns.push(version_field.storage_name.clone());
                reference_columns.push("version_id".to_owned());
                stored_columns = columns.clone();
                stored_reference_columns = reference_columns.clone();
            }
            entity.foreign_keys.insert(
                fk_name.clone(),
                ForeignKey {
                    name: fk_name,
                    column: field.storage_name.clone(),
                    reference_entity: field.target_entity_name.clone(),
                    reference_column,
                    columns: stored_columns,
                    reference_columns: stored_reference_columns,
                    on_delete,
                    on_update: "cascade".to_owned(),
                },
            );
            let index_name =
                generated_database_object_name("idx", &spec.entity_name, &field.storage_name);
            entity.indexes.insert(
                index_name.clone(),
                Index {
                    name: index_name,
                    unique: false,
                    columns,
                },
            );
        }
    }
    for index in &spec.indexes {
        if index.translation {
            continue;
        }
        entity.indexes.insert(
            index.name.clone(),
            Index {
                name: index.name.clone(),
                unique: index.kind == INDEX_UNIQUE,
                columns: index.columns.clone(),
            },
        );
    }
    Ok(entity)
}

/// Fields contributing to the definition's schema: overridden default
/// fields, the defineFields fields, then overridden base fields.
pub(crate) fn schema_definition_fields(spec: &EntitySpec) -> std::borrow::Cow<'_, [FieldSpec]> {
    let Some(behavior) = &spec.definition_behavior else {
        return std::borrow::Cow::Borrowed(&spec.fields);
    };
    let mut extra = 0;
    if behavior.override_default_fields {
        extra += behavior.default_fields.len();
    }
    if behavior.override_base_fields {
        extra += behavior.base_fields.len();
    }
    if extra == 0 {
        return std::borrow::Cow::Borrowed(&spec.fields);
    }
    let mut fields = Vec::with_capacity(spec.fields.len() + extra);
    if behavior.override_default_fields {
        fields.extend(behavior.default_fields.iter().cloned());
    }
    fields.extend(spec.fields.iter().cloned());
    if behavior.override_base_fields {
        fields.extend(behavior.base_fields.iter().cloned());
    }
    std::borrow::Cow::Owned(fields)
}

fn default_timestamp_columns() -> BTreeMap<String, Column> {
    BTreeMap::from([
        (
            "created_at".to_owned(),
            Column {
                name: "created_at".to_owned(),
                sql_type: "DATETIME(3)".to_owned(),
                not_null: true,
                backfill_sql: "CURRENT_TIMESTAMP(3)".to_owned(),
                ..Column::default()
            },
        ),
        (
            "updated_at".to_owned(),
            Column {
                name: "updated_at".to_owned(),
                sql_type: "DATETIME(3)".to_owned(),
                ..Column::default()
            },
        ),
    ])
}

/// The parent table and, when enabled, its translation table, so the two DAL
/// definitions cannot drift apart.
pub fn schema_entities_from_spec(spec: &EntitySpec) -> Result<Vec<Entity>> {
    let spec = complete_spec(spec.clone());
    if spec.definition_kind == DEFINITION_BULK_EXTENSION {
        let mut result = Vec::with_capacity(spec.bulk_extensions.len());
        for target in &spec.bulk_extensions {
            result.push(schema_from_spec(&bulk_target_entity_spec(&spec, target))?);
        }
        return Ok(result);
    }
    if spec.definition_kind != DEFINITION_ENTITY
        && spec
            .translation
            .as_ref()
            .is_some_and(|translation| translation.enabled)
    {
        return Err(errorf!(
            "{} definitions cannot own translation bundles",
            spec.definition_kind
        ));
    }
    let parent = schema_from_completed_spec(&spec)?;
    let mut result = vec![parent];
    if !spec
        .translation
        .as_ref()
        .is_some_and(|translation| translation.enabled)
    {
        return Ok(result);
    }
    result.push(translation_schema_from_spec(&spec)?);
    Ok(result)
}

pub(crate) fn bulk_target_entity_spec(
    owner: &EntitySpec,
    target: &BulkExtensionTargetSpec,
) -> EntitySpec {
    complete_spec(EntitySpec {
        mode: "edit".to_owned(),
        definition_kind: DEFINITION_EXTENSION.to_owned(),
        namespace: owner.namespace.clone(),
        class_name: owner.class_name.clone(),
        definition_class: owner.definition_class.clone(),
        shopware_version: owner.shopware_version.clone(),
        entity_name: target.entity_name.clone(),
        extended_definition_class: target.extended_definition_class.clone(),
        extended_fields: target.extended_fields.clone(),
        fields: target.fields.clone(),
        indexes: target.indexes.clone(),
        create_migration: owner.create_migration,
        ..EntitySpec::default()
    })
}

/// Adds the database objects owned by `spec`. Multiple EntityExtension
/// classes may contribute disjoint columns to the same external table.
pub fn merge_spec_schema(schema: &mut Schema, spec: &EntitySpec) -> Result<()> {
    for entity in schema_entities_from_spec(spec)? {
        merge_schema_entity(schema, entity)?;
    }
    Ok(())
}

/// Swaps one class's owned database contribution without removing fields
/// contributed by another EntityExtension for the same table.
pub fn replace_spec_schema(
    schema: &mut Schema,
    previous: Option<&EntitySpec>,
    next: &EntitySpec,
) -> Result<()> {
    if let Some(previous) = previous {
        for entity in schema_entities_from_spec(previous)? {
            remove_schema_entity_contribution(schema, &entity);
        }
    }
    merge_spec_schema(schema, next)
}

fn merge_schema_entity(schema: &mut Schema, incoming: Entity) -> Result<()> {
    let Some(existing) = schema.entities.get(&incoming.name) else {
        schema.entities.insert(incoming.name.clone(), incoming);
        return Ok(());
    };
    if !existing.external && !incoming.external {
        if same_schema_entity(existing, &incoming) {
            return Ok(());
        }
        return Err(errorf!(
            "entity {} is defined more than once",
            incoming.name
        ));
    }
    let name = incoming.name.clone();
    let (mut existing, incoming) = if existing.external && !incoming.external {
        (incoming, existing.clone())
    } else {
        (existing.clone(), incoming)
    };
    existing.external = existing.external && incoming.external;
    for (column_name, column) in incoming.columns {
        if let Some(current) = existing.columns.get(&column_name)
            && *current != column
        {
            return Err(errorf!(
                "entity {} column {} is extended with conflicting definitions",
                incoming.name,
                column_name
            ));
        }
        existing.columns.insert(column_name, column);
    }
    for (index_name, index) in incoming.indexes {
        if let Some(current) = existing.indexes.get(&index_name)
            && !same_index(current, &index)
        {
            return Err(errorf!(
                "entity {} index {} is extended with conflicting definitions",
                incoming.name,
                index_name
            ));
        }
        existing.indexes.insert(index_name, index);
    }
    for (key_name, foreign_key) in incoming.foreign_keys {
        if let Some(current) = existing.foreign_keys.get(&key_name)
            && !same_foreign_key(current, &foreign_key)
        {
            return Err(errorf!(
                "entity {} foreign key {} is extended with conflicting definitions",
                incoming.name,
                key_name
            ));
        }
        existing.foreign_keys.insert(key_name, foreign_key);
    }
    schema.entities.insert(name, existing);
    Ok(())
}

fn same_schema_entity(left: &Entity, right: &Entity) -> bool {
    if left.name != right.name
        || left.columns.len() != right.columns.len()
        || left.indexes.len() != right.indexes.len()
        || left.foreign_keys.len() != right.foreign_keys.len()
    {
        return false;
    }
    left.columns.iter().all(|(name, column)| {
        right
            .columns
            .get(name)
            .is_some_and(|other| same_database_column(column, other))
    }) && left.indexes.iter().all(|(name, index)| {
        right
            .indexes
            .get(name)
            .is_some_and(|other| same_index(index, other))
    }) && left.foreign_keys.iter().all(|(name, foreign_key)| {
        right
            .foreign_keys
            .get(name)
            .is_some_and(|other| same_foreign_key(foreign_key, other))
    })
}

fn remove_schema_entity_contribution(schema: &mut Schema, contribution: &Entity) {
    let Some(existing) = schema.entities.get_mut(&contribution.name) else {
        return;
    };
    if !contribution.external {
        schema.entities.remove(&contribution.name);
        return;
    }
    for name in contribution.columns.keys() {
        existing.columns.remove(name);
    }
    for name in contribution.indexes.keys() {
        existing.indexes.remove(name);
    }
    for name in contribution.foreign_keys.keys() {
        existing.foreign_keys.remove(name);
    }
    if existing.columns.is_empty()
        && existing.indexes.is_empty()
        && existing.foreign_keys.is_empty()
    {
        schema.entities.remove(&contribution.name);
    }
}

fn translation_schema_from_spec(spec: &EntitySpec) -> Result<Entity> {
    let Some(translation) = &spec.translation else {
        return Err(errorf!("translation metadata is missing"));
    };
    let behavior = translation.definition_behavior.as_ref();
    let columns = if behavior.is_some_and(|behavior| behavior.override_default_fields) {
        BTreeMap::new()
    } else {
        default_timestamp_columns()
    };
    let mut entity = Entity {
        name: translation.entity_name.clone(),
        columns,
        ..Entity::default()
    };
    let mut behavior_fields = Vec::new();
    if let Some(behavior) = behavior {
        if behavior.override_default_fields {
            behavior_fields.extend(behavior.default_fields.iter().cloned());
        }
        if behavior.override_base_fields {
            behavior_fields.extend(behavior.base_fields.iter().cloned());
        }
    }
    if !behavior_fields.is_empty() {
        let contribution = schema_from_spec(&EntitySpec {
            definition_kind: DEFINITION_MAPPING.to_owned(),
            entity_name: translation.entity_name.clone(),
            fields: behavior_fields,
            ..EntitySpec::default()
        })
        .map_err(|error| errorf!("translation behavior fields: {error}"))?;
        entity.columns.extend(contribution.columns);
        entity.indexes.extend(contribution.indexes);
        entity.foreign_keys.extend(contribution.foreign_keys);
    }
    let parent_column = translation.parent_storage_name.clone();
    let mut version_column = String::new();
    let overrides_base_fields = behavior.is_some_and(|behavior| behavior.override_base_fields);
    if !overrides_base_fields {
        if schema_definition_fields(spec)
            .iter()
            .any(|field| field.kind == FIELD_VERSION)
        {
            version_column = format!(
                "{}_version_id",
                parent_column.strip_suffix("_id").unwrap_or(&parent_column)
            );
        }
        entity
            .columns
            .insert(parent_column.clone(), binary_primary_column(&parent_column));
        if !version_column.is_empty() {
            entity.columns.insert(
                version_column.clone(),
                binary_primary_column(&version_column),
            );
        }
        entity.columns.insert(
            "language_id".to_owned(),
            binary_primary_column("language_id"),
        );
    }
    for field in &spec.fields {
        if !field.translated || field.kind == FIELD_LOCKED {
            continue;
        }
        if field
            .behavior
            .as_ref()
            .is_some_and(|behavior| behavior.runtime)
        {
            continue;
        }
        if field.storage_name.is_empty() {
            return Err(errorf!(
                "translated field {:?} has no storage name",
                field.property_name
            ));
        }
        let sql_type = sql_type(field)?;
        entity.columns.insert(
            field.storage_name.clone(),
            Column {
                name: field.storage_name.clone(),
                sql_type,
                not_null: field.required,
                backfill_sql: field.migration_default.trim().to_owned(),
                ..Column::default()
            },
        );
    }
    for index in &spec.indexes {
        if !index.translation {
            continue;
        }
        entity.indexes.insert(
            index.name.clone(),
            Index {
                name: index.name.clone(),
                unique: index.kind == INDEX_UNIQUE,
                columns: index.columns.clone(),
            },
        );
    }
    if !overrides_base_fields {
        let parent_fk_name = generated_database_object_name("fk", &entity.name, &parent_column);
        let mut stored_columns = Vec::new();
        let mut stored_reference_columns = Vec::new();
        if !version_column.is_empty() {
            stored_columns = vec![parent_column.clone(), version_column.clone()];
            stored_reference_columns = vec!["id".to_owned(), "version_id".to_owned()];
        }
        entity.foreign_keys.insert(
            parent_fk_name.clone(),
            ForeignKey {
                name: parent_fk_name,
                column: parent_column.clone(),
                reference_entity: spec.entity_name.clone(),
                reference_column: "id".to_owned(),
                columns: stored_columns,
                reference_columns: stored_reference_columns,
                on_delete: DELETE_CASCADE.to_owned(),
                on_update: "cascade".to_owned(),
            },
        );
        let language_fk_name = generated_database_object_name("fk", &entity.name, "language_id");
        entity.foreign_keys.insert(
            language_fk_name.clone(),
            ForeignKey {
                name: language_fk_name,
                column: "language_id".to_owned(),
                reference_entity: "language".to_owned(),
                reference_column: "id".to_owned(),
                on_delete: DELETE_CASCADE.to_owned(),
                on_update: "cascade".to_owned(),
                ..ForeignKey::default()
            },
        );
    }
    Ok(entity)
}

fn binary_primary_column(name: &str) -> Column {
    Column {
        name: name.to_owned(),
        sql_type: "BINARY(16)".to_owned(),
        not_null: true,
        primary_key: true,
        ..Column::default()
    }
}

/// Restores only designer-owned indexes. Relation indexes are structural
/// and regenerated from their logical relation rows.
pub fn index_specs_from_entity(spec: &EntitySpec, entity: &Entity) -> Vec<IndexSpec> {
    let fields = schema_definition_fields(spec);
    let mut reference_versions: HashMap<&str, &str> = HashMap::new();
    for field in fields.iter() {
        if field.kind == FIELD_REFERENCE_VERSION && !field.target_definition_class.is_empty() {
            reference_versions.insert(&field.target_definition_class, &field.storage_name);
        }
    }
    let mut automatic: HashMap<String, Index> = HashMap::new();
    for field in fields.iter() {
        if !is_foreign_key_kind(&field.kind)
            || field.storage_name.is_empty()
            || field.uses_existing_column
        {
            continue;
        }
        let name = generated_database_object_name("idx", &spec.entity_name, &field.storage_name);
        let mut columns = vec![field.storage_name.clone()];
        if field.kind == FIELD_HIERARCHY && field.hierarchy_version_aware {
            columns.push("parent_version_id".to_owned());
        } else if let Some(version_column) = reference_versions
            .get(field.target_definition_class.as_str())
            .filter(|column| !column.is_empty())
        {
            columns.push((*version_column).to_owned());
        }
        automatic.insert(
            name.clone(),
            Index {
                name,
                unique: false,
                columns,
            },
        );
    }
    let mut result = Vec::new();
    for (name, index) in &entity.indexes {
        if automatic
            .get(name)
            .is_some_and(|generated| same_index(generated, index))
        {
            continue;
        }
        result.push(IndexSpec {
            name: name.clone(),
            kind: if index.unique {
                INDEX_UNIQUE
            } else {
                INDEX_NORMAL
            }
            .to_owned(),
            columns: index.columns.clone(),
            translation: false,
        });
    }
    result.sort_by(|left, right| left.name.cmp(&right.name));
    result
}

/// Restores parent- and translation-table indexes from a committed snapshot
/// without conflating their independently scoped database names.
pub fn index_specs_from_entities(spec: &EntitySpec, schema: &Schema) -> Vec<IndexSpec> {
    let mut result = Vec::new();
    if let Some(parent) = schema.entities.get(&spec.entity_name) {
        let mut parent_indexes = index_specs_from_entity(spec, parent);
        if spec.definition_kind == DEFINITION_EXTENSION {
            let owned = extension_owned_columns(spec);
            parent_indexes.retain(|index| {
                index
                    .columns
                    .iter()
                    .any(|column| owned.contains(column.as_str()))
            });
        }
        result.extend(parent_indexes);
    }
    if let Some(translation) = spec
        .translation
        .as_ref()
        .filter(|translation| translation.enabled)
        && let Some(table) = schema.entities.get(&translation.entity_name)
    {
        for (name, index) in &table.indexes {
            if relation_index(table, index) {
                continue;
            }
            result.push(IndexSpec {
                name: name.clone(),
                kind: if index.unique {
                    INDEX_UNIQUE
                } else {
                    INDEX_NORMAL
                }
                .to_owned(),
                columns: index.columns.clone(),
                translation: true,
            });
        }
    }
    result.sort_by(|left, right| {
        left.translation
            .cmp(&right.translation)
            .then_with(|| left.name.cmp(&right.name))
    });
    result
}

/// Restores snapshot-only indexes for a single definition/extension or for
/// every target of a BulkEntityExtension.
pub fn restore_spec_indexes_from_entities(spec: &mut EntitySpec, schema: &Schema) {
    if spec.definition_kind != DEFINITION_BULK_EXTENSION {
        spec.indexes = index_specs_from_entities(spec, schema);
        return;
    }
    for index in 0..spec.bulk_extensions.len() {
        let target_spec = bulk_target_entity_spec(spec, &spec.bulk_extensions[index]);
        spec.bulk_extensions[index].indexes = index_specs_from_entities(&target_spec, schema);
    }
}

fn extension_owned_columns(spec: &EntitySpec) -> HashSet<&str> {
    let mut owned = HashSet::new();
    for field in &spec.fields {
        if field.kind == FIELD_LOCKED
            || field.translated
            || field.storage_name.is_empty()
            || field
                .behavior
                .as_ref()
                .is_some_and(|behavior| behavior.runtime)
            || field.kind == FIELD_ONE_TO_MANY
            || field.kind == FIELD_MANY_TO_MANY
            || (field.kind == FIELD_ONE_TO_ONE || field.kind == FIELD_MANY_TO_ONE)
                && field.uses_existing_column
        {
            continue;
        }
        owned.insert(field.storage_name.as_str());
    }
    owned
}

/// Overlays indexes a DAL EntityDefinition cannot represent. Foreign-key
/// indexes stay derived from the current PHP relation fields.
pub fn restore_snapshot_only_indexes(scanned: &Schema, snapshot: &Schema) -> Schema {
    let mut scanned = scanned.clone();
    for (entity_name, current) in scanned.entities.iter_mut() {
        let Some(committed) = snapshot.entities.get(entity_name) else {
            continue;
        };
        for (name, index) in &committed.indexes {
            if relation_index(committed, index) {
                continue;
            }
            current.indexes.insert(name.clone(), index.clone());
        }
    }
    scanned
}

pub(crate) fn relation_index(entity: &Entity, index: &Index) -> bool {
    entity.foreign_keys.values().any(|foreign_key| {
        index.name == generated_database_object_name("idx", &entity.name, &foreign_key.column)
            && !index.unique
            && index.columns.as_slice() == foreign_key_columns(foreign_key)
    })
}

pub fn sql_type(field: &FieldSpec) -> Result<String> {
    Ok(match field.kind.as_str() {
        FIELD_ID
        | FIELD_BINARY_ID
        | FIELD_VERSION
        | FIELD_REFERENCE_VERSION
        | FIELD_FOREIGN_KEY
        | FIELD_MANY_TO_ONE
        | FIELD_ONE_TO_ONE
        | FIELD_HIERARCHY => "BINARY(16)".to_owned(),
        FIELD_STRING => {
            let length = if field.max_length <= 0 {
                255
            } else {
                field.max_length
            };
            format!("VARCHAR({length})")
        }
        FIELD_ENUM => match field.enum_backing_type.as_str() {
            "int" => "INT".to_owned(),
            "string" => "VARCHAR(255)".to_owned(),
            _ => {
                return Err(errorf!(
                    "enum field {:?} has no backing type",
                    field.property_name
                ));
            }
        },
        FIELD_LONG_TEXT => "LONGTEXT".to_owned(),
        FIELD_INT => "INT".to_owned(),
        FIELD_FLOAT => "DOUBLE".to_owned(),
        FIELD_BOOL => "TINYINT(1)".to_owned(),
        FIELD_DATE => "DATE".to_owned(),
        FIELD_DATE_TIME | FIELD_CREATED_AT | FIELD_UPDATED_AT => "DATETIME(3)".to_owned(),
        FIELD_JSON | FIELD_LIST | FIELD_OBJECT => "JSON".to_owned(),
        FIELD_BLOB => "LONGBLOB".to_owned(),
        FIELD_AUTO_INCREMENT => "BIGINT UNSIGNED".to_owned(),
        _ => {
            return Err(errorf!("unsupported stored field kind {:?}", field.kind));
        }
    })
}

pub(crate) fn is_foreign_key_kind(kind: &str) -> bool {
    matches!(
        kind,
        FIELD_FOREIGN_KEY | FIELD_MANY_TO_ONE | FIELD_ONE_TO_ONE | FIELD_HIERARCHY
    )
}

pub(crate) fn generated_database_object_name(prefix: &str, entity: &str, column: &str) -> String {
    let name = format!("{prefix}.{entity}.{column}");
    if name.len() <= 64 {
        return name;
    }
    let digest = Sha256::digest(name.as_bytes());
    let suffix = hex(&digest[..6]);
    // The name is cut at a byte length; a split multi-byte character
    // becomes U+FFFD so the result stays stable for existing schemas.
    let cut = 64 - suffix.len() - 1;
    let head = String::from_utf8_lossy(&name.as_bytes()[..cut]).into_owned();
    format!("{head}.{suffix}")
}

pub(crate) fn hex(bytes: &[u8]) -> String {
    const DIGITS: &[u8; 16] = b"0123456789abcdef";
    let mut result = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        result.push(DIGITS[(byte >> 4) as usize] as char);
        result.push(DIGITS[(byte & 15) as usize] as char);
    }
    result
}

pub fn short_class(class: &str) -> &str {
    let class = class.trim_matches('\\');
    match class.rfind('\\') {
        Some(index) => &class[index + 1..],
        None => class,
    }
}

pub(crate) fn is_implicit_timestamp_field(spec: &EntitySpec, kind: &str) -> bool {
    let overridden = spec
        .definition_behavior
        .as_ref()
        .is_some_and(|behavior| behavior.override_default_fields);
    spec.definition_kind == DEFINITION_ENTITY
        && !overridden
        && (kind == FIELD_CREATED_AT || kind == FIELD_UPDATED_AT)
}

pub(crate) fn same_index(left: &Index, right: &Index) -> bool {
    left.name == right.name && left.unique == right.unique && left.columns == right.columns
}

pub(crate) fn same_database_column(left: &Column, right: &Column) -> bool {
    left.name == right.name
        && left.sql_type == right.sql_type
        && left.not_null == right.not_null
        && left.auto_increment == right.auto_increment
}

pub(crate) fn same_foreign_key(left: &ForeignKey, right: &ForeignKey) -> bool {
    left.name == right.name
        && left.column == right.column
        && left.reference_entity == right.reference_entity
        && left.reference_column == right.reference_column
        && left.on_delete == right.on_delete
        && left.on_update == right.on_update
        && foreign_key_columns(left) == foreign_key_columns(right)
        && foreign_key_reference_columns(left) == foreign_key_reference_columns(right)
}

pub(crate) fn foreign_key_columns(foreign_key: &ForeignKey) -> Vec<String> {
    if !foreign_key.columns.is_empty() {
        return foreign_key.columns.clone();
    }
    if !foreign_key.column.is_empty() {
        return vec![foreign_key.column.clone()];
    }
    Vec::new()
}

pub(crate) fn foreign_key_reference_columns(foreign_key: &ForeignKey) -> Vec<String> {
    if !foreign_key.reference_columns.is_empty() {
        return foreign_key.reference_columns.clone();
    }
    if !foreign_key.reference_column.is_empty() {
        return vec![foreign_key.reference_column.clone()];
    }
    Vec::new()
}
