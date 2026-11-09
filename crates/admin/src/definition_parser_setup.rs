//! Composition API `setup()` members.

use crate::*;

#[derive(Clone, Debug, Default)]
pub(crate) struct ComponentSetupBinding {
    pub kind: VueComponentMemberKind,
    pub member_type: String,
    pub source_expression: String,
    pub open_runtime_shape: bool,
    pub cms_registry_kind: AdminCMSRegistrationKind,
}

/// Extracts the values a setup function exposes through a statically
/// returned object. Spreads and arbitrary return expressions are ignored.
pub(crate) fn parse_component_setup_members(
    property: &SyntaxNode,
    line_index: &LineIndex,
) -> Vec<VueComponentMember> {
    let Some(setup) = component_setup_function(property) else {
        return Vec::new();
    };
    let bindings = component_setup_bindings(&setup);
    let mut positions: HashMap<String, usize> = HashMap::new();
    let mut result: Vec<VueComponentMember> = Vec::new();
    let mut add = |mut member: VueComponentMember, result: &mut Vec<VueComponentMember>| {
        if member.name.is_empty() {
            return;
        }
        if let Some(&index) = positions.get(&member.name) {
            // Prefer the declaration carrying the stronger static contract.
            let current = &result[index];
            if !current.type_.is_empty() && member.type_.is_empty() {
                member.type_ = current.type_.clone();
            }
            if !current.source_expression.is_empty() && member.source_expression.is_empty() {
                member.source_expression = current.source_expression.clone();
            }
            if !current.cms_registry_kind.is_unset() && member.cms_registry_kind.is_unset() {
                member.cms_registry_kind = current.cms_registry_kind;
            }
            member.open_runtime_shape = member.open_runtime_shape || current.open_runtime_shape;
            result[index] = member;
            return;
        }
        positions.insert(member.name.clone(), result.len());
        result.push(member);
    };

    for returned in component_setup_returned_objects(&setup) {
        for returned_property in jsquery::properties(&returned) {
            let property_text = returned_property.text().to_string();
            if compact_javascript_text(&property_text).starts_with("...") {
                continue;
            }
            let name = jsquery::property_name(&returned_property)
                .unwrap_or_default()
                .trim()
                .to_owned();
            if name.is_empty() {
                continue;
            }
            let mut member = VueComponentMember {
                kind: VueComponentMemberKind::Data,
                line: definition_node_line(line_index, &returned_property),
                deprecated: javascript_deprecation(&returned_property),
                ..Default::default()
            };
            let mut binding_name = name.clone();
            let value = jsquery::property_value(&returned_property);
            member.name_range = component_member_name_range(
                jsquery::property_name_node(&returned_property).as_ref(),
                line_index,
            );
            if let Some(value) = value
                .as_ref()
                .filter(|value| value.kind() == jssyntax::JS_IDENTIFIER)
            {
                binding_name = jsquery::identifier_text(value)
                    .unwrap_or_default()
                    .trim()
                    .to_owned();
            }
            member.shorthand = value.is_none() && is_static_vue_identifier(&name);
            member.name = name;
            if let Some(binding) = bindings.get(&binding_name) {
                member.kind = binding.kind;
                member.type_ = binding.member_type.clone();
                member.source_expression = binding.source_expression.clone();
                member.open_runtime_shape = binding.open_runtime_shape;
                member.cms_registry_kind = binding.cms_registry_kind;
            } else if returned_property.kind() == jssyntax::JS_METHOD {
                member.kind = VueComponentMemberKind::Method;
                member.type_ = vue_method_signature(&returned_property, None);
                member.source_expression = vue_method_return_expression(&returned_property);
                member.cms_registry_kind =
                    cms_registry_kind_from_expression(&property_text).unwrap_or_default();
            } else if let Some(value) = &value {
                member.type_ = vue_expression_type(value, None);
                member.source_expression = trim_vue_source_expression(&value.text().to_string());
                member.open_runtime_shape = value.kind() == jssyntax::JS_OBJECT;
            }
            member.binding_name = binding_name;
            add(member, &mut result);
        }
    }
    result
}

pub(crate) fn component_setup_function(property: &SyntaxNode) -> Option<SyntaxNode> {
    if property.kind() == jssyntax::JS_METHOD {
        return Some(property.clone());
    }
    jsquery::property_value(property).filter(|value| {
        matches!(
            value.kind(),
            jssyntax::JS_FUNCTION | jssyntax::JS_ARROW_FUNCTION
        )
    })
}

