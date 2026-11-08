//! Prop declarations, JSDoc metadata and closed validator domains (port of
//! Go `definition_parser_props.go`).

use crate::*;

pub(crate) fn parse_props(node: &SyntaxNode, line_index: &LineIndex) -> Vec<VueComponentProp> {
    if node.kind() == jssyntax::JS_ARRAY {
        let mut props = Vec::new();
        for item in jsquery::array_items(node) {
            if item.kind() != jssyntax::JS_STRING {
                continue;
            }
            let name = jsquery::string_value(&item);
            if name.is_empty() {
                continue;
            }
            props.push(VueComponentProp {
                name,
                line: definition_node_line(line_index, &item),
                documentation: javascript_documentation(&item),
                name_range: component_member_name_range(Some(&item), line_index),
                deprecated: javascript_deprecation(&item),
                ..Default::default()
            });
        }
        return props;
    }
    if node.kind() != jssyntax::JS_OBJECT {
        return Vec::new();
    }

    let mut props = Vec::new();
    for property in jsquery::properties(node) {
        let name = jsquery::property_name(&property).unwrap_or_default();
        if name.is_empty() {
            continue;
        }
        let mut prop = VueComponentProp {
            name,
            line: definition_node_line(line_index, &property),
            documentation: javascript_documentation(&property),
            name_range: component_member_name_range(
                jsquery::property_name_node(&property).as_ref(),
                line_index,
            ),
            deprecated: javascript_deprecation(&property),
            ..Default::default()
        };
        let Some(value) = jsquery::property_value(&property) else {
            props.push(prop);
            continue;
        };

        if value.kind() == jssyntax::JS_IDENTIFIER {
            prop.type_ = value.text().to_string().trim().to_owned();
        } else if value.kind() == jssyntax::JS_OBJECT {
            if let Some(type_property) = jsquery::property(&value, "type") {
                prop.type_ = parse_vue_prop_type(&value, &type_property);
            }
            if let Some(required) = jsquery::property(&value, "required") {
                prop.required =
                    node_text(jsquery::property_value(&required).as_ref()).trim() == "true";
            }
            if let Some(default_property) = jsquery::property(&value, "default") {
                let default_value = jsquery::property_value(&default_property);
                prop.default = node_text(default_value.as_ref()).trim().to_owned();
                if let Some(default_value) =
                    default_value.filter(|value| value.kind() == jssyntax::JS_STRING)
                {
                    append_allowed_value(
                        &mut prop.allowed_values,
                        &jsquery::string_value(&default_value),
                    );
                }
            }
            if let Some(valid_values) = jsquery::property(&value, "validValues") {
                let (values, complete) =
                    parse_vue_prop_valid_values(jsquery::property_value(&valid_values).as_ref());
                for allowed in values {
                    append_allowed_value(&mut prop.allowed_values, &allowed);
                }
                prop.allowed_values_complete = complete;
            }
            if let Some(validator) = jsquery::property(&value, "validator") {
                let (values, complete) = parse_vue_prop_validator_values(&validator, &prop.type_);
                for allowed in values {
                    append_allowed_value(&mut prop.allowed_values, &allowed);
                }
                prop.allowed_values_complete = prop.allowed_values_complete || complete;
            }
            // A closed domain of string literals is also a complete runtime
            // string contract.
            if prop.type_.is_empty()
                && prop.allowed_values_complete
                && !prop.allowed_values.is_empty()
            {
                prop.type_ = "String".to_owned();
            }
        }
        props.push(prop);
    }
    props
}

/// The normalized text of a leading JSDoc `@deprecated` tag. Only trivia
/// before the declaration's first syntax token is inspected.
pub fn javascript_deprecation(node: &SyntaxNode) -> String {
    for token in leading_trivia_tokens(node) {
        if matches!(
            token.kind(),
            jssyntax::TK_BLOCK_COMMENT | jssyntax::TK_LINE_COMMENT
        ) {
            let deprecation = parse_javascript_deprecation(token.text());
            if !deprecation.is_empty() {
                return deprecation;
            }
        }
    }
    String::new()
}

