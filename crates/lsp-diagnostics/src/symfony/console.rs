//! Missing console commands, arguments and options.

use std::sync::Arc;

use swlsp_console::{
    Index, InputKind, ReferenceRole, inputs_for_reference, reference_at, validate_reference,
};
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_php::PhpIndex;

use super::{lower_ext, symfony_warning};
use crate::common::value_node_text_range;
use crate::{DeclaredAnalyzer, ProblemAnalyzer};

pub const MISSING_CONSOLE_COMMAND_CODE: &str = "symfony.console.command.missing";
pub const MISSING_CONSOLE_ARGUMENT_CODE: &str = "symfony.console.argument.missing";
pub const MISSING_CONSOLE_OPTION_CODE: &str = "symfony.console.option.missing";

pub struct ConsoleAnalyzer {
    index: Option<Arc<Index>>,
    php_index: Option<PhpIndex>,
}

impl ConsoleAnalyzer {
    pub fn new(index: Option<Arc<Index>>, php_index: Option<PhpIndex>) -> Self {
        Self { index, php_index }
    }
}

impl DeclaredAnalyzer for ConsoleAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "symfony.console",
        description: "Console commands, arguments and options that are referenced but not defined.",
        domain: "symfony.console",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Php],
        source: "symfony",
        codes: &[
            MISSING_CONSOLE_ARGUMENT_CODE,
            MISSING_CONSOLE_COMMAND_CODE,
            MISSING_CONSOLE_OPTION_CODE,
        ],
    };
}

impl ProblemAnalyzer for ConsoleAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::inputs(&self.index) + crate::inputs(&self.php_index))
    }

    fn analyze(
        &self,
        _ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(index), Some(php_index), Some(root)) =
            (&self.index, &self.php_index, document.root())
        else {
            return Ok(Vec::new());
        };
        if lower_ext(&document.uri) != ".php" {
            return Ok(Vec::new());
        }
        let context = swlsp_lsp_phpanalysis::context_for_document(php_index, document)?;
        let mut result = Vec::new();
        for literal in phpquery::nodes(&root, &[PHP_STRING]) {
            let Some(reference) = reference_at(&literal) else {
                continue;
            };
            if reference.name.is_empty() || !validate_reference(context.as_ref(), &reference) {
                continue;
            }
            match reference.role {
                ReferenceRole::Command => {
                    if !index.get_command(&reference.name)?.is_empty() {
                        continue;
                    }
                    let names: Vec<String> = index
                        .get_commands()?
                        .into_iter()
                        .map(|command| command.name)
                        .collect();
                    result.push(symfony_warning(
                        MISSING_CONSOLE_COMMAND_CODE,
                        value_node_text_range(Some(&reference.node), &reference.name),
                        format!("Console command '{}' not found", reference.name),
                        swlsp_suggestion::similar(&reference.name, &names),
                    ));
                }
                ReferenceRole::Argument | ReferenceRole::Option => {
                    let inputs = inputs_for_reference(context.as_ref(), index, &reference)?;
                    let mut found = false;
                    let mut names = Vec::with_capacity(inputs.len());
                    for input in &inputs {
                        names.push(input.name.clone());
                        if input.name == reference.name
                            || input.kind == InputKind::Option && input.shortcut == reference.name
                        {
                            found = true;
                        }
                    }
                    if found || inputs.is_empty() {
                        continue;
                    }
                    let (kind, code) = if reference.role == ReferenceRole::Option {
                        ("option", MISSING_CONSOLE_OPTION_CODE)
                    } else {
                        ("argument", MISSING_CONSOLE_ARGUMENT_CODE)
                    };
                    result.push(symfony_warning(
                        code,
                        value_node_text_range(Some(&reference.node), &reference.name),
                        format!(
                            "Console {kind} '{}' is not defined for this command",
                            reference.name
                        ),
                        swlsp_suggestion::similar(&reference.name, &names),
                    ));
                }
                ReferenceRole::None => {}
            }
        }
        Ok(result)
    }
}