pub(crate) fn component_setup_returned_objects(setup: &SyntaxNode) -> Vec<SyntaxNode> {
    let owned_by_setup =
        |node: &SyntaxNode| closest_javascript_function(node).as_ref() == Some(setup);
    let mut result = Vec::new();
    for statement in jsquery::nodes(setup, &[jssyntax::JS_RETURN_STATEMENT]) {
        if !owned_by_setup(&statement) {
            continue;
        }
        if let Some(object) = jsquery::nodes(&statement, &[jssyntax::JS_OBJECT])
            .into_iter()
            .find(|object| {
                owned_by_setup(object)
                    && component_setup_object_is_direct_return(&statement, object)
            })
        {
            result.push(object);
        }
    }
    if result.is_empty()
        && setup.kind() == jssyntax::JS_ARROW_FUNCTION
        && let Some(object) = jsquery::nodes(setup, &[jssyntax::JS_OBJECT])
            .into_iter()
            .find(|object| {
                owned_by_setup(object) && component_setup_object_is_direct_arrow_body(setup, object)
            })
    {
        result.push(object);
    }
    result
}

/// The text of `container` before and after `object` (trimmed range), or
/// `None` when `object` lies outside it.
fn surrounding_text(container: &SyntaxNode, object: &SyntaxNode) -> Option<(String, String)> {
    let container_range = container.text_range();
    let object_range = object.range_trimmed_trivia();
    if object_range.start() < container_range.start() || object_range.end() > container_range.end()
    {
        return None;
    }
    let text = container.text().to_string();
    let start = usize::from(object_range.start() - container_range.start());
    let end = usize::from(object_range.end() - container_range.start());
    Some((text.get(..start)?.to_owned(), text.get(end..)?.to_owned()))
}

pub(crate) fn component_setup_object_is_direct_return(
    statement: &SyntaxNode,
    object: &SyntaxNode,
) -> bool {
    let Some((before, after)) = surrounding_text(statement, object) else {
        return false;
    };
    let Some(before) = before.trim().strip_prefix("return") else {
        return false;
    };
    before
        .trim()
        .trim_matches(['(', ' ', '\t', '\r', '\n'])
        .is_empty()
        && after
            .trim()
            .trim_matches([')', ';', ' ', '\t', '\r', '\n'])
            .is_empty()
}

pub(crate) fn component_setup_object_is_direct_arrow_body(
    setup: &SyntaxNode,
    object: &SyntaxNode,
) -> bool {
    let Some((before, after)) = surrounding_text(setup, object) else {
        return false;
    };
    let Some(arrow) = before.rfind("=>") else {
        return false;
    };
    before[arrow + 2..]
        .trim()
        .trim_matches(['(', ' ', '\t', '\r', '\n'])
        .is_empty()
        && after
            .trim()
            .trim_matches([')', ',', ';', ' ', '\t', '\r', '\n'])
            .is_empty()
}

pub(crate) fn component_setup_bindings(
    setup: &SyntaxNode,
) -> HashMap<String, ComponentSetupBinding> {
    let mut bindings = HashMap::new();
    let mut known: HashMap<String, String> = HashMap::new();
    let owned_by_setup =
        |node: &SyntaxNode| closest_javascript_function(node).as_ref() == Some(setup);
    for declaration in jsquery::nodes(setup, &[jssyntax::JS_VARIABLE_DECLARATION]) {
        if !owned_by_setup(&declaration) {
            continue;
        }
        let Some(name_node) = first_direct_identifier(&declaration) else {
            continue;
        };
        let name = jsquery::identifier_text(&name_node)
            .unwrap_or_default()
            .trim()
            .to_owned();
        if name.is_empty() {
            continue;
        }
        let kind = component_setup_binding_kind(&declaration);
        let (member_type, source_expression, open_runtime_shape) =
            component_setup_binding_inference(&declaration, kind, &known);
        let cms_registry_kind =
            cms_registry_kind_from_expression(&declaration.text().to_string()).unwrap_or_default();
        if !member_type.is_empty() {
            known.insert(name.clone(), member_type.clone());
        }
        bindings.insert(
            name,
            ComponentSetupBinding {
                kind,
                member_type,
                source_expression,
                open_runtime_shape,
                cms_registry_kind,
            },
        );
    }
    for function in jsquery::nodes(setup, &[jssyntax::JS_FUNCTION]) {
        if !owned_by_setup(&function) {
            continue;
        }
        let Some(name_node) = first_direct_identifier(&function) else {
            continue;
        };
        let name = jsquery::identifier_text(&name_node)
            .unwrap_or_default()
            .trim()
            .to_owned();
        if name.is_empty() {
            continue;
        }
        let cms_registry_kind =
            cms_registry_kind_from_expression(&function.text().to_string()).unwrap_or_default();
        bindings.insert(
            name,
            ComponentSetupBinding {
                kind: VueComponentMemberKind::Method,
                member_type: vue_method_signature(&function, Some(&known)),
                source_expression: vue_method_return_expression(&function),
                cms_registry_kind,
                ..Default::default()
            },
        );
    }
    bindings
}

