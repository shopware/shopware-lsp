//! `<script setup>` compiler macros and bindings.

use crate::*;

/// One compiler macro call such as `defineProps<T>(...)`.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(crate) struct ScriptSetupMacro {
    pub name_start: usize,
    /// Byte range of the type argument between `<` and `>`.
    pub type_range: Option<(usize, usize)>,
    pub args_start: usize,
    pub args_end: usize,
}

fn line_at(line_index: &LineIndex, offset: u32) -> u32 {
    line_index.position(offset).0 + 1
}

fn node_start(node: &SyntaxNode) -> u32 {
    u32::from(node.range_trimmed_trivia().start())
}

/// Normalizes Vue compiler macros and the bindings exposed by
/// `<script setup>` into the Options API component contract.
pub(crate) fn parse_script_setup_definition(
    root: &SyntaxNode,
    source: &str,
    file_path: &str,
    line_index: &LineIndex,
    body_range: TextRange,
) -> Option<ComponentDefinition> {
    let program = script_setup_program(root, body_range)?;
    let mut definition = ComponentDefinition {
        file_path: file_path.to_owned(),
        ..Default::default()
    };

    for call in jsquery::iter_calls(&program, &["defineProps"]) {
        if closest_javascript_function(&call).is_some() {
            continue;
        }
        let props = jsquery::argument_expression(&call, 0)
            .map(|argument| parse_props(&argument, line_index))
            .unwrap_or_default();
        definition.props = overlay_script_setup_props(&definition.props, &props);
    }
    for macro_ in script_setup_macros(source, body_range, "defineProps") {
        let Some((type_start, type_end)) = macro_.type_range else {
            continue;
        };
        let type_expression = source[type_start..type_end].trim();
        append_unique(&mut definition.script_setup_prop_types, type_expression);
        let props = script_setup_typed_props(source, type_expression, file_path, line_index);
        definition.props = overlay_script_setup_props(&definition.props, &props);
    }
    definition.script_setup_prop_defaults =
        apply_script_setup_defaults(&program, &mut definition.props);
    definition.script_setup_prop_bindings =
        script_setup_reactive_prop_bindings(&program, line_index);
    for binding in &definition.script_setup_prop_bindings {
        if binding.default.is_empty() {
            continue;
        }
        definition.script_setup_prop_defaults = overlay_script_setup_prop_defaults(
            &definition.script_setup_prop_defaults,
            &[ScriptSetupPropDefault {
                name: binding.prop_name.clone(),
                value: binding.default.clone(),
            }],
        );
    }
    apply_script_setup_defaults_to_props(
        &mut definition.props,
        &definition.script_setup_prop_defaults,
    );

    for call in jsquery::iter_calls(&program, &["defineEmits"]) {
        if closest_javascript_function(&call).is_some() {
            continue;
        }
        let Some(argument) = jsquery::argument_expression(&call, 0) else {
            continue;
        };
        for event in parse_event_declarations(&argument, line_index) {
            let name = event.name.clone();
            append_component_event(&mut definition.events, event);
            append_unique(&mut definition.emits, &name);
        }
    }
    for macro_ in script_setup_macros(source, body_range, "defineEmits") {
        let Some((type_start, type_end)) = macro_.type_range else {
            continue;
        };
        let type_expression = source[type_start..type_end].trim();
        append_unique(&mut definition.script_setup_event_types, type_expression);
        for event in script_setup_typed_events(source, type_start, type_end, file_path, line_index)
        {
            let name = event.name.clone();
            append_component_event(&mut definition.events, event);
            append_unique(&mut definition.emits, &name);
        }
    }
    for macro_ in script_setup_macros(source, body_range, "defineSlots") {
        let Some((type_start, type_end)) = macro_.type_range else {
            continue;
        };
        append_unique(
            &mut definition.script_setup_slot_types,
            source[type_start..type_end].trim(),
        );
    }

    for macro_ in script_setup_macros(source, body_range, "defineModel") {
        let (prop, event) = script_setup_model(source, &macro_, file_path, line_index);
        if prop.name.is_empty() {
            continue;
        }
        let prop_name = prop.name.clone();
        let event_name = event.name.clone();
        definition.props =
            overlay_script_setup_props(&definition.props, std::slice::from_ref(&prop));
        append_component_event(&mut definition.events, event);
        append_unique(&mut definition.emits, &event_name);
        if prop_name == "modelValue" && definition.model_prop.is_empty() {
            definition.model_prop = prop_name;
            definition.model_event = event_name;
        }
    }

    let prop_members = script_setup_prop_members(&program, &definition.props, source, line_index);
    definition.members.extend(prop_members);
    definition.members = overlay_script_setup_members(
        &definition.members,
        &script_setup_bindings(&program, line_index),
    );
    let (import_members, local_components) = script_setup_imports(&program, file_path, line_index);
    definition.members = overlay_script_setup_members(&definition.members, &import_members);
    definition.local_components = local_components;
    populate_script_setup_legacy_members(&mut definition);
    set_definition_file_path(&mut definition, file_path);
    Some(definition)
}

