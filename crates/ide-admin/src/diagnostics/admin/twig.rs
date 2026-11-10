//! Template diagnostics.

use std::collections::{HashMap, HashSet};

use swlsp_admin::{
    AdminTwigRegistryReference, JavaScriptDocumentAnalysis, TwigVueMember, TwigVueMemberAccess,
    VueComponent, VueDirectiveReference, normalize_slot_name, twig_block_scope_member_at,
    twig_directive_references, twig_registry_references, twig_scoped_slot_at_offset,
    twig_vue_binding_at_offset, twig_vue_expression_member_accesses,
    twig_vue_expression_root_identifiers, twig_vue_local_root_identifier_ranges,
    vue_builtin_members, vue_template_globals,
};
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::{DiagnosticSeverity, DiagnosticTag};
use swlsp_parser_js::{query as jsquery, syntax as jssyntax};
use swlsp_parser_twig::ast::{AstNode, HtmlAttribute};
use swlsp_parser_twig::{query as twigquery, syntax as twigsyntax};
use swlsp_suggestion::similar;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};

use super::analyzer::AdminAnalyzer;
use super::{payload, string_array, text};
use crate::diagnostics::common::admin_nearby_suggestions;

/// Per-document facts shared by the Twig diagnostics.
pub(super) struct AdminTwigDiagnosticDocument {
    pub template_path: String,
    pub live_owner: Option<VueComponent>,
    pub root_identifiers: Vec<TwigVueMember>,
    pub local_identifiers: HashSet<TextRange>,
    pub member_accesses: Vec<TwigVueMemberAccess>,
    pub registry_references: Vec<AdminTwigRegistryReference>,
    pub directive_references: Vec<VueDirectiveReference>,
    /// Template-tag resolutions keyed by the lowercased tag name.
    pub components: HashMap<String, Option<VueComponent>>,
}

impl AdminAnalyzer {
    pub(super) fn twig_diagnostics(
        &self,
        ctx: &Context,
        document: &TextDocument,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let Some(indexer) = self.indexer() else {
            // Without an index nothing can be validated.
            return Ok(Vec::new());
        };
        let template_path = swlsp_uriutil::path(&document.uri)?;
        let live_owner = indexer.get_component_for_document(
            &template_path,
            Some(&root),
            document.source(),
            Some(document.line_index()),
        )?;
        let source = document.source();
        let root_identifiers = twig_vue_expression_root_identifiers(&root, source);
        let local_identifiers = if live_owner.is_some() {
            twig_vue_local_root_identifier_ranges(&root, source, &root_identifiers)
        } else {
            HashSet::new()
        };
        let mut analysis = AdminTwigDiagnosticDocument {
            template_path,
            live_owner,
            member_accesses: twig_vue_expression_member_accesses(&root, source),
            registry_references: twig_registry_references(&root),
            directive_references: twig_directive_references(&root),
            root_identifiers,
            local_identifiers,
            components: HashMap::new(),
        };

        let mut diagnostics = Vec::new();
        diagnostics.extend(self.twig_directive_diagnostics(&analysis)?);
        diagnostics.extend(self.twig_privilege_diagnostics(&analysis)?);
        diagnostics.extend(self.twig_module_route_diagnostics(&analysis)?);
        self.find_html_start_tags(ctx, &root, source, &mut analysis, &mut diagnostics)?;
        if ctx.is_cancelled() {
            return Ok(Vec::new());
        }
        diagnostics.extend(self.unknown_scoped_slot_binding_diagnostics(ctx, &root, &analysis)?);
        diagnostics
            .extend(self.unknown_scoped_slot_member_diagnostics(ctx, &root, source, &analysis)?);
        diagnostics
            .extend(self.unknown_vue_binding_member_diagnostics(ctx, &root, source, &analysis)?);
        diagnostics.extend(self.unknown_twig_component_member_diagnostics(ctx, &root, &analysis));
        diagnostics.extend(self.deprecated_twig_component_member_diagnostics(ctx, &analysis));
        self.check_block_references(&root, &analysis, &mut diagnostics);
        Ok(diagnostics)
    }

    pub(super) fn diagnostic_component_for_template_tag(
        &self,
        analysis: &mut AdminTwigDiagnosticDocument,
        name: &str,
    ) -> Result<Option<VueComponent>, BoxError> {
        let key = name.trim().to_lowercase();
        if let Some(resolved) = analysis.components.get(&key) {
            return Ok(resolved.clone());
        }
        let Some(indexer) = self.indexer() else {
            return Ok(None);
        };
        let component = indexer.get_component_for_template_tag(
            &analysis.template_path,
            name,
            analysis.live_owner.as_ref(),
        )?;
        analysis.components.insert(key, component.clone());
        Ok(component)
    }

