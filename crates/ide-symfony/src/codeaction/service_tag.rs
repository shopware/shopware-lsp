//! The Symfony plugin's XML/YAML "Add Tags"
//! intentions, one deterministic action per inferred tag.

use std::collections::{BTreeMap, HashSet};

use swlsp_language::LanguageId;
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax::{
    YAML_FLOW_MAPPING, YAML_FLOW_SEQUENCE, YAML_MAPPING, YAML_PAIR, YAML_SCALAR, YAML_SEQUENCE,
};
use swlsp_php::PhpIndex;
use swlsp_php_semantic::Snapshot;
use swlsp_symfony::{ServiceTagTypeHierarchy, suggested_service_tags};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};

use swlsp_ide::codeaction::{
    end_of, indentation_at, line_start_offset, offset_range, start_of, trimmed_end, trimmed_start,
    yaml_flow_closing_offset,
};

/// Adapts a semantic snapshot (which may contain an unsaved overlay) to the
/// tag-inference hierarchy.
pub(crate) struct SnapshotHierarchy<'a>(pub &'a Snapshot);

impl ServiceTagTypeHierarchy for SnapshotHierarchy<'_> {
    fn is_subtype_of(&self, candidate: &str, target: &str) -> bool {
        self.0.is_subtype_of(candidate, target)
    }
}

pub(crate) use swlsp_textutil::html_escape;

/// The unquoted value of attribute `name` of `element` (empty when absent).
pub(crate) fn xml_attribute_value(element: &SyntaxNode, name: &str) -> String {
    xmlquery::attribute(element, name)
        .and_then(|attribute| xmlquery::attribute_value(&attribute))
        .unwrap_or_default()
}

pub(crate) fn xml_element_named(node: &SyntaxNode) -> String {
    xmlquery::element_name(node).unwrap_or_default()
}

pub struct ServiceTagCodeActionProvider {
    php_index: Option<PhpIndex>,
}

impl ServiceTagCodeActionProvider {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

#[derive(Default)]
struct ServiceTagActionTarget {
    node: Option<SyntaxNode>,
    class_name: String,
    tags: HashSet<String>,
}

impl ActionProvider for ServiceTagCodeActionProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_REWRITE]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        let Some(php_index) = self.php_index.as_ref() else {
            return Vec::new();
        };
        if ctx.is_cancelled() || request.root.is_none() {
            return Vec::new();
        }
        let (Some(document), Some(node)) = (request.document.as_ref(), request.node.as_ref())
        else {
            return Vec::new();
        };
        let language = document.language;
        let target = match language {
            Some(LanguageId::Xml) => xml_service_tag_target(node),
            Some(LanguageId::Yaml) => yaml_service_tag_target(node),
            _ => return Vec::new(),
        };
        let class_name = target.class_name.trim().trim_matches('\\');
        let Some(target_node) = target.node.as_ref() else {
            return Vec::new();
        };
        if class_name.is_empty() || class_name.contains('%') {
            return Vec::new();
        }
        if php_index.find_class(class_name).is_none() {
            return Vec::new();
        }
        let snapshot = php_index.semantic_snapshot();
        let suggested = suggested_service_tags(class_name, Some(&SnapshotHierarchy(&snapshot)));
        let mut result = Vec::with_capacity(suggested.len());
        for tag in suggested {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            if target.tags.contains(&tag) {
                continue;
            }
            let edit = match language {
                Some(LanguageId::Xml) => xml_service_tag_edit(request, target_node, &tag),
                _ => yaml_service_tag_edit(request, target_node, &tag),
            };
            let Some(edit) = edit else {
                continue;
            };
            result.push(protocol::CodeAction {
                title: format!("Symfony: Add service tag '{tag}'"),
                kind: Some(CodeActionKind::REFACTOR_REWRITE),
                edit: Some(protocol::WorkspaceEdit {
                    changes: BTreeMap::from([(
                        request.params.text_document.uri.clone(),
                        vec![edit],
                    )]),
                    ..protocol::WorkspaceEdit::default()
                }),
                ..protocol::CodeAction::default()
            });
        }
        result
    }
}

