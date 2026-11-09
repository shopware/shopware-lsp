//! EntityExtension and BulkEntityExtension import.

use std::collections::HashMap;

use rowan::NodeOrToken;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax as php;

use super::flags::{
    imported_api_aware_sources, imported_choice, imported_default_bool_argument,
    imported_deprecation, imported_rule_areas, imported_runtime_dependencies,
    imported_search_ranking, outer_object_creation,
};
use super::*;
use crate::field_modification::field_flag_kind_for_class;
use crate::model::bulk_target_entity_spec;
use crate::validate_spec::{ENTITY_NAME_PATTERN, validate_spec};

fn has_comment_or_attribute(text: &str) -> bool {
    text.contains("//") || text.contains("/*") || text.contains("#[")
}

/// Imports literal `$collection->add(...)` calls of an EntityExtension
/// through the same field importer as EntityDefinition.
pub fn import_extension(source: &str, lookup: Option<RelationLookup>) -> Result<EntitySpec> {
    import_extension_class(source, lookup, "")
}

pub(crate) fn import_extension_class(
    source: &str,
    lookup: Option<RelationLookup>,
    selected_class: &str,
) -> Result<EntitySpec> {
    import_extension_parsed(&ParsedSource::new(source), lookup, selected_class)
}

pub(crate) fn import_extension_parsed(
    parsed: &ParsedSource,
    lookup: Option<RelationLookup>,
    selected_class: &str,
) -> Result<EntitySpec> {
    if !parsed.valid {
        return Err(errorf!(
            "parse entity extension: PHP source contains syntax errors"
        ));
    }
    let root = &parsed.root;
    let resolve = &parsed.resolver;
    let namespace = parsed.namespace.clone();
    let Some((extension, _)) = select_class(
        &parsed.classes,
        &namespace,
        selected_class,
        &["EntityExtension"],
    ) else {
        return Err(errorf!("PHP file contains no concrete EntityExtension"));
    };
    let class_name = phpquery::class_name(&extension);
    let mut base_name = trim_suffix(&class_name, "Extension");
    if base_name == class_name {
        base_name = trim_suffix(&class_name, "Definition");
    }
    let mut spec = EntitySpec {
        mode: "edit".to_owned(),
        definition_kind: DEFINITION_EXTENSION.to_owned(),
        namespace: namespace.clone(),
        class_name: base_name.to_owned(),
        definition_class: qualify(&namespace, &class_name),
        create_migration: true,
        ..EntitySpec::default()
    };
    let mut extend_fields = None;
    let mut extend_protections = None;
    let mut modify_fields = None;
    for method in phpquery::methods(&extension) {
        match phpquery::method_name(&method).as_str() {
            "extendFields" => extend_fields = Some(method),
            "extendProtections" => extend_protections = Some(method),
            "modifyFields" => modify_fields = Some(method),
            "getDefinitionClass" => {
                let target = returned_imported_class(Some(&method), resolve);
                if !target.is_empty() {
                    spec.extended_definition_class = target;
                }
            }
            "getEntityName" => {
                let target = returned_entity_name_definition_class(&method, resolve);
                if !target.is_empty() {
                    spec.extended_definition_class = target;
                }
                if spec.entity_name.is_empty()
                    && let Some(literal) = imported_literal_string_return(Some(&method))
                {
                    spec.entity_name = literal;
                }
            }
            _ => {}
        }
    }
    if spec.extended_definition_class.is_empty()
        && !spec.entity_name.is_empty()
        && let Some(target) = lookup_relation_by_entity_name(lookup, &spec.entity_name)
    {
        spec.extended_definition_class = target.definition_class;
        spec.entity_name = target.entity_name;
        spec.extended_fields = target.fields;
    }
    if spec.extended_definition_class.is_empty() {
        return Err(errorf!(
            "entity extension target {:?} is not present in the indexed DAL catalog",
            spec.entity_name
        ));
    }
    if let Some(target) = lookup_relation(lookup, &spec.extended_definition_class) {
        spec.entity_name = target.entity_name;
        spec.extended_fields = target.fields;
    }
    if spec.entity_name.is_empty() {
        return Err(errorf!(
            "entity extension target has no technical entity name"
        ));
    }
    let mut expressions = Vec::new();
    if let Some(method) = &extend_fields {
        let (fields, imported_expressions) = import_extension_fields(root, method, lookup)?;
        spec.fields = fields;
        expressions = imported_expressions;
    }
    import_entity_extension_protections(&mut spec, extend_protections.as_ref());
    import_entity_extension_field_modifications(&mut spec, modify_fields.as_ref(), resolve);
    let mut spec = complete_spec(spec);
    if !validate_spec(&spec).is_empty() {
        // Static calls can still contain constants or helper calls the typed
        // model cannot prove; preserve every call losslessly instead.
        spec.fields = expressions
            .iter()
            .enumerate()
            .map(|(index, expression)| locked_field(index, expression))
            .collect();
        spec = complete_spec(spec);
    }
    Ok(spec)
}