pub(crate) fn script_setup_program(root: &SyntaxNode, body_range: TextRange) -> Option<SyntaxNode> {
    jsquery::nodes(root, &[jssyntax::JS_PROGRAM])
        .into_iter()
        .find(|program| {
            let range = program.text_range();
            range.start() >= body_range.start() && range.end() <= body_range.end()
        })
}

pub(crate) fn script_setup_macros(
    source: &str,
    body_range: TextRange,
    name: &str,
) -> Vec<ScriptSetupMacro> {
    let start = usize::from(body_range.start());
    let end = usize::from(body_range.end());
    let bytes = source.as_bytes();
    if end < start || end > source.len() || name.is_empty() {
        return Vec::new();
    }
    let mut result = Vec::new();
    let mut cursor = start;
    while cursor < end {
        let Some(relative) = find_bytes(&bytes[cursor..end], name.as_bytes()) else {
            break;
        };
        let position = cursor + relative;
        cursor = position + name.len();
        if (position > start && is_vue_identifier_part(bytes[position - 1]))
            || (cursor < end && is_vue_identifier_part(bytes[cursor]))
            || !admin_type_code_position(source, position)
        {
            continue;
        }
        let mut macro_ = ScriptSetupMacro {
            name_start: position,
            ..Default::default()
        };
        while cursor < end && is_javascript_space(bytes[cursor]) {
            cursor += 1;
        }
        if cursor < end && bytes[cursor] == b'<' {
            let Some(close) =
                matching_slot_delimiter(source, cursor, b'<', b'>').filter(|close| *close < end)
            else {
                continue;
            };
            macro_.type_range = Some((cursor + 1, close));
            cursor = close + 1;
            while cursor < end && is_javascript_space(bytes[cursor]) {
                cursor += 1;
            }
        }
        if cursor >= end || bytes[cursor] != b'(' {
            continue;
        }
        let Some(close) =
            matching_slot_delimiter(source, cursor, b'(', b')').filter(|close| *close < end)
        else {
            continue;
        };
        macro_.args_start = cursor + 1;
        macro_.args_end = close;
        result.push(macro_);
        cursor = close + 1;
    }
    result
}

pub(crate) fn script_setup_typed_props(
    source: &str,
    type_expression: &str,
    file_path: &str,
    line_index: &LineIndex,
) -> Vec<VueComponentProp> {
    let fields = script_setup_type_fields(source, type_expression);
    let mut props = Vec::with_capacity(fields.len());
    for field in fields {
        if field.name.is_empty() {
            continue;
        }
        props.push(VueComponentProp {
            name: field.name.clone(),
            type_: field.value.trim().to_owned(),
            documentation: admin_type_field_documentation(source, field.offset),
            required: !field.optional,
            file_path: file_path.to_owned(),
            line: line_at(line_index, field.offset as u32),
            name_range: type_declaration_field_name_range(&field, line_index, 0),
            ..Default::default()
        });
    }
    props
}