/// The public description from the leading JSDoc of a declaration.
pub fn javascript_documentation(node: &SyntaxNode) -> String {
    for token in leading_trivia_tokens(node) {
        if token.kind() != jssyntax::TK_BLOCK_COMMENT {
            continue;
        }
        let documentation = parse_javascript_documentation(token.text());
        if !documentation.is_empty() {
            return documentation;
        }
    }
    String::new()
}

pub(crate) fn parse_javascript_documentation(comment: &str) -> String {
    let comment = comment.trim();
    let Some(comment) = comment.strip_prefix("/**") else {
        return String::new();
    };
    let comment = comment.strip_suffix("*/").unwrap_or(comment);
    let mut result: Vec<&str> = Vec::new();
    let mut collecting = true;
    for line in comment.split('\n') {
        let line = line.trim();
        let mut line = line.strip_prefix('*').unwrap_or(line).trim();
        if let Some(rest) = line.strip_prefix("@description") {
            collecting = true;
            line = rest.trim();
        } else if line.starts_with('@') {
            collecting = false;
            continue;
        }
        if collecting {
            result.push(line);
        }
    }
    while result.first() == Some(&"") {
        result.remove(0);
    }
    while result.last() == Some(&"") {
        result.pop();
    }
    result.join("\n").trim().to_owned()
}

pub(crate) fn parse_javascript_deprecation(comment: &str) -> String {
    let comment = comment.trim();
    let comment = comment.strip_prefix("/*").unwrap_or(comment);
    let comment = comment.strip_suffix("*/").unwrap_or(comment);
    let mut result: Vec<&str> = Vec::new();
    let mut found = false;
    for line in comment.split('\n') {
        let line = line.trim();
        let line = line.strip_prefix("//").unwrap_or(line).trim();
        let mut line = line.strip_prefix('*').unwrap_or(line).trim();
        if !found {
            let Some(position) = line.find("@deprecated") else {
                continue;
            };
            found = true;
            line = line[position + "@deprecated".len()..].trim();
        } else if line.starts_with('@') {
            break;
        }
        if !line.is_empty() {
            result.push(line);
        }
    }
    result.join(" ")
}

pub(crate) fn parse_vue_prop_valid_values(node: Option<&SyntaxNode>) -> (Vec<String>, bool) {
    let Some(node) = node.filter(|node| node.kind() == jssyntax::JS_ARRAY) else {
        return (Vec::new(), false);
    };
    let items = jsquery::array_items(node);
    let mut values = Vec::with_capacity(items.len());
    let mut complete = true;
    for item in &items {
        if item.kind() != jssyntax::JS_STRING {
            complete = false;
            continue;
        }
        append_allowed_value(&mut values, &jsquery::string_value(item));
    }
    let complete = complete && !values.is_empty();
    (values, complete)
}

