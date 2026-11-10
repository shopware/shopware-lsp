//! PHP semantic diagnostics.

use std::collections::HashSet;

use serde_json::json;
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoxError, Cancelled, Context, DiagnosticId, InspectionScope, InspectionSpec, Problem,
    TextDocument,
};
use swlsp_lsp_protocol::{DiagnosticSeverity, DiagnosticTag};
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::{
    PHP_ARROW_FUNCTION, PHP_ASSIGNMENT_EXPRESSION, PHP_BINARY_EXPRESSION, PHP_BLOCK,
    PHP_BREAK_STATEMENT, PHP_CLASS_DECLARATION, PHP_CLOSURE, PHP_CONTINUE_STATEMENT,
    PHP_EXTENDS_CLAUSE, PHP_FUNCTION_CALL, PHP_IF_STATEMENT, PHP_MEMBER_ACCESS, PHP_MEMBER_CALL,
    PHP_NAME, PHP_PARENTHESIZED, PHP_RETURN_STATEMENT, PHP_SCOPED_ACCESS, PHP_SCOPED_CALL,
    PHP_STRING, PHP_TERNARY_EXPRESSION, PHP_THROW_STATEMENT, PHP_UNARY_EXPRESSION, TK_KEYWORD,
    TK_OPERATOR,
};
use swlsp_php_languagelevel as languagelevel;
use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{
    Document, Flags, NameKind, Reference, Snapshot, Symbol, SymbolId, SymbolKind, Visibility,
    deprecation_of,
};
use swlsp_php_suppression::Set as Suppressions;
use swlsp_php_types::{Kind, Type};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::Provider;
use crate::helpers::{
    compact_name, direct_nodes, is_implicit_variable, is_php, name_context_at, visibility_name,
};

/// The source of every problem this analyzer reports.
pub const SOURCE: &str = "shopware-php";

/// The `php.semantic` inspection: every diagnostic in [`DIAGNOSTIC_IDS`].
pub const INSPECTION: InspectionSpec = InspectionSpec {
    id: "php.semantic",
    description: "Core PHP checks: undefined symbols, wrong argument counts and types, return types, visibility, inheritance and abstract-member rules, duplicate declarations, deprecations, and features or extensions the configured PHP version lacks.",
    domain: "php",
    scope: InspectionScope::Language,
    languages: &[LanguageId::Php],
    source: SOURCE,
    codes: DIAGNOSTIC_IDS,
};

/// Every diagnostic ID the PHP semantic analyzer can report; the ownership
/// catalog of the `php.semantic` inspection.
pub const DIAGNOSTIC_IDS: &[&str] = &[
    "php.abstract",
    "php.arguments",
    "php.deprecated",
    "php.duplicate",
    "php.extension",
    "php.inheritance",
    "php.override",
    "php.parse",
    "php.returnType",
    "php.undefined",
    "php.version",
    "php.visibility",
];

impl Provider {
    /// Semantic PHP diagnostics of an open document.
    pub fn analyze(
        &self,
        ctx: &Context,
        document: &TextDocument,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(index) = &self.index else {
            return Ok(Vec::new());
        };
        if !is_php(&document.uri) {
            return Ok(Vec::new());
        }
        let Some(analysis) = swlsp_lsp_phpanalysis::for_document(index, document)? else {
            return Ok(Vec::new());
        };
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let run = DiagnosticRun {
            provider: self,
            document,
            root: &root,
            semantic: &analysis.document,
            snapshot: &analysis.snapshot,
            suppressions: swlsp_php_suppression::parse(document.source()),
            diagnostics: Vec::new(),
        };
        let problems = run.analyze();
        if ctx.is_cancelled() {
            return Err(Box::new(Cancelled));
        }
        Ok(problems)
    }

    fn unavailable_extension_diagnostic(&self, reference: &Reference) -> (Option<Problem>, bool) {
        let Some(index) = &self.index else {
            return (None, false);
        };
        let extension = reference
            .qualified_names()
            .iter()
            .find_map(|name| swlsp_php_stubs::extension_for_symbol(name))
            .or_else(|| swlsp_php_stubs::extension_for_symbol(&reference.name));
        let Some(extension) = extension else {
            return (None, false);
        };
        let (enabled, known) = index.project().map_or((false, false), |model| {
            model.extension_availability(extension)
        });
        if enabled {
            return (None, false);
        }
        if !known {
            // The symbol belongs to a real optional runtime extension, but
            // Composer's positive requirements do not prove that the local
            // runtime lacks it.
            return (None, true);
        }
        let problem = problem(
            "php.extension",
            reference.range,
            DiagnosticSeverity::WARNING,
            format!(
                "{} requires disabled PHP extension ext-{}",
                reference.name,
                extension.replace('_', "-")
            ),
        );
        (
            Some(Problem {
                payload: Some(json!({ "extension": extension })),
                ..problem
            }),
            true,
        )
    }
}

fn problem(id: &str, range: TextRange, severity: DiagnosticSeverity, message: String) -> Problem {
    Problem {
        id: DiagnosticId::from(id),
        range,
        severity: Some(severity),
        source: SOURCE.to_owned(),
        message,
        ..Problem::default()
    }
}

#[inline]
fn start(range: TextRange) -> u32 {
    range.start().into()
}