pub(crate) fn script_setup_type_fields(
    source: &str,
    expression: &str,
) -> Vec<TypeDeclarationField> {
    let expression = expression.trim();
    if let Some(open) = expression.find('{') {
        if let Some(absolute) = source.find(expression) {
            return parse_type_declaration_fields(source, absolute + open);
        }
        return parse_type_declaration_fields(expression, open);
    }
    let (name, _) = parse_admin_named_type(expression);
    if name.is_empty() {
        return Vec::new();
    }
    for captures in ADMIN_TYPE_DECLARATION_PATTERN.captures_iter(source) {
        let (Some(whole), Some(kind), Some(declared)) =
            (captures.get(0), captures.get(1), captures.get(2))
        else {
            continue;
        };
        if declared.as_str() != name || !admin_type_code_position(source, whole.start()) {
            continue;
        }
        let (_, mut cursor) = parse_admin_type_parameters(source, declared.end());
        if kind.as_str() == "type" {
            let Some(equals) = index_admin_type_token(source, cursor, b'=') else {
                continue;
            };
            cursor = equals + 1;
        }
        if let Some(open) = index_admin_type_token(source, cursor, b'{') {
            return parse_type_declaration_fields(source, open);
        }
    }
    Vec::new()
}

pub(crate) fn apply_script_setup_defaults(
    program: &SyntaxNode,
    props: &mut [VueComponentProp],
) -> Vec<ScriptSetupPropDefault> {
    let mut positions: HashMap<String, usize> = HashMap::with_capacity(props.len());
    for (index, prop) in props.iter().enumerate() {
        positions.insert(prop.name.clone(), index);
    }
    let mut result: Vec<ScriptSetupPropDefault> = Vec::new();
    let mut result_positions: HashMap<String, usize> = HashMap::new();
    for call in jsquery::iter_calls(program, &["withDefaults"]) {
        if closest_javascript_function(&call).is_some() {
            continue;
        }
        let Some(defaults) = jsquery::object_argument(&call, 1) else {
            continue;
        };
        for property in jsquery::properties(&defaults) {
            let name = jsquery::property_name(&property).unwrap_or_default();
            let value = node_text(jsquery::property_value(&property).as_ref())
                .trim()
                .to_owned();
            if name.is_empty() || value.is_empty() {
                continue;
            }
            let entry = ScriptSetupPropDefault {
                name: name.clone(),
                value: value.clone(),
            };
            if let Some(&index) = result_positions.get(&name) {
                result[index] = entry;
            } else {
                result_positions.insert(name.clone(), result.len());
                result.push(entry);
            }
            let Some(&index) = positions.get(&name) else {
                continue;
            };
            props[index].required = false;
            props[index].default = value;
        }
    }
    result
}

pub(crate) fn apply_script_setup_defaults_to_props(
    props: &mut [VueComponentProp],
    defaults: &[ScriptSetupPropDefault],
) {
    let values: HashMap<&str, &str> = defaults
        .iter()
        .map(|value| (value.name.as_str(), value.value.as_str()))
        .collect();
    for prop in props {
        if let Some(value) = values
            .get(prop.name.as_str())
            .filter(|value| !value.is_empty())
        {
            prop.default = (*value).to_owned();
            prop.required = false;
        }
    }
}

fn leading_space_len(text: &str) -> usize {
    text.len() - text.trim_start().len()
}

pub(crate) fn script_setup_reactive_prop_bindings(
    program: &SyntaxNode,
    line_index: &LineIndex,
) -> Vec<ScriptSetupPropBinding> {
    let mut result: Vec<ScriptSetupPropBinding> = Vec::new();
    let mut positions: HashMap<String, usize> = HashMap::new();
    for declaration in jsquery::nodes(program, &[jssyntax::JS_VARIABLE_DECLARATION]) {
        let declaration_text = compact_javascript_text(&declaration.text().to_string());
        if closest_javascript_function(&declaration).is_some()
            || (!declaration_text.contains("defineProps(")
                && !declaration_text.contains("defineProps<"))
        {
            continue;
        }
        let Some(pattern) = script_setup_destructuring_object(&declaration) else {
            continue;
        };
        let text = pattern.text().to_string();
        let leading = leading_space_len(&text);
        if text.len() - leading < 2 || text.as_bytes()[leading] != b'{' {
            continue;
        }
        let Some(close) = text.rfind('}').filter(|close| *close > leading) else {
            continue;
        };
        let base = u32::from(pattern.text_range().start()) as usize + leading + 1;
        for segment in split_top_level_declarations(&text[leading + 1..close], base) {
            let Some(binding) = script_setup_reactive_prop_binding(&segment, line_index) else {
                continue;
            };
            if let Some(&index) = positions.get(&binding.binding_name) {
                result[index] = binding;
            } else {
                positions.insert(binding.binding_name.clone(), result.len());
                result.push(binding);
            }
        }
    }
    result
}

