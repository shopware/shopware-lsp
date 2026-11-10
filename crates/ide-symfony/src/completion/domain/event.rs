//! Event names and listener methods.

use std::sync::Arc;

use swlsp_event::{Index, Occurrence, ReferenceRole};
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_php::PhpIndex;
use swlsp_symfony::ServiceIndex;

use super::{item, strings};
use crate::completion::common::php_context;

pub struct EventCompletionProvider {
    index: Arc<Index>,
    php: Option<PhpIndex>,
    services: Option<Arc<ServiceIndex>>,
}

impl EventCompletionProvider {
    pub fn new(
        index: Arc<Index>,
        php: Option<PhpIndex>,
        services: Option<Arc<ServiceIndex>>,
    ) -> Self {
        Self {
            index,
            php,
            services,
        }
    }

    fn listener_class(&self, listener: &Occurrence) -> String {
        if !listener.class.is_empty() || listener.service.is_empty() {
            return listener.class.clone();
        }
        let Some(services) = self.services.as_ref() else {
            return listener.class.clone();
        };
        match services.get_service_by_id(&listener.service) {
            Ok(Some(service)) => service.class,
            _ => String::new(),
        }
    }
}

impl CompletionProvider for EventCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let (Some(node), Some(root)) = (request.node.as_ref(), request.root.as_ref()) else {
            return Vec::new();
        };
        let context = self.php.as_ref().and_then(|php| php_context(php, request));
        let uri = &request.params.text_document.uri;
        let Some(reference) = swlsp_event::reference_at(context.as_ref(), uri, root, node) else {
            return Vec::new();
        };
        if reference.role == ReferenceRole::Event {
            let Ok(events) = self.index.events() else {
                return Vec::new();
            };
            return events
                .into_iter()
                .map(|current| {
                    let detail = if current.event_type.is_empty() {
                        format!("{} listener(s)", current.listeners().len())
                    } else {
                        current.event_type.clone()
                    };
                    item(current.name, CompletionItemKind::EVENT, detail)
                })
                .collect();
        }
        let Some(php) = self.php.as_ref() else {
            return Vec::new();
        };
        if reference.role != ReferenceRole::ListenerMethod {
            return Vec::new();
        }
        let path = swlsp_uriutil::path(uri).unwrap_or_default();
        let Ok(Some(listener)) =
            swlsp_event::resolve_listener(Some(&self.index), &path, &reference)
        else {
            return Vec::new();
        };
        let class_name = self.listener_class(&listener);
        swlsp_event::public_methods(Some(php), &class_name)
            .into_iter()
            .map(|method| {
                let detail = if method.doc_summary().is_empty() {
                    class_name.clone()
                } else {
                    method.doc_summary().to_string()
                };
                item(method.name.to_string(), CompletionItemKind::METHOD, detail)
            })
            .collect()
    }

    fn trigger_characters(&self) -> Vec<String> {
        strings(&["'", "\""])
    }
}
