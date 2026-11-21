//! The inspection of a module description: what does not fit the entity,
//! and modules whose files differ from what is described, with the fix
//! that writes them.

use std::collections::BTreeMap;
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoxError, Context, DiagnosticId, FixContext, FixId, FixPresentation, FixResolution, Inspection,
    InspectionDefinition, InspectionScope, Problem, ProblemDefinition, QuickFix, RewriteQuickFix,
    TextDocument, bind_fix,
};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_yaml::query as yaml;
use swlsp_rewrite::{HandleError, WorkspacePlan};
use swlsp_syntax::{SyntaxNode, TextRange, TextSize};
use swlsp_uriutil::filepath;

use super::plan::{self, Action, Snapshot};
use super::{GenerateError, Generator, plugin_root};
use crate::diagnostics::ProblemAnalyzer;
use crate::inspections::BoundInspection;

pub(crate) const INVALID_CODE: &str = "shopware.admin_module.invalid";
pub(crate) const OUTDATED_CODE: &str = "shopware.admin_module.outdated";
pub const GENERATE_ADMIN_MODULES_FIX_ID: &str = "generate-admin-modules";

pub fn new_admin_module(generator: Generator) -> Arc<dyn Inspection> {
    let problem = |code: &str, severity| ProblemDefinition {
        id: DiagnosticId::from(code),
        source: "shopware-lsp".to_owned(),
        default_severity: severity,
        disabled_by_default: false,
    };
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "shopware.admin_module".to_owned(),
            description: "Administration modules described in src/Resources/shopware-lsp/admin-modules.yaml of a plugin: a description that does not fit its entity, and module files that differ from what it describes, with a fix that generates them.",
            domain: "administration",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Yaml],
            problems: vec![
                problem(INVALID_CODE, protocol::DiagnosticSeverity::ERROR),
                problem(OUTDATED_CODE, protocol::DiagnosticSeverity::WARNING),
            ],
        },
        analyzers: vec![Arc::new(AdminModuleAnalyzer {
            generator: generator.clone(),
        })],
        fixes: vec![Arc::new(GenerateFix { generator })],
        bind: Some(Arc::new(|code, _| match code.as_str() {
            OUTDATED_CODE => vec![bind_fix(GENERATE_ADMIN_MODULES_FIX_ID, ())],
            _ => Vec::new(),
        })),
    })
}

struct AdminModuleAnalyzer {
    generator: Generator,
}

/// The key the `path` of mapping keys leads to, or the deepest one that
/// exists; the start of the document without any.
fn key_range(root: &SyntaxNode, path: &[String]) -> TextRange {
    let mut found = TextRange::new(TextSize::from(0), TextSize::from(0));
    let mut mapping = yaml::root_value(root);
    for key in path {
        let Some(pair) = mapping
            .as_ref()
            .filter(|node| yaml::is_mapping(node))
            .and_then(|node| yaml::property_pair(node, key))
        else {
            break;
        };
        if let Some(token) = yaml::pair_key(&pair).and_then(|key| yaml::scalar_token(&key)) {
            found = token.text_range();
        }
        mapping = yaml::pair_value(&pair);
    }
    found
}

/// The first key named like the field a decoding error calls unknown
/// (``unknown field `name`, expected …``).
fn unknown_key_range(root: &SyntaxNode, message: &str) -> Option<TextRange> {
    let name = message.strip_prefix("unknown field `")?.split('`').next()?;
    root.descendants()
        .filter(yaml::is_mapping)
        .flat_map(|mapping| yaml::pairs(&mapping))
        .filter_map(|pair| yaml::pair_key(&pair))
        .filter(|key| yaml::scalar_value(key) == name)
        .find_map(|key| yaml::scalar_token(&key))
        .map(|token| token.text_range())
}

fn problem(
    code: &str,
    severity: protocol::DiagnosticSeverity,
    range: TextRange,
    message: String,
) -> Problem {
    Problem {
        id: DiagnosticId::from(code),
        range,
        message,
        severity: Some(severity),
        source: "shopware-lsp".to_owned(),
        ..Problem::default()
    }
}

