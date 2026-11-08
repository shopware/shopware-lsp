//! Tree queries over SCSS syntax (port of Go `internal/parser/scss/query`).
//!
//! The `*_at` functions accept any node inside the construct (including the
//! construct itself) and walk up to the nearest enclosing one. Start from a
//! token with `token.parent()`.

use swlsp_syntax::{Kind, SyntaxNode, SyntaxToken};

use crate::syntax::*;

/// All nodes of `kinds` in the subtree of `root` (including `root`), in
/// preorder.
pub fn nodes(root: &SyntaxNode, kinds: &[Kind]) -> Vec<SyntaxNode> {
    root.descendants()
        .filter(|node| kinds.contains(&node.kind()))
        .collect()
}

/// The nearest `SCSS_VARIABLE` at or above `node`.
pub fn variable_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, SCSS_VARIABLE)
}

/// The `$`-prefixed variable token of the variable at `node`.
pub fn variable_token(node: &SyntaxNode) -> Option<SyntaxToken> {
    child_token(&variable_at(node)?, |kind| kind == TK_VARIABLE)
}

/// The variable name at `node` without the leading `$`.
pub fn variable_name(node: &SyntaxNode) -> Option<String> {
    let token = variable_token(node)?;
    let text = token.text();
    Some(text.strip_prefix('$').unwrap_or(text).to_owned())
}

/// The nearest `SCSS_STRING` at or above `node`.
pub fn string_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, SCSS_STRING)
}

/// The quoted string token of the string at `node`.
pub fn string_token(node: &SyntaxNode) -> Option<SyntaxToken> {
    child_token(&string_at(node)?, |kind| {
        kind == TK_SINGLE_QUOTED_STRING || kind == TK_DOUBLE_QUOTED_STRING
    })
}

/// The unquoted value of the string at `node` (escapes are kept verbatim).
pub fn string_value(node: &SyntaxNode) -> Option<String> {
    let token = string_token(node)?;
    let quote = if token.kind() == TK_SINGLE_QUOTED_STRING {
        '\''
    } else {
        '"'
    };
    Some(trim_quoted(token.text(), quote).to_owned())
}

/// The nearest `SCSS_FUNCTION_CALL` at or above `node`.
pub fn function_call_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, SCSS_FUNCTION_CALL)
}

/// The name identifier token of the function call at `node`.
pub fn function_name_token(node: &SyntaxNode) -> Option<SyntaxToken> {
    child_token(&function_call_at(node)?, |kind| kind == TK_IDENTIFIER)
}

/// The name of the function call at `node`.
pub fn function_name(node: &SyntaxNode) -> Option<String> {
    function_name_token(node).map(|token| token.text().to_owned())
}

/// Whether the string at `node` is (part of) an argument of the nearest
/// enclosing function call and that call is named one of `names`.
pub fn string_in_function(node: &SyntaxNode, names: &[&str]) -> bool {
    let Some(string) = string_at(node) else {
        return false;
    };
    let Some(call) = string.parent().and_then(|parent| function_call_at(&parent)) else {
        return false;
    };
    function_name_token(&call).is_some_and(|token| names.contains(&token.text()))
}

/// The string at `node` when [`string_in_function`] holds.
pub fn string_argument_in_function(node: &SyntaxNode, names: &[&str]) -> Option<SyntaxNode> {
    if !string_in_function(node, names) {
        return None;
    }
    string_at(node)
}

fn ancestor_or_self(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.ancestors().find(|current| current.kind() == kind)
}

fn child_token(node: &SyntaxNode, accept: impl Fn(Kind) -> bool) -> Option<SyntaxToken> {
    node.children_with_tokens()
        .filter_map(|element| element.into_token())
        .find(|token| accept(token.kind()))
}

fn trim_quoted(text: &str, quote: char) -> &str {
    let text = text.strip_prefix(quote).unwrap_or(text);
    text.strip_suffix(quote).unwrap_or(text)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::parse;

    #[test]
    fn variable_and_function_queries() {
        let parse = parse(
            "\n$sw-color-brand-primary: #0042a0;\n.button {\n  color: $sw-color-brand-primary;\n  display: feature(\"ACCESSIBILITY_TWEAKS\");\n}",
        );
        assert!(parse.errors.is_empty(), "{}", parse.debug_dump());
        let root = parse.root();

        let variables = nodes(&root, &[SCSS_VARIABLE]);
        assert_eq!(variables.len(), 2);
        assert_eq!(
            variable_name(&variables[1]).as_deref(),
            Some("sw-color-brand-primary")
        );

        let strings = nodes(&root, &[SCSS_STRING]);
        assert_eq!(strings.len(), 1);
        assert!(string_in_function(&strings[0], &["feature"]));
        assert_eq!(
            string_value(&strings[0]).as_deref(),
            Some("ACCESSIBILITY_TWEAKS")
        );
        assert_eq!(
            string_argument_in_function(&strings[0], &["feature"]),
            Some(strings[0].clone())
        );
        assert_eq!(function_name(&strings[0]).as_deref(), Some("feature"));

        // Queries from a token's parent and from unrelated nodes.
        let token = strings[0].first_token().unwrap();
        assert_eq!(
            string_value(&token.parent().unwrap()).as_deref(),
            Some("ACCESSIBILITY_TWEAKS")
        );
        assert_eq!(variable_name(&root), None);
        assert_eq!(string_value(&root), None);
        assert!(!string_in_function(&root, &["feature"]));
    }

    #[test]
    fn nested_function_uses_nearest_call() {
        let parse = parse("feature(unquote('FLAG'))");
        let string = &nodes(&parse.root(), &[SCSS_STRING])[0];
        assert!(!string_in_function(string, &["feature"]));
        assert!(string_in_function(string, &["unquote"]));
        assert_eq!(string_argument_in_function(string, &["feature"]), None);
        assert_eq!(string_value(string).as_deref(), Some("FLAG"));
    }

    #[test]
    fn unterminated_string_value() {
        let parse = parse("a: f(\"FLAG");
        let string = &nodes(&parse.root(), &[SCSS_STRING])[0];
        assert_eq!(string_value(string).as_deref(), Some("FLAG"));
    }
}
