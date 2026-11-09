//! Lightweight value-type inference for Options API members and template
//! expressions.

use std::sync::LazyLock;

use regex::Regex;

use crate::*;

static VUE_JSDOC_TYPE_PATTERN: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"@type[\t\n\f\r ]*\{([^}]+)\}").expect("valid regex"));
static VUE_JSDOC_RETURN_PATTERN: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"@returns?[\t\n\f\r ]*\{([^}]+)\}").expect("valid regex"));

/// ASCII white space including vertical tab (`\t`, `\n`, `\x0B`, `\x0C`,
/// `\r`, space).
pub(crate) fn is_ascii_space(value: u8) -> bool {
    value.is_ascii_whitespace() || value == 0x0b
}

fn property_name_or_empty(node: &SyntaxNode) -> String {
    jsquery::property_name(node).unwrap_or_default()
}

fn node_properties(node: Option<&SyntaxNode>) -> Vec<SyntaxNode> {
    node.map(jsquery::properties).unwrap_or_default()
}

/// Retains type information already present in an Options API component.
pub(crate) fn enrich_definition_member_types(
    object: &SyntaxNode,
    definition: &mut ComponentDefinition,
) {
    let mut known: HashMap<String, String> = HashMap::new();
    for prop in &definition.props {
        known.insert(prop.name.clone(), prop.type_.clone());
    }

    if let Some(data) = jsquery::property(object, "data") {
        let declared = vue_method_return_object_member_types(&data);
        for property in node_properties(first_object(&data).as_ref()) {
            let name = property_name_or_empty(&property);
            if name.is_empty() {
                continue;
            }
            let mut member_type = declared.get(&name).cloned().unwrap_or_default();
            let mut open_runtime_shape = false;
            if member_type.is_empty() {
                member_type = vue_property_value_type(&property, Some(&known));
                open_runtime_shape = vue_property_has_inferred_object_shape(&property);
            }
            set_definition_member_inference(
                definition,
                VueComponentMemberKind::Data,
                &name,
                &member_type,
                &vue_property_source_expression(&property),
                open_runtime_shape,
            );
            if !member_type.is_empty() {
                known.insert(name, member_type);
            }
        }
    }

    if let Some(computed) = jsquery::property(object, "computed") {
        let computed_object = jsquery::property_value(&computed);
        for method in node_properties(computed_object.as_ref()) {
            let name = property_name_or_empty(&method);
            if name.is_empty() {
                continue;
            }
            let member_type = vue_method_return_type(&method, Some(&known));
            set_definition_member_inference(
                definition,
                VueComponentMemberKind::Computed,
                &name,
                &member_type,
                &vue_method_return_expression(&method),
                vue_method_has_inferred_object_shape(&method),
            );
            set_definition_member_returns(
                definition,
                VueComponentMemberKind::Computed,
                &name,
                &method,
            );
            set_definition_member_cms_registry(
                definition,
                VueComponentMemberKind::Computed,
                &name,
                &method,
            );
            if !member_type.is_empty() {
                known.insert(name, member_type);
            }
        }
    }

    if let Some(methods) = jsquery::property(object, "methods") {
        let methods_object = jsquery::property_value(&methods);
        for method in node_properties(methods_object.as_ref()) {
            let name = property_name_or_empty(&method);
            if name.is_empty() {
                continue;
            }
            set_definition_member_inference(
                definition,
                VueComponentMemberKind::Method,
                &name,
                &vue_method_signature(&method, Some(&known)),
                &vue_method_return_expression(&method),
                vue_method_has_inferred_object_shape(&method),
            );
            set_definition_member_returns(
                definition,
                VueComponentMemberKind::Method,
                &name,
                &method,
            );
            set_definition_member_cms_registry(
                definition,
                VueComponentMemberKind::Method,
                &name,
                &method,
            );
        }
    }
}

pub(crate) fn set_definition_member_cms_registry(
    definition: &mut ComponentDefinition,
    kind: VueComponentMemberKind,
    name: &str,
    method: &SyntaxNode,
) {
    let Some(registry_kind) = cms_registry_kind_from_expression(&method.text().to_string()) else {
        return;
    };
    if let Some(member) = definition
        .members
        .iter_mut()
        .find(|member| member.kind == kind && member.name == name)
    {
        member.cms_registry_kind = registry_kind;
    }
}

pub(crate) fn set_definition_member_returns(
    definition: &mut ComponentDefinition,
    kind: VueComponentMemberKind,
    name: &str,
    method: &SyntaxNode,
) {
    if let Some(member) = definition
        .members
        .iter_mut()
        .find(|member| member.kind == kind && member.name == name)
    {
        member.return_expressions = vue_method_return_expressions(method);
        member.returns_complete = vue_method_returns_complete(method);
    }
}

pub(crate) fn vue_method_return_object_member_types(
    method: &SyntaxNode,
) -> HashMap<String, String> {
    let mut result = HashMap::new();
    let Some((_, return_type)) = vue_method_header(&method.text().to_string()) else {
        return result;
    };
    if return_type.is_empty() {
        return result;
    }
    for member in vue_type_members(&return_type) {
        if !member.name.is_empty() && !member.type_.is_empty() {
            result.insert(member.name, member.type_);
        }
    }
    result
}