/// Recognizes the closed runtime-validator forms used by Shopware's Options
/// API components (`['a', 'b'].includes(value)`, optionally behind the empty
/// string guard or a local const array). Everything else stays open.
pub(crate) fn parse_vue_prop_validator_values(
    validator: &SyntaxNode,
    prop_type: &str,
) -> (Vec<String>, bool) {
    let callable = if validator.kind() == jssyntax::JS_PROPERTY {
        jsquery::property_value(validator)
    } else {
        Some(validator.clone())
    };
    let Some(callable) = callable else {
        return (Vec::new(), false);
    };
    let parameter = callback_first_parameter(Some(&callable));
    if parameter.is_empty() {
        return (Vec::new(), false);
    }
    let mut callable_source = callable.text().to_string();
    let arrow_function = callable.kind() == jssyntax::JS_ARROW_FUNCTION;
    let mut accepts_empty_string = false;
    if vue_prop_value_type(prop_type) == "string" {
        (callable_source, accepts_empty_string) =
            strip_vue_validator_empty_string_guard(&callable_source, &parameter, arrow_function);
    }
    let mut local_name = String::new();
    let mut local_values = Vec::new();
    let mut local_values_complete = false;
    if let Some((stripped, name, values, complete)) =
        strip_vue_validator_local_string_array(&callable_source, arrow_function)
    {
        callable_source = stripped;
        local_name = name;
        local_values = values;
        local_values_complete = complete;
    }
    let Some(expression) = vue_validator_return_expression(&callable_source, arrow_function) else {
        return (Vec::new(), false);
    };
    let expression = expression.trim();
    if expression.is_empty() {
        return (Vec::new(), false);
    }
    let mut values;
    let complete;
    let mut tail: &str;
    if !local_name.is_empty() {
        let Some(rest) = expression.strip_prefix(local_name.as_str()) else {
            return (Vec::new(), false);
        };
        tail = rest.trim();
        values = local_values;
        complete = local_values_complete;
    } else {
        if !expression.starts_with('[') {
            return (Vec::new(), false);
        }
        let Some(array_end) = matching_slot_delimiter(expression, 0, b'[', b']') else {
            return (Vec::new(), false);
        };
        tail = expression[array_end + 1..].trim();
        (values, complete) = parse_vue_validator_literal_string_array(&expression[..array_end + 1]);
    }
    let Some(rest) = tail.strip_prefix(".includes") else {
        return (Vec::new(), false);
    };
    tail = rest.trim();
    if !tail.starts_with('(') {
        return (Vec::new(), false);
    }
    let Some(call_end) = matching_slot_delimiter(tail, 0, b'(', b')') else {
        return (Vec::new(), false);
    };
    let after = tail[call_end + 1..].trim();
    if tail[1..call_end].trim() != parameter
        || after.strip_suffix(';').unwrap_or(after).trim() != ""
    {
        return (Vec::new(), false);
    }
    if accepts_empty_string {
        append_allowed_value(&mut values, "");
    }
    (values, complete)
}

pub(crate) fn parse_vue_validator_literal_string_array(source: &str) -> (Vec<String>, bool) {
    let root = swlsp_parser_js::parse(source).root();
    let arrays = jsquery::nodes(&root, &[jssyntax::JS_ARRAY]);
    if arrays.len() != 1 {
        return (Vec::new(), false);
    }
    parse_vue_prop_valid_values(Some(&arrays[0]))
}

fn skip_javascript_space_bytes(bytes: &[u8], mut cursor: usize) -> usize {
    while cursor < bytes.len() && is_javascript_space(bytes[cursor]) {
        cursor += 1;
    }
    cursor
}

/// Resolves only an immutable literal array declared as the validator body's
/// first statement and consumed by its sole return. Returns the stripped
/// source, the local name, its values and whether they are complete.
pub(crate) fn strip_vue_validator_local_string_array(
    value: &str,
    arrow_function: bool,
) -> Option<(String, String, Vec<String>, bool)> {
    let value = value.trim();
    let (body_open, body_close) = vue_validator_body_range(value, arrow_function)?;
    let body = &value[body_open + 1..body_close];
    let bytes = body.as_bytes();
    let declaration_start = skip_javascript_space_bytes(bytes, 0);
    if !body[declaration_start..].starts_with("const") {
        return None;
    }
    let mut cursor = declaration_start + "const".len();
    if cursor >= bytes.len() || !is_javascript_space(bytes[cursor]) {
        return None;
    }
    cursor = skip_javascript_space_bytes(bytes, cursor);
    if cursor >= bytes.len() || !is_vue_identifier_start(bytes[cursor]) {
        return None;
    }
    let name_start = cursor;
    cursor += 1;
    while cursor < bytes.len() && is_vue_identifier_part(bytes[cursor]) {
        cursor += 1;
    }
    let name = &body[name_start..cursor];
    cursor = skip_javascript_space_bytes(bytes, cursor);
    if cursor >= bytes.len()
        || bytes[cursor] != b'='
        || cursor + 1 < bytes.len() && (bytes[cursor + 1] == b'=' || bytes[cursor + 1] == b'>')
    {
        return None;
    }
    cursor = skip_javascript_space_bytes(bytes, cursor + 1);
    if cursor >= bytes.len() || bytes[cursor] != b'[' {
        return None;
    }
    let array_start = cursor;
    let array_end = matching_slot_delimiter(body, array_start, b'[', b']')?;
    let (values, complete) =
        parse_vue_validator_literal_string_array(&body[array_start..array_end + 1]);
    cursor = array_end + 1;
    let mut has_line_terminator = false;
    while cursor < bytes.len() && is_javascript_space(bytes[cursor]) {
        has_line_terminator =
            has_line_terminator || bytes[cursor] == b'\n' || bytes[cursor] == b'\r';
        cursor += 1;
    }
    if cursor < bytes.len() && bytes[cursor] == b';' {
        cursor += 1;
    } else if !has_line_terminator {
        return None;
    }
    cursor = skip_javascript_space_bytes(bytes, cursor);
    let return_end = cursor + "return".len();
    if !body[cursor..].starts_with("return")
        || return_end < bytes.len() && is_vue_identifier_part(bytes[return_end])
    {
        return None;
    }
    let stripped = format!(
        "{}{}{}{}",
        &value[..body_open + 1],
        &body[..declaration_start],
        &body[cursor..],
        &value[body_close..]
    );
    Some((stripped, name.to_owned(), values, complete))
}