struct DiagnosticRun<'a> {
    provider: &'a Provider,
    document: &'a TextDocument,
    root: &'a SyntaxNode,
    semantic: &'a Document,
    snapshot: &'a Snapshot,
    suppressions: Suppressions,
    diagnostics: Vec<Problem>,
}

impl DiagnosticRun<'_> {
    fn analyze(mut self) -> Vec<Problem> {
        self.add_parse_errors();
        self.add_language_level_problems();
        self.add_reference_problems();
        self.add_semantic_issues();
        self.add_duplicate_declarations();
        self.diagnostics
    }

    fn suppressed(&self, range: TextRange, code: &str) -> bool {
        self.suppressions.suppresses(start(range), code)
    }

    fn add_parse_errors(&mut self) {
        for parse_error in self.document.parse_errors() {
            if self.suppressed(parse_error.range, "php.parse") {
                continue;
            }
            self.diagnostics.push(problem(
                "php.parse",
                parse_error.range,
                DiagnosticSeverity::ERROR,
                parse_error.message(),
            ));
        }
    }

    fn add_language_level_problems(&mut self) {
        let Some(model) = self
            .provider
            .index
            .as_ref()
            .and_then(|index| index.project())
        else {
            return;
        };
        for occurrence in languagelevel::detect(self.root) {
            let definition = languagelevel::lookup(occurrence.feature);
            if languagelevel::supports(model.php_version, occurrence.feature)
                || self.suppressed(occurrence.range, "php.version")
            {
                continue;
            }
            let problem = problem(
                "php.version",
                occurrence.range,
                DiagnosticSeverity::ERROR,
                languagelevel::unsupported_message(&definition, model.php_version),
            );
            self.diagnostics.push(Problem {
                payload: Some(json!({
                    "feature": occurrence.feature.id(),
                    "minimumPHP": format!("{}.{}", definition.major, definition.minor),
                    "configuredPHP": format!("{}.{}", model.php_version.major, model.php_version.minor),
                })),
                ..problem
            });
        }
    }

    fn add_reference_problems(&mut self) {
        for reference in &self.semantic.references {
            let candidates = reference_candidates(self.snapshot, reference);
            if !candidates.is_empty() {
                self.add_resolved_reference_problems(reference, &candidates);
                continue;
            }
            self.add_undefined_reference_problem(reference);
        }
    }

    fn add_resolved_reference_problems(&mut self, reference: &Reference, candidates: &[Symbol]) {
        self.add_deprecation_problem(reference, candidates);
        self.add_soft_final_extension_problem(reference, candidates);
        if reference.kind != NameKind::Member
            || any_member_accessible(
                self.semantic,
                self.snapshot,
                self.root,
                reference,
                candidates,
            )
            || self.suppressed(reference.range, "php.visibility")
        {
            return;
        }
        self.diagnostics.push(problem(
            "php.visibility",
            reference.range,
            DiagnosticSeverity::ERROR,
            inaccessible_member_message(reference, &candidates[0]),
        ));
    }

    fn add_soft_final_extension_problem(&mut self, reference: &Reference, candidates: &[Symbol]) {
        if reference.kind != NameKind::Class || self.suppressed(reference.range, "php.inheritance")
        {
            return;
        }
        let Some(node) = self.root.node_at(reference.range.start()) else {
            return;
        };
        let Some(class) = query::class_at(&node) else {
            return;
        };
        if class.kind() != PHP_CLASS_DECLARATION {
            return;
        }
        let Some(extends) = query::direct_child(&class, PHP_EXTENDS_CLAUSE) else {
            return;
        };
        if !extends.text_range().contains(reference.range.start()) {
            return;
        }
        for symbol in candidates {
            if symbol.kind != SymbolKind::Class
                || !symbol.flags.has(Flags::SOFT_FINAL)
                || symbol.flags.has(Flags::FINAL)
            {
                continue;
            }
            let problem = problem(
                "php.inheritance",
                reference.range,
                DiagnosticSeverity::WARNING,
                format!(
                    "Class {} is marked @final and should not be extended",
                    symbol.fully_qualified
                ),
            );
            self.diagnostics.push(Problem {
                payload: Some(json!({ "class": &*symbol.fully_qualified })),
                ..problem
            });
            return;
        }
    }

    fn add_deprecation_problem(&mut self, reference: &Reference, candidates: &[Symbol]) {
        if self.suppressed(reference.range, "php.deprecated") {
            return;
        }
        for symbol in candidates {
            if !symbol.flags.has(Flags::DEPRECATED)
                || is_deprecation_suppressed_at_reference(
                    self.semantic,
                    self.snapshot,
                    reference,
                    symbol,
                )
            {
                continue;
            }
            self.diagnostics.push(Problem {
                tags: vec![DiagnosticTag::DEPRECATED],
                ..problem(
                    "php.deprecated",
                    reference.range,
                    DiagnosticSeverity::HINT,
                    format_deprecation_diagnostic(symbol),
                )
            });
            return;
        }
    }

    fn add_undefined_reference_problem(&mut self, reference: &Reference) {
        let Some(message) = self.undefined_reference_message(reference) else {
            return;
        };
        if self.suppressed(reference.range, "php.undefined") {
            return;
        }
        self.diagnostics.push(problem(
            "php.undefined",
            reference.range,
            DiagnosticSeverity::WARNING,
            message,
        ));
    }

    fn undefined_reference_message(&mut self, reference: &Reference) -> Option<String> {
        match reference.kind {
            NameKind::Class => {
                if class_existence_guarded(self.root, self.semantic, reference)
                    || self.add_extension_problem(reference)
                {
                    return None;
                }
                Some(format!("Undefined class {}", reference.name))
            }
            NameKind::Function | NameKind::Constant => {
                self.add_extension_problem(reference);
                None
            }
            NameKind::Member => {
                if !self.diagnosable_undefined_member(reference) {
                    return None;
                }
                Some(undefined_member_message(reference))
            }
            NameKind::Variable => {
                if is_implicit_variable(&reference.name) {
                    return None;
                }
                Some(format!("Undefined variable {}", reference.name))
            }
        }
    }

    fn add_extension_problem(&mut self, reference: &Reference) -> bool {
        let (diagnostic, handled) = self.provider.unavailable_extension_diagnostic(reference);
        if handled
            && let Some(diagnostic) = diagnostic
            && !self.suppressed(reference.range, "php.extension")
        {
            self.diagnostics.push(diagnostic);
        }
        handled
    }

    fn diagnosable_undefined_member(&self, reference: &Reference) -> bool {
        diagnosable_member_reference(self.snapshot, reference)
            && !late_static_member_may_be_declared_by_subclass(self.root, self.snapshot, reference)
            && (reference.target_kind != SymbolKind::Method
                || !method_exists_guarded(self.root, reference))
            && !is_implicit_trait_requirement(self.semantic, self.snapshot, reference)
    }

    fn add_semantic_issues(&mut self) {
        for issue in &self.semantic.issues {
            self.diagnostics.push(problem(
                &issue.code,
                issue.range,
                DiagnosticSeverity::ERROR,
                issue.message.to_string(),
            ));
        }
    }

    fn add_duplicate_declarations(&mut self) {
        for symbol in &self.semantic.symbols {
            if !symbol.is_class_like()
                || !has_duplicate_class(self.snapshot, &symbol.fully_qualified)
            {
                continue;
            }
            self.diagnostics.push(problem(
                "php.duplicate",
                symbol.selection_range,
                DiagnosticSeverity::ERROR,
                format!("Duplicate declaration of {}", symbol.fully_qualified),
            ));
        }
    }
}

