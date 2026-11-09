//! Serializer `deserialize()` target references.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{Context, ProviderResult, ReferenceRequest, ReferencesProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::{PhpIndex, symbol_at};
use swlsp_serializer::{Index, usage_at, usages_in_document};
use swlsp_textutil::eq_ignore_case;

use crate::{FileLines, document_location, php_context, request_offset, symbol_range};

pub struct SerializerReferenceProvider {
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
}

impl SerializerReferenceProvider {
    pub fn new(index: Option<Arc<Index>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl ReferencesProvider for SerializerReferenceProvider {
    fn get_references(
        &self,
        _ctx: &Context,
        request: &ReferenceRequest,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let (Some(index), Some(_), Some(line_index), Some(offset)) = (
            &self.index,
            &request.node,
            request.line_index(),
            request_offset(request),
        ) else {
            return Ok(Vec::new());
        };
        let uri = &request.params.text_document.uri;
        if !uri.to_lowercase().ends_with(".php") {
            return Ok(Vec::new());
        }
        let root = request.root.as_ref();
        let mut class_name = String::new();
        if let Some(usage) = root.and_then(|root| usage_at(root, offset)) {
            class_name = usage.class;
        } else if let Some(context) = php_context(self.php.as_ref(), request)
            && let Some(symbol) = symbol_at(&context.document, &context.snapshot, offset)
            && symbol.is_class_like()
        {
            class_name = symbol.fully_qualified.to_string();
        }
        if class_name.is_empty() {
            return Ok(Vec::new());
        }
        let current_path = swlsp_uriutil::path(uri).unwrap_or_default();
        let mut filtered: Vec<_> = index
            .usages(&class_name)?
            .into_iter()
            .filter(|usage| usage.file != current_path)
            .collect();
        if let Some(root) = root {
            filtered.extend(
                usages_in_document(&current_path, root)
                    .into_iter()
                    .filter(|usage| eq_ignore_case(&usage.class, &class_name)),
            );
        }

        let mut files = FileLines::default();
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        if request.params.context.include_declaration
            && let Some(php) = &self.php
            && let Some(symbol) = php.find_class(&class_name)
        {
            let location = files
                .location(&symbol.path, symbol_range(&symbol))
                .unwrap_or_else(|| protocol::Location {
                    uri: swlsp_uriutil::file_uri(&symbol.path),
                    range: protocol::Range::default(),
                });
            seen.insert(location.clone());
            result.push(location);
        }
        for usage in &filtered {
            let location = if usage.file == current_path {
                document_location(request, line_index, usage.range)
            } else {
                let Some(location) = files.location(&usage.file, usage.range) else {
                    continue;
                };
                location
            };
            if seen.insert(location.clone()) {
                result.push(location);
            }
        }
        Ok(result)
    }
}
