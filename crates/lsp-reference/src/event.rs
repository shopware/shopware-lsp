//! Symfony event references (port of `event_reference.go`).

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_event::{Event, Index, Occurrence, OccurrenceKind, ReferenceRole, reference_at};
use swlsp_lsp::{Context, ProviderResult, ReferenceRequest, ReferencesProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_php::{NameResolver, PhpIndex};
use swlsp_php_types::equal_fold;
use swlsp_syntax::SyntaxNodeExt;

use crate::{FileLines, lower_ext, php_context};

/// `php` supplies the request's semantic PHP context, which Go receives
/// through the server's context enricher.
pub struct EventReferenceProvider {
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
}

impl EventReferenceProvider {
    pub fn new(index: Option<Arc<Index>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl ReferencesProvider for EventReferenceProvider {
    fn get_references(
        &self,
        _ctx: &Context,
        request: &ReferenceRequest,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let (Some(index), Some(node)) = (&self.index, &request.node) else {
            return Ok(Vec::new());
        };
        let uri = &request.params.text_document.uri;
        let context = php_context(self.php.as_ref(), request);
        if let Some(root) = &request.root
            && let Some(reference) = reference_at(context.as_ref(), uri, root, node)
        {
            match reference.role {
                ReferenceRole::Event => {
                    let Some(current) = index.event(&reference.name)? else {
                        return Ok(Vec::new());
                    };
                    return Ok(occurrence_locations(current.occurrences.iter(), false));
                }
                ReferenceRole::ListenerMethod => {
                    let mut class_name = reference.class.clone();
                    if class_name.is_empty() {
                        let path = swlsp_uriutil::path(uri).unwrap_or_default();
                        let Some(listener) = index
                            .listener_at(&path, reference.node.range_trimmed_trivia().start())?
                        else {
                            return Ok(Vec::new());
                        };
                        class_name = listener.class;
                    }
                    let events = index.events_for_listener(&class_name, &reference.name)?;
                    return Ok(listener_event_locations(
                        &events,
                        &class_name,
                        &reference.name,
                    ));
                }
                ReferenceRole::None => {}
            }
        }

        if lower_ext(uri) != ".php" {
            return Ok(Vec::new());
        }
        let Some(method) = phpquery::method_at(node) else {
            return Ok(Vec::new());
        };
        let method_name = phpquery::method_name(&method);
        let Some(class) = phpquery::class_at(&method) else {
            return Ok(Vec::new());
        };
        let Some(root) = &request.root else {
            return Ok(Vec::new());
        };
        if method_name.is_empty() {
            return Ok(Vec::new());
        }
        let resolved = NameResolver::new(root).resolve(&phpquery::class_name(&class));
        let class_name = resolved.strip_prefix('\\').unwrap_or(&resolved);
        let events = index.events_for_listener(class_name, &method_name)?;
        Ok(listener_event_locations(&events, class_name, &method_name))
    }
}

fn listener_event_locations(
    events: &[Event],
    class_name: &str,
    method_name: &str,
) -> Vec<protocol::Location> {
    let occurrences = events
        .iter()
        .flat_map(|event| &event.occurrences)
        .filter(|occurrence| {
            occurrence.kind == OccurrenceKind::Dispatch
                || (equal_fold(&occurrence.class, class_name)
                    && equal_fold(&occurrence.method, method_name))
        });
    occurrence_locations(occurrences, true)
}

fn occurrence_locations<'a>(
    occurrences: impl Iterator<Item = &'a Occurrence>,
    prefer_method: bool,
) -> Vec<protocol::Location> {
    let mut files = FileLines::default();
    let mut seen = HashSet::new();
    let mut result = Vec::new();
    for occurrence in occurrences {
        let range = if prefer_method && !occurrence.method_range.is_empty() {
            occurrence.method_range
        } else {
            occurrence.range
        };
        let Some(location) = files.location(&occurrence.file, range) else {
            continue;
        };
        if seen.insert(location.clone()) {
            result.push(location);
        }
    }
    result
}