/// Whether at least two classes are declared under `name`, without
/// materializing the class views.
fn has_duplicate_class(snapshot: &Snapshot, name: &str) -> bool {
    let mut count = 0;
    snapshot.visit_class_views(name, |_| {
        count += 1;
        count < 2
    });
    count >= 2
}

pub(crate) fn format_deprecation_hover(symbol: &Symbol) -> String {
    let mut result = String::from("\n\n**Deprecated**");
    let Some(details) = deprecation_of(symbol.attributes()) else {
        return result;
    };
    if !details.since.is_empty() {
        result.push_str(" since ");
        result.push_str(&details.since);
    }
    if !details.reason.is_empty() {
        result.push_str("\n\n");
        result.push_str(&details.reason);
    }
    if !details.replacement.is_empty() {
        result.push_str("\n\nReplacement: `");
        result.push_str(&details.replacement.replace('`', "\\`"));
        result.push('`');
    }
    result
}

fn format_deprecation_diagnostic(symbol: &Symbol) -> String {
    let mut message = format!("{} is deprecated", symbol.name);
    let Some(details) = deprecation_of(symbol.attributes()) else {
        return message;
    };
    if !details.since.is_empty() {
        message.push_str(" since ");
        message.push_str(&details.since);
    }
    if !details.reason.is_empty() {
        message.push_str(": ");
        message.push_str(&details.reason);
    }
    if !details.replacement.is_empty() {
        message.push_str("; use ");
        message.push_str(&details.replacement);
    }
    message
}

fn late_static_member_may_be_declared_by_subclass(
    root: &SyntaxNode,
    snapshot: &Snapshot,
    reference: &Reference,
) -> bool {
    if !reference.is_static {
        return false;
    }
    let Some(node) = root.node_at(reference.range.start()) else {
        return false;
    };
    let mut late_static = false;
    for current in node.ancestors() {
        if matches!(
            current.kind(),
            PHP_MEMBER_ACCESS | PHP_SCOPED_ACCESS | PHP_MEMBER_CALL | PHP_SCOPED_CALL
        ) {
            late_static = current
                .children()
                .next()
                .is_some_and(|first| eq_ignore_case(first.text().to_string().trim(), "static"));
        }
        if late_static {
            break;
        }
    }
    if !late_static {
        return false;
    }

    let mut extensible = false;
    let mut found = false;
    visit_object_alternatives(&reference.receiver, &mut |receiver| {
        snapshot.visit_class_views(receiver.name(), |class| {
            found = true;
            if !class.flags().has(Flags::FINAL) {
                extensible = true;
            }
            !extensible
        });
    });
    !found || extensible
}

fn visit_object_alternatives(value: &Type, visit: &mut dyn FnMut(&Type)) {
    match value.kind() {
        Kind::Object => visit(value),
        Kind::Union | Kind::Intersection => {
            for alternative in value.arguments() {
                visit_object_alternatives(alternative, visit);
            }
        }
        _ => {}
    }
}