impl ProblemAnalyzer for AdminModuleAnalyzer {
    fn input_revision(&self, document: &TextDocument) -> Option<u64> {
        // The description is compared with files on disk: it is checked on
        // every refresh. Any other file is none of this analyzer's.
        match description_root(document) {
            Some(_) => None,
            None => Some(0),
        }
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(root) = description_root(document) else {
            return Ok(Vec::new());
        };
        let start = TextRange::new(TextSize::from(0), TextSize::from(0));
        let tree = swlsp_parser_yaml::parse(document.source()).root();
        let generation = match self.generator.generate(&root, document.source()) {
            Ok(generation) => generation,
            Err(GenerateError::NotReady) => return Ok(Vec::new()),
            Err(error) => {
                let message = error.to_string();
                return Ok(vec![problem(
                    INVALID_CODE,
                    protocol::DiagnosticSeverity::ERROR,
                    unknown_key_range(&tree, &message).unwrap_or(start),
                    message,
                )]);
            }
        };
        let mut problems: Vec<Problem> = generation
            .problems
            .iter()
            .map(|found| {
                problem(
                    INVALID_CODE,
                    protocol::DiagnosticSeverity::ERROR,
                    key_range(&tree, &found.path),
                    found.message.clone(),
                )
            })
            .collect();
        ctx.check()?;
        // What differs, by module; a module of an earlier description is
        // reported on the `modules` key.
        let mut differing: BTreeMap<String, (usize, bool)> = BTreeMap::new();
        for change in plan::changes(&generation, &plan::read_disk)? {
            let entry = differing.entry(change.module).or_default();
            entry.0 += 1;
            entry.1 |= !matches!(change.action, Action::Create(_));
        }
        for (module, (count, existed)) in differing {
            let files = if count == 1 { "file" } else { "files" };
            let (path, message) = match (module.is_empty(), existed) {
                (true, _) => (
                    vec!["modules".to_owned()],
                    format!(
                        "{count} generated {files} belong to a module that is no longer described"
                    ),
                ),
                (false, false) => (
                    vec!["modules".to_owned(), module.clone()],
                    format!("Module \"{module}\" is not generated yet: {count} {files} to write"),
                ),
                (false, true) => (
                    vec!["modules".to_owned(), module.clone()],
                    format!(
                        "Module \"{module}\" differs from its description in {count} {files}: the description or the entity changed, or a generated file was edited"
                    ),
                ),
            };
            problems.push(problem(
                OUTDATED_CODE,
                protocol::DiagnosticSeverity::WARNING,
                key_range(&tree, &path),
                message,
            ));
        }
        problems.sort_by_key(|problem| (problem.range.start(), problem.id.clone()));
        Ok(problems)
    }
}

/// The plugin root when `document` is the module description of a plugin.
fn description_root(document: &TextDocument) -> Option<String> {
    let path = swlsp_uriutil::path(&document.uri).ok()?;
    plugin_root(&filepath::clean(&path))
}

struct GenerateFix {
    generator: Generator,
}

impl QuickFix for GenerateFix {
    fn id(&self) -> FixId {
        FixId::from(GENERATE_ADMIN_MODULES_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        _fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        Ok(Some(FixPresentation {
            title: "Shopware: Generate Administration modules".to_owned(),
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            // Regenerating discards an edit of a generated file: a person
            // decides that, `check --fix` does not.
            preferred: false,
            resolution: FixResolution::Lazy,
        }))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for GenerateFix {
    fn build(&self, ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let root = description_root(&fix.document).ok_or(HandleError::Stale)?;
        let generation = self.generator.generate(&root, fix.document.source())?;
        if let Some(found) = generation.problems.first() {
            return Err(format!("the description is invalid: {}", found.message).into());
        }
        // Open documents are read as the editor has them.
        let read = |path: &str| -> Result<Option<Snapshot>, BoxError> {
            if !std::path::Path::new(path).exists() {
                return Ok(None);
            }
            let snapshot = fix
                .documents
                .resolve_document(ctx, &swlsp_uriutil::file_uri(path))?;
            Ok(Some((
                snapshot.document.source().to_owned(),
                snapshot.version,
            )))
        };
        let changes = plan::changes(&generation, &read)?;
        if changes.is_empty() {
            return Err(Box::new(HandleError::Stale));
        }
        plan::workspace_plan(changes)
    }
}
