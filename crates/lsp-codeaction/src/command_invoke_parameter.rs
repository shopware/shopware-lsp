//! Adds Symfony console
//! parameters to an `#[AsCommand]` class's `__invoke`.

use crate::shared::document_snapshot;
use std::collections::BTreeMap;

use swlsp_language::LanguageId;
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_parser_php::query as phpquery;
use swlsp_php::{NameResolver, PhpIndex};
use swlsp_syntax::SyntaxNode;
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::shared::{
    PhpSuggestedParameter, has_resolved_php_attribute, php_class_qualifier, php_class_short_name,
    php_existing_parameter_types, php_required_parameter_edit, trim_backslashes,
};

pub(crate) const AS_COMMAND_ATTRIBUTE_CLASS: &str =
    "Symfony\\Component\\Console\\Attribute\\AsCommand";
pub(crate) const CONSOLE_COMMAND_CLASS: &str = "Symfony\\Component\\Console\\Command\\Command";

const COMMAND_INVOKE_PARAMETERS: [PhpSuggestedParameter; 5] = [
    PhpSuggestedParameter {
        class_name: "Symfony\\Component\\Console\\Input\\InputInterface",
        variable_name: "input",
    },
    PhpSuggestedParameter {
        class_name: "Symfony\\Component\\Console\\Output\\OutputInterface",
        variable_name: "output",
    },
    PhpSuggestedParameter {
        class_name: "Symfony\\Component\\Console\\Cursor",
        variable_name: "cursor",
    },
    PhpSuggestedParameter {
        class_name: "Symfony\\Component\\Console\\Style\\SymfonyStyle",
        variable_name: "io",
    },
    PhpSuggestedParameter {
        class_name: "Symfony\\Component\\Console\\Application",
        variable_name: "application",
    },
];

/// Ports the Symfony plugin's `CommandInvokeParameterIntention` as one
/// refactor action per available parameter type.
pub struct CommandInvokeParameterCodeActionProvider {
    php_index: Option<PhpIndex>,
}

impl CommandInvokeParameterCodeActionProvider {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }

    fn is_console_command_subclass(
        &self,
        index: &PhpIndex,
        request: &CodeActionRequest,
        root: &SyntaxNode,
        class: &SyntaxNode,
        resolver: &NameResolver,
    ) -> bool {
        for parent in phpquery::class_extends(class) {
            if eq_ignore_case(
                trim_backslashes(&resolver.resolve(&parent)),
                CONSOLE_COMMAND_CLASS,
            ) {
                return true;
            }
        }
        let Some(document) = request.document.as_ref() else {
            return false;
        };
        let Ok(path) = swlsp_uriutil::path(&document.uri) else {
            return false;
        };
        let snapshot = document_snapshot(index, document, &path, root);
        let mut class_name = phpquery::class_name(class);
        let namespace = phpquery::namespace(root);
        if !namespace.is_empty() {
            class_name = format!("{namespace}\\{class_name}");
        }
        snapshot.is_subtype_of(&class_name, CONSOLE_COMMAND_CLASS)
    }
}

impl ActionProvider for CommandInvokeParameterCodeActionProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_REWRITE]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        let Some(index) = self.php_index.as_ref() else {
            return Vec::new();
        };
        let (Some(document), Some(root), Some(node)) = (
            request.document.as_ref(),
            request.root.as_ref(),
            request.node.as_ref(),
        ) else {
            return Vec::new();
        };
        if ctx.is_cancelled() || document.language != Some(LanguageId::Php) {
            return Vec::new();
        }
        let Some(class) = phpquery::class_at(node) else {
            return Vec::new();
        };
        let resolver = NameResolver::new(root);
        if !has_resolved_php_attribute(&class, &resolver, &[AS_COMMAND_ATTRIBUTE_CLASS])
            || self.is_console_command_subclass(index, request, root, &class, &resolver)
        {
            return Vec::new();
        }
        let Some(invoke_method) = phpquery::methods(&class)
            .into_iter()
            .find(|method| eq_ignore_case(&phpquery::method_name(method), "__invoke"))
        else {
            return Vec::new();
        };
        let existing_types = php_existing_parameter_types(&invoke_method, &resolver);
        let mut result = Vec::new();
        for parameter in COMMAND_INVOKE_PARAMETERS {
            if existing_types.contains(&lowercase(parameter.class_name)) {
                continue;
            }
            let (qualifier, import_edit) = php_class_qualifier(request, parameter.class_name);
            let Some(parameter_edit) = php_required_parameter_edit(
                request,
                &class,
                &invoke_method,
                &qualifier,
                parameter.variable_name,
            ) else {
                continue;
            };
            let mut edits = vec![parameter_edit];
            edits.extend(import_edit);
            result.push(protocol::CodeAction {
                title: format!(
                    "Symfony: Add {} parameter to __invoke",
                    php_class_short_name(parameter.class_name)
                ),
                kind: Some(CodeActionKind::REFACTOR_REWRITE),
                edit: Some(protocol::WorkspaceEdit {
                    changes: BTreeMap::from([(request.params.text_document.uri.clone(), edits)]),
                    ..protocol::WorkspaceEdit::default()
                }),
                ..protocol::CodeAction::default()
            });
        }
        result
    }
}

#[cfg(test)]
#[path = "tests/command_invoke_parameter.rs"]
mod tests;