/// The ternary branch or `if` body relationship of `child` to `parent`:
/// `Some(true)` for the truth branch, `Some(false)` for the false branch.
fn ternary_branch(parent: &SyntaxNode, offset: TextSize) -> Option<bool> {
    let nodes = direct_nodes(parent);
    if nodes.len() < 3 {
        return None;
    }
    let truth = nodes[nodes.len() - 2].text_range().contains(offset);
    let falsity = nodes[nodes.len() - 1].text_range().contains(offset);
    (truth || falsity).then_some(truth)
}

/// Walks the statements preceding the one containing `start` in every
/// enclosing block. `visit` returns `Some(result)` to stop.
fn visit_preceding_statements(
    start: &SyntaxNode,
    mut visit: impl FnMut(&SyntaxNode) -> Option<bool>,
) -> bool {
    let mut current = start.clone();
    loop {
        let mut statement = current.clone();
        while let Some(parent) = statement.parent()
            && parent.kind() != PHP_BLOCK
        {
            statement = parent;
        }
        let Some(block) = statement.parent() else {
            break;
        };
        let statements = direct_nodes(&block);
        if let Some(index) = statements
            .iter()
            .position(|candidate| *candidate == statement)
        {
            for guard in statements[..index].iter().rev() {
                if let Some(result) = visit(guard) {
                    return result;
                }
            }
        }
        current = block;
    }
    false
}

fn method_exists_guarded(root: &SyntaxNode, reference: &Reference) -> bool {
    let offset = reference.range.start();
    let Some(call) = root.node_at(offset).and_then(|node| query::call_at(&node)) else {
        return false;
    };
    if call.kind() != PHP_MEMBER_CALL {
        return false;
    }
    let Some(receiver) = query::call_receiver(&call) else {
        return false;
    };
    let receiver_key = normalized_guard_expression(&receiver.text().to_string());
    if receiver_key.is_empty() {
        return false;
    }

    let mut current = call.clone();
    while let Some(parent) = current.parent() {
        if parent.kind() == PHP_TERNARY_EXPRESSION
            && let Some(truth) = ternary_branch(&parent, offset)
            && condition_guarantees_method(
                parent.children().next().as_ref(),
                &receiver_key,
                &reference.name,
                truth,
            )
        {
            return true;
        }
        if parent.kind() == PHP_IF_STATEMENT {
            let nodes = direct_nodes(&parent);
            if nodes.len() >= 2
                && nodes[1].text_range().contains(offset)
                && condition_guarantees_method(
                    Some(&nodes[0]),
                    &receiver_key,
                    &reference.name,
                    true,
                )
            {
                return true;
            }
        }
        current = parent;
    }

    visit_preceding_statements(&call, |guard| {
        if statement_assigns_expression(guard, &receiver_key) {
            return Some(false);
        }
        if guard.kind() != PHP_IF_STATEMENT {
            return None;
        }
        let nodes = direct_nodes(guard);
        (nodes.len() == 2
            && statement_terminates(Some(&nodes[1]))
            && condition_guarantees_method(Some(&nodes[0]), &receiver_key, &reference.name, false))
        .then_some(true)
    })
}

fn class_existence_guarded(root: &SyntaxNode, document: &Document, reference: &Reference) -> bool {
    let names = diagnostic_reference_class_names(document, reference);
    if names.is_empty() {
        return false;
    }
    let offset = reference.range.start();
    let Some(node) = root.node_at(offset) else {
        return false;
    };
    if let Some(call) = query::call_at(&node)
        && class_existence_predicate(&call)
        && let Some(argument) = query::argument_expression(&call, 0)
        && argument.text_range().contains(offset)
        && guard_expression_matches_class(document, Some(&argument), &names)
    {
        return true;
    }

    let mut current = node.clone();
    while let Some(parent) = current.parent() {
        if parent.kind() == PHP_TERNARY_EXPRESSION
            && let Some(truth) = ternary_branch(&parent, offset)
            && condition_guarantees_class(
                parent.children().next().as_ref(),
                document,
                &names,
                truth,
            )
        {
            return true;
        }
        // `class_exists(X::class) && $value instanceof X`: the right operand
        // only runs when the left one held (`&&`) or failed (`||`).
        if parent.kind() == PHP_BINARY_EXPRESSION
            && let (Some(left), Some(right)) = (parent.children().next(), parent.children().last())
            && right.text_range().contains(offset)
            && !left.text_range().contains(offset)
        {
            let truth = match lowercase(&diagnostic_direct_operator(&parent)).as_str() {
                "&&" | "and" => Some(true),
                "||" | "or" => Some(false),
                _ => None,
            };
            if let Some(truth) = truth
                && condition_guarantees_class(Some(&left), document, &names, truth)
            {
                return true;
            }
        }
        if parent.kind() == PHP_IF_STATEMENT {
            let nodes = direct_nodes(&parent);
            if nodes.len() >= 2
                && nodes[1].text_range().contains(offset)
                && condition_guarantees_class(Some(&nodes[0]), document, &names, true)
            {
                return true;
            }
        }
        current = parent;
    }

    visit_preceding_statements(&node, |guard| {
        if guard.kind() != PHP_IF_STATEMENT {
            return None;
        }
        let nodes = direct_nodes(guard);
        (nodes.len() == 2
            && statement_terminates(Some(&nodes[1]))
            && condition_guarantees_class(Some(&nodes[0]), document, &names, false))
        .then_some(true)
    })
}

