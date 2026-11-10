//! Symfony `#[AsCommand]` `__invoke()` return type and value inspections.

use std::sync::Arc;

use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{PHP_NAME, PHP_RETURN_STATEMENT};
use swlsp_php::{NameResolver, PhpIndex};
use swlsp_php_types::{Kind as TypeKind, Type};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};
use swlsp_textutil::eq_ignore_case;

use super::{lower_ext, problem};
use crate::diagnostics::ProblemAnalyzer;
use crate::diagnostics::common::direct_return_expression;

const INVOKABLE_COMMAND_RETURN_TYPE_CODE: &str = "symfony.console.invoke.return_type";
const INVOKABLE_COMMAND_RETURN_VALUE_CODE: &str = "symfony.console.invoke.return_value";

pub const INVOKABLE_COMMAND_RETURN_TYPE_MESSAGE: &str =
    "Symfony: Consider adding int return type to command __invoke()";
pub const INVOKABLE_COMMAND_RETURN_VALUE_MESSAGE: &str =
    "Symfony: Command must return an integer value";

const AS_COMMAND_ATTRIBUTE: &str = "Symfony\\Component\\Console\\Attribute\\AsCommand";

/// Ports Symfony's AsCommand `__invoke()` return-type and return-value
/// inspections.
pub struct InvokableCommandAnalyzer {
    php_index: Option<PhpIndex>,
}

impl InvokableCommandAnalyzer {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl ProblemAnalyzer for InvokableCommandAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::diagnostics::inputs(&self.php_index))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(php) = &self.php_index else {
            return Ok(Vec::new());
        };
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        if lower_ext(&document.uri) != ".php" || !document.source().contains("AsCommand") {
            return Ok(Vec::new());
        }
        let Some(analysis) = swlsp_lsp_phpanalysis::for_document(php, document)? else {
            return Ok(Vec::new());
        };
        let resolver = NameResolver::new(&root);
        let mut result = Vec::new();
        for class in phpquery::classes(&root) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            if !has_resolved_as_command_attribute(&class, &resolver) {
                continue;
            }
            for method in phpquery::methods(&class) {
                if !eq_ignore_case(&phpquery::method_name(&method), "__invoke") {
                    continue;
                }
                if native_return_includes_int(&phpquery::method_return_type(&method)) {
                    continue;
                }
                result.push(problem(
                    INVOKABLE_COMMAND_RETURN_TYPE_CODE,
                    method_name_range(&method),
                    INVOKABLE_COMMAND_RETURN_TYPE_MESSAGE.to_owned(),
                    DiagnosticSeverity::HINT,
                ));
                for php_return in phpquery::nodes(&method, &[PHP_RETURN_STATEMENT]) {
                    if phpquery::function_like_at(&php_return).as_ref() != Some(&method) {
                        continue;
                    }
                    let expression = direct_return_expression(&php_return);
                    if let Some(expression) = &expression
                        && type_includes_int(&analysis.document.type_of(expression).ty)
                    {
                        continue;
                    }
                    let range = expression
                        .as_ref()
                        .unwrap_or(&php_return)
                        .range_trimmed_trivia();
                    result.push(problem(
                        INVOKABLE_COMMAND_RETURN_VALUE_CODE,
                        range,
                        INVOKABLE_COMMAND_RETURN_VALUE_MESSAGE.to_owned(),
                        DiagnosticSeverity::WARNING,
                    ));
                }
            }
        }
        Ok(result)
    }
}

fn has_resolved_as_command_attribute(class: &SyntaxNode, resolver: &NameResolver) -> bool {
    phpquery::attributes(class).iter().any(|attribute| {
        let name = resolver.resolve(&phpquery::attribute_name(attribute));
        eq_ignore_case(
            name.strip_prefix('\\').unwrap_or(&name),
            AS_COMMAND_ATTRIBUTE,
        )
    })
}

fn native_return_includes_int(source: &str) -> bool {
    source
        .split(|character: char| {
            character != '\\' && character != '_' && !character.is_alphabetic()
        })
        .filter(|part| !part.is_empty())
        .any(|part| eq_ignore_case(part, "int") || eq_ignore_case(part, "integer"))
}

fn type_includes_int(value: &Type) -> bool {
    match value.kind() {
        TypeKind::Int | TypeKind::LiteralInt => true,
        TypeKind::Union | TypeKind::Intersection => value.arguments().iter().any(type_includes_int),
        _ => false,
    }
}

fn method_name_range(method: &SyntaxNode) -> TextRange {
    phpquery::direct_child(method, PHP_NAME)
        .unwrap_or_else(|| method.clone())
        .range_trimmed_trivia()
}
