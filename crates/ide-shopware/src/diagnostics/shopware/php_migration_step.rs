//! Schema changes in `MigrationStep` classes that break on a second run or
//! during a rolling deployment.
//!
//! - A migration can run again: after a failed update, on a restored
//!   database, or when a plugin is reinstalled without its data being
//!   removed. `CREATE TABLE` without `IF NOT EXISTS`, and adding a column
//!   or key without checking for it first, then fail.
//! - During a deployment the previous version of the code still runs
//!   against the new schema. Dropping or renaming what it uses belongs in
//!   `updateDestructive()`, which runs once every server has the new code.
//!
//! The SQL is read from the string literals of the class with
//! [`swlsp_sql`]; a statement assembled at runtime is not looked at.

use std::collections::HashSet;
use std::sync::Arc;

use serde_json::json;
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, DiagnosticId, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::*;
use swlsp_php::PhpIndex;
use swlsp_sql::{ActionKind, Kind, statements};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::diagnostics::ProblemAnalyzer;
use crate::diagnostics::common::call_target_name;

pub const MIGRATION_CREATE_CODE: &str = "shopware.migration_step.create_unguarded";
pub const MIGRATION_ADD_CODE: &str = "shopware.migration_step.add_unguarded";
pub const MIGRATION_DESTRUCTIVE_CODE: &str = "shopware.migration_step.destructive_update";
pub const MIGRATION_INSERT_CODE: &str = "shopware.migration_step.insert_unguarded";

/// The codes of [`ShopwareMigrationStepAnalyzer`], and whether each is
/// reported unless configured.
pub const MIGRATION_STEP_CODES: &[(&str, bool)] = &[
    (MIGRATION_CREATE_CODE, true),
    (MIGRATION_ADD_CODE, true),
    (MIGRATION_DESTRUCTIVE_CODE, true),
    // Inserting a row with a fresh ID is often meant; the rule is opt-in.
    (MIGRATION_INSERT_CODE, false),
];

const MIGRATION_STEP_CLASS: &str = "Shopware\\Core\\Framework\\Migration\\MigrationStep";
const DESTRUCTIVE_METHOD: &str = "updatedestructive";
/// Schema manager calls that read what exists.
const INTROSPECTION_CALLS: &[&str] = &[
    "introspecttable",
    "listtablecolumns",
    "listtableindexes",
    "listtableforeignkeys",
    "listtablenames",
    "listtables",
];
/// Connection calls that read rows: what an insert may be guarded by.
const FETCH_CALLS: &[&str] = &[
    "fetchone",
    "fetchassociative",
    "fetchallassociative",
    "fetchfirstcolumn",
    "fetchallkeyvalue",
    "fetchnumeric",
    "fetchallnumeric",
    "fetchcolumn",
    "fetchall",
    "fetch",
];

pub struct ShopwareMigrationStepAnalyzer {
    php_index: Option<PhpIndex>,
}

impl ShopwareMigrationStepAnalyzer {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl ProblemAnalyzer for ShopwareMigrationStepAnalyzer {
    fn input_revision(&self, _document: &TextDocument) -> Option<u64> {
        Some(crate::diagnostics::inputs(&self.php_index))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(php_index) = &self.php_index else {
            return Ok(Vec::new());
        };
        if document.language != Some(LanguageId::Php)
            || !document.source().contains("MigrationStep")
        {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let Some(state) = swlsp_lsp_phpanalysis::for_document(php_index, document)? else {
            return Ok(Vec::new());
        };
        let mut problems = Vec::new();
        for class in phpquery::classes(&root) {
            ctx.check()?;
            let name =
                phpquery::direct_child(&class, PHP_NAME).map(|name| name.range_trimmed_trivia());
            let symbol = state.document.symbols.iter().find(|symbol| {
                symbol.is_class_like() && name.is_some_and(|name| symbol.selection_range == name)
            });
            let resolved = symbol.is_some_and(|symbol| {
                symbol.extends().iter().any(|parent| {
                    state
                        .snapshot
                        .is_subtype_of(parent.trim_start_matches('\\'), MIGRATION_STEP_CLASS)
                })
            });
            // Shopware may not be indexed.
            let is_migration = resolved
                || phpquery::class_extends(&class)
                    .iter()
                    .any(|parent| parent.rsplit('\\').next() == Some("MigrationStep"));
            if is_migration {
                check_migration(document.source(), &class, &mut problems);
            }
        }
        problems.sort_by(|left, right| {
            left.range
                .start()
                .cmp(&right.range.start())
                .then_with(|| left.id.cmp(&right.id))
        });
        Ok(problems)
    }
}

/// One SQL statement of a string literal, with where its text starts in
/// the source.
struct Found {
    statement: swlsp_sql::Statement,
    /// The source offset of the SQL text the statement ranges refer to.
    base: usize,
    method: usize,
}

impl Found {
    fn range(&self, range: &std::ops::Range<usize>) -> TextRange {
        TextRange::new(
            TextSize::from((self.base + range.start) as u32),
            TextSize::from((self.base + range.end) as u32),
        )
    }