fn diagnostic_reference_class_names(document: &Document, reference: &Reference) -> Vec<String> {
    let mut names: Vec<String> = reference
        .qualified_names()
        .iter()
        .map(|name| normalize_diagnostic_class_name(name))
        .filter(|name| !name.is_empty())
        .collect();
    if names.is_empty() && !reference.name.is_empty() {
        let resolved =
            name_context_at(document, start(reference.range)).resolve_class(&reference.name);
        let name = normalize_diagnostic_class_name(&resolved);
        if !name.is_empty() {
            names.push(name);
        }
    }
    names
}

/// Evaluates `&&`/`||` composition of a guard condition.
fn compose_condition(
    node: &SyntaxNode,
    truth: bool,
    guarantees: impl Fn(&SyntaxNode, bool) -> bool,
) -> Option<bool> {
    let nodes = direct_nodes(node);
    if nodes.len() < 2 {
        return None;
    }
    let left = guarantees(&nodes[0], truth);
    let right = guarantees(&nodes[nodes.len() - 1], truth);
    match lowercase(&diagnostic_direct_operator(node)).as_str() {
        "&&" | "and" => Some(if truth { left || right } else { left && right }),
        "||" | "or" => Some(if truth { left && right } else { left || right }),
        _ => None,
    }
}

fn condition_guarantees_class(
    node: Option<&SyntaxNode>,
    document: &Document,
    names: &[String],
    truth: bool,
) -> bool {
    let Some(node) = node else {
        return false;
    };
    if node.kind() == PHP_PARENTHESIZED {
        return condition_guarantees_class(node.children().next().as_ref(), document, names, truth);
    }
    if node.kind() == PHP_UNARY_EXPRESSION && diagnostic_direct_operator(node) == "!" {
        return condition_guarantees_class(
            node.children().last().as_ref(),
            document,
            names,
            !truth,
        );
    }
    if node.kind() == PHP_BINARY_EXPRESSION
        && let Some(result) = compose_condition(node, truth, |child, truth| {
            condition_guarantees_class(Some(child), document, names, truth)
        })
    {
        return result;
    }
    if !truth || !class_existence_predicate(node) {
        return false;
    }
    guard_expression_matches_class(
        document,
        query::argument_expression(node, 0).as_ref(),
        names,
    )
}

fn class_existence_predicate(node: &SyntaxNode) -> bool {
    if node.kind() != PHP_FUNCTION_CALL {
        return false;
    }
    let name = query::call_method_name(node);
    matches!(
        lowercase(name.strip_prefix('\\').unwrap_or(&name)).as_str(),
        "class_exists" | "interface_exists" | "trait_exists" | "enum_exists"
    )
}

fn guard_expression_matches_class(
    document: &Document,
    expression: Option<&SyntaxNode>,
    names: &[String],
) -> bool {
    let guarded = guarded_class_name(document, expression);
    !guarded.is_empty() && names.contains(&guarded)
}

fn guarded_class_name(document: &Document, expression: Option<&SyntaxNode>) -> String {
    let Some(expression) = expression else {
        return String::new();
    };
    if expression.kind() == PHP_STRING {
        return normalize_diagnostic_class_name(&diagnostic_php_string_value(expression));
    }
    if expression.kind() != PHP_MEMBER_ACCESS && expression.kind() != PHP_SCOPED_ACCESS {
        return String::new();
    }
    let nodes = direct_nodes(expression);
    if nodes.len() < 2
        || !eq_ignore_case(
            &compact_name(&nodes[nodes.len() - 1].text().to_string()),
            "class",
        )
    {
        return String::new();
    }
    let receiver = compact_name(&nodes[0].text().to_string());
    if receiver.is_empty() {
        return String::new();
    }
    let resolved =
        name_context_at(document, nodes[0].text_range().start().into()).resolve_class(&receiver);
    normalize_diagnostic_class_name(&resolved)
}

fn normalize_diagnostic_class_name(name: &str) -> String {
    lowercase(name.trim().trim_matches('\\'))
}

fn diagnostic_php_string_value(node: &SyntaxNode) -> String {
    let value = query::string_value(node);
    let text = node.text().to_string();
    let text = text.trim();
    if text.len() < 2 {
        return value;
    }
    match text.as_bytes()[0] {
        b'\'' => unescape(&value, '\''),
        b'"' => unescape(&value, '"'),
        _ => value,
    }
}

/// Unescapes `\\` and an escaped `quote` in a single pass, left to right;
/// every other backslash stays as written.
fn unescape(value: &str, quote: char) -> String {
    let mut result = String::with_capacity(value.len());
    let mut characters = value.chars().peekable();
    while let Some(character) = characters.next() {
        if character == '\\'
            && let Some(&next) = characters.peek()
            && (next == '\\' || next == quote)
        {
            result.push(next);
            characters.next();
            continue;
        }
        result.push(character);
    }
    result
}

fn statement_assigns_expression(node: &SyntaxNode, expression: &str) -> bool {
    if expression.is_empty() {
        return false;
    }
    node.descendants().any(|candidate| {
        candidate.kind() == PHP_ASSIGNMENT_EXPRESSION
            && candidate.children().next().is_some_and(|target| {
                normalized_guard_expression(&target.text().to_string()) == expression
            })
    })
}