    fn unknown_twig_component_member_diagnostics(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        analysis: &AdminTwigDiagnosticDocument,
    ) -> Vec<Problem> {
        let Some(component) = &analysis.live_owner else {
            return Vec::new();
        };
        let mut known = HashSet::new();
        let mut candidates: Vec<String> = Vec::new();
        let mut add_known = |name: &str, suggest: bool| {
            if name.is_empty() || known.contains(name) {
                return;
            }
            known.insert(name.to_owned());
            if suggest {
                candidates.push(name.to_owned());
            }
        };
        for member in component.template_members() {
            add_known(&member.name, !component.open_runtime_members);
        }
        for member in vue_builtin_members() {
            add_known(&member.name, true);
        }
        for global in vue_template_globals() {
            add_known(&global.name, true);
        }

        let mut diagnostics = Vec::new();
        let mut seen = HashSet::new();
        for identifier in &analysis.root_identifiers {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            if known.contains(&identifier.name)
                || seen.contains(&identifier.range)
                || analysis.local_identifiers.contains(&identifier.range)
            {
                continue;
            }
            if let Some(node) = root.node_at(identifier.range.start())
                && twig_block_scope_member_at(component, &node, &identifier.name).is_some()
            {
                continue;
            }
            let suggestions = admin_nearby_suggestions(&identifier.name, &candidates);
            // Runtime mixins and application plugins can add arbitrary
            // template globals. Report only a close misspelling of a
            // statically known name.
            if suggestions.is_empty() {
                continue;
            }
            seen.insert(identifier.range);
            diagnostics.push(Problem {
                range: identifier.range,
                message: format!(
                    "Unknown Administration component template member '{}'",
                    identifier.name
                ),
                source: "shopware".into(),
                severity: Some(DiagnosticSeverity::WARNING),
                id: "admin.component.unknown-template-member".into(),
                payload: Some(payload(vec![
                    ("memberName", text(&identifier.name)),
                    ("suggestions", string_array(suggestions)),
                ])),
                ..Problem::default()
            });
        }
        diagnostics
    }

    pub(super) fn deprecated_instance_member_diagnostics(
        &self,
        document: &TextDocument,
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
        let mut diagnostics = Vec::new();
        let mut seen = HashSet::new();
        for expression in analysis.nodes(&[jssyntax::JS_MEMBER_EXPRESSION]) {
            let Some(name) = jsquery::this_member(&expression) else {
                continue;
            };
            if name.is_empty() {
                continue;
            }
            let Some(name_node) = jsquery::this_member_name_node(&expression) else {
                continue;
            };
            let range = name_node.range_trimmed_trivia();
            if seen.contains(&range) {
                continue;
            }
            let message = common_deprecated_admin_member(&components, &name);
            if message.is_empty() {
                continue;
            }
            seen.insert(range);
            diagnostics.push(deprecated_admin_member_problem(&name, &message, range));
        }
        diagnostics
    }

    fn deprecated_twig_component_member_diagnostics(
        &self,
        ctx: &Context,
        analysis: &AdminTwigDiagnosticDocument,
    ) -> Vec<Problem> {
        let Some(component) = &analysis.live_owner else {
            return Vec::new();
        };
        let mut diagnostics = Vec::new();
        for identifier in &analysis.root_identifiers {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            if analysis.local_identifiers.contains(&identifier.range) {
                continue;
            }
            let Some(member) = component.template_member(&identifier.name) else {
                continue;
            };
            if member.deprecated.is_empty() {
                continue;
            }
            diagnostics.push(deprecated_admin_member_problem(
                &member.name,
                &member.deprecated,
                identifier.range,
            ));
        }
        diagnostics
    }

