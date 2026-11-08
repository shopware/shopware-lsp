use swlsp_parser_php::query;
use swlsp_parser_php::syntax::{PHP_FUNCTION_CALL, PHP_STRING};
use swlsp_syntax::{SyntaxNode, TextRange};

use crate::model::{Occurrence, Reference, range};
use crate::parser::{
    is_docker_compose_path, is_dockerfile_path, is_dot_env_path, is_name_part,
    parse_docker_compose, parse_dockerfile, parse_dot_env, valid_name,
};

pub(crate) const ENV_PREFIX: &str = "%env(";

/// Every complete `%env(...)%` reference with a valid variable name. A
/// nested `%env(` inside an unterminated expression restarts the scan there.
pub fn references(source: &str) -> Vec<Reference> {
    let mut result = Vec::new();
    let mut cursor = 0;
    while cursor < source.len() {
        let Some(relative_start) = source[cursor..].find(ENV_PREFIX) else {
            break;
        };
        let start = cursor + relative_start;
        let expression_start = start + ENV_PREFIX.len();
        let Some(relative_end) = source[expression_start..].find(")%") else {
            break;
        };
        let expression_end = expression_start + relative_end;
        if let Some(nested) = source[expression_start..expression_end].find(ENV_PREFIX) {
            cursor = expression_start + nested;
            continue;
        }
        if let Some(reference) = parse_reference(
            &source[expression_start..expression_end],
            start,
            expression_start,
            expression_end + 2,
        ) {
            result.push(reference);
        }
        cursor = expression_end + 2;
    }
    result
}

/// The reference whose name contains `offset` (inclusive on both ends).
pub fn reference_at(source: &str, offset: u32) -> Option<Reference> {
    references(source)
        .into_iter()
        .find(|reference| contains_inclusive(reference.name_range, offset))
}

/// The reference or (for environment files) declaration whose name contains
/// `offset`.
pub fn occurrence_at(path: &str, source: &str, offset: u32) -> Option<Occurrence> {
    if let Some(reference) = reference_at(source, offset) {
        return Some(reference.into_occurrence());
    }
    let declarations = if is_dot_env_path(path) {
        parse_dot_env(source)
    } else if is_dockerfile_path(path) {
        parse_dockerfile(source)
    } else if is_docker_compose_path(path) {
        parse_docker_compose(source)
    } else {
        return None;
    };
    declarations
        .into_iter()
        .find(|declaration| contains_inclusive(declaration.name_range, offset))
}

/// The editable name segment of an unfinished `%env(` expression ending at
/// `offset`.
pub fn completion_reference_at(source: &str, offset: u32) -> Option<Reference> {
    let cursor = (offset as usize).min(source.len());
    // Go slices bytes; a cursor inside a character cannot be a name position.
    if !source.is_char_boundary(cursor) {
        return None;
    }
    let start = source[..cursor].rfind(ENV_PREFIX)?;
    let expression_start = start + ENV_PREFIX.len();
    if source[expression_start..cursor].contains(")%") {
        return None;
    }
    if let Some(close) = source[expression_start..].find(")%")
        && expression_start + close < cursor
    {
        return None;
    }
    let expression = &source[expression_start..cursor];
    let segment_start = expression.rfind(':').map_or(0, |colon| colon + 1);
    let name_part = &expression[segment_start..];
    let left_trimmed = name_part.trim_start_matches([' ', '\t']);
    let name_start = cursor - name_part.len() + (name_part.len() - left_trimmed.len());
    let name = left_trimmed.trim();
    if !name.bytes().all(is_name_part) {
        return None;
    }
    Some(Reference {
        name: name.to_owned(),
        range: range(start, cursor),
        name_range: range(name_start, cursor),
        processors: reference_processors(&expression[..segment_start]),
    })
}

/// Direct PHP environment references configured through
/// `env('resolve:APP_ENV')` or `#[Autowire(env: 'resolve:APP_ENV')]`.
/// Symfony accepts the processor chain without the `%env(...)%` wrapper in
/// these forms.
pub fn php_references(root: &SyntaxNode) -> Vec<Reference> {
    root.descendants()
        .filter(|node| node.kind() == PHP_STRING)
        .filter_map(|literal| php_reference(&literal, false))
        .collect()
}

/// A complete direct PHP environment reference under the cursor.
pub fn php_reference_at(node: &SyntaxNode, offset: u32) -> Option<Reference> {
    php_reference(node, false).filter(|reference| contains_inclusive(reference.name_range, offset))
}

