//! Named constructor arguments and default bindings in YAML service files.

use swlsp_indexer::Result;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

use crate::service_index::ServiceIndex;
use crate::util::{go_trim_space, yaml_scalar_content_range};
use crate::yaml_context::static_php_class_name;
use crate::yaml_services::{yaml_property, yaml_scalar};

/// A named constructor argument inside a Symfony YAML service definition.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct YamlServiceNamedArgument {
    pub name: String,
    pub service_id: String,
    pub class_name: String,
    pub range: TextRange,
    pub existing: Vec<String>,
    pub complete: bool,
    pub has_factory: bool,
}

/// A name-only or typed constructor argument binding under
/// `services._defaults.bind`.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct YamlServiceDefaultBinding {
    pub name: String,
    pub type_name: String,
    pub range: TextRange,
}

fn contains_offset(range: TextRange, offset: u32) -> bool {
    let offset = TextSize::from(offset);
    offset >= range.start() && offset <= range.end()
}

/// The default binding key under the cursor. Symfony accepts both `$name`
/// and `Type $name` keys.
pub fn yaml_service_default_binding_at(
    root: &SyntaxNode,
    offset: u32,
) -> Option<YamlServiceDefaultBinding> {
    let document = yamlquery::root_value(root);
    let services = yaml_property(document.as_ref(), "services").filter(yamlquery::is_mapping)?;
    let defaults = yamlquery::property(&services, "_defaults").filter(yamlquery::is_mapping)?;
    let bindings = yamlquery::property(&defaults, "bind").filter(yamlquery::is_mapping)?;
    for pair in yamlquery::pairs(&bindings) {
        let key = yamlquery::pair_key(&pair);
        let range = yaml_scalar_content_range(key.as_ref());
        if !contains_offset(range, offset) {
            continue;
        }
        let (name, type_name) = parse_yaml_service_default_binding_key(&yaml_scalar(key.as_ref()))?;
        return Some(YamlServiceDefaultBinding {
            name,
            type_name,
            range,
        });
    }
    None
}

/// Complete `$name: value` argument keys.
pub fn yaml_service_named_arguments(root: &SyntaxNode) -> Vec<YamlServiceNamedArgument> {
    yaml_service_pairs(root)
        .iter()
        .flat_map(yaml_service_named_argument_contexts_for_service)
        .filter(|context| context.complete)
        .collect()
}

/// The named-argument key under the cursor. Incomplete keys such as `$log`
/// are included so completion remains available while the document is
/// temporarily malformed.
pub fn yaml_service_named_argument_at(
    root: &SyntaxNode,
    offset: u32,
) -> Option<YamlServiceNamedArgument> {
    for service_pair in yaml_service_pairs(root) {
        if !contains_offset(service_pair.range_trimmed_trivia(), offset) {
            continue;
        }
        return yaml_service_named_argument_contexts_for_service(&service_pair)
            .into_iter()
            .find(|context| contains_offset(context.range, offset));
    }
    None
}

/// Resolves the configured class, falling back to the indexed service and
/// following aliases when the local definition only provides a service ID.
pub fn resolve_yaml_service_named_argument_class(
    index: Option<&ServiceIndex>,
    argument: &YamlServiceNamedArgument,
) -> Result<Option<String>> {
    if !argument.class_name.is_empty() {
        return Ok(Some(
            argument
                .class_name
                .strip_prefix('\\')
                .unwrap_or(&argument.class_name)
                .to_owned(),
        ));
    }
    match index {
        Some(index) if !argument.service_id.is_empty() => {
            index.resolve_service_class_name(&argument.service_id)
        }
        _ => Ok(None),
    }
}

pub(crate) fn yaml_service_pairs(root: &SyntaxNode) -> Vec<SyntaxNode> {
    let document = yamlquery::root_value(root);
    match yaml_property(document.as_ref(), "services").filter(yamlquery::is_mapping) {
        Some(services) => yamlquery::pairs(&services).collect(),
        None => Vec::new(),
    }
}

fn yaml_service_named_argument_contexts_for_service(
    service_pair: &SyntaxNode,
) -> Vec<YamlServiceNamedArgument> {
    let service_id = yaml_scalar(yamlquery::pair_key(service_pair).as_ref());
    if service_id.is_empty() || service_id.starts_with('_') {
        return Vec::new();
    }
    let Some(config) = yamlquery::pair_value(service_pair).filter(yamlquery::is_mapping) else {
        return Vec::new();
    };
    let Some(arguments) = yamlquery::property(&config, "arguments") else {
        return Vec::new();
    };
    let base = YamlServiceNamedArgument {
        class_name: yaml_service_class_name(&service_id, &config),
        service_id,
        has_factory: yamlquery::property(&config, "factory").is_some(),
        ..YamlServiceNamedArgument::default()
    };
    if yamlquery::is_mapping(&arguments) {
        let pairs: Vec<SyntaxNode> = yamlquery::pairs(&arguments).collect();
        let existing: Vec<String> = pairs
            .iter()
            .map(|pair| yaml_scalar(yamlquery::pair_key(pair).as_ref()))
            .filter(|name| is_yaml_named_argument(name))
            .collect();
        let mut result = Vec::with_capacity(existing.len());
        for pair in &pairs {
            let key = yamlquery::pair_key(pair);
            let name = yaml_scalar(key.as_ref());
            if !is_yaml_named_argument(&name) {
                continue;
            }
            result.push(YamlServiceNamedArgument {
                name,
                range: yaml_scalar_content_range(key.as_ref()),
                existing: existing.clone(),
                complete: true,
                ..base.clone()
            });
        }
        return result;
    }
    // During completion, "$name" without a trailing colon is parsed as the
    // scalar value of the arguments pair instead of as a nested mapping.
    let name = yamlquery::scalar_value(&arguments);
    if !is_yaml_named_argument(&name) {
        return Vec::new();
    }
    vec![YamlServiceNamedArgument {
        range: yaml_scalar_content_range(Some(&arguments)),
        existing: vec![name.clone()],
        name,
        ..base
    }]
}

pub(crate) fn yaml_service_class_name(service_id: &str, config: &SyntaxNode) -> String {
    let class = yaml_scalar(yamlquery::property(config, "class").as_ref());
    if static_yaml_service_class_name(&class) {
        return class.strip_prefix('\\').unwrap_or(&class).to_owned();
    }
    if static_php_class_name(service_id) {
        return service_id
            .strip_prefix('\\')
            .unwrap_or(service_id)
            .to_owned();
    }
    String::new()
}

pub(crate) fn static_yaml_service_class_name(value: &str) -> bool {
    let value = go_trim_space(value.strip_prefix('\\').unwrap_or(value));
    !value.is_empty() && !value.contains(['%', '$', '{', '}', '@', ' ', '\t'])
}

fn is_yaml_named_argument(name: &str) -> bool {
    let name = go_trim_space(name);
    name.len() > 1 && name.starts_with('$') && !name[1..].contains([' ', '\t', ':', '$'])
}

fn parse_yaml_service_default_binding_key(value: &str) -> Option<(String, String)> {
    let value = go_trim_space(value);
    let dollar = value.rfind('$')?;
    let name = go_trim_space(&value[dollar..]);
    if !is_yaml_named_argument(name) {
        return None;
    }
    let type_name = go_trim_space(&value[..dollar]);
    if type_name.contains('$') {
        return None;
    }
    Some((name.to_owned(), type_name.to_owned()))
}

#[cfg(test)]
#[path = "tests_refs/yaml_named_argument.rs"]
mod tests;