pub(crate) fn script_setup_reactive_prop_binding(
    segment: &DeclarationSegment,
    line_index: &LineIndex,
) -> Option<ScriptSetupPropBinding> {
    let raw = segment.text.as_str();
    let leading = leading_space_len(raw);
    let value = raw.trim();
    if value.is_empty() || value.starts_with("...") {
        return None;
    }
    let colon = index_slot_top_level(value, b':');
    let equals = index_slot_top_level(value, b'=');
    let public_end = colon.or(equals).unwrap_or(value.len());
    let prop_name = value[..public_end]
        .trim()
        .trim_matches(|c| c == '\'' || c == '"');
    if !is_admin_type_identifier(prop_name) {
        return None;
    }
    let mut binding_name = prop_name.to_owned();
    let mut default_value = String::new();
    let binding_start;
    if let Some(colon) = colon {
        let after = &value[colon + 1..];
        let right = after.trim();
        binding_start = colon + 1 + leading_space_len(after);
        if let Some(right_equals) = index_slot_top_level(right, b'=') {
            binding_name = right[..right_equals].trim().to_owned();
            default_value = right[right_equals + 1..].trim().to_owned();
        } else {
            binding_name = right.to_owned();
        }
    } else {
        binding_start = value.find(prop_name).unwrap_or(0);
        if let Some(equals) = equals {
            default_value = value[equals + 1..].trim().to_owned();
        }
    }
    if !is_admin_type_identifier(&binding_name) {
        return None;
    }
    let absolute_start = (segment.offset + leading + binding_start) as u32;
    Some(ScriptSetupPropBinding {
        prop_name: prop_name.to_owned(),
        line: line_at(line_index, absolute_start),
        name_range: source_range_at(
            line_index,
            absolute_start,
            absolute_start + binding_name.len() as u32,
            true,
        ),
        binding_name,
        default: default_value,
    })
}

pub(crate) fn script_setup_typed_events(
    source: &str,
    start: usize,
    end: usize,
    file_path: &str,
    line_index: &LineIndex,
) -> Vec<VueComponentEvent> {
    if end < start || end > source.len() {
        return Vec::new();
    }
    let type_expression = &source[start..end];
    let mut seen: HashSet<String> = HashSet::new();
    let mut result = Vec::new();
    let mut add = |name: &str,
                   event_type: &str,
                   documentation: String,
                   offset: usize,
                   name_range: AdminSourceRange| {
        let name = name.trim_matches(|c| c == '\'' || c == '"').trim();
        let canonical = canonical_event_name(name);
        if name.is_empty() || seen.contains(&canonical) {
            return;
        }
        seen.insert(canonical);
        result.push(VueComponentEvent {
            name: name.to_owned(),
            type_: event_type.trim().to_owned(),
            documentation,
            file_path: file_path.to_owned(),
            line: line_at(line_index, offset as u32),
            name_range,
        });
    };
    if let Some(open) = type_expression.find('{') {
        for field in parse_type_declaration_fields(type_expression, open) {
            add(
                &field.name,
                &field.value,
                admin_type_field_documentation(type_expression, field.offset),
                start + field.offset,
                type_declaration_field_name_range(&field, line_index, start),
            );
        }
    }
    let bytes = type_expression.as_bytes();
    let mut cursor = 0;
    while cursor < bytes.len() {
        let Some(relative) = bytes[cursor..].iter().position(|byte| *byte == b':') else {
            break;
        };
        let colon = cursor + relative;
        let after = &type_expression[colon + 1..];
        let value = after.trim_start_matches([' ', '\t', '\r', '\n']);
        let leading = after.len() - value.len();
        let value_bytes = value.as_bytes();
        if value_bytes.len() > 2
            && (value_bytes[0] == b'\'' || value_bytes[0] == b'"')
            && let Some(close) = value_bytes[1..]
                .iter()
                .position(|byte| *byte == value_bytes[0])
        {
            let name_start = start + colon + 1 + leading + 1;
            let mut event_type = String::new();
            let mut documentation = String::new();
            if let Some(open) = type_expression[..colon].rfind('(')
                && let Some(signature_end) =
                    matching_slot_delimiter(type_expression, open, b'(', b')')
                        .filter(|end| *end > open)
            {
                event_type = type_expression[open..=signature_end].trim().to_owned();
                documentation = admin_type_field_documentation(type_expression, open);
            }
            add(
                &value[1..1 + close],
                &event_type,
                documentation,
                name_start,
                source_range_at(
                    line_index,
                    name_start as u32,
                    (name_start + close) as u32,
                    false,
                ),
            );
        }
        cursor = colon + 1;
    }
    result
}