    fn unknown_scoped_slot_binding_diagnostics(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        analysis: &AdminTwigDiagnosticDocument,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(indexer) = self.indexer() else {
            return Ok(Vec::new());
        };
        let mut diagnostics = Vec::new();
        let mut seen_scopes = HashSet::new();
        for attribute_node in twigquery::iterate_nodes(root, &[twigsyntax::HTML_ATTRIBUTE]) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let Some(attribute) = HtmlAttribute::cast(attribute_node.clone()) else {
                continue;
            };
            let attribute_name =
                twigquery::html_attribute_name(&attribute_node).unwrap_or_default();
            if normalize_slot_name(&attribute_name).is_empty() {
                continue;
            }
            let Some(inner) = attribute.value().and_then(|value| value.inner()) else {
                continue;
            };
            let Some(scope) =
                twig_scoped_slot_at_offset(root, inner.syntax().text_range().start().into())
            else {
                continue;
            };
            if !seen_scopes.insert(scope.binding_range) {
                continue;
            }
            let Some(resolved) = indexer.resolve_twig_scoped_slot_for_owner(
                root,
                scope.binding_range.start().into(),
                &analysis.template_path,
                analysis.live_owner.as_ref(),
            )?
            else {
                continue;
            };
            if !resolved.slot.members_complete {
                continue;
            }
            let member_names: Vec<String> = resolved
                .slot
                .members
                .iter()
                .map(|member| member.name.clone())
                .collect();
            let component_name = if resolved.scope.component_name.is_empty() {
                resolved.component.name.clone()
            } else {
                resolved.scope.component_name.clone()
            };
            for binding in &scope.bindings {
                if binding.whole_object
                    || binding.member_name.is_empty()
                    || binding.member_range.is_empty()
                {
                    continue;
                }
                if resolved.slot.member(&binding.member_name).is_some() {
                    continue;
                }
                diagnostics.push(Problem {
                    range: binding.member_range,
                    message: format!(
                        "Unknown scoped-slot prop '{}' on '{}'",
                        binding.member_name,
                        resolved.qualified_name()
                    ),
                    source: "shopware".into(),
                    severity: Some(DiagnosticSeverity::WARNING),
                    id: "admin.component.unknown-slot-prop".into(),
                    payload: Some(payload(vec![
                        ("componentName", text(&component_name)),
                        ("componentNames", string_array(resolved.component_names())),
                        ("slotName", text(&resolved.scope.slot_name)),
                        ("memberName", text(&binding.member_name)),
                        (
                            "suggestions",
                            string_array(similar(&binding.member_name, &member_names)),
                        ),
                    ])),
                    ..Problem::default()
                });
            }
        }
        Ok(diagnostics)
    }

    fn twig_directive_diagnostics(
        &self,
        analysis: &AdminTwigDiagnosticDocument,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(indexer) = self.indexer() else {
            return Ok(Vec::new());
        };
        if analysis.directive_references.is_empty() {
            return Ok(Vec::new());
        }
        let directives = indexer.get_all_directives_for_template(&analysis.template_path)?;
        if directives.is_empty() {
            return Ok(Vec::new());
        }
        let mut known = HashSet::new();
        let mut names = Vec::new();
        for directive in &directives {
            if directive.name.is_empty() || !known.insert(directive.name.clone()) {
                continue;
            }
            names.push(directive.name.clone());
        }
        let mut result = Vec::new();
        for reference in &analysis.directive_references {
            if known.contains(&reference.name) {
                continue;
            }
            let suggestions = admin_nearby_suggestions(&reference.name, &names);
            // Custom Vue directives may be installed by third-party code
            // outside the Shopware registry. Report only likely misspellings
            // of an indexed name.
            if suggestions.is_empty() {
                continue;
            }
            result.push(Problem {
                range: reference.range,
                message: format!(
                    "Administration Vue directive 'v-{}' is not registered",
                    reference.name
                ),
                source: "shopware".into(),
                severity: Some(DiagnosticSeverity::WARNING),
                id: "admin.directive.not-found".into(),
                payload: Some(payload(vec![
                    ("directiveName", text(&reference.name)),
                    ("suggestions", string_array(suggestions)),
                ])),
                ..Problem::default()
            });
        }
        Ok(result)
    }

    fn unknown_vue_binding_member_diagnostics(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        source: &str,
        analysis: &AdminTwigDiagnosticDocument,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(indexer) = self.indexer() else {
            return Ok(Vec::new());
        };
        let template_path = &analysis.template_path;
        let live_component = analysis.live_owner.as_ref();
        let mut result = Vec::new();
        for access in &analysis.member_accesses {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let offset: u32 = access.member_range.start().into();
            if live_component.is_none() || analysis.local_identifiers.contains(&access.root_range) {
                let resolved_slot = match root.node_at(access.member_range.start()) {
                    Some(node) => indexer.resolve_twig_scoped_slot_member_for_owner(
                        root,
                        &node,
                        source,
                        offset,
                        template_path,
                        live_component,
                    )?,
                    None => None,
                };
                let resolved = indexer.resolve_twig_vue_member_for_component(
                    root,
                    source,
                    offset,
                    template_path,
                    live_component,
                )?;
                if let Some(resolved) = resolved {
                    if resolved.member_found
                        || !resolved.receiver_found
                        || !resolved.members_complete
                    {
                        continue;
                    }
                    if let Some(slot) = &resolved_slot
                        && resolved.binding.scope_range.len()
                            > slot.scoped.scope.template_range.len()
                    {
                        continue;
                    }
                    result.push(unknown_typed_vue_member_problem(
                        access,
                        &access.qualified_name(),
                        "typed Vue binding",
                        twig_vue_member_names(&resolved.receiver_members),
                    ));
                }
                continue;
            }
            let Some(resolved_instance) = indexer
                .resolve_twig_vue_instance_member_access_for_component(
                    root,
                    source,
                    access,
                    template_path,
                    live_component,
                )?
            else {
                continue;
            };
            if resolved_instance.member_found
                || !resolved_instance.receiver_found
                || !resolved_instance.members_complete
            {
                continue;
            }
            result.push(unknown_typed_vue_member_problem(
                access,
                &resolved_instance.qualified_name(),
                "typed component member",
                twig_vue_member_names(&resolved_instance.receiver_members),
            ));
        }
        Ok(result)
    }

    fn unknown_scoped_slot_member_diagnostics(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        source: &str,
        analysis: &AdminTwigDiagnosticDocument,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(indexer) = self.indexer() else {
            return Ok(Vec::new());
        };
        let mut diagnostics = Vec::new();
        for access in &analysis.member_accesses {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let Some(node) = root.node_at(access.member_range.start()) else {
                continue;
            };
            let Some(resolved) = indexer.resolve_twig_scoped_slot_member_for_owner(
                root,
                &node,
                source,
                access.member_range.start().into(),
                &analysis.template_path,
                analysis.live_owner.as_ref(),
            )?
            else {
                continue;
            };
            if resolved.member_found || !resolved.receiver_found || !resolved.members_complete {
                continue;
            }
            if let Some(vue_binding) =
                twig_vue_binding_at_offset(root, source, access.root_range.start().into())
                && vue_binding.scope_range.len() <= resolved.scoped.scope.template_range.len()
            {
                continue;
            }
            let scoped = &resolved.scoped;
            let member_names: Vec<String> = scoped
                .slot
                .members
                .iter()
                .map(|member| member.name.clone())
                .collect();
            let component_name = if scoped.scope.component_name.is_empty() {
                scoped.component.name.clone()
            } else {
                scoped.scope.component_name.clone()
            };
            diagnostics.push(Problem {
                range: access.member_range,
                message: format!(
                    "Unknown scoped-slot prop '{}' on '{}'",
                    access.member,
                    scoped.qualified_name()
                ),
                source: "shopware".into(),
                severity: Some(DiagnosticSeverity::WARNING),
                id: "admin.component.unknown-slot-prop".into(),
                payload: Some(payload(vec![
                    ("componentName", text(&component_name)),
                    ("componentNames", string_array(scoped.component_names())),
                    ("slotName", text(&scoped.scope.slot_name)),
                    ("memberName", text(&access.member)),
                    (
                        "suggestions",
                        string_array(similar(&access.member, &member_names)),
                    ),
                ])),
                ..Problem::default()
            });
        }
        Ok(diagnostics)
    }
}