fn xml_service_tag_target(node: &SyntaxNode) -> ServiceTagActionTarget {
    let mut service = xmlquery::element_at(node);
    while let Some(current) = service.as_ref() {
        if xml_element_named(current) == "service" {
            break;
        }
        service = xmlquery::parent_element(current);
    }
    let Some(service) = service else {
        return ServiceTagActionTarget::default();
    };
    let mut class_name = xml_attribute_value(&service, "class");
    if class_name.is_empty() {
        class_name = xml_attribute_value(&service, "id");
    }
    let mut tags = HashSet::new();
    for child in xmlquery::child_elements(&service, &["tag"]) {
        let tag = xml_attribute_value(&child, "name");
        if !tag.is_empty() {
            tags.insert(tag);
        }
    }
    ServiceTagActionTarget {
        node: Some(service),
        class_name,
        tags,
    }
}

fn yaml_service_tag_target(node: &SyntaxNode) -> ServiceTagActionTarget {
    let mut pair = yamlquery::ancestor_pair(node);
    while let Some(current) = pair.as_ref() {
        let path = yamlquery::pair_path(current);
        if path.len() == 2 && path[0] == "services" {
            break;
        }
        pair = yaml_ancestor_pair(current);
    }
    let Some(pair) = pair else {
        return ServiceTagActionTarget::default();
    };
    let service_id = yaml_scalar(yamlquery::pair_key(&pair).as_ref());
    if service_id.is_empty() || service_id.starts_with('_') {
        return ServiceTagActionTarget::default();
    }
    let Some(config) = yamlquery::pair_value(&pair).filter(yamlquery::is_mapping) else {
        return ServiceTagActionTarget::default();
    };
    if yamlquery::property(&config, "alias").is_some() {
        return ServiceTagActionTarget::default();
    }
    let mut class_name = yaml_scalar(yamlquery::property(&config, "class").as_ref());
    if class_name.is_empty() {
        class_name = service_id;
    }
    ServiceTagActionTarget {
        node: Some(pair),
        class_name,
        tags: yaml_configured_service_tags(&config),
    }
}

fn yaml_scalar(node: Option<&SyntaxNode>) -> String {
    node.map(yamlquery::scalar_value).unwrap_or_default()
}

fn yaml_ancestor_pair(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.parent()?.ancestor_or_self_of_kind(YAML_PAIR)
}

fn yaml_configured_service_tags(config: &SyntaxNode) -> HashSet<String> {
    let mut result = HashSet::new();
    let Some(tags) = yamlquery::property(config, "tags") else {
        return result;
    };
    if yamlquery::is_sequence(&tags) {
        for item in yamlquery::items(&tags) {
            let tag = yaml_configured_service_tag(yamlquery::item_value(&item).as_ref());
            if !tag.is_empty() {
                result.insert(tag);
            }
        }
        return result;
    }
    let tag = yaml_configured_service_tag(Some(&tags));
    if !tag.is_empty() {
        result.insert(tag);
    }
    result
}

fn yaml_configured_service_tag(node: Option<&SyntaxNode>) -> String {
    let Some(node) = node else {
        return String::new();
    };
    if yamlquery::is_mapping(node) {
        return yaml_scalar(yamlquery::property(node, "name").as_ref());
    }
    yamlquery::scalar_value(node)
}