fn is_quoted_literal(value: &str) -> bool {
    let bytes = value.as_bytes();
    bytes.len() >= 2
        && (bytes[0] == b'\'' || bytes[0] == b'"')
        && bytes[bytes.len() - 1] == bytes[0]
}

pub(crate) fn script_setup_model(
    source: &str,
    macro_: &ScriptSetupMacro,
    file_path: &str,
    line_index: &LineIndex,
) -> (VueComponentProp, VueComponentEvent) {
    let mut name = "modelValue".to_owned();
    let arguments = split_slot_top_level(&source[macro_.args_start..macro_.args_end], b',');
    let mut option = String::new();
    if let Some(first) = arguments.first() {
        let first = first.trim();
        if is_quoted_literal(first) {
            name = first[1..first.len() - 1].to_owned();
            if arguments.len() > 1 {
                option = arguments[1].trim().to_owned();
            }
        } else {
            option = first.to_owned();
        }
    }
    let mut prop = VueComponentProp {
        name: name.clone(),
        file_path: file_path.to_owned(),
        line: line_at(line_index, macro_.name_start as u32),
        ..Default::default()
    };
    if let Some(first) = arguments.first() {
        let trimmed = first.trim();
        if is_quoted_literal(trimmed) {
            let leading = first.find(trimmed).unwrap_or(0);
            let start = macro_.args_start + leading + 1;
            prop.name_range =
                source_range_at(line_index, start as u32, (start + name.len()) as u32, false);
        }
    }
    if let Some((type_start, type_end)) = macro_.type_range {
        prop.type_ = source[type_start..type_end].trim().to_owned();
    }
    if let Some(open) = option.find('{') {
        for field in parse_type_declaration_fields(&option, open) {
            match field.name.as_str() {
                "type" => {
                    if prop.type_.is_empty() {
                        prop.type_ = meteor_runtime_type(&field.value);
                    }
                }
                "required" => prop.required = field.value.trim() == "true",
                "default" => {
                    prop.default = field.value.trim().to_owned();
                    prop.required = false;
                }
                _ => {}
            }
        }
    }
    let mut event = VueComponentEvent {
        name: format!("update:{}", camel_to_kebab(&name)),
        type_: prop.type_.clone(),
        file_path: file_path.to_owned(),
        line: prop.line,
        name_range: prop.name_range.clone(),
        ..Default::default()
    };
    if event.name_range == AdminSourceRange::default() {
        event.name_range = source_range_at(
            line_index,
            macro_.name_start as u32,
            (macro_.name_start + "defineModel".len()) as u32,
            true,
        );
    }
    (prop, event)
}

pub(crate) fn overlay_script_setup_props(
    base: &[VueComponentProp],
    overlay: &[VueComponentProp],
) -> Vec<VueComponentProp> {
    let mut result = base.to_vec();
    let mut positions: HashMap<String, usize> = HashMap::with_capacity(result.len());
    for (index, prop) in result.iter().enumerate() {
        positions.insert(prop.name.clone(), index);
    }
    for prop in overlay {
        if prop.name.is_empty() {
            continue;
        }
        let mut prop = prop.clone();
        if let Some(&index) = positions.get(&prop.name) {
            let current = &result[index];
            if prop.documentation.is_empty() {
                prop.documentation = current.documentation.clone();
            }
            if prop.type_.is_empty() {
                prop.type_ = current.type_.clone();
            }
            if prop.default.is_empty() {
                prop.default = current.default.clone();
            }
            prop.required = prop.required || current.required;
            if prop.file_path.is_empty() {
                prop.file_path = current.file_path.clone();
            }
            if prop.line == 0 {
                prop.line = current.line;
            }
            if prop.name_range == AdminSourceRange::default() {
                prop.name_range = current.name_range.clone();
            }
            result[index] = prop;
            continue;
        }
        positions.insert(prop.name.clone(), result.len());
        result.push(prop);
    }
    result
}

