//! FieldCollection import (port of Go `import_fields.go`,
//! `import_simple_fields.go`, `import_associations.go` and
//! `import_hierarchy.go`).

use std::collections::{BTreeMap, HashMap};

use swlsp_parser_php::query as phpquery;
use swlsp_syntax::SyntaxNode;

use crate::import::node_string;

use super::flags::{
    ImportedFlagSet, ImportedModifierSet, imported_flags_in, imported_modifiers_in,
    normalize_imported_php_expression, with_field_modifiers,
};
use super::*;
use crate::specialized_fields::import_specialized_field;

struct ImportedForeignKey {
    field: FieldSpec,
    raw: String,
}

/// Owns the ordered assembly of a single FieldCollection. Foreign keys and
/// hierarchy fragments are combined without changing source IDs.
struct DefinitionFieldImporter<'a> {
    resolve: &'a Resolver,
    lookup: Option<RelationLookup<'a>>,
    fields: Vec<FieldSpec>,
    translation: Option<TranslationSpec>,
    foreign_keys: BTreeMap<String, ImportedForeignKey>,
    local_fields: HashMap<String, FieldSpec>,
    hierarchy_index: Option<usize>,
}

struct ImportedFieldExpression {
    index: usize,
    id: String,
    raw: String,
    kind_name: String,
    value: SyntaxNode,
    creation: SyntaxNode,
    creations: Vec<SyntaxNode>,
    flags: ImportedFlagSet,
    modifiers: ImportedModifierSet,
}

/// Imports `defineFields` with the file's resolver.
pub(crate) fn import_fields(
    method: &SyntaxNode,
    resolve: &Resolver,
    lookup: Option<RelationLookup>,
) -> Result<(Vec<FieldSpec>, Option<TranslationSpec>)> {
    import_fields_with(method, resolve, lookup)
}

pub(crate) fn import_fields_with(
    method: &SyntaxNode,
    resolve: &Resolver,
    lookup: Option<RelationLookup>,
) -> Result<(Vec<FieldSpec>, Option<TranslationSpec>)> {
    let collections = phpquery::object_creations(method, &["FieldCollection"]);
    let Some(collection) = collections.first() else {
        return Err(errorf!(
            "defineFields does not return a literal FieldCollection"
        ));
    };
    let Some(array) = imported_field_collection_array(method, collection) else {
        return Err(errorf!("defineFields FieldCollection has no literal array"));
    };
    let items = phpquery::array_items(&array);
    let mut importer = DefinitionFieldImporter {
        resolve,
        lookup,
        fields: Vec::with_capacity(items.len()),
        translation: None,
        foreign_keys: BTreeMap::new(),
        local_fields: imported_local_fields(method, resolve, lookup),
        hierarchy_index: None,
    };
    for (index, item) in items.iter().enumerate() {
        importer.import_item(index, item);
    }
    let fields = collapse_imported_hierarchy(importer.fields, importer.hierarchy_index);
    let mut fields = append_unassociated_foreign_keys(fields, importer.foreign_keys);
    let mut translation = importer.translation;
    promote_imported_write_protection(&mut fields, translation.as_mut());
    Ok((fields, translation))
}

