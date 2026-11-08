//! Go `live_component_event_diagnostics.go`.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_php_types::{equal_fold, go_to_lower};
use swlsp_twigcomponent::{Index, LiveListener};

use super::component::suggestions_payload;
use super::{contains_fold, live_argument_attribute_segment, lower_ext, uri_path};
use crate::ProblemAnalyzer;

const MISSING_LIVE_EVENT_CODE: &str = "twig.component.live_event.missing";
const MISSING_LIVE_EVENT_ARGUMENT_CODE: &str = "twig.component.live_event_argument.missing";

/// Go `LiveComponentEventAnalyzer`.
pub struct LiveComponentEventAnalyzer {
    index: Option<Arc<Index>>,
}

impl LiveComponentEventAnalyzer {
    pub fn new(index: Option<Arc<Index>>) -> Self {
        Self { index }
    }
}

impl ProblemAnalyzer for LiveComponentEventAnalyzer {
    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(index), Some(root)) = (&self.index, document.root()) else {
            return Ok(Vec::new());
        };
        let path = uri_path(&document.uri);
        let extension = lower_ext(&path);
        if extension != ".php" && extension != ".twig" {
            return Ok(Vec::new());
        }
        let php = extension == ".php";
        let mut listeners = index.live_listeners()?;
        if php {
            listeners = merge_live_event_listeners(
                listeners,
                swlsp_twigcomponent::live_listeners_in_php(&path, &root),
            );
        }
        let (references, arguments) = if php {
            (
                swlsp_twigcomponent::live_event_references_in_php(&path, &root),
                swlsp_twigcomponent::live_event_argument_references_in_php(&path, &root),
            )
        } else {
            (
                swlsp_twigcomponent::live_event_references_in_twig(&path, &root),
                swlsp_twigcomponent::live_event_argument_references_in_twig(&path, &root),
            )
        };
        let source = extension.trim_start_matches('.').to_owned();
        let event_names = unique_fold(listeners.iter().map(|listener| listener.name.as_str()));
        let mut result = Vec::new();
        for reference in references {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            if contains_fold(&event_names, &reference.name) {
                continue;
            }
            result.push(Problem {
                id: MISSING_LIVE_EVENT_CODE.into(),
                range: reference.range,
                message: format!("Live event '{}' has no #[LiveListener]", reference.name),
                severity: Some(protocol::DiagnosticSeverity::WARNING),
                source: source.clone(),
                payload: suggestions_payload(swlsp_suggestion::similar(
                    &reference.name,
                    &event_names,
                )),
                ..Problem::default()
            });
        }
        for reference in arguments {
            let parameter_names = unique_fold(
                listeners
                    .iter()
                    .filter(|listener| equal_fold(&listener.name, &reference.event))
                    .flat_map(|listener| listener.parameters.iter())
                    .filter(|parameter| parameter.live_arg)
                    .map(|parameter| parameter.name.as_str()),
            );
            if parameter_names.is_empty() || contains_fold(&parameter_names, &reference.name) {
                continue;
            }
            let mut suggestions = swlsp_suggestion::similar(&reference.name, &parameter_names);
            if !php {
                for suggestion in &mut suggestions {
                    *suggestion = live_argument_attribute_segment(suggestion);
                }
            }
            result.push(Problem {
                id: MISSING_LIVE_EVENT_ARGUMENT_CODE.into(),
                range: reference.range,
                message: format!(
                    "Live event '{}' has no argument named '{}'",
                    reference.event, reference.name
                ),
                severity: Some(protocol::DiagnosticSeverity::WARNING),
                source: source.clone(),
                payload: suggestions_payload(suggestions),
                ..Problem::default()
            });
        }
        Ok(result)
    }
}

fn merge_live_event_listeners(
    indexed: Vec<LiveListener>,
    current: Vec<LiveListener>,
) -> Vec<LiveListener> {
    let mut result = indexed;
    for listener in current {
        match result
            .iter_mut()
            .find(|existing| existing.file == listener.file && existing.range == listener.range)
        {
            Some(existing) => *existing = listener,
            None => result.push(listener),
        }
    }
    result
}

/// Go `uniqueLiveEventListenerNames` / `uniqueLiveEventArgumentNames`.
fn unique_fold<'a>(values: impl Iterator<Item = &'a str>) -> Vec<String> {
    let mut seen = HashSet::new();
    let mut result = Vec::new();
    for value in values {
        if value.is_empty() || !seen.insert(go_to_lower(value)) {
            continue;
        }
        result.push(value.to_owned());
    }
    result
}
