//! Doctrine route annotation migrations (Go
//! `shopware_migration_annotations.go`).

use super::scan;
use swlsp_lsp::{Context, Problem};
use swlsp_parser_php::query as phpquery;
use swlsp_php_rewrite::{PhpDocAnnotation, find_phpdoc_annotation};
use swlsp_php_types::equal_fold;
use swlsp_syntax::SyntaxNode;

use super::{ShopwareMigrationAnalyzer, ShopwareMigrationPayload, migration_problem};

const ROUTE_ANNOTATION_DEFAULTS_CODE: &str = "shopware.migration.annotation.route_defaults";

impl ShopwareMigrationAnalyzer {
    pub(super) fn route_annotation_migration_problems(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
    ) -> Vec<Problem> {
        let mut result = Vec::new();
        for class in scan::classes(root) {
            if ctx.is_cancelled() {
                return result;
            }
            result.extend(route_annotation_problems_for_owner(&class));
            for method in phpquery::methods(&class) {
                result.extend(route_annotation_problems_for_owner(&method));
            }
        }
        result
    }
}

fn route_annotation_problems_for_owner(owner: &SyntaxNode) -> Vec<Problem> {
    let route = find_phpdoc_annotation(owner, "Route");
    let mut result = Vec::new();
    for (annotation, kind, key) in [
        ("Captcha", "captcha", "_captcha"),
        ("LoginRequired", "login-required", "_loginRequired"),
    ] {
        let Some(tag) = find_phpdoc_annotation(owner, annotation) else {
            continue;
        };
        let Some(route) = &route else {
            continue;
        };
        let (replacement, changed, safe) = add_doctrine_route_default(&route.text, key, "true");
        if !changed {
            continue;
        }
        result.push(route_annotation_problem(
            owner,
            &tag,
            kind,
            annotation,
            replacement,
            safe,
        ));
    }
    let Some(route_scope) = find_phpdoc_annotation(owner, "RouteScope") else {
        return result;
    };
    let scopes = doctrine_annotation_named_value(&route_scope.text, "scopes");
    let Some(scopes) = scopes.filter(|scopes| scopes.trim().starts_with('{')) else {
        result.push(route_annotation_problem(
            owner,
            &route_scope,
            "route-scope",
            "RouteScope",
            String::new(),
            false,
        ));
        return result;
    };
    let scopes = scopes.trim();
    let (replacement, changed, safe) = match &route {
        Some(route) => add_doctrine_route_default(&route.text, "_routeScope", scopes),
        None => (
            format!("@Route(defaults={{\"_routeScope\"={scopes}}})"),
            true,
            true,
        ),
    };
    if changed {
        result.push(route_annotation_problem(
            owner,
            &route_scope,
            "route-scope",
            "RouteScope",
            replacement,
            safe,
        ));
    }
    result
}

fn route_annotation_problem(
    owner: &SyntaxNode,
    annotation: &PhpDocAnnotation,
    kind: &str,
    remove: &str,
    replacement: String,
    safe: bool,
) -> Problem {
    migration_problem(
        ROUTE_ANNOTATION_DEFAULTS_CODE,
        annotation.range,
        owner,
        format!("Shopware 6.5: migrate @{remove} into @Route defaults"),
        &ShopwareMigrationPayload {
            rule: "route-annotation-default".into(),
            kind: kind.into(),
            safe,
            value: remove.into(),
            replacement,
            ..ShopwareMigrationPayload::default()
        },
    )
}

/// Go `addDoctrineRouteDefault`: returns the updated annotation, whether it
/// changed and whether the rewrite is safe.
pub(crate) fn add_doctrine_route_default(
    annotation: &str,
    key: &str,
    value: &str,
) -> (String, bool, bool) {
    let Some((open, close)) = doctrine_annotation_arguments(annotation) else {
        return (String::new(), true, false);
    };
    let content = &annotation[open + 1..close];
    if let Some((value_start, value_end)) = doctrine_named_value_range(content, "defaults") {
        let defaults = &content[value_start..value_end];
        let Some((brace_open, brace_close)) = doctrine_container(defaults, b'{', b'}') else {
            return (String::new(), true, false);
        };
        let inner = &defaults[brace_open + 1..brace_close];
        if doctrine_named_value_range(inner, key).is_some() {
            return (annotation.to_owned(), false, true);
        }
        let separator = if inner.trim().is_empty() { "" } else { ", " };
        let updated_defaults = format!(
            "{}{separator}\"{key}\"={value}{}",
            &defaults[..brace_close],
            &defaults[brace_close..]
        );
        let updated_content = format!(
            "{}{updated_defaults}{}",
            &content[..value_start],
            &content[value_end..]
        );
        return (
            format!(
                "{}{updated_content}{}",
                &annotation[..open + 1],
                &annotation[close..]
            ),
            true,
            true,
        );
    }
    let separator = if content.trim().is_empty() { "" } else { ", " };
    let updated_content = format!("{content}{separator}defaults={{\"{key}\"={value}}}");
    (
        format!(
            "{}{updated_content}{}",
            &annotation[..open + 1],
            &annotation[close..]
        ),
        true,
        true,
    )
}

