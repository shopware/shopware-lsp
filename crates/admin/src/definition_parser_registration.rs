//! Local components/directives, instance assignments and lexical const
//! resolution.

use crate::*;

fn declaration_source_range(
    name_node: &SyntaxNode,
    line_index: &LineIndex,
) -> (u32, AdminSourceRange, bool) {
    let mut name_range = name_node.range_trimmed_trivia();
    let quoted = name_node.kind() == jssyntax::JS_STRING;
    if quoted && let Some(inner) = js_string_content_range(name_node) {
        name_range = inner;
    }
    let (start_line, start_character) = line_index.position_utf16(u32::from(name_range.start()));
    let (end_line, end_character) = line_index.position_utf16(u32::from(name_range.end()));
    (
        start_line + 1,
        AdminSourceRange {
            start_line,
            start_character,
            end_line,
            end_character,
            declaration: true,
            identifier: !quoted,
            ..Default::default()
        },
        quoted,
    )
}

pub(crate) fn parse_local_components(
    object: &SyntaxNode,
    line_index: &LineIndex,
) -> Vec<VueLocalComponent> {
    if object.kind() != jssyntax::JS_OBJECT {
        return Vec::new();
    }
    let mut result = Vec::new();
    for property in jsquery::properties(object) {
        let mut name = jsquery::property_name(&property)
            .unwrap_or_default()
            .trim()
            .to_owned();
        let name_node = jsquery::property_name_node(&property);
        let value = jsquery::property_value(&property);
        let Some(name_node) = name_node.filter(|_| !name.is_empty()) else {
            continue;
        };
        let mut symbol = String::new();
        let shorthand = value.is_none() && is_static_vue_identifier(&name);
        if shorthand {
            // JavaScript shorthand: components: { SwCard }.
            symbol = name.clone();
        } else if let Some(value) = value
            .as_ref()
            .filter(|value| value.kind() == jssyntax::JS_IDENTIFIER)
        {
            symbol = jsquery::identifier_text(value)
                .unwrap_or_default()
                .trim()
                .to_owned();
        }
        if symbol.is_empty() {
            continue;
        }
        if is_static_vue_identifier(&name) {
            name = camel_to_kebab(&name);
        }
        let (line, name_range, quoted) = declaration_source_range(&name_node, line_index);
        result.push(VueLocalComponent {
            name,
            symbol,
            line,
            name_range,
            shorthand,
            quoted,
            ..Default::default()
        });
    }
    result
}

pub(crate) fn parse_local_directives(
    object: &SyntaxNode,
    line_index: &LineIndex,
) -> Vec<VueLocalDirective> {
    if object.kind() != jssyntax::JS_OBJECT {
        return Vec::new();
    }
    let mut result = Vec::new();
    for property in jsquery::properties(object) {
        let mut name = jsquery::property_name(&property)
            .unwrap_or_default()
            .trim()
            .to_owned();
        let Some(name_node) = jsquery::property_name_node(&property).filter(|_| !name.is_empty())
        else {
            continue;
        };
        if is_static_vue_identifier(&name) {
            name = camel_to_kebab(&name);
        }
        let (line, name_range, quoted) = declaration_source_range(&name_node, line_index);
        result.push(VueLocalDirective {
            name,
            line,
            name_range,
            shorthand: jsquery::property_value(&property).is_none(),
            quoted,
            ..Default::default()
        });
    }
    result
}

pub(crate) fn enrich_local_component_imports(
    root: &SyntaxNode,
    definition: &mut ComponentDefinition,
) {
    for local in &mut definition.local_components {
        local.import_path = jsquery::import_path(root, &local.symbol).unwrap_or_default();
    }
}

pub(crate) fn enrich_definition_collection_element_members(
    object: &SyntaxNode,
    definition: &mut ComponentDefinition,
    line_index: &LineIndex,
) {
    for call in jsquery::iter_calls(object, &[]) {
        if jsquery::call_method_name(&call).as_deref() != Some("forEach") {
            continue;
        }
        let Some(receiver) = static_callback_call_receiver(&call, "forEach") else {
            continue;
        };
        let Some(collection_name) = direct_vue_instance_reference(&receiver) else {
            continue;
        };
        let Some(callback) = jsquery::argument_expression(&call, 0) else {
            continue;
        };
        let parameter = callback_first_parameter(Some(&callback));
        if parameter.is_empty() {
            continue;
        }
        for statement in jsquery::nodes(&callback, &[jssyntax::JS_EXPRESSION_STATEMENT]) {
            let Some((name, expression)) =
                direct_named_member_assignment(&statement.text_string(), &parameter)
            else {
                continue;
            };
            let mut member_type = vue_expression_text_type(&expression, None);
            if member_type.is_empty() {
                member_type = "unknown".to_owned();
            }
            append_definition_element_member(
                definition,
                &collection_name,
                VueComponentElementMember {
                    name,
                    type_: member_type,
                    line: definition_node_line(line_index, &statement),
                    ..Default::default()
                },
            );
        }
    }
}

