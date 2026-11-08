//! Discovers Composer `ClassLoader::addPsr4` mappings installed by a PHPUnit
//! bootstrap script. Projects use these for isolated tool dependencies (for
//! example a package under `vendor-bin/`) that are absent from the root lock.

use std::collections::HashMap;
use std::path::{Path, PathBuf};

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::{
    PHP_ASSIGNMENT_EXPRESSION, PHP_BINARY_EXPRESSION, PHP_FUNCTION_CALL, PHP_NAME,
    PHP_PARENTHESIZED, PHP_STRING, PHP_VARIABLE, TK_OPERATOR,
};
use swlsp_syntax::SyntaxNode;
use swlsp_syntax::rowan::NodeOrToken;

use crate::{Model, clean, normalize_namespace};

pub(crate) fn add_phpunit_bootstrap_autoloads(model: &mut Model, root: &Path) {
    for config_name in ["phpunit.xml", "phpunit.xml.dist"] {
        let config_path = root.join(config_name);
        let Ok(content) = std::fs::read_to_string(&config_path) else {
            continue;
        };
        let Some(bootstrap) = root_attribute(&content, "bootstrap") else {
            continue;
        };
        if bootstrap.trim().is_empty() {
            continue;
        }
        let mut path = PathBuf::from(&bootstrap);
        if !path.is_absolute() {
            path = config_path.parent().unwrap_or(root).join(path);
        }
        add_bootstrap_autoloads(model, &clean(&path));
    }
}

fn add_bootstrap_autoloads(model: &mut Model, path: &Path) {
    let Ok(content) = std::fs::read(path) else {
        return;
    };
    let source = String::from_utf8_lossy(&content);
    let root = swlsp_parser_php::root(&source);
    let mut values: HashMap<String, String> = HashMap::new();
    for assignment in query::nodes(&root, &[PHP_ASSIGNMENT_EXPRESSION]) {
        let nodes: Vec<SyntaxNode> = assignment.children().collect();
        if nodes.len() < 2 || nodes[0].kind() != PHP_VARIABLE {
            continue;
        }
        let name = query::variable_key(&nodes[0]);
        if let Some(value) = string_value(nodes.last(), path, &values)
            && !name.is_empty()
        {
            values.insert(name, value);
        }
    }
    for call in query::calls(&root, &[]) {
        if !query::call_method_name(&call).eq_ignore_ascii_case("addPsr4") {
            continue;
        }
        let namespace = string_value(query::argument_expression(&call, 0).as_ref(), path, &values);
        let source_root =
            string_value(query::argument_expression(&call, 1).as_ref(), path, &values);
        let (Some(namespace), Some(source_root)) = (namespace, source_root) else {
            continue;
        };
        if namespace.is_empty() || source_root.is_empty() {
            continue;
        }
        let mut source_root = PathBuf::from(source_root);
        if !source_root.is_absolute() {
            source_root = path.parent().unwrap_or(Path::new(".")).join(source_root);
        }
        let source_root = clean(&source_root);
        if !source_root.is_dir() {
            continue;
        }
        let roots = model
            .psr4
            .entry(normalize_namespace(&namespace))
            .or_default();
        if !roots.contains(&source_root) {
            roots.push(source_root);
        }
    }
}

fn string_value(
    node: Option<&SyntaxNode>,
    bootstrap: &Path,
    variables: &HashMap<String, String>,
) -> Option<String> {
    let node = node?;
    match node.kind() {
        PHP_STRING => Some(php_string_value(node)),
        PHP_VARIABLE => variables.get(&query::variable_key(node)).cloned(),
        PHP_NAME => {
            if node
                .text()
                .to_string()
                .trim()
                .eq_ignore_ascii_case("__DIR__")
            {
                Some(go_dir(&bootstrap.to_string_lossy()))
            } else {
                None
            }
        }
        PHP_PARENTHESIZED => string_value(node.children().next().as_ref(), bootstrap, variables),
        PHP_BINARY_EXPRESSION => {
            if direct_operator(node) != "." {
                return None;
            }
            let nodes: Vec<SyntaxNode> = node.children().collect();
            if nodes.len() < 2 {
                return None;
            }
            let left = string_value(nodes.first(), bootstrap, variables);
            let right = string_value(nodes.last(), bootstrap, variables);
            Some(left? + &right?)
        }
        PHP_FUNCTION_CALL => {
            let name = query::call_method_name(node);
            if !name
                .trim_start_matches('\\')
                .eq_ignore_ascii_case("dirname")
            {
                return None;
            }
            let mut value = string_value(
                query::argument_expression(node, 0).as_ref(),
                bootstrap,
                variables,
            )?;
            let mut levels = 1usize;
            if let Some(argument) = query::argument_expression(node, 1) {
                let parsed: i64 = argument.text().to_string().trim().parse().ok()?;
                if parsed < 1 {
                    return None;
                }
                levels = parsed as usize;
            }
            for _ in 0..levels {
                value = go_dir(&value);
            }
            Some(value)
        }
        _ => None,
    }
}

