//! Component, mixin and module registrations in JavaScript (port of Go
//! `indexer_javascript.go`).

use crate::*;

#[allow(dead_code)]
pub(crate) fn parse_component_registrations(
    root: &SyntaxNode,
    content: &str,
    file_path: &str,
) -> Vec<VueComponent> {
    parse_component_registrations_with_line_index(root, file_path, &LineIndex::new(content))
}

const COMPONENT_REGISTRATION_CALLS: &[&str] = &[
    "Shopware.Component.register",
    "Shopware.Component.extend",
    "Shopware.Component.override",
    "Component.register",
    "Component.extend",
    "Component.override",
];

fn string_argument_value(call: &SyntaxNode, index: usize) -> String {
    jsquery::string_argument(call, index)
        .map(|argument| jsquery::string_value(&argument))
        .unwrap_or_default()
}

pub(crate) fn parse_component_registrations_with_line_index(
    root: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> Vec<VueComponent> {
    let mut components: Vec<VueComponent> = Vec::new();
    for call in jsquery::iter_calls(root, COMPONENT_REGISTRATION_CALLS) {
        let name = jsquery::call_name(&call).unwrap_or_default();
        let mut component = VueComponent {
            file_path: file_path.to_owned(),
            line: definition_node_line(line_index, &call),
            kind: ComponentRegistrationKind::Register,
            ..Default::default()
        };
        component.deprecated = component_registration_deprecation(&call);
        component.name = string_argument_value(&call, 0);
        if component.name.is_empty() {
            continue;
        }

        let mut definition_index = 1;
        if name.ends_with(".extend") {
            component.kind = ComponentRegistrationKind::Extend;
            component.extends_component = string_argument_value(&call, 1);
            component.target_component = component.extends_component.clone();
            definition_index = 2;
        } else if name.ends_with(".override") {
            component.kind = ComponentRegistrationKind::Override;
            component.target_component = component.name.clone();
        }
        if let Some(definition) = jsquery::argument_expression(&call, definition_index) {
            match definition.kind() {
                jssyntax::JS_OBJECT => {
                    component.inline_definition =
                        parse_inline_definition(Some(root), &definition, file_path, line_index)
                            .map(Box::new);
                    component.definition_path = file_path.to_owned();
                }
                jssyntax::JS_ARROW_FUNCTION | jssyntax::JS_FUNCTION => {
                    if let Some(import_path) =
                        jsquery::dynamic_import_path(&definition).filter(|path| !path.is_empty())
                    {
                        component.definition_path = resolve_import_path(file_path, &import_path);
                        component.import_path = import_path;
                    }
                }
                jssyntax::JS_CALL_EXPRESSION => {
                    if let Some(object) = component_definition_object_impl(&definition) {
                        component.inline_definition =
                            parse_inline_definition(Some(root), &object, file_path, line_index)
                                .map(Box::new);
                        component.definition_path = file_path.to_owned();
                    }
                }
                _ => {}
            }
        }
        components.push(component);
    }
    for component in parse_vue_application_component_collections(root, file_path, line_index) {
        if !components
            .iter()
            .any(|existing| existing.name == component.name)
        {
            components.push(component);
        }
    }
    components
}

pub(crate) fn component_registration_deprecation(call: &SyntaxNode) -> String {
    for current in call.ancestors() {
        if current.kind() == jssyntax::JS_PROGRAM {
            break;
        }
        let deprecation = javascript_deprecation(&current);
        if !deprecation.is_empty() {
            return deprecation;
        }
    }
    String::new()
}

/// Static component maps registered on the Vue application after
/// normalizing their keys to kebab-case (eager and lazy Meteor components).
pub(crate) fn parse_vue_application_component_collections(
    root: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> Vec<VueComponent> {
    let mut result: Vec<VueComponent> = Vec::new();
    for declaration in jsquery::nodes(root, &[jssyntax::JS_VARIABLE_DECLARATION]) {
        let (Some(name_node), Some(object)) = (
            first_direct_identifier(&declaration),
            first_object(&declaration),
        ) else {
            continue;
        };
        let collection_name = jsquery::identifier_text(&name_node).unwrap_or_default();
        if collection_name.is_empty()
            || !is_vue_application_component_collection(root, &collection_name)
        {
            continue;
        }
        for property in jsquery::properties(&object) {
            let property_name = jsquery::property_name(&property)
                .unwrap_or_default()
                .trim()
                .to_owned();
            if property_name.is_empty() || !is_static_vue_identifier(&property_name) {
                continue;
            }
            let import_path = match jsquery::property_value(&property) {
                // JavaScript shorthand: { MtButton }.
                None => jsquery::import_path(root, &property_name),
                Some(value) => match value.kind() {
                    jssyntax::JS_IDENTIFIER => jsquery::import_path(
                        root,
                        &jsquery::identifier_text(&value).unwrap_or_default(),
                    ),
                    jssyntax::JS_ARROW_FUNCTION | jssyntax::JS_FUNCTION => {
                        jsquery::dynamic_import_path(&value)
                    }
                    _ => continue,
                },
            }
            .unwrap_or_default();
            result.push(VueComponent {
                name: camel_to_kebab(&property_name),
                file_path: file_path.to_owned(),
                definition_path: file_path.to_owned(),
                import_path,
                line: definition_node_line(line_index, &property),
                kind: ComponentRegistrationKind::Register,
                ..Default::default()
            });
        }
    }
    for component in parse_raw_vue_application_component_collections(root, file_path, line_index) {
        if !result
            .iter()
            .any(|existing| existing.name == component.name)
        {
            result.push(component);
        }
    }
    result.sort_by(|left, right| {
        left.line
            .cmp(&right.line)
            .then_with(|| left.name.cmp(&right.name))
    });
    result
}

/// Scans the strongly identified registration shape textually when
/// TypeScript recovery kept a body without nested expression nodes.
pub(crate) fn parse_raw_vue_application_component_collections(
    root: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> Vec<VueComponent> {
    let source = root.text().to_string();
    let collections = raw_vue_application_component_collections(&source);
    let mut result = Vec::new();
    // Go iterates a map here; the caller sorts the combined result.
    for (collection_name, loop_offset) in collections {
        let Some(open) =
            static_vue_collection_object_start(&source[..loop_offset], &collection_name)
        else {
            continue;
        };
        let Some(close) = balanced_brace_end(&source, open) else {
            continue;
        };
        if close <= open || close > loop_offset {
            continue;
        }
        for segment in split_top_level_declarations(&source[open + 1..close], open + 1) {
            let Some(entry) = raw_vue_component_collection_entry(&segment) else {
                continue;
            };
            let line = line_index.position(entry.offset as u32).0 + 1;
            let mut import_path = jsquery::import_path(root, &entry.symbol).unwrap_or_default();
            if import_path.is_empty() {
                import_path = raw_dynamic_import_path(&entry.expression);
            }
            result.push(VueComponent {
                name: camel_to_kebab(&entry.name),
                file_path: file_path.to_owned(),
                definition_path: file_path.to_owned(),
                import_path,
                line,
                kind: ComponentRegistrationKind::Register,
                ..Default::default()
            });
        }
    }
    result
}

/// Collection names passed to `Object.entries(<name>).forEach(...)` with a
/// kebab-case component registration callback, mapped to the loop offset.
/// Sorted by name for determinism.
pub(crate) fn raw_vue_application_component_collections(source: &str) -> BTreeMap<String, usize> {
    let mut result = BTreeMap::new();
    const MARKER: &str = "Object.entries";
    let bytes = source.as_bytes();
    let mut search_offset = 0;
    while search_offset < source.len() {
        let Some(relative) = source[search_offset..].find(MARKER) else {
            break;
        };
        let position = search_offset + relative;
        search_offset = position + MARKER.len();
        if !admin_type_code_position(source, position) {
            continue;
        }
        let cursor = skip_javascript_spaces(source, search_offset);
        if cursor >= bytes.len() || bytes[cursor] != b'(' {
            continue;
        }
        let Some(close) = matching_slot_delimiter(source, cursor, b'(', b')') else {
            continue;
        };
        let collection_name = source[cursor + 1..close].trim();
        if !is_static_vue_identifier(collection_name) {
            continue;
        }
        let cursor = skip_javascript_spaces(source, close + 1);
        if cursor >= bytes.len() || bytes[cursor] != b'.' {
            continue;
        }
        let cursor = skip_javascript_spaces(source, cursor + 1);
        if !source[cursor..].starts_with("forEach") {
            continue;
        }
        let cursor = skip_javascript_spaces(source, cursor + "forEach".len());
        if cursor >= bytes.len() || bytes[cursor] != b'(' {
            continue;
        }
        let Some(callback_close) = matching_slot_delimiter(source, cursor, b'(', b')') else {
            continue;
        };
        let callback = compact_javascript_text(&source[cursor + 1..callback_close]);
        if !callback.contains("kebabCase(")
            || (!callback.contains(".component(") && !callback.contains("registerAsyncComponent("))
        {
            continue;
        }
        result.insert(collection_name.to_owned(), position);
    }
    result
}

pub(crate) fn static_vue_collection_object_start(
    source: &str,
    collection_name: &str,
) -> Option<usize> {
    let bytes = source.as_bytes();
    let mut search_end = source.len();
    while search_end > 0 {
        let position = source[..search_end].rfind(collection_name)?;
        search_end = position;
        let after = position + collection_name.len();
        if !admin_type_code_position(source, position)
            || position > 0 && is_vue_identifier_part(bytes[position - 1])
            || after < bytes.len() && is_vue_identifier_part(bytes[after])
        {
            continue;
        }
        let mut before = position;
        while before > 0 && is_javascript_space(bytes[before - 1]) {
            before -= 1;
        }
        let mut keyword_start = before;
        while keyword_start > 0 && is_vue_identifier_part(bytes[keyword_start - 1]) {
            keyword_start -= 1;
        }
        let keyword = &bytes[keyword_start..before];
        if keyword != b"const" && keyword != b"let" {
            continue;
        }
        let cursor = skip_javascript_spaces(source, after);
        if cursor >= bytes.len() || bytes[cursor] != b'=' {
            continue;
        }
        let cursor = skip_javascript_spaces(source, cursor + 1);
        if cursor < bytes.len() && bytes[cursor] == b'{' {
            return Some(cursor);
        }
    }
    None
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub(crate) struct RawVueComponentEntry {
    pub name: String,
    pub symbol: String,
    pub expression: String,
    pub offset: usize,
}

pub(crate) fn raw_vue_component_collection_entry(
    segment: &DeclarationSegment,
) -> Option<RawVueComponentEntry> {
    let (trimmed, skipped) = trim_declaration_prefix(&segment.text);
    let bytes = trimmed.as_bytes();
    if bytes.is_empty() || !is_vue_identifier_start(bytes[0]) {
        return None;
    }
    let mut end = 1;
    while end < bytes.len() && is_vue_identifier_part(bytes[end]) {
        end += 1;
    }
    let name = trimmed.get(..end)?;
    let rest = trimmed[end..].trim();
    let mut entry = RawVueComponentEntry {
        name: name.to_owned(),
        symbol: name.to_owned(),
        offset: segment.offset + skipped,
        ..Default::default()
    };
    if rest.is_empty() {
        return Some(entry);
    }
    let expression = rest.strip_prefix(':')?.trim();
    if expression.is_empty() {
        return None;
    }
    entry.expression = expression.to_owned();
    entry.symbol = if is_static_vue_identifier(expression) {
        expression.to_owned()
    } else {
        String::new()
    };
    Some(entry)
}

pub(crate) fn raw_dynamic_import_path(expression: &str) -> String {
    let bytes = expression.as_bytes();
    let Some(position) = expression.find("import") else {
        return String::new();
    };
    let cursor = skip_javascript_spaces(expression, position + "import".len());
    if cursor >= bytes.len() || bytes[cursor] != b'(' {
        return String::new();
    }
    let cursor = skip_javascript_spaces(expression, cursor + 1);
    if cursor >= bytes.len() || (bytes[cursor] != b'\'' && bytes[cursor] != b'"') {
        return String::new();
    }
    let quote = bytes[cursor];
    let start = cursor + 1;
    let mut cursor = start;
    while cursor < bytes.len() {
        if bytes[cursor] == b'\\' {
            cursor += 2;
            continue;
        }
        if bytes[cursor] == quote {
            return String::from_utf8_lossy(&bytes[start..cursor]).into_owned();
        }
        cursor += 1;
    }
    String::new()
}

pub(crate) fn skip_javascript_spaces(value: &str, mut cursor: usize) -> usize {
    let bytes = value.as_bytes();
    while cursor < bytes.len() && is_javascript_space(bytes[cursor]) {
        cursor += 1;
    }
    cursor
}

pub(crate) fn is_vue_application_component_collection(
    root: &SyntaxNode,
    collection_name: &str,
) -> bool {
    let wanted_receiver = format!("Object.entries({collection_name})");
    for call in jsquery::iter_calls(root, &[]) {
        let Some(receiver) = static_callback_call_receiver(&call, "forEach") else {
            continue;
        };
        if compact_javascript_text(&receiver) != wanted_receiver {
            continue;
        }
        let Some(callback) = jsquery::argument_expression(&call, 0) else {
            continue;
        };
        let text = compact_javascript_text(&callback.text().to_string());
        if !text.contains("kebabCase(") {
            continue;
        }
        if text.contains(".component(") || text.contains("registerAsyncComponent(") {
            return true;
        }
    }
    false
}

pub(crate) fn parse_mixins_and_modules(
    root: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> (Vec<AdminMixin>, Vec<AdminModule>) {
    let mut mixins = Vec::new();
    for call in jsquery::iter_calls(root, &["Shopware.Mixin.register", "Mixin.register"]) {
        let name = string_argument_value(&call, 0);
        if name.is_empty() {
            continue;
        }
        let mut mixin = AdminMixin {
            name,
            file_path: file_path.to_owned(),
            line: definition_node_line(line_index, &call),
            ..Default::default()
        };
        if let Some(object) = jsquery::argument_expression(&call, 1)
            .and_then(|expression| component_definition_object_impl(&expression))
            && let Some(definition) =
                parse_inline_definition(Some(root), &object, file_path, line_index)
        {
            mixin.definition = definition;
        }
        mixins.push(mixin);
    }

    let mut modules = Vec::new();
    for call in jsquery::iter_calls(root, &["Shopware.Module.register", "Module.register"]) {
        let name = string_argument_value(&call, 0);
        let config = resolve_admin_module_config(root, &call);
        let Some(config) = config.filter(|_| !name.is_empty()) else {
            continue;
        };
        let mut module = AdminModule {
            file_path: file_path.to_owned(),
            line: definition_node_line(line_index, &call),
            display_name: string_property(&config, "name"),
            type_: string_property(&config, "type"),
            title: string_property(&config, "title"),
            description: string_property(&config, "description"),
            ..Default::default()
        };
        if let Some(routes_property) = jsquery::property(&config, "routes") {
            let routes_object = resolve_admin_route_object(
                root,
                jsquery::property_value(&routes_property).as_ref(),
            );
            append_admin_module_routes(
                &mut module.routes,
                root,
                routes_object.as_ref(),
                &name.replace('-', "."),
                line_index,
            );
        }
        append_admin_route_middleware_routes(&mut module.routes, root, &config, line_index);
        module.name = name;
        modules.push(module);
    }
    (mixins, modules)
}

pub(crate) fn resolve_admin_module_config(
    root: &SyntaxNode,
    call: &SyntaxNode,
) -> Option<SyntaxNode> {
    let expression = jsquery::argument_expression(call, 1)?;
    if expression.kind() == jssyntax::JS_OBJECT {
        return Some(expression);
    }
    let identifier = jsquery::identifier_text(&expression)
        .unwrap_or_default()
        .trim()
        .to_owned();
    if identifier.is_empty() {
        return None;
    }
    let (declaration, _) = visible_javascript_const_declaration(call, &identifier, root)?;
    declaration
        .children()
        .find(|child| child.kind() == jssyntax::JS_OBJECT)
}

pub(crate) fn append_admin_route_middleware_routes(
    destination: &mut Vec<AdminModuleRoute>,
    root: &SyntaxNode,
    config: &SyntaxNode,
    line_index: &LineIndex,
) {
    let property = jsquery::property(config, "routeMiddleware");
    let Some(middleware) = resolve_admin_route_middleware(root, property.as_ref()) else {
        return;
    };
    let mut positions: HashSet<String> =
        destination.iter().map(|route| route.name.clone()).collect();
    for call in jsquery::iter_calls(&middleware, &[]) {
        let call_name = jsquery::call_name(&call).unwrap_or_default();
        if call_name != "children.push" && !call_name.ends_with(".children.push") {
            continue;
        }
        let Some(route_config) = jsquery::object_argument(&call, 0) else {
            continue;
        };
        let name = admin_static_string_property(root, &route_config, "name");
        if name.is_empty() || positions.contains(&name) {
            continue;
        }
        positions.insert(name.clone());
        destination.push(AdminModuleRoute {
            local_name: admin_module_route_local_name(&name),
            path: string_property(&route_config, "path"),
            component: admin_module_route_component(Some(&route_config)),
            line: definition_node_line(line_index, &route_config),
            name,
        });
    }
}

pub(crate) fn resolve_admin_route_middleware(
    root: &SyntaxNode,
    property: Option<&SyntaxNode>,
) -> Option<SyntaxNode> {
    let property = property?;
    if property.kind() == jssyntax::JS_METHOD {
        return Some(property.clone());
    }
    let value = jsquery::property_value(property)?;
    if matches!(
        value.kind(),
        jssyntax::JS_FUNCTION | jssyntax::JS_ARROW_FUNCTION | jssyntax::JS_METHOD
    ) {
        return Some(value);
    }
    let identifier = jsquery::identifier_text(&value)
        .unwrap_or_default()
        .trim()
        .to_owned();
    if identifier.is_empty() {
        return None;
    }
    if let Some(function) = jsquery::nodes(root, &[jssyntax::JS_FUNCTION])
        .into_iter()
        .find(|function| admin_javascript_function_name(Some(function)) == identifier)
    {
        return Some(function);
    }
    let (declaration, _) = visible_javascript_const_declaration(property, &identifier, root)?;
    declaration.children().find(|child| {
        matches!(
            child.kind(),
            jssyntax::JS_FUNCTION | jssyntax::JS_ARROW_FUNCTION
        )
    })
}

pub(crate) fn admin_static_string_property(
    root: &SyntaxNode,
    object: &SyntaxNode,
    name: &str,
) -> String {
    let Some(property) = jsquery::property(object, name) else {
        return String::new();
    };
    let value = jsquery::property_value(&property);
    let direct = value
        .as_ref()
        .map(jsquery::string_value)
        .unwrap_or_default();
    if !direct.is_empty() {
        return direct;
    }
    let identifier = match &value {
        Some(value) => jsquery::identifier_text(value)
            .unwrap_or_default()
            .trim()
            .to_owned(),
        None if property.kind() == jssyntax::JS_PROPERTY => {
            jsquery::property_name(&property).unwrap_or_default()
        }
        None => String::new(),
    };
    if identifier.is_empty() {
        return String::new();
    }
    let Some(expression) = visible_javascript_const_initializer(&property, &identifier, root)
    else {
        return String::new();
    };
    let parsed = swlsp_parser_js::parse(&expression).root();
    jsquery::nodes(&parsed, &[jssyntax::JS_STRING])
        .first()
        .map(jsquery::string_value)
        .unwrap_or_default()
}

pub(crate) fn admin_module_route_local_name(name: &str) -> String {
    match name.rfind('.') {
        Some(position) => name[position + 1..].to_owned(),
        None => name.to_owned(),
    }
}

pub(crate) fn append_admin_module_routes(
    destination: &mut Vec<AdminModuleRoute>,
    root: &SyntaxNode,
    routes_object: Option<&SyntaxNode>,
    prefix: &str,
    line_index: &LineIndex,
) {
    let Some(routes_object) = routes_object else {
        return;
    };
    for route_property in jsquery::properties(routes_object) {
        let local_name = jsquery::property_name(&route_property).unwrap_or_default();
        let Some(route_config) =
            jsquery::property_value(&route_property).filter(|_| !local_name.is_empty())
        else {
            continue;
        };
        let name = format!("{prefix}.{local_name}");
        destination.push(AdminModuleRoute {
            name: name.clone(),
            local_name,
            path: string_property(&route_config, "path"),
            component: admin_module_route_component(Some(&route_config)),
            line: definition_node_line(line_index, &route_property),
        });
        let children = resolve_admin_route_object(
            root,
            jsquery::property(&route_config, "children")
                .and_then(|property| jsquery::property_value(&property))
                .as_ref(),
        );
        append_admin_module_routes(destination, root, children.as_ref(), &name, line_index);
    }
}

/// Follows the small static factory pattern used by core modules, e.g.
/// `children: detailChildren()` with a local function returning an object.
pub(crate) fn resolve_admin_route_object(
    root: &SyntaxNode,
    expression: Option<&SyntaxNode>,
) -> Option<SyntaxNode> {
    let expression = expression?;
    if expression.kind() == jssyntax::JS_OBJECT {
        return Some(expression.clone());
    }
    if expression.kind() != jssyntax::JS_CALL_EXPRESSION {
        return None;
    }
    let name = jsquery::call_name(expression).unwrap_or_default();
    if name.is_empty() || name.contains('.') || jsquery::iter_arguments(expression).next().is_some()
    {
        return None;
    }
    for function in jsquery::nodes(root, &[jssyntax::JS_FUNCTION]) {
        if admin_javascript_function_name(Some(&function)) != name {
            continue;
        }
        if let Some(object) = function
            .descendants()
            .find(|node| node.kind() == jssyntax::JS_OBJECT)
        {
            return Some(object);
        }
    }
    None
}

pub(crate) fn admin_javascript_function_name(function: Option<&SyntaxNode>) -> String {
    let Some(function) = function.filter(|function| function.kind() == jssyntax::JS_FUNCTION)
    else {
        return String::new();
    };
    function
        .children()
        .find(|child| child.kind() == jssyntax::JS_IDENTIFIER)
        .map(|child| child.text().to_string().trim().to_owned())
        .unwrap_or_default()
}

pub(crate) fn admin_module_route_component(route_config: Option<&SyntaxNode>) -> String {
    let component = string_property_of(route_config, "component");
    if !component.is_empty() {
        return component;
    }
    let components = route_config
        .and_then(|config| jsquery::property(config, "components"))
        .and_then(|property| jsquery::property_value(&property));
    string_property_of(components.as_ref(), "default")
}

pub(crate) fn string_property(object: &SyntaxNode, name: &str) -> String {
    string_property_of(Some(object), name)
}

/// Go `stringProperty` with a possibly missing object.
pub(crate) fn string_property_of(object: Option<&SyntaxNode>, name: &str) -> String {
    object
        .and_then(|object| jsquery::property(object, name))
        .and_then(|property| jsquery::property_value(&property))
        .map(|value| jsquery::string_value(&value))
        .unwrap_or_default()
}

pub(crate) fn parse_inline_definition(
    root: Option<&SyntaxNode>,
    object: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> Option<ComponentDefinition> {
    let mut definition = parse_component_object(object, file_path, line_index)?;
    let root = root
        .cloned()
        .unwrap_or_else(|| object.ancestors().last().unwrap_or_else(|| object.clone()));
    enrich_local_component_imports(&root, &mut definition);
    if let Some(template_import) =
        jsquery::import_path(&root, "template").filter(|path| !path.is_empty())
    {
        definition.template_path = resolve_template_path(file_path, &template_import);
        if let Ok(result) = parse_template_from_file(&definition.template_path) {
            definition.slots = result.slots;
            definition.blocks = result.blocks;
        }
    }
    Some(definition)
}

/// Resolves an import path relative to the registration file.
pub(crate) fn resolve_import_path(registration_file: &str, import_path: &str) -> String {
    if import_path.is_empty() {
        return String::new();
    }
    let base_path = if import_path.starts_with("src/") {
        const ADMIN_ROOT: &str = "Resources/app/administration/";
        let Some(admin_index) = registration_file.find(ADMIN_ROOT) else {
            return import_path.to_owned();
        };
        let admin_root = &registration_file[..admin_index + ADMIN_ROOT.len()];
        filepath::join(&[admin_root, import_path])
    } else if import_path.starts_with("./") || import_path.starts_with("../") {
        filepath::join(&[&filepath::dir(registration_file), import_path])
    } else {
        return import_path.to_owned();
    };
    resolve_js_file(&base_path)
}

/// Resolves Administration JavaScript, TypeScript and Vue SFC imports.
pub(crate) fn resolve_js_file(base_path: &str) -> String {
    if base_path.ends_with(".js") || base_path.ends_with(".ts") || base_path.ends_with(".vue") {
        return base_path.to_owned();
    }
    let candidates = [
        format!("{base_path}.js"),
        format!("{base_path}.ts"),
        format!("{base_path}.vue"),
        filepath::join(&[base_path, "index.js"]),
        filepath::join(&[base_path, "index.ts"]),
        filepath::join(&[base_path, "index.vue"]),
    ];
    for candidate in candidates {
        if std::fs::metadata(&candidate).is_ok() {
            return candidate;
        }
    }
    filepath::join(&[base_path, "index.js"])
}
