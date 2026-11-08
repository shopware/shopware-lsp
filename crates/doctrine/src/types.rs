//! Conventional custom DBAL `Type` subclasses (port of Go `types.go`).

use std::collections::HashSet;

use swlsp_php::PhpIndex;
use swlsp_php_semantic::{ConstantReturn, Symbol};
use swlsp_php_types::{Kind as TypeKind, equal_fold, go_to_lower};
use swlsp_syntax::TextRange;

use crate::helpers::go_trim_space;

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TypeDeclaration {
    pub name: String,
    pub class: String,
    pub file: String,
    pub range: TextRange,
}

/// Discovers custom DBAL `Type` subclasses. Doctrine allows an arbitrary
/// runtime registration name, but the conventional `FooBarType` => `foo_bar`
/// spelling (or a literal `getName()` return) gives useful completion and
/// navigation without executing project code.
pub fn type_declarations(index: &PhpIndex) -> Vec<TypeDeclaration> {
    let snapshot = index.semantic_snapshot();
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    for symbol in index.class_symbols_view().iter() {
        if !snapshot.is_subtype_of(&symbol.fully_qualified, "Doctrine\\DBAL\\Types\\Type") {
            continue;
        }
        let mut name = doctrine_type_declared_name(index, &symbol.fully_qualified);
        if name.is_empty() {
            let short = symbol.name.strip_suffix("Type").unwrap_or(&symbol.name);
            name = snake_case_doctrine_type(short);
        }
        if name.is_empty() {
            continue;
        }
        if !seen.insert(go_to_lower(&format!("{name}|{}", symbol.fully_qualified))) {
            continue;
        }
        result.push(TypeDeclaration {
            name,
            class: symbol.fully_qualified.to_string(),
            file: symbol.path.to_string(),
            range: symbol.selection_range,
        });
    }
    sort_declarations(&mut result);
    result
}

pub(crate) fn sort_declarations(declarations: &mut [TypeDeclaration]) {
    declarations.sort_by(|left, right| {
        left.name
            .cmp(&right.name)
            .then_with(|| left.class.cmp(&right.class))
    });
}

fn doctrine_type_declared_name(index: &PhpIndex, class: &str) -> String {
    for method in index.methods(class) {
        if !equal_fold(&method.name, "getName") {
            continue;
        }
        for literal in method.literal_returns() {
            if literal.ty.kind() != TypeKind::LiteralString {
                continue;
            }
            let name = go_trim_space(&literal.value);
            if !name.is_empty() {
                return name.to_owned();
            }
        }
        for returned in method.constant_returns() {
            let receiver = doctrine_constant_return_receiver(index, &method, returned);
            if receiver.is_empty() {
                continue;
            }
            for constant in index.find_constants(&receiver, &returned.name) {
                if constant.ty.kind() != TypeKind::LiteralString {
                    continue;
                }
                let name = go_trim_space(constant.ty.name());
                if !name.is_empty() {
                    return name.to_owned();
                }
            }
        }
        return String::new();
    }
    String::new()
}

fn doctrine_constant_return_receiver(
    index: &PhpIndex,
    method: &Symbol,
    returned: &ConstantReturn,
) -> String {
    let receiver = go_trim_space(&returned.receiver);
    match go_to_lower(receiver).as_str() {
        "self" | "static" | "parent" => {
            let snapshot = index.semantic_snapshot();
            let Some(owner) = snapshot.symbol(method.container.as_str()) else {
                return String::new();
            };
            if !equal_fold(receiver, "parent") {
                return owner.fully_qualified.to_string();
            }
            owner
                .extends()
                .first()
                .map(ToString::to_string)
                .unwrap_or_default()
        }
        _ => receiver.strip_prefix('\\').unwrap_or(receiver).to_owned(),
    }
}

fn lower_char(character: char) -> char {
    character.to_lowercase().next().unwrap_or(character)
}

pub(crate) fn snake_case_doctrine_type(value: &str) -> String {
    let mut result = String::new();
    let mut previous_lower = false;
    for character in value.chars() {
        if character.is_uppercase() {
            if previous_lower && !result.is_empty() {
                result.push('_');
            }
            result.push(lower_char(character));
            previous_lower = false;
            continue;
        }
        result.push(lower_char(character));
        previous_lower = character.is_alphabetic() || character.is_numeric();
    }
    result.trim_matches('_').to_owned()
}
