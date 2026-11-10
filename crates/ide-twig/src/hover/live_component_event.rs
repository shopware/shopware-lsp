//! Symfony UX Live event and Live event argument hover.

use std::fmt::Write as _;
use std::sync::Arc;

use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{LineIndex, TextRange};
use swlsp_textutil::eq_ignore_case;
use swlsp_twigcomponent::{Index, LiveEventArgumentReference, LiveListener};

use crate::hover::{
    escape_markdown, hover_protocol_range, lower_ext, markdown_hover, request_offset, request_uri,
};

type HoverResult = ProviderResult<Option<protocol::Hover>>;

pub struct LiveComponentEventHoverProvider {
    index: Option<Arc<Index>>,
}

impl LiveComponentEventHoverProvider {
    pub fn new(index: Option<Arc<Index>>) -> Self {
        Self { index }
    }
}

impl HoverProvider for LiveComponentEventHoverProvider {
    fn get_hover(&self, _ctx: &Context, request: &HoverRequest) -> HoverResult {
        let Some(index) = self.index.as_deref() else {
            return Ok(None);
        };
        let (Some(root), Some(line_index), Some(offset)) = (
            request.root.as_ref(),
            request.line_index(),
            request_offset(request),
        ) else {
            return Ok(None);
        };
        let path = swlsp_uriutil::path(request_uri(request)).unwrap_or_default();
        match lower_ext(&path).as_str() {
            ".php" => {
                if let Some(argument) =
                    swlsp_twigcomponent::live_event_argument_reference_at_php(&path, root, offset)
                {
                    return argument_hover(index, &argument, line_index);
                }
                if let Some(event) =
                    swlsp_twigcomponent::live_event_reference_at_php(&path, root, offset)
                {
                    return event_hover(index, &event.name, event.range, line_index);
                }
            }
            ".twig" => {
                if let Some(argument) =
                    swlsp_twigcomponent::live_event_argument_reference_at_twig(&path, root, offset)
                {
                    return argument_hover(index, &argument, line_index);
                }
                if let Some(event) =
                    swlsp_twigcomponent::live_event_reference_at_twig(&path, root, offset)
                {
                    return event_hover(index, &event.name, event.range, line_index);
                }
            }
            _ => {}
        }
        Ok(None)
    }
}

fn event_hover(
    index: &Index,
    event: &str,
    range: TextRange,
    line_index: &LineIndex,
) -> HoverResult {
    let listeners = index.live_listeners()?;
    let matching: Vec<&LiveListener> = listeners
        .iter()
        .filter(|listener| eq_ignore_case(&listener.name, event))
        .collect();
    if matching.is_empty() {
        return Ok(None);
    }
    let references = index.live_event_references(event).unwrap_or_default();
    let mut value = format!("**Symfony UX Live event** `{}`\n\n", escape_markdown(event));
    for (position, listener) in matching.iter().enumerate() {
        if position != 0 {
            value.push('\n');
        }
        let _ = writeln!(
            value,
            "```php\n{}\n```",
            live_event_listener_signature(listener)
        );
    }
    let _ = write!(
        value,
        "\n{} listener(s) • {} indexed emission(s)",
        matching.len(),
        references.len()
    );
    Ok(Some(markdown_hover(
        value,
        Some(hover_protocol_range(range, line_index)),
    )))
}

fn argument_hover(
    index: &Index,
    reference: &LiveEventArgumentReference,
    line_index: &LineIndex,
) -> HoverResult {
    for listener in index.live_listeners()? {
        if !eq_ignore_case(&listener.name, &reference.event) {
            continue;
        }
        for parameter in &listener.parameters {
            if !parameter.live_arg || !eq_ignore_case(&parameter.name, &reference.name) {
                continue;
            }
            let mut value = format!(
                "**Live event argument** `{}`\n\nEvent: `{}`\n\nListener: `{}::{}()`",
                escape_markdown(&parameter.name),
                escape_markdown(&reference.event),
                escape_markdown(&listener.class),
                escape_markdown(&listener.method)
            );
            if !parameter.ty.is_empty() {
                let _ = write!(value, "\n\nPHP type: `{}`", escape_markdown(&parameter.ty));
            }
            if parameter.name != parameter.php_name {
                let _ = write!(
                    value,
                    "\n\nMapped to PHP parameter `${}` via `#[LiveArg]`",
                    escape_markdown(&parameter.php_name)
                );
            }
            return Ok(Some(markdown_hover(
                value,
                Some(hover_protocol_range(reference.range, line_index)),
            )));
        }
    }
    Ok(None)
}

fn live_event_listener_signature(listener: &LiveListener) -> String {
    let parameters: Vec<String> = listener
        .parameters
        .iter()
        .map(|parameter| {
            let mut name = format!("${}", parameter.php_name);
            if !parameter.ty.is_empty() {
                name = format!("{} {}", parameter.ty, name);
            }
            if parameter.optional {
                name.push_str(" = …");
            }
            name
        })
        .collect();
    format!(
        "{}::{}({})",
        listener.class,
        listener.method,
        parameters.join(", ")
    )
}