/// Imports a literal BulkEntityExtension::collect method; each yield becomes
/// one independently validated extension target.
pub fn import_bulk_extension(source: &str, lookup: Option<RelationLookup>) -> Result<EntitySpec> {
    import_bulk_extension_class(source, lookup, "")
}

pub(crate) fn import_bulk_extension_class(
    source: &str,
    lookup: Option<RelationLookup>,
    selected_class: &str,
) -> Result<EntitySpec> {
    import_bulk_extension_parsed(&ParsedSource::new(source), lookup, selected_class)
}

pub(crate) fn import_bulk_extension_parsed(
    parsed: &ParsedSource,
    lookup: Option<RelationLookup>,
    selected_class: &str,
) -> Result<EntitySpec> {
    if !parsed.valid {
        return Err(errorf!(
            "parse bulk entity extension: PHP source contains syntax errors"
        ));
    }
    let root = &parsed.root;
    let resolve = &parsed.resolver;
    let namespace = parsed.namespace.clone();
    let Some((extension, _)) = select_class(
        &parsed.classes,
        &namespace,
        selected_class,
        &["BulkEntityExtension"],
    ) else {
        return Err(errorf!("PHP file contains no concrete BulkEntityExtension"));
    };
    let class_name = phpquery::class_name(&extension);
    let mut base_name = class_name.as_str();
    for suffix in [
        "BulkEntityExtension",
        "BulkExtension",
        "Extension",
        "Definition",
    ] {
        if base_name != class_name {
            break;
        }
        base_name = trim_suffix(&class_name, suffix);
    }
    let mut spec = EntitySpec {
        mode: "edit".to_owned(),
        definition_kind: DEFINITION_BULK_EXTENSION.to_owned(),
        namespace: namespace.clone(),
        class_name: base_name.to_owned(),
        definition_class: qualify(&namespace, &class_name),
        create_migration: true,
        ..EntitySpec::default()
    };
    let Some(collect) = phpquery::methods(&extension)
        .into_iter()
        .find(|method| phpquery::method_name(method) == "collect")
    else {
        return Err(errorf!("bulk entity extension has no collect method"));
    };
    let preserve_collect = |mut spec: EntitySpec| {
        spec.bulk_extensions.clear();
        spec.collect_method_raw = node_string(&collect).trim().to_owned();
        complete_spec(spec)
    };
    let yields = phpquery::nodes(&collect, &[php::PHP_YIELD_EXPRESSION]);
    if yields.is_empty()
        || phpquery::nodes(&collect, &[php::PHP_EXPRESSION_STATEMENT]).len() != yields.len()
    {
        return Ok(preserve_collect(spec));
    }
    for (index, yield_expression) in yields.iter().enumerate() {
        let (key, value) = imported_yield_parts(yield_expression);
        let (Some(key), Some(value)) = (key, value.filter(|value| value.kind() == php::PHP_ARRAY))
        else {
            return Ok(preserve_collect(spec));
        };
        let mut target = BulkExtensionTargetSpec {
            id: format!("bulk-target-{index}"),
            ..BulkExtensionTargetSpec::default()
        };
        if key.kind() == php::PHP_STRING {
            target.entity_name = phpquery::string_value(&key);
            if let Some(relation) = lookup_relation_by_entity_name(lookup, &target.entity_name) {
                target.extended_definition_class = relation.definition_class;
                target.extended_fields = relation.fields;
            }
        } else {
            let class_name = phpquery::scoped_access_class(&key, "ENTITY_NAME");
            if !class_name.is_empty() {
                target.extended_definition_class = resolve.resolve(&class_name);
                if let Some(relation) = lookup_relation(lookup, &target.extended_definition_class) {
                    target.entity_name = relation.entity_name;
                    target.extended_fields = relation.fields;
                }
            }
        }
        if !ENTITY_NAME_PATTERN.is_match(&target.entity_name) {
            return Ok(preserve_collect(spec));
        }
        let mut expressions = Vec::new();
        for item in phpquery::array_items(&value) {
            let Some(expression) = phpquery::array_item_value(&item) else {
                return Ok(preserve_collect(spec));
            };
            expressions.push(node_string(&expression).trim().to_owned());
        }
        let Ok(fields) = import_extension_field_expressions(root, &expressions, lookup) else {
            return Ok(preserve_collect(spec));
        };
        target.fields = fields;
        let mut target_spec = bulk_target_entity_spec(&spec, &target);
        if target_spec.extended_definition_class.is_empty() {
            target_spec.extended_definition_class = spec.definition_class.clone();
        }
        if !validate_spec(&target_spec).is_empty() {
            target.fields = expressions
                .iter()
                .enumerate()
                .map(|(expression_index, expression)| locked_field(expression_index, expression))
                .collect();
        }
        spec.bulk_extensions.push(target);
    }
    Ok(complete_spec(spec))
}

