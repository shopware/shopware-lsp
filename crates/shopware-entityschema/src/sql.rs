//! MySQL migration statements.

use std::collections::{HashMap, HashSet};

use crate::diff::{SchemaDiff, resolve_schema_diff};
use crate::model::*;
use crate::snapshot::Decision;
use crate::validate_spec::valid_backfill_expression;

/// The ordered statements migrating `previous` to `next`; the diff is
/// returned even when the decisions are rejected.
pub fn migration_statements(
    previous: &Schema,
    next: &Schema,
    decisions: &[Decision],
) -> (Result<Vec<String>>, SchemaDiff) {
    let (resolved_previous, diff, normalized) = resolve_schema_diff(previous, next, decisions);
    let normalized = match normalized {
        Ok(normalized) => normalized,
        Err(error) => return (Err(error), diff),
    };
    let mut builder = MigrationSqlBuilder {
        original_previous: previous,
        previous: &resolved_previous,
        next,
        diff: &diff,
        decision_by_target: HashMap::new(),
        renamed_from: HashSet::new(),
        renamed_to: HashSet::new(),
        dropped_json: HashSet::new(),
        entity_renames: Vec::new(),
        statements: Vec::new(),
    };
    for decision in &normalized {
        builder.decision_by_target.insert(
            format!("{}\0{}", decision.entity, decision.to),
            decision.clone(),
        );
        if decision.kind == "entityRename" {
            builder.entity_renames.push(decision.clone());
        }
    }
    let result = builder.build();
    (result, diff)
}

struct MigrationSqlBuilder<'a> {
    original_previous: &'a Schema,
    previous: &'a Schema,
    next: &'a Schema,
    diff: &'a SchemaDiff,
    decision_by_target: HashMap<String, Decision>,
    renamed_from: HashSet<String>,
    renamed_to: HashSet<String>,
    dropped_json: HashSet<String>,
    entity_renames: Vec<Decision>,
    statements: Vec<String>,
}

