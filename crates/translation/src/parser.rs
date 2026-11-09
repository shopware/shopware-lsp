//! Catalogue resource parsing.

use swlsp_indexer::ParsedFile;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{PHP_ARRAY, PHP_RETURN_STATEMENT};
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_syntax::SyntaxNode;
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::{Message, base_name, lower_extension, new_message, normalize_domain};

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub(crate) struct ResourceMetadata {
    pub domain: String,
    pub locale: String,
    pub format: String,
    pub compiled: bool,
}

pub(crate) fn catalogue_metadata(path: &str) -> Option<ResourceMetadata> {
    let extension = lower_extension(path);
    if !matches!(
        extension.as_str(),
        ".php" | ".yaml" | ".yml" | ".xml" | ".xlf" | ".xliff"
    ) {
        return None;
    }
    let parts: Vec<&str> = base_name(path).split('.').collect();
    if parts.len() < 3 {
        return None;
    }
    let domain = parts[..parts.len() - 2].join(".");
    let locale = parts[parts.len() - 2];
    if domain.is_empty() || locale.is_empty() {
        return None;
    }
    let mut metadata = ResourceMetadata {
        domain: normalize_domain(&domain).to_owned(),
        locale: locale.to_owned(),
        format: extension[1..].to_owned(),
        compiled: false,
    };
    if eq_ignore_case(&domain, "catalogue") && extension == ".php" {
        metadata.compiled = true;
        return Some(metadata);
    }

    let in_translation_directory = lowercase(path).contains("/translations/");
    let domain_lower = lowercase(&metadata.domain);
    let is_conventional_domain = domain_lower == "messages" || domain_lower == "validators";
    let is_xliff = extension == ".xlf" || extension == ".xliff";
    if !in_translation_directory
        && !is_conventional_domain
        && !is_xliff
        && !looks_like_locale(locale)
    {
        return None;
    }
    Some(metadata)
}

/// Whether the first two bytes, read as Latin-1 characters, are lowercase
/// letters.
fn looks_like_locale(value: &str) -> bool {
    let bytes = value.as_bytes();
    if bytes.len() < 2 {
        return false;
    }
    let is_lower = |byte: u8| {
        // The ordinal indicators `ª` and `º` are letters without case.
        byte != 0xAA && byte != 0xBA && char::from(byte).is_lowercase()
    };
    is_lower(bytes[0]) && is_lower(bytes[1])
}

pub(crate) fn parse_yaml_resource(file: &ParsedFile, metadata: &ResourceMetadata) -> Vec<Message> {
    let Some(tree) = file.syntax_tree() else {
        return Vec::new();
    };
    let Some(root) = yamlquery::root_value(&tree.root()) else {
        return Vec::new();
    };
    if !yamlquery::is_mapping(&root) {
        return Vec::new();
    }
    let mut result = Vec::new();
    collect_yaml_messages(&root, &[], file, metadata, &mut result);
    result
}

fn collect_yaml_messages(
    mapping: &SyntaxNode,
    path: &[String],
    file: &ParsedFile,
    metadata: &ResourceMetadata,
    result: &mut Vec<Message>,
) {
    for pair in yamlquery::pairs(mapping) {
        let key_node = yamlquery::pair_key(&pair);
        let value = yamlquery::pair_value(&pair);
        let key = key_node
            .as_ref()
            .map(yamlquery::scalar_value)
            .unwrap_or_default();
        let key = key.trim();
        if key.is_empty() {
            continue;
        }
        let mut next_path = path.to_vec();
        next_path.push(key.to_owned());
        if let Some(value) = value.as_ref().filter(|value| yamlquery::is_mapping(value)) {
            collect_yaml_messages(value, &next_path, file, metadata, result);
            continue;
        }
        let mut text = value
            .as_ref()
            .map(yamlquery::scalar_value)
            .unwrap_or_default();
        if text.is_empty() {
            text = value.as_ref().map(yamlquery::raw_text).unwrap_or_default();
        }
        result.push(new_message(
            &metadata.domain,
            next_path.join("."),
            text,
            &metadata.locale,
            file.path(),
            &metadata.format,
            key_node.as_ref(),
            file.line_index(),
        ));
    }
}