impl DefinitionFieldImporter<'_> {
    fn import_item(&mut self, index: usize, item: &SyntaxNode) {
        let Some(value) = phpquery::array_item_value(item) else {
            return;
        };
        let creations = phpquery::object_creations(&value, &[]);
        let Some(creation) = creations.first().cloned() else {
            self.import_local_or_locked_field(index, item, &value);
            return;
        };
        let calls = phpquery::calls(&value, &[]);
        let expression = ImportedFieldExpression {
            index,
            id: format!("field-{}", index + 1),
            raw: node_string(item).trim().to_owned(),
            kind_name: short_class(&phpquery::object_class_name(&creation)).to_owned(),
            flags: imported_flags_in(&calls, &creation, self.resolve),
            modifiers: imported_modifiers_in(calls, &creation),
            value,
            creation,
            creations,
        };
        if self.import_value_field(&expression) || self.import_hierarchy_field(&expression) {
            return;
        }
        if expression.kind_name == "FkField" {
            self.import_foreign_key(&expression);
            return;
        }
        if let Some(field) = self.import_association_field(&expression) {
            self.fields.push(field);
            return;
        }
        self.fields.push(locked_field(index, &expression.raw));
    }

    fn import_local_or_locked_field(
        &mut self,
        index: usize,
        item: &SyntaxNode,
        value: &SyntaxNode,
    ) {
        let variable = direct_variable_expression(Some(value));
        if !variable.is_empty()
            && let Some(local) = self.local_fields.get(&variable)
        {
            let mut local = local.clone();
            local.id = format!("field-{}", index + 1);
            self.fields.push(local);
            return;
        }
        self.fields.push(locked_field(index, &node_string(item)));
    }

    fn import_value_field(&mut self, e: &ImportedFieldExpression) -> bool {
        if let Some(conditional) = import_conditional_association_value(
            &e.id,
            &e.raw,
            &e.value,
            &e.flags,
            &e.modifiers,
            self.resolve,
            self.lookup,
        ) {
            self.fields.push(conditional);
            return true;
        }
        if let Some(specialized) = import_specialized_field(
            &e.id,
            &e.raw,
            &e.creation,
            &e.flags,
            &e.modifiers,
            self.resolve,
        ) {
            self.fields.push(specialized);
            return true;
        }
        let Some((simple, translation)) =
            import_simple_definition_field(e, self.resolve, self.lookup)
        else {
            return false;
        };
        if translation.is_some() {
            self.translation = translation;
        }
        if let Some(simple) = simple {
            self.fields.push(simple);
        }
        true
    }

    fn import_foreign_key(&mut self, e: &ImportedFieldExpression) {
        let storage = imported_string_argument(&e.creation, 0);
        let mut field = FieldSpec {
            id: e.id.clone(),
            kind: FIELD_MANY_TO_ONE.to_owned(),
            storage_name: storage.clone(),
            foreign_key_property_name: imported_string_argument(&e.creation, 1),
            target_definition_class: self
                .resolve
                .resolve(&imported_class_argument(&e.creation, 2)),
            reference_field: default_string(&imported_string_argument(&e.creation, 3), "id"),
            editable: true,
            ..FieldSpec::default()
        };
        e.flags.apply_stored(&mut field);
        self.foreign_keys.insert(
            storage,
            ImportedForeignKey {
                field: with_field_modifiers(field, &e.modifiers),
                raw: e.raw.clone(),
            },
        );
    }

    fn import_association_field(&mut self, e: &ImportedFieldExpression) -> Option<FieldSpec> {
        match e.kind_name.as_str() {
            "ManyToOneAssociationField" => {
                Some(self.import_to_one_association(e, FIELD_MANY_TO_ONE))
            }
            "OneToOneAssociationField" => Some(self.import_to_one_association(e, FIELD_ONE_TO_ONE)),
            "OneToManyAssociationField" => {
                let mut field = FieldSpec {
                    id: e.id.clone(),
                    kind: FIELD_ONE_TO_MANY.to_owned(),
                    property_name: imported_string_argument(&e.creation, 0),
                    target_definition_class: self
                        .resolve
                        .resolve(&imported_class_argument(&e.creation, 1)),
                    reference_storage_name: imported_string_argument(&e.creation, 2),
                    source_column: default_string(&imported_string_argument(&e.creation, 3), "id"),
                    editable: true,
                    raw: e.raw.clone(),
                    ..FieldSpec::default()
                };
                self.finish_association(&mut field, e);
                Some(field)
            }
            "ManyToManyAssociationField" => {
                let mut field = FieldSpec {
                    id: e.id.clone(),
                    kind: FIELD_MANY_TO_MANY.to_owned(),
                    property_name: imported_string_argument(&e.creation, 0),
                    target_definition_class: self
                        .resolve
                        .resolve(&imported_class_argument(&e.creation, 1)),
                    mapping_definition_class: self
                        .resolve
                        .resolve(&imported_class_argument(&e.creation, 2)),
                    mapping_local_column: imported_string_argument(&e.creation, 3),
                    mapping_reference_column: imported_string_argument(&e.creation, 4),
                    source_column: default_string(&imported_string_argument(&e.creation, 5), "id"),
                    reference_field: default_string(
                        &imported_string_argument(&e.creation, 6),
                        "id",
                    ),
                    editable: true,
                    raw: e.raw.clone(),
                    ..FieldSpec::default()
                };
                self.finish_association(&mut field, e);
                Some(field)
            }
            _ => None,
        }
    }

    /// To-one constructors differ in argument order and autoload default but
    /// share foreign-key pairing and association-flag assembly.
    fn import_to_one_association(&mut self, e: &ImportedFieldExpression, kind: &str) -> FieldSpec {
        let (class_argument, reference_argument, autoload) = if kind == FIELD_ONE_TO_ONE {
            (3, 2, true)
        } else {
            (2, 3, false)
        };
        let storage = imported_string_argument(&e.creation, 1);
        let foreign_key = self.foreign_keys.remove(&storage);
        let mut field = match &foreign_key {
            Some(foreign_key) => foreign_key.field.clone(),
            None => FieldSpec {
                id: e.id.clone(),
                storage_name: storage.clone(),
                uses_existing_column: true,
                editable: true,
                raw: e.raw.clone(),
                ..FieldSpec::default()
            },
        };
        field.kind = kind.to_owned();
        field.property_name = imported_string_argument(&e.creation, 0);
        field.target_definition_class = self
            .resolve
            .resolve(&imported_class_argument(&e.creation, class_argument));
        field.reference_field = default_string(
            &imported_string_argument(&e.creation, reference_argument),
            "id",
        );
        field.reference_storage_name = field.reference_field.clone();
        field.association_autoload = imported_association_autoload(&e.creation, autoload);
        if let Some(foreign_key) = foreign_key {
            field.raw = format!("{},\n{}", foreign_key.raw, e.raw);
        }
        self.finish_association(&mut field, e);
        field
    }

    fn finish_association(&self, field: &mut FieldSpec, e: &ImportedFieldExpression) {
        field.delete_behavior = imported_delete_behavior(&e.creations);
        apply_imported_delete_options(field, &e.creations);
        apply_imported_association_flags(field, &e.flags, &e.modifiers);
        if let Some(target) = lookup_relation(self.lookup, &field.target_definition_class) {
            enrich_relation(field, &target);
        }
    }

    fn hierarchy_field(&mut self, id: &str) -> usize {
        if let Some(index) = self.hierarchy_index {
            return index;
        }
        let index = self.fields.len();
        self.hierarchy_index = Some(index);
        self.fields.push(FieldSpec {
            id: id.to_owned(),
            kind: FIELD_HIERARCHY.to_owned(),
            property_name: "children".to_owned(),
            hierarchy_parent_property: "parent".to_owned(),
            foreign_key_property_name: "parentId".to_owned(),
            storage_name: "parent_id".to_owned(),
            reference_field: "id".to_owned(),
            reference_storage_name: "id".to_owned(),
            delete_behavior: DELETE_CASCADE.to_owned(),
            editable: true,
            ..FieldSpec::default()
        });
        index
    }

    fn import_hierarchy_field(&mut self, e: &ImportedFieldExpression) -> bool {
        if !matches!(
            e.kind_name.as_str(),
            "ParentFkField" | "ParentAssociationField" | "ChildrenAssociationField"
        ) {
            return false;
        }
        let index = self.hierarchy_field(&e.id);
        let resolve = self.resolve;
        let field = &mut self.fields[index];
        match e.kind_name.as_str() {
            "ParentFkField" => {
                field.target_definition_class =
                    resolve.resolve(&imported_class_argument(&e.creation, 0));
                e.flags.apply_stored(field);
                field.required = false;
                field.primary = false;
                field.raw = e.raw.clone();
                field.modifiers_before_flags = e.modifiers.before_flags.clone();
                field.modifiers_after_flags = e.modifiers.after_flags.clone();
            }
            "ParentAssociationField" => {
                field.target_definition_class =
                    resolve.resolve(&imported_class_argument(&e.creation, 0));
                field.reference_field =
                    default_string(&imported_string_argument(&e.creation, 1), "id");
                field.reference_storage_name = field.reference_field.clone();
                apply_imported_association_flags(field, &e.flags, &e.modifiers);
            }
            _ => {
                field.target_definition_class =
                    resolve.resolve(&imported_class_argument(&e.creation, 0));
                field.property_name =
                    default_string(&imported_string_argument(&e.creation, 1), "children");
                field.hierarchy_children_flags = e.flags.preserved.clone();
                field.hierarchy_children_api_aware = e.flags.api_aware;
                field.hierarchy_children_api_sources = e.flags.api_aware_sources.clone();
                field.hierarchy_children_rank = e.flags.ranking;
                field.hierarchy_children_tokenize = e.flags.ranking_tokenize;
                field.hierarchy_children_behavior = e.flags.behavior.clone();
                field.hierarchy_children_metadata = e.flags.metadata.clone();
                field.hierarchy_children_inherited = e.flags.inherited;
                field.hierarchy_children_inherited_fk = e.flags.inherited_foreign_key.clone();
                field.hierarchy_children_reverse = e.flags.reverse_inherited_property.clone();
                field.hierarchy_children_before = e.modifiers.before_flags.clone();
                field.hierarchy_children_after = e.modifiers.after_flags.clone();
                field.delete_behavior = DELETE_CASCADE.to_owned();
            }
        }
        if let Some(target) = lookup_relation(self.lookup, &field.target_definition_class) {
            enrich_relation(field, &target);
        }
        true
    }
}

