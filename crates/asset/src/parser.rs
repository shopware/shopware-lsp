use swlsp_parser_js::query as js;
use swlsp_parser_json::syntax::{
    JSON_ARRAY, JSON_BOOLEAN, JSON_NULL_VALUE, JSON_NUMBER, JSON_OBJECT, JSON_PAIR, JSON_STRING,
};
use swlsp_parser_php::query as php;
use swlsp_parser_php::syntax::{PHP_ARRAY, PHP_RETURN_STATEMENT};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize, Tree};

use crate::model::{Kind, Resource, normalize_name};
use crate::paths::{base, clean, dir, join};
use crate::reference::php_static_string_value;

pub(crate) fn parse_manifest(path: &str, tree: Option<&Tree>) -> Vec<Resource> {
    let Some(object) = tree.and_then(|tree| json_root_value(&tree.root())) else {
        return Vec::new();
    };
    if object.kind() != JSON_OBJECT {
        return Vec::new();
    }
    let mut result = Vec::new();
    for pair in json_pairs(&object) {
        let key = json_pair_key(&pair);
        let name = normalize_name(&json_string_value(key.as_ref()));
        if name.is_empty() {
            continue;
        }
        let target = json_string_value(json_pair_value(&pair).as_ref());
        result.push(Resource {
            name,
            file: path.to_owned(),
            target: resolve_public_target(path, &target),
            kind: Kind::ManifestAsset,
            range: json_string_range(key.as_ref()),
            ..Resource::default()
        });
    }
    result
}

pub(crate) fn parse_entrypoints(path: &str, tree: Option<&Tree>) -> Vec<Resource> {
    let Some(root) = tree.and_then(|tree| json_root_value(&tree.root())) else {
        return Vec::new();
    };
    let Some(entrypoints) = json_property(&root, "entrypoints") else {
        return Vec::new();
    };
    if entrypoints.kind() != JSON_OBJECT {
        return Vec::new();
    }
    let mut result = Vec::new();
    for pair in json_pairs(&entrypoints) {
        let key = json_pair_key(&pair);
        let name = json_string_value(key.as_ref());
        if name.is_empty() {
            continue;
        }
        result.push(Resource {
            name,
            file: path.to_owned(),
            target: entrypoint_target(path, json_pair_value(&pair).as_ref()),
            kind: Kind::EncoreEntry,
            range: json_string_range(key.as_ref()),
            ..Resource::default()
        });
    }
    result
}

fn entrypoint_target(path: &str, value: Option<&SyntaxNode>) -> String {
    let Some(value) = value.filter(|value| value.kind() == JSON_OBJECT) else {
        return String::new();
    };
    for property in ["js", "css"] {
        let Some(values) =
            json_property(value, property).filter(|values| values.kind() == JSON_ARRAY)
        else {
            continue;
        };
        if let Some(child) = values.children().find(|child| child.kind() == JSON_STRING) {
            return resolve_public_target(path, &json_string_value(Some(&child)));
        }
    }
    String::new()
}

pub(crate) fn parse_webpack_config(path: &str, tree: Option<&Tree>) -> Vec<Resource> {
    let Some(tree) = tree else {
        return Vec::new();
    };
    let mut result = Vec::new();
    for call in js::iter_calls(&tree.root(), &[]) {
        let lower = js::call_name(&call).unwrap_or_default().to_lowercase();
        if !lower.ends_with("addentry")
            && !lower.ends_with("addstyleentry")
            && !lower.ends_with("addsassentry")
        {
            continue;
        }
        let entry = js::string_argument(&call, 0);
        let entry_name = entry.as_ref().map(js::string_value).unwrap_or_default();
        if entry_name.is_empty() {
            continue;
        }
        let target = js::string_argument(&call, 1).map_or_else(String::new, |value| {
            resolve_relative_target(&dir(path), &js::string_value(&value))
        });
        result.push(Resource {
            name: entry_name,
            file: path.to_owned(),
            target,
            kind: Kind::EncoreEntry,
            range: javascript_string_range(entry.as_ref()),
            ..Resource::default()
        });
    }
    result
}