    fn start(&self) -> usize {
        self.base + self.statement.range.start
    }
}

/// What a method does besides its statements.
struct Method {
    node: SyntaxNode,
    name: String,
    /// Offsets of checks for what exists: `columnExists()`, a schema
    /// manager read, a `SHOW …` or `information_schema` query.
    schema_guards: Vec<usize>,
    /// Offsets of row reads, which may guard an insert.
    row_reads: Vec<usize>,
    /// `try` blocks: a failing statement is caught.
    tries: Vec<TextRange>,
}

impl Method {
    fn guarded(&self, offset: usize, rows: bool) -> bool {
        let position = TextSize::from(offset as u32);
        self.schema_guards.iter().any(|guard| *guard < offset)
            || (rows && self.row_reads.iter().any(|read| *read < offset))
            || self.tries.iter().any(|range| range.contains(position))
    }
}

/// The SQL text of a string literal and its offset in the token.
fn string_content(token: &str) -> Option<(usize, &str)> {
    if let Some(rest) = token.strip_prefix("<<<") {
        let first = rest.find('\n')? + 1;
        let last = rest.rfind('\n')?;
        return (first <= last).then(|| (3 + first, &rest[first..last]));
    }
    let quote = token
        .chars()
        .next()
        .filter(|quote| matches!(quote, '\'' | '"'))?;
    let inner = token.strip_prefix(quote)?.strip_suffix(quote)?;
    Some((1, inner))
}

fn check_migration(source: &str, class: &SyntaxNode, problems: &mut Vec<Problem>) {
    let mut methods: Vec<Method> = Vec::new();
    let mut found: Vec<Found> = Vec::new();
    // `$connection->insert('table', …)`: a row added through the DBAL.
    let mut dbal_inserts: Vec<(usize, SyntaxNode)> = Vec::new();
    for node in phpquery::methods(class) {
        let index = methods.len();
        let mut method = Method {
            name: lowercase(&phpquery::method_name(&node)),
            schema_guards: Vec::new(),
            row_reads: Vec::new(),
            tries: phpquery::nodes(&node, &[PHP_TRY_STATEMENT])
                .iter()
                .map(|statement| statement.text_range())
                .collect(),
            node: node.clone(),
        };
        for call in phpquery::nodes(
            &node,
            &[PHP_MEMBER_CALL, PHP_SCOPED_CALL, PHP_FUNCTION_CALL],
        ) {
            let Some(name) = call_target_name(&call) else {
                continue;
            };
            let offset = usize::from(call.range_trimmed_trivia().start());
            let name = lowercase(name.text().to_string().trim());
            let name = name.rsplit('\\').next().unwrap_or(&name).to_owned();
            if name.contains("exist") || INTROSPECTION_CALLS.contains(&name.as_str()) {
                method.schema_guards.push(offset);
            } else if FETCH_CALLS.contains(&name.as_str()) {
                method.row_reads.push(offset);
            } else if name == "insert" && call.kind() == PHP_MEMBER_CALL {
                dbal_inserts.push((index, call));
            }
        }
        for string in phpquery::nodes(&node, &[PHP_STRING]) {
            let token = string.range_trimmed_trivia();
            let (start, end) = (usize::from(token.start()), usize::from(token.end()));
            let Some((offset, sql)) = source.get(start..end).and_then(string_content) else {
                continue;
            };
            let upper = sql.to_ascii_uppercase();
            if upper.contains("INFORMATION_SCHEMA") || upper.trim_start().starts_with("SHOW ") {
                method.schema_guards.push(start);
                continue;
            }
            if !["TABLE", "INDEX", "INSERT", "REPLACE"]
                .iter()
                .any(|keyword| upper.contains(keyword))
            {
                continue;
            }
            found.extend(statements(sql).into_iter().map(|statement| Found {
                statement,
                base: start + offset,
                method: index,
            }));
        }
        methods.push(method);
    }
    // A helper only called from updateDestructive() is destructive code,
    // and one only called after a check is checked.
    let callers = |helper: &Method| -> Vec<(usize, usize)> {
        methods
            .iter()
            .enumerate()
            .flat_map(|(index, method)| {
                phpquery::nodes(&method.node, &[PHP_MEMBER_CALL, PHP_SCOPED_CALL])
                    .into_iter()
                    .filter(|call| {
                        call_target_name(call).is_some_and(|name| {
                            eq_ignore_case(name.text().to_string().trim(), &helper.name)
                        })
                    })
                    .map(move |call| (index, usize::from(call.range_trimmed_trivia().start())))
                    .collect::<Vec<_>>()
            })
            .collect()
    };
    let destructive = |method: &Method| {
        if method.name == DESTRUCTIVE_METHOD {
            return true;
        }
        let callers = callers(method);
        !callers.is_empty()
            && callers
                .iter()
                .all(|(caller, _)| methods[*caller].name == DESTRUCTIVE_METHOD)
    };
    let guarded = |found: &Found, rows: bool| {
        let method = &methods[found.method];
        if method.guarded(found.start(), rows) {
            return true;
        }
        let callers = callers(method);
        !callers.is_empty()
            && callers
                .iter()
                .all(|(caller, offset)| methods[*caller].guarded(*offset, rows))
    };
    // What the migration creates itself is not in use by anything yet.
    let created: HashSet<String> = found
        .iter()
        .filter_map(|found| match &found.statement.kind {
            Kind::CreateTable { table, .. } => Some(lowercase(table)),
            _ => None,
        })
        .collect();
    let own = |table: &str| created.contains(&lowercase(table));
    let mut report = |code: &str, range: TextRange, message: String| -> usize {
        problems.push(Problem {
            id: DiagnosticId::from(code),
            range,
            message,
            severity: Some(DiagnosticSeverity::WARNING),
            source: "shopware-lsp".to_owned(),
            ..Problem::default()
        });
        problems.len() - 1
    };

    let mut create_fixes: Vec<(usize, String)> = Vec::new();
    for item in &found {
        let schema_guarded = guarded(item, false);
        let in_update = !destructive(&methods[item.method]);
        let statement = source
            .get(item.start()..item.base + item.statement.range.end)
            .unwrap_or("");
        match &item.statement.kind {
            Kind::CreateTable {
                table,
                if_not_exists: false,
                temporary: false,
                keywords,
            } if !table.is_empty() && statement.contains('(') && !schema_guarded => {
                let range = item.range(keywords);
                let index = report(
                    MIGRATION_CREATE_CODE,
                    range,
                    format!(
                        "CREATE TABLE `{table}` fails when the migration runs again; write \
                         CREATE TABLE IF NOT EXISTS"
                    ),
                );
                let text = source
                    .get(usize::from(range.start())..usize::from(range.end()))
                    .unwrap_or("");
                create_fixes.push((index, text.to_owned()));
            }
            Kind::CreateIndex {
                table,
                index,
                if_not_exists: false,
            } if !table.is_empty() && !own(table) && !schema_guarded => {
                report(
                    MIGRATION_ADD_CODE,
                    item.range(&item.statement.range),
                    format!(
                        "Creating the index `{index}` fails when the migration runs again; \
                         check that it does not exist first"
                    ),
                );
            }
            Kind::AlterTable { table, actions } if !table.is_empty() && !own(table) => {
                for action in actions {
                    let range = item.range(&action.range);
                    match &action.kind {
                        ActionKind::AddColumn {
                            column,
                            if_not_exists,
                            required,
                        } if !column.is_empty() => {
                            if !if_not_exists && !schema_guarded {
                                report(
                                    MIGRATION_ADD_CODE,
                                    range,
                                    format!(
                                        "Adding the column `{column}` to `{table}` fails when \
                                         the migration runs again; check columnExists() first"
                                    ),
                                );
                            }
                            if *required && in_update {
                                report(
                                    MIGRATION_DESTRUCTIVE_CODE,
                                    range,
                                    format!(
                                        "`{column}` is NOT NULL without a default: inserts of \
                                         the previous version fail during a deployment; give \
                                         it a default or allow NULL"
                                    ),
                                );
                            }
                        }
                        ActionKind::AddKey {
                            name,
                            if_not_exists: false,
                        } if !schema_guarded => {
                            let what = if name.is_empty() {
                                "a key".to_owned()
                            } else {
                                format!("`{name}`")
                            };
                            report(
                                MIGRATION_ADD_CODE,
                                range,
                                format!(
                                    "Adding {what} to `{table}` fails when the migration runs \
                                     again; check that it does not exist first"
                                ),
                            );
                        }
                        ActionKind::DropColumn { column, .. } if in_update => {
                            report(
                                MIGRATION_DESTRUCTIVE_CODE,
                                range,
                                destructive_message(&format!("Dropping the column `{column}`")),
                            );
                        }
                        ActionKind::RenameColumn { from, to } if in_update => {
                            report(
                                MIGRATION_DESTRUCTIVE_CODE,
                                range,
                                destructive_message(&format!(
                                    "Renaming the column `{from}` to `{to}`"
                                )),
                            );
                        }
                        ActionKind::RenameTable { to } if in_update => {
                            report(
                                MIGRATION_DESTRUCTIVE_CODE,
                                range,
                                destructive_message(&format!(
                                    "Renaming the table `{table}` to `{to}`"
                                )),
                            );
                        }
                        _ => {}
                    }
                }
            }
            Kind::DropTable { tables, .. } if in_update => {
                if let Some(table) = tables.iter().find(|table| !own(table)) {
                    report(
                        MIGRATION_DESTRUCTIVE_CODE,
                        item.range(&item.statement.range),
                        destructive_message(&format!("Dropping the table `{table}`")),
                    );
                }
            }
            Kind::RenameTable { tables } if in_update => {
                if let Some(table) = tables.iter().find(|table| !own(table)) {
                    report(
                        MIGRATION_DESTRUCTIVE_CODE,
                        item.range(&item.statement.range),
                        destructive_message(&format!("Renaming the table `{table}`")),
                    );
                }
            }
            Kind::Insert {
                table,
                tolerant: false,
            } if !table.is_empty() && !own(table) && !guarded(item, true) => {
                report(
                    MIGRATION_INSERT_CODE,
                    item.range(&item.statement.range),
                    insert_message(table),
                );
            }
            _ => {}
        }
    }
    for (index, call) in dbal_inserts {
        let Some(table) = phpquery::argument(&call, 0)
            .and_then(|argument| argument.children().last())
            .filter(|value| value.kind() == PHP_STRING)
            .map(|value| phpquery::string_value(&value))
        else {
            continue;
        };
        let offset = usize::from(call.range_trimmed_trivia().start());
        let method = &methods[index];
        let checked = method.guarded(offset, true) || {
            let callers = callers(method);
            !callers.is_empty()
                && callers
                    .iter()
                    .all(|(caller, at)| methods[*caller].guarded(*at, true))
        };
        if own(&table) || checked {
            continue;
        }
        if let Some(name) = call_target_name(&call) {
            report(
                MIGRATION_INSERT_CODE,
                name.range_trimmed_trivia(),
                insert_message(&table),
            );
        }
    }
    for (index, text) in create_fixes {
        problems[index].payload = Some(json!({
            "title": "Add IF NOT EXISTS",
            "expected": text,
            "replacement": format!("{text} IF NOT EXISTS"),
            "equivalent": true,
        }));
    }
}

fn destructive_message(what: &str) -> String {
    format!(
        "{what} in update() breaks the previous version of the code, which still runs during \
         a deployment; do it in updateDestructive()"
    )
}

fn insert_message(table: &str) -> String {
    format!(
        "This insert into `{table}` adds the row again, or fails on a unique key, when the \
         migration runs again; use INSERT IGNORE, ON DUPLICATE KEY UPDATE, or check for the \
         row first"
    )
}
