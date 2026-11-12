//! Services, stores, directives, filters and CMS registrations.

use std::sync::LazyLock;

use regex::Regex;

use crate::*;

string_enum! {
    pub enum AdminServiceRegistrationKind {
        Provider => "provider",
        Factory => "factory",
    }
}

/// A service registered through `Application.addServiceProvider` or
/// `Service().register`.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminService {
    pub name: String,
    pub kind: AdminServiceRegistrationKind,
    pub file_path: String,
    pub line: u32,
    pub implementation_name: String,
    pub implementation_path: String,
}

string_enum! {
    pub enum AdminCMSRegistrationKind {
        Element => "element",
        Block => "block",
    }
}

/// One statically registered Shopping Experiences element or block.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminCMSRegistration {
    pub kind: AdminCMSRegistrationKind,
    pub name: String,
    pub label: String,
    pub category: String,
    pub component: String,
    pub config_component: String,
    pub preview_component: String,
    pub slots: Vec<AdminCMSReference>,
    pub file_path: String,
    pub line: u32,
    pub name_range: AdminSourceRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminCMSReference {
    pub name: String,
    pub range: AdminSourceRange,
}

pub fn admin_cms_key(kind: AdminCMSRegistrationKind, name: &str) -> String {
    format!("{}\x00{}", kind.as_str(), name)
}

string_enum! {
    pub enum AdminStoreMemberKind {
        State => "state",
        Getter => "getter",
        Action => "action",
    }
}

/// A Pinia store registered through `Shopware.Store.register`.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminStore {
    pub name: String,
    pub file_path: String,
    pub line: u32,
    pub state_type: String,
    pub factory_name: String,
    pub factory_path: String,
    pub members: Vec<AdminStoreMember>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminStoreMember {
    pub name: String,
    pub kind: AdminStoreMemberKind,
    #[serde(rename = "type")]
    pub type_: String,
    pub open_runtime_shape: bool,
    pub file_path: String,
    pub line: u32,
}

/// The setup-style function used by a registered Pinia store, indexed
/// independently from the registration.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminStoreFactory {
    pub file_path: String,
    pub members: Vec<AdminStoreMember>,
}

impl AdminStore {
    pub fn member(&self, name: &str) -> Option<&AdminStoreMember> {
        self.members.iter().find(|member| member.name == name)
    }
}

static SERVICE_IMPLEMENTATION_PATTERNS: LazyLock<Vec<Regex>> = LazyLock::new(|| {
    [
        r"(?-u:\b)return\s+new\s+([A-Za-z_$][A-Za-z0-9_$]*)",
        r"=>\s*new\s+([A-Za-z_$][A-Za-z0-9_$]*)",
        r"(?-u:\b)return\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*\(",
        r"=>\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\(",
        r"(?-u:\b)return\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*;",
    ]
    .iter()
    .map(|pattern| Regex::new(pattern).expect("valid service implementation pattern"))
    .collect()
});

/// 1-based line of the first non-trivia byte of `node`.
fn node_line(node: &SyntaxNode, line_index: &LineIndex) -> u32 {
    line_index
        .position(u32::from(node.range_trimmed_trivia().start()))
        .0
        + 1
}

fn property_value_of(object: Option<&SyntaxNode>, name: &str) -> Option<SyntaxNode> {
    jsquery::property(object?, name).and_then(|property| jsquery::property_value(&property))
}

fn string_value_of(node: Option<&SyntaxNode>) -> String {
    node.map(jsquery::string_value).unwrap_or_default()
}