pub(crate) fn set_definition_member_inference(
    definition: &mut ComponentDefinition,
    kind: VueComponentMemberKind,
    name: &str,
    member_type: &str,
    source_expression: &str,
    open_runtime_shape: bool,
) {
    if let Some(member) = definition
        .members
        .iter_mut()
        .find(|member| member.kind == kind && member.name == name)
    {
        if !member_type.is_empty() {
            member.type_ = member_type.to_owned();
        }
        member.source_expression = source_expression.to_owned();
        member.open_runtime_shape = open_runtime_shape;
    }
}

pub(crate) fn vue_property_has_inferred_object_shape(property: &SyntaxNode) -> bool {
    let text = property.text().to_string();
    if VUE_JSDOC_TYPE_PATTERN.is_match(&text) || !vue_type_assertion(&text).is_empty() {
        return false;
    }
    jsquery::property_value(property).is_some_and(|value| value.kind() == jssyntax::JS_OBJECT)
}

pub(crate) fn vue_method_has_inferred_object_shape(method: &SyntaxNode) -> bool {
    let text = method.text().to_string();
    if VUE_JSDOC_RETURN_PATTERN.is_match(&text) {
        return false;
    }
    if let Some((_, return_type)) = vue_method_header(&text)
        && !return_type.is_empty()
    {
        return false;
    }
    vue_runtime_object_literal_expression(&vue_method_return_expression(method))
}

/// Strips balanced outer parentheses repeatedly.
fn strip_outer_parentheses(value: &str) -> &str {
    let mut value = value;
    while value.len() >= 2
        && value.as_bytes()[0] == b'('
        && matching_slot_delimiter(value, 0, b'(', b')') == Some(value.len() - 1)
    {
        value = value[1..value.len() - 1].trim();
    }
    value
}

pub(crate) fn vue_runtime_object_literal_expression(value: &str) -> bool {
    let value = trim_vue_source_expression(value);
    if value.is_empty() || !vue_type_assertion(&value).is_empty() {
        return false;
    }
    strip_outer_parentheses(&value).starts_with('{')
}

pub(crate) fn vue_property_source_expression(property: &SyntaxNode) -> String {
    jsquery::property_value(property)
        .map(|value| trim_vue_source_expression(&value.text().to_string()))
        .unwrap_or_default()
}

pub(crate) fn vue_property_value_type<'k>(
    property: &SyntaxNode,
    known: impl Into<Option<&'k HashMap<String, String>>>,
) -> String {
    let known: Option<&HashMap<String, String>> = known.into();
    let text = property.text().to_string();
    if let Some(captures) = VUE_JSDOC_TYPE_PATTERN.captures(&text) {
        return captures[1].trim().to_owned();
    }
    let asserted = vue_type_assertion(&text);
    if !asserted.is_empty() {
        return asserted;
    }
    jsquery::property_value(property)
        .map(|value| vue_expression_type(&value, known))
        .unwrap_or_default()
}

pub(crate) fn vue_expression_type<'k>(
    node: &SyntaxNode,
    known: impl Into<Option<&'k HashMap<String, String>>>,
) -> String {
    let known: Option<&HashMap<String, String>> = known.into();
    use jssyntax::*;
    match node.kind() {
        JS_STRING => "string".to_owned(),
        JS_NUMBER => "number".to_owned(),
        JS_BOOLEAN => "boolean".to_owned(),
        JS_NULL => "null".to_owned(),
        JS_ARRAY => {
            let mut element_types: Vec<String> = Vec::new();
            let mut seen = HashSet::new();
            for item in jsquery::array_items(node) {
                let item_type = vue_expression_type(&item, known);
                if item_type.is_empty() || !seen.insert(item_type.clone()) {
                    continue;
                }
                element_types.push(item_type);
            }
            if element_types.is_empty() {
                return "Array".to_owned();
            }
            format!("Array<{}>", element_types.join(" | "))
        }
        JS_OBJECT => {
            let mut fields = Vec::new();
            for property in jsquery::properties(node) {
                let name = property_name_or_empty(&property);
                if name.is_empty() {
                    continue;
                }
                let mut field_type = vue_property_value_type(&property, known);
                if field_type.is_empty() {
                    field_type = "unknown".to_owned();
                }
                fields.push(format!("{name}: {field_type}"));
            }
            if fields.is_empty() {
                return "Object".to_owned();
            }
            format!("{{ {} }}", fields.join("; "))
        }
        JS_MEMBER_EXPRESSION | JS_IDENTIFIER => {
            let value = compact_vue_expression(&node.text().to_string());
            if let Some(name) = value.strip_prefix("this.")
                && !name.contains(['.', '[', '(', ')', '?'])
            {
                return known
                    .and_then(|known| known.get(name))
                    .cloned()
                    .unwrap_or_default();
            }
            String::new()
        }
        JS_ARROW_FUNCTION | JS_FUNCTION => "Function".to_owned(),
        _ => String::new(),
    }
}