/// Accepts only Shopware's exact leading String-prop guard
/// `if (!value.length) { return true; }`.
pub(crate) fn strip_vue_validator_empty_string_guard(
    value: &str,
    parameter: &str,
    arrow_function: bool,
) -> (String, bool) {
    let value = value.trim();
    if value.is_empty() || parameter.is_empty() {
        return (value.to_owned(), false);
    }
    let Some((body_open, body_close)) = vue_validator_body_range(value, arrow_function) else {
        return (value.to_owned(), false);
    };
    let body = &value[body_open + 1..body_close];
    let bytes = body.as_bytes();
    let guard_start = skip_javascript_space_bytes(bytes, 0);
    if !body[guard_start..].starts_with("if") {
        return (value.to_owned(), false);
    }
    let mut cursor = skip_javascript_space_bytes(bytes, guard_start + "if".len());
    if cursor >= bytes.len() || bytes[cursor] != b'(' {
        return (value.to_owned(), false);
    }
    let Some(condition_end) = matching_slot_delimiter(body, cursor, b'(', b')') else {
        return (value.to_owned(), false);
    };
    if compact_vue_validator_code(&body[cursor + 1..condition_end])
        != format!("!{parameter}.length")
    {
        return (value.to_owned(), false);
    }
    cursor = skip_javascript_space_bytes(bytes, condition_end + 1);
    if cursor >= bytes.len() || bytes[cursor] != b'{' {
        return (value.to_owned(), false);
    }
    let Some(guard_end) = matching_slot_delimiter(body, cursor, b'{', b'}') else {
        return (value.to_owned(), false);
    };
    let guard = compact_vue_validator_code(&body[cursor + 1..guard_end]);
    if guard.strip_suffix(';').unwrap_or(&guard) != "returntrue" {
        return (value.to_owned(), false);
    }
    (
        format!(
            "{}{}{}{}",
            &value[..body_open + 1],
            &body[..guard_start],
            &body[guard_end + 1..],
            &value[body_close..]
        ),
        true,
    )
}

pub(crate) fn vue_validator_body_range(
    value: &str,
    arrow_function: bool,
) -> Option<(usize, usize)> {
    let value = value.trim();
    if value.is_empty() {
        return None;
    }
    let bytes = value.as_bytes();
    let body_open = if arrow_function {
        let arrow = value.find("=>")?;
        let open = skip_javascript_space_bytes(bytes, arrow + 2);
        if open >= bytes.len() || bytes[open] != b'{' {
            return None;
        }
        open
    } else {
        value.find('{')?
    };
    let body_close = matching_slot_delimiter(value, body_open, b'{', b'}')?;
    if !value[body_close + 1..].trim().is_empty() {
        return None;
    }
    Some((body_open, body_close))
}

pub(crate) fn compact_vue_validator_code(value: &str) -> String {
    value
        .chars()
        .filter(|character| !(character.is_ascii() && is_javascript_space(*character as u8)))
        .collect()
}

