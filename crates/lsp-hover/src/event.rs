//! Symfony event and event-listener hover.

use std::fmt::Write as _;
use std::sync::Arc;

use swlsp_event::{Index, Occurrence, ReferenceRole};
use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_symfony::ServiceIndex;
use swlsp_syntax::SyntaxNodeExt;
use swlsp_uriutil::filepath;

use crate::{
    escape_markdown, hover_protocol_range, markdown_hover, php_context, rel_or, request_uri,
};

pub struct EventHoverProvider {
    root: String,
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
    services: Option<Arc<ServiceIndex>>,
}

impl EventHoverProvider {
    pub fn new(
        root: impl Into<String>,
        index: Option<Arc<Index>>,
        php: Option<PhpIndex>,
        services: Option<Arc<ServiceIndex>>,
    ) -> Self {
        Self {
            root: root.into(),
            index,
            php,
            services,
        }
    }

    fn listener_class(&self, listener: &Occurrence) -> String {
        if !listener.class.is_empty() || listener.service.is_empty() {
            return listener.class.clone();
        }
        let Some(services) = self.services.as_deref() else {
            return listener.class.clone();
        };
        match services.get_service_by_id(&listener.service) {
            Ok(Some(service)) => service.class,
            _ => String::new(),
        }
    }
}

impl HoverProvider for EventHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let Some(index) = self.index.as_deref() else {
            return Ok(None);
        };
        let (Some(node), Some(root)) = (request.node.as_ref(), request.root.as_ref()) else {
            return Ok(None);
        };
        let context = php_context(self.php.as_ref(), request);
        let Some(reference) =
            swlsp_event::reference_at(context.as_ref(), request_uri(request), root, node)
        else {
            return Ok(None);
        };
        let mut markdown = String::new();
        match reference.role {
            ReferenceRole::Event => {
                let Some(current) = index.event(&reference.name)? else {
                    return Ok(None);
                };
                let _ = writeln!(
                    markdown,
                    "**Symfony event** `{}`",
                    escape_markdown(&current.name)
                );
                if !current.event_type.is_empty() {
                    let _ = writeln!(
                        markdown,
                        "\nType: `{}`",
                        escape_markdown(&current.event_type)
                    );
                }
                let listeners = current.listeners();
                if !listeners.is_empty() {
                    let _ = writeln!(markdown, "\n{} listener(s):", listeners.len());
                    for (position, listener) in listeners.iter().enumerate() {
                        if position == 8 {
                            let _ =
                                writeln!(markdown, "- … and {} more", listeners.len() - position);
                            break;
                        }
                        let mut target = self.listener_class(listener);
                        if !listener.method.is_empty() {
                            target.push_str("::");
                            target.push_str(&listener.method);
                            target.push_str("()");
                        }
                        let _ = write!(markdown, "- `{}`", escape_markdown(&target));
                        if !listener.priority.is_empty() {
                            let _ = write!(
                                markdown,
                                " (priority `{}`)",
                                escape_markdown(&listener.priority)
                            );
                        }
                        let display = rel_or(&self.root, &listener.file);
                        let _ = writeln!(
                            markdown,
                            " — `{}`",
                            escape_markdown(&filepath::to_slash(&display))
                        );
                    }
                }
                let dispatch_count = current.dispatches().len();
                if dispatch_count != 0 {
                    let _ = write!(markdown, "\n{dispatch_count} indexed dispatch site(s)");
                }
            }
            ReferenceRole::ListenerMethod => {
                let path = swlsp_uriutil::path(request_uri(request)).unwrap_or_default();
                let Some(listener) = swlsp_event::resolve_listener(Some(index), &path, &reference)?
                else {
                    return Ok(None);
                };
                let class_name = self.listener_class(&listener);
                let _ = write!(
                    markdown,
                    "**Symfony event listener** `{}::{}()`",
                    escape_markdown(&class_name),
                    escape_markdown(&reference.name)
                );
                if let Some(php) = self.php.as_ref() {
                    let methods = php.find_methods(&class_name, &reference.name);
                    if let Some(method) = methods.first()
                        && !method.doc_summary().is_empty()
                    {
                        let _ = write!(markdown, "\n\n{}", method.doc_summary());
                    }
                }
                let events = index.events_for_listener(&class_name, &reference.name)?;
                if !events.is_empty() {
                    let names: Vec<String> = events
                        .iter()
                        .map(|current| format!("`{}`", current.name))
                        .collect();
                    let _ = write!(markdown, "\n\nListens to: {}", names.join(", "));
                }
            }
            _ => return Ok(None),
        }
        let Some(line_index) = request.line_index() else {
            return Ok(None);
        };
        Ok(Some(markdown_hover(
            markdown,
            Some(hover_protocol_range(
                reference.node.range_trimmed_trivia(),
                line_index,
            )),
        )))
    }
}
