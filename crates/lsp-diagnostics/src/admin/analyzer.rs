//! Go `admin_diagnostics.go`: the analyzer entry point and its JavaScript
//! diagnostics.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use swlsp_admin::{
    AdminComponentIndexer, ComponentDefinition, JavaScriptDocumentAnalysis, VueComponent,
    component_definition_object, javascript_shopware_context_member,
    javascript_shopware_context_member_name_node, parse_component_object, vue_builtin_members,
    vue_type_allows_undefined, vue_types_provably_incompatible,
};
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_js::{query as jsquery, syntax as jssyntax};
use swlsp_php_types::go_to_lower;
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, TextRange};

use super::{go_ext, go_string_list, payload, text};
use crate::ProblemAnalyzer;
use crate::common::{admin_nearby_suggestions, javascript_string_content_range};

/// Diagnostics for Shopware Administration Vue components (Go
/// `AdminAnalyzer`).
#[derive(Clone, Default)]
pub struct AdminAnalyzer {
    pub(super) admin_indexer: Option<Arc<AdminComponentIndexer>>,
    pub(super) suppressed_component_diagnostics: HashSet<String>,
}

impl AdminAnalyzer {
    /// Go `NewAdminAnalyzer`.
    pub fn new(admin_indexer: Option<Arc<AdminComponentIndexer>>) -> Self {
        Self {
            admin_indexer,
            suppressed_component_diagnostics: HashSet::new(),
        }
    }

    /// Lets a more specific, version-aware migration diagnostic own selected
    /// component tags without producing duplicate generic deprecation or
    /// not-found problems on the same source range (Go
    /// `SuppressComponentDiagnostics`).
    pub fn suppress_component_diagnostics<S: AsRef<str>>(&mut self, names: &[S]) -> &mut Self {
        for name in names {
            self.suppressed_component_diagnostics
                .insert(name.as_ref().to_owned());
        }
        self
    }

    pub(super) fn indexer(&self) -> Option<&AdminComponentIndexer> {
        self.admin_indexer.as_deref()
    }

