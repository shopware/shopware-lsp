//! Associations that entity extensions add to another definition.
//!
//! An extension is stored as its own record of kind
//! [`DEFINITION_KIND_EXTENSION`], so the definition it extends stays as its
//! own file declares it. [`apply_extensions`] merges the two for the
//! consumers that want the fields an entity has at runtime.

use std::collections::BTreeMap;

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax as php;
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::eq_ignore_case;

use crate::index::{ClassResolver, Definition, Field, parse_fields, short_name};

pub const DEFINITION_KIND_EXTENSION: &str = "extension";

/// The class constant that holds the name of every classic definition.
const ENTITY_NAME_CONSTANT: &str = "ENTITY_NAME";

/// Resolves the names an extension gives by class constant.
pub(crate) struct ConstantNames<'a> {
    resolver: &'a ClassResolver,
    /// The fully qualified class being parsed.
    class: String,
    /// Its own string constants.
    constants: BTreeMap<String, String>,
}

impl ConstantNames<'_> {
    /// The name `expression` stands for: the value of a constant of the
    /// class itself, or a `Class::CONSTANT` reference that
    /// [`apply_extensions`] resolves once every definition is known.
    pub(crate) fn name(&self, expression: &SyntaxNode) -> Option<String> {
        if !matches!(
            expression.kind(),
            php::PHP_SCOPED_ACCESS | php::PHP_MEMBER_ACCESS
        ) {
            return None;
        }
        let text: String = expression.text().to_string().split_whitespace().collect();
        let (class, constant) = text.split_once("::")?;
        if constant.is_empty() || eq_ignore_case(constant, "class") || constant.contains(['(', '$'])
        {
            return None;
        }
        let own = ["self", "static"]
            .iter()
            .any(|keyword| eq_ignore_case(class, keyword));
        let class = if own {
            self.class.clone()
        } else {
            self.resolver.resolve(class)
        };
        if class == self.class {
            return self.constants.get(constant).cloned();
        }
        Some(format!("{class}::{constant}"))
    }
}

/// The string constants a class declares.
pub(crate) fn string_constants(class: &SyntaxNode) -> BTreeMap<String, String> {
    let mut constants = BTreeMap::new();
    for declaration in phpquery::nodes(class, &[php::PHP_CLASS_CONST_DECLARATION]) {
        // `const A = 'a', B = 'b';` pairs each name with the value after it.
        let mut name = None;
        for child in declaration.children() {
            match child.kind() {
                php::PHP_NAME => name = Some(child.text().to_string().trim().to_owned()),
                php::PHP_STRING => {
                    if let Some(name) = name.take() {
                        constants.insert(name, phpquery::string_value(&child));
                    }
                }
                _ => {}
            }
        }
    }
    constants
}