pub(crate) fn overlay_script_setup_prop_defaults(
    base: &[ScriptSetupPropDefault],
    overlay: &[ScriptSetupPropDefault],
) -> Vec<ScriptSetupPropDefault> {
    let mut result = base.to_vec();
    let mut positions: HashMap<String, usize> = HashMap::with_capacity(result.len());
    for (index, value) in result.iter().enumerate() {
        positions.insert(value.name.clone(), index);
    }
    for value in overlay {
        if value.name.is_empty() || value.value.is_empty() {
            continue;
        }
        if let Some(&index) = positions.get(&value.name) {
            result[index] = value.clone();
            continue;
        }
        positions.insert(value.name.clone(), result.len());
        result.push(value.clone());
    }
    result
}

pub(crate) fn overlay_script_setup_prop_bindings(
    base: &[ScriptSetupPropBinding],
    overlay: &[ScriptSetupPropBinding],
) -> Vec<ScriptSetupPropBinding> {
    let mut result = base.to_vec();
    let mut positions: HashMap<String, usize> = HashMap::with_capacity(result.len());
    for (index, binding) in result.iter().enumerate() {
        positions.insert(binding.binding_name.clone(), index);
    }
    for binding in overlay {
        if binding.prop_name.is_empty() || binding.binding_name.is_empty() {
            continue;
        }
        if let Some(&index) = positions.get(&binding.binding_name) {
            result[index] = binding.clone();
            continue;
        }
        positions.insert(binding.binding_name.clone(), result.len());
        result.push(binding.clone());
    }
    result
}

pub(crate) fn script_setup_prop_members(
    program: &SyntaxNode,
    props: &[VueComponentProp],
    source: &str,
    line_index: &LineIndex,
) -> Vec<VueComponentMember> {
    let mut locations: HashMap<String, Option<SyntaxNode>> = HashMap::new();
    for call in jsquery::iter_calls(program, &["defineProps"]) {
        let Some(argument) = jsquery::argument_expression(&call, 0) else {
            continue;
        };
        for property in jsquery::properties(&argument) {
            locations.insert(
                jsquery::property_name(&property).unwrap_or_default(),
                jsquery::property_name_node(&property),
            );
        }
    }
    let mut result = Vec::with_capacity(props.len());
    for prop in props {
        let mut member = VueComponentMember {
            name: prop.name.clone(),
            kind: VueComponentMemberKind::Prop,
            type_: prop.type_.clone(),
            line: prop.line,
            binding_name: prop.name.clone(),
            ..Default::default()
        };
        if let Some(Some(node)) = locations.get(&prop.name) {
            member.name_range = component_member_name_range(Some(node), line_index);
        } else if let Some(offset) = script_setup_type_property_offset(source, prop) {
            member.name_range = source_range_at(
                line_index,
                offset as u32,
                (offset + prop.name.len()) as u32,
                true,
            );
        }
        result.push(member);
    }
    result
}

pub(crate) fn script_setup_type_property_offset(
    source: &str,
    prop: &VueComponentProp,
) -> Option<usize> {
    if prop.line == 0 || prop.name.is_empty() {
        return None;
    }
    let lines: Vec<&str> = source.split('\n').collect();
    let line = prop.line as usize;
    if line > lines.len() {
        return None;
    }
    let line_start: usize = lines[..line - 1].iter().map(|text| text.len() + 1).sum();
    lines[line - 1]
        .find(prop.name.as_str())
        .map(|relative| line_start + relative)
}