/// The editable environment-name segment, including an empty string while
/// completion is being requested.
pub fn php_completion_reference_at(node: &SyntaxNode, offset: u32) -> Option<Reference> {
    php_reference(node, true).filter(|reference| contains_inclusive(reference.name_range, offset))
}

pub fn php_occurrence_at(node: &SyntaxNode, offset: u32) -> Option<Occurrence> {
    php_reference_at(node, offset).map(Reference::into_occurrence)
}

fn php_reference(node: &SyntaxNode, allow_empty: bool) -> Option<Reference> {
    let literal = query::string_at(node)?;
    if !php_reference_context(&literal) {
        return None;
    }
    let value = query::string_value(&literal);
    let segment_start = value.rfind(':').map_or(0, |colon| colon + 1);
    let name_part = &value[segment_start..];
    let left_trimmed = name_part.trim_start_matches([' ', '\t']);
    let name = left_trimmed.trim();
    if name.is_empty() {
        if !allow_empty {
            return None;
        }
    } else if !valid_name(name) {
        return None;
    }
    let content_range = query::string_content_range(&literal);
    let name_start = u32::from(content_range.start()) as usize
        + segment_start
        + (name_part.len() - left_trimmed.len());
    Some(Reference {
        name: name.to_owned(),
        range: content_range,
        name_range: range(name_start, name_start + name.len()),
        processors: reference_processors(&value[..segment_start]),
    })
}

fn php_reference_context(literal: &SyntaxNode) -> bool {
    if let Some(attribute) = query::attribute_at(literal)
        && short_php_name(&query::attribute_name(&attribute)) == "Autowire"
    {
        return query::argument_index(&attribute, literal)
            .and_then(|index| query::argument(&attribute, index))
            .is_some_and(|argument| query::argument_name(&argument) == "env");
    }
    query::call_at(literal).is_some_and(|call| {
        call.kind() == PHP_FUNCTION_CALL
            && query::string_argument_index(literal) == Some(0)
            && short_php_name(&query::call_name(&call)) == "env"
    })
}

fn short_php_name(name: &str) -> &str {
    name.rfind('\\')
        .map_or(name, |separator| &name[separator + 1..])
}

fn parse_reference(
    expression: &str,
    start: usize,
    expression_start: usize,
    end: usize,
) -> Option<Reference> {
    let segment_start = expression.rfind(':').map_or(0, |colon| colon + 1);
    let name_part = &expression[segment_start..];
    let left_trimmed = name_part.trim_start_matches([' ', '\t']);
    let name = left_trimmed.trim();
    if !valid_name(name) {
        return None;
    }
    let name_start = expression_start + segment_start + (name_part.len() - left_trimmed.len());
    Some(Reference {
        name: name.to_owned(),
        range: range(start, end),
        name_range: range(name_start, name_start + name.len()),
        processors: reference_processors(&expression[..segment_start]),
    })
}

fn reference_processors(value: &str) -> Vec<String> {
    let value = value.strip_suffix(':').unwrap_or(value);
    if value.is_empty() {
        return Vec::new();
    }
    value
        .split(':')
        .map(str::trim)
        .filter(|part| !part.is_empty())
        .map(str::to_owned)
        .collect()
}

fn contains_inclusive(range: TextRange, offset: u32) -> bool {
    offset >= u32::from(range.start()) && offset <= u32::from(range.end())
}

#[cfg(test)]
mod tests {
    use super::*;
    use swlsp_syntax::SyntaxNodeExt;

    fn slice(source: &str, range: TextRange) -> &str {
        &source[range]
    }

    #[test]
    fn references_parse_processor_chains_and_ranges() {
        let source = "\ndatabase: '%env(resolve:int:default:port:DATABASE_PORT)%'\nhost: \"%env(DATABASE_HOST)%\"\n";
        let found = references(source);
        assert_eq!(found.len(), 2);
        assert_eq!(found[0].name, "DATABASE_PORT");
        assert_eq!(found[0].processors, ["resolve", "int", "default", "port"]);
        assert_eq!(slice(source, found[0].name_range), "DATABASE_PORT");
        assert_eq!(found[1].name, "DATABASE_HOST");

        let offset = (source.find("DATABASE_PORT").unwrap() + 3) as u32;
        assert_eq!(reference_at(source, offset), Some(found[0].clone()));
    }