impl MigrationSqlBuilder<'_> {
    fn build(mut self) -> Result<Vec<String>> {
        self.rename_entities();
        self.drop_foreign_keys_and_indexes();
        self.drop_primary_keys();
        self.create_and_drop_entities();
        self.rename_columns()?;
        self.change_columns()?;
        self.add_columns()?;
        self.remove_columns();
        self.add_primary_keys_and_constraints();
        Ok(self.statements)
    }

    fn rename_entities(&mut self) {
        for decision in self.entity_renames.clone() {
            self.statements.push(format!(
                "RENAME TABLE {} TO {};",
                sql_ident(&decision.from),
                sql_ident(&decision.to)
            ));
            let Some(before) = self.original_previous.entities.get(&decision.from) else {
                continue;
            };
            let after = self.next.entities.get(&decision.to);
            for (name, column) in &before.columns {
                if !is_json_column(column) {
                    continue;
                }
                self.statements.push(drop_named_json_constraint_sql(
                    &decision.to,
                    &json_constraint_name(&decision.from, name),
                ));
                self.dropped_json.insert(format!("{}\0{name}", decision.to));
                if after
                    .and_then(|after| after.columns.get(name))
                    .is_some_and(is_json_column)
                {
                    self.statements
                        .push(json_constraint_sql(&decision.to, name));
                }
            }
        }
    }

    fn drop_foreign_keys_and_indexes(&mut self) {
        for change in &self.diff.removed_foreign_keys {
            self.statements.push(format!(
                "ALTER TABLE {} DROP FOREIGN KEY {};",
                sql_ident(&change.entity),
                sql_ident(&change.foreign_key.name)
            ));
        }
        for change in &self.diff.removed_indexes {
            self.statements.push(format!(
                "ALTER TABLE {} DROP INDEX {};",
                sql_ident(&change.entity),
                sql_ident(&change.index.name)
            ));
        }
    }

    fn drop_primary_keys(&mut self) {
        for change in &self.diff.changed_primary_keys {
            if !change.before.is_empty() {
                self.statements.push(format!(
                    "ALTER TABLE {} DROP PRIMARY KEY;",
                    sql_ident(&change.entity)
                ));
            }
        }
    }

    fn create_and_drop_entities(&mut self) {
        for entity in &self.diff.created_entities {
            self.statements.push(create_table_sql(entity));
        }
        for entity in &self.diff.removed_entities {
            self.statements
                .push(format!("DROP TABLE IF EXISTS {};", sql_ident(&entity.name)));
        }
    }

    fn rename_columns(&mut self) -> Result<()> {
        for change in &self.diff.added_columns {
            let Some(after) = &change.after else {
                continue;
            };
            let Some(decision) = self
                .decision_by_target
                .get(&format!("{}\0{}", change.entity, after.name))
                .filter(|decision| decision.kind == "columnRename")
                .cloned()
            else {
                continue;
            };
            let Some(old_column) = self
                .previous
                .entities
                .get(&change.entity)
                .and_then(|entity| entity.columns.get(&decision.from))
                .cloned()
            else {
                return Err(errorf!(
                    "rename source {}.{} does not exist",
                    change.entity,
                    decision.from
                ));
            };
            if old_column.auto_increment {
                self.statements.push(format!(
                    "ALTER TABLE {} DROP INDEX {};",
                    sql_ident(&change.entity),
                    sql_ident(&old_column.name)
                ));
            }
            if is_json_column(&old_column) {
                self.drop_json_constraint(&change.entity, &old_column.name);
            }
            if !old_column.not_null && after.not_null && !after.auto_increment {
                if !valid_backfill_expression(&after.backfill_sql) {
                    return Err(errorf!(
                        "rename to NOT NULL column {}.{} requires a valid backfill expression",
                        change.entity,
                        after.name
                    ));
                }
                let mut nullable = after.clone();
                nullable.not_null = false;
                self.statements.push(format!(
                    "ALTER TABLE {} CHANGE COLUMN {} {};",
                    sql_ident(&change.entity),
                    sql_ident(&decision.from),
                    column_sql(&nullable)
                ));
                self.statements.push(backfill_sql(&change.entity, after));
                self.statements.push(format!(
                    "ALTER TABLE {} MODIFY COLUMN {};",
                    sql_ident(&change.entity),
                    column_sql(after)
                ));
            } else {
                self.statements.push(format!(
                    "ALTER TABLE {} CHANGE COLUMN {} {};",
                    sql_ident(&change.entity),
                    sql_ident(&decision.from),
                    column_sql(after)
                ));
            }
            if is_json_column(after) {
                self.statements
                    .push(json_constraint_sql(&change.entity, &after.name));
            }
            self.renamed_from
                .insert(format!("{}\0{}", change.entity, decision.from));
            self.renamed_to
                .insert(format!("{}\0{}", change.entity, after.name));
        }
        Ok(())
    }

    fn change_columns(&mut self) -> Result<()> {
        for change in &self.diff.changed_columns {
            let (Some(before), Some(after)) = (&change.before, &change.after) else {
                continue;
            };
            let before_json = is_json_column(before);
            let after_json = is_json_column(after);
            if before.auto_increment && !after.auto_increment {
                self.statements.push(format!(
                    "ALTER TABLE {} DROP INDEX {};",
                    sql_ident(&change.entity),
                    sql_ident(&before.name)
                ));
            }
            if before_json && !after_json {
                self.drop_json_constraint(&change.entity, &before.name);
            }
            if !before.not_null && after.not_null && !after.auto_increment {
                if !valid_backfill_expression(&after.backfill_sql) {
                    return Err(errorf!(
                        "NOT NULL change for {}.{} requires a valid backfill expression",
                        change.entity,
                        after.name
                    ));
                }
                self.statements.push(backfill_sql(&change.entity, after));
            }
            self.statements.push(format!(
                "ALTER TABLE {} MODIFY COLUMN {};",
                sql_ident(&change.entity),
                column_sql(after)
            ));
            if !before_json && after_json {
                self.statements
                    .push(json_constraint_sql(&change.entity, &after.name));
            }
        }
        Ok(())
    }

    fn add_columns(&mut self) -> Result<()> {
        for change in &self.diff.added_columns {
            let Some(after) = &change.after else {
                continue;
            };
            if self
                .renamed_to
                .contains(&format!("{}\0{}", change.entity, after.name))
            {
                continue;
            }
            if after.not_null && !after.auto_increment {
                if !valid_backfill_expression(&after.backfill_sql) {
                    return Err(errorf!(
                        "adding NOT NULL column {}.{} requires a valid backfill expression",
                        change.entity,
                        after.name
                    ));
                }
                let mut nullable = after.clone();
                nullable.not_null = false;
                self.statements.push(format!(
                    "ALTER TABLE {} ADD COLUMN {};",
                    sql_ident(&change.entity),
                    column_sql(&nullable)
                ));
                if is_json_column(after) {
                    self.statements
                        .push(json_constraint_sql(&change.entity, &after.name));
                }
                self.statements.push(backfill_sql(&change.entity, after));
                self.statements.push(format!(
                    "ALTER TABLE {} MODIFY COLUMN {};",
                    sql_ident(&change.entity),
                    column_sql(after)
                ));
                continue;
            }
            self.statements.push(format!(
                "ALTER TABLE {} ADD COLUMN {};",
                sql_ident(&change.entity),
                column_sql(after)
            ));
            if is_json_column(after) {
                self.statements
                    .push(json_constraint_sql(&change.entity, &after.name));
            }
        }
        Ok(())
    }

    fn remove_columns(&mut self) {
        for change in &self.diff.removed_columns {
            let Some(before) = &change.before else {
                continue;
            };
            if self
                .renamed_from
                .contains(&format!("{}\0{}", change.entity, before.name))
            {
                continue;
            }
            if is_json_column(before) {
                self.drop_json_constraint(&change.entity, &before.name);
            }
            self.statements.push(format!(
                "ALTER TABLE {} DROP COLUMN {};",
                sql_ident(&change.entity),
                sql_ident(&before.name)
            ));
        }
    }

    fn drop_json_constraint(&mut self, entity: &str, column: &str) {
        if self.dropped_json.insert(format!("{entity}\0{column}")) {
            self.statements
                .push(drop_json_constraint_sql(entity, column));
        }
    }

    fn add_primary_keys_and_constraints(&mut self) {
        for change in &self.diff.changed_primary_keys {
            if !change.after.is_empty() {
                self.statements.push(format!(
                    "ALTER TABLE {} ADD PRIMARY KEY ({});",
                    sql_ident(&change.entity),
                    sql_columns(&change.after)
                ));
            }
        }
        for change in &self.diff.added_indexes {
            self.statements
                .push(add_index_sql(&change.entity, &change.index));
        }
        for change in &self.diff.added_foreign_keys {
            self.statements
                .push(add_foreign_key_sql(&change.entity, &change.foreign_key));
        }
    }
}