pub(crate) fn script_setup_bindings(
    program: &SyntaxNode,
    line_index: &LineIndex,
) -> Vec<VueComponentMember> {
    let mut known: HashMap<String, String> = HashMap::new();
    let mut result = Vec::new();
    for declaration in jsquery::nodes(program, &[jssyntax::JS_VARIABLE_DECLARATION]) {
        if closest_javascript_function(&declaration).is_some() {
            continue;
        }
        if let Some(name_node) = first_direct_identifier(&declaration) {
            let name = jsquery::identifier_text(&name_node)
                .unwrap_or_default()
                .trim()
                .to_owned();
            if name.is_empty() {
                continue;
            }
            let kind = component_setup_binding_kind(&declaration);
            let (member_type, expression, open) =
                component_setup_binding_inference(&declaration, kind, &known);
            result.push(VueComponentMember {
                name: name.clone(),
                binding_name: name.clone(),
                kind,
                type_: member_type.clone(),
                line: line_at(line_index, node_start(&name_node)),
                name_range: component_member_name_range(Some(&name_node), line_index),
                source_expression: expression,
                open_runtime_shape: open,
                ..Default::default()
            });
            if !member_type.is_empty() {
                known.insert(name, member_type);
            }
            continue;
        }
        let Some(pattern) = script_setup_destructuring_object(&declaration) else {
            continue;
        };
        for property in jsquery::properties(&pattern) {
            let name = jsquery::property_name(&property)
                .unwrap_or_default()
                .trim()
                .to_owned();
            if name.is_empty()
                || compact_javascript_text(&property.text().to_string()).starts_with("...")
            {
                continue;
            }
            let name_node = jsquery::property_name_node(&property);
            let mut binding_name = name;
            if let Some(value) = jsquery::property_value(&property)
                && value.kind() == jssyntax::JS_IDENTIFIER
            {
                binding_name = jsquery::identifier_text(&value)
                    .unwrap_or_default()
                    .trim()
                    .to_owned();
            }
            result.push(VueComponentMember {
                name: binding_name.clone(),
                binding_name,
                kind: VueComponentMemberKind::Data,
                line: line_at(line_index, node_start(&property)),
                name_range: component_member_name_range(name_node.as_ref(), line_index),
                shorthand: value_is_nil(&property),
                ..Default::default()
            });
        }
    }
    for function in jsquery::nodes(program, &[jssyntax::JS_FUNCTION]) {
        if closest_javascript_function(&function).is_some() {
            continue;
        }
        let Some(name_node) = first_direct_identifier(&function) else {
            continue;
        };
        let name = jsquery::identifier_text(&name_node)
            .unwrap_or_default()
            .trim()
            .to_owned();
        result.push(VueComponentMember {
            name: name.clone(),
            binding_name: name,
            kind: VueComponentMemberKind::Method,
            type_: vue_method_signature(&function, &known),
            line: line_at(line_index, node_start(&name_node)),
            name_range: component_member_name_range(Some(&name_node), line_index),
            source_expression: vue_method_return_expression(&function),
            ..Default::default()
        });
    }
    result
}

pub(crate) fn script_setup_destructuring_object(declaration: &SyntaxNode) -> Option<SyntaxNode> {
    let text = declaration.text().to_string();
    let equals = index_slot_top_level(&text, b'=')?;
    let cutoff = u32::from(declaration.text_range().start()) + equals as u32;
    let mut result: Option<SyntaxNode> = None;
    for object in jsquery::nodes(declaration, &[jssyntax::JS_OBJECT]) {
        let range = object.range_trimmed_trivia();
        if u32::from(range.end()) > cutoff {
            continue;
        }
        if result
            .as_ref()
            .is_none_or(|current| range.start() < current.range_trimmed_trivia().start())
        {
            result = Some(object);
        }
    }
    result
}

pub(crate) fn value_is_nil(property: &SyntaxNode) -> bool {
    jsquery::property_value(property).is_none()
}

