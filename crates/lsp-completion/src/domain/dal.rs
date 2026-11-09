//! DAL entity and field names.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context, effective_syntax_language};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_shopware_dal::{Index, JsFieldReferenceKind};

use super::{item, lower_ext};
use swlsp_textutil::lowercase;

pub struct DalCompletionProvider {
    index: Arc<Index>,
}

impl DalCompletionProvider {
    pub fn new(index: Arc<Index>) -> Self {
        Self { index }
    }

    fn definition_items(&self) -> Vec<protocol::CompletionItem> {
        let Ok(definitions) = self.index.definitions() else {
            return Vec::new();
        };
        definitions
            .into_iter()
            .map(|definition| item(definition.name, CompletionItemKind::CLASS, definition.class))
            .collect()
    }

    fn js_field_completions(&self, associations_only: bool) -> Vec<protocol::CompletionItem> {
        #[derive(Default)]
        struct Candidate {
            association: bool,
            types: BTreeSet<String>,
            entities: BTreeSet<String>,
        }
        let Ok(definitions) = self.index.definitions() else {
            return Vec::new();
        };
        let mut candidates: BTreeMap<String, Candidate> = BTreeMap::new();
        for definition in &definitions {
            for field in &definition.fields {
                if associations_only && !field.association {
                    continue;
                }
                let current = candidates.entry(field.name.clone()).or_default();
                current.association |= field.association;
                current.types.insert(field.type_.clone());
                current.entities.insert(definition.name.clone());
            }
        }
        candidates
            .into_iter()
            .map(|(name, current)| {
                let types: Vec<&str> = current.types.iter().map(String::as_str).collect();
                let mut detail = types.join(" | ");
                if current.entities.len() <= 3 {
                    let entities: Vec<&str> = current.entities.iter().map(String::as_str).collect();
                    detail.push_str(" · ");
                    detail.push_str(&entities.join(", "));
                } else {
                    detail.push_str(&format!(" · {} entities", current.entities.len()));
                }
                let kind = if current.association {
                    CompletionItemKind::PROPERTY
                } else {
                    CompletionItemKind::FIELD
                };
                item(name, kind, detail)
            })
            .collect()
    }
}

impl CompletionProvider for DalCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        let uri = &request.params.text_document.uri;
        match lower_ext(uri).as_str() {
            ".js" | ".ts" | ".vue" => {
                if lowercase(uri).ends_with(".vue")
                    && effective_syntax_language(Some(LanguageId::Vue), Some(node))
                        != Some(LanguageId::JavaScript)
                {
                    return Vec::new();
                }
                if swlsp_shopware_dal::is_js_entity_reference(node) {
                    return self.definition_items();
                }
                match swlsp_shopware_dal::js_field_reference_at(node) {
                    JsFieldReferenceKind::None => Vec::new(),
                    kind => self.js_field_completions(kind == JsFieldReferenceKind::Association),
                }
            }
            ".twig" => {
                if !swlsp_uriutil::filepath::to_slash(uri).contains("/Resources/scripts/")
                    || !swlsp_shopware_dal::is_twig_entity_reference(node)
                {
                    return Vec::new();
                }
                self.definition_items()
            }
            ".php" => {
                if !swlsp_shopware_dal::is_php_field_reference(node) {
                    return Vec::new();
                }
                let Ok(fields) = self.index.fields() else {
                    return Vec::new();
                };
                fields
                    .into_iter()
                    .map(|field| item(field.name, CompletionItemKind::FIELD, field.type_))
                    .collect()
            }
            _ => Vec::new(),
        }
    }

    fn trigger_characters(&self) -> Vec<String> {
        Vec::new()
    }
}