pub(crate) fn direct_vue_instance_reference(value: &str) -> Option<String> {
    let name = value.trim().strip_prefix("this.")?.trim();
    is_static_vue_identifier(name).then(|| name.to_owned())
}

/// Matches `<receiver>.<name> = <expression>`; returns name and expression.
pub(crate) fn direct_named_member_assignment(
    value: &str,
    receiver: &str,
) -> Option<(String, String)> {
    let value = value.trim();
    let prefix = format!("{receiver}.");
    if !value.starts_with(&prefix) {
        return None;
    }
    let (name, expression) = direct_assignment_after(value, prefix.len())?;
    Some((name, expression))
}

/// Parses `<identifier> = <expression>` starting at `cursor`.
fn direct_assignment_after(value: &str, mut cursor: usize) -> Option<(String, String)> {
    let bytes = value.as_bytes();
    if cursor >= bytes.len() || !is_vue_identifier_start(bytes[cursor]) {
        return None;
    }
    let start = cursor;
    cursor += 1;
    while cursor < bytes.len() && is_vue_identifier_part(bytes[cursor]) {
        cursor += 1;
    }
    let name = value.get(start..cursor)?.to_owned();
    while cursor < bytes.len() && is_javascript_space(bytes[cursor]) {
        cursor += 1;
    }
    if cursor >= bytes.len()
        || bytes[cursor] != b'='
        || cursor + 1 < bytes.len() && (bytes[cursor + 1] == b'=' || bytes[cursor + 1] == b'>')
    {
        return None;
    }
    let expression = trim_vue_source_expression(value.get(cursor + 1..)?);
    (!expression.is_empty()).then_some((name, expression))
}

pub(crate) fn append_definition_element_member(
    definition: &mut ComponentDefinition,
    collection_name: &str,
    element_member: VueComponentElementMember,
) {
    let Some(member) = definition
        .members
        .iter_mut()
        .find(|member| member.name == collection_name)
    else {
        return;
    };
    if let Some(existing) = member
        .element_members
        .iter_mut()
        .find(|existing| existing.name == element_member.name)
    {
        *existing = element_member;
        return;
    }
    member.element_members.push(element_member);
}

pub(crate) fn parse_component_assignments(
    object: &SyntaxNode,
    line_index: &LineIndex,
) -> Vec<VueComponentAssignment> {
    let initializers = component_const_initializers(object);
    let mut result = Vec::new();
    for statement in jsquery::nodes(object, &[jssyntax::JS_EXPRESSION_STATEMENT]) {
        let Some((target, mut expression)) =
            direct_vue_instance_assignment(&statement.text_string())
        else {
            continue;
        };
        if let Some(local) =
            visible_component_const_initializer(&statement, &expression, &initializers)
        {
            expression = local;
        } else if let Some(callback) =
            component_promise_callback_expression(&statement, &expression)
        {
            expression = callback;
        }
        result.push(VueComponentAssignment {
            target,
            expression,
            line: definition_node_line(line_index, &statement),
            ..Default::default()
        });
    }
    result
}

#[derive(Clone, Debug)]
pub(crate) struct ComponentConstInitializer {
    pub name: String,
    pub expression: String,
    pub start: u32,
    pub function: SyntaxNode,
    pub block: SyntaxNode,
}

pub(crate) fn component_const_initializers(object: &SyntaxNode) -> Vec<ComponentConstInitializer> {
    let mut result = Vec::new();
    for declaration in jsquery::nodes(object, &[jssyntax::JS_VARIABLE_DECLARATION]) {
        let Some((name, expression)) =
            direct_component_const_initializer(&declaration.text_string())
        else {
            continue;
        };
        let Some(function) = closest_javascript_function_scope(&declaration) else {
            continue;
        };
        let Some(block) = closest_javascript_block_scope(&declaration, Some(&function)) else {
            continue;
        };
        result.push(ComponentConstInitializer {
            name,
            expression,
            start: u32::from(declaration.range_trimmed_trivia().start()),
            function,
            block,
        });
    }
    result
}

