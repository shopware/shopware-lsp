//! Service registration of DAL definitions (port of Go `service.go`).

use swlsp_language::LanguageId;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_yaml::query as yamlquery;

use crate::model::*;
use crate::util::*;
use swlsp_uriutil::filepath;

/// Registers a definition while preserving the surrounding configuration
/// file. Callers pass an empty source and the target path for a new file.
pub fn patch_service_configuration(
    path: &str,
    source: &str,
    definition_class: &str,
) -> Result<String> {
    patch_tagged_service_configuration(path, source, definition_class, "shopware.entity.definition")
}

/// Registers a DAL definition or extension with its exact Shopware service
/// tag while preserving the surrounding file.
pub fn patch_tagged_service_configuration(
    path: &str,
    source: &str,
    class_name: &str,
    tag: &str,
) -> Result<String> {
    let class_name = trim_class(class_name);
    if class_name.is_empty() {
        return Err(errorf!("entity service class is empty"));
    }
    if tag != "shopware.entity.definition"
        && tag != "shopware.entity.extension"
        && tag != "shopware.bulk.entity.extension"
    {
        return Err(errorf!("unsupported entity service tag {}", go_quote(tag)));
    }
    if source.contains(class_name) && source.contains(tag) {
        return Ok(source.to_owned());
    }
    let result = match go_to_lower(filepath::ext(path)).as_str() {
        ".xml" => {
            if source.trim().is_empty() {
                format!(
                    r#"<?xml version="1.0" ?>
<container xmlns="http://symfony.com/schema/dic/services"
           xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
           xsi:schemaLocation="http://symfony.com/schema/dic/services https://symfony.com/schema/dic/services/services-1.0.xsd">
    <services>
        <service id="{class_name}">
            <tag name="{tag}"/>
        </service>
    </services>
</container>
"#
                )
            } else {
                let Some(position) = source.rfind("</services>") else {
                    return Err(errorf!("services XML has no services element"));
                };
                let indent = format!("{}    ", indentation_before(source, position));
                let entry = format!(
                    "{indent}<service id=\"{class_name}\">\n{indent}    <tag name=\"{tag}\"/>\n{indent}</service>\n"
                );
                format!("{}{entry}{}", &source[..position], &source[position..])
            }
        }
        ".yaml" | ".yml" => {
            if source.trim().is_empty() {
                format!("services:\n  {class_name}:\n    tags:\n      - {{ name: {tag} }}\n")
            } else if !has_yaml_services_root(source) {
                return Err(errorf!("services YAML has no top-level services mapping"));
            } else {
                let position = yaml_services_end(source);
                let prefix = if position > 0 && source.as_bytes()[position - 1] != b'\n' {
                    "\n"
                } else {
                    ""
                };
                let entry =
                    format!("{prefix}  {class_name}:\n    tags:\n      - {{ name: {tag} }}\n");
                format!("{}{entry}{}", &source[..position], &source[position..])
            }
        }
        ".php" => {
            if source.trim().is_empty() {
                return Err(errorf!(
                    "cannot create PHP service configuration without its closure skeleton"
                ));
            }
            let Some(position) = source.rfind("};") else {
                return Err(errorf!("services PHP has no closing configurator closure"));
            };
            let indent = format!("{}    ", indentation_before(source, position));
            let entry = format!("{indent}$services->set(\\{class_name}::class)->tag('{tag}');\n");
            format!("{}{entry}{}", &source[..position], &source[position..])
        }
        _ => {
            return Err(errorf!(
                "unsupported service configuration {}",
                filepath::base(path)
            ));
        }
    };
    validate_patched(path, &result, "patched")?;
    Ok(result)
}

fn validate_patched(path: &str, result: &str, action: &str) -> Result<()> {
    if let Some(language) = LanguageId::from_path(path)
        && let Some(parsed) = language.parse(result)
        && let Some(error) = parsed.errors.first()
    {
        return Err(errorf!(
            "{action} service configuration is invalid: {}",
            error.message()
        ));
    }
    Ok(())
}