fn apply_imported_association_flags(
    field: &mut FieldSpec,
    flags: &ImportedFlagSet,
    modifiers: &ImportedModifierSet,
) {
    flags.apply_association(field);
    field.association_before_flags = modifiers.before_flags.clone();
    field.association_after_flags = modifiers.after_flags.clone();
}

fn append_unassociated_foreign_keys(
    mut fields: Vec<FieldSpec>,
    foreign_keys: BTreeMap<String, ImportedForeignKey>,
) -> Vec<FieldSpec> {
    for (_, foreign_key) in foreign_keys {
        let mut field = foreign_key.field;
        field.kind = FIELD_FOREIGN_KEY.to_owned();
        field.property_name = std::mem::take(&mut field.foreign_key_property_name);
        field.raw = foreign_key.raw;
        fields.push(field);
    }
    fields
}

fn collapse_imported_hierarchy(
    fields: Vec<FieldSpec>,
    hierarchy_index: Option<usize>,
) -> Vec<FieldSpec> {
    let Some(hierarchy_index) = hierarchy_index else {
        return fields;
    };
    let mut hierarchy = fields[hierarchy_index].clone();
    let mut reference_version_index = None;
    for (index, field) in fields.iter().enumerate() {
        if index == hierarchy_index
            || field.kind != FIELD_REFERENCE_VERSION
            || field.storage_name != "parent_version_id"
            || field.target_definition_class != hierarchy.target_definition_class
        {
            continue;
        }
        reference_version_index = Some(index);
        hierarchy.hierarchy_version_aware = true;
        hierarchy.hierarchy_version_api_aware = field.api_aware;
        hierarchy.hierarchy_version_api_sources = field.api_aware_sources.clone();
        hierarchy.hierarchy_version_behavior = field.behavior.clone();
        hierarchy.hierarchy_version_metadata = field.metadata.clone();
        hierarchy.hierarchy_version_inherited = field.inherited;
        hierarchy.hierarchy_version_inherited_fk = field.inherited_foreign_key.clone();
        hierarchy.hierarchy_version_flags = field.preserved_flags.clone();
        hierarchy.hierarchy_version_before = field.modifiers_before_flags.clone();
        hierarchy.hierarchy_version_after = field.modifiers_after_flags.clone();
        break;
    }
    let mut collapsed = Vec::with_capacity(fields.len());
    let mut hierarchy = Some(hierarchy);
    for (index, field) in fields.into_iter().enumerate() {
        if Some(index) == reference_version_index {
            continue;
        }
        if index == hierarchy_index {
            collapsed.push(hierarchy.take().unwrap_or(field));
        } else {
            collapsed.push(field);
        }
    }
    collapsed
}