fn imported_yield_parts(yield_expression: &SyntaxNode) -> (Option<SyntaxNode>, Option<SyntaxNode>) {
    let mut key = None;
    let mut value = None;
    let mut after_arrow = false;
    for child in yield_expression.children_with_tokens() {
        match child {
            NodeOrToken::Token(token) => {
                if token.kind() == php::TK_ARROW {
                    after_arrow = true;
                }
            }
            NodeOrToken::Node(node) => {
                if !after_arrow {
                    key = Some(node);
                } else if value.is_none() {
                    value = Some(node);
                }
            }
        }
    }
    (key, value)
}

pub(crate) fn import_entity_extension_field_modifications(
    spec: &mut EntitySpec,
    method: Option<&SyntaxNode>,
    resolve: &Resolver,
) {
    let Some(method) = method else {
        return;
    };
    let method_text = node_string(method);
    let lock = |spec: &mut EntitySpec| {
        spec.field_modifications.clear();
        spec.modify_fields_method_raw = method_text.trim().to_owned();
    };
    if has_comment_or_attribute(&method_text) {
        lock(spec);
        return;
    }
    let mut by_property: HashMap<String, usize> = HashMap::new();
    let mut mutation_calls = 0;
    for call in phpquery::calls(method, &[]) {
        let operation = phpquery::call_method_name(&call);
        if operation == "get" {
            continue;
        }
        if operation != "addFlags" && operation != "removeFlag" {
            lock(spec);
            return;
        }
        let property = modified_field_property(&call);
        if property.is_empty() {
            lock(spec);
            return;
        }
        let index = *by_property.entry(property.clone()).or_insert_with(|| {
            spec.field_modifications.push(FieldModificationSpec {
                id: format!("modify-{property}"),
                property_name: property.clone(),
                ..FieldModificationSpec::default()
            });
            spec.field_modifications.len() - 1
        });
        if operation == "addFlags" {
            let count = phpquery::arguments(&call).count();
            for argument_index in 0..count {
                let expression = phpquery::argument_expression(&call, argument_index);
                let creation = outer_object_creation(expression.as_ref());
                let (Some(expression), Some(creation)) = (expression, creation) else {
                    lock(spec);
                    return;
                };
                if node_string(&creation).trim() != node_string(&expression).trim() {
                    lock(spec);
                    return;
                }
                let Some(flag) = imported_field_modification_flag(&creation, resolve) else {
                    lock(spec);
                    return;
                };
                spec.field_modifications[index].add_flags.push(flag);
            }
        } else {
            let class_name = imported_class_argument(&call, 0);
            if class_name.is_empty() {
                lock(spec);
                return;
            }
            let Some(kind) = field_flag_kind_for_class(&resolve.resolve(&class_name)) else {
                lock(spec);
                return;
            };
            spec.field_modifications[index].remove_flags.push(kind);
        }
        mutation_calls += 1;
    }
    if phpquery::nodes(method, &[php::PHP_EXPRESSION_STATEMENT]).len() != mutation_calls {
        lock(spec);
    }
}

