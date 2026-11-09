//! Doctrine DBAL type classes, tables, columns and aliases.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_doctrine::{
    DbalCompletionKind, Index, TypeRegistrationReference, TypeRegistrationReferenceRole,
};
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_php::PhpIndex;
use swlsp_syntax::{TextRange, TextSize};
use swlsp_textutil::eq_ignore_case;

use super::{item, lower_ext, strings};
use crate::common::php_context;
use swlsp_textutil::lowercase;

const DBAL_TYPE: &str = "Doctrine\\DBAL\\Types\\Type";

pub struct DoctrineCompletionProvider {
    index: Arc<Index>,
    php: Option<PhpIndex>,
}

impl DoctrineCompletionProvider {
    pub fn new(index: Arc<Index>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }

    fn dbal_type_class_completions(
        &self,
        reference: &TypeRegistrationReference,
        extension: &str,
        source: &str,
    ) -> Vec<protocol::CompletionItem> {
        let Some(php) = self.php.as_ref() else {
            return Vec::new();
        };
        let snapshot = php.semantic_snapshot();
        let classes: Vec<String> = php
            .class_symbols_view()
            .iter()
            .filter(|symbol| {
                !eq_ignore_case(&symbol.fully_qualified, DBAL_TYPE)
                    && snapshot.is_subtype_of(&symbol.fully_qualified, DBAL_TYPE)
            })
            .map(|symbol| symbol.fully_qualified.to_string())
            .collect();
        let mut items = type_class_completion_items(
            &classes,
            reference.range,
            extension,
            source,
            "Doctrine DBAL type class",
            reference.class_constant,
        );
        if !reference.object_creation {
            return items;
        }
        for item in &mut items {
            let class_name = if item.filter_text.is_empty() {
                item.label.clone()
            } else {
                item.filter_text.clone()
            };
            let short = match class_name.rfind('\\') {
                Some(separator) => class_name[separator + 1..].to_string(),
                None => class_name.clone(),
            };
            let prefix = if reference.object_creation_started {
                ""
            } else {
                "new "
            };
            item.label = short;
            item.insert_text = format!("{prefix}\\{class_name}()");
            item.filter_text = class_name;
        }
        items
    }
}

fn type_class_completion_items(
    class_names: &[String],
    reference: TextRange,
    extension: &str,
    source: &str,
    detail: &str,
    force_class_constant: bool,
) -> Vec<protocol::CompletionItem> {
    let class_constant = force_class_constant
        || (extension == ".php" && class_constant_at(source, reference.end().into()));
    let mut result = Vec::with_capacity(class_names.len());
    let mut seen = HashSet::with_capacity(class_names.len());
    for class_name in class_names {
        let class_name = class_name.strip_prefix('\\').unwrap_or(class_name);
        if class_name.is_empty() || !seen.insert(lowercase(class_name)) {
            continue;
        }
        let mut completion = item(class_name, CompletionItemKind::CLASS, detail);
        if class_constant {
            let short = class_name.rsplit('\\').next().unwrap_or(class_name);
            completion.label = short.to_string();
            completion.filter_text = class_name.to_string();
            completion.insert_text = format!("\\{class_name}::class");
        }
        result.push(completion);
    }
    result
}

fn class_constant_at(source: &str, end: u32) -> bool {
    let end = end as usize;
    if end > source.len() {
        return false;
    }
    let suffix = &source.as_bytes()[end..];
    let suffix = &suffix[..suffix.len().min("::class".len())];
    suffix.eq_ignore_ascii_case(b"::class")
}

fn dbal_completion_items(
    completions: Vec<swlsp_doctrine::DbalCompletion>,
) -> Vec<protocol::CompletionItem> {
    completions
        .into_iter()
        .map(|completion| {
            let kind = match completion.kind {
                DbalCompletionKind::Table => CompletionItemKind::STRUCT,
                DbalCompletionKind::Alias => CompletionItemKind::VARIABLE,
                DbalCompletionKind::Column => CompletionItemKind::FIELD,
            };
            item(completion.label, kind, completion.detail)
        })
        .collect()
}

impl CompletionProvider for DoctrineCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let (Some(root), Some(document)) = (request.root.as_ref(), request.document.as_deref())
        else {
            return Vec::new();
        };
        let path = swlsp_uriutil::path(&request.params.text_document.uri).unwrap_or_default();
        let extension = lower_ext(&path);
        let position = request.params.position;
        let offset = document
            .line_index()
            .offset_utf16(position.line, position.character);
        if let Some(registration) =
            swlsp_doctrine::type_registration_reference_at(&path, root, TextSize::from(offset))
            && registration.role == TypeRegistrationReferenceRole::Class
        {
            return self.dbal_type_class_completions(&registration, &extension, document.source());
        }
        if extension != ".php" {
            return Vec::new();
        }
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        let context = self.php.as_ref().and_then(|php| php_context(php, request));
        dbal_completion_items(self.index.dbal_completions_at(context.as_ref(), root, node))
    }

    fn trigger_characters(&self) -> Vec<String> {
        strings(&["'", "\"", ".", ":"])
    }
}
