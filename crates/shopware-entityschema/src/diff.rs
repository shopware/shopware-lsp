//! Schema drift detection and rename resolution.

use std::collections::{BTreeMap, BTreeSet, HashMap, HashSet};

use serde::{Deserialize, Serialize};

use crate::model::*;
use crate::snapshot::Decision;

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct ColumnChange {
    pub entity: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub before: Option<Column>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub after: Option<Column>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct RenameCandidate {
    pub from: String,
    pub score: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct RenameQuestion {
    pub entity: String,
    pub added: String,
    #[serde(
        deserialize_with = "crate::canonical_json::nullable",
        serialize_with = "crate::canonical_json::null_if_empty"
    )]
    pub candidates: Vec<RenameCandidate>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct EntityRenameQuestion {
    pub added: String,
    #[serde(
        deserialize_with = "crate::canonical_json::nullable",
        serialize_with = "crate::canonical_json::null_if_empty"
    )]
    pub candidates: Vec<RenameCandidate>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct SchemaDiff {
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub created_entities: Vec<Entity>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub removed_entities: Vec<Entity>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub added_columns: Vec<ColumnChange>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub removed_columns: Vec<ColumnChange>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub changed_columns: Vec<ColumnChange>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub rename_questions: Vec<RenameQuestion>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub added_indexes: Vec<IndexChange>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub removed_indexes: Vec<IndexChange>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub added_foreign_keys: Vec<ForeignKeyChange>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub removed_foreign_keys: Vec<ForeignKeyChange>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub changed_primary_keys: Vec<PrimaryKeyChange>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub entity_rename_questions: Vec<EntityRenameQuestion>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct IndexChange {
    pub entity: String,
    pub index: Index,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct ForeignKeyChange {
    pub entity: String,
    pub foreign_key: ForeignKey,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct PrimaryKeyChange {
    pub entity: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub before: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub after: Vec<String>,
}

fn union_keys<V>(left: &BTreeMap<String, V>, right: &BTreeMap<String, V>) -> Vec<String> {
    left.keys()
        .chain(right.keys())
        .cloned()
        .collect::<BTreeSet<_>>()
        .into_iter()
        .collect()
}

pub fn diff_schemas(previous: &Schema, next: &Schema) -> SchemaDiff {
    let mut result = SchemaDiff::default();
    let mut created = Vec::new();
    let mut removed_entities = Vec::new();
    for entity_name in union_keys(&previous.entities, &next.entities) {
        let before = previous.entities.get(&entity_name);
        let after = next.entities.get(&entity_name);
        let (before, after) = match (before, after) {
            (None, Some(after)) => {
                created.push(after.clone());
                if after.external {
                    append_external_entity_added(&mut result, after);
                } else {
                    result.created_entities.push(after.clone());
                }
                continue;
            }
            (Some(before), None) => {
                removed_entities.push(before.clone());
                if before.external {
                    append_external_entity_removed(&mut result, before);
                } else {
                    result.removed_entities.push(before.clone());
                }
                continue;
            }
            (Some(before), Some(after)) => (before, after),
            (None, None) => continue,
        };
        let mut added = Vec::new();
        let mut removed = Vec::new();
        for name in union_keys(&before.columns, &after.columns) {
            match (before.columns.get(&name), after.columns.get(&name)) {
                (None, Some(new_column)) => {
                    result.added_columns.push(ColumnChange {
                        entity: entity_name.clone(),
                        before: None,
                        after: Some(new_column.clone()),
                    });
                    added.push(new_column.clone());
                }
                (Some(old_column), None) => {
                    result.removed_columns.push(ColumnChange {
                        entity: entity_name.clone(),
                        before: Some(old_column.clone()),
                        after: None,
                    });
                    removed.push(old_column.clone());
                }
                (Some(old_column), Some(new_column)) => {
                    if !same_database_column(old_column, new_column) {
                        result.changed_columns.push(ColumnChange {
                            entity: entity_name.clone(),
                            before: Some(old_column.clone()),
                            after: Some(new_column.clone()),
                        });
                    }
                }
                (None, None) => {}
            }
        }
        if !removed.is_empty() {
            for column in &added {
                let mut question = RenameQuestion {
                    entity: entity_name.clone(),
                    added: column.name.clone(),
                    candidates: removed
                        .iter()
                        .map(|candidate| RenameCandidate {
                            from: candidate.name.clone(),
                            score: column_similarity(candidate, column),
                        })
                        .collect(),
                };
                sort_candidates(&mut question.candidates);
                result.rename_questions.push(question);
            }
        }
        append_index_diff(
            &mut result,
            &entity_name,
            Some(&before.indexes),
            Some(&after.indexes),
        );
        append_foreign_key_diff(
            &mut result,
            &entity_name,
            Some(&before.foreign_keys),
            Some(&after.foreign_keys),
        );
        let before_primary = primary_columns(before);
        let after_primary = primary_columns(after);
        if before_primary != after_primary {
            result.changed_primary_keys.push(PrimaryKeyChange {
                entity: entity_name.clone(),
                before: before_primary,
                after: after_primary,
            });
        }
    }
    append_entity_rename_questions(&mut result, &created, &removed_entities);
    result
}

fn sort_candidates(candidates: &mut [RenameCandidate]) {
    candidates.sort_by(|left, right| {
        right
            .score
            .cmp(&left.score)
            .then_with(|| left.from.cmp(&right.from))
    });
}

fn append_entity_rename_questions(result: &mut SchemaDiff, created: &[Entity], removed: &[Entity]) {
    for added in created {
        if added.external {
            continue;
        }
        let mut question = EntityRenameQuestion {
            added: added.name.clone(),
            candidates: Vec::new(),
        };
        for candidate in removed {
            if candidate.external {
                continue;
            }
            let score = entity_similarity(candidate, added);
            if score < 60 {
                continue;
            }
            question.candidates.push(RenameCandidate {
                from: candidate.name.clone(),
                score,
            });
        }
        if question.candidates.is_empty() {
            continue;
        }
        sort_candidates(&mut question.candidates);
        result.entity_rename_questions.push(question);
    }
}

fn append_external_entity_added(result: &mut SchemaDiff, entity: &Entity) {
    for column in entity.columns.values() {
        result.added_columns.push(ColumnChange {
            entity: entity.name.clone(),
            before: None,
            after: Some(column.clone()),
        });
    }
    append_index_diff(result, &entity.name, None, Some(&entity.indexes));
    append_foreign_key_diff(result, &entity.name, None, Some(&entity.foreign_keys));
}

fn append_external_entity_removed(result: &mut SchemaDiff, entity: &Entity) {
    for column in entity.columns.values() {
        result.removed_columns.push(ColumnChange {
            entity: entity.name.clone(),
            before: Some(column.clone()),
            after: None,
        });
    }
    append_index_diff(result, &entity.name, Some(&entity.indexes), None);
    append_foreign_key_diff(result, &entity.name, Some(&entity.foreign_keys), None);
}

fn append_index_diff(
    result: &mut SchemaDiff,
    entity: &str,
    before: Option<&BTreeMap<String, Index>>,
    after: Option<&BTreeMap<String, Index>>,
) {
    let empty = BTreeMap::new();
    let before = before.unwrap_or(&empty);
    let after = after.unwrap_or(&empty);
    for name in union_keys(before, after) {
        let old_index = before.get(&name);
        let new_index = after.get(&name);
        if let (Some(old_index), Some(new_index)) = (old_index, new_index)
            && same_index(old_index, new_index)
        {
            continue;
        }
        if let Some(old_index) = old_index {
            result.removed_indexes.push(IndexChange {
                entity: entity.to_owned(),
                index: old_index.clone(),
            });
        }
        if let Some(new_index) = new_index {
            result.added_indexes.push(IndexChange {
                entity: entity.to_owned(),
                index: new_index.clone(),
            });
        }
    }
}

fn append_foreign_key_diff(
    result: &mut SchemaDiff,
    entity: &str,
    before: Option<&BTreeMap<String, ForeignKey>>,
    after: Option<&BTreeMap<String, ForeignKey>>,
) {
    let empty = BTreeMap::new();
    let before = before.unwrap_or(&empty);
    let after = after.unwrap_or(&empty);
    for name in union_keys(before, after) {
        let old_key = before.get(&name);
        let new_key = after.get(&name);
        if let (Some(old_key), Some(new_key)) = (old_key, new_key)
            && same_foreign_key(old_key, new_key)
        {
            continue;
        }
        if let Some(old_key) = old_key {
            result.removed_foreign_keys.push(ForeignKeyChange {
                entity: entity.to_owned(),
                foreign_key: old_key.clone(),
            });
        }
        if let Some(new_key) = new_key {
            result.added_foreign_keys.push(ForeignKeyChange {
                entity: entity.to_owned(),
                foreign_key: new_key.clone(),
            });
        }
    }
}

impl SchemaDiff {
    pub fn destructive(&self) -> bool {
        !self.removed_entities.is_empty()
            || !self.removed_columns.is_empty()
            || !self.removed_indexes.is_empty()
            || !self.removed_foreign_keys.is_empty()
            || !self.changed_columns.is_empty()
            || !self.changed_primary_keys.is_empty()
    }

    pub fn database_changed(&self) -> bool {
        !self.created_entities.is_empty()
            || !self.removed_entities.is_empty()
            || !self.entity_rename_questions.is_empty()
            || !self.added_columns.is_empty()
            || !self.removed_columns.is_empty()
            || !self.changed_columns.is_empty()
            || !self.added_indexes.is_empty()
            || !self.removed_indexes.is_empty()
            || !self.added_foreign_keys.is_empty()
            || !self.removed_foreign_keys.is_empty()
            || !self.changed_primary_keys.is_empty()
    }
}

pub(crate) fn primary_columns(entity: &Entity) -> Vec<String> {
    entity
        .columns
        .iter()
        .filter(|(_, column)| column.primary_key)
        .map(|(name, _)| name.clone())
        .collect()
}

pub fn resolve_rename_questions(
    diff: SchemaDiff,
    decisions: &[Decision],
) -> Result<(SchemaDiff, Vec<Decision>)> {
    let mut by_target: HashMap<String, &Decision> = HashMap::new();
    let mut used_sources = HashSet::new();
    for decision in decisions {
        if decision.kind != "columnRename" && decision.kind != "columnCreate" {
            continue;
        }
        let key = format!("{}\0{}", decision.entity, decision.to);
        if by_target.contains_key(&key) {
            return Err(errorf!(
                "duplicate decision for {}.{}",
                decision.entity,
                decision.to
            ));
        }
        if decision.kind == "columnRename"
            && !used_sources.insert(format!("{}\0{}", decision.entity, decision.from))
        {
            return Err(errorf!(
                "column {}.{} is used by more than one rename",
                decision.entity,
                decision.from
            ));
        }
        by_target.insert(key, decision);
    }
    let mut normalized = Vec::new();
    for question in &diff.rename_questions {
        let Some(decision) = by_target.get(&format!("{}\0{}", question.entity, question.added))
        else {
            return Err(errorf!(
                "unresolved column change for {}.{}",
                question.entity,
                question.added
            ));
        };
        if decision.kind == "columnRename"
            && !question
                .candidates
                .iter()
                .any(|candidate| candidate.from == decision.from)
        {
            return Err(errorf!(
                "{}.{} is not a rename candidate for {}",
                question.entity,
                decision.from,
                question.added
            ));
        }
        normalized.push((*decision).clone());
    }
    Ok((diff, normalized))
}

/// Applies explicit technical-entity rename decisions before calculating
/// column-level changes, so a table rename is never a destructive
/// drop/create pair.
pub fn resolve_schema_diff(
    previous: &Schema,
    next: &Schema,
    decisions: &[Decision],
) -> (Schema, SchemaDiff, Result<Vec<Decision>>) {
    let raw = diff_schemas(previous, next);
    let entity_decisions = match resolve_entity_rename_questions(&raw, decisions) {
        Ok(decisions) => decisions,
        Err(error) => return (previous.clone(), raw, Err(error)),
    };
    let resolved_previous = match apply_entity_rename_decisions(previous, next, &entity_decisions) {
        Ok(schema) => schema,
        Err(error) => return (previous.clone(), raw, Err(error)),
    };
    let mut resolved = diff_schemas(&resolved_previous, next);
    resolved.entity_rename_questions = raw.entity_rename_questions;
    let column_decisions = match resolve_rename_questions(resolved.clone(), decisions) {
        Ok((_, decisions)) => decisions,
        Err(error) => return (resolved_previous, resolved, Err(error)),
    };
    let mut normalized = entity_decisions;
    normalized.extend(column_decisions);
    (resolved_previous, resolved, Ok(normalized))
}

fn resolve_entity_rename_questions(
    diff: &SchemaDiff,
    decisions: &[Decision],
) -> Result<Vec<Decision>> {
    let mut by_target: HashMap<&str, &Decision> = HashMap::new();
    let mut used_sources = HashSet::new();
    for decision in decisions {
        if decision.kind != "entityRename" && decision.kind != "entityCreate" {
            continue;
        }
        if by_target.contains_key(decision.to.as_str()) {
            return Err(errorf!("duplicate decision for entity {}", decision.to));
        }
        if decision.kind == "entityRename" && !used_sources.insert(decision.from.as_str()) {
            return Err(errorf!(
                "entity {} is used by more than one rename",
                decision.from
            ));
        }
        by_target.insert(&decision.to, decision);
    }
    let mut result = Vec::with_capacity(diff.entity_rename_questions.len());
    for question in &diff.entity_rename_questions {
        let Some(decision) = by_target.get(question.added.as_str()) else {
            return Err(errorf!("unresolved entity change for {}", question.added));
        };
        if decision.kind == "entityRename"
            && !question
                .candidates
                .iter()
                .any(|candidate| candidate.from == decision.from)
        {
            return Err(errorf!(
                "{} is not a rename candidate for entity {}",
                decision.from,
                question.added
            ));
        }
        result.push((*decision).clone());
    }
    Ok(result)
}

fn apply_entity_rename_decisions(
    previous: &Schema,
    next: &Schema,
    decisions: &[Decision],
) -> Result<Schema> {
    let mut result = previous.clone();
    for decision in decisions {
        if decision.kind != "entityRename" {
            continue;
        }
        if !result.entities.contains_key(&decision.from) {
            return Err(errorf!(
                "rename source entity {} does not exist",
                decision.from
            ));
        }
        let Some(after) = next.entities.get(&decision.to) else {
            return Err(errorf!(
                "rename target entity {} does not exist",
                decision.to
            ));
        };
        if result.entities.contains_key(&decision.to) && decision.from != decision.to {
            return Err(errorf!(
                "rename target entity {} already exists",
                decision.to
            ));
        }
        let mut before = result.entities.remove(&decision.from).unwrap_or_default();
        before.name = decision.to.clone();
        before.external = after.external;
        result.entities.insert(decision.to.clone(), before);
        for entity in result.entities.values_mut() {
            for foreign_key in entity.foreign_keys.values_mut() {
                if foreign_key.reference_entity == decision.from {
                    foreign_key.reference_entity = decision.to.clone();
                }
            }
        }
    }
    Ok(result)
}

pub(crate) fn column_similarity(left: &Column, right: &Column) -> i64 {
    let mut score = 0;
    if left.sql_type == right.sql_type {
        score += 60;
    } else if sql_type_family(&left.sql_type) == sql_type_family(&right.sql_type) {
        score += 25;
    }
    if left.not_null == right.not_null {
        score += 15;
    }
    if left.primary_key == right.primary_key {
        score += 15;
    }
    if left.auto_increment == right.auto_increment {
        score += 5;
    }
    score
}

/// Greedy column matching, iterating both column maps sorted by column name
/// so the score is deterministic.
pub(crate) fn entity_similarity(left: &Entity, right: &Entity) -> i64 {
    if left.columns.is_empty() || right.columns.is_empty() {
        return 0;
    }
    let left_columns: Vec<&Column> = left.columns.values().collect();
    let mut used = vec![false; left_columns.len()];
    let mut total = 0;
    for (name, column) in &right.columns {
        let mut best_index = None;
        let mut best = -1;
        for (index, candidate) in left_columns.iter().enumerate() {
            if used[index] {
                continue;
            }
            let mut score = column_similarity(candidate, column);
            if candidate.name == *name {
                score += 5;
            }
            if score > best {
                best_index = Some(index);
                best = score;
            }
        }
        if let Some(index) = best_index {
            used[index] = true;
            total += best;
        }
    }
    let denominator = left.columns.len().max(right.columns.len()) as i64;
    let mut score = total * 80 / (denominator * 100);
    if primary_columns(left) == primary_columns(right) {
        score += 10;
    }
    if left.columns.len() == right.columns.len() {
        score += 5;
    }
    if left.external == right.external {
        score += 5;
    }
    score.min(100)
}

fn sql_type_family(value: &str) -> String {
    let upper = value.to_uppercase();
    let base = match upper.find('(') {
        Some(index) => &upper[..index],
        None => &upper,
    };
    match base {
        "VARCHAR" | "LONGTEXT" | "TEXT" => "text".to_owned(),
        "INT" | "INTEGER" | "BIGINT" | "TINYINT" | "DOUBLE" | "FLOAT" | "DECIMAL" => {
            "number".to_owned()
        }
        "DATE" | "DATETIME" | "TIMESTAMP" => "date".to_owned(),
        _ => base.to_owned(),
    }
}