pub(crate) fn component_setup_binding_kind(declaration: &SyntaxNode) -> VueComponentMemberKind {
    let compact = compact_javascript_text(&declaration.text().to_string());
    if compact.contains("=computed(") || compact.contains("=computed<") {
        return VueComponentMemberKind::Computed;
    }
    let initializer = component_setup_initializer(declaration);
    let initializer = initializer.trim();
    let function_initializer = initializer.starts_with("function")
        || initializer.starts_with("async function")
        || index_vue_top_level_operator(initializer, "=>").is_some();
    if function_initializer {
        return VueComponentMemberKind::Method;
    }
    VueComponentMemberKind::Data
}

/// Member type, source expression and open-runtime-shape flag of one setup
/// declaration.
pub(crate) fn component_setup_binding_inference(
    declaration: &SyntaxNode,
    kind: VueComponentMemberKind,
    known: &HashMap<String, String>,
) -> (String, String, bool) {
    let store_kind = match kind {
        VueComponentMemberKind::Computed => AdminStoreMemberKind::Getter,
        VueComponentMemberKind::Method => AdminStoreMemberKind::Action,
        _ => AdminStoreMemberKind::State,
    };
    let mut member_type =
        unwrap_vue_setup_ref_type(&setup_store_binding_type(declaration, store_kind));
    let initializer = component_setup_initializer(declaration);
    if initializer.is_empty() {
        return (member_type, String::new(), false);
    }
    let mut source_expression = initializer.clone();
    let mut open_runtime_shape = false;
    let compact_initializer = compact_javascript_text(&initializer);
    for call in jsquery::iter_calls(declaration, &[]) {
        let call_name = jsquery::call_name(&call).unwrap_or_default();
        if !matches!(
            call_name.as_str(),
            "ref" | "shallowRef" | "reactive" | "computed"
        ) {
            continue;
        }
        if !compact_initializer.starts_with(&format!("{call_name}("))
            && !compact_initializer.starts_with(&format!("{call_name}<"))
        {
            continue;
        }
        let Some(argument) = jsquery::argument_expression(&call, 0) else {
            break;
        };
        if call_name == "computed" {
            source_expression = vue_method_return_expression(&argument);
            let inferred = vue_method_return_type(&argument, Some(known));
            if !inferred.is_empty() {
                member_type = inferred;
            } else {
                let inferred = vue_expression_text_type(
                    &unwrap_component_setup_expression(&source_expression),
                    Some(known),
                );
                if !inferred.is_empty() {
                    member_type = inferred;
                }
            }
            open_runtime_shape = vue_runtime_object_literal_expression(&source_expression);
        } else {
            source_expression = trim_vue_source_expression(&argument.text().to_string());
            let inferred = vue_expression_type(&argument, Some(known));
            if !inferred.is_empty() {
                member_type = inferred;
            }
            open_runtime_shape = argument.kind() == jssyntax::JS_OBJECT;
        }
        break;
    }
    if kind == VueComponentMemberKind::Method
        && let Some(function) = declaration.descendants().find(|node| {
            matches!(
                node.kind(),
                jssyntax::JS_ARROW_FUNCTION | jssyntax::JS_FUNCTION
            )
        })
    {
        member_type = vue_method_signature(&function, Some(known));
        source_expression = vue_method_return_expression(&function);
        open_runtime_shape = vue_runtime_object_literal_expression(&source_expression);
    }
    (member_type, source_expression, open_runtime_shape)
}

pub(crate) fn unwrap_component_setup_expression(value: &str) -> String {
    let mut value = trim_vue_source_expression(value);
    while value.len() >= 2
        && value.starts_with('(')
        && matching_slot_delimiter(&value, 0, b'(', b')') == Some(value.len() - 1)
    {
        value = value[1..value.len() - 1].trim().to_owned();
    }
    value
}

pub(crate) fn component_setup_initializer(declaration: &SyntaxNode) -> String {
    let full_text = declaration.text().to_string();
    let text = full_text.trim();
    let Some(equals) = text.find('=') else {
        return String::new();
    };
    trim_vue_source_expression(&text[equals + 1..])
}

pub(crate) fn unwrap_vue_setup_ref_type(value: &str) -> String {
    let value = value.trim();
    for wrapper in ["Ref", "ShallowRef", "ComputedRef", "WritableComputedRef"] {
        let prefix = format!("{wrapper}<");
        if !value.starts_with(&prefix) {
            continue;
        }
        if matching_slot_delimiter(value, wrapper.len(), b'<', b'>') == Some(value.len() - 1) {
            return value[prefix.len()..value.len() - 1].trim().to_owned();
        }
    }
    value.to_owned()
}