/// Removes an explicitly configured service class while preserving unrelated
/// configuration. Resource-based discovery needs no edit.
pub fn remove_service_configuration(path: &str, source: &str, class_name: &str) -> Result<String> {
    let class_name = trim_class(class_name);
    if class_name.is_empty() {
        return Err(errorf!("entity service class is empty"));
    }
    if source.trim().is_empty() {
        return Ok(source.to_owned());
    }
    let mut found: Option<(usize, usize)> = None;
    let range = |node: &swlsp_syntax::SyntaxNode| {
        let range = node.text_range();
        (
            u32::from(range.start()) as usize,
            u32::from(range.end()) as usize,
        )
    };
    match go_to_lower(filepath::ext(path)).as_str() {
        ".xml" => {
            let parsed = swlsp_parser_xml::parse(source);
            if !parsed.errors.is_empty() {
                return Err(errorf!("services XML cannot be parsed"));
            }
            for service in xmlquery::elements(&parsed.root(), &["service"]) {
                let id = xmlquery::attribute(&service, "id")
                    .and_then(|attribute| xmlquery::attribute_value(&attribute))
                    .unwrap_or_default();
                if trim_class(&id) == class_name {
                    found = Some(range(&service));
                    break;
                }
            }
        }
        ".yaml" | ".yml" => {
            let parsed = swlsp_parser_yaml::parse(source);
            if !parsed.errors.is_empty() {
                return Err(errorf!("services YAML cannot be parsed"));
            }
            let pair = yamlquery::root_value(&parsed.root())
                .and_then(|root| yamlquery::property(&root, "services"))
                .and_then(|services| yamlquery::property_pair(&services, class_name));
            if let Some(pair) = pair {
                found = Some(range(&pair));
            }
        }
        ".php" => {
            let parsed = swlsp_parser_php::parse(source);
            if !parsed.errors.is_empty() {
                return Err(errorf!("services PHP cannot be parsed"));
            }
            for statement in phpquery::expression_statements(&parsed.root()) {
                if php_service_statement_registers(&statement.to_string(), class_name) {
                    found = Some(range(&statement));
                    break;
                }
            }
        }
        _ => {
            return Err(errorf!(
                "unsupported service configuration {}",
                filepath::base(path)
            ));
        }
    }
    let Some((start, end)) = found else {
        return Ok(source.to_owned());
    };
    let (start, end) = whole_line_range(source, start, end);
    let result = format!("{}{}", &source[..start], &source[end..]);
    validate_patched(path, &result, "removed")?;
    Ok(result)
}

fn php_service_statement_registers(source: &str, class_name: &str) -> bool {
    let compact: String = source
        .chars()
        .filter(|character| !matches!(character, ' ' | '\t' | '\r' | '\n'))
        .collect();
    compact.contains(&format!("$services->set(\\{class_name}::class"))
        || compact.contains(&format!("$services->set('{class_name}'"))
        || compact.contains(&format!("$services->set(\"{class_name}\""))
}

/// Extends `start..end` to whole lines when only whitespace surrounds it.
pub(crate) fn whole_line_range(source: &str, start: usize, end: usize) -> (usize, usize) {
    let line_start = source[..start].rfind('\n').map_or(0, |index| index + 1);
    let line_end = source[end..]
        .find('\n')
        .map_or(source.len(), |relative| end + relative + 1);
    if source[line_start..start].trim().is_empty() && source[end..line_end].trim().is_empty() {
        return (line_start, line_end);
    }
    (start, end)
}

fn indentation_before(source: &str, offset: usize) -> &str {
    let start = source[..offset].rfind('\n').map_or(0, |index| index + 1);
    let line = &source[start..offset];
    &line[..line.len() - line.trim_start_matches([' ', '\t']).len()]
}

fn is_root_level(line: &str) -> bool {
    line.len() == line.trim_start_matches([' ', '\t']).len()
}

fn has_yaml_services_root(source: &str) -> bool {
    source
        .split('\n')
        .any(|line| line.trim() == "services:" && is_root_level(line))
}

fn yaml_services_end(source: &str) -> usize {
    let mut offset = 0;
    let mut in_services = false;
    for line in source.split_inclusive('\n') {
        let plain = line.strip_suffix('\n').unwrap_or(line);
        let trimmed = plain.trim();
        let root_level = is_root_level(plain);
        if !in_services {
            if root_level && trimmed == "services:" {
                in_services = true;
            }
            offset += line.len();
            continue;
        }
        if root_level && !trimmed.is_empty() && !trimmed.starts_with('#') {
            return offset;
        }
        offset += line.len();
    }
    source.len()
}