fn xml_service_tag_edit(
    request: &CodeActionRequest,
    service: &SyntaxNode,
    tag: &str,
) -> Option<protocol::TextEdit> {
    let content = request.source().as_bytes();
    let text = service.text().to_string();
    let service_indent = indentation_at(content, start_of(service));
    let mut child_indent = format!("{service_indent}    ");
    if let Some(first) = xmlquery::child_elements(service, &[]).first() {
        let indent = indentation_at(content, start_of(first));
        if indent.len() > service_indent.len() {
            child_indent = indent;
        }
    }
    let tag_text = format!("{child_indent}<tag name=\"{}\"/>", html_escape(tag));

    if let Some(index) = text.rfind("/>")
        && text[index..].trim() == "/>"
    {
        let start = start_of(service) + index as u32;
        return Some(protocol::TextEdit::new(
            offset_range(request, start, start + 2),
            format!(">\n{tag_text}\n{service_indent}</service>"),
        ));
    }
    let index = text.rfind("</service")?;
    let closing_offset = start_of(service) + index as u32;
    let line_start = line_start_offset(content, closing_offset);
    if String::from_utf8_lossy(&content[line_start as usize..closing_offset as usize])
        .trim()
        .is_empty()
    {
        return Some(protocol::TextEdit::new(
            offset_range(request, line_start, line_start),
            format!("{tag_text}\n"),
        ));
    }
    Some(protocol::TextEdit::new(
        offset_range(request, closing_offset, closing_offset),
        format!("\n{tag_text}\n{service_indent}"),
    ))
}

fn yaml_service_tag_edit(
    request: &CodeActionRequest,
    service_pair: &SyntaxNode,
    tag: &str,
) -> Option<protocol::TextEdit> {
    let config = yamlquery::pair_value(service_pair).filter(yamlquery::is_mapping)?;
    if let Some(tags) = yamlquery::property(&config, "tags") {
        return yaml_existing_service_tags_edit(request, &tags, tag);
    }

    if config.kind() == YAML_FLOW_MAPPING {
        let offset = yaml_flow_closing_offset(Some(&config), b'}')?;
        let prefix = if yamlquery::pairs(&config).next().is_some() {
            ", "
        } else {
            ""
        };
        return Some(protocol::TextEdit::new(
            offset_range(request, offset, offset),
            format!("{prefix}tags: [{tag}]"),
        ));
    }

    let content = request.source().as_bytes();
    let key = yamlquery::pair_key(service_pair)?;
    let service_indent = indentation_at(content, start_of(&key));
    let property_indent = format!("{service_indent}  ");
    let item_indent = format!("{property_indent}  ");
    let offset = yaml_block_content_end(content, &config);
    Some(protocol::TextEdit::new(
        offset_range(request, offset, offset),
        format!("\n{property_indent}tags:\n{item_indent}- {tag}"),
    ))
}

fn yaml_existing_service_tags_edit(
    request: &CodeActionRequest,
    tags: &SyntaxNode,
    tag: &str,
) -> Option<protocol::TextEdit> {
    let kind = tags.kind();
    if kind == YAML_FLOW_SEQUENCE {
        let offset = yaml_flow_closing_offset(Some(tags), b']')?;
        let prefix = if yamlquery::items(tags).next().is_some() {
            ", "
        } else {
            ""
        };
        return Some(protocol::TextEdit::new(
            offset_range(request, offset, offset),
            format!("{prefix}{tag}"),
        ));
    }
    if kind == YAML_SEQUENCE {
        let content = request.source().as_bytes();
        let mut indent = indentation_at(content, start_of(tags));
        if let Some(first) = yamlquery::items(tags).next() {
            indent = indentation_at(content, start_of(&first));
        }
        let offset = yaml_block_content_end(content, tags);
        return Some(protocol::TextEdit::new(
            offset_range(request, offset, offset),
            format!("\n{indent}- {tag}"),
        ));
    }
    if kind == YAML_SCALAR || kind == YAML_FLOW_MAPPING || kind == YAML_MAPPING {
        let text = tags.text().to_string();
        let raw = text.trim();
        if raw.is_empty() {
            return None;
        }
        return Some(protocol::TextEdit::new(
            offset_range(request, trimmed_start(tags), trimmed_end(tags)),
            format!("[{raw}, {tag}]"),
        ));
    }
    None
}

fn yaml_block_content_end(content: &[u8], node: &SyntaxNode) -> u32 {
    let start = start_of(node);
    let mut end = end_of(node).min(content.len() as u32);
    while end > start {
        match content[end as usize - 1] {
            b' ' | b'\t' | b'\r' | b'\n' => end -= 1,
            _ => return end,
        }
    }
    end
}

#[cfg(test)]
#[path = "tests/service_tag.rs"]
mod tests;
