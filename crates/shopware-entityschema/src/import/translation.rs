//! EntityTranslationDefinition import.

use std::collections::{HashMap, HashSet};

use swlsp_parser_php::query as phpquery;

use super::*;

/// Imports the literal, designer-owned portion of an
/// EntityTranslationDefinition; [`attach_translation`] pairs it with the
/// parent's TranslatedField facades.
pub fn import_translation_definition(
    source: &str,
    lookup: Option<RelationLookup>,
) -> Result<ImportedTranslation> {
    import_translation_class(source, lookup, "")
}

fn import_translation_class(
    source: &str,
    lookup: Option<RelationLookup>,
    selected_class: &str,
) -> Result<ImportedTranslation> {
    import_translation_parsed(&ParsedSource::new(source), lookup, selected_class)
}

pub(crate) fn import_translation_parsed(
    parsed: &ParsedSource,
    lookup: Option<RelationLookup>,
    selected_class: &str,
) -> Result<ImportedTranslation> {
    if !parsed.valid {
        return Err(errorf!(
            "parse translation definition: PHP source contains syntax errors"
        ));
    }
    let namespace = parsed.namespace.clone();
    let Some((definition, _)) = select_class(
        &parsed.classes,
        &namespace,
        selected_class,
        &["EntityTranslationDefinition"],
    ) else {
        return Err(errorf!("PHP file contains no EntityTranslationDefinition"));
    };
    let class_name = phpquery::class_name(&definition);
    let base_name = trim_suffix(&class_name, "Definition");
    let resolve = &parsed.resolver;
    let mut result = ImportedTranslation {
        spec: TranslationSpec {
            enabled: true,
            definition_class: qualify(&namespace, &class_name),
            entity_class: qualify(&namespace, &format!("{base_name}Entity")),
            collection_class: qualify(&namespace, &format!("{base_name}Collection")),
            ..TranslationSpec::default()
        },
        fields: Vec::new(),
    };
    if let Some(name) = entity_name_constant(&node_string(&definition)) {
        result.spec.entity_name = name;
    }
    let methods = phpquery::methods(&definition);
    let mut define_fields = None;
    for method in &methods {
        match phpquery::method_name(method).as_str() {
            "getEntityName" => {
                if result.spec.entity_name.is_empty()
                    && let Some(literal) = imported_literal_string_return(Some(method))
                {
                    result.spec.entity_name = literal;
                }
            }
            "getEntityClass" => {
                let class = returned_imported_class(Some(method), resolve);
                if !class.is_empty() {
                    result.spec.entity_class = class;
                }
            }
            "getCollectionClass" => {
                let class = returned_imported_class(Some(method), resolve);
                if !class.is_empty() {
                    result.spec.collection_class = class;
                }
            }
            "getParentDefinitionClass" => {
                result.spec.parent_definition_class =
                    returned_imported_class(Some(method), resolve);
            }
            "defineFields" => define_fields = Some(method.clone()),
            _ => {}
        }
    }
    result.spec.definition_metadata =
        import_definition_metadata(&methods, resolve, true, false, false);
    result.spec.definition_behavior = import_definition_behavior(&methods, resolve, lookup, true);
    if result.spec.entity_name.is_empty() && !result.spec.parent_definition_class.is_empty() {
        if let Some(target) = lookup_relation(lookup, &result.spec.parent_definition_class) {
            result.spec.entity_name = format!("{}_translation", target.entity_name);
        } else {
            let parent_name = infer_entity_name(&result.spec.parent_definition_class);
            if !parent_name.is_empty() {
                result.spec.entity_name = format!("{parent_name}_translation");
            }
        }
    }
    let Some(define_fields) = define_fields.filter(|_| {
        !result.spec.entity_name.is_empty() && !result.spec.parent_definition_class.is_empty()
    }) else {
        return Err(errorf!(
            "translation definition has incomplete literal identity or fields"
        ));
    };
    let (fields, _) = import_fields(&define_fields, resolve, lookup)?;
    result.fields = fields;
    Ok(result)
}

/// Replaces parent TranslatedField placeholders with the concrete storage
/// fields imported from the companion definition.
pub fn attach_translation(mut parent: EntitySpec, imported: ImportedTranslation) -> EntitySpec {
    let mut child_by_property: HashMap<&str, usize> = HashMap::new();
    for (index, field) in imported.fields.iter().enumerate() {
        if field.kind != FIELD_LOCKED && !field.property_name.is_empty() {
            child_by_property.insert(&field.property_name, index);
        }
    }
    let mut used = HashSet::new();
    let mut combined = Vec::with_capacity(parent.fields.len() + imported.fields.len());
    for parent_field in &parent.fields {
        let child = child_by_property
            .get(parent_field.property_name.as_str())
            .copied();
        let (true, Some(child_index)) = (parent_field.translated, child) else {
            combined.push(parent_field.clone());
            continue;
        };
        let mut field = imported.fields[child_index].clone();
        field.id = parent_field.id.clone();
        field.translated = true;
        field.translation_use_for_sort = parent_field.translation_use_for_sort;
        field.translation_api_aware = parent_field.translation_api_aware;
        field.translation_api_aware_sources = parent_field.translation_api_aware_sources.clone();
        field.translation_search_rank = parent_field.translation_search_rank;
        field.translation_search_tokenize = parent_field.translation_search_tokenize;
        field.translation_behavior = parent_field.translation_behavior.clone();
        field.translation_metadata = parent_field.translation_metadata.clone();
        field.translation_write_protected = parent_field.translation_write_protected;
        field.translation_write_scopes = parent_field.translation_write_scopes.clone();
        field.translation_inherited = parent_field.translation_inherited;
        field.translation_inherited_fk = parent_field.translation_inherited_fk.clone();
        field.translation_flags = parent_field.translation_flags.clone();
        field.translation_before_flags = parent_field.translation_before_flags.clone();
        field.translation_after_flags = parent_field.translation_after_flags.clone();
        combined.push(field);
        used.insert(child_index);
    }
    for (index, field) in imported.fields.iter().enumerate() {
        if used.contains(&index) {
            continue;
        }
        let mut field = field.clone();
        field.id = format!("translation-{}", field.id);
        field.translated = true;
        field.translation_definition_only = true;
        combined.push(field);
    }
    parent.fields = combined;
    let mut translation = imported.spec;
    if let Some(previous) = &parent.translation {
        translation.parent_storage_name = previous.parent_storage_name.clone();
        translation.parent_property_name = previous.parent_property_name.clone();
        translation.association_property = previous.association_property.clone();
        translation.association_local_field = previous.association_local_field.clone();
        translation.association_required = previous.association_required;
        translation.association_api_aware = previous.association_api_aware;
        translation.association_api_aware_sources = previous.association_api_aware_sources.clone();
        translation.association_behavior = previous.association_behavior.clone();
        translation.association_metadata = previous.association_metadata.clone();
        translation.association_write_protected = previous.association_write_protected;
        translation.association_write_scopes = previous.association_write_scopes.clone();
        translation.association_inherited = previous.association_inherited;
        translation.association_inherited_fk = previous.association_inherited_fk.clone();
        translation.reverse_inherited_property = previous.reverse_inherited_property.clone();
        translation.association_flags = previous.association_flags.clone();
        translation.association_before_flags = previous.association_before_flags.clone();
        translation.association_after_flags = previous.association_after_flags.clone();
    }
    parent.translation = Some(translation);
    complete_spec(parent)
}