/// The joined deprecation when every component deprecates the member, else
/// empty.
fn common_deprecated_admin_member(components: &[VueComponent], name: &str) -> String {
    let mut messages: Vec<String> = Vec::new();
    for component in components {
        let Some(member) = component.template_member(name) else {
            return String::new();
        };
        if member.deprecated.is_empty() {
            return String::new();
        }
        if !messages.contains(&member.deprecated) {
            messages.push(member.deprecated);
        }
    }
    messages.join(" / ")
}

fn deprecated_admin_member_problem(name: &str, deprecation: &str, range: TextRange) -> Problem {
    Problem {
        range,
        message: format!("Administration component member '{name}' is deprecated: {deprecation}"),
        source: "shopware-lsp".into(),
        severity: Some(DiagnosticSeverity::HINT),
        id: "admin.component.deprecated-member".into(),
        tags: vec![DiagnosticTag::DEPRECATED],
        ..Problem::default()
    }
}

fn twig_vue_member_names(members: &[TwigVueMember]) -> Vec<String> {
    members.iter().map(|member| member.name.clone()).collect()
}

fn unknown_typed_vue_member_problem(
    access: &TwigVueMemberAccess,
    qualified: &str,
    receiver_kind: &str,
    members: Vec<String>,
) -> Problem {
    Problem {
        range: access.member_range,
        message: format!(
            "Unknown property '{}' on {receiver_kind} '{qualified}'",
            access.member
        ),
        source: "shopware".into(),
        severity: Some(DiagnosticSeverity::WARNING),
        id: "admin.component.unknown-vue-member".into(),
        payload: Some(payload(vec![
            ("bindingName", text(&access.root)),
            ("memberName", text(&access.member)),
            (
                "suggestions",
                string_array(similar(&access.member, &members)),
            ),
        ])),
        ..Problem::default()
    }
}
