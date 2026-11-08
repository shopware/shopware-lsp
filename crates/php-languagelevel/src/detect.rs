use swlsp_parser_php::syntax::*;
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt, SyntaxToken, TextRange};

use crate::Feature;

/// Ties a registered language feature to the syntax that uses it.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Occurrence {
    pub feature: Feature,
    pub range: TextRange,
}

/// Every version-gated syntax occurrence in source order. No version
/// filtering happens here so diagnostics, completion, code actions, and tests
/// share one classification.
pub fn detect(root: &SyntaxNode) -> Vec<Occurrence> {
    let mut result = Vec::new();
    for element in root.descendants_with_tokens() {
        let node = match element {
            NodeOrToken::Token(token) => {
                if token.kind() == TK_NULLSAFE_OBJECT_OPERATOR {
                    push(&mut result, Feature::NullsafeOperator, token.text_range());
                }
                continue;
            }
            NodeOrToken::Node(node) => node,
        };
        match node.kind() {
            PHP_ATTRIBUTE_GROUP => push(
                &mut result,
                Feature::Attributes,
                syntax_range(&node, TK_ATTRIBUTE_OPEN),
            ),
            PHP_NAMED_ARGUMENT => push(
                &mut result,
                Feature::NamedArguments,
                syntax_range(&node, TK_COLON),
            ),
            PHP_MATCH_EXPRESSION => push(
                &mut result,
                Feature::MatchExpressions,
                keyword_range(&node, "match"),
            ),
            PHP_THROW_EXPRESSION => push(
                &mut result,
                Feature::ThrowExpressions,
                keyword_range(&node, "throw"),
            ),
            PHP_ENUM_DECLARATION => push(&mut result, Feature::Enums, keyword_range(&node, "enum")),
            PHP_UNION_TYPE => {
                let feature = if contains_node(&node, PHP_INTERSECTION_TYPE) {
                    Feature::DnfTypes
                } else {
                    Feature::UnionTypes
                };
                push(&mut result, feature, node.range_trimmed_trivia());
            }
            PHP_INTERSECTION_TYPE => {
                if node.ancestor_of_kind(PHP_UNION_TYPE).is_none() {
                    push(
                        &mut result,
                        Feature::IntersectionTypes,
                        node.range_trimmed_trivia(),
                    );
                }
            }
            PHP_CLASS_DECLARATION => {
                if let Some(range) = direct_keyword_range(&node, "readonly") {
                    push(&mut result, Feature::ReadonlyClasses, range);
                }
            }
            PHP_PROPERTY_DECLARATION => {
                if let Some(range) = direct_keyword_range(&node, "readonly") {
                    push(&mut result, Feature::ReadonlyProperties, range);
                }
                if let Some(range) = asymmetric_visibility_range(&node) {
                    push(&mut result, Feature::AsymmetricVisibility, range);
                }
            }
            PHP_PARAMETER => {
                if promoted_parameter(&node) {
                    push(
                        &mut result,
                        Feature::PropertyPromotion,
                        modifier_range(&node),
                    );
                }
                if let Some(range) = direct_keyword_range(&node, "readonly") {
                    push(&mut result, Feature::ReadonlyProperties, range);
                }
            }
            PHP_CLASS_CONST_DECLARATION => {
                if has_direct_type(&node) {
                    push(
                        &mut result,
                        Feature::TypedClassConstants,
                        node.range_trimmed_trivia(),
                    );
                }
            }
            PHP_PROPERTY_HOOK_LIST => push(
                &mut result,
                Feature::PropertyHooks,
                syntax_range(&node, TK_OPEN_BRACE),
            ),
            _ => {}
        }
    }
    result
}

fn push(result: &mut Vec<Occurrence>, feature: Feature, range: TextRange) {
    if range.end() > range.start() {
        result.push(Occurrence { feature, range });
    }
}

fn syntax_range(node: &SyntaxNode, kind: Kind) -> TextRange {
    node.descendants_with_tokens()
        .find(|element| element.kind() == kind)
        .map_or_else(
            || node.range_trimmed_trivia(),
            |element| element.text_range(),
        )
}