pub(crate) fn vue_type_assertion(value: &str) -> String {
    let value = value.trim();
    let bytes = value.as_bytes();
    let mut position: Option<usize> = None;
    let mut state = SlotScanState::default();
    let mut index = 0;
    while index + 4 <= bytes.len() {
        if state.top_level() && &bytes[index..index + 4] == b" as " {
            position = Some(index + 4);
        }
        state.consume(bytes[index]);
        index += 1;
    }
    let Some(position) = position else {
        return String::new();
    };
    if position >= value.len() {
        return String::new();
    }
    let asserted = value[position..].trim();
    let asserted = asserted.strip_suffix(',').unwrap_or(asserted);
    asserted.trim().to_owned()
}

pub(crate) fn vue_method_return_type<'k>(
    method: &SyntaxNode,
    known: impl Into<Option<&'k HashMap<String, String>>>,
) -> String {
    let known: Option<&HashMap<String, String>> = known.into();
    let method = vue_method_implementation(method);
    let text = method.text().to_string();
    if let Some(captures) = VUE_JSDOC_RETURN_PATTERN.captures(&text) {
        return captures[1].trim().to_owned();
    }
    if let Some((_, return_type)) = vue_method_header(&text)
        && !return_type.is_empty()
    {
        return return_type;
    }
    let expressions = vue_method_return_expressions(&method);
    if expressions.is_empty() {
        return String::new();
    }
    let mut result = String::new();
    for expression in &expressions {
        let inferred = vue_expression_text_type(expression, known);
        if inferred.is_empty() {
            // One unresolved branch makes the complete return contract open.
            return String::new();
        }
        result = merge_vue_types(&[&result, &inferred]);
    }
    result
}

pub(crate) fn vue_method_declared_return_type(method: &SyntaxNode) -> String {
    let method = vue_method_implementation(method);
    match vue_method_header(&method.text().to_string()) {
        Some((_, return_type)) => return_type.trim().to_owned(),
        None => String::new(),
    }
}

/// One unconditional top-level return value; branch-dependent and multiple
/// returns are ignored.
pub(crate) fn vue_method_return_expression(method: &SyntaxNode) -> String {
    let mut expressions = vue_method_return_expressions(method);
    if expressions.len() != 1 {
        return String::new();
    }
    expressions.pop().unwrap_or_default()
}

/// Returns owned by the method, excluding callbacks and nested functions.
pub(crate) fn vue_method_return_expressions(method: &SyntaxNode) -> Vec<String> {
    use jssyntax::*;
    let method = vue_method_implementation(method);
    if let Some(block) = method.children().find(|child| child.kind() == JS_BLOCK) {
        fn collect(node: &SyntaxNode, result: &mut Vec<String>) -> bool {
            for child in node.children() {
                match child.kind() {
                    JS_ARROW_FUNCTION | JS_FUNCTION => continue,
                    JS_RETURN_STATEMENT => {
                        let expression = vue_return_statement_expression(&child);
                        if expression.is_empty() {
                            return false;
                        }
                        result.push(expression);
                    }
                    JS_EXPRESSION_STATEMENT => {
                        let loose = vue_loose_return_expressions(&child);
                        if !loose.is_empty() {
                            result.extend(loose);
                            continue;
                        }
                        if !collect(&child, result) {
                            return false;
                        }
                    }
                    _ => {
                        if !collect(&child, result) {
                            return false;
                        }
                    }
                }
            }
            true
        }
        let mut result = Vec::new();
        if !collect(&block, &mut result) {
            return Vec::new();
        }
        return result;
    }

    if method.kind() != JS_ARROW_FUNCTION {
        return Vec::new();
    }
    let text = method.text().to_string();
    let Some(arrow) = text.find("=>") else {
        return Vec::new();
    };
    let body = text[arrow + 2..].trim();
    if body.is_empty() || body.starts_with('{') {
        return Vec::new();
    }
    vec![trim_vue_source_expression(body)]
}

pub(crate) fn vue_loose_return_expressions(node: &SyntaxNode) -> Vec<String> {
    let node_range = node.text_range();
    let range_start = u32::from(node_range.start());
    let range_end = u32::from(node_range.end());
    let text = node.text().to_string();
    let mut result = Vec::new();
    let mut pending: Vec<u32> = Vec::new();
    visit_vue_syntax_tokens(node, &mut |token: &SyntaxToken| {
        if token.kind() == jssyntax::TK_KEYWORD && token.text() == "return" {
            pending.push(u32::from(token.text_range().end()));
        }
        if token.kind() != jssyntax::TK_SEMICOLON || pending.is_empty() {
            return;
        }
        append_vue_loose_return_expressions(
            &mut result,
            &pending,
            u32::from(token.text_range().start()),
            range_start,
            range_end,
            &text,
        );
        pending.clear();
    });
    append_vue_loose_return_expressions(
        &mut result,
        &pending,
        range_end,
        range_start,
        range_end,
        &text,
    );
    result
}

pub(crate) fn append_vue_loose_return_expressions(
    result: &mut Vec<String>,
    starts: &[u32],
    end: u32,
    range_start: u32,
    range_end: u32,
    text: &str,
) {
    for &start in starts {
        if start > end || start < range_start || end > range_end {
            continue;
        }
        let relative_start = (start - range_start) as usize;
        let relative_end = (end - range_start) as usize;
        let Some(slice) = text.get(relative_start..relative_end) else {
            continue;
        };
        let expression = trim_vue_source_expression(slice);
        if !expression.is_empty() {
            result.push(expression);
        }
    }
}

