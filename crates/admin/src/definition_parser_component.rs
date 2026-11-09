//! Component definition parsing entry points.

use crate::*;

/// The parsed public definition of a component.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ComponentDefinition {
    pub file_path: String,
    pub deprecated: String,
    pub props: Vec<VueComponentProp>,
    pub model_prop: String,
    pub model_event: String,
    pub emits: Vec<String>,
    pub events: Vec<VueComponentEvent>,
    pub methods: Vec<String>,
    pub computed: Vec<String>,
    pub data: Vec<String>,
    pub injected: Vec<String>,
    pub mixins: Vec<String>,
    pub local_components: Vec<VueLocalComponent>,
    pub local_directives: Vec<VueLocalDirective>,
    pub members: Vec<VueComponentMember>,
    pub open_runtime_members: bool,
    pub assignments: Vec<VueComponentAssignment>,
    pub slots: Vec<VueComponentSlot>,
    pub blocks: Vec<TwigBlock>,
    pub template_path: String,
    pub has_template: bool,
    /// Lexical type arguments of Vue compiler macros, resolved lazily.
    pub script_setup_prop_types: Vec<String>,
    pub script_setup_event_types: Vec<String>,
    pub script_setup_slot_types: Vec<String>,
    pub script_setup_prop_defaults: Vec<ScriptSetupPropDefault>,
    pub script_setup_prop_bindings: Vec<ScriptSetupPropBinding>,
}

/// One `withDefaults` entry of a typed `defineProps` contract.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct ScriptSetupPropDefault {
    pub name: String,
    pub value: String,
}

/// Maps one public prop to the local identifier created by reactive props
/// destructuring.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct ScriptSetupPropBinding {
    pub prop_name: String,
    pub binding_name: String,
    pub default: String,
    pub line: u32,
    pub name_range: AdminSourceRange,
}

/// Extracts a Vue component's public definition from an export-default
/// object.
pub fn parse_component_definition(root: &SyntaxNode, content: &str) -> ComponentDefinition {
    parse_component_definition_with_line_index(root, &LineIndex::new(content))
}

pub fn parse_component_definition_with_line_index(
    root: &SyntaxNode,
    line_index: &LineIndex,
) -> ComponentDefinition {
    let mut definition = ComponentDefinition::default();
    let exports = jsquery::export_defaults(root);
    let Some(export) = exports.first() else {
        return definition;
    };
    definition.deprecated = javascript_deprecation(export);

    let Some(object) = jsquery::export_default_expression(export)
        .and_then(|expression| component_definition_object(&expression))
    else {
        return definition;
    };

    parse_definition_object(&object, &mut definition, line_index);
    let annotations = javascript_event_annotations(export, line_index);
    let has_emits = jsquery::property(&object, "emits").is_some();
    merge_javascript_event_annotations(&mut definition, &annotations, has_emits);
    enrich_local_component_imports(root, &mut definition);
    definition.template_path = jsquery::import_path(root, "template").unwrap_or_default();
    definition
}

/// Unwraps the definition forms used by current Administration code:
/// object literals, `defineComponent` and Shopware's `wrapComponentConfig`.
pub(crate) fn component_definition_object_impl(expression: &SyntaxNode) -> Option<SyntaxNode> {
    if expression.kind() == jssyntax::JS_OBJECT {
        return Some(expression.clone());
    }
    if expression.kind() != jssyntax::JS_CALL_EXPRESSION {
        return None;
    }
    match jsquery::call_name(expression).as_deref() {
        Some(
            "defineComponent"
            | "Vue.defineComponent"
            | "Component.wrapComponentConfig"
            | "Shopware.Component.wrapComponentConfig",
        ) => jsquery::object_argument(expression, 0),
        _ => None,
    }
}

/// Unwraps the component-definition expression forms used by Shopware and
/// Vue, exposing the lossless source node.
pub fn component_definition_object(expression: &SyntaxNode) -> Option<SyntaxNode> {
    component_definition_object_impl(expression)
}