/// The extension records of one class: one for an `EntityExtension`, one
/// per yielded entity for a `BulkEntityExtension`.
pub(crate) fn parse_extensions(
    class: &SyntaxNode,
    root: &SyntaxNode,
    file: &str,
    lines: &LineIndex,
) -> Vec<Definition> {
    let class_name = phpquery::class_name(class);
    if class_name.is_empty() || phpquery::is_abstract(class) {
        return Vec::new();
    }
    let resolver = ClassResolver::new(root);
    let fully_qualified_class = resolver.resolve(&class_name);
    let names = ConstantNames {
        resolver: &resolver,
        class: fully_qualified_class.clone(),
        constants: string_constants(class),
    };
    let class_range = class.range_trimmed_trivia();
    let (line, _) = lines.position(class_range.start().into());
    let record = |target: String, fields: Vec<Field>| Definition {
        name: target,
        kind: DEFINITION_KIND_EXTENSION.to_owned(),
        class: class_name.clone(),
        fully_qualified_class: fully_qualified_class.clone(),
        file: file.to_owned(),
        line: line + 1,
        class_range,
        fields,
        ..Definition::default()
    };
    let associations = |scope: &SyntaxNode| -> Vec<Field> {
        parse_fields(scope, lines, &resolver, Some(&names))
            .into_iter()
            .filter(|field| field.association)
            .collect()
    };

    let bulk = phpquery::class_extends(class)
        .iter()
        .any(|parent| short_name(parent) == "BulkEntityExtension");
    let mut target = String::new();
    let mut fields = Vec::new();
    let mut records = Vec::new();
    for method in phpquery::methods(class) {
        match phpquery::method_name(&method).as_str() {
            "collect" if bulk => {
                for entry in phpquery::nodes(&method, &[php::PHP_YIELD_EXPRESSION]) {
                    let mut children = entry.children();
                    let (Some(key), Some(value)) = (children.next(), children.next()) else {
                        continue;
                    };
                    let Some(target) = target_name(&key, &names) else {
                        continue;
                    };
                    let fields = associations(&value);
                    if !fields.is_empty() {
                        records.push(record(target, fields));
                    }
                }
            }
            "extendFields" => fields = associations(&method),
            "getEntityName" => {
                target = phpquery::nodes(
                    &method,
                    &[
                        php::PHP_STRING,
                        php::PHP_SCOPED_ACCESS,
                        php::PHP_MEMBER_ACCESS,
                    ],
                )
                .iter()
                .find_map(|node| target_name(node, &names))
                .unwrap_or_default();
            }
            // Before `getEntityName()` an extension named the definition.
            "getDefinitionClass" if target.is_empty() => {
                target =
                    phpquery::nodes(&method, &[php::PHP_SCOPED_ACCESS, php::PHP_MEMBER_ACCESS])
                        .iter()
                        .map(phpquery::class_constant_name)
                        .find(|class| !class.is_empty())
                        .map(|class| resolver.resolve(&class))
                        .unwrap_or_default();
            }
            _ => {}
        }
    }
    if !target.is_empty() && !fields.is_empty() {
        records.push(record(target, fields));
    }
    records
}

/// The entity an extension names: a literal, or a constant reference.
fn target_name(node: &SyntaxNode, names: &ConstantNames<'_>) -> Option<String> {
    if node.kind() == php::PHP_STRING {
        return Some(phpquery::string_value(node)).filter(|name| !name.is_empty());
    }
    names.name(node)
}

/// Adds the associations of `extensions` to the definitions they extend.
/// Each added field carries the file that declares it.
pub(crate) fn apply_extensions(definitions: &mut [Definition], mut extensions: Vec<Definition>) {
    extensions.sort_by(|left, right| {
        (&left.file, left.line, &left.name).cmp(&(&right.file, right.line, &right.name))
    });
    for extension in extensions {
        let Some(target) = resolve_name(definitions, &extension.name)
            .and_then(|name| target_index(definitions, &name))
        else {
            continue;
        };
        for mut field in extension.fields {
            let Some(name) = resolve_name(definitions, &field.name) else {
                continue;
            };
            if definitions[target]
                .fields
                .iter()
                .any(|existing| existing.name == name)
            {
                continue;
            }
            field.name = name;
            field.source_file.clone_from(&extension.file);
            definitions[target].fields.push(field);
        }
    }
}

/// The value of a `Class::CONSTANT` reference; any other name is itself.
fn resolve_name(definitions: &[Definition], name: &str) -> Option<String> {
    let Some((class, constant)) = name.split_once("::") else {
        return Some(name.to_owned());
    };
    let definition = by_class(definitions, class)?;
    definition.constants.get(constant).cloned().or_else(|| {
        // The name of an entity whose constant the index did not read.
        (constant == ENTITY_NAME_CONSTANT).then(|| definition.name.clone())
    })
}

fn by_class<'a>(definitions: &'a [Definition], class: &str) -> Option<&'a Definition> {
    let class = class.trim_start_matches('\\');
    definitions
        .iter()
        .find(|definition| definition.fully_qualified_class.trim_start_matches('\\') == class)
}

/// The definition `name` stands for: an entity name, or the class a
/// `getDefinitionClass()` extension names.
fn target_index(definitions: &[Definition], name: &str) -> Option<usize> {
    definitions
        .iter()
        .enumerate()
        .filter(|(_, definition)| definition.name == name)
        .min_by_key(|(_, definition)| definition.kind != "entity")
        .map(|(index, _)| index)
        .or_else(|| {
            let class = name.trim_start_matches('\\');
            definitions.iter().position(|definition| {
                definition.fully_qualified_class.trim_start_matches('\\') == class
            })
        })
}
