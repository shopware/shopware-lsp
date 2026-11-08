//! Go `service_argument_rewrite.go`: inserts missing service arguments into
//! XML and YAML service definitions.

use serde_json::Value;
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax::{
    YAML_FLOW_MAPPING, YAML_FLOW_SEQUENCE, YAML_MAPPING, YAML_SEQUENCE,
};
use swlsp_syntax::{SyntaxNode, TextRange, TextSize};
use swlsp_textutil::html_escape;

pub(crate) struct MissingServiceArgument {
    name: String,
    service: String,
}

/// Go `missingServiceArguments`.
pub(crate) fn missing_service_arguments(value: Option<&Value>) -> Vec<MissingServiceArgument> {
    let Some(Value::Array(values)) = value else {
        return Vec::new();
    };
    values
        .iter()
        .filter_map(Value::as_object)
        .map(|item| {
            let name = item.get("name").and_then(Value::as_str).unwrap_or_default();
            let service = item
                .get("suggestedService")
                .and_then(Value::as_str)
                .filter(|service| !service.is_empty())
                .unwrap_or("?");
            MissingServiceArgument {
                name: name.to_owned(),
                service: service.to_owned(),
            }
        })
        .collect()
}

fn range(start: u32, end: u32) -> TextRange {
    TextRange::new(TextSize::from(start), TextSize::from(end))
}

fn start(node: &SyntaxNode) -> u32 {
    u32::from(node.text_range().start())
}

fn end(node: &SyntaxNode) -> u32 {
    u32::from(node.text_range().end())
}

/// Go `serviceArgumentRewrite`.
pub(crate) fn service_argument_rewrite(
    content: &str,
    node: &SyntaxNode,
    format: &str,
    arguments: &[MissingServiceArgument],
) -> Option<(TextRange, String)> {
    match format {
        "xml" => xml_service_argument_rewrite(content, node, arguments),
        "yaml" => yaml_service_argument_rewrite(content, node, arguments),
        _ => None,
    }
}

fn xml_service_argument_rewrite(
    content: &str,
    node: &SyntaxNode,
    arguments: &[MissingServiceArgument],
) -> Option<(TextRange, String)> {
    let service = xmlquery::element_at(node)?;
    if xmlquery::element_name(&service).as_deref() != Some("service") {
        return None;
    }
    let text = service.text().to_string();
    let service_indent = indentation_at(content, start(&service));
    let child_indent = format!("{service_indent}    ");
    let lines: Vec<String> = arguments
        .iter()
        .map(|argument| {
            format!(
                r#"{child_indent}<argument type="service" id="{}"/>"#,
                html_escape(&argument.service)
            )
        })
        .collect();
    if let Some(index) = text.rfind("/>")
        && text[index..].trim() == "/>"
    {
        let offset = start(&service) + index as u32;
        return Some((
            range(offset, offset + 2),
            format!(">\n{}\n{service_indent}</service>", lines.join("\n")),
        ));
    }
    let index = text.rfind("</service")?;
    let closing_offset = start(&service) + index as u32;
    let line_start = line_start_offset(content, closing_offset);
    if content[line_start as usize..closing_offset as usize]
        .trim()
        .is_empty()
    {
        return Some((
            range(line_start, line_start),
            format!("{}\n", lines.join("\n")),
        ));
    }
    Some((
        range(closing_offset, closing_offset),
        format!("\n{}\n{service_indent}", lines.join("\n")),
    ))
}