fn backfill_sql(entity: &str, column: &Column) -> String {
    format!(
        "UPDATE {} SET {} = {} WHERE {} IS NULL;",
        sql_ident(entity),
        sql_ident(&column.name),
        column.backfill_sql.trim(),
        sql_ident(&column.name)
    )
}

pub(crate) fn create_table_sql(entity: &Entity) -> String {
    let mut definitions = Vec::new();
    let mut primary = Vec::new();
    for column in entity.columns.values() {
        definitions.push(format!("    {}", column_sql(column)));
        if column.primary_key {
            primary.push(sql_ident(&column.name));
        }
        if is_json_column(column) {
            definitions.push(format!(
                "    {}",
                json_constraint_definition(&entity.name, &column.name)
            ));
        }
    }
    if !primary.is_empty() {
        definitions.push(format!("    PRIMARY KEY ({})", primary.join(", ")));
    }
    for index in entity.indexes.values() {
        let prefix = if index.unique { "UNIQUE KEY" } else { "KEY" };
        definitions.push(format!(
            "    {prefix} {} ({})",
            sql_ident(&index.name),
            sql_columns(&index.columns)
        ));
    }
    for foreign_key in entity.foreign_keys.values() {
        definitions.push(format!("    {}", foreign_key_definition(foreign_key)));
    }
    format!(
        "CREATE TABLE IF NOT EXISTS {} (\n{}\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;",
        sql_ident(&entity.name),
        definitions.join(",\n")
    )
}