    #[test]
    fn completion_reference_supports_incomplete_expressions() {
        let cases: [(&str, &str, &[&str]); 3] = [
            ("value: '%env(DATAB", "DATAB", &[]),
            (
                "value: '%env(resolve:int:DATAB",
                "DATAB",
                &["resolve", "int"],
            ),
            (
                "value: '%env(resolve:int:DATABASE_URL)%'",
                "DATABASE_URL",
                &["resolve", "int"],
            ),
        ];
        for (source, name, processors) in cases {
            let offset = (source.find(name).unwrap() + name.len()) as u32;
            let reference =
                completion_reference_at(source, offset).unwrap_or_else(|| panic!("{source}"));
            assert_eq!(reference.name, name);
            assert_eq!(reference.processors, processors);
        }
    }

    #[test]
    fn reference_rejects_malformed_environment_names() {
        assert!(references("%env(string:not-valid)%").is_empty());
        assert!(completion_reference_at("value: %env(NO-PE", 17).is_none());
        // A cursor inside a multi-byte character never panics.
        assert!(completion_reference_at("%env(Ä", 6).is_none());
    }

    #[test]
    fn references_recover_after_an_incomplete_earlier_expression() {
        let found = references("# unfinished %env(BROKEN\nvalue: '%env(APP_ENV)%'");
        assert_eq!(found.len(), 1);
        assert_eq!(found[0].name, "APP_ENV");
    }

    fn php_root(source: &str) -> SyntaxNode {
        swlsp_parser_php::parse(source).tree.root()
    }

    fn php_strings(root: &SyntaxNode) -> Vec<SyntaxNode> {
        root.descendants()
            .filter(|node| node.kind() == PHP_STRING)
            .collect()
    }

    #[test]
    fn php_autowire_env_attribute_references() {
        let source = "<?php\n#[Autowire(env: 'resolve:int:DATABASE_URL')]\n#[Autowire(service: 'not-an-environment-variable')]\nclass Config {}\n";
        let root = php_root(source);
        let found = php_references(&root);
        assert_eq!(found.len(), 1);
        let reference = &found[0];
        assert_eq!(reference.name, "DATABASE_URL");
        assert_eq!(reference.processors, ["resolve", "int"]);
        assert_eq!(slice(source, reference.name_range), "DATABASE_URL");

        let offset = (source.find("DATABASE_URL").unwrap() + 2) as u32;
        let node = root.node_at(offset.into()).unwrap();
        assert_eq!(php_reference_at(&node, offset).as_ref(), Some(reference));

        let service = &php_strings(&root)[1];
        assert!(php_reference_at(service, u32::from(service.text_range().start()) + 1).is_none());
    }

    #[test]
    fn php_autowire_env_attribute_completion_supports_empty_value() {
        let source = "<?php #[Autowire(env: '')] class Config {}";
        let root = php_root(source);
        let literal = &php_strings(&root)[0];
        let offset = u32::from(query::string_content_range(literal).start());
        let reference = php_completion_reference_at(literal, offset).unwrap();
        assert!(reference.name.is_empty());
        assert_eq!(u32::from(reference.name_range.start()), offset);
        assert_eq!(u32::from(reference.name_range.end()), offset);
    }

    #[test]
    fn php_env_function_references() {
        let source = "<?php\nenv('bool:SOME_ENV_VAR');\nenv (\n    'resolve:DATABASE_URL',\n);\n$object->env('NOT_A_REFERENCE');\nother('ALSO_NOT_A_REFERENCE');\n";
        let root = php_root(source);
        let found = php_references(&root);
        assert_eq!(found.len(), 2);
        assert_eq!(found[0].name, "SOME_ENV_VAR");
        assert_eq!(found[0].processors, ["bool"]);
        assert_eq!(found[1].name, "DATABASE_URL");
        assert_eq!(found[1].processors, ["resolve"]);

        let offset = (source.find("SOME_ENV_VAR").unwrap() + 2) as u32;
        let node = root.node_at(offset.into()).unwrap();
        assert_eq!(php_reference_at(&node, offset).as_ref(), Some(&found[0]));
    }

    #[test]
    fn php_env_function_completion_supports_empty_value() {
        let source = "<?php env('')";
        let root = php_root(source);
        let literal = &php_strings(&root)[0];
        let offset = u32::from(query::string_content_range(literal).start());
        assert!(
            php_completion_reference_at(literal, offset)
                .unwrap()
                .name
                .is_empty()
        );
    }

    #[test]
    fn occurrence_at_finds_declarations_and_references() {
        let occurrence = occurrence_at("/p/.env", "APP_ENV=dev\n", 2).unwrap();
        assert_eq!(occurrence.name, "APP_ENV");
        let reference = occurrence_at("/p/services.yaml", "a: '%env(APP_ENV)%'", 10).unwrap();
        assert_eq!(reference.name, "APP_ENV");
        assert!(occurrence_at("/p/readme.md", "APP_ENV=dev", 2).is_none());
    }
}