fn yaml_service_argument_rewrite(
    content: &str,
    node: &SyntaxNode,
    arguments: &[MissingServiceArgument],
) -> Option<(TextRange, String)> {
    let service_pair = yamlquery::ancestor_pair(node)?;
    let config = yamlquery::pair_value(&service_pair).filter(yamlquery::is_mapping)?;
    let arguments_node = yamlquery::property(&config, "arguments");
    let key = yamlquery::pair_key(&service_pair)?;
    let service_indent = indentation_at(content, start(&key));
    let property_indent = format!("{service_indent}  ");
    let item_indent = format!("{property_indent}  ");

    let Some(arguments_node) = arguments_node else {
        if config.kind() == YAML_FLOW_MAPPING {
            let offset = yaml_flow_closing_offset(&config, '}')?;
            let values = sequence_values(arguments);
            let prefix = if yamlquery::pairs(&config).next().is_some() {
                ", "
            } else {
                ""
            };
            return Some((
                range(offset, offset),
                format!("{prefix}arguments: [{}]", values.join(", ")),
            ));
        }
        let mut lines = vec![format!("{property_indent}arguments:")];
        for argument in arguments {
            lines.push(format!(
                "{item_indent}- '{}'",
                yaml_single_quoted(&yaml_service_reference(&argument.service))
            ));
        }
        let offset = end(&config);
        return Some((range(offset, offset), format!("\n{}", lines.join("\n"))));
    };

    match arguments_node.kind() {
        YAML_FLOW_SEQUENCE => {
            let text = arguments_node.text().to_string();
            let index = text.rfind(']')?;
            let prefix = if yamlquery::items(&arguments_node).next().is_some() {
                ", "
            } else {
                ""
            };
            let offset = start(&arguments_node) + index as u32;
            Some((
                range(offset, offset),
                format!("{prefix}{}", sequence_values(arguments).join(", ")),
            ))
        }
        YAML_SEQUENCE => {
            let lines: Vec<String> = arguments
                .iter()
                .map(|argument| {
                    format!(
                        "{item_indent}- '{}'",
                        yaml_single_quoted(&yaml_service_reference(&argument.service))
                    )
                })
                .collect();
            let offset = end(&arguments_node);
            Some((range(offset, offset), format!("\n{}", lines.join("\n"))))
        }
        YAML_FLOW_MAPPING => {
            let offset = yaml_flow_closing_offset(&arguments_node, '}')?;
            let prefix = if yamlquery::pairs(&arguments_node).next().is_some() {
                ", "
            } else {
                ""
            };
            Some((
                range(offset, offset),
                format!("{prefix}{}", mapping_values(arguments).join(", ")),
            ))
        }
        YAML_MAPPING => {
            let values: Vec<String> = mapping_values(arguments)
                .into_iter()
                .map(|value| format!("{item_indent}{value}"))
                .collect();
            let offset = end(&arguments_node);
            Some((range(offset, offset), format!("\n{}", values.join("\n"))))
        }
        _ => None,
    }
}

fn sequence_values(arguments: &[MissingServiceArgument]) -> Vec<String> {
    arguments
        .iter()
        .map(|argument| {
            format!(
                "'{}'",
                yaml_single_quoted(&yaml_service_reference(&argument.service))
            )
        })
        .collect()
}

fn mapping_values(arguments: &[MissingServiceArgument]) -> Vec<String> {
    arguments
        .iter()
        .map(|argument| {
            format!(
                "{}: '@{}'",
                argument.name,
                yaml_single_quoted(
                    argument
                        .service
                        .strip_prefix('@')
                        .unwrap_or(&argument.service)
                )
            )
        })
        .collect()
}

fn indentation_at(content: &str, offset: u32) -> String {
    let start = line_start_offset(content, offset) as usize;
    let bytes = content.as_bytes();
    let mut end = start;
    while end < bytes.len() && matches!(bytes[end], b' ' | b'\t') {
        end += 1;
    }
    content[start..end].to_owned()
}

fn line_start_offset(content: &str, offset: u32) -> u32 {
    let bytes = content.as_bytes();
    let mut offset = (offset as usize).min(bytes.len());
    while offset > 0 && bytes[offset - 1] != b'\n' {
        offset -= 1;
    }
    offset as u32
}

fn yaml_single_quoted(value: &str) -> String {
    value.replace('\'', "''")
}

fn yaml_service_reference(value: &str) -> String {
    if value.starts_with('@') {
        value.to_owned()
    } else {
        format!("@{value}")
    }
}

fn yaml_flow_closing_offset(node: &SyntaxNode, delimiter: char) -> Option<u32> {
    let text = node.text().to_string();
    let mut index = text.rfind(delimiter)?;
    let bytes = text.as_bytes();
    while index > 0 && matches!(bytes[index - 1], b' ' | b'\t') {
        index -= 1;
    }
    Some(start(node) + index as u32)
}
