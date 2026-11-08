use serde::{Deserialize, Serialize};
use swlsp_parser_scss::syntax::{
    SCSS_ARGUMENT, SCSS_AT_RULE, SCSS_BLOCK, SCSS_FUNCTION_CALL, SCSS_STYLESHEET, SCSS_VARIABLE,
    SCSS_VARIABLE_DECLARATION, TK_AT_KEYWORD, TK_COLON, TK_IDENTIFIER, TK_OPERATOR, TK_VARIABLE,
};
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, SyntaxToken};
use swlsp_theme::ThemeConfigField;

use crate::model::{ByteRange, SourcePosition};

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum VariableOccurrenceKind {
    #[default]
    GlobalDeclaration = 0,
    LocalBinding = 1,
    Reference = 2,
    ThemeConfig = 3,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct VariableOccurrence {
    /// The name without the leading `$`.
    pub name: String,
    pub file: String,
    pub range: ByteRange,
    pub start: SourcePosition,
    pub end: SourcePosition,
    pub kind: VariableOccurrenceKind,
}

/// All declarations of one normalized variable name in one file.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct VariableCatalog {
    pub name: String,
    pub file: String,
    pub occurrences: Vec<VariableOccurrence>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct VariableAnalysis {
    pub bindings: Vec<VariableOccurrence>,
    pub global_declarations: Vec<VariableOccurrence>,
    pub references: Vec<VariableOccurrence>,
}

/// Sass identifier equivalence (`-` and `_` are interchangeable); names are
/// stored without the leading `$`.
pub fn normalize_variable_name(name: &str) -> String {
    name.strip_prefix('$').unwrap_or(name).replace('_', "-")
}

/// Classifies every SCSS variable node in one traversal. Bindings stay
/// document-wide: diagnostics prefer a possible false negative over
/// reporting scoped parameters or loop variables in incomplete SCSS.
pub fn analyze_variables(path: &str, root: &SyntaxNode) -> VariableAnalysis {
    let mut analysis = VariableAnalysis::default();
    for node in root.descendants() {
        if node.kind() != SCSS_VARIABLE {
            continue;
        }
        let Some((name, range)) = variable_name_and_range(&node) else {
            continue;
        };
        let mut occurrence = VariableOccurrence {
            name,
            file: path.to_owned(),
            range,
            kind: VariableOccurrenceKind::Reference,
            ..VariableOccurrence::default()
        };
        match classify_variable(&node) {
            Role::GlobalDeclaration => {
                occurrence.kind = VariableOccurrenceKind::GlobalDeclaration;
                analysis.bindings.push(occurrence.clone());
                analysis.global_declarations.push(occurrence);
            }
            Role::LocalBinding => {
                occurrence.kind = VariableOccurrenceKind::LocalBinding;
                analysis.bindings.push(occurrence);
            }
            Role::Ignored => {}
            Role::Reference => analysis.references.push(occurrence),
        }
    }
    analysis
}

/// SCSS-visible theme configuration fields as variable declarations.
pub fn theme_variables(fields: &[ThemeConfigField]) -> Vec<VariableOccurrence> {
    let mut result: Vec<VariableOccurrence> = fields
        .iter()
        .filter(|field| field.scss && !normalize_variable_name(&field.key).is_empty())
        .map(|field| {
            let line = u32::try_from((field.line - 1).max(0)).unwrap_or(u32::MAX);
            VariableOccurrence {
                name: field.key.clone(),
                file: field.path.clone(),
                start: SourcePosition { line, character: 0 },
                end: SourcePosition { line, character: 0 },
                kind: VariableOccurrenceKind::ThemeConfig,
                ..VariableOccurrence::default()
            }
        })
        .collect();
    result.sort_by(|left, right| {
        (&left.file, left.start.line, &left.name).cmp(&(&right.file, right.start.line, &right.name))
    });
    result
}

enum Role {
    Reference,
    GlobalDeclaration,
    LocalBinding,
    Ignored,
}

fn classify_variable(variable: &SyntaxNode) -> Role {
    if let Some(declaration) = variable.ancestor_or_self_of_kind(SCSS_VARIABLE_DECLARATION)
        && first_variable(&declaration).as_ref() == Some(variable)
    {
        if declaration
            .parent()
            .is_some_and(|parent| parent.kind() == SCSS_STYLESHEET)
            || has_important_flag(&declaration, "global")
        {
            return Role::GlobalDeclaration;
        }
        return Role::LocalBinding;
    }

    if let Some(at_rule) = variable.ancestor_or_self_of_kind(SCSS_AT_RULE)
        && before_at_rule_block(variable, &at_rule)
    {
        let binding = match at_rule_name(&at_rule).as_str() {
            "mixin" | "function" => leading_argument_variable(variable),
            "each" => direct_variable_before_identifier(variable, &at_rule, "in"),
            "for" => first_direct_variable(variable, &at_rule),
            "content" => function_name(variable, "using") && leading_argument_variable(variable),
            _ => false,
        };
        if binding {
            return Role::LocalBinding;
        }
    }

    if is_module_variable(variable) || leading_named_argument_variable(variable) {
        return Role::Ignored;
    }
    Role::Reference
}

fn variable_name_and_range(variable: &SyntaxNode) -> Option<(String, ByteRange)> {
    let token = variable.child_token_of_kind(TK_VARIABLE)?;
    let text = token.text();
    let name = text.strip_prefix('$').unwrap_or(text);
    if name.is_empty() {
        return None;
    }
    Some((name.to_owned(), token.text_range().into()))
}

fn first_variable(root: &SyntaxNode) -> Option<SyntaxNode> {
    root.descendants().find(|node| node.kind() == SCSS_VARIABLE)
}

fn tokens(root: &SyntaxNode) -> impl Iterator<Item = SyntaxToken> + use<> {
    root.descendants_with_tokens()
        .filter_map(NodeOrToken::into_token)
}

fn has_important_flag(root: &SyntaxNode, name: &str) -> bool {
    let mut previous_bang = false;
    for token in tokens(root) {
        if token.kind().is_trivia() {
            continue;
        }
        if previous_bang && token.kind() == TK_IDENTIFIER && equal_fold(token.text(), name) {
            return true;
        }
        previous_bang = token.kind() == TK_OPERATOR && token.text() == "!";
    }
    false
}

fn before_at_rule_block(variable: &SyntaxNode, at_rule: &SyntaxNode) -> bool {
    match at_rule.children().find(|child| child.kind() == SCSS_BLOCK) {
        Some(block) => variable.text_range().start() < block.text_range().start(),
        None => true,
    }
}

fn at_rule_name(at_rule: &SyntaxNode) -> String {
    tokens(at_rule)
        .find(|token| token.kind() == TK_AT_KEYWORD)
        .map(|token| {
            let text = token.text();
            text.strip_prefix('@').unwrap_or(text).to_lowercase()
        })
        .unwrap_or_default()
}

fn leading_argument_variable(variable: &SyntaxNode) -> bool {
    variable
        .ancestor_or_self_of_kind(SCSS_ARGUMENT)
        .is_some_and(|argument| first_variable(&argument).as_ref() == Some(variable))
}

fn leading_named_argument_variable(variable: &SyntaxNode) -> bool {
    let Some(argument) = variable.ancestor_or_self_of_kind(SCSS_ARGUMENT) else {
        return false;
    };
    if first_variable(&argument).as_ref() != Some(variable) {
        return false;
    }
    let end = variable.text_range().end();
    argument
        .children_with_tokens()
        .filter_map(NodeOrToken::into_token)
        .any(|token| token.kind() == TK_COLON && end <= token.text_range().start())
}

fn direct_variable_before_identifier(
    variable: &SyntaxNode,
    at_rule: &SyntaxNode,
    identifier: &str,
) -> bool {
    if variable.parent().as_ref() != Some(at_rule) {
        return false;
    }
    tokens(at_rule)
        .find(|token| token.kind() == TK_IDENTIFIER && equal_fold(token.text(), identifier))
        .is_some_and(|token| variable.text_range().end() <= token.text_range().start())
}

fn first_direct_variable(variable: &SyntaxNode, at_rule: &SyntaxNode) -> bool {
    if variable.parent().as_ref() != Some(at_rule) {
        return false;
    }
    at_rule
        .children()
        .find(|child| child.kind() == SCSS_VARIABLE)
        .as_ref()
        == Some(variable)
}

fn function_name(variable: &SyntaxNode, name: &str) -> bool {
    variable
        .ancestor_or_self_of_kind(SCSS_FUNCTION_CALL)
        .and_then(|call| swlsp_parser_scss::query::function_name(&call))
        .is_some_and(|function| equal_fold(&function, name))
}

fn is_module_variable(variable: &SyntaxNode) -> bool {
    let mut sibling = variable.prev_sibling_or_token();
    while let Some(current) = sibling {
        if current.kind().is_trivia() {
            sibling = current.prev_sibling_or_token();
            continue;
        }
        return matches!(current, NodeOrToken::Token(token) if token.text() == ".");
    }
    false
}

/// Go `strings.EqualFold` against a lowercase ASCII target: besides ASCII
/// case, `ſ` (U+017F) folds to `s` and the Kelvin sign (U+212A) to `k`.
pub(crate) fn equal_fold(value: &str, target: &str) -> bool {
    let mut chars = value.chars();
    for expected in target.chars() {
        let Some(actual) = chars.next() else {
            return false;
        };
        let matches = actual.eq_ignore_ascii_case(&expected)
            || (actual == '\u{17f}' && expected.eq_ignore_ascii_case(&'s'))
            || (actual == '\u{212a}' && expected.eq_ignore_ascii_case(&'k'));
        if !matches {
            return false;
        }
    }
    chars.next().is_none()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn names(occurrences: &[VariableOccurrence]) -> Vec<&str> {
        let mut names: Vec<&str> = occurrences
            .iter()
            .map(|occurrence| occurrence.name.as_str())
            .collect();
        names.sort_unstable();
        names
    }

    fn sorted(mut values: Vec<&str>) -> Vec<&str> {
        values.sort_unstable();
        values
    }

    #[test]
    fn analyze_variables_classifies_bindings_references_and_ignored_labels() {
        let source = r#"$global-value: $seed !default;
.block {
  $local: red;
  $forced: blue !global;
  color: $local;
}
@mixin button($variant, $color: $global-value) {
  color: $color;
  border-color: $missing;
}
@function tone($input) { @return $input; }
@each $key, $value in $items { color: $value; }
@for $i from 1 through $count { z-index: $i; }
@include button($color: $global-value);
math.$pi;
@use "theme" with ($configured: $global-value);"#;
        let parsed = swlsp_parser_scss::parse(source);
        assert!(parsed.errors.is_empty());
        let analysis = analyze_variables("/project/main.scss", &parsed.root());
        assert_eq!(
            names(&analysis.bindings),
            sorted(vec![
                "global-value",
                "local",
                "forced",
                "variant",
                "color",
                "input",
                "key",
                "value",
                "i"
            ])
        );
        assert_eq!(
            names(&analysis.global_declarations),
            sorted(vec!["global-value", "forced"])
        );
        assert_eq!(
            names(&analysis.references),
            sorted(vec![
                "seed",
                "local",
                "global-value",
                "color",
                "missing",
                "input",
                "items",
                "value",
                "count",
                "i",
                "global-value",
                "global-value",
            ])
        );
        let missing = &analysis.references[4];
        assert_eq!(
            &source[missing.range.start as usize..missing.range.end as usize],
            "$missing"
        );
    }

    #[test]
    fn normalize_variable_name_matches_sass_identifier_rules() {
        assert_eq!(normalize_variable_name("$brand_primary"), "brand-primary");
        assert_eq!(normalize_variable_name("brand-primary"), "brand-primary");
    }
}