fn keyword_range(node: &SyntaxNode, keyword: &str) -> TextRange {
    node.descendants_with_tokens()
        .filter_map(NodeOrToken::into_token)
        .find(|token| token.kind() == TK_KEYWORD && token.text().eq_ignore_ascii_case(keyword))
        .map_or_else(|| node.range_trimmed_trivia(), |token| token.text_range())
}

fn child_tokens(node: &SyntaxNode) -> impl Iterator<Item = SyntaxToken> {
    node.children_with_tokens()
        .filter_map(NodeOrToken::into_token)
}

fn direct_keyword_range(node: &SyntaxNode, keyword: &str) -> Option<TextRange> {
    child_tokens(node)
        .find(|token| token.kind() == TK_KEYWORD && token.text().eq_ignore_ascii_case(keyword))
        .map(|token| token.text_range())
}

fn contains_node(node: &SyntaxNode, kind: Kind) -> bool {
    node.descendants()
        .skip(1)
        .any(|descendant| descendant.kind() == kind)
}

fn has_direct_type(node: &SyntaxNode) -> bool {
    node.children().any(|child| {
        matches!(
            child.kind(),
            PHP_TYPE | PHP_NULLABLE_TYPE | PHP_UNION_TYPE | PHP_INTERSECTION_TYPE
        )
    })
}

fn promoted_parameter(node: &SyntaxNode) -> bool {
    let Some(list) = node
        .parent()
        .filter(|parent| parent.kind() == PHP_PARAMETER_LIST)
    else {
        return false;
    };
    let Some(method) = list
        .parent()
        .filter(|method| method.kind() == PHP_METHOD_DECLARATION)
    else {
        return false;
    };
    if !direct_name_equals(&method, "__construct") {
        return false;
    }
    ["public", "protected", "private", "readonly"]
        .iter()
        .any(|modifier| direct_keyword_range(node, modifier).is_some())
}

fn direct_name_equals(node: &SyntaxNode, name: &str) -> bool {
    node.children()
        .find(|child| child.kind() == PHP_NAME)
        .is_some_and(|child| child.text().to_string().trim().eq_ignore_ascii_case(name))
}

fn modifier_range(node: &SyntaxNode) -> TextRange {
    child_tokens(node)
        .find(|token| {
            token.kind() == TK_KEYWORD
                && matches!(
                    token.text().to_ascii_lowercase().as_str(),
                    "public" | "protected" | "private" | "readonly"
                )
        })
        .map_or_else(|| node.range_trimmed_trivia(), |token| token.text_range())
}

fn asymmetric_visibility_range(node: &SyntaxNode) -> Option<TextRange> {
    let mut state = 0;
    let mut start = None;
    for token in child_tokens(node) {
        if matches!(
            token.kind(),
            TK_WHITESPACE | TK_LINE_BREAK | TK_LINE_COMMENT | TK_BLOCK_COMMENT
        ) {
            continue;
        }
        match state {
            0 => {
                if visibility_keyword(&token) {
                    start = Some(token.text_range().start());
                    state = 1;
                }
            }
            1 => {
                if token.kind() == TK_OPEN_PAREN {
                    state = 2;
                } else if visibility_keyword(&token) {
                    start = Some(token.text_range().start());
                } else {
                    state = 0;
                }
            }
            2 => {
                state = if matches!(token.kind(), TK_IDENTIFIER | TK_KEYWORD)
                    && token.text().eq_ignore_ascii_case("set")
                {
                    3
                } else {
                    0
                };
            }
            _ => {
                if token.kind() == TK_CLOSE_PAREN {
                    return Some(TextRange::new(start?, token.text_range().end()));
                }
                state = 0;
            }
        }
    }
    None
}

fn visibility_keyword(token: &SyntaxToken) -> bool {
    token.kind() == TK_KEYWORD
        && matches!(
            token.text().to_ascii_lowercase().as_str(),
            "public" | "protected" | "private"
        )
}
