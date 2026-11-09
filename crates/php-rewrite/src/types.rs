//! Native type edits.

use swlsp_parser_php::query::direct_child;
use swlsp_parser_php::syntax::{
    PHP_ARROW_FUNCTION, PHP_CLOSURE, PHP_FUNCTION_DECLARATION, PHP_INTERSECTION_TYPE,
    PHP_METHOD_DECLARATION, PHP_NULLABLE_TYPE, PHP_PARAMETER, PHP_PARAMETER_LIST,
    PHP_PROPERTY_DECLARATION, PHP_TYPE, PHP_UNION_TYPE, PHP_VARIABLE,
};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};

use crate::editor::{Editor, Result, end, invalid, range, start};

impl Editor<'_> {
    /// Adds or replaces the native type of one parameter.
    pub fn set_parameter_type(&mut self, parameter: &SyntaxNode, type_text: &str) -> Result<()> {
        if parameter.kind() != PHP_PARAMETER {
            return invalid("set PHP parameter type: parameter is unavailable");
        }
        self.set_declared_type(parameter, type_text, "set PHP parameter type")
    }

    /// Adds or replaces the native type of a property declaration.
    pub fn set_property_type(&mut self, property: &SyntaxNode, type_text: &str) -> Result<()> {
        if property.kind() != PHP_PROPERTY_DECLARATION {
            return invalid("set PHP property type: property is unavailable");
        }
        self.set_declared_type(property, type_text, "set PHP property type")
    }

    fn set_declared_type(
        &mut self,
        owner: &SyntaxNode,
        type_text: &str,
        operation: &str,
    ) -> Result<()> {
        let type_text = type_text.trim();
        if type_text.is_empty() {
            return invalid(format!("{operation}: type is empty"));
        }
        if let Some(existing) = direct_type_node(owner) {
            return self.replace_range(existing.range_trimmed_trivia(), type_text);
        }
        let Some(variable) = direct_child(owner, PHP_VARIABLE) else {
            return invalid(format!("{operation}: variable is unavailable"));
        };
        self.insert(
            start(variable.range_trimmed_trivia()),
            format!("{type_text} "),
        )
    }

    /// Adds or replaces a function-like declaration's return type.
    pub fn set_return_type(&mut self, function_like: &SyntaxNode, type_text: &str) -> Result<()> {
        if !matches!(
            function_like.kind(),
            PHP_METHOD_DECLARATION | PHP_FUNCTION_DECLARATION | PHP_CLOSURE | PHP_ARROW_FUNCTION
        ) {
            return invalid("set PHP return type: declaration is unavailable");
        }
        let type_text = type_text.trim();
        if type_text.is_empty() {
            return invalid("set PHP return type: type is empty");
        }
        if let Some(existing) = direct_type_node(function_like) {
            return self.replace_range(existing.range_trimmed_trivia(), type_text);
        }
        let Some(parameters) = direct_child(function_like, PHP_PARAMETER_LIST) else {
            return invalid("set PHP return type: parameter list is unavailable");
        };
        let offset = end(parameters.range_trimmed_trivia());
        self.replace_range(range(offset, offset), format!(": {type_text}"))
    }
}

fn direct_type_node(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.children().find(|child| {
        matches!(
            child.kind(),
            PHP_TYPE | PHP_NULLABLE_TYPE | PHP_UNION_TYPE | PHP_INTERSECTION_TYPE
        )
    })
}