/// Normalizes one live Options API object without reading its imported
/// template.
pub fn parse_component_object(
    object: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> Option<ComponentDefinition> {
    if object.kind() != jssyntax::JS_OBJECT {
        return None;
    }
    let mut definition = ComponentDefinition {
        file_path: file_path.to_owned(),
        ..Default::default()
    };
    parse_definition_object(object, &mut definition, line_index);
    set_definition_file_path(&mut definition, file_path);
    Some(definition)
}

fn node_text_contains(node: Option<&SyntaxNode>, needle: &str) -> bool {
    node.is_some_and(|node| node.text().to_string().contains(needle))
}

pub(crate) fn parse_definition_object(
    object: &SyntaxNode,
    definition: &mut ComponentDefinition,
    line_index: &LineIndex,
) {
    for property in jsquery::properties(object) {
        let name = jsquery::property_name(&property).unwrap_or_default();
        let value = jsquery::property_value(&property);
        let property_text = property.text().to_string();
        if name.is_empty() && property_text.trim().starts_with("...") {
            definition.open_runtime_members = true;
        }
        match name.as_str() {
            "props" => {
                definition.open_runtime_members =
                    definition.open_runtime_members || node_text_contains(value.as_ref(), "...");
                definition.props = value
                    .as_ref()
                    .map(|value| parse_props(value, line_index))
                    .unwrap_or_default();
                let props = definition.props.clone();
                definition.members.extend(member_definitions(
                    Some(&property),
                    &props,
                    VueComponentMemberKind::Prop,
                    line_index,
                ));
            }
            "model" => {
                (definition.model_prop, definition.model_event) =
                    parse_component_model(value.as_ref());
            }
            "emits" => {
                if let Some(value) = &value {
                    for event in parse_event_declarations(value, line_index) {
                        let event_name = event.name.clone();
                        append_component_event(&mut definition.events, event);
                        append_unique(&mut definition.emits, &event_name);
                    }
                }
            }
            "methods" | "computed" => {
                definition.open_runtime_members =
                    definition.open_runtime_members || node_text_contains(value.as_ref(), "...");
                let names = parse_method_names(value.as_ref());
                let kind = if name == "methods" {
                    definition.methods = names.clone();
                    VueComponentMemberKind::Method
                } else {
                    definition.computed = names.clone();
                    VueComponentMemberKind::Computed
                };
                definition.members.extend(member_definitions(
                    Some(&property),
                    &names,
                    kind,
                    line_index,
                ));
            }
            "data" => {
                definition.open_runtime_members =
                    definition.open_runtime_members || property_text.contains("...");
                definition.data = parse_data_names(&property);
                let names = definition.data.clone();
                definition.members.extend(member_definitions(
                    Some(&property),
                    &names,
                    VueComponentMemberKind::Data,
                    line_index,
                ));
            }
            "inject" => {
                definition.injected = parse_inject_names(value.as_ref());
                let names = definition.injected.clone();
                definition.members.extend(member_definitions(
                    Some(&property),
                    &names,
                    VueComponentMemberKind::Inject,
                    line_index,
                ));
            }
            "setup" => {
                definition.open_runtime_members =
                    definition.open_runtime_members || property_text.contains("...");
                definition
                    .members
                    .extend(parse_component_setup_members(&property, line_index));
            }
            "mixins" => {
                definition.mixins = parse_mixin_names(value.as_ref());
                if definition.mixins.is_empty() {
                    definition.open_runtime_members = true;
                }
            }
            "components" => {
                definition.local_components = value
                    .as_ref()
                    .map(|value| parse_local_components(value, line_index))
                    .unwrap_or_default();
            }
            "directives" => {
                definition.local_directives = value
                    .as_ref()
                    .map(|value| parse_local_directives(value, line_index))
                    .unwrap_or_default();
            }
            "template" => definition.has_template = true,
            _ => {}
        }
    }
    for event in parse_emitted_events(object, line_index) {
        let event_name = event.name.clone();
        append_component_event(&mut definition.events, event);
        append_unique(&mut definition.emits, &event_name);
    }
    definition.assignments = parse_component_assignments(object, line_index);
    enrich_definition_member_types(object, definition);
    enrich_definition_collection_element_members(object, definition, line_index);
}

pub(crate) fn parse_component_model(node: Option<&SyntaxNode>) -> (String, String) {
    let Some(node) = node.filter(|node| node.kind() == jssyntax::JS_OBJECT) else {
        return (String::new(), String::new());
    };
    let value_of = |name: &str| {
        jsquery::property(node, name)
            .and_then(|property| jsquery::property_value(&property))
            .map(|value| jsquery::string_value(&value))
            .unwrap_or_default()
    };
    let mut prop_name = value_of("prop");
    let mut event_name = value_of("event");
    if prop_name.is_empty() {
        prop_name = "value".to_owned();
    }
    if event_name.is_empty() {
        event_name = "input".to_owned();
    }
    (prop_name, event_name)
}