    fn js_diagnostics(
        &self,
        ctx: &Context,
        document: &TextDocument,
    ) -> Result<Vec<Problem>, BoxError> {
        let mut diagnostics = Vec::new();
        let Some(root) = document.root() else {
            return Ok(diagnostics);
        };
        let analysis = JavaScriptDocumentAnalysis::new(&root);

        for call in analysis.calls(&["Component.extend", "Shopware.Component.extend"]) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let Some(parent_name_node) = jsquery::string_argument(&call, 1) else {
                continue;
            };
            let parent_name = jsquery::string_value(&parent_name_node);
            if parent_name.is_empty() {
                continue;
            }
            let missing = match self.indexer() {
                // Go dereferences a nil indexer here; without an index no
                // component is registered.
                None => true,
                Some(indexer) => indexer
                    .get_component(&parent_name)
                    .map_or(true, |components| components.is_empty()),
            };
            if missing {
                diagnostics.push(Problem {
                    range: parent_name_node.range_trimmed_trivia(),
                    message: format!("Parent component '{parent_name}' is not registered"),
                    source: "shopware".into(),
                    severity: Some(DiagnosticSeverity::WARNING),
                    id: "admin.component.parent-not-found".into(),
                    payload: Some(payload(vec![("componentName", text(&parent_name))])),
                    ..Problem::default()
                });
            }
        }
        if ctx.is_cancelled() {
            return Ok(Vec::new());
        }
        diagnostics.extend(self.unknown_instance_member_diagnostics(document, &root, &analysis));
        diagnostics.extend(self.deprecated_instance_member_diagnostics(document, &analysis));
        diagnostics.extend(self.application_container_diagnostics(document, &analysis)?);
        diagnostics.extend(self.shopware_context_diagnostics(document, &analysis)?);
        diagnostics.extend(self.shopware_utils_diagnostics(document, &analysis)?);
        diagnostics.extend(self.shopware_event_bus_diagnostics(document, &analysis)?);
        diagnostics.extend(self.javascript_module_route_diagnostics(document, &analysis)?);
        diagnostics.extend(self.javascript_registry_diagnostics(document, &analysis)?);
        Ok(diagnostics)
    }

    fn shopware_event_bus_diagnostics(
        &self,
        document: &TextDocument,
        analysis: &JavaScriptDocumentAnalysis,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(indexer) = self.indexer() else {
            return Ok(Vec::new());
        };
        let Ok(path) = swlsp_uriutil::path(&document.uri) else {
            return Ok(Vec::new());
        };
        let mut diagnostics = Vec::new();
        for call in analysis.iterate_calls(&[]) {
            let Some(event_node) = jsquery::string_argument(call, 0) else {
                continue;
            };
            let Some((operation, event_name)) = analysis.shopware_event_bus_event_at(&event_node)
            else {
                continue;
            };
            if event_name.is_empty() {
                continue;
            }
            // The core EventBus map is open to extensions and legacy events.
            let Some(event) = indexer.resolve_shopware_event_bus_event(&event_name, &path)? else {
                continue;
            };
            let second_argument = jsquery::argument(call, 1);
            match operation.as_str() {
                "emit" => {
                    let Some(argument) = second_argument else {
                        if !vue_type_allows_undefined(&event.type_) {
                            diagnostics.push(Problem {
                                range: javascript_string_content_range(
                                    &event_node,
                                    document.source(),
                                ),
                                message: format!(
                                    "EventBus event '{event_name}' requires a payload of type {}",
                                    event.type_
                                ),
                                source: "shopware".into(),
                                severity: Some(DiagnosticSeverity::WARNING),
                                id: "admin.event-bus.missing-payload".into(),
                                payload: Some(payload(vec![
                                    ("eventName", text(&event_name)),
                                    ("expectedType", text(&event.type_)),
                                ])),
                                ..Problem::default()
                            });
                        }
                        continue;
                    };
                    if let Some(problem) = self.event_bus_argument_diagnostic(
                        indexer,
                        &path,
                        &event_name,
                        &event.type_,
                        &argument,
                        "emit",
                    )? {
                        diagnostics.push(problem);
                    }
                }
                "on" => {
                    let Some(argument) = second_argument else {
                        diagnostics.push(Problem {
                            range: javascript_string_content_range(&event_node, document.source()),
                            message: format!(
                                "EventBus.on('{event_name}') requires an event handler"
                            ),
                            source: "shopware".into(),
                            severity: Some(DiagnosticSeverity::WARNING),
                            id: "admin.event-bus.missing-handler".into(),
                            payload: Some(payload(vec![("eventName", text(&event_name))])),
                            ..Problem::default()
                        });
                        continue;
                    };
                    if let Some(problem) = self.event_bus_argument_diagnostic(
                        indexer,
                        &path,
                        &event_name,
                        "Function",
                        &argument,
                        "on",
                    )? {
                        diagnostics.push(problem);
                    }
                }
                "off" => {
                    let Some(argument) = second_argument else {
                        continue;
                    };
                    if let Some(problem) = self.event_bus_argument_diagnostic(
                        indexer,
                        &path,
                        &event_name,
                        "Function",
                        &argument,
                        "off",
                    )? {
                        diagnostics.push(problem);
                    }
                }
                _ => {}
            }
        }
        Ok(diagnostics)
    }

    fn event_bus_argument_diagnostic(
        &self,
        indexer: &AdminComponentIndexer,
        path: &str,
        event_name: &str,
        expected_type: &str,
        argument: &SyntaxNode,
        operation: &str,
    ) -> Result<Option<Problem>, BoxError> {
        let Some(expression) = argument.children().next() else {
            return Ok(None);
        };
        let Some(actual_type) =
            indexer.resolve_javascript_expression_type(&expression.text().to_string(), path)?
        else {
            return Ok(None);
        };
        if !vue_types_provably_incompatible(expected_type, &actual_type) {
            return Ok(None);
        }
        let (id, message) = if operation == "on" || operation == "off" {
            (
                "admin.event-bus.handler-type",
                format!(
                    "EventBus.{operation}('{event_name}') expects a function handler, but the argument has type {actual_type}"
                ),
            )
        } else {
            (
                "admin.event-bus.payload-type",
                format!(
                    "EventBus event '{event_name}' expects payload {expected_type}, but the argument has type {actual_type}"
                ),
            )
        };
        Ok(Some(Problem {
            range: expression.range_trimmed_trivia(),
            message,
            source: "shopware".into(),
            severity: Some(DiagnosticSeverity::WARNING),
            id: id.into(),
            payload: Some(payload(vec![
                ("eventName", text(event_name)),
                ("expectedType", text(expected_type)),
                ("actualType", text(&actual_type)),
            ])),
            ..Problem::default()
        }))
    }

    fn shopware_utils_diagnostics(
        &self,
        document: &TextDocument,
        analysis: &JavaScriptDocumentAnalysis,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(indexer) = self.indexer() else {
            return Ok(Vec::new());
        };
        let Ok(path) = swlsp_uriutil::path(&document.uri) else {
            return Ok(Vec::new());
        };
        let mut contracts: HashMap<String, MemberContract> = HashMap::new();
        let mut diagnostics = Vec::new();
        let mut seen = HashSet::new();
        for expression in analysis.nodes(&[jssyntax::JS_MEMBER_EXPRESSION]) {
            let Some((receiver, member_name)) = analysis.shopware_utils_member(&expression) else {
                continue;
            };
            if member_name.is_empty() {
                continue;
            }
            let receiver_path = receiver.join(".");
            if !contracts.contains_key(&receiver_path) {
                let shape = indexer.resolve_shopware_utils(&receiver_path, &path)?;
                contracts.insert(
                    receiver_path.clone(),
                    MemberContract::new(shape.complete, shape.members.iter().map(|m| &m.name)),
                );
            }
            let contract = &contracts[&receiver_path];
            if !contract.complete || contract.known.contains(&member_name) {
                continue;
            }
            let suggestions = admin_nearby_suggestions(&member_name, &contract.names);
            if suggestions.is_empty() {
                continue;
            }
            let Some(name_node) = analysis.shopware_utils_member_name_node(&expression) else {
                continue;
            };
            let range = name_node.range_trimmed_trivia();
            if !seen.insert(range) {
                continue;
            }
            let mut qualified_receiver = "Shopware.Utils".to_owned();
            if !receiver_path.is_empty() {
                qualified_receiver.push('.');
                qualified_receiver.push_str(&receiver_path);
            }
            diagnostics.push(Problem {
                range,
                message: format!("Unknown member '{member_name}' on {qualified_receiver}"),
                source: "shopware".into(),
                severity: Some(DiagnosticSeverity::WARNING),
                id: "admin.shopware-utils.unknown-member".into(),
                payload: Some(payload(vec![
                    ("receiver", text(&receiver_path)),
                    ("memberName", text(&member_name)),
                    ("suggestions", go_string_list(suggestions)),
                ])),
                ..Problem::default()
            });
        }
        Ok(diagnostics)
    }

    fn shopware_context_diagnostics(
        &self,
        document: &TextDocument,
        analysis: &JavaScriptDocumentAnalysis,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(indexer) = self.indexer() else {
            return Ok(Vec::new());
        };
        let Ok(path) = swlsp_uriutil::path(&document.uri) else {
            return Ok(Vec::new());
        };
        let mut contracts: HashMap<String, MemberContract> = HashMap::new();
        let mut diagnostics = Vec::new();
        let mut seen = HashSet::new();
        for expression in analysis.nodes(&[jssyntax::JS_MEMBER_EXPRESSION]) {
            let Some((receiver, member_name)) = javascript_shopware_context_member(&expression)
            else {
                continue;
            };
            if member_name.is_empty() {
                continue;
            }
            let receiver_path = receiver.join(".");
            if !contracts.contains_key(&receiver_path) {
                let shape = indexer.resolve_shopware_context(&receiver_path, &path)?;
                contracts.insert(
                    receiver_path.clone(),
                    MemberContract::new(shape.complete, shape.members.iter().map(|m| &m.name)),
                );
            }
            let contract = &contracts[&receiver_path];
            if !contract.complete || contract.known.contains(&member_name) {
                continue;
            }
            let suggestions = admin_nearby_suggestions(&member_name, &contract.names);
            if suggestions.is_empty() {
                continue;
            }
            let Some(name_node) = javascript_shopware_context_member_name_node(&expression) else {
                continue;
            };
            let range = name_node.range_trimmed_trivia();
            if !seen.insert(range) {
                continue;
            }
            diagnostics.push(Problem {
                range,
                message: format!(
                    "Unknown member '{member_name}' on Shopware.Context.{receiver_path}"
                ),
                source: "shopware".into(),
                severity: Some(DiagnosticSeverity::WARNING),
                id: "admin.shopware-context.unknown-member".into(),
                payload: Some(payload(vec![
                    ("receiver", text(&receiver_path)),
                    ("memberName", text(&member_name)),
                    ("suggestions", go_string_list(suggestions)),
                ])),
                ..Problem::default()
            });
        }
        Ok(diagnostics)
    }

    fn application_container_diagnostics(
        &self,
        document: &TextDocument,
        analysis: &JavaScriptDocumentAnalysis,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(indexer) = self.indexer() else {
            return Ok(Vec::new());
        };
        let Ok(path) = swlsp_uriutil::path(&document.uri) else {
            return Ok(Vec::new());
        };
        let mut contracts: HashMap<String, MemberContract> = HashMap::new();
        let mut diagnostics = Vec::new();
        let mut seen = HashSet::new();
        for expression in analysis.nodes(&[jssyntax::JS_MEMBER_EXPRESSION]) {
            let Some((container_name, member_name)) =
                analysis.application_container_member(&expression)
            else {
                continue;
            };
            if member_name.is_empty() {
                continue;
            }
            if !contracts.contains_key(&container_name) {
                let shape = indexer.resolve_application_container(&container_name, &path)?;
                contracts.insert(
                    container_name.clone(),
                    MemberContract::new(shape.complete, shape.members.iter().map(|m| &m.name)),
                );
            }
            let contract = &contracts[&container_name];
            if !contract.complete || contract.known.contains(&member_name) {
                continue;
            }
            let suggestions = admin_nearby_suggestions(&member_name, &contract.names);
            if suggestions.is_empty() {
                continue;
            }
            let Some(name_node) = analysis.application_container_member_name_node(&expression)
            else {
                continue;
            };
            let range = name_node.range_trimmed_trivia();
            if !seen.insert(range) {
                continue;
            }
            diagnostics.push(Problem {
                range,
                message: format!(
                    "Unknown member '{member_name}' on Application '{container_name}' container"
                ),
                source: "shopware".into(),
                severity: Some(DiagnosticSeverity::WARNING),
                id: "admin.application-container.unknown-member".into(),
                payload: Some(payload(vec![
                    ("containerName", text(&container_name)),
                    ("memberName", text(&member_name)),
                    ("suggestions", go_string_list(suggestions)),
                ])),
                ..Problem::default()
            });
        }
        Ok(diagnostics)
    }

    fn unknown_instance_member_diagnostics(
        &self,
        document: &TextDocument,
        root: &SyntaxNode,
        analysis: &JavaScriptDocumentAnalysis,
    ) -> Vec<Problem> {
        let Some(indexer) = self.indexer() else {
            return Vec::new();
        };
        let Ok(path) = swlsp_uriutil::path(&document.uri) else {
            return Vec::new();
        };
        let components = match indexer.get_components_by_definition_path(&path) {
            Ok(components) if !components.is_empty() => components,
            _ => return Vec::new(),
        };
        let scopes =
            admin_instance_member_scopes(root, document.line_index(), &path, analysis, &components);
        if scopes.is_empty() {
            return Vec::new();
        }

        let mut diagnostics = Vec::new();
        let mut seen = HashSet::new();
        for member_expression in analysis.nodes(&[jssyntax::JS_MEMBER_EXPRESSION]) {
            let Some(name) = jsquery::this_member(&member_expression) else {
                continue;
            };
            if name.is_empty() || name.starts_with('$') {
                continue;
            }
            let Some(scope) = smallest_admin_instance_member_scope(&member_expression, &scopes)
            else {
                continue;
            };
            if scope.open || scope.known.contains(&name) {
                continue;
            }
            let Some(name_node) = jsquery::this_member_name_node(&member_expression) else {
                continue;
            };
            let range = name_node.range_trimmed_trivia();
            if !seen.insert(range) {
                continue;
            }
            diagnostics.push(Problem {
                range,
                message: format!("Unknown Administration component instance member '{name}'"),
                source: "shopware".into(),
                severity: Some(DiagnosticSeverity::WARNING),
                id: "admin.component.unknown-instance-member".into(),
                payload: Some(payload(vec![
                    ("memberName", text(&name)),
                    (
                        "suggestions",
                        go_string_list(admin_nearby_suggestions(&name, &scope.known_names)),
                    ),
                ])),
                ..Problem::default()
            });
        }
        diagnostics
    }
}