fn column_sql(column: &Column) -> String {
    let nullability = if column.not_null { "NOT NULL" } else { "NULL" };
    let suffix = if column.auto_increment {
        " AUTO_INCREMENT UNIQUE"
    } else {
        ""
    };
    format!(
        "{} {} {nullability}{suffix}",
        sql_ident(&column.name),
        column.sql_type
    )
}

fn is_json_column(column: &Column) -> bool {
    column.sql_type.trim().eq_ignore_ascii_case("JSON")
}

fn add_index_sql(entity: &str, index: &Index) -> String {
    let kind = if index.unique {
        "UNIQUE INDEX"
    } else {
        "INDEX"
    };
    format!(
        "ALTER TABLE {} ADD {kind} {} ({});",
        sql_ident(entity),
        sql_ident(&index.name),
        sql_columns(&index.columns)
    )
}

fn add_foreign_key_sql(entity: &str, foreign_key: &ForeignKey) -> String {
    format!(
        "ALTER TABLE {} ADD {};",
        sql_ident(entity),
        foreign_key_definition(foreign_key)
    )
}

fn foreign_key_definition(foreign_key: &ForeignKey) -> String {
    let mut on_delete = foreign_key.on_delete.replace('-', " ").to_uppercase();
    if on_delete.is_empty() {
        on_delete = "RESTRICT".to_owned();
    }
    let mut on_update = foreign_key.on_update.to_uppercase();
    if on_update.is_empty() {
        on_update = "CASCADE".to_owned();
    }
    format!(
        "CONSTRAINT {} FOREIGN KEY ({}) REFERENCES {} ({}) ON DELETE {on_delete} ON UPDATE {on_update}",
        sql_ident(&foreign_key.name),
        sql_columns(&foreign_key_columns(foreign_key)),
        sql_ident(&foreign_key.reference_entity),
        sql_columns(&foreign_key_reference_columns(foreign_key))
    )
}

fn json_constraint_sql(entity: &str, column: &str) -> String {
    format!(
        "ALTER TABLE {} ADD {};",
        sql_ident(entity),
        json_constraint_definition(entity, column)
    )
}

fn drop_json_constraint_sql(entity: &str, column: &str) -> String {
    drop_named_json_constraint_sql(entity, &json_constraint_name(entity, column))
}

fn drop_named_json_constraint_sql(entity: &str, constraint: &str) -> String {
    format!(
        "ALTER TABLE {} DROP CHECK {};",
        sql_ident(entity),
        sql_ident(constraint)
    )
}

fn json_constraint_definition(entity: &str, column: &str) -> String {
    format!(
        "CONSTRAINT {} CHECK (JSON_VALID({}))",
        sql_ident(&json_constraint_name(entity, column)),
        sql_ident(column)
    )
}

fn json_constraint_name(entity: &str, column: &str) -> String {
    generated_database_object_name("json", entity, column)
}

fn sql_columns(columns: &[String]) -> String {
    columns
        .iter()
        .map(|column| sql_ident(column))
        .collect::<Vec<_>>()
        .join(", ")
}

fn sql_ident(value: &str) -> String {
    format!("`{}`", value.replace('`', "``"))
}
