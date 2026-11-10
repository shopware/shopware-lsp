use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_php::PhpIndex;
use swlsp_translation::{Index, Message, ReferenceRole};

use crate::definition::common::{ext_lower, request_uri};
use crate::definition::php_common::php_context;

pub struct TranslationDefinitionProvider {
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
}

impl TranslationDefinitionProvider {
    pub fn new(index: Option<Arc<Index>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl GotoDefinitionProvider for TranslationDefinitionProvider {
    fn get_definition(
        &self,
        ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let (Some(index), Some(node)) = (&self.index, request.node.as_ref()) else {
            return Vec::new();
        };
        let uri = request_uri(request);
        let extension = ext_lower(uri);
        if extension != ".php" && extension != ".twig" {
            return Vec::new();
        }
        let is_php = extension == ".php";
        let context = if is_php {
            php_context(self.php.as_ref(), request)
        } else {
            None
        };
        if let Some(context) = context.as_ref() {
            if swlsp_php::assistant_argument_reference(context, node, "TranslationDomain").is_some()
            {
                let Ok(messages) = index.domain_messages(&phpquery::string_value(node)) else {
                    return Vec::new();
                };
                return locations(&first_per_file(messages));
            }
            if swlsp_php::assistant_argument_reference(context, node, "TranslationKey").is_some() {
                let domain = swlsp_php::assistant_sibling_string_argument(
                    context,
                    node,
                    "TranslationDomain",
                )
                .unwrap_or_else(|| "messages".to_owned());
                let Ok(messages) = index.messages_for(&domain, &phpquery::string_value(node))
                else {
                    return Vec::new();
                };
                return locations(&messages);
            }
        }
        let Some(reference) = swlsp_translation::reference_at(uri, node, request.source()) else {
            return Vec::new();
        };
        if is_php
            && !swlsp_translation::validate_php_reference(
                context.as_ref(),
                &reference,
                self.php.as_ref(),
            )
        {
            return Vec::new();
        }
        let messages = match reference.role {
            ReferenceRole::Key => index.messages_for(&reference.domain, &reference.key),
            ReferenceRole::Domain => index.domain_messages(&reference.domain),
            _ => return Vec::new(),
        };
        let Ok(mut messages) = messages else {
            return Vec::new();
        };
        if ctx.is_cancelled() {
            return Vec::new();
        }
        if reference.role == ReferenceRole::Domain {
            messages = first_per_file(messages);
        }
        locations(&messages)
    }
}

fn first_per_file(messages: Vec<Message>) -> Vec<Message> {
    let mut seen = HashSet::new();
    messages
        .into_iter()
        .filter(|message| seen.insert(message.file.clone()))
        .collect()
}

fn locations(messages: &[Message]) -> Vec<protocol::Location> {
    messages
        .iter()
        .map(|message| protocol::Location {
            uri: swlsp_uriutil::file_uri(&message.file),
            range: protocol::Range {
                start: protocol::Position {
                    line: message.line,
                    character: message.character,
                },
                end: protocol::Position {
                    line: message.end_line,
                    character: message.end_character,
                },
            },
        })
        .collect()
}