impl ProblemAnalyzer for AdminAnalyzer {
    fn input_revision(&self, document: &swlsp_lsp::TextDocument) -> Option<u64> {
        // Only Administration files are analyzed, against the Administration
        // index and its open-document overlays.
        if !document.uri.contains("Resources/app/administration") {
            Some(0)
        } else {
            Some(crate::inputs(&self.admin_indexer))
        }
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let uri = &document.uri;
        // Only process files in the administration directory.
        if !uri.contains("Resources/app/administration") {
            return Ok(Vec::new());
        }
        let ext = go_to_lower(go_ext(uri));
        match ext.as_str() {
            ".js" | ".ts" => self.js_diagnostics(ctx, document),
            ".twig" => self.twig_diagnostics(ctx, document),
            ".vue" => {
                let mut diagnostics = self.js_diagnostics(ctx, document)?;
                diagnostics.extend(self.twig_diagnostics(ctx, document)?);
                Ok(diagnostics)
            }
            _ => Ok(Vec::new()),
        }
    }
}

/// A closed or open member contract keyed by receiver.
struct MemberContract {
    complete: bool,
    known: HashSet<String>,
    names: Vec<String>,
}

impl MemberContract {
    fn new<'a>(complete: bool, members: impl Iterator<Item = &'a String>) -> Self {
        let mut known = HashSet::new();
        let mut names = Vec::new();
        for name in members {
            known.insert(name.clone());
            names.push(name.clone());
        }
        Self {
            complete,
            known,
            names,
        }
    }
}