/// Proves the small control-flow shapes used by component selectors.
pub(crate) fn vue_method_returns_complete(method: &SyntaxNode) -> bool {
    let method = vue_method_implementation(method);
    let Some(block) = direct_javascript_block(&method) else {
        return method.kind() == jssyntax::JS_ARROW_FUNCTION
            && vue_method_return_expressions(&method).len() == 1;
    };
    let mut flow = VueControlFlowCompleteness::default();
    visit_vue_syntax_tokens(&block, &mut |token: &SyntaxToken| flow.visit(token));
    flow.top_level_return.complete() || flow.terminal_switch.complete()
}

#[derive(Default)]
pub(crate) struct VueControlFlowCompleteness {
    top_level_return: VueTopLevelReturnFlow,
    terminal_switch: VueTerminalSwitchFlow,
    depth: i32,
}

impl VueControlFlowCompleteness {
    fn visit(&mut self, token: &SyntaxToken) {
        use jssyntax::*;
        let kind = token.kind();
        if matches!(
            kind,
            TK_WHITESPACE | TK_LINE_BREAK | TK_LINE_COMMENT | TK_BLOCK_COMMENT
        ) {
            return;
        }
        let mut depth = self.depth;
        if kind == TK_OPEN_BRACE {
            depth += 1;
            self.depth = depth;
        }
        let text = if kind == TK_KEYWORD { token.text() } else { "" };
        self.top_level_return.observe(kind, text, depth);
        self.terminal_switch.observe(kind, text, depth);
        if kind == TK_CLOSE_BRACE {
            self.depth -= 1;
        }
    }
}

#[derive(Default)]
struct VueTopLevelReturnFlow {
    found: bool,
    semicolon: bool,
    invalid: bool,
}

impl VueTopLevelReturnFlow {
    fn observe(&mut self, kind: Kind, text: &str, depth: i32) {
        use jssyntax::*;
        if kind == TK_KEYWORD && text == "return" && depth == 1 {
            self.found = true;
            self.semicolon = false;
            self.invalid = false;
            return;
        }
        if !self.found {
            return;
        }
        if kind == TK_SEMICOLON {
            if depth == 1 {
                self.semicolon = true;
            }
            return;
        }
        if depth > 1 || kind == TK_CLOSE_BRACE && depth == 1 {
            return;
        }
        if self.semicolon {
            self.invalid = true;
        }
    }

    fn complete(&self) -> bool {
        self.found && !self.invalid
    }
}

#[derive(Default, Clone, Copy, PartialEq, Eq)]
enum VueSwitchPhase {
    #[default]
    Absent,
    SeekingBody,
    InBody,
    Closed,
}

#[derive(Default)]
struct VueTerminalSwitchFlow {
    phase: VueSwitchPhase,
    has_entry: bool,
    has_default: bool,
    pending: bool,
    invalid: bool,
}

impl VueTerminalSwitchFlow {
    fn observe(&mut self, kind: Kind, text: &str, depth: i32) {
        use jssyntax::*;
        if kind == TK_KEYWORD && text == "switch" && depth == 1 {
            *self = VueTerminalSwitchFlow {
                phase: VueSwitchPhase::SeekingBody,
                ..Default::default()
            };
            return;
        }
        match self.phase {
            VueSwitchPhase::SeekingBody => {
                if kind == TK_OPEN_BRACE && depth == 2 {
                    self.phase = VueSwitchPhase::InBody;
                }
            }
            VueSwitchPhase::InBody => {
                if kind == TK_CLOSE_BRACE && depth == 2 {
                    self.invalid = self.invalid || self.pending;
                    self.phase = VueSwitchPhase::Closed;
                    return;
                }
                if kind != TK_KEYWORD || depth != 2 {
                    return;
                }
                match text {
                    "case" => {
                        self.has_entry = true;
                        self.pending = true;
                    }
                    "default" => {
                        self.has_entry = true;
                        self.has_default = true;
                        self.pending = true;
                    }
                    "return" | "throw" => self.pending = false,
                    "break" if self.pending => {
                        self.invalid = true;
                        self.pending = false;
                    }
                    _ => {}
                }
            }
            VueSwitchPhase::Closed => {
                if kind != TK_SEMICOLON && (kind != TK_CLOSE_BRACE || depth != 1) {
                    self.invalid = true;
                }
            }
            VueSwitchPhase::Absent => {}
        }
    }

    fn complete(&self) -> bool {
        self.phase == VueSwitchPhase::Closed && self.has_entry && self.has_default && !self.invalid
    }
}

pub(crate) fn direct_javascript_block(method: &SyntaxNode) -> Option<SyntaxNode> {
    method
        .children()
        .find(|child| child.kind() == jssyntax::JS_BLOCK)
}

/// Visits every token below `node` in source order.
pub(crate) fn visit_vue_syntax_tokens(node: &SyntaxNode, visit: &mut dyn FnMut(&SyntaxToken)) {
    for element in node.descendants_with_tokens() {
        if let rowan::NodeOrToken::Token(token) = element {
            visit(&token);
        }
    }
}