pub(crate) fn script_setup_imports(
    program: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> (Vec<VueComponentMember>, Vec<VueLocalComponent>) {
    let mut members = Vec::new();
    let mut components = Vec::new();
    for statement in jsquery::nodes(program, &[jssyntax::JS_IMPORT_STATEMENT]) {
        let path = jsquery::import_path(&statement, "").unwrap_or_default();
        let statement_text = statement.text().to_string();
        let statement_start = u32::from(statement.text_range().start());
        for binding in script_setup_import_bindings(&statement_text) {
            if binding.is_empty() {
                continue;
            }
            let mut range = statement.range_trimmed_trivia();
            if let Some(relative) = statement_text.find(binding.as_str()) {
                let start = statement_start + relative as u32;
                range = TextRange::new(start.into(), (start + binding.len() as u32).into());
            }
            let line = line_at(line_index, u32::from(range.start()));
            let name_range =
                source_range_at(line_index, range.start().into(), range.end().into(), true);
            members.push(VueComponentMember {
                name: binding.clone(),
                binding_name: binding.clone(),
                kind: VueComponentMemberKind::Data,
                line,
                name_range: name_range.clone(),
                ..Default::default()
            });
            if !script_setup_component_import(&binding, &path) {
                continue;
            }
            components.push(VueLocalComponent {
                name: camel_to_kebab(&binding),
                symbol: binding,
                import_path: path.clone(),
                file_path: file_path.to_owned(),
                line,
                name_range,
                ..Default::default()
            });
        }
    }
    (members, components)
}

pub(crate) fn script_setup_import_bindings(statement: &str) -> Vec<String> {
    let statement = statement.trim();
    let Some(specifier) = statement.strip_prefix("import") else {
        return Vec::new();
    };
    let mut specifier = specifier.trim();
    if specifier.starts_with("type ") {
        return Vec::new();
    }
    let Some(from) = specifier.rfind(" from ") else {
        return Vec::new();
    };
    specifier = specifier[..from].trim();
    let mut result = Vec::new();
    if let Some(open) = specifier.find('{') {
        if let Some(close) =
            matching_slot_delimiter(specifier, open, b'{', b'}').filter(|close| *close > open)
        {
            for raw in split_slot_top_level(&specifier[open + 1..close], b',') {
                let parts: Vec<&str> = raw.split_whitespace().collect();
                if parts.is_empty() || parts[0] == "type" {
                    continue;
                }
                let mut name = parts[0];
                if parts.len() >= 3 && parts[parts.len() - 2] == "as" {
                    name = parts[parts.len() - 1];
                }
                append_unique(&mut result, name);
            }
        }
        let before = &specifier[..open];
        specifier = before.strip_suffix(',').unwrap_or(before).trim();
    }
    if specifier.starts_with('*') {
        let parts: Vec<&str> = specifier.split_whitespace().collect();
        if parts.len() >= 3 && parts[parts.len() - 2] == "as" {
            append_unique(&mut result, parts[parts.len() - 1]);
        }
    } else {
        let name = specifier.trim();
        if is_static_vue_identifier(name) {
            append_unique(&mut result, name);
        }
    }
    result
}

pub(crate) fn script_setup_component_import(binding: &str, import_path: &str) -> bool {
    let Some(first) = binding.bytes().next() else {
        return false;
    };
    if import_path.is_empty() || !first.is_ascii_uppercase() {
        return false;
    }
    let ext = filepath::ext(import_path).to_lowercase();
    ext == ".vue" || (ext.is_empty() && import_path != "vue")
}

pub(crate) fn overlay_script_setup_members(
    base: &[VueComponentMember],
    overlay: &[VueComponentMember],
) -> Vec<VueComponentMember> {
    let mut result = base.to_vec();
    let mut positions: HashMap<String, usize> = HashMap::with_capacity(result.len());
    for (index, member) in result.iter().enumerate() {
        positions.insert(member.name.clone(), index);
    }
    for member in overlay {
        if member.name.is_empty() {
            continue;
        }
        let mut member = member.clone();
        if let Some(&index) = positions.get(&member.name) {
            let current = &result[index];
            if !current.type_.is_empty() && member.type_.is_empty() {
                member.type_ = current.type_.clone();
            }
            if current.kind == VueComponentMemberKind::Prop {
                member.kind = current.kind;
            }
            if member.name_range == AdminSourceRange::default() {
                member.name_range = current.name_range.clone();
            }
            result[index] = member;
            continue;
        }
        positions.insert(member.name.clone(), result.len());
        result.push(member);
    }
    result
}

pub(crate) fn populate_script_setup_legacy_members(definition: &mut ComponentDefinition) {
    for member in &definition.members {
        match member.kind {
            VueComponentMemberKind::Data => append_unique(&mut definition.data, &member.name),
            VueComponentMemberKind::Computed => {
                append_unique(&mut definition.computed, &member.name)
            }
            VueComponentMemberKind::Method => append_unique(&mut definition.methods, &member.name),
            _ => {}
        }
    }
}

/// A declaration range in UTF-16 coordinates.
pub(crate) fn source_range_at(
    line_index: &LineIndex,
    start: u32,
    end: u32,
    identifier: bool,
) -> AdminSourceRange {
    let (start_line, start_character) = line_index.position_utf16(start);
    let (end_line, end_character) = line_index.position_utf16(end);
    AdminSourceRange {
        start_line,
        start_character,
        end_line,
        end_character,
        declaration: true,
        identifier,
        ..Default::default()
    }
}
