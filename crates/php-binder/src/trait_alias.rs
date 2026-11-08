//! `use Trait { a as protected b; }` adaptations.

use swlsp_parser_php::syntax::*;
use swlsp_php_resolver::NameContext;
use swlsp_php_semantic::{Str, TraitAlias, Visibility};
use swlsp_php_types::{equal_fold, go_to_lower};
use swlsp_syntax::SyntaxNode;
use swlsp_syntax::rowan::NodeOrToken;

pub(crate) fn bind_trait_aliases(
    node: &SyntaxNode,
    context: &NameContext,
    used_traits: &[String],
) -> Vec<TraitAlias> {
    let mut inside_adaptations = false;
    let mut statement: Vec<String> = Vec::new();
    let mut result = Vec::new();
    let flush = |statement: &mut Vec<String>, result: &mut Vec<TraitAlias>| {
        if let Some(alias) = parse_trait_alias(statement, context, used_traits) {
            result.push(alias);
        }
        statement.clear();
    };
    for child in node.children_with_tokens() {
        let NodeOrToken::Token(token) = child else {
            continue;
        };
        if token.kind().is_trivia() {
            continue;
        }
        match token.kind() {
            TK_OPEN_BRACE => {
                inside_adaptations = true;
                continue;
            }
            TK_CLOSE_BRACE => {
                flush(&mut statement, &mut result);
                inside_adaptations = false;
                continue;
            }
            TK_SEMICOLON => {
                if inside_adaptations {
                    flush(&mut statement, &mut result);
                }
                continue;
            }
            _ => {}
        }
        if inside_adaptations {
            statement.push(token.text().to_owned());
        }
    }
    result
}

fn parse_trait_alias(
    parts: &[String],
    context: &NameContext,
    used_traits: &[String],
) -> Option<TraitAlias> {
    let as_index = parts.iter().position(|part| equal_fold(part, "as"))?;
    if as_index == 0 || as_index + 1 >= parts.len() {
        return None;
    }
    let before = parts[..as_index].concat();
    let mut method = before.as_str();
    let mut trait_name = String::new();
    if let Some(separator) = before.rfind("::") {
        trait_name = context.resolve_class(&before[..separator]);
        method = &before[separator + 2..];
    } else if used_traits.len() == 1 {
        trait_name = used_traits[0].clone();
    }
    let method = method.trim();
    if trait_name.is_empty() || method.is_empty() {
        return None;
    }
    let mut alias = TraitAlias {
        trait_name: Str::from(trait_name),
        method: Str::from(method),
        alias: Str::from(method),
        ..TraitAlias::default()
    };
    for part in &parts[as_index + 1..] {
        match go_to_lower(part).as_str() {
            "public" => {
                alias.visibility = Visibility::Public;
                alias.has_visibility = true;
            }
            "protected" => {
                alias.visibility = Visibility::Protected;
                alias.has_visibility = true;
            }
            "private" => {
                alias.visibility = Visibility::Private;
                alias.has_visibility = true;
            }
            _ => alias.alias = Str::from(part.trim()),
        }
    }
    if alias.alias.is_empty() {
        alias.alias = Str::from(method);
    }
    Some(alias)
}