fn doctrine_annotation_named_value(annotation: &str, name: &str) -> Option<String> {
    let (open, close) = doctrine_annotation_arguments(annotation)?;
    let content = &annotation[open + 1..close];
    let (start, end) = doctrine_named_value_range(content, name)?;
    Some(content[start..end].to_owned())
}

fn doctrine_annotation_arguments(annotation: &str) -> Option<(usize, usize)> {
    let open = annotation.find('(')?;
    let close = matching_doctrine_delimiter(annotation, open, b'(', b')')?;
    if !annotation[close + 1..].trim().is_empty() {
        return None;
    }
    Some((open, close))
}

fn is_doctrine_space(byte: u8) -> bool {
    matches!(byte, b' ' | b'\t' | b'\r' | b'\n')
}

fn doctrine_named_value_range(content: &str, requested: &str) -> Option<(usize, usize)> {
    let bytes = content.as_bytes();
    let mut start = 0;
    while start <= content.len() {
        let end = doctrine_top_level_comma(content, start);
        let segment = &content[start..end];
        if let Some(equals) = doctrine_top_level_equals(segment) {
            let name = segment[..equals].trim().trim_matches(['"', '\'']);
            if equal_fold(name, requested) {
                let mut value_start = start + equals + 1;
                while value_start < end && is_doctrine_space(bytes[value_start]) {
                    value_start += 1;
                }
                let mut value_end = end;
                while value_end > value_start && is_doctrine_space(bytes[value_end - 1]) {
                    value_end -= 1;
                }
                return Some((value_start, value_end));
            }
        }
        if end == content.len() {
            break;
        }
        start = end + 1;
    }
    None
}

/// Shared quote/depth scanner of the top-level helpers: returns the first
/// top-level index of `target` from `start`.
fn doctrine_top_level(source: &str, start: usize, target: u8) -> Option<usize> {
    let bytes = source.as_bytes();
    let mut depth = 0usize;
    let mut quote = 0u8;
    let mut escaped = false;
    for (index, &character) in bytes.iter().enumerate().skip(start) {
        if quote != 0 {
            if escaped {
                escaped = false;
            } else if character == b'\\' {
                escaped = true;
            } else if character == quote {
                quote = 0;
            }
            continue;
        }
        match character {
            b'\'' | b'"' => quote = character,
            b'(' | b'{' | b'[' => depth += 1,
            b')' | b'}' | b']' => depth = depth.saturating_sub(1),
            _ if character == target && depth == 0 => return Some(index),
            _ => {}
        }
    }
    None
}

fn doctrine_top_level_comma(source: &str, start: usize) -> usize {
    doctrine_top_level(source, start, b',').unwrap_or(source.len())
}

fn doctrine_top_level_equals(source: &str) -> Option<usize> {
    doctrine_top_level(source, 0, b'=')
}

fn doctrine_container(source: &str, open: u8, close: u8) -> Option<(usize, usize)> {
    let start = source.bytes().position(|byte| byte == open)?;
    if !source[..start].trim().is_empty() {
        return None;
    }
    let end = matching_doctrine_delimiter(source, start, open, close)?;
    if !source[end + 1..].trim().is_empty() {
        return None;
    }
    Some((start, end))
}

fn matching_doctrine_delimiter(source: &str, start: usize, open: u8, close: u8) -> Option<usize> {
    let bytes = source.as_bytes();
    let mut depth = 0i64;
    let mut quote = 0u8;
    let mut escaped = false;
    for (index, &character) in bytes.iter().enumerate().skip(start) {
        if quote != 0 {
            if escaped {
                escaped = false;
            } else if character == b'\\' {
                escaped = true;
            } else if character == quote {
                quote = 0;
            }
            continue;
        }
        if character == b'\'' || character == b'"' {
            quote = character;
            continue;
        }
        if character == open {
            depth += 1;
        } else if character == close {
            depth -= 1;
            if depth == 0 {
                return Some(index);
            }
        }
    }
    None
}