fn condition_guarantees_method(
    node: Option<&SyntaxNode>,
    receiver: &str,
    method: &str,
    truth: bool,
) -> bool {
    let Some(node) = node else {
        return false;
    };
    if node.kind() == PHP_PARENTHESIZED {
        return condition_guarantees_method(
            node.children().next().as_ref(),
            receiver,
            method,
            truth,
        );
    }
    if node.kind() == PHP_UNARY_EXPRESSION && diagnostic_direct_operator(node) == "!" {
        return condition_guarantees_method(
            node.children().last().as_ref(),
            receiver,
            method,
            !truth,
        );
    }
    if node.kind() == PHP_BINARY_EXPRESSION
        && let Some(result) = compose_condition(node, truth, |child, truth| {
            condition_guarantees_method(Some(child), receiver, method, truth)
        })
    {
        return result;
    }
    if !truth || node.kind() != PHP_FUNCTION_CALL {
        return false;
    }
    let name = query::call_method_name(node);
    if !eq_ignore_case(name.strip_prefix('\\').unwrap_or(&name), "method_exists") {
        return false;
    }
    let (Some(target), Some(name)) = (
        query::argument_expression(node, 0),
        query::argument_expression(node, 1),
    ) else {
        return false;
    };
    normalized_guard_expression(&target.text().to_string()) == receiver
        && eq_ignore_case(&query::string_value(&name), method)
}

fn statement_terminates(node: Option<&SyntaxNode>) -> bool {
    let Some(node) = node else {
        return false;
    };
    if node.kind() == PHP_BLOCK {
        return statement_terminates(node.children().last().as_ref());
    }
    matches!(
        node.kind(),
        PHP_RETURN_STATEMENT | PHP_THROW_STATEMENT | PHP_BREAK_STATEMENT | PHP_CONTINUE_STATEMENT
    )
}

fn diagnostic_direct_operator(node: &SyntaxNode) -> String {
    node.children_with_tokens()
        .filter_map(|child| child.into_token())
        .find(|token| matches!(token.kind(), TK_OPERATOR | TK_KEYWORD))
        .map_or_else(String::new, |token| token.text().to_owned())
}

fn normalized_guard_expression(value: &str) -> String {
    let compact: String = value
        .chars()
        .filter(|character| !matches!(character, ' ' | '\t' | '\r' | '\n'))
        .collect();
    compact.replace("?->", "->")
}

/// Walks the scope chain enclosing `offset`, calling `visit` with each
/// owner declaration found in the snapshot. Returning `Some` stops.
fn visit_scope_owners<T>(
    document: &Document,
    snapshot: &Snapshot,
    offset: u32,
    mut visit: impl FnMut(&Symbol) -> Option<T>,
) -> Option<T> {
    let mut scope = document.scope_at(offset)?;
    loop {
        if let Some(owner) = snapshot.symbol(scope.owner.as_str())
            && let Some(result) = visit(&owner)
        {
            return Some(result);
        }
        if scope.id == scope.parent || scope.parent as usize >= document.scopes.len() {
            return None;
        }
        scope = &document.scopes[scope.parent as usize];
    }
}

fn is_deprecation_suppressed_at_reference(
    document: &Document,
    snapshot: &Snapshot,
    reference: &Reference,
    symbol: &Symbol,
) -> bool {
    if document.symbols.iter().any(|declaration| {
        declaration.flags.has(Flags::DEPRECATED)
            && declaration.range.contains(reference.range.start())
    }) {
        return true;
    }
    visit_scope_owners(document, snapshot, start(reference.range), |owner| {
        if owner.flags.has(Flags::DEPRECATED)
            || symbol.id == owner.id
            || symbol.container == owner.id
        {
            return Some(());
        }
        if !owner.container.is_empty()
            && snapshot
                .symbol_view(owner.container.as_str())
                .is_some_and(|container| container.flags().has(Flags::DEPRECATED))
        {
            return Some(());
        }
        None
    })
    .is_some()
}

pub(crate) fn reference_candidates(snapshot: &Snapshot, reference: &Reference) -> Vec<Symbol> {
    let mut result: Vec<Symbol> = Vec::with_capacity(
        reference.candidate_ids().len() + usize::from(!reference.resolved.is_empty()),
    );
    let mut append = |id: &SymbolId| {
        if result.iter().any(|existing| existing.id == *id) {
            return;
        }
        if let Some(symbol) = snapshot.symbol(id.as_str()) {
            result.push(symbol);
        }
    };
    if !reference.resolved.is_empty() {
        append(&reference.resolved);
    }
    for id in reference.candidate_ids() {
        append(id);
    }
    result
}

fn diagnosable_member_reference(snapshot: &Snapshot, reference: &Reference) -> bool {
    fn check(snapshot: &Snapshot, reference: &Reference, receiver: &Type) -> bool {
        match receiver.kind() {
            Kind::Union | Kind::Intersection => {
                let members = receiver.arguments();
                !members.is_empty()
                    && members
                        .iter()
                        .all(|member| check(snapshot, reference, member))
            }
            Kind::Object => {
                if receiver.name().is_empty() {
                    return false;
                }
                let classes = snapshot.classes(receiver.name());
                !classes.is_empty()
                    && classes
                        .iter()
                        .all(|class| !class_has_dynamic_member_fallback(snapshot, class, reference))
            }
            _ => false,
        }
    }
    !reference.receiver.is_unknown() && check(snapshot, reference, &reference.receiver)
}