pub(crate) fn parse_importmap(path: &str, tree: Option<&Tree>) -> Vec<Resource> {
    let Some(tree) = tree else {
        return Vec::new();
    };
    let source_type = base(path).to_lowercase();
    let Some(mapping) = php::arrays(&tree.root()).into_iter().find(|array| {
        array
            .parent()
            .is_some_and(|parent| parent.kind() == PHP_RETURN_STATEMENT)
    }) else {
        return Vec::new();
    };
    let mut result = Vec::new();
    for item in php::array_items(&mapping) {
        let key = php::array_item_key(&item);
        let name = php_static_string_value(key.as_ref());
        let Some(config) = php::array_item_value(&item).filter(|config| config.kind() == PHP_ARRAY)
        else {
            continue;
        };
        if name.is_empty() {
            continue;
        }
        let module_type = php_array_string(&config, "type");
        let configured_path = php_array_string(&config, "path");
        result.push(Resource {
            target: importmap_target(path, &source_type, &name, &configured_path, &module_type),
            name,
            file: path.to_owned(),
            kind: Kind::ImportmapModule,
            range: php_string_content_range(key.as_ref()),
            url: php_array_string(&config, "url"),
            version: php_array_string(&config, "version"),
            module_type,
            entrypoint: php_array_bool(&config, "entrypoint"),
        });
    }
    result
}

fn php_array_string(array: &SyntaxNode, name: &str) -> String {
    php::array_items(array)
        .iter()
        .find(|item| php_static_string_value(php::array_item_key(item).as_ref()) == name)
        .map(|item| php_static_string_value(php::array_item_value(item).as_ref()))
        .unwrap_or_default()
}

fn php_array_bool(array: &SyntaxNode, name: &str) -> bool {
    php::array_items(array)
        .iter()
        .find(|item| php_static_string_value(php::array_item_key(item).as_ref()) == name)
        .is_some_and(|item| {
            php::array_item_value(item)
                .is_some_and(|value| value.text().to_string().trim().eq_ignore_ascii_case("true"))
        })
}

fn importmap_target(
    mapping_path: &str,
    source_type: &str,
    name: &str,
    configured_path: &str,
    module_type: &str,
) -> String {
    let mut base_dir = dir(mapping_path);
    if !configured_path.is_empty() {
        return resolve_relative_target(&base_dir, configured_path);
    }
    if source_type == "importmap.php" {
        base_dir = join(&[base_dir.as_str(), "assets", "vendor"]);
    }
    let name = name.trim_start_matches('/');
    if name.is_empty() {
        return String::new();
    }
    if module_type.eq_ignore_ascii_case("css")
        || source_type == "installed.php" && name.to_lowercase().ends_with(".css")
    {
        return resolve_relative_target(&base_dir, name);
    }
    let parts: Vec<&str> = name.split('/').collect();
    if name.starts_with('@') && parts.len() == 2 {
        resolve_relative_target(&base_dir, &join(&[name, &format!("{}.index.js", parts[1])]))
    } else if parts.len() > 1 {
        resolve_relative_target(&base_dir, &format!("{name}.js"))
    } else {
        resolve_relative_target(&base_dir, &join(&[name, &format!("{name}.index.js")]))
    }
}

/// A manifest target; a root-relative URL resolves against the enclosing
/// `public`/`web` directory of the metadata file.
pub(crate) fn resolve_public_target(metadata_path: &str, target: &str) -> String {
    let target = target.replace('\\', "/");
    let target = target.trim();
    if target.is_empty() {
        return String::new();
    }
    if target.starts_with('/') {
        for marker in ["/public/", "/web/"] {
            if let Some(position) = metadata_path.find(marker) {
                let root = &metadata_path[..position + marker.len() - 1];
                return clean(&format!("{root}/{}", target.trim_start_matches('/')));
            }
        }
    }
    resolve_relative_target(&dir(metadata_path), target)
}