/// `Some((None, translation))` for a TranslationsAssociationField,
/// `Some((Some(field), None))` for a recognized field, `None` otherwise.
#[allow(clippy::type_complexity)]
fn import_simple_definition_field(
    e: &ImportedFieldExpression,
    resolve: &Resolver,
    lookup: Option<RelationLookup>,
) -> Option<(Option<FieldSpec>, Option<TranslationSpec>)> {
    let flags = &e.flags;
    let modifiers = &e.modifiers;
    let creation = &e.creation;
    let mut base = FieldSpec {
        id: e.id.clone(),
        editable: true,
        raw: e.raw.clone(),
        ..FieldSpec::default()
    };
    flags.apply_stored(&mut base);
    match e.kind_name.as_str() {
        "TranslatedField" => {
            let field = FieldSpec {
                id: e.id.clone(),
                kind: FIELD_LOCKED.to_owned(),
                property_name: imported_string_argument(creation, 0),
                translated: true,
                translation_use_for_sort: imported_bool_argument(creation, 1),
                translation_api_aware: Some(flags.api_aware),
                translation_search_rank: Some(flags.ranking),
                translation_api_aware_sources: flags.api_aware_sources.clone(),
                translation_search_tokenize: flags.ranking_tokenize,
                translation_behavior: flags.behavior.clone(),
                translation_metadata: flags.metadata.clone(),
                translation_inherited: flags.inherited,
                translation_inherited_fk: flags.inherited_foreign_key.clone(),
                translation_flags: flags.preserved.clone(),
                translation_before_flags: modifiers.before_flags.clone(),
                translation_after_flags: modifiers.after_flags.clone(),
                editable: false,
                raw: e.raw.clone(),
                ..FieldSpec::default()
            };
            return Some((Some(field), None));
        }
        "TranslationsAssociationField" => {
            let translation = TranslationSpec {
                enabled: true,
                definition_class: resolve.resolve(&imported_class_argument(creation, 0)),
                parent_storage_name: default_string(&imported_string_argument(creation, 1), "id"),
                association_property: default_string(
                    &imported_string_argument(creation, 2),
                    "translations",
                ),
                association_local_field: default_string(
                    &imported_string_argument(creation, 3),
                    "id",
                ),
                association_required: flags.required,
                association_api_aware: flags.api_aware,
                association_api_aware_sources: flags.api_aware_sources.clone(),
                association_behavior: flags.behavior.clone(),
                association_metadata: flags.metadata.clone(),
                association_inherited: flags.inherited,
                association_inherited_fk: flags.inherited_foreign_key.clone(),
                reverse_inherited_property: flags.reverse_inherited_property.clone(),
                association_flags: flags.preserved.clone(),
                association_before_flags: modifiers.before_flags.clone(),
                association_after_flags: modifiers.after_flags.clone(),
                ..TranslationSpec::default()
            };
            return Some((None, Some(translation)));
        }
        "IdField" => {
            base.storage_name = imported_string_argument(creation, 0);
            base.property_name = imported_string_argument(creation, 1);
            base.kind = if base.storage_name == "id" && base.property_name == "id" {
                FIELD_ID
            } else {
                FIELD_BINARY_ID
            }
            .to_owned();
        }
        "StringField" | "LongTextField" | "IntField" | "FloatField" | "BoolField" | "DateField"
        | "DateTimeField" | "JsonField" | "ListField" | "ObjectField" | "BlobField" => {
            base.kind = imported_scalar_kind(&e.kind_name).to_owned();
            base.storage_name = imported_string_argument(creation, 0);
            base.property_name = imported_string_argument(creation, 1);
            match base.kind.as_str() {
                FIELD_STRING => base.max_length = imported_int_argument(creation, 2, 255),
                FIELD_INT => {
                    base.min = imported_optional_int_argument(creation, 2);
                    base.max = imported_optional_int_argument(creation, 3);
                }
                FIELD_LIST => {
                    base.element_type_class =
                        resolve.resolve(&imported_class_argument(creation, 2));
                }
                FIELD_JSON => {
                    let count = phpquery::arguments(creation).count();
                    if count > 2 {
                        base.json_property_mapping_expression = normalize_imported_php_expression(
                            &phpquery::argument_value_text(creation, 2),
                            resolve,
                        );
                    }
                    if count > 3 {
                        base.json_default_expression = normalize_imported_php_expression(
                            &phpquery::argument_value_text(creation, 3),
                            resolve,
                        );
                    }
                }
                _ => {}
            }
        }
        "EnumField" => {
            let (class, case) = imported_enum_case(creation, resolve);
            if class.is_empty() || case.is_empty() {
                return Some((Some(locked_field(e.index, &e.raw)), None));
            }
            base.enum_class = class;
            base.enum_case = case;
            base.kind = FIELD_ENUM.to_owned();
            base.storage_name = imported_string_argument(creation, 0);
            base.property_name = imported_string_argument(creation, 1);
        }
        "AutoIncrementField" => {
            base.kind = FIELD_AUTO_INCREMENT.to_owned();
            base.property_name = "autoIncrement".to_owned();
            base.storage_name = "auto_increment".to_owned();
            base.required = true;
        }
        "CreatedAtField" => {
            base.kind = FIELD_CREATED_AT.to_owned();
            base.property_name = "createdAt".to_owned();
            base.storage_name = "created_at".to_owned();
            base.required = true;
        }
        "UpdatedAtField" => {
            base.kind = FIELD_UPDATED_AT.to_owned();
            base.property_name = "updatedAt".to_owned();
            base.storage_name = "updated_at".to_owned();
        }
        "VersionField" => {
            base.kind = FIELD_VERSION.to_owned();
            base.property_name = "versionId".to_owned();
            base.storage_name = "version_id".to_owned();
            base.required = true;
        }
        "ReferenceVersionField" => {
            return Some((
                Some(import_reference_version_field(
                    &e.id, &e.raw, creation, flags, modifiers, resolve, lookup,
                )),
                None,
            ));
        }
        _ => return None,
    }
    Some((Some(with_field_modifiers(base, modifiers)), None))
}