fn modified_field_property(call: &SyntaxNode) -> String {
    let Some(receiver) = phpquery::call_receiver(call) else {
        return String::new();
    };
    let mut candidates = phpquery::calls(&receiver, &[]);
    if phpquery::call_method_name(&receiver) == "get" {
        candidates.insert(0, receiver.clone());
    }
    for candidate in &candidates {
        if phpquery::call_method_name(candidate) != "get" {
            continue;
        }
        let Some(collection) = phpquery::call_receiver(candidate) else {
            continue;
        };
        if node_string(&collection).trim() != "$collection" {
            continue;
        }
        if let Some(expression) = phpquery::argument_expression(candidate, 0)
            && expression.kind() == php::PHP_STRING
        {
            return phpquery::string_value(&expression);
        }
    }
    String::new()
}

fn imported_field_modification_flag(
    creation: &SyntaxNode,
    resolve: &Resolver,
) -> Option<FieldFlagSpec> {
    let kind = field_flag_kind_for_class(&resolve.resolve(&phpquery::object_class_name(creation)))?;
    let argument_count = phpquery::arguments(creation).count();
    let mut flag = FieldFlagSpec {
        kind: kind.clone(),
        ..FieldFlagSpec::default()
    };
    let recognized = match kind.as_str() {
        FLAG_API_AWARE => match imported_api_aware_sources(creation, resolve) {
            Some(sources) => {
                flag.api_sources = sources;
                true
            }
            None => false,
        },
        FLAG_SEARCH_RANKING => match imported_search_ranking(creation) {
            Some((ranking, tokenize)) => {
                flag.search_ranking = ranking;
                flag.search_tokenize = tokenize;
                true
            }
            None => false,
        },
        FLAG_RUNTIME => {
            let (dependencies, expression) = imported_runtime_dependencies(creation);
            flag.runtime_dependencies = dependencies;
            flag.runtime_dependencies_expression = expression;
            true
        }
        FLAG_INHERITED => {
            flag.inherited_foreign_key = imported_string_argument(creation, 0);
            argument_count == 0 || !flag.inherited_foreign_key.is_empty()
        }
        FLAG_REVERSE_INHERITED => {
            flag.reverse_property = imported_string_argument(creation, 0);
            !flag.reverse_property.is_empty() && argument_count == 1
        }
        FLAG_WRITE_PROTECTED => match parse_write_protected_flag(node_string(creation).trim()) {
            Some(scopes) => {
                flag.write_scopes = scopes;
                true
            }
            None => false,
        },
        FLAG_ALLOW_HTML => {
            let value = imported_default_bool_argument(creation, 0, true);
            flag.allow_html_sanitized = Some(value.unwrap_or(false));
            value.is_some()
        }
        FLAG_CASCADE_DELETE => {
            if argument_count != 0 {
                let value = imported_default_bool_argument(creation, 0, true);
                flag.clone_relevant = Some(value.unwrap_or(false));
                value.is_some()
            } else {
                true
            }
        }
        FLAG_SET_NULL_ON_DELETE => {
            if argument_count != 0 {
                let value = imported_default_bool_argument(creation, 0, false);
                flag.enforced_by_constraint = Some(value.unwrap_or(false));
                value.is_some()
            } else {
                true
            }
        }
        FLAG_SINCE => {
            flag.since = imported_string_argument(creation, 0);
            !flag.since.is_empty() && argument_count == 1
        }
        FLAG_DEPRECATED => {
            flag.deprecated = imported_deprecation(creation);
            flag.deprecated.is_some()
        }
        FLAG_RULE_AREAS => match imported_rule_areas(creation) {
            Some(areas) => {
                flag.rule_areas = areas;
                true
            }
            None => false,
        },
        FLAG_CHOICE => {
            flag.choice = imported_choice(creation);
            flag.choice.is_some()
        }
        _ => argument_count == 0,
    };
    recognized.then_some(flag)
}