struct AdminInstanceMemberScope {
    object: SyntaxNode,
    components: Vec<VueComponent>,
    current: Option<ComponentDefinition>,
    known: HashSet<String>,
    known_names: Vec<String>,
    open: bool,
}

fn admin_instance_member_scopes(
    root: &SyntaxNode,
    line_index: &LineIndex,
    file_path: &str,
    analysis: &JavaScriptDocumentAnalysis,
    components: &[VueComponent],
) -> Vec<AdminInstanceMemberScope> {
    let mut by_name: HashMap<&str, Vec<VueComponent>> = HashMap::new();
    for component in components {
        by_name
            .entry(component.name.as_str())
            .or_default()
            .push(component.clone());
    }

    let mut scopes = Vec::new();
    let mut seen: HashSet<TextRange> = HashSet::new();
    let mut add = |object: Option<SyntaxNode>, effective: Vec<VueComponent>| {
        let Some(object) = object else {
            return;
        };
        let key = object.range_trimmed_trivia();
        if seen.contains(&key) {
            return;
        }
        let Some(current) = parse_component_object(&object, file_path, line_index) else {
            return;
        };
        seen.insert(key);
        let mut scope = AdminInstanceMemberScope {
            object,
            components: effective,
            current: Some(current),
            known: HashSet::new(),
            known_names: Vec::new(),
            open: false,
        };
        populate_admin_instance_member_scope(&mut scope);
        scopes.push(scope);
    };

    for call in analysis.iterate_calls(&[
        "Component.register",
        "Shopware.Component.register",
        "Component.extend",
        "Shopware.Component.extend",
        "Component.override",
        "Shopware.Component.override",
    ]) {
        let definition_index = if jsquery::call_name(call)
            .unwrap_or_default()
            .ends_with(".extend")
        {
            2
        } else {
            1
        };
        let object = jsquery::argument_expression(call, definition_index)
            .and_then(|expression| component_definition_object(&expression));
        let name = jsquery::string_argument(call, 0)
            .map(|node| jsquery::string_value(&node))
            .unwrap_or_default();
        let effective = by_name.get(name.as_str()).cloned().unwrap_or_default();
        add(object, effective);
    }
    for export in jsquery::export_defaults(root) {
        let object = jsquery::export_default_expression(&export)
            .and_then(|expression| component_definition_object(&expression));
        add(object, components.to_vec());
    }
    scopes
}

