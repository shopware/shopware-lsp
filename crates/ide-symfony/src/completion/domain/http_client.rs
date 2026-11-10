//! Symfony HttpClient option keys.

use std::collections::HashSet;

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_php::PhpIndex;

use super::{item, markdown, strings};
use crate::completion::common::{php_context, protocol_range, text_edit};

pub struct HttpClientCompletionProvider {
    php: Option<PhpIndex>,
}

impl HttpClientCompletionProvider {
    pub fn new(php: Option<PhpIndex>) -> Self {
        Self { php }
    }
}

impl CompletionProvider for HttpClientCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let (Some(php), Some(node), Some(line_index)) = (
            self.php.as_ref(),
            request.node.as_ref(),
            request.line_index(),
        ) else {
            return Vec::new();
        };
        let Some(reference) = swlsp_httpclient::reference_at(node) else {
            return Vec::new();
        };
        let context = php_context(php, request);
        if !swlsp_httpclient::validate(php, context.as_ref(), &reference) {
            return Vec::new();
        }
        let range = protocol_range(reference.range, line_index);
        let used = swlsp_httpclient::used_option_names(&reference);
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for option in swlsp_httpclient::options(php) {
            if !seen.insert(option.name.clone()) || used.contains(&option.name) {
                continue;
            }
            let mut detail = String::from("Symfony HttpClient option");
            if !option.ty.is_unknown() {
                detail.push_str(" · ");
                detail.push_str(&option.ty.to_string());
            }
            let mut completion = item(option.name.clone(), CompletionItemKind::PROPERTY, detail);
            completion.text_edit = Some(text_edit(range, option.name));
            completion.documentation = Some(markdown(format!(
                "Default from `{}::OPTIONS_DEFAULTS`:\n\n```php\n{}\n```",
                swlsp_httpclient::CLIENT_INTERFACE,
                option.default
            )));
            result.push(completion);
        }
        result
    }

    fn trigger_characters(&self) -> Vec<String> {
        strings(&["'", "\""])
    }
}