pub(crate) fn import_entity_extension_protections(
    spec: &mut EntitySpec,
    method: Option<&SyntaxNode>,
) {
    let Some(method) = method else {
        return;
    };
    let method_text = node_string(method);
    let raw = method_text.trim().to_owned();
    if has_comment_or_attribute(&method_text) {
        spec.protection_method_raw = raw;
        return;
    }
    let mut expressions = Vec::new();
    for call in phpquery::calls(method, &[]) {
        let receiver = phpquery::call_receiver(&call);
        if phpquery::call_method_name(&call) != "add"
            || receiver.is_none_or(|receiver| node_string(&receiver).trim() != "$protections")
        {
            spec.protection_method_raw = raw;
            return;
        }
        let Some(expression) = phpquery::argument_expression(&call, 0) else {
            spec.protection_method_raw = raw;
            return;
        };
        expressions.push(expression);
    }
    if phpquery::nodes(method, &[php::PHP_EXPRESSION_STATEMENT]).len() != expressions.len() {
        spec.protection_method_raw = raw;
        return;
    }
    for expression in &expressions {
        let text = node_string(expression).trim().to_owned();
        let creations = phpquery::object_creations(expression, &[]);
        if creations.len() != 1 || node_string(&creations[0]).trim() != text {
            spec.preserved_protections.push(text);
            continue;
        }
        apply_protection(spec, &creations[0], text);
    }
}

fn import_extension_fields(
    root: &SyntaxNode,
    method: &SyntaxNode,
    lookup: Option<RelationLookup>,
) -> Result<(Vec<FieldSpec>, Vec<String>)> {
    let mut expressions = Vec::new();
    for call in phpquery::calls(method, &[]) {
        if phpquery::call_method_name(&call) != "add" {
            continue;
        }
        let Some(receiver) = phpquery::call_receiver(&call) else {
            continue;
        };
        if node_string(&receiver).trim() != "$collection" {
            continue;
        }
        if let Some(argument) = phpquery::argument(&call, 0) {
            expressions.push(node_string(&argument).trim().to_owned());
        }
    }
    if expressions.is_empty() {
        if !phpquery::object_creations(method, &[]).is_empty() {
            return Err(errorf!(
                "entity extension fields are not literal $collection->add calls"
            ));
        }
        return Ok((Vec::new(), Vec::new()));
    }
    let fields = import_extension_field_expressions(root, &expressions, lookup)?;
    Ok((fields, expressions))
}

fn import_extension_field_expressions(
    root: &SyntaxNode,
    expressions: &[String],
    lookup: Option<RelationLookup>,
) -> Result<Vec<FieldSpec>> {
    if expressions.is_empty() {
        return Ok(Vec::new());
    }
    let mut synthetic = String::from("<?php declare(strict_types=1);\nnamespace ");
    synthetic.push_str(trim_backslash(&phpquery::namespace(root)));
    synthetic.push_str(";\n");
    for declaration in phpquery::use_declarations(root) {
        synthetic.push_str(node_string(&declaration).trim());
        synthetic.push('\n');
    }
    synthetic.push_str("class ShopwareLspImportedDefinition extends \\Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityDefinition {\n");
    synthetic.push_str("public const ENTITY_NAME = 'shopware_lsp_extension';\n");
    synthetic.push_str("protected function defineFields(): \\Shopware\\Core\\Framework\\DataAbstractionLayer\\FieldCollection { return new FieldCollection([\n");
    synthetic.push_str(&expressions.join(",\n"));
    synthetic.push_str("\n]); }\n}\n");
    let imported = import_definition(&synthetic, lookup)
        .map_err(|error| errorf!("import entity extension fields: {error}"))?;
    Ok(imported.fields)
}

fn returned_entity_name_definition_class(method: &SyntaxNode, resolve: &Resolver) -> String {
    let Some(returned) = single_return_only(Some(method)) else {
        return String::new();
    };
    let expression = returned_expression_text(&returned);
    for access in phpquery::nodes(&returned, &[php::PHP_SCOPED_ACCESS, php::PHP_MEMBER_ACCESS]) {
        if node_string(&access).trim() != expression {
            continue;
        }
        let class_name = phpquery::scoped_access_class(&access, "ENTITY_NAME");
        if !class_name.is_empty() {
            return resolve.resolve(&class_name);
        }
    }
    String::new()
}
