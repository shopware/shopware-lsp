//! Dispatched events without listeners and missing listener methods.

use std::collections::HashSet;
use std::sync::Arc;

use serde_json::{Value, json};
use swlsp_event::{Event, Index, Occurrence, Reference, ReferenceOrigin, ReferenceRole};
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_xml::syntax::XML_ATTRIBUTE;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax::YAML_SCALAR;
use swlsp_php::{PhpContext, PhpIndex};
use swlsp_symfony::ServiceIndex;
use swlsp_syntax::{SyntaxNodeExt, TextRange};
use swlsp_textutil::eq_ignore_case;
use swlsp_textutil::lowercase;

use super::{lower_ext, problem, similar_value};
use crate::diagnostics::ProblemAnalyzer;
use crate::diagnostics::common::value_node_text_range;

const MISSING_EVENT_CODE: &str = "symfony.event.missing";
const MISSING_LISTENER_METHOD_CODE: &str = "symfony.event.listener_method.missing";

pub struct EventAnalyzer {
    index: Option<Arc<Index>>,
    php_index: Option<PhpIndex>,
    services: Option<Arc<ServiceIndex>>,
}

impl EventAnalyzer {
    pub fn new(
        index: Option<Arc<Index>>,
        php_index: Option<PhpIndex>,
        services: Option<Arc<ServiceIndex>>,
    ) -> Self {
        Self {
            index,
            php_index,
            services,
        }
    }
}

impl ProblemAnalyzer for EventAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(
            crate::diagnostics::inputs(&self.index)
                + crate::diagnostics::inputs(&self.php_index)
                + crate::diagnostics::inputs(&self.services),
        )
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(index), Some(php)) = (&self.index, &self.php_index) else {
            return Ok(Vec::new());
        };
        if document.tree().is_none() {
            return Ok(Vec::new());
        }
        let path = swlsp_uriutil::path(&document.uri).unwrap_or_default();
        let context = if lower_ext(&document.uri) == ".php" {
            swlsp_lsp_phpanalysis::context_for_document(php, document)?
        } else {
            None
        };
        let references = event_document_references(context.as_ref(), document);
        if references.is_empty() {
            return Ok(Vec::new());
        }
        let events = index.events_view()?;
        let listener_event_names: Vec<String> = events
            .iter()
            .filter(|current| !current.listeners().is_empty())
            .map(|current| current.name.clone())
            .collect();

        let mut result = Vec::new();
        for reference in &references {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            match reference.role {
                ReferenceRole::Event => {
                    if reference.origin != ReferenceOrigin::Dispatch
                        || reference.name.is_empty()
                        || listener_event_names.is_empty()
                    {
                        continue;
                    }
                    if index
                        .event(&reference.name)?
                        .is_some_and(|current| !current.listeners().is_empty())
                    {
                        continue;
                    }
                    result.push(event_diagnostic(
                        reference,
                        MISSING_EVENT_CODE,
                        format!("Symfony event '{}' has no indexed listener", reference.name),
                        similar_value(&reference.name, &listener_event_names),
                    ));
                }
                ReferenceRole::ListenerMethod => {
                    if reference.name.is_empty() {
                        continue;
                    }
                    let Some(listener) =
                        swlsp_event::resolve_listener(Some(index), &path, reference)?
                    else {
                        continue;
                    };
                    let class_name = self.listener_class(&listener);
                    if class_name.is_empty()
                        || !php.find_methods(&class_name, &reference.name).is_empty()
                    {
                        continue;
                    }
                    let names: Vec<String> = swlsp_event::public_methods(Some(php), &class_name)
                        .into_iter()
                        .map(|method| method.name.as_str().to_owned())
                        .collect();
                    let mut diagnostic = event_diagnostic(
                        reference,
                        MISSING_LISTENER_METHOD_CODE,
                        format!(
                            "Event listener method '{class_name}::{}' not found",
                            reference.name
                        ),
                        similar_value(&reference.name, &names),
                    );
                    if let Some(Value::Object(data)) = &mut diagnostic.payload {
                        data.insert("className".to_owned(), Value::String(class_name));
                        data.insert(
                            "methodName".to_owned(),
                            Value::String(reference.name.clone()),
                        );
                        data.insert(
                            "eventTypes".to_owned(),
                            Value::from(event_listener_types(&events, &path, reference, &listener)),
                        );
                    }
                    result.push(diagnostic);
                }
                ReferenceRole::None => {}
            }
        }
        Ok(result)
    }
}

fn event_listener_types(
    events: &[Event],
    path: &str,
    reference: &Reference,
    resolved: &Occurrence,
) -> Vec<String> {
    let offset = u32::from(reference.node.range_trimmed_trivia().start());
    let mut seen = HashSet::new();
    let mut result = Vec::new();
    for current in events {
        if current.event_type.is_empty() {
            continue;
        }
        let matches = current.listeners().iter().any(|listener| {
            (listener.file == path
                && (range_contains(listener.range, offset)
                    || range_contains(listener.method_range, offset)))
                || (!resolved.class.is_empty()
                    && eq_ignore_case(&listener.class, &resolved.class)
                    && eq_ignore_case(&listener.method, &reference.name))
                || (!resolved.service.is_empty()
                    && eq_ignore_case(&listener.service, &resolved.service)
                    && eq_ignore_case(&listener.method, &reference.name))
        });
        if !matches || !seen.insert(lowercase(&current.event_type)) {
            continue;
        }
        result.push(current.event_type.clone());
    }
    result.sort();
    result
}

fn range_contains(range: TextRange, offset: u32) -> bool {
    !range.is_empty() && offset >= u32::from(range.start()) && offset <= u32::from(range.end())
}

fn event_document_references(
    context: Option<&PhpContext>,
    document: &TextDocument,
) -> Vec<Reference> {
    let Some(root) = document.root() else {
        return Vec::new();
    };
    let nodes = match lower_ext(&document.uri).as_str() {
        ".php" => phpquery::nodes(&root, &[PHP_STRING]),
        ".yaml" | ".yml" => yamlquery::nodes(&root, &[YAML_SCALAR]),
        ".xml" => xmlquery::nodes(&root, &[XML_ATTRIBUTE]),
        _ => return Vec::new(),
    };
    nodes
        .iter()
        .filter_map(|node| swlsp_event::reference_at(context, &document.uri, &root, node))
        .collect()
}

fn event_diagnostic(
    reference: &Reference,
    code: &str,
    message: String,
    suggestions: Value,
) -> Problem {
    let mut problem = problem(
        code,
        value_node_text_range(Some(&reference.node), &reference.name),
        message,
        DiagnosticSeverity::WARNING,
    );
    problem.payload = Some(json!({ "suggestions": suggestions }));
    problem
}

impl EventAnalyzer {
    fn listener_class(&self, listener: &Occurrence) -> String {
        if !listener.class.is_empty() || listener.service.is_empty() {
            return listener.class.clone();
        }
        let Some(services) = &self.services else {
            return listener.class.clone();
        };
        match services.get_service_by_id(&listener.service) {
            Ok(Some(service)) => service.class,
            _ => String::new(),
        }
    }
}