fn class_has_dynamic_member_fallback(
    snapshot: &Snapshot,
    class: &Symbol,
    reference: &Reference,
) -> bool {
    if reference.target_kind == SymbolKind::Property
        && class_allows_dynamic_properties(snapshot, class, &mut HashSet::new())
    {
        return true;
    }
    let names: &[&str] = match reference.target_kind {
        SymbolKind::Method if reference.is_static => &["__callStatic"],
        SymbolKind::Method => &["__call"],
        SymbolKind::Property if reference.is_static => return false,
        SymbolKind::Property if reference.write => &["__set"],
        SymbolKind::Property => &["__get", "__isset"],
        _ => return false,
    };
    let resolver = MemberResolver::new(snapshot);
    let receiver = Type::named(&class.fully_qualified, Vec::new());
    names
        .iter()
        .any(|name| !resolver.visit_methods(&receiver, name, |_| false))
}

fn class_allows_dynamic_properties(
    snapshot: &Snapshot,
    class: &Symbol,
    visited: &mut HashSet<SymbolId>,
) -> bool {
    if eq_ignore_case(&class.fully_qualified, "stdClass") || has_allow_dynamic_properties(class) {
        return true;
    }
    if !visited.insert(class.id.clone()) {
        return false;
    }
    for parent in class.extends() {
        let mut allowed = false;
        snapshot.visit_class_views(parent, |parent| {
            allowed = class_allows_dynamic_properties(snapshot, &parent.materialize(), visited);
            !allowed
        });
        if allowed {
            return true;
        }
    }
    false
}

fn has_allow_dynamic_properties(class: &Symbol) -> bool {
    class.attributes().iter().any(|attribute| {
        let name = attribute.name.strip_prefix('\\').unwrap_or(&attribute.name);
        eq_ignore_case(name, "AllowDynamicProperties")
            || lowercase(name).ends_with("\\allowdynamicproperties")
    })
}

fn undefined_member_message(reference: &Reference) -> String {
    let name = reference.name.strip_prefix('$').unwrap_or(&reference.name);
    match reference.target_kind {
        SymbolKind::Method => format!("Undefined method {name} on {}", reference.receiver),
        SymbolKind::ClassConstant => {
            format!("Undefined class constant {name} on {}", reference.receiver)
        }
        _ => format!("Undefined property ${name} on {}", reference.receiver),
    }
}

fn is_implicit_trait_requirement(
    document: &Document,
    snapshot: &Snapshot,
    reference: &Reference,
) -> bool {
    let Some(current) = enclosing_class(document, snapshot, start(reference.range)) else {
        return false;
    };
    if current.kind != SymbolKind::Trait {
        return false;
    }
    let trait_name = current
        .fully_qualified
        .strip_prefix('\\')
        .unwrap_or(&current.fully_qualified);
    fn contains_trait(value: &Type, trait_name: &str) -> bool {
        match value.kind() {
            Kind::Object => eq_ignore_case(
                value.name().strip_prefix('\\').unwrap_or(value.name()),
                trait_name,
            ),
            Kind::Union | Kind::Intersection => value
                .arguments()
                .iter()
                .any(|member| contains_trait(member, trait_name)),
            _ => false,
        }
    }
    contains_trait(&reference.receiver, trait_name)
}

fn any_member_accessible(
    document: &Document,
    snapshot: &Snapshot,
    root: &SyntaxNode,
    reference: &Reference,
    candidates: &[Symbol],
) -> bool {
    let current = enclosing_class(document, snapshot, start(reference.range));
    let bound_classes = closure_binding_classes(document, snapshot, root, start(reference.range));
    for candidate in candidates {
        let mut visibility = candidate.visibility;
        if reference.write && candidate.kind == SymbolKind::Property {
            if candidate.flags.has(Flags::READONLY)
                && current
                    .as_ref()
                    .is_none_or(|current| candidate.container != current.id)
                && !bound_classes
                    .iter()
                    .any(|class| class.id == candidate.container)
            {
                continue;
            }
            if candidate.has_write_visibility {
                visibility = candidate.write_visibility;
            }
        }
        if visibility == Visibility::Public {
            return true;
        }
        if let Some(current) = &current
            && member_accessible_from_class(snapshot, current, candidate, visibility)
        {
            return true;
        }
        if bound_classes
            .iter()
            .any(|class| member_accessible_from_class(snapshot, class, candidate, visibility))
        {
            return true;
        }
    }
    receiver_classes(snapshot, &reference.receiver)
        .iter()
        .any(|class| class_has_dynamic_member_fallback(snapshot, class, reference))
}

fn member_accessible_from_class(
    snapshot: &Snapshot,
    current: &Symbol,
    candidate: &Symbol,
    visibility: Visibility,
) -> bool {
    if candidate.container == current.id {
        return true;
    }
    let Some(container) = snapshot.symbol_view(candidate.container.as_str()) else {
        return false;
    };
    if container.kind() == SymbolKind::Trait {
        if visibility == Visibility::Private {
            return class_directly_uses_trait(
                snapshot,
                &current.fully_qualified,
                container.fully_qualified(),
                &mut HashSet::new(),
            );
        }
        if class_uses_trait(
            snapshot,
            &current.fully_qualified,
            container.fully_qualified(),
            &mut HashSet::new(),
        ) {
            return true;
        }
    }
    visibility != Visibility::Private
        && snapshot.is_subtype_of(&current.fully_qualified, container.fully_qualified())
}

