//! Serializer usage lenses on PHP classes.

use std::sync::Arc;

use serde_json::Value;
use swlsp_lsp::{CodeLensProvider, CodeLensRequest, Context, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_serializer::{Index, Usage};
use swlsp_syntax::LineIndex;

use swlsp_ide::codelens::{OPEN_REFERENCES_COMMAND, related_protocol_range};

pub struct SerializerCodeLensProvider {
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
}

impl SerializerCodeLensProvider {
    pub fn new(index: Option<Arc<Index>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl CodeLensProvider for SerializerCodeLensProvider {
    fn get_code_lenses(
        &self,
        _ctx: &Context,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let (Some(index), Some(php)) = (&self.index, &self.php) else {
            return Ok(Vec::new());
        };
        let uri = &request.params.text_document.uri;
        if !uri.to_ascii_lowercase().ends_with(".php") {
            return Ok(Vec::new());
        }
        let path = swlsp_uriutil::path(uri)?;
        let lines = request.document.line_index();
        let mut result = Vec::new();
        for class in php.class_symbols_in(&path) {
            let usages = index.usages(&class.fully_qualified)?;
            if usages.is_empty() {
                continue;
            }
            // Serializer targets keep the zero-based line, unlike the other
            // lenses.
            let targets: Vec<Value> = usages
                .iter()
                .map(|usage| {
                    Value::String(swlsp_uriutil::file_uri_with_fragment(
                        &usage.file,
                        &serializer_usage_line(usage).to_string(),
                    ))
                })
                .collect();
            result.push(protocol::CodeLens {
                range: related_protocol_range(class.selection_range, lines),
                command: Some(protocol::Command {
                    title: format!("{} serializer use(s)", usages.len()),
                    command: OPEN_REFERENCES_COMMAND.to_owned(),
                    arguments: vec![Value::Array(targets)],
                }),
                data: None,
            });
        }
        Ok(result)
    }

    fn resolve_code_lens(
        &self,
        _ctx: &Context,
        lens: &protocol::CodeLens,
    ) -> ProviderResult<Option<protocol::CodeLens>> {
        Ok(Some(lens.clone()))
    }
}

fn serializer_usage_line(usage: &Usage) -> u32 {
    let Ok(source) = std::fs::read(&usage.file) else {
        return 0;
    };
    LineIndex::new(String::from_utf8_lossy(&source).into_owned())
        .position_utf16(usage.range.start().into())
        .0
}