/// Parses a single-declarator `const name = expression`.
pub(crate) fn direct_component_const_initializer(value: &str) -> Option<(String, String)> {
    let value = value.trim();
    let bytes = value.as_bytes();
    if !value.starts_with("const")
        || bytes.len() == "const".len()
        || !is_javascript_space(bytes["const".len()])
    {
        return None;
    }
    let mut cursor = "const".len();
    while cursor < bytes.len() && is_javascript_space(bytes[cursor]) {
        cursor += 1;
    }
    if cursor >= bytes.len() || !is_vue_identifier_start(bytes[cursor]) {
        return None;
    }
    let start = cursor;
    cursor += 1;
    while cursor < bytes.len() && is_vue_identifier_part(bytes[cursor]) {
        cursor += 1;
    }
    let name = value.get(start..cursor)?.to_owned();
    let mut state = SlotScanState::default();
    while cursor < bytes.len() {
        let current = bytes[cursor];
        if state.top_level() && current == b',' {
            // Multiple declarators require binding-aware parsing.
            return None;
        }
        if state.top_level()
            && current == b'='
            && (cursor + 1 >= bytes.len() || bytes[cursor + 1] != b'>')
        {
            let right = value.get(cursor + 1..)?;
            let mut right_state = SlotScanState::default();
            for &byte in right.as_bytes() {
                if right_state.top_level() && byte == b',' {
                    return None;
                }
                right_state.consume(byte);
            }
            let expression = trim_vue_source_expression(right);
            return (!expression.is_empty()).then_some((name, expression));
        }
        state.consume(current);
        cursor += 1;
    }
    None
}

pub(crate) fn visible_component_const_initializer(
    use_node: &SyntaxNode,
    identifier: &str,
    initializers: &[ComponentConstInitializer],
) -> Option<String> {
    let identifier = identifier.trim();
    if !is_static_vue_identifier(identifier) {
        return None;
    }
    let function = closest_javascript_function_scope(use_node)?;
    let blocks = visible_javascript_block_scopes(use_node, Some(&function));
    let use_start = u32::from(use_node.range_trimmed_trivia().start());
    let mut best_depth = blocks.len() + 1;
    let mut best: Option<&ComponentConstInitializer> = None;
    for initializer in initializers {
        let Some(&depth) = blocks.get(&initializer.block) else {
            continue;
        };
        if initializer.name != identifier
            || initializer.function != function
            || initializer.start >= use_start
        {
            continue;
        }
        let better = match best {
            None => true,
            Some(current) => {
                depth < best_depth || depth == best_depth && initializer.start > current.start
            }
        };
        if better {
            best = Some(initializer);
            best_depth = depth;
        }
    }
    best.map(|initializer| initializer.expression.clone())
}

pub(crate) fn closest_javascript_function_scope(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.ancestors().find(|current| {
        matches!(
            current.kind(),
            jssyntax::JS_METHOD | jssyntax::JS_FUNCTION | jssyntax::JS_ARROW_FUNCTION
        )
    })
}

pub(crate) fn closest_javascript_block_scope(
    node: &SyntaxNode,
    function: Option<&SyntaxNode>,
) -> Option<SyntaxNode> {
    for current in node.ancestors().skip(1) {
        if current.kind() == jssyntax::JS_BLOCK {
            return Some(current);
        }
        if function == Some(&current) {
            break;
        }
    }
    None
}

/// Every enclosing block up to `function`, mapped to its nesting distance
/// from `node` (0 = innermost).
pub(crate) fn visible_javascript_block_scopes(
    node: &SyntaxNode,
    function: Option<&SyntaxNode>,
) -> HashMap<SyntaxNode, usize> {
    let mut result = HashMap::new();
    let mut depth = 0;
    for current in node.ancestors().skip(1) {
        let stop = function == Some(&current);
        if current.kind() == jssyntax::JS_BLOCK {
            result.insert(current, depth);
            depth += 1;
        }
        if stop {
            break;
        }
    }
    result
}

pub(crate) fn is_static_vue_identifier(value: &str) -> bool {
    let bytes = value.as_bytes();
    !bytes.is_empty()
        && is_vue_identifier_start(bytes[0])
        && bytes[1..].iter().all(|byte| is_vue_identifier_part(*byte))
}

