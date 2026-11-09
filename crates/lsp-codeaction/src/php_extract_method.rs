//! The PHP "Extract method" and "Extract function" refactorings.

use std::collections::HashSet;

use swlsp_language::LanguageId;
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context};
use swlsp_lsp_phpanalysis::State;
use swlsp_lsp_protocol as protocol;
use swlsp_lsp_protocol::CodeActionKind;
use swlsp_parser_php::query::{MethodSelection, select_method_statements};
use swlsp_php::PhpIndex;
use swlsp_php_refactoring::extract_method;
use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{Snapshot, Symbol, SymbolId, SymbolKind};
use swlsp_php_types::Type;
use swlsp_rewrite::{DocumentPlan, WorkspacePlan};
use swlsp_syntax::{SyntaxNode, TextRange, TextSize};

pub struct PhpExtractMethodProvider {
    index: Option<PhpIndex>,
}

impl PhpExtractMethodProvider {
    pub fn new(index: Option<PhpIndex>) -> Self {
        Self { index }
    }
}

type NameCheck<'a> = Box<dyn Fn(Option<&SyntaxNode>, &str) -> bool + 'a>;

impl ActionProvider for PhpExtractMethodProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_EXTRACT]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        let Some(index) = self.index.as_ref() else {
            return Vec::new();
        };
        if ctx.is_cancelled() {
            return Vec::new();
        }
        let Some(document) = request.document.as_ref() else {
            return Vec::new();
        };
        if document.language != Some(LanguageId::Php) || !document.parse_errors().is_empty() {
            return Vec::new();
        }
        let Some(root) = document.root() else {
            return Vec::new();
        };
        let range = request.params.range;
        let line_index = document.line_index();
        let start = line_index.offset_utf16(range.start.line, range.start.character);
        let end = line_index.offset_utf16(range.end.line, range.end.character);
        if start > end {
            return Vec::new();
        }
        let selection = TextRange::new(TextSize::from(start), TextSize::from(end));
        let cancelled = || ctx.is_cancelled();
        let Some(selected) =
            select_method_statements(&root, document.source(), selection, cancelled)
        else {
            return Vec::new();
        };
        let Ok(Some(state)) = swlsp_lsp_phpanalysis::for_document(index, document) else {
            return Vec::new();
        };
        let Some((available, label)) = method_name_checker(ctx, &state, &selected) else {
            return Vec::new();
        };
        let Ok(Some(result)) = extract_method(
            &root,
            document.source(),
            selection,
            Some(&*available),
            &cancelled,
        ) else {
            return Vec::new();
        };
        let plan = WorkspacePlan {
            documents: vec![
                DocumentPlan::new(
                    document.uri.clone(),
                    Some(document.version),
                    document.source_arc().clone(),
                    result.edits,
                )
                .with_line_index(document.line_index_arc().clone()),
            ],
            ..WorkspacePlan::default()
        };
        let Ok(edit) = plan.workspace_edit() else {
            return Vec::new();
        };
        vec![protocol::CodeAction {
            title: format!("{label} '{}'", result.name),
            kind: Some(CodeActionKind::REFACTOR_EXTRACT),
            edit: Some(edit),
            ..protocol::CodeAction::default()
        }]
    }
}

fn method_hierarchy_known(
    ctx: &Context,
    snapshot: &Snapshot,
    class: &Symbol,
    seen: &mut HashSet<SymbolId>,
) -> bool {
    if ctx.is_cancelled() {
        return false;
    }
    if !seen.insert(class.id.clone()) {
        return true;
    }
    let Some(hierarchy) = class.hierarchy.get() else {
        return true;
    };
    for group in [&hierarchy.extends, &hierarchy.implements, &hierarchy.traits] {
        for name in group {
            let name: &str = name;
            let parents = snapshot.classes(name.strip_prefix('\\').unwrap_or(name));
            if parents.len() != 1 || !method_hierarchy_known(ctx, snapshot, &parents[0], seen) {
                return false;
            }
        }
    }
    true
}

fn method_name_checker<'a>(
    ctx: &Context,
    state: &'a State,
    selected: &MethodSelection,
) -> Option<(NameCheck<'a>, &'static str)> {
    let contains = |symbol: &Symbol| {
        symbol.range.start() <= selected.range.start() && symbol.range.end() >= selected.range.end()
    };
    if selected.class.is_none() {
        for symbol in &state.document.symbols {
            if symbol.kind != SymbolKind::Function || !contains(symbol) {
                continue;
            }
            let qualified: &str = &symbol.fully_qualified;
            let namespace = match qualified.rfind('\\') {
                Some(index) => qualified[..=index].to_owned(),
                None => String::new(),
            };
            let snapshot = &state.snapshot;
            return Some((
                Box::new(move |_: Option<&SyntaxNode>, name: &str| {
                    snapshot
                        .function_views(&format!("{namespace}{name}"))
                        .is_empty()
                }),
                "Extract function",
            ));
        }
        return None;
    }
    for class in &state.document.symbols {
        if class.kind != SymbolKind::Class || !contains(class) {
            continue;
        }
        if !method_hierarchy_known(ctx, &state.snapshot, class, &mut HashSet::new()) {
            return None;
        }
        let receiver = Type::named(&class.fully_qualified, Vec::new());
        let snapshot = &state.snapshot;
        return Some((
            Box::new(move |_: Option<&SyntaxNode>, name: &str| {
                MemberResolver::new(snapshot)
                    .methods(&receiver, name)
                    .is_empty()
            }),
            "Extract method",
        ));
    }
    None
}
