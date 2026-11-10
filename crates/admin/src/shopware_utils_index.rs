//! `Shopware.Utils` value declarations.

use crate::*;

pub(crate) const SHOPWARE_UTILS_TYPE: &str = "Shopware.Utils";

/// Compact value declarations of the Shopware utility sources, stored next
/// to TypeScript declarations in the type index.
pub(crate) fn parse_shopware_utility_value_declarations(
    file_path: &str,
    source: &str,
    line_index: &LineIndex,
) -> Vec<AdminTypeDeclaration> {
    if !is_shopware_utility_source(file_path) || source.trim().is_empty() {
        return Vec::new();
    }
    let root = swlsp_parser_js::parse(source).root();
    let mut result: Vec<AdminTypeDeclaration> = Vec::new();
    let mut seen: HashSet<String> = HashSet::new();
    let mut append_declaration = |declaration: AdminTypeDeclaration| {
        let key = if declaration.default {
            "\x00default".to_owned()
        } else {
            declaration.name.clone()
        };
        if declaration.name.is_empty() || seen.contains(&key) {
            return;
        }
        seen.insert(key);
        result.push(declaration);
    };

    for function in jsquery::nodes(&root, &[jssyntax::JS_FUNCTION]) {
        if !top_level_javascript_node(&function) {
            continue;
        }
        let Some(name_node) = first_direct_identifier(&function) else {
            continue;
        };
        let name = jsquery::identifier_text(&name_node).unwrap_or_default();
        if name.is_empty() || !function.text_string().contains("function") {
            continue;
        }
        append_declaration(AdminTypeDeclaration {
            name,
            file_path: file_path.to_owned(),
            line: utility_node_line(&name_node, line_index),
            alias: vue_method_signature(&function, &HashMap::new()),
            definition_range: component_member_name_range(Some(&name_node), line_index),
            ..Default::default()
        });
    }

    for variable in jsquery::nodes(&root, &[jssyntax::JS_VARIABLE_DECLARATION]) {
        if !top_level_javascript_node(&variable) {
            continue;
        }
        let Some(name_node) = first_direct_identifier(&variable) else {
            continue;
        };
        let name = jsquery::identifier_text(&name_node).unwrap_or_default();
        if name.is_empty() {
            continue;
        }
        let mut declaration = AdminTypeDeclaration {
            name,
            file_path: file_path.to_owned(),
            line: utility_node_line(&name_node, line_index),
            definition_range: component_member_name_range(Some(&name_node), line_index),
            ..Default::default()
        };
        if let Some(object) = first_object(&variable) {
            declaration.interface = true;
            declaration.members = shopware_utility_object_members(&object, file_path, line_index);
        } else {
            declaration.alias = shopware_utility_variable_type(&variable);
        }
        append_declaration(declaration);
    }

    for export_node in jsquery::export_defaults(&root) {
        let Some(expression) = jsquery::export_default_expression(&export_node) else {
            continue;
        };
        let name = if is_shopware_utility_service(file_path) {
            SHOPWARE_UTILS_TYPE
        } else {
            "default"
        };
        let mut declaration = AdminTypeDeclaration {
            name: name.to_owned(),
            file_path: file_path.to_owned(),
            default: true,
            line: utility_node_line(&expression, line_index),
            ..Default::default()
        };
        match expression.kind() {
            jssyntax::JS_OBJECT => {
                declaration.interface = true;
                declaration.members =
                    shopware_utility_object_members(&expression, file_path, line_index);
            }
            jssyntax::JS_IDENTIFIER => {
                declaration.alias = jsquery::identifier_text(&expression).unwrap_or_default();
            }
            _ => declaration.alias = expression.text_string().trim().to_owned(),
        }
        append_declaration(declaration);
    }
    result
}

pub(crate) fn shopware_utility_object_members(
    object: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> Vec<TwigVueMember> {
    let properties = jsquery::properties(object);
    let mut result = Vec::with_capacity(properties.len());
    for property in properties {
        let name = jsquery::property_name(&property).unwrap_or_default();
        let Some(name_node) = jsquery::property_name_node(&property) else {
            continue;
        };
        if name.is_empty() {
            continue;
        }
        let mut member = TwigVueMember {
            name: name.clone(),
            definition_path: file_path.to_owned(),
            definition_line: utility_node_line(&name_node, line_index),
            definition_range: component_member_name_range(Some(&name_node), line_index),
            ..Default::default()
        };
        if property.kind() == jssyntax::JS_METHOD {
            member.type_ = vue_method_signature(&property, &HashMap::new());
            result.push(member);
            continue;
        }
        let Some(value) = jsquery::property_value(&property) else {
            member.type_ = name;
            result.push(member);
            continue;
        };
        match value.kind() {
            jssyntax::JS_OBJECT => {
                member.type_ = format!("{{{name}}}");
                member.nested_members =
                    shopware_utility_object_members(&value, file_path, line_index);
                member.nested_complete = true;
            }
            jssyntax::JS_FUNCTION | jssyntax::JS_ARROW_FUNCTION => {
                member.type_ = vue_method_signature(&value, &HashMap::new());
            }
            _ => member.type_ = compact_vue_expression(&value.text_string()),
        }
        result.push(member);
    }
    result
}

pub(crate) fn shopware_utility_variable_type(variable: &SyntaxNode) -> String {
    let full = variable.text_string();
    let text = full.trim();
    let Some(equals) = text.find('=') else {
        return String::new();
    };
    let value = trim_vue_source_expression(&text[equals + 1..]);
    if value.starts_with("mitt<")
        && let Some(open) = value.find('<')
        && let Some(close) =
            matching_slot_delimiter(&value, open, b'<', b'>').filter(|close| *close > open)
    {
        return format!("Emitter<{}>", value[open + 1..close].trim());
    }
    if value.contains("=>") || value.starts_with("function") {
        return setup_store_binding_type(variable, AdminStoreMemberKind::Action);
    }
    setup_store_binding_type(variable, AdminStoreMemberKind::State)
}

pub(crate) fn is_shopware_utility_source(file_path: &str) -> bool {
    let normalized = filepath::to_slash(&filepath::clean(file_path));
    normalized.contains("/core/service/utils/") || is_shopware_utility_service(&normalized)
}

pub(crate) fn is_shopware_utility_service(file_path: &str) -> bool {
    let normalized = filepath::to_slash(&filepath::clean(file_path));
    normalized.ends_with("/core/service/util.service.ts")
        || normalized.ends_with("/core/service/util.service.js")
}

pub(crate) fn top_level_javascript_node(node: &SyntaxNode) -> bool {
    let Some(mut parent) = node.parent() else {
        return false;
    };
    if parent.kind() == jssyntax::JS_EXPRESSION_STATEMENT {
        let Some(grandparent) = parent.parent() else {
            return false;
        };
        parent = grandparent;
    }
    parent.kind() == jssyntax::JS_PROGRAM
}

pub(crate) fn utility_node_line(node: &SyntaxNode, line_index: &LineIndex) -> u32 {
    line_index
        .position(u32::from(node.range_trimmed_trivia().start()))
        .0
        + 1
}