pub(crate) fn component_promise_callback_expression(
    use_node: &SyntaxNode,
    identifier: &str,
) -> Option<String> {
    let identifier = identifier.trim();
    if !is_static_vue_identifier(identifier) {
        return None;
    }
    let function = closest_javascript_function_scope(use_node)?;
    if function.kind() != jssyntax::JS_ARROW_FUNCTION
        || callback_first_parameter(Some(&function)) != identifier
    {
        return None;
    }
    let (_, call, argument_index) = callback_argument_call(&function)?;
    if argument_index != 0 || jsquery::call_method_name(&call).as_deref() != Some("then") {
        return None;
    }
    let receiver = static_callback_call_receiver(&call, "then")?;
    Some(format!("await {receiver}"))
}

/// The argument containing `function`, its call and the argument index.
pub(crate) fn callback_argument_call(
    function: &SyntaxNode,
) -> Option<(SyntaxNode, SyntaxNode, usize)> {
    let current = function
        .ancestors()
        .skip(1)
        .find(|current| current.kind() == jssyntax::JS_ARGUMENT)?;
    let list = current
        .parent()
        .filter(|list| list.kind() == jssyntax::JS_ARGUMENT_LIST)?;
    let call = list
        .parent()
        .filter(|call| call.kind() == jssyntax::JS_CALL_EXPRESSION)?;
    let index = jsquery::iter_arguments(&call).position(|argument| argument == current)?;
    Some((current, call, index))
}

pub(crate) fn callback_first_parameter(function: Option<&SyntaxNode>) -> String {
    let Some(function) = function else {
        return String::new();
    };
    let full_text = function.text_string();
    let text = full_text.trim();
    let parameters: String = if function.kind() == jssyntax::JS_ARROW_FUNCTION {
        let Some(arrow) = text.find("=>") else {
            return String::new();
        };
        let mut left = text[..arrow].trim();
        if let Some(rest) = left.strip_prefix("async ") {
            left = rest.trim();
        }
        if left.starts_with('(')
            && matching_slot_delimiter(left, 0, b'(', b')') == Some(left.len() - 1)
        {
            left[1..left.len() - 1].trim().to_owned()
        } else {
            left.to_owned()
        }
    } else {
        let Some((parameters, _)) = vue_method_header(text) else {
            return String::new();
        };
        parameters
    };
    let parts = split_admin_type_top_level(&parameters, b',');
    let Some(first) = parts.first() else {
        return String::new();
    };
    let mut parameter = first.trim();
    if let Some(rest) = parameter.strip_prefix("...") {
        parameter = rest.trim();
    }
    let bytes = parameter.as_bytes();
    if bytes.is_empty() || !is_vue_identifier_start(bytes[0]) {
        return String::new();
    }
    let mut cursor = 1;
    while cursor < bytes.len() && is_vue_identifier_part(bytes[cursor]) {
        cursor += 1;
    }
    if cursor < bytes.len()
        && !is_javascript_space(bytes[cursor])
        && !matches!(bytes[cursor], b':' | b'?' | b'=')
    {
        return String::new();
    }
    parameter.get(..cursor).unwrap_or_default().to_owned()
}

/// The receiver text before `.method` (or `?.method`) of a call's member
/// callee.
pub(crate) fn static_callback_call_receiver(call: &SyntaxNode, method: &str) -> Option<String> {
    if method.is_empty() {
        return None;
    }
    let callee = call.first_child()?;
    if callee.kind() != jssyntax::JS_MEMBER_EXPRESSION {
        return None;
    }
    let full_text = callee.text_string();
    let text = full_text.trim();
    let bytes = text.as_bytes();
    let mut end = bytes.len();
    while end > 0 && is_javascript_space(bytes[end - 1]) {
        end -= 1;
    }
    let mut start = end;
    while start > 0 && is_vue_identifier_part(bytes[start - 1]) {
        start -= 1;
    }
    if &bytes[start..end] != method.as_bytes() {
        return None;
    }
    let mut separator = start;
    while separator > 0 && is_javascript_space(bytes[separator - 1]) {
        separator -= 1;
    }
    if separator == 0 || bytes[separator - 1] != b'.' {
        return None;
    }
    separator -= 1;
    if separator > 0 && bytes[separator - 1] == b'?' {
        separator -= 1;
    }
    let receiver = String::from_utf8_lossy(&bytes[..separator])
        .trim()
        .to_owned();
    (!receiver.is_empty()).then_some(receiver)
}

/// Matches `this.<target> = <expression>`.
pub(crate) fn direct_vue_instance_assignment(value: &str) -> Option<(String, String)> {
    let value = value.trim();
    if !value.starts_with("this.") {
        return None;
    }
    direct_assignment_after(value, "this.".len())
}
