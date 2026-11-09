//! Static DAL reference contexts in JavaScript, Twig and PHP.

use swlsp_parser_js::query as jsquery;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_twig::query as twigquery;
use swlsp_syntax::SyntaxNode;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum JsFieldReferenceKind {
    #[default]
    None,
    Field,
    Association,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum JsEntityReferenceKind {
    #[default]
    None,
    Repository,
    DefinitionGet,
    DefinitionHas,
    AppScript,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct JsEntityReference {
    pub name: String,
    pub kind: JsEntityReferenceKind,
}

pub fn is_js_entity_reference(node: &SyntaxNode) -> bool {
    js_entity_reference_at(node).is_some()
}

/// Recognizes static Shopware DAL entity-name arguments in Administration
/// repository and EntityDefinition APIs, plus App Script repository calls.
/// The kind lets diagnostics distinguish a strict lookup from an intentional
/// `has()` existence guard.
pub fn js_entity_reference_at(node: &SyntaxNode) -> Option<JsEntityReference> {
    if jsquery::string_at(node).is_none() || jsquery::string_argument_index(node) != Some(0) {
        return None;
    }
    let name = jsquery::call_name(node).unwrap_or_default();
    if name.ends_with(".create") && name.to_lowercase().contains("repositoryfactory") {
        return js_entity_reference(node, JsEntityReferenceKind::Repository);
    }
    let kind = match name.as_str() {
        "EntityDefinition.get" | "Shopware.EntityDefinition.get" | "this.EntityDefinition.get" => {
            JsEntityReferenceKind::DefinitionGet
        }
        "EntityDefinition.has" | "Shopware.EntityDefinition.has" | "this.EntityDefinition.has" => {
            JsEntityReferenceKind::DefinitionHas
        }
        "services.repository.search"
        | "services.repository.aggregate"
        | "services.repository.searchIds" => JsEntityReferenceKind::AppScript,
        _ => return None,
    };
    js_entity_reference(node, kind)
}

fn js_entity_reference(
    node: &SyntaxNode,
    kind: JsEntityReferenceKind,
) -> Option<JsEntityReference> {
    let name = jsquery::string_value(node);
    if name.is_empty() {
        return None;
    }
    Some(JsEntityReference { name, kind })
}

/// Recognizes Shopware Criteria field and association path arguments.
/// Aggregation helpers use their first argument as the aggregation name and
/// their second argument as the field path.
pub fn js_field_reference_at(node: &SyntaxNode) -> JsFieldReferenceKind {
    if jsquery::string_at(node).is_none() {
        return JsFieldReferenceKind::None;
    }
    let Some(argument_index) = jsquery::string_argument_index(node) else {
        return JsFieldReferenceKind::None;
    };
    let call_name = jsquery::call_name(node).unwrap_or_default();
    let method_name = jsquery::call_method_name(node).unwrap_or_default();
    match method_name.as_str() {
        "addAssociation" | "getAssociation" | "hasAssociation" if argument_index == 0 => {
            return JsFieldReferenceKind::Association;
        }
        "addFields" => return JsFieldReferenceKind::Field,
        "addGroupField" if argument_index == 0 => return JsFieldReferenceKind::Field,
        _ => {}
    }
    if argument_index == 0 {
        match call_name.as_str() {
            "Criteria.equals"
            | "Criteria.equalsAny"
            | "Criteria.contains"
            | "Criteria.prefix"
            | "Criteria.suffix"
            | "Criteria.range"
            | "Criteria.sort"
            | "Criteria.naturalSorting" => return JsFieldReferenceKind::Field,
            "Criteria.hasAssociation" => return JsFieldReferenceKind::Association,
            _ => {}
        }
    }
    if argument_index == 1
        && matches!(
            call_name.as_str(),
            "Criteria.terms"
                | "Criteria.count"
                | "Criteria.avg"
                | "Criteria.sum"
                | "Criteria.max"
                | "Criteria.min"
                | "Criteria.stats"
        )
    {
        return JsFieldReferenceKind::Field;
    }
    JsFieldReferenceKind::None
}

/// Returns the path segment under the cursor and whether it is followed by a
/// dot (in which case it must itself be an association).
pub fn js_field_reference_segment(node: &SyntaxNode, offset: u32) -> (String, bool) {
    let Some(string_node) = jsquery::string_at(node) else {
        return (String::new(), false);
    };
    if js_field_reference_at(&string_node) == JsFieldReferenceKind::None {
        return (String::new(), false);
    }
    let value = jsquery::string_value(&string_node);
    if value.is_empty() {
        return (String::new(), false);
    }
    let raw = string_node.text().to_string();
    let trimmed = raw.trim();
    let leading = raw.find(trimmed).unwrap_or(0);
    let mut content_start = u32::from(string_node.text_range().start()) + leading as u32;
    if matches!(trimmed.as_bytes().first(), Some(b'\'' | b'"' | b'`')) {
        content_start += 1;
    }
    let relative = (offset.saturating_sub(content_start) as usize).min(value.len());
    let bytes = value.as_bytes();
    let start = bytes[..relative]
        .iter()
        .rposition(|&byte| byte == b'.')
        .map_or(0, |index| index + 1);
    let end = bytes[relative..]
        .iter()
        .position(|&byte| byte == b'.')
        .map_or(bytes.len(), |next| relative + next);
    if start > end {
        return (String::new(), false);
    }
    // Both bounds sit next to an ASCII dot or at the ends of the value.
    (value[start..end].to_owned(), end < bytes.len())
}

/// Whether `node` is the static entity name of an App Script repository call.
pub fn is_twig_entity_reference(node: &SyntaxNode) -> bool {
    let (Some(literal), Some(call)) = (
        twigquery::literal_string_at(node),
        twigquery::function_call_at(node),
    ) else {
        return false;
    };
    if twigquery::function_argument_index(&literal) != Some(0) {
        return false;
    }
    let text: String = call.text().to_string().split_whitespace().collect();
    let text = text.split('(').next().unwrap_or_default();
    matches!(
        text,
        "services.repository.search"
            | "services.repository.aggregate"
            | "services.repository.searchIds"
    )
}

/// Whether `node` is the field-name argument of a DAL `*Filter` constructor.
pub fn is_php_field_reference(node: &SyntaxNode) -> bool {
    let (Some(string_node), Some(creation)) = (
        phpquery::string_at(node),
        phpquery::object_creation_at(node),
    ) else {
        return false;
    };
    if phpquery::argument_index(&creation, &string_node) != Some(0) {
        return false;
    }
    let class_name = phpquery::object_class_name(&creation);
    let class_name = class_name.rsplit('\\').next().unwrap_or_default();
    class_name.ends_with("Filter") && class_name != "Filter"
}