pub(crate) fn resolve_relative_target(base_dir: &str, target: &str) -> String {
    let target = target.replace('\\', "/");
    let target = target.trim();
    let target = target.strip_prefix("./").unwrap_or(target);
    if target.is_empty() || target.contains("://") {
        return String::new();
    }
    join(&[base_dir, target])
}

pub(crate) fn php_string_content_range(node: Option<&SyntaxNode>) -> TextRange {
    let Some(node) = node else {
        return TextRange::default();
    };
    let range = node.range_trimmed_trivia();
    let text = node.text().to_string();
    let text = text.trim().as_bytes();
    if text.len() >= 2 && matches!(text[0], b'\'' | b'"') && text[text.len() - 1] == text[0] {
        return shrink(range);
    }
    range
}

pub(crate) fn json_string_range(node: Option<&SyntaxNode>) -> TextRange {
    let Some(node) = node else {
        return TextRange::default();
    };
    let range = node.range_trimmed_trivia();
    let text = node.text().to_string();
    let text = text.trim().as_bytes();
    let start = range.start() + TextSize::from(u32::from(text.first() == Some(&b'"')));
    let end =
        range.end() - TextSize::from(u32::from(text.len() >= 2 && text[text.len() - 1] == b'"'));
    TextRange::new(start, end.max(start))
}

pub(crate) fn javascript_string_range(node: Option<&SyntaxNode>) -> TextRange {
    let Some(node) = node else {
        return TextRange::default();
    };
    let range = node.range_trimmed_trivia();
    let text = node.text().to_string();
    let text = text.trim().as_bytes();
    let quote = |byte: Option<&u8>| matches!(byte, Some(b'\'' | b'"' | b'`'));
    let start = range.start() + TextSize::from(u32::from(quote(text.first())));
    let end = range.end() - TextSize::from(u32::from(text.len() >= 2 && quote(text.last())));
    TextRange::new(start, end.max(start))
}

/// Shrinks a quoted range by one byte on each side.
pub(crate) fn shrink(range: TextRange) -> TextRange {
    TextRange::new(
        range.start() + TextSize::from(1),
        range.end() - TextSize::from(1),
    )
}

// -- JSON queries ----------------------------

fn json_is_value(node: &SyntaxNode) -> bool {
    matches!(
        node.kind(),
        JSON_OBJECT | JSON_ARRAY | JSON_STRING | JSON_NUMBER | JSON_BOOLEAN | JSON_NULL_VALUE
    )
}

fn json_root_value(root: &SyntaxNode) -> Option<SyntaxNode> {
    root.children().find(json_is_value)
}

fn json_pairs(object: &SyntaxNode) -> Vec<SyntaxNode> {
    if object.kind() != JSON_OBJECT {
        return Vec::new();
    }
    object
        .children()
        .filter(|child| child.kind() == JSON_PAIR)
        .collect()
}

fn json_pair_key(pair: &SyntaxNode) -> Option<SyntaxNode> {
    pair.children().find(|child| child.kind() == JSON_STRING)
}

fn json_pair_value(pair: &SyntaxNode) -> Option<SyntaxNode> {
    let mut children = pair.children();
    children.find(|child| child.kind() == JSON_STRING)?;
    children.find(json_is_value)
}

fn json_property(object: &SyntaxNode, name: &str) -> Option<SyntaxNode> {
    json_pairs(object)
        .iter()
        .find(|pair| json_string_value(json_pair_key(pair).as_ref()) == name)
        .and_then(json_pair_value)
}

/// String content without the surrounding quotes; escapes are kept.
fn json_string_value(node: Option<&SyntaxNode>) -> String {
    let Some(node) = node.filter(|node| node.kind() == JSON_STRING) else {
        return String::new();
    };
    let text = node.text().to_string();
    let text = text.trim();
    let text = text.strip_prefix('"').unwrap_or(text);
    text.strip_suffix('"').unwrap_or(text).to_owned()
}