pub(crate) fn vue_return_statement_expression(statement: &SyntaxNode) -> String {
    if statement.kind() != jssyntax::JS_RETURN_STATEMENT {
        return String::new();
    }
    let statement_range = statement.text_range();
    for token in statement
        .children_with_tokens()
        .filter_map(rowan::NodeOrToken::into_token)
    {
        if token.kind() != jssyntax::TK_KEYWORD || token.text() != "return" {
            continue;
        }
        let start = token.text_range().end();
        if start < statement_range.start() || start > statement_range.end() {
            return String::new();
        }
        let relative = usize::from(start - statement_range.start());
        let text = statement.text().to_string();
        return match text.get(relative..) {
            Some(rest) => trim_vue_source_expression(rest),
            None => String::new(),
        };
    }
    String::new()
}

/// Unwraps property-valued functions and the computed `{ get(), set() }`
/// form.
pub(crate) fn vue_method_implementation(method: &SyntaxNode) -> SyntaxNode {
    use jssyntax::*;
    if method.kind() != JS_PROPERTY {
        return method.clone();
    }
    let Some(value) = jsquery::property_value(method) else {
        return method.clone();
    };
    if value.kind() == JS_OBJECT {
        return jsquery::property(&value, "get").unwrap_or_else(|| method.clone());
    }
    match value.kind() {
        JS_ARROW_FUNCTION | JS_FUNCTION => value,
        _ => method.clone(),
    }
}

pub(crate) fn trim_vue_source_expression(value: &str) -> String {
    let value = value.trim();
    let value = value.strip_suffix(',').unwrap_or(value);
    let value = value.strip_suffix(';').unwrap_or(value);
    value.trim().to_owned()
}

pub(crate) fn vue_method_signature<'k>(
    method: &SyntaxNode,
    known: impl Into<Option<&'k HashMap<String, String>>>,
) -> String {
    let known: Option<&HashMap<String, String>> = known.into();
    let Some((parameters, mut return_type)) = vue_method_header(&method.text().to_string()) else {
        return String::new();
    };
    if return_type.is_empty() {
        return_type = vue_method_return_type(method, known);
    }
    if return_type.is_empty() {
        return_type = "unknown".to_owned();
    }
    format!("({parameters}) => {return_type}")
}

/// Splits `(...)` followed by `=>` or `:` into the parenthesized header end
/// and the return type start.
fn vue_callable_parts(value: &str) -> Option<(&str, usize, usize)> {
    let value = strip_outer_parentheses(value.trim());
    if value.is_empty() || value.as_bytes()[0] != b'(' {
        return None;
    }
    let close = matching_slot_delimiter(value, 0, b'(', b')')?;
    let bytes = value.as_bytes();
    let mut cursor = close + 1;
    while cursor < bytes.len() && is_javascript_space(bytes[cursor]) {
        cursor += 1;
    }
    if cursor + 1 < bytes.len() && &bytes[cursor..cursor + 2] == b"=>" {
        cursor += 2;
    } else if cursor < bytes.len() && bytes[cursor] == b':' {
        cursor += 1;
    } else {
        return None;
    }
    Some((value, close, cursor))
}

/// The result of a statically declared function or method signature.
pub fn vue_callable_return_type(value: &str) -> String {
    match vue_callable_parts(value) {
        Some((value, _, cursor)) => value[cursor..].trim().to_owned(),
        None => String::new(),
    }
}

/// Decomposes `(a: A) => R` and `(a: A): R` spellings.
pub fn vue_callable_signature(value: &str) -> Option<(Vec<String>, String)> {
    let (value, close, cursor) = vue_callable_parts(value)?;
    let mut parameters = Vec::new();
    for parameter in split_slot_top_level(&value[1..close], b',') {
        let parameter = parameter.trim();
        if !parameter.is_empty() {
            parameters.push(parameter.to_owned());
        }
    }
    Some((parameters, value[cursor..].trim().to_owned()))
}

/// Normalizes runtime and TypeScript prop type spellings into the value
/// type accepted by a template binding.
pub fn vue_prop_value_type(value: &str) -> String {
    let mut value = value.trim().to_owned();
    if value.is_empty() {
        return String::new();
    }
    let mut runtime_type = value.as_str();
    if let Some(position) = runtime_type.find(" as ") {
        runtime_type = runtime_type[..position].trim();
    }
    let runtime_type = runtime_type.to_owned();
    let asserted = vue_type_assertion(&value);
    if !asserted.is_empty() {
        let return_type = vue_callable_return_type(&asserted);
        if !return_type.is_empty() && (runtime_type == "Object" || runtime_type == "Array") {
            value = return_type;
        } else {
            value = asserted;
        }
    }
    while let Some(inner) = admin_type_generic_inner(&value, "PropType") {
        value = inner.trim().to_owned();
    }
    if value.len() >= 2
        && value.as_bytes()[0] == b'['
        && matching_slot_delimiter(&value, 0, b'[', b']') == Some(value.len() - 1)
    {
        let mut result = String::new();
        for entry in split_slot_top_level(&value[1..value.len() - 1], b',') {
            result = merge_vue_types(&[&result, &vue_prop_value_type(entry)]);
        }
        return result;
    }
    match value.as_str() {
        "String" => "string".to_owned(),
        "Number" => "number".to_owned(),
        "Boolean" => "boolean".to_owned(),
        "Array" => "Array".to_owned(),
        "Object" => "object".to_owned(),
        "Function" => "Function".to_owned(),
        _ => value,
    }
}

