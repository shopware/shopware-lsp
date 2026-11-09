//! Doctrine DBAL table, column and join alias intelligence on
//! `Connection`/`QueryBuilder` calls.

use std::collections::{BTreeSet, HashSet};

use swlsp_parser_php::query as phpquery;
use swlsp_php::PhpContext;
use swlsp_php_types::Type;
use swlsp_shopware_dal::{Definition, Field};
use swlsp_syntax::{SyntaxNode, TextRange};
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::index::Index;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum DbalReferenceRole {
    #[default]
    Table,
    Column,
    Alias,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct DbalReference {
    pub role: DbalReferenceRole,
    pub name: String,
    pub table: String,
    pub node: SyntaxNode,
    pub range: TextRange,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum DbalCompletionKind {
    #[default]
    Table,
    Column,
    Alias,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct DbalCompletion {
    pub label: String,
    pub detail: String,
    pub kind: DbalCompletionKind,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Receiver {
    QueryBuilder,
    Connection,
}

fn string_argument_value(call: &SyntaxNode, index: usize) -> String {
    phpquery::string_argument(call, index)
        .map(|node| phpquery::string_value(&node))
        .unwrap_or_default()
}

fn is_argument(call: &SyntaxNode, index: usize, literal: &SyntaxNode) -> bool {
    phpquery::argument_expression(call, index).as_ref() == Some(literal)
}

fn column_name(field: &Field) -> &str {
    if field.storage_name.is_empty() {
        &field.name
    } else {
        &field.storage_name
    }
}

impl Index {
    /// The DBAL table, column or join alias string under `node`.
    pub fn dbal_reference_at(
        &self,
        context: Option<&PhpContext>,
        _root: &SyntaxNode,
        node: &SyntaxNode,
    ) -> Option<DbalReference> {
        let literal = phpquery::string_at(node)?;
        let call = phpquery::call_at(&literal)?;
        let receiver = dbal_receiver(context, &call)?;
        let method = lowercase(&phpquery::call_method_name(&call));
        let argument = phpquery::argument_index(&call, &literal);
        let mut reference = DbalReference {
            role: DbalReferenceRole::Table,
            name: phpquery::string_value(&literal),
            table: String::new(),
            range: phpquery::string_content_range(&literal),
            node: literal.clone(),
        };

        if receiver == Receiver::QueryBuilder {
            match method.as_str() {
                "update" | "insert" | "from" | "delete" => {
                    if argument == Some(0) && is_argument(&call, 0, &literal) {
                        return Some(reference);
                    }
                }
                "join" | "innerjoin" | "leftjoin" | "rightjoin" => {
                    if argument == Some(1) && is_argument(&call, 1, &literal) {
                        return Some(reference);
                    }
                    if argument == Some(2) && is_argument(&call, 2, &literal) {
                        reference.role = DbalReferenceRole::Alias;
                        reference.table = string_argument_value(&call, 1);
                        return Some(reference);
                    }
                }
                _ => {}
            }
            return None;
        }

        if method != "insert" && method != "update" {
            return None;
        }
        if argument == Some(0) && is_argument(&call, 0, &literal) {
            return Some(reference);
        }
        if argument != Some(1) || !is_dbal_column_array_literal(&call, &literal) {
            return None;
        }
        reference.role = DbalReferenceRole::Column;
        reference.table = string_argument_value(&call, 0);
        (!reference.table.is_empty()).then_some(reference)
    }

    /// Completion candidates for the DBAL string under `node`.
    pub fn dbal_completions_at(
        &self,
        context: Option<&PhpContext>,
        root: &SyntaxNode,
        node: &SyntaxNode,
    ) -> Vec<DbalCompletion> {
        let Some(reference) = self.dbal_reference_at(context, root, node) else {
            return Vec::new();
        };
        match reference.role {
            DbalReferenceRole::Table => {
                let Some(dal) = self.dal() else {
                    return Vec::new();
                };
                let Ok(models) = dal.definitions() else {
                    return Vec::new();
                };
                let mut result: Vec<DbalCompletion> = models
                    .into_iter()
                    .filter(|model| !model.name.is_empty())
                    .map(|model| DbalCompletion {
                        label: model.name,
                        detail: model.class,
                        kind: DbalCompletionKind::Table,
                    })
                    .collect();
                result.sort_by(|left, right| {
                    left.label
                        .cmp(&right.label)
                        .then_with(|| left.detail.cmp(&right.detail))
                });
                result.dedup_by(|current, previous| current.label == previous.label);
                result
            }
            DbalReferenceRole::Column => {
                let Ok(Some(model)) = self.definition_for_table(&reference.table) else {
                    return Vec::new();
                };
                let mut seen = HashSet::new();
                let mut result = Vec::new();
                for field in &model.fields {
                    if field.association {
                        continue;
                    }
                    let name = column_name(field);
                    if name.is_empty() || !seen.insert(lowercase(name)) {
                        continue;
                    }
                    result.push(DbalCompletion {
                        label: name.to_owned(),
                        detail: format!("{}::{}", model.class, field.name),
                        kind: DbalCompletionKind::Column,
                    });
                }
                result.sort_by(|left, right| left.label.cmp(&right.label));
                result
            }
            DbalReferenceRole::Alias => {
                let from_alias = phpquery::call_at(&reference.node)
                    .map(|call| string_argument_value(&call, 0))
                    .unwrap_or_default();
                dbal_alias_suggestions(&from_alias, &reference.table)
                    .into_iter()
                    .map(|alias| DbalCompletion {
                        label: alias,
                        detail: "Doctrine DBAL join alias".to_owned(),
                        kind: DbalCompletionKind::Alias,
                    })
                    .collect()
            }
        }
    }

    /// The DAL definition backing a physical table, preferring the first by
    /// file and class position.
    pub fn definition_for_table(&self, table: &str) -> swlsp_indexer::Result<Option<Definition>> {
        let Some(dal) = self.dal() else {
            return Ok(None);
        };
        let mut definitions = dal.definition(table)?;
        definitions.sort_by(|left, right| {
            left.file
                .cmp(&right.file)
                .then_with(|| left.class_range.start().cmp(&right.class_range.start()))
        });
        Ok(definitions.into_iter().next())
    }

    /// The non-association field stored in `column` of `table`.
    pub fn field_for_column(
        &self,
        table: &str,
        column: &str,
    ) -> swlsp_indexer::Result<Option<(Definition, Field)>> {
        let Some(definition) = self.definition_for_table(table)? else {
            return Ok(None);
        };
        let field = definition
            .fields
            .iter()
            .find(|field| !field.association && eq_ignore_case(column_name(field), column))
            .cloned();
        Ok(field.map(|field| (definition, field)))
    }
}

fn dbal_receiver(context: Option<&PhpContext>, call: &SyntaxNode) -> Option<Receiver> {
    let context = context?;
    let receiver = phpquery::call_receiver(call)?;
    let receiver_type = context.document.type_of(&receiver).ty;
    if receiver_type.is_unknown() {
        return None;
    }
    let relations = context.snapshot.relations();
    if relations.is_subtype(
        &receiver_type,
        &Type::named("Doctrine\\DBAL\\Query\\QueryBuilder", Vec::new()),
    ) {
        return Some(Receiver::QueryBuilder);
    }
    if relations.is_subtype(
        &receiver_type,
        &Type::named("Doctrine\\DBAL\\Connection", Vec::new()),
    ) {
        return Some(Receiver::Connection);
    }
    None
}

fn is_dbal_column_array_literal(call: &SyntaxNode, literal: &SyntaxNode) -> bool {
    let Some(array) =
        phpquery::argument_expression(call, 1).and_then(|node| phpquery::array_at(&node))
    else {
        return false;
    };
    if phpquery::array_at(literal).as_ref() != Some(&array) {
        return false;
    }
    let Some(item) = phpquery::array_item_at(literal) else {
        return false;
    };
    if let Some(key) = phpquery::array_item_key(&item) {
        return phpquery::string_at(&key).as_ref() == Some(literal);
    }
    phpquery::array_item_value(&item)
        .and_then(|value| phpquery::string_at(&value))
        .as_ref()
        == Some(literal)
}

fn is_separator(character: char) -> bool {
    character == '_' || character == '-' || character.is_whitespace()
}

fn fields(value: &str) -> impl Iterator<Item = &str> {
    value.split(is_separator).filter(|part| !part.is_empty())
}

fn first_char(value: &str) -> &str {
    value
        .char_indices()
        .nth(1)
        .map_or(value, |(index, _)| &value[..index])
}

fn dbal_alias_suggestions(from_alias: &str, table: &str) -> Vec<String> {
    let table = table.trim();
    if table.is_empty() {
        return Vec::new();
    }
    let mut seen = BTreeSet::new();
    let mut add = |value: String| {
        if !value.is_empty() {
            seen.insert(value);
        }
    };
    add(table.to_owned());
    add(dbal_camelize(table));
    add(first_char(table).to_owned());
    add(dbal_initials(table));
    if !from_alias.is_empty() {
        add(format!("{from_alias}_{table}"));
        add(dbal_camelize(&format!("{from_alias}_{table}")));
    }
    seen.into_iter().collect()
}

fn dbal_camelize(value: &str) -> String {
    let mut parts = fields(value);
    let Some(first) = parts.next() else {
        return String::new();
    };
    let mut result = lowercase(first);
    for part in parts {
        let lower = lowercase(part);
        let head = first_char(&lower);
        result.push_str(&head.to_uppercase());
        result.push_str(&lower[head.len()..]);
    }
    result
}

fn dbal_initials(value: &str) -> String {
    fields(value).map(first_char).collect()
}