pub(crate) fn parse_xml_resource(file: &ParsedFile, metadata: &ResourceMetadata) -> Vec<Message> {
    let Some(tree) = file.syntax_tree() else {
        return Vec::new();
    };
    let mut result = Vec::new();
    for unit in xmlquery::elements(&tree.root(), &["trans-unit", "unit"]) {
        let source = translation_source_element(&unit);
        let mut key_node = source.clone();
        let mut key = source
            .as_ref()
            .map(xmlquery::text_content)
            .unwrap_or_default()
            .trim()
            .to_owned();
        if let Some(resname) = xmlquery::attribute(&unit, "resname") {
            key = xmlquery::attribute_value(&resname)
                .unwrap_or_default()
                .trim()
                .to_owned();
            key_node = Some(resname);
        }
        if key.is_empty() {
            continue;
        }
        let text = translation_target_element(&unit)
            .as_ref()
            .map(xmlquery::text_content)
            .unwrap_or_default()
            .trim()
            .to_owned();
        result.push(new_message(
            &metadata.domain,
            key,
            text,
            &metadata.locale,
            file.path(),
            &metadata.format,
            key_node.as_ref(),
            file.line_index(),
        ));
    }
    result
}

fn translation_child(unit: &SyntaxNode, name: &str) -> Option<SyntaxNode> {
    if let Some(child) = xmlquery::child_element(unit, &[name]) {
        return Some(child);
    }
    if let Some(segment) = xmlquery::child_element(unit, &["segment"]) {
        return xmlquery::child_element(&segment, &[name]);
    }
    xmlquery::elements(unit, &["segment"])
        .iter()
        .find_map(|segment| xmlquery::child_element(segment, &[name]))
}

fn translation_source_element(unit: &SyntaxNode) -> Option<SyntaxNode> {
    translation_child(unit, "source")
}

fn translation_target_element(unit: &SyntaxNode) -> Option<SyntaxNode> {
    translation_child(unit, "target")
}

pub(crate) fn parse_php_resource(file: &ParsedFile, metadata: &ResourceMetadata) -> Vec<Message> {
    let Some(tree) = file.syntax_tree() else {
        return Vec::new();
    };
    let mut result = Vec::new();
    for statement in phpquery::nodes(&tree.root(), &[PHP_RETURN_STATEMENT]) {
        let Some(array) = phpquery::direct_child(&statement, PHP_ARRAY) else {
            continue;
        };
        append_php_array_messages(&mut result, &array, file, metadata);
    }
    result
}

pub(crate) fn parse_compiled_php_catalogue(
    file: &ParsedFile,
    metadata: &ResourceMetadata,
) -> Vec<Message> {
    let Some(tree) = file.syntax_tree() else {
        return Vec::new();
    };
    let mut result = Vec::new();
    for object in phpquery::object_creations(&tree.root(), &[]) {
        if !eq_ignore_case(
            last_name_part(&phpquery::object_class_name(&object)),
            "MessageCatalogue",
        ) {
            continue;
        }
        let locale = match phpquery::string_argument(&object, 0) {
            Some(locale) => phpquery::string_value(&locale),
            None => metadata.locale.clone(),
        };
        let Some(domains) = phpquery::argument_expression(&object, 1) else {
            continue;
        };
        if domains.kind() != PHP_ARRAY {
            continue;
        }
        for domain_item in phpquery::array_items(&domains) {
            let (Some(domain_node), Some(messages)) = (
                phpquery::array_item_key(&domain_item),
                phpquery::array_item_value(&domain_item),
            ) else {
                continue;
            };
            if messages.kind() != PHP_ARRAY {
                continue;
            }
            let value = phpquery::string_value(&domain_node);
            let domain = normalize_domain(&value);
            if domain.is_empty() {
                continue;
            }
            let local = ResourceMetadata {
                domain: domain.to_owned(),
                locale: locale.clone(),
                ..metadata.clone()
            };
            append_php_array_messages(&mut result, &messages, file, &local);
        }
    }
    result
}

fn append_php_array_messages(
    result: &mut Vec<Message>,
    array: &SyntaxNode,
    file: &ParsedFile,
    metadata: &ResourceMetadata,
) {
    for item in phpquery::array_items(array) {
        let (Some(key_node), Some(value_node)) = (
            phpquery::array_item_key(&item),
            phpquery::array_item_value(&item),
        ) else {
            continue;
        };
        let key = phpquery::string_value(&key_node);
        if key.is_empty() {
            continue;
        }
        let mut text = phpquery::string_value(&value_node);
        if text.is_empty() {
            text = value_node.text().to_string().trim().to_owned();
        }
        result.push(new_message(
            &metadata.domain,
            key,
            text,
            &metadata.locale,
            file.path(),
            &metadata.format,
            Some(&key_node),
            file.line_index(),
        ));
    }
}

pub(crate) fn last_name_part(value: &str) -> &str {
    let value = value.strip_prefix('\\').unwrap_or(value);
    match value.rfind('\\') {
        Some(index) => &value[index + 1..],
        None => value,
    }
}