fn closure_binding_classes(
    document: &Document,
    snapshot: &Snapshot,
    root: &SyntaxNode,
    offset: u32,
) -> Vec<Symbol> {
    let Some(node) = root.node_at(TextSize::from(offset)) else {
        return Vec::new();
    };
    for current in node.ancestors() {
        if current.kind() != PHP_CLOSURE && current.kind() != PHP_ARROW_FUNCTION {
            continue;
        }
        let Some(call) = current.parent().and_then(|parent| query::call_at(&parent)) else {
            return Vec::new();
        };
        if call.kind() != PHP_SCOPED_CALL
            || query::argument_index(&call, &current) != Some(0)
            || !eq_ignore_case(&query::call_method_name(&call), "bind")
        {
            return Vec::new();
        }
        let Some(receiver) = query::call_receiver(&call) else {
            return Vec::new();
        };
        if receiver.kind() != PHP_NAME
            || !eq_ignore_case(
                &name_context_at(document, receiver.text_range().start().into())
                    .resolve_class(&query::name_value(&receiver)),
                "Closure",
            )
        {
            return Vec::new();
        }

        let mut scope_expression = query::argument_expression(&call, 2);
        for (index, argument) in query::arguments(&call).enumerate() {
            if eq_ignore_case(&query::argument_name(&argument), "newScope") {
                scope_expression = query::argument_expression(&call, index);
                break;
            }
        }
        let Some(scope_expression) = scope_expression else {
            return Vec::new();
        };
        let mut result = Vec::new();
        append_binding_classes(
            snapshot,
            &document.type_of(&scope_expression).ty,
            &mut result,
        );
        return result;
    }
    Vec::new()
}

fn append_binding_classes(snapshot: &Snapshot, value: &Type, result: &mut Vec<Symbol>) {
    match value.kind() {
        Kind::ClassString => {
            if value.argument_count() > 0 {
                append_binding_classes(snapshot, &value.argument(0), result);
            }
        }
        Kind::Object => {
            if value.name().is_empty() {
                return;
            }
            snapshot.visit_class_views(value.name(), |class| {
                let id = class.id();
                if !result.iter().any(|existing| existing.id.as_str() == id) {
                    result.push(class.materialize());
                }
                true
            });
        }
        Kind::Union | Kind::Intersection => {
            for member in value.arguments() {
                append_binding_classes(snapshot, member, result);
            }
        }
        _ => {}
    }
}

fn receiver_classes(snapshot: &Snapshot, receiver: &Type) -> Vec<Symbol> {
    match receiver.kind() {
        Kind::Object => snapshot.classes(receiver.name()),
        Kind::Union | Kind::Intersection => receiver
            .arguments()
            .iter()
            .flat_map(|member| receiver_classes(snapshot, member))
            .collect(),
        _ => Vec::new(),
    }
}

fn trait_name_matches(used: &str, trait_name: &str) -> bool {
    eq_ignore_case(
        used.strip_prefix('\\').unwrap_or(used),
        trait_name.strip_prefix('\\').unwrap_or(trait_name),
    )
}

fn class_uses_trait(
    snapshot: &Snapshot,
    class_name: &str,
    trait_name: &str,
    visited: &mut HashSet<String>,
) -> bool {
    if class_name.is_empty() || trait_name.is_empty() {
        return false;
    }
    if !visited.insert(lowercase(
        class_name.strip_prefix('\\').unwrap_or(class_name),
    )) {
        return false;
    }
    let mut found = false;
    snapshot.visit_class_views(class_name, |view| {
        let class = view.materialize();
        for used in class.traits() {
            if trait_name_matches(used, trait_name)
                || class_uses_trait(snapshot, used, trait_name, visited)
            {
                found = true;
                return false;
            }
        }
        for parent in class.extends() {
            if class_uses_trait(snapshot, parent, trait_name, visited) {
                found = true;
                return false;
            }
        }
        true
    });
    found
}

fn class_directly_uses_trait(
    snapshot: &Snapshot,
    class_name: &str,
    trait_name: &str,
    visited: &mut HashSet<String>,
) -> bool {
    if class_name.is_empty() || trait_name.is_empty() {
        return false;
    }
    if !visited.insert(lowercase(
        class_name.strip_prefix('\\').unwrap_or(class_name),
    )) {
        return false;
    }
    let mut found = false;
    snapshot.visit_class_views(class_name, |view| {
        let class = view.materialize();
        for used in class.traits() {
            if trait_name_matches(used, trait_name)
                || class_directly_uses_trait(snapshot, used, trait_name, visited)
            {
                found = true;
                return false;
            }
        }
        true
    });
    found
}

fn enclosing_class(document: &Document, snapshot: &Snapshot, offset: u32) -> Option<Symbol> {
    visit_scope_owners(document, snapshot, offset, |owner| {
        if owner.is_class_like() {
            return Some(owner.clone());
        }
        if !owner.container.is_empty()
            && let Some(container) = snapshot.symbol(owner.container.as_str())
            && container.is_class_like()
        {
            return Some(container);
        }
        None
    })
}

fn inaccessible_member_message(reference: &Reference, symbol: &Symbol) -> String {
    let mut visibility = symbol.visibility;
    if reference.write && symbol.kind == SymbolKind::Property && symbol.has_write_visibility {
        visibility = symbol.write_visibility;
    }
    format!(
        "Cannot access {} member {}",
        visibility_name(visibility),
        reference.name.strip_prefix('$').unwrap_or(&reference.name)
    )
}