fn populate_admin_instance_member_scope(scope: &mut AdminInstanceMemberScope) {
    let mut known = HashSet::new();
    let mut known_names = Vec::new();
    let mut add = |name: &str| {
        if name.is_empty() || known.contains(name) {
            return;
        }
        known.insert(name.to_owned());
        known_names.push(name.to_owned());
    };
    for member in vue_builtin_members() {
        add(&member.name);
    }
    if let Some(current) = &scope.current {
        scope.open = current.open_runtime_members;
        for member in &current.members {
            add(&member.name);
        }
        for assignment in &current.assignments {
            add(&assignment.target);
        }
    }
    if !scope.components.is_empty() {
        let mut common = admin_component_instance_member_names(&scope.components[0]);
        for component in &scope.components {
            scope.open = scope.open || component.open_runtime_members;
        }
        for component in &scope.components[1..] {
            let names = admin_component_instance_member_names(component);
            common.retain(|name| names.contains(name));
        }
        for member in scope.components[0].template_members() {
            if common.contains(&member.name) {
                add(&member.name);
            }
        }
        for assignment in &scope.components[0].assignments {
            if common.contains(&assignment.target) {
                add(&assignment.target);
            }
        }
    }
    scope.known = known;
    scope.known_names = known_names;
}

fn admin_component_instance_member_names(component: &VueComponent) -> HashSet<String> {
    let mut result = HashSet::new();
    for member in component.template_members() {
        if !member.name.is_empty() {
            result.insert(member.name);
        }
    }
    for assignment in &component.assignments {
        if !assignment.target.is_empty() {
            result.insert(assignment.target.clone());
        }
    }
    result
}

fn smallest_admin_instance_member_scope<'a>(
    node: &SyntaxNode,
    scopes: &'a [AdminInstanceMemberScope],
) -> Option<&'a AdminInstanceMemberScope> {
    let node_range = node.range_trimmed_trivia();
    let mut result: Option<&AdminInstanceMemberScope> = None;
    for scope in scopes {
        let scope_range = scope.object.range_trimmed_trivia();
        if node_range.start() < scope_range.start() || node_range.end() > scope_range.end() {
            continue;
        }
        match result {
            None => result = Some(scope),
            Some(current) => {
                if scope_range.len() < current.object.range_trimmed_trivia().len() {
                    result = Some(scope);
                }
            }
        }
    }
    result
}