/// Statically known string values of a prop; the flag is true only for a
/// closed domain.
pub fn vue_prop_allowed_values(prop: &VueComponentProp) -> (Vec<String>, bool) {
    let mut values = Vec::with_capacity(prop.allowed_values.len());
    for value in &prop.allowed_values {
        append_allowed_value(&mut values, value);
    }
    let (type_values, type_complete) =
        vue_string_literal_union_values(&vue_prop_value_type(&prop.type_));
    for value in &type_values {
        append_allowed_value(&mut values, value);
    }
    let complete = !values.is_empty() && (prop.allowed_values_complete || type_complete);
    (values, complete)
}

/// A direct JavaScript string literal and the byte range of its content.
pub fn vue_static_string_literal(expression: &str) -> Option<(String, u32, u32)> {
    let trimmed_left = expression.trim_start();
    let leading = expression.len() - trimmed_left.len();
    let trimmed = trimmed_left.trim_end();
    let value = decode_admin_type_string_literal(trimmed)?;
    Some((
        value,
        (leading + 1) as u32,
        (leading + trimmed.len() - 1) as u32,
    ))
}

pub(crate) fn vue_string_literal_union_values(value: &str) -> (Vec<String>, bool) {
    let value = value.trim();
    if value.is_empty() {
        return (Vec::new(), false);
    }
    let branches = split_admin_type_top_level(value, b'|');
    let mut values = Vec::with_capacity(branches.len());
    let mut complete = true;
    for branch in &branches {
        let branch = trim_admin_type_parentheses(branch.trim());
        if matches!(branch, "null" | "undefined" | "void" | "never") {
            continue;
        }
        let Some(literal) = decode_admin_type_string_literal(branch) else {
            complete = false;
            continue;
        };
        append_allowed_value(&mut values, &literal);
    }
    let complete = complete && !values.is_empty();
    (values, complete)
}

pub(crate) fn append_allowed_value(values: &mut Vec<String>, value: &str) {
    if values.iter().any(|existing| existing == value) {
        return;
    }
    values.push(value.to_owned());
}

pub(crate) fn decode_admin_type_string_literal(value: &str) -> Option<String> {
    let bytes = value.as_bytes();
    if bytes.len() < 2
        || !matches!(bytes[0], b'\'' | b'"' | b'`')
        || bytes[bytes.len() - 1] != bytes[0]
    {
        return None;
    }
    let inner = &bytes[1..bytes.len() - 1];
    if bytes[0] == b'`' && inner.windows(2).any(|window| window == b"${") {
        return None;
    }
    let mut result: Vec<u8> = Vec::with_capacity(inner.len());
    let mut index = 0;
    while index < inner.len() {
        let current = inner[index];
        if current != b'\\' {
            result.push(current);
            index += 1;
            continue;
        }
        if index + 1 >= inner.len() {
            return None;
        }
        index += 1;
        let escaped = inner[index];
        match escaped {
            b'n' => result.push(b'\n'),
            b'r' => result.push(b'\r'),
            b't' => result.push(b'\t'),
            b'b' => result.push(0x08),
            b'f' => result.push(0x0c),
            b'v' => result.push(0x0b),
            b'x' | b'u' => {
                let digits = if escaped == b'u' { 4 } else { 2 };
                if index + digits >= inner.len() {
                    return None;
                }
                let hex = &inner[index + 1..index + 1 + digits];
                if !hex.iter().all(u8::is_ascii_hexdigit) {
                    return None;
                }
                let parsed = u32::from_str_radix(std::str::from_utf8(hex).ok()?, 16).ok()?;
                let character = char::from_u32(parsed).unwrap_or('\u{fffd}');
                let mut buffer = [0u8; 4];
                result.extend_from_slice(character.encode_utf8(&mut buffer).as_bytes());
                index += digits;
            }
            other => result.push(other),
        }
        index += 1;
    }
    Some(String::from_utf8_lossy(&result).into_owned())
}

/// Reports only closed, category-level mismatches suitable for template
/// diagnostics.
pub fn vue_types_provably_incompatible(expected: &str, actual: &str) -> bool {
    let Some(expected_kinds) = vue_comparable_type_kinds(expected, true) else {
        return false;
    };
    let Some(actual_kinds) = vue_comparable_type_kinds(actual, false) else {
        return false;
    };
    if expected_kinds.is_empty() || actual_kinds.is_empty() {
        return false;
    }
    !actual_kinds
        .iter()
        .any(|kind| expected_kinds.contains(kind))
}

/// Whether a top-level union permits an omitted/undefined value.
pub fn vue_type_allows_undefined(value: &str) -> bool {
    split_admin_type_top_level(value, b'|')
        .iter()
        .any(|branch| {
            matches!(
                trim_admin_type_parentheses(branch.trim()),
                "undefined" | "void"
            )
        })
}