pub(crate) fn vue_validator_return_expression(value: &str, arrow_function: bool) -> Option<String> {
    let value = value.trim();
    if value.is_empty() {
        return None;
    }
    if arrow_function {
        let arrow = value.find("=>")?;
        let body = value[arrow + 2..].trim();
        if body.is_empty() {
            return None;
        }
        if !body.starts_with('{') {
            return Some(trim_vue_source_expression(body));
        }
        if matching_slot_delimiter(body, 0, b'{', b'}') != Some(body.len() - 1) {
            return None;
        }
        return vue_validator_block_return(&body[1..body.len() - 1]);
    }
    let open = value.find('{')?;
    let close = matching_slot_delimiter(value, open, b'{', b'}')?;
    if !value[close + 1..].trim().is_empty() {
        return None;
    }
    vue_validator_block_return(&value[open + 1..close])
}

pub(crate) fn vue_validator_block_return(body: &str) -> Option<String> {
    let bytes = body.as_bytes();
    let keyword = b"return";
    let mut return_at: Option<usize> = None;
    let mut cursor = 0;
    while cursor + keyword.len() <= bytes.len() {
        let end = cursor + keyword.len();
        let matched = &bytes[cursor..end] == keyword
            && !(cursor > 0 && is_vue_identifier_part(bytes[cursor - 1]))
            && !(end < bytes.len() && is_vue_identifier_part(bytes[end]))
            && admin_type_code_position(body, cursor);
        if matched {
            if return_at.is_some() {
                return None;
            }
            return_at = Some(cursor);
        }
        cursor += 1;
    }
    let return_at = return_at?;
    if !body[..return_at].trim().is_empty() {
        return None;
    }
    let expression = body[return_at + keyword.len()..].trim();
    (!expression.is_empty()).then(|| expression.to_owned())
}

/// Preserves both Vue's runtime constructor and its optional TypeScript
/// assertion (`Object as PropType<Foo>`) by scanning to the top-level comma.
pub(crate) fn parse_vue_prop_type(config: &SyntaxNode, type_property: &SyntaxNode) -> String {
    let source = config.text().to_string();
    let bytes = source.as_bytes();
    let config_start = u32::from(config.text_range().start());
    let type_start = u32::from(type_property.range_trimmed_trivia().start());
    if type_start < config_start {
        return String::new();
    }
    let mut start = (type_start - config_start) as usize;
    if start >= bytes.len() {
        return String::new();
    }
    let Some(colon) = source[start..].find(':') else {
        return String::new();
    };
    start = skip_javascript_space_bytes(bytes, start + colon + 1);
    let mut end = start;
    let (mut round, mut square, mut curly, mut angle) = (0usize, 0usize, 0usize, 0usize);
    let mut quote = 0u8;
    let slice = |start: usize, end: usize| {
        String::from_utf8_lossy(&bytes[start.min(bytes.len())..end.min(bytes.len())])
            .trim()
            .to_owned()
    };
    while end < bytes.len() {
        let current = bytes[end];
        if quote != 0 {
            if current == b'\\' {
                end += 2;
                continue;
            }
            if current == quote {
                quote = 0;
            }
            end += 1;
            continue;
        }
        match current {
            b'\'' | b'"' | b'`' => quote = current,
            b'(' => round += 1,
            b')' => round = round.saturating_sub(1),
            b'[' => square += 1,
            b']' => square = square.saturating_sub(1),
            b'{' => curly += 1,
            b'}' => {
                if curly == 0 && round == 0 && square == 0 && angle == 0 {
                    return slice(start, end);
                }
                curly = curly.saturating_sub(1);
            }
            b'<' => angle += 1,
            b'>' => angle = angle.saturating_sub(1),
            b',' if round == 0 && square == 0 && curly == 0 && angle == 0 => {
                return slice(start, end);
            }
            _ => {}
        }
        end += 1;
    }
    slice(start, end)
}

pub(crate) fn is_javascript_space(value: u8) -> bool {
    matches!(value, b' ' | b'\t' | b'\r' | b'\n')
}
