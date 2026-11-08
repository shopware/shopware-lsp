use swlsp_parser_php::query as php;
use swlsp_parser_php::syntax::{
    PHP_ARGUMENT, PHP_ARRAY, PHP_ARRAY_ITEM, PHP_NAMED_ARGUMENT, PHP_RETURN_STATEMENT, TK_ARROW,
};
use swlsp_parser_yaml::query as yaml;
use swlsp_parser_yaml::syntax::{
    YAML_FLOW_MAPPING, YAML_FLOW_SEQUENCE, YAML_MAPPING, YAML_PAIR, YAML_SCALAR, YAML_SEQUENCE,
};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

use crate::parser::static_config_string;

/// A top-level configuration key (`framework`, or a key below `when@env`).
#[derive(Clone, Debug)]
pub struct RootReference {
    pub name: String,
    pub node: SyntaxNode,
    pub range: TextRange,
}

/// The `resource` of an `imports` entry.
#[derive(Clone, Debug)]
pub struct ResourceReference {
    pub path: String,
    pub node: SyntaxNode,
    pub range: TextRange,
}

/// The configuration root key at `node` in a PHP or YAML config file.
pub fn root_reference_at(node: &SyntaxNode) -> Option<RootReference> {
    php_root_reference_at(node).or_else(|| yaml_root_reference_at(node))
}

/// The `imports` resource at `node` in a PHP or YAML config file.
pub fn resource_reference_at(node: &SyntaxNode) -> Option<ResourceReference> {
    php_resource_reference_at(node).or_else(|| yaml_resource_reference_at(node))
}

fn php_root_reference_at(node: &SyntaxNode) -> Option<RootReference> {
    let literal = php::string_at(node)?;
    let item = php::array_item_at(&literal)?;
    if !same_node(php::array_item_key(&item).as_ref(), Some(&literal)) {
        return None;
    }
    let array = parent_array(&item)?;
    if !is_config_entries_array(&array) {
        return None;
    }
    let name = static_config_string(&literal)?;
    if name.is_empty() || name.starts_with("when@") || name == "imports" {
        return None;
    }
    Some(RootReference {
        name,
        range: php::string_content_range(&literal),
        node: literal,
    })
}

fn php_resource_reference_at(node: &SyntaxNode) -> Option<ResourceReference> {
    let literal = php::string_at(node)?;
    let resource_item = php::array_item_at(&literal)?;
    if !same_node(
        php::array_item_value(&resource_item).as_ref(),
        Some(&literal),
    ) || string_node_value(php::array_item_key(&resource_item).as_ref()) != "resource"
    {
        return None;
    }
    let entry_array = parent_array(&resource_item);
    let entry_item = entry_array.as_ref().and_then(parent_array_item)?;
    if array_item_has_direct_arrow(&entry_item) {
        return None;
    }
    let imports_array = parent_array(&entry_item);
    let imports_item = imports_array.as_ref().and_then(parent_array_item)?;
    if string_node_value(php::array_item_key(&imports_item).as_ref()) != "imports"
        || !same_node(
            php::array_item_value(&imports_item).as_ref(),
            imports_array.as_ref(),
        )
    {
        return None;
    }
    let config_array = parent_array(&imports_item)?;
    if !is_config_entries_array(&config_array) {
        return None;
    }
    let path = static_config_string(&literal)?;
    if path.is_empty() {
        return None;
    }
    Some(ResourceReference {
        path,
        range: php::string_content_range(&literal),
        node: literal,
    })
}

fn yaml_root_reference_at(node: &SyntaxNode) -> Option<RootReference> {
    let scalar = yaml_scalar_at(node)?;
    let pair = yaml::ancestor_pair(&scalar)?;
    if !same_node(yaml::pair_key(&pair).as_ref(), Some(&scalar)) {
        return None;
    }
    let path = yaml::pair_path(&scalar);
    if path.len() != 1 && (path.len() != 2 || !path[0].starts_with("when@")) {
        return None;
    }
    let name = yaml::scalar_value(&scalar);
    if name.is_empty() || name == "imports" || name.starts_with("when@") {
        return None;
    }
    Some(RootReference {
        name,
        range: yaml_scalar_content_range(&scalar),
        node: scalar,
    })
}

