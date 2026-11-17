//! `this.$super('name')` calls no overridden or extended component answers.

use swlsp_admin::{ComponentRegistrationKind, JavaScriptDocumentAnalysis, VueComponent};
use swlsp_lsp::{Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_js::query as jsquery;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

use super::{AdminAnalyzer, payload, string_array, text};
use crate::diagnostics::common::{admin_nearby_suggestions, javascript_string_content_range};

const OVERRIDE_CALLS: &[&str] = &["Component.override", "Shopware.Component.override"];
const EXTEND_CALLS: &[&str] = &["Component.extend", "Shopware.Component.extend"];

impl AdminAnalyzer {
    /// Reports `this.$super('name')` when the component the file overrides
    /// or extends has no method or computed property `name`: the call throws
    /// at runtime.
    pub(super) fn super_call_diagnostics(
        &self,
        document: &TextDocument,
        analysis: &JavaScriptDocumentAnalysis,
    ) -> Vec<Problem> {
        let Some(indexer) = self.indexer() else {
            return Vec::new();
        };
        let super_calls: Vec<&SyntaxNode> = analysis
            .iterate_calls(&["this.$super"])
            .filter(|call| jsquery::string_argument(call, 0).is_some())
            .collect();
        if super_calls.is_empty() {
            return Vec::new();
        }
        let Ok(path) = swlsp_uriutil::path(&document.uri) else {
            return Vec::new();
        };
        let Ok(registrations) = indexer.get_derived_registrations_by_definition_path(&path) else {
            return Vec::new();
        };
        if registrations.is_empty() {
            return Vec::new();
        }
        let inline = inline_registrations(analysis);
        let mut diagnostics = Vec::new();
        for call in super_calls {
            let Some(registration) = owning_registration(call, &inline, &registrations) else {
                continue;
            };
            let Ok(Some(members)) = indexer.get_super_members(registration) else {
                continue;
            };
            if !members.complete {
                continue;
            }
            let Some(name_node) = jsquery::string_argument(call, 0) else {
                continue;
            };
            let written = jsquery::string_value(&name_node);
            // A computed getter or setter is addressed as `name.get`.
            let name = written
                .strip_suffix(".get")
                .or_else(|| written.strip_suffix(".set"))
                .unwrap_or(&written);
            if name.is_empty() || name == "$super" || members.names.contains(name) {
                continue;
            }
            let content = javascript_string_content_range(&name_node, document.source());
            let length = TextSize::from(u32::try_from(name.len()).unwrap_or(0));
            let range = if content.len() >= length {
                TextRange::new(content.start(), content.start() + length)
            } else {
                name_node.range_trimmed_trivia()
            };
            let known: Vec<String> = members.names.iter().cloned().collect();
            diagnostics.push(Problem {
                range,
                message: format!(
                    "Component '{}' has no method or computed property '{name}' for $super to call",
                    members.component
                ),
                source: "shopware".into(),
                severity: Some(DiagnosticSeverity::WARNING),
                id: "admin.component.unknown-super-member".into(),
                payload: Some(payload(vec![
                    ("memberName", text(name)),
                    ("componentName", text(&members.component)),
                    (
                        "suggestions",
                        string_array(admin_nearby_suggestions(name, &known)),
                    ),
                ])),
                ..Problem::default()
            });
        }
        diagnostics
    }
}

/// An inline `Component.override('name', …)` or `Component.extend('name',
/// 'parent', …)` call of the document.
struct InlineRegistration {
    range: TextRange,
    name: String,
    kind: ComponentRegistrationKind,
}

fn inline_registrations(analysis: &JavaScriptDocumentAnalysis) -> Vec<InlineRegistration> {
    let collect = |names: &[&str], kind: ComponentRegistrationKind| {
        analysis
            .calls(names)
            .into_iter()
            .filter_map(move |call| {
                let name = jsquery::string_value(&jsquery::string_argument(&call, 0)?);
                Some(InlineRegistration {
                    range: call.range_trimmed_trivia(),
                    name,
                    kind,
                })
            })
            .collect::<Vec<_>>()
    };
    let mut result = collect(OVERRIDE_CALLS, ComponentRegistrationKind::Override);
    result.extend(collect(EXTEND_CALLS, ComponentRegistrationKind::Extend));
    result
}

/// The registration whose configuration holds `call`: the innermost inline
/// registration around it, otherwise the only one the file owns.
fn owning_registration<'a>(
    call: &SyntaxNode,
    inline: &[InlineRegistration],
    registrations: &'a [VueComponent],
) -> Option<&'a VueComponent> {
    let range = call.range_trimmed_trivia();
    let enclosing = inline
        .iter()
        .filter(|registration| registration.range.contains_range(range))
        .min_by_key(|registration| registration.range.len());
    match enclosing {
        Some(enclosing) => {
            let mut matching = registrations.iter().filter(|registration| {
                registration.name == enclosing.name
                    && (registration.kind == ComponentRegistrationKind::Override)
                        == (enclosing.kind == ComponentRegistrationKind::Override)
            });
            let found = matching.next()?;
            // Two registrations of one name in a file cannot be told apart.
            matching.next().is_none().then_some(found)
        }
        None => match registrations {
            [only] => Some(only),
            _ => None,
        },
    }
}