/// Whether a static template expression can be the write target of v-model.
pub fn vue_model_expression_assignable(expression: &str) -> bool {
    let expression = trim_vue_source_expression(expression);
    if matches!(expression.as_str(), "true" | "false" | "null" | "undefined") {
        return false;
    }
    if !vue_static_literal_type(&expression).is_empty() {
        return false;
    }
    let Some(path) = vue_static_template_expression(&expression) else {
        return false;
    };
    if path.is_empty() {
        return false;
    }
    let last = path.len() - 1;
    !path
        .iter()
        .enumerate()
        .any(|(index, segment)| segment.optional || segment.called && index == last)
}

/// The first payload parameter of an indexed component event signature.
pub fn vue_event_payload_type(event_type: &str) -> String {
    event_payload_type(event_type)
}

pub(crate) fn vue_comparable_type_kinds(
    value: &str,
    use_runtime_fallback: bool,
) -> Option<HashSet<String>> {
    let original = value.trim();
    if use_runtime_fallback && let Some(kind) = vue_prop_runtime_comparable_kind(original) {
        return Some(HashSet::from([kind.to_owned()]));
    }
    let value = vue_prop_value_type(original);
    let mut result = HashSet::new();
    for branch in split_admin_type_top_level(&value, b'|') {
        let mut kind = vue_comparable_type_kind(&branch);
        if kind.is_none() && use_runtime_fallback {
            kind = vue_prop_runtime_comparable_kind(original);
        }
        let kind = kind?;
        result.insert(kind.to_owned());
    }
    (!result.is_empty()).then_some(result)
}

pub(crate) fn vue_prop_runtime_comparable_kind(value: &str) -> Option<&'static str> {
    let mut value = value.trim();
    if let Some(position) = value.find(" as ") {
        value = value[..position].trim();
    }
    match value {
        "String" => Some("string"),
        "Number" => Some("number"),
        "Boolean" => Some("boolean"),
        "Array" => Some("array"),
        "Object" => Some("object"),
        "Function" => Some("function"),
        _ => None,
    }
}

/// Whether `value` spells a decimal number (`1`, `-2.5`, `1e3`, `Infinity`);
/// underscores and hexadecimal literals are not numbers here.
fn is_decimal_number(value: &str) -> bool {
    if value.is_empty() || value.contains('_') {
        return false;
    }
    let unsigned = value.strip_prefix(['+', '-']).unwrap_or(value);
    if unsigned.len() > 2 && (unsigned.starts_with("0x") || unsigned.starts_with("0X")) {
        return false;
    }
    value.parse::<f64>().is_ok()
}

pub(crate) fn vue_comparable_type_kind(value: &str) -> Option<&'static str> {
    let value = trim_admin_type_parentheses(value.trim());
    let value = value.strip_prefix("readonly ").unwrap_or(value);
    match value {
        "" | "any" | "unknown" | "never" | "null" | "undefined" | "void" => return None,
        "string" | "String" => return Some("string"),
        "number" | "Number" => return Some("number"),
        "boolean" | "Boolean" => return Some("boolean"),
        "object" | "Object" => return Some("object"),
        "Function" => return Some("function"),
        _ => {}
    }
    let bytes = value.as_bytes();
    if bytes.len() >= 2
        && matches!(bytes[0], b'\'' | b'"' | b'`')
        && bytes[bytes.len() - 1] == bytes[0]
    {
        return Some("string");
    }
    if is_decimal_number(value) {
        return Some("number");
    }
    if value.starts_with('(')
        && (!vue_callable_return_type(value).is_empty() || value.contains("=>"))
    {
        return Some("function");
    }
    if value.starts_with("Array<")
        || value.starts_with("ReadonlyArray<")
        || value.ends_with("[]")
        || bytes.len() >= 2
            && bytes[0] == b'['
            && matching_slot_delimiter(value, 0, b'[', b']') == Some(bytes.len() - 1)
    {
        return Some("array");
    }
    if value.starts_with("Record<") || value.starts_with('{') {
        return Some("object");
    }
    None
}

/// `(parameters, return_type)` of a method signature when a parenthesized
/// header exists.
pub(crate) fn vue_method_header(value: &str) -> Option<(String, String)> {
    let bytes = value.as_bytes();
    let open = value.find('(')?;
    let close = matching_slot_delimiter(value, open, b'(', b')')?;
    let parameters = value[open + 1..close].trim().to_owned();
    let mut cursor = close + 1;
    while cursor < bytes.len() && is_javascript_space(bytes[cursor]) {
        cursor += 1;
    }
    if cursor >= bytes.len() || bytes[cursor] != b':' {
        return Some((parameters, String::new()));
    }
    cursor += 1;
    let start = cursor;
    let (mut angle, mut square, mut round) = (0usize, 0usize, 0usize);
    while cursor < bytes.len() {
        if angle == 0
            && square == 0
            && round == 0
            && cursor + 1 < bytes.len()
            && &bytes[cursor..cursor + 2] == b"=>"
        {
            return Some((parameters, value[start..cursor].trim().to_owned()));
        }
        match bytes[cursor] {
            b'<' => angle += 1,
            b'>' => angle = angle.saturating_sub(1),
            b'[' => square += 1,
            b']' => square = square.saturating_sub(1),
            b'(' => round += 1,
            b')' => round = round.saturating_sub(1),
            b'{' if angle == 0 && square == 0 && round == 0 => {
                if let Some(close) = balanced_brace_end(value, cursor)
                    && close > cursor
                {
                    let mut after = close + 1;
                    while after < bytes.len() && is_javascript_space(bytes[after]) {
                        after += 1;
                    }
                    if after < bytes.len()
                        && (bytes[after] == b'{' || bytes[after..].starts_with(b"=>"))
                    {
                        return Some((parameters, value[start..close + 1].trim().to_owned()));
                    }
                }
                return Some((parameters, value[start..cursor].trim().to_owned()));
            }
            _ => {}
        }
        cursor += 1;
    }
    Some((parameters, value[start..].trim().to_owned()))
}