pub(crate) fn parse_admin_runtime_registries(
    root: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> (Vec<AdminService>, Vec<AdminStore>) {
    let mut services = Vec::new();
    let mut stores = Vec::new();
    for call in jsquery::iter_calls(root, &[]) {
        if let Some(kind) = service_registration_kind(&call) {
            let name_node = jsquery::string_argument(&call, 0);
            let name = string_value_of(name_node.as_ref());
            if let (false, Some(name_node)) = (name.is_empty(), name_node) {
                let mut service = AdminService {
                    name,
                    kind,
                    file_path: file_path.to_owned(),
                    line: node_line(&name_node, line_index),
                    ..Default::default()
                };
                service.implementation_name = service_implementation_name(&call);
                if !service.implementation_name.is_empty()
                    && let Some(import_path) =
                        jsquery::import_path(root, &service.implementation_name)
                    && !import_path.is_empty()
                {
                    service.implementation_path = resolve_import_path(file_path, &import_path);
                }
                services.push(service);
            }
        }
        if let Some(store) = parse_store_registration(root, &call, file_path, line_index) {
            stores.push(store);
        }
    }
    (services, stores)
}

pub(crate) fn parse_admin_directives(
    root: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> Vec<AdminDirective> {
    let mut result = Vec::new();
    for call in jsquery::iter_calls(root, &["Directive.register", "Shopware.Directive.register"]) {
        let Some(name_node) = jsquery::string_argument(&call, 0) else {
            continue;
        };
        let name = jsquery::string_value(&name_node);
        if name.is_empty() {
            continue;
        }
        result.push(AdminDirective {
            name,
            file_path: file_path.to_owned(),
            line: node_line(&name_node, line_index),
            ..Default::default()
        });
    }
    result
}

pub(crate) fn parse_admin_filters(
    root: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> Vec<AdminFilter> {
    let mut result = Vec::new();
    for call in jsquery::iter_calls(root, &["Filter.register", "Shopware.Filter.register"]) {
        let Some(name_node) = jsquery::string_argument(&call, 0) else {
            continue;
        };
        let name = jsquery::string_value(&name_node);
        if name.is_empty() {
            continue;
        }
        let signature = jsquery::argument_expression(&call, 1)
            .map(|expression| vue_method_signature(&expression, &HashMap::new()))
            .unwrap_or_default();
        result.push(AdminFilter {
            name,
            file_path: file_path.to_owned(),
            line: node_line(&name_node, line_index),
            name_range: component_member_name_range(Some(&name_node), line_index),
            signature,
        });
    }
    result
}

pub(crate) fn parse_admin_cms_registrations(
    root: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> Vec<AdminCMSRegistration> {
    let mut result = Vec::new();
    for call in jsquery::iter_calls(root, &[]) {
        let kind = match jsquery::call_method_name(&call).as_deref() {
            Some("registerCmsElement") => AdminCMSRegistrationKind::Element,
            Some("registerCmsBlock") => AdminCMSRegistrationKind::Block,
            _ => continue,
        };
        let config = jsquery::object_argument(&call, 0);
        let Some(name_node) = property_value_of(config.as_ref(), "name") else {
            continue;
        };
        let mut name = jsquery::string_value(&name_node);
        if name.is_empty() {
            // `const NAME = 'stat-row'; … name: NAME`.
            name = jsquery::identifier_text(&name_node)
                .and_then(|identifier| constant_string_value(root, &identifier))
                .unwrap_or_default();
        }
        if name.is_empty() {
            continue;
        }
        let string_prop = |property: &str| {
            config
                .as_ref()
                .map(|config| string_property(config, property))
                .unwrap_or_default()
        };
        let mut registration = AdminCMSRegistration {
            kind,
            name,
            file_path: file_path.to_owned(),
            line: node_line(&name_node, line_index),
            name_range: component_member_name_range(Some(&name_node), line_index),
            label: string_prop("label"),
            category: string_prop("category"),
            component: string_prop("component"),
            config_component: string_prop("configComponent"),
            preview_component: string_prop("previewComponent"),
            slots: Vec::new(),
        };
        if kind == AdminCMSRegistrationKind::Block
            && let Some(slots) = property_value_of(config.as_ref(), "slots")
            && slots.kind() == jssyntax::JS_OBJECT
        {
            for property in jsquery::properties(&slots) {
                let mut reference = jsquery::property_value(&property);
                if let Some(value) = reference.as_ref()
                    && value.kind() == jssyntax::JS_OBJECT
                {
                    reference = property_value_of(Some(value), "type");
                }
                let Some(reference) = reference else {
                    continue;
                };
                let name = jsquery::string_value(&reference);
                if name.is_empty() {
                    continue;
                }
                registration.slots.push(AdminCMSReference {
                    name,
                    range: component_member_name_range(Some(&reference), line_index),
                });
            }
        }
        result.push(registration);
    }
    result
}

pub(crate) fn preferred_cms_registrations(
    values: &[AdminCMSRegistration],
) -> Vec<AdminCMSRegistration> {
    prefer_runtime_definitions(values, |value| &value.file_path)
}

pub(crate) fn service_implementation_name(call: &SyntaxNode) -> String {
    let arguments: Vec<SyntaxNode> = jsquery::iter_arguments(call).collect();
    if arguments.len() < 2 {
        return String::new();
    }
    // The shallow JavaScript parser can recover `() => new X()` as two
    // argument nodes around `new`; inspect the complete factory tail.
    let mut text = String::new();
    for argument in &arguments[1..] {
        text.push_str(&argument.text_string());
    }
    for pattern in SERVICE_IMPLEMENTATION_PATTERNS.iter() {
        if let Some(captures) = pattern.captures(&text)
            && let Some(name) = captures.get(1)
        {
            return name.as_str().to_owned();
        }
    }
    String::new()
}

pub(crate) fn service_registration_kind(call: &SyntaxNode) -> Option<AdminServiceRegistrationKind> {
    let name = jsquery::call_name(call).unwrap_or_default();
    match jsquery::call_method_name(call).as_deref() {
        Some("addServiceProvider") if name.ends_with(".addServiceProvider") => {
            Some(AdminServiceRegistrationKind::Provider)
        }
        Some("register") if name.ends_with("Service().register") => {
            Some(AdminServiceRegistrationKind::Factory)
        }
        _ => None,
    }
}

pub(crate) fn parse_store_registration(
    root: &SyntaxNode,
    call: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> Option<AdminStore> {
    let name = jsquery::call_name(call).unwrap_or_default();
    if name != "Shopware.Store.register" && name != "Store.register" {
        return None;
    }
    let mut name_node = None;
    let mut options = None;
    let first = jsquery::argument_expression(call, 0);
    if let Some(first) = first {
        if first.kind() == jssyntax::JS_STRING {
            name_node = Some(first);
            options = jsquery::argument_expression(call, 1);
        } else if first.kind() == jssyntax::JS_OBJECT {
            if let Some(id) = jsquery::property(&first, "id") {
                name_node = jsquery::property_value(&id);
            }
            options = Some(first);
        }
    }
    let store_name = string_value_of(name_node.as_ref());
    if store_name.is_empty() {
        return None;
    }
    let name_node = name_node?;
    let mut store = AdminStore {
        name: store_name,
        file_path: file_path.to_owned(),
        line: node_line(&name_node, line_index),
        ..Default::default()
    };
    if let Some(options) = options {
        if options.kind() == jssyntax::JS_OBJECT {
            if let Some(state) = jsquery::property(&options, "state") {
                store.state_type = vue_method_declared_return_type(&state);
            }
            store.members = parse_store_members(&options, file_path, line_index);
        } else if options.kind() == jssyntax::JS_IDENTIFIER {
            store.factory_name = jsquery::identifier_text(&options).unwrap_or_default();
            if let Some(import_path) = jsquery::import_path(root, &store.factory_name)
                && !import_path.is_empty()
            {
                store.factory_path = resolve_import_path(file_path, &import_path);
            }
        }
    }
    Some(store)
}

pub(crate) fn parse_admin_store_factory(
    root: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> Option<AdminStoreFactory> {
    for export in jsquery::export_defaults(root) {
        let Some(function) = jsquery::export_default_expression(&export) else {
            continue;
        };
        if function.kind() != jssyntax::JS_FUNCTION
            && function.kind() != jssyntax::JS_ARROW_FUNCTION
        {
            continue;
        }
        let Some(returned) = returned_setup_object(&function) else {
            continue;
        };
        return Some(AdminStoreFactory {
            file_path: file_path.to_owned(),
            members: parse_setup_store_members(root, &returned, file_path, line_index),
        });
    }
    None
}

pub(crate) fn returned_setup_object(function: &SyntaxNode) -> Option<SyntaxNode> {
    let mut arrow_fallback = None;
    let text = function.text_string();
    let function_start = u32::from(function.text_range().start());
    for object in jsquery::nodes(function, &[jssyntax::JS_OBJECT]) {
        if closest_javascript_function(&object).as_ref() != Some(function) {
            continue;
        }
        let start =
            u32::from(object.range_trimmed_trivia().start()).wrapping_sub(function_start) as usize;
        let Some(before) = text.get(..start) else {
            continue;
        };
        if before.trim().ends_with("return") {
            return Some(object);
        }
        if function.kind() == jssyntax::JS_ARROW_FUNCTION && arrow_fallback.is_none() {
            arrow_fallback = Some(object);
        }
    }
    arrow_fallback
}

pub(crate) fn closest_javascript_function(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.ancestors().skip(1).find(|current| {
        matches!(
            current.kind(),
            jssyntax::JS_FUNCTION | jssyntax::JS_ARROW_FUNCTION | jssyntax::JS_METHOD
        )
    })
}

#[derive(Clone, Debug)]
pub(crate) struct SetupBinding {
    pub kind: AdminStoreMemberKind,
    pub line: u32,
    pub value_type: String,
    pub object: Option<SyntaxNode>,
}

pub(crate) fn parse_setup_store_members(
    root: &SyntaxNode,
    returned: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> Vec<AdminStoreMember> {
    let bindings = setup_bindings(root, line_index);
    let mut members: Vec<AdminStoreMember> = Vec::new();
    let mut positions: HashMap<String, usize> = HashMap::new();
    let mut add = |member: AdminStoreMember| {
        if member.name.is_empty() {
            return;
        }
        if let Some(&position) = positions.get(&member.name) {
            members[position] = member;
            return;
        }
        positions.insert(member.name.clone(), members.len());
        members.push(member);
    };
    for property in jsquery::properties(returned) {
        let name = jsquery::property_name(&property).unwrap_or_default();
        if name.is_empty() {
            continue;
        }
        if property.text_string().contains("...") {
            let Some(object) = bindings
                .get(&name)
                .and_then(|binding| binding.object.as_ref())
            else {
                continue;
            };
            for state_property in jsquery::properties(object) {
                add(AdminStoreMember {
                    name: jsquery::property_name(&state_property).unwrap_or_default(),
                    kind: AdminStoreMemberKind::State,
                    type_: vue_property_value_type(&state_property, &HashMap::new()),
                    file_path: file_path.to_owned(),
                    line: node_line(&state_property, line_index),
                    ..Default::default()
                });
            }
            continue;
        }
        let mut binding_name = name.clone();
        if let Some(value) = jsquery::property_value(&property)
            && value.kind() == jssyntax::JS_IDENTIFIER
        {
            binding_name = jsquery::identifier_text(&value).unwrap_or_default();
        }
        let mut member = AdminStoreMember {
            name,
            kind: AdminStoreMemberKind::State,
            file_path: file_path.to_owned(),
            line: node_line(&property, line_index),
            ..Default::default()
        };
        if property.kind() == jssyntax::JS_METHOD {
            member.kind = AdminStoreMemberKind::Action;
        }
        if let Some(binding) = bindings.get(&binding_name) {
            member.kind = binding.kind;
            member.line = binding.line;
            member.type_ = binding.value_type.clone();
        }
        add(member);
    }
    members
}

pub(crate) fn setup_bindings(
    root: &SyntaxNode,
    line_index: &LineIndex,
) -> HashMap<String, SetupBinding> {
    let mut bindings = HashMap::new();
    for function in jsquery::nodes(root, &[jssyntax::JS_FUNCTION]) {
        let Some(name_node) = first_direct_identifier(&function) else {
            continue;
        };
        let name = jsquery::identifier_text(&name_node).unwrap_or_default();
        bindings.insert(
            name,
            SetupBinding {
                kind: AdminStoreMemberKind::Action,
                line: node_line(&name_node, line_index),
                value_type: vue_method_signature(&function, &HashMap::new()),
                object: None,
            },
        );
    }
    for declaration in jsquery::nodes(root, &[jssyntax::JS_VARIABLE_DECLARATION]) {
        let Some(name_node) = first_direct_identifier(&declaration) else {
            continue;
        };
        let name = jsquery::identifier_text(&name_node).unwrap_or_default();
        let text = compact_javascript_text(&declaration.text_string());
        let kind = if text.contains("=computed(") || text.contains("=computed<") {
            AdminStoreMemberKind::Getter
        } else if text.contains("=>") || text.contains("=function") {
            AdminStoreMemberKind::Action
        } else {
            AdminStoreMemberKind::State
        };
        bindings.insert(
            name,
            SetupBinding {
                kind,
                line: node_line(&name_node, line_index),
                value_type: setup_store_binding_type(&declaration, kind),
                object: first_object(&declaration),
            },
        );
    }
    bindings
}

pub(crate) fn setup_store_binding_type(
    declaration: &SyntaxNode,
    kind: AdminStoreMemberKind,
) -> String {
    let full = declaration.text_string();
    let text = full.trim();
    let Some(equals) = text.find('=') else {
        return String::new();
    };
    let left = text[..equals].trim();
    if let Some(colon) = left.rfind(':') {
        let declared = left[colon + 1..].trim();
        if !declared.is_empty() {
            return declared.to_owned();
        }
    }
    let right = trim_vue_source_expression(&text[equals + 1..]);
    for wrapper in ["ref", "shallowRef", "computed", "reactive"] {
        let compact = right.trim();
        if !compact.starts_with(wrapper) || !compact[wrapper.len()..].starts_with('<') {
            continue;
        }
        let open = wrapper.len();
        if let Some(close) = matching_slot_delimiter(compact, open, b'<', b'>')
            && close > open
        {
            return compact[open + 1..close].trim().to_owned();
        }
    }
    if kind == AdminStoreMemberKind::Action
        && let Some((parameters, return_type)) = vue_method_header(&right)
    {
        let return_type = if return_type.is_empty() {
            "unknown".to_owned()
        } else {
            return_type
        };
        return format!("({parameters}) => {return_type}");
    }
    if let Some(open) = right.find('(') {
        let name = right[..open].trim();
        if (name == "ref" || name == "shallowRef" || name == "reactive")
            && let Some(close) = matching_slot_delimiter(&right, open, b'(', b')')
            && close > open
        {
            return vue_expression_text_type(right[open + 1..close].trim(), &HashMap::new());
        }
        if name == "computed"
            && let Some(arrow) = right[open + 1..].find("=>")
        {
            let body = right[open + 1 + arrow + 2..].trim();
            let body = body.strip_suffix(')').unwrap_or(body);
            return vue_expression_text_type(body, &HashMap::new());
        }
    }
    vue_expression_text_type(&right, &HashMap::new())
}

pub(crate) fn first_direct_identifier(node: &SyntaxNode) -> Option<SyntaxNode> {
    let mut child = node
        .children()
        .find(|child| child.kind() == jssyntax::JS_IDENTIFIER)?;
    // Type annotations wrap the declared identifier together with its type
    // (`state: ContextState`); follow the leading identifier node.
    loop {
        match child
            .children()
            .find(|candidate| candidate.kind() == jssyntax::JS_IDENTIFIER)
        {
            Some(nested) => child = nested,
            None => return Some(child),
        }
    }
}

pub(crate) fn compact_javascript_text(value: &str) -> String {
    value.split_whitespace().collect()
}

pub(crate) fn merge_store_members(
    base: &[AdminStoreMember],
    overlay: &[AdminStoreMember],
) -> Vec<AdminStoreMember> {
    let mut result = base.to_vec();
    let mut positions: HashMap<String, usize> = HashMap::with_capacity(result.len());
    for (index, member) in result.iter().enumerate() {
        positions.insert(member.name.clone(), index);
    }
    for member in overlay {
        if let Some(&index) = positions.get(&member.name) {
            result[index] = member.clone();
        } else {
            positions.insert(member.name.clone(), result.len());
            result.push(member.clone());
        }
    }
    result
}

pub(crate) fn parse_store_members(
    options: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> Vec<AdminStoreMember> {
    let sections = [
        ("state", AdminStoreMemberKind::State),
        ("getters", AdminStoreMemberKind::Getter),
        ("actions", AdminStoreMemberKind::Action),
    ];
    let mut members = Vec::new();
    let mut seen: HashSet<String> = HashSet::new();
    let mut known: HashMap<String, String> = HashMap::new();
    for (section, kind) in sections {
        let Some(object) =
            jsquery::property(options, section).and_then(|property| first_object(&property))
        else {
            continue;
        };
        for member_property in jsquery::properties(&object) {
            let name = jsquery::property_name(&member_property).unwrap_or_default();
            let key = format!("{}\x00{}", kind.as_str(), name);
            if name.is_empty() || seen.contains(&key) {
                continue;
            }
            seen.insert(key);
            let member_type = match kind {
                AdminStoreMemberKind::State => vue_property_value_type(&member_property, &known),
                AdminStoreMemberKind::Getter => vue_method_return_type(&member_property, &known),
                _ => vue_method_signature(&member_property, &known),
            };
            if !member_type.is_empty() {
                known.insert(name.clone(), member_type.clone());
            }
            members.push(AdminStoreMember {
                name,
                kind,
                type_: member_type,
                open_runtime_shape: kind == AdminStoreMemberKind::State
                    && vue_property_has_inferred_object_shape(&member_property),
                file_path: file_path.to_owned(),
                line: node_line(&member_property, line_index),
            });
        }
    }
    members
}

pub(crate) fn preferred_services(values: &[AdminService]) -> Vec<AdminService> {
    prefer_runtime_definitions(values, |value| &value.file_path)
}

pub(crate) fn preferred_stores(values: &[AdminStore]) -> Vec<AdminStore> {
    prefer_runtime_definitions(values, |value| &value.file_path)
}

/// Drops test-only definitions when a production definition exists and
/// orders the rest by file path.
pub(crate) fn prefer_runtime_definitions<T: Clone>(
    values: &[T],
    file_path: impl Fn(&T) -> &str,
) -> Vec<T> {
    if values.len() < 2 {
        return values.to_vec();
    }
    let has_production = values
        .iter()
        .any(|value| !is_admin_test_path(file_path(value)));
    let mut result: Vec<T> = values
        .iter()
        .filter(|value| !(has_production && is_admin_test_path(file_path(value))))
        .cloned()
        .collect();
    result.sort_by(|left, right| file_path(left).cmp(file_path(right)));
    result
}

pub(crate) fn is_admin_test_path(file_path: &str) -> bool {
    let base = file_path.to_lowercase();
    base.contains(".spec.") || base.contains(".test.") || base.contains("/__tests__/")
}

/// The string a top-level `const NAME = '…'` of the file holds.
fn constant_string_value(root: &SyntaxNode, identifier: &str) -> Option<String> {
    jsquery::nodes(root, &[jssyntax::JS_VARIABLE_DECLARATION])
        .into_iter()
        .find(|declaration| {
            declaration
                .children()
                .find(|child| child.kind() == jssyntax::JS_IDENTIFIER)
                .and_then(|name| jsquery::identifier_text(&name))
                .is_some_and(|name| name == identifier)
        })
        .and_then(|declaration| {
            declaration
                .children()
                .find(|child| child.kind() == jssyntax::JS_STRING)
        })
        .map(|string| jsquery::string_value(&string))
        .filter(|value| !value.is_empty())
}