/// Go `filepath.Dir` on a string path.
fn go_dir(path: &str) -> String {
    let separator = path.rfind('/').map_or(0, |index| index + 1);
    let directory = clean(Path::new(&path[..separator]));
    let directory = directory.to_string_lossy().into_owned();
    if directory.is_empty() {
        ".".to_owned()
    } else {
        directory
    }
}

fn php_string_value(node: &SyntaxNode) -> String {
    let value = query::string_value(node);
    let text = node.text().to_string();
    let text = text.trim();
    if text.len() < 2 {
        return value;
    }
    match text.as_bytes()[0] {
        b'\'' => unescape(&value, '\''),
        b'"' => unescape(&value, '"'),
        _ => value,
    }
}

/// Go `strings.NewReplacer("\\\\", "\\", "\\"+quote, quote)`: a single
/// left-to-right pass replacing `\\` and `\<quote>`.
fn unescape(value: &str, quote: char) -> String {
    let mut result = String::with_capacity(value.len());
    let mut chars = value.chars().peekable();
    while let Some(current) = chars.next() {
        if current == '\\'
            && let Some(&next) = chars.peek()
            && (next == '\\' || next == quote)
        {
            result.push(next);
            chars.next();
            continue;
        }
        result.push(current);
    }
    result
}

fn direct_operator(node: &SyntaxNode) -> String {
    node.children_with_tokens()
        .filter_map(NodeOrToken::into_token)
        .find(|token| token.kind() == TK_OPERATOR)
        .map_or_else(String::new, |token| token.text().trim().to_owned())
}

/// Reads one attribute of the XML root element, decoding the predefined
/// entities. Prolog, comments, processing instructions, and doctype
/// declarations before the root element are skipped. Malformed input yields
/// `None`.
fn root_attribute(content: &str, attribute: &str) -> Option<String> {
    let mut rest = content.trim_start_matches('\u{feff}');
    loop {
        let start = rest.find('<')?;
        rest = &rest[start..];
        if let Some(after) = rest.strip_prefix("<!--") {
            rest = &after[after.find("-->")? + 3..];
        } else if rest.starts_with("<?") {
            rest = &rest[rest.find("?>")? + 2..];
        } else if rest.starts_with("<!") {
            rest = &rest[rest.find('>')? + 1..];
        } else {
            break;
        }
    }
    let tag_end = rest.find('>')?;
    let tag = rest[1..tag_end].trim_end_matches('/');
    let mut attributes = tag.trim_start_matches(|character: char| !character.is_whitespace());
    loop {
        attributes = attributes.trim_start();
        if attributes.is_empty() {
            return None;
        }
        let equals = attributes.find('=')?;
        let name = attributes[..equals].trim();
        let after = attributes[equals + 1..].trim_start();
        let quote = after.chars().next()?;
        if quote != '"' && quote != '\'' {
            return None;
        }
        let value_end = after[1..].find(quote)? + 1;
        let value = &after[1..value_end];
        let local = name.rsplit(':').next().unwrap_or(name);
        if local == attribute {
            return Some(decode_entities(value));
        }
        attributes = &after[value_end + 1..];
    }
}

fn decode_entities(value: &str) -> String {
    if !value.contains('&') {
        return value.to_owned();
    }
    value
        .replace("&lt;", "<")
        .replace("&gt;", ">")
        .replace("&quot;", "\"")
        .replace("&apos;", "'")
        .replace("&amp;", "&")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn reads_root_attribute() {
        assert_eq!(
            root_attribute(
                "<?xml version=\"1.0\"?>\n<!-- c -->\n<phpunit colors='true' bootstrap=\"a&amp;b.php\"/>",
                "bootstrap"
            ),
            Some("a&b.php".to_owned())
        );
        assert_eq!(root_attribute("<phpunit/>", "bootstrap"), None);
    }

    #[test]
    fn go_dir_matches_filepath_dir() {
        assert_eq!(go_dir("/a/b/c.php"), "/a/b");
        assert_eq!(go_dir("/a"), "/");
        assert_eq!(go_dir("file"), ".");
    }
}