pub(crate) fn vue_expression_text_type<'k>(
    value: &str,
    known: impl Into<Option<&'k HashMap<String, String>>>,
) -> String {
    let known: Option<&HashMap<String, String>> = known.into();
    let value = value.trim();
    if value.is_empty() {
        return String::new();
    }
    if let Some((when_true, when_false)) = split_vue_conditional_expression(value) {
        let true_type = vue_expression_text_type(&when_true, known);
        let false_type = vue_expression_text_type(&when_false, known);
        if true_type.is_empty() || false_type.is_empty() {
            return String::new();
        }
        return merge_vue_types(&[&true_type, &false_type]);
    }
    if value == "null" {
        return "null".to_owned();
    }
    if value == "undefined" {
        return "undefined".to_owned();
    }
    if index_vue_top_level_operator(value, "=>").is_some() {
        return "Function".to_owned();
    }
    if value == "true"
        || value == "false"
        || value.starts_with('!') && !value.starts_with("!=")
        || vue_has_top_level_comparison(value)
    {
        return "boolean".to_owned();
    }
    for (constructor, result_type) in [
        ("Boolean", "boolean"),
        ("Number", "number"),
        ("String", "string"),
    ] {
        if value.starts_with(constructor) && value[constructor.len()..].starts_with('(') {
            return result_type.to_owned();
        }
    }
    if let Some(name) = value.strip_prefix("this.")
        && !name.contains(['.', '[', '(', ' ', '?', ':', '+', '-', '*', '/'])
        && let Some(member_type) = known.and_then(|known| known.get(name))
        && !member_type.is_empty()
    {
        return member_type.clone();
    }
    let first = value.as_bytes()[0];
    match first {
        b'\'' | b'"' | b'`' => return "string".to_owned(),
        b'[' => return "Array".to_owned(),
        b'{' => return "Object".to_owned(),
        _ => {}
    }
    if first.is_ascii_digit() || first == b'-' {
        return "number".to_owned();
    }
    String::new()
}

pub(crate) fn vue_has_top_level_comparison(value: &str) -> bool {
    let bytes = value.as_bytes();
    for operator in ["===", "!==", "==", "!=", ">=", "<=", ">", "<"] {
        let Some(position) = index_vue_top_level_operator(value, operator) else {
            continue;
        };
        if operator == ">" && position > 0 && bytes[position - 1] == b'=' {
            continue;
        }
        if operator == "<" && position + 1 < bytes.len() && bytes[position + 1] == b'=' {
            continue;
        }
        return true;
    }
    false
}

pub(crate) fn split_vue_conditional_expression(value: &str) -> Option<(String, String)> {
    let bytes = value.as_bytes();
    let mut state = SlotScanState::default();
    let mut question: Option<usize> = None;
    let mut nested = 0usize;
    for index in 0..bytes.len() {
        if state.top_level() {
            match bytes[index] {
                b'?' => {
                    let operator = index + 1 < bytes.len()
                        && (bytes[index + 1] == b'?' || bytes[index + 1] == b'.')
                        || index > 0 && bytes[index - 1] == b'?';
                    if !operator {
                        if question.is_none() {
                            question = Some(index);
                        } else {
                            nested += 1;
                        }
                    }
                }
                b':' => {
                    if let Some(question) = question {
                        if nested > 0 {
                            nested -= 1;
                        } else {
                            let when_true = value[question + 1..index].trim().to_owned();
                            let when_false = value[index + 1..].trim().to_owned();
                            if when_true.is_empty() || when_false.is_empty() {
                                return None;
                            }
                            return Some((when_true, when_false));
                        }
                    }
                }
                _ => {}
            }
        }
        state.consume(bytes[index]);
    }
    None
}

pub(crate) fn compact_vue_expression(value: &str) -> String {
    value.split_whitespace().collect()
}

/// Direct fields of an inline TypeScript object type, sorted by name.
pub fn vue_type_members(value: &str) -> Vec<TwigVueMember> {
    let Some(open) = value.find('{') else {
        return Vec::new();
    };
    let fields = parse_type_declaration_fields(value, open);
    let mut result: Vec<TwigVueMember> = fields
        .iter()
        .filter(|field| !field.name.is_empty())
        .map(|field| TwigVueMember {
            name: field.name.clone(),
            type_: field.value.trim().to_owned(),
            optional: field.optional,
            ..Default::default()
        })
        .collect();
    result.sort_by(|left, right| left.name.cmp(&right.name));
    result
}