fn yaml_resource_reference_at(node: &SyntaxNode) -> Option<ResourceReference> {
    let scalar = yaml_scalar_at(node)?;
    let pair = yaml::ancestor_pair(&scalar)?;
    let key = yaml::pair_key(&pair);
    if !same_node(yaml::pair_value(&pair).as_ref(), Some(&scalar))
        || key.as_ref().map(yaml::scalar_value).as_deref() != Some("resource")
    {
        return None;
    }
    let path = yaml::pair_path(&scalar);
    let path: Vec<&str> = path.iter().map(String::as_str).collect();
    let accepted = match path.as_slice() {
        ["imports", "resource"] => true,
        [when, "imports", "resource"] => when.starts_with("when@"),
        _ => false,
    };
    if !accepted {
        return None;
    }
    let value = yaml::scalar_value(&scalar);
    if value.is_empty() || value.contains(['\r', '\n']) {
        return None;
    }
    Some(ResourceReference {
        path: value,
        range: yaml_scalar_content_range(&scalar),
        node: scalar,
    })
}

fn yaml_scalar_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    for current in node.ancestors() {
        match current.kind() {
            YAML_SCALAR => return Some(current),
            YAML_PAIR | YAML_MAPPING | YAML_FLOW_MAPPING | YAML_SEQUENCE | YAML_FLOW_SEQUENCE => {
                return None;
            }
            _ => {}
        }
    }
    None
}

fn yaml_scalar_content_range(node: &SyntaxNode) -> TextRange {
    let range = node.range_trimmed_trivia();
    let text = node.text().to_string();
    let text = text.trim().as_bytes();
    if text.len() >= 2
        && matches!(text[0], b'\'' | b'"')
        && text[text.len() - 1] == text[0]
        && range.end() > range.start() + TextSize::from(1)
    {
        return TextRange::new(
            range.start() + TextSize::from(1),
            range.end() - TextSize::from(1),
        );
    }
    range
}

fn is_config_entries_array(array: &SyntaxNode) -> bool {
    if is_accepted_config_root_array(Some(array)) {
        return true;
    }
    let Some(when_item) = parent_array_item(array) else {
        return false;
    };
    same_node(php::array_item_value(&when_item).as_ref(), Some(array))
        && string_node_value(php::array_item_key(&when_item).as_ref()).starts_with("when@")
        && is_accepted_config_root_array(parent_array(&when_item).as_ref())
}

/// A `return [...]` outside a class, or the first argument of `config()`.
fn is_accepted_config_root_array(array: Option<&SyntaxNode>) -> bool {
    let Some(array) = array.filter(|array| array.kind() == PHP_ARRAY) else {
        return false;
    };
    if let Some(parent) = array.parent()
        && parent.kind() == PHP_RETURN_STATEMENT
        && php::class_at(&parent).is_none()
    {
        return true;
    }
    php::call_at(array).is_some_and(|call| {
        php::call_method_name(&call).eq_ignore_ascii_case("config")
            && same_node(php::argument_expression(&call, 0).as_ref(), Some(array))
    })
}

fn parent_array(node: &SyntaxNode) -> Option<SyntaxNode> {
    for parent in node.ancestors().skip(1) {
        match parent.kind() {
            PHP_ARRAY => return Some(parent),
            PHP_ARRAY_ITEM => {}
            _ => break,
        }
    }
    None
}

fn parent_array_item(node: &SyntaxNode) -> Option<SyntaxNode> {
    for parent in node.ancestors().skip(1) {
        match parent.kind() {
            PHP_ARRAY_ITEM => return Some(parent),
            PHP_ARGUMENT | PHP_NAMED_ARGUMENT => {}
            _ => break,
        }
    }
    None
}

fn same_node(left: Option<&SyntaxNode>, right: Option<&SyntaxNode>) -> bool {
    match (left, right) {
        (Some(left), Some(right)) => left == right || left.text_range() == right.text_range(),
        _ => false,
    }
}

fn string_node_value(node: Option<&SyntaxNode>) -> String {
    node.and_then(static_config_string).unwrap_or_default()
}

fn array_item_has_direct_arrow(item: &SyntaxNode) -> bool {
    item.kind() == PHP_ARRAY_ITEM && item.child_token_of_kind(TK_ARROW).is_some()
}
