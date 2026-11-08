//! Member chains and root identifiers in evaluated Vue template expressions
//! (port of Go `vue_scope_members.go`).

use swlsp_syntax::rowan::NodeOrToken;

use crate::*;

/// A statically inspectable property chain rooted in one identifier. Calls
/// and bracket access are retained as receiver operations.
pub fn twig_vue_expression_member_at_offset(
    root: &SyntaxNode,
    content: &str,
    offset: u32,
) -> Option<TwigVueMemberAccess> {
    let expression_range = twig_vue_expression_range_at(root, content, offset)?;
    let bytes = content.as_bytes();
    let expression_start = u32::from(expression_range.start()) as usize;
    let expression_end = u32::from(expression_range.end()) as usize;

    let identifier = identifier_at_offset(content, offset);
    let has_member = identifier.is_some();
    let (member, member_range) =
        identifier.unwrap_or_else(|| (String::new(), slot_text_range(offset, offset)));
    // `dot` mirrors Go's signed cursor: `None` is "before the expression".
    let mut dot: isize = if has_member {
        u32::from(member_range.start()) as isize - 1
    } else {
        offset as isize - 1
    };
    while dot >= expression_start as isize && is_slot_space(bytes[dot as usize]) {
        dot -= 1;
    }
    if dot < expression_start as isize || bytes[dot as usize] != b'.' {
        return None;
    }
    let dot = dot as usize;
    let mut receiver_end = dot;
    if receiver_end > expression_start && bytes[receiver_end - 1] == b'?' {
        receiver_end -= 1;
    }
    while receiver_end > expression_start && is_slot_space(bytes[receiver_end - 1]) {
        receiver_end -= 1;
    }
    let mut reversed: Vec<TwigVueMemberSegment> = Vec::new();
    let mut cursor = receiver_end;
    let mut root_start;
    loop {
        let (segment, segment_start) =
            twig_vue_chain_segment_before(bytes, expression_start, cursor)?;
        let indexed = segment.indexed;
        reversed.push(segment);
        root_start = segment_start;
        if indexed {
            if segment_start <= expression_start {
                return None;
            }
            cursor = segment_start;
            continue;
        }
        let mut previous = segment_start as isize - 1;
        while previous >= expression_start as isize && is_slot_space(bytes[previous as usize]) {
            previous -= 1;
        }
        if previous < expression_start as isize || bytes[previous as usize] != b'.' {
            break;
        }
        cursor = previous as usize;
        if cursor > expression_start && bytes[cursor - 1] == b'?' {
            cursor -= 1;
        }
        while cursor > expression_start && is_slot_space(bytes[cursor - 1]) {
            cursor -= 1;
        }
    }
    let mut previous = root_start as isize - 1;
    while previous >= expression_start as isize && is_slot_space(bytes[previous as usize]) {
        previous -= 1;
    }
    if previous >= expression_start as isize
        && matches!(bytes[previous as usize], b'.' | b']' | b')')
    {
        return None;
    }
    reversed.reverse();
    let mut segments = reversed;
    if segments.is_empty() || segments[0].indexed || segments[0].name.is_empty() {
        return None;
    }
    let root_range = segments[0].range;
    if vue_expression_position_in_literal_or_comment(
        &bytes[expression_start..u32::from(root_range.start()) as usize],
    ) {
        return None;
    }
    if has_member
        && vue_expression_position_in_literal_or_comment(
            &bytes[expression_start..u32::from(member_range.start()) as usize],
        )
    {
        return None;
    }
    let first = segments.remove(0);
    Some(TwigVueMemberAccess {
        root: first.name,
        root_called: first.called,
        member,
        member_called: twig_vue_member_called_after(
            bytes,
            u32::from(member_range.end()) as usize,
            expression_end,
        ),
        root_range,
        member_range,
        receiver: segments,
    })
}

pub(crate) fn twig_vue_chain_segment_before(
    content: &[u8],
    expression_start: usize,
    mut cursor: usize,
) -> Option<(TwigVueMemberSegment, usize)> {
    while cursor > expression_start && is_slot_space(content[cursor - 1]) {
        cursor -= 1;
    }
    if cursor > expression_start && content[cursor - 1] == b']' {
        let close = cursor - 1;
        let open = matching_vue_index_open(content, expression_start, close)?;
        if open < expression_start {
            return None;
        }
        let mut segment_start = open;
        let mut optional = false;
        if open >= expression_start + 2 && content[open - 1] == b'.' && content[open - 2] == b'?' {
            segment_start = open - 2;
            optional = true;
        }
        return Some((
            TwigVueMemberSegment {
                range: slot_text_range(segment_start as u32, cursor as u32),
                indexed: true,
                optional,
                index_expression: String::from_utf8_lossy(&content[open + 1..close])
                    .trim()
                    .to_owned(),
                index_range: slot_text_range(open as u32 + 1, close as u32),
                ..Default::default()
            },
            segment_start,
        ));
    }
    let mut called = false;
    if cursor > expression_start && content[cursor - 1] == b')' {
        let open = matching_vue_call_open(content, expression_start, cursor - 1)?;
        if open < expression_start {
            return None;
        }
        cursor = open;
        while cursor > expression_start && is_slot_space(content[cursor - 1]) {
            cursor -= 1;
        }
        if cursor >= expression_start + 2
            && content[cursor - 1] == b'.'
            && content[cursor - 2] == b'?'
        {
            cursor -= 2;
            while cursor > expression_start && is_slot_space(content[cursor - 1]) {
                cursor -= 1;
            }
        }
        called = true;
    }
    let segment_end = cursor;
    let mut segment_start = segment_end;
    while segment_start > expression_start
        && is_slot_identifier_continue(content[segment_start - 1])
    {
        segment_start -= 1;
    }
    if segment_start == segment_end || !is_slot_identifier_start(content[segment_start]) {
        return None;
    }
    Some((
        TwigVueMemberSegment {
            name: String::from_utf8_lossy(&content[segment_start..segment_end]).into_owned(),
            range: slot_text_range(segment_start as u32, segment_end as u32),
            called,
            ..Default::default()
        },
        segment_start,
    ))
}

/// The `[` opening the index access that `close` terminates.
pub(crate) fn matching_vue_index_open(content: &[u8], start: usize, close: usize) -> Option<usize> {
    matching_vue_open(content, start, close, b'[', b']')
}

/// The `(` opening the call that `close` terminates.
pub(crate) fn matching_vue_call_open(content: &[u8], start: usize, close: usize) -> Option<usize> {
    matching_vue_open(content, start, close, b'(', b')')
}

fn matching_vue_open(
    content: &[u8],
    start: usize,
    close: usize,
    open: u8,
    closing: u8,
) -> Option<usize> {
    if close <= start || close >= content.len() || content[close] != closing {
        return None;
    }
    let value = String::from_utf8_lossy(&content[start..=close]);
    let value_bytes = value.as_bytes();
    // Lossy decoding of valid UTF-8 keeps offsets; content is a `&str`.
    for candidate in 0..value_bytes.len() {
        if value_bytes[candidate] != open
            || vue_expression_position_in_literal_or_comment(&content[start..start + candidate])
        {
            continue;
        }
        if matching_slot_delimiter(&value, candidate, open, closing) == Some(value_bytes.len() - 1)
        {
            return Some(start + candidate);
        }
    }
    None
}

pub(crate) fn twig_vue_member_called_after(
    content: &[u8],
    mut cursor: usize,
    expression_end: usize,
) -> bool {
    if cursor > content.len() {
        return false;
    }
    let expression_end = expression_end.min(content.len());
    while cursor < expression_end && is_slot_space(content[cursor]) {
        cursor += 1;
    }
    if cursor + 1 < expression_end && content[cursor] == b'?' && content[cursor + 1] == b'.' {
        cursor += 2;
        while cursor < expression_end && is_slot_space(content[cursor]) {
            cursor += 1;
        }
    }
    cursor < expression_end && content[cursor] == b'('
}

/// All complete member accesses in evaluated Vue expressions in source
/// order.
pub fn twig_vue_expression_member_accesses(
    root: &SyntaxNode,
    content: &str,
) -> Vec<TwigVueMemberAccess> {
    let bytes = content.as_bytes();
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    for position in 0..bytes.len() {
        if bytes[position] != b'.' {
            continue;
        }
        let mut member = position + 1;
        while member < bytes.len() && is_slot_space(bytes[member]) {
            member += 1;
        }
        if member >= bytes.len() || !is_slot_identifier_start(bytes[member]) {
            continue;
        }
        let Some(access) = twig_vue_expression_member_at_offset(root, content, member as u32)
        else {
            continue;
        };
        if access.member.is_empty() || !seen.insert(access.member_range) {
            continue;
        }
        result.push(access);
    }
    result
}

/// Root identifiers of evaluated Vue expressions in source order.
pub fn twig_vue_expression_root_identifiers(
    root: &SyntaxNode,
    content: &str,
) -> Vec<TwigVueMember> {
    let bytes = content.as_bytes();
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    let mut position = 0;
    while position < bytes.len() {
        if !is_slot_identifier_start(bytes[position])
            || position > 0 && is_slot_identifier_continue(bytes[position - 1])
        {
            position += 1;
            continue;
        }
        let mut end = position + 1;
        while end < bytes.len() && is_slot_identifier_continue(bytes[end]) {
            end += 1;
        }
        if let Some((name, range)) =
            twig_vue_expression_root_identifier_at_offset(root, content, position as u32)
            && u32::from(range.start()) == position as u32
            && seen.insert(range)
        {
            result.push(TwigVueMember {
                name,
                range,
                ..Default::default()
            });
        }
        position = end;
    }
    result
}

fn resolves_to_target(bindings: &[TwigVueBinding], offset: u32, target: &TwigVueBinding) -> bool {
    twig_vue_bindings_at_offset_impl(bindings, offset)
        .iter()
        .any(|candidate| candidate.name == target.name && candidate.same_identity(target))
}

/// The direct properties used through `target` in its lexical scope, sorted
/// by name.
pub fn twig_vue_binding_members(
    root: &SyntaxNode,
    content: &str,
    target: &TwigVueBinding,
) -> Vec<TwigVueMember> {
    if target.name.is_empty() {
        return Vec::new();
    }
    let bindings = twig_vue_bindings_for_target(root, content, target);
    let bytes = content.as_bytes();
    let needle = target.name.as_bytes();
    let mut seen = HashSet::new();
    let mut result = Vec::new();
    let mut start = 0;
    while start < bytes.len() {
        let Some(relative) = find_bytes(&bytes[start..], needle) else {
            break;
        };
        let position = start + relative;
        start = position + needle.len();
        let Some(member_offset) = twig_vue_member_offset_after_root(bytes, position + needle.len())
        else {
            continue;
        };
        let Some(access) = twig_vue_expression_member_at_offset(root, content, member_offset)
        else {
            continue;
        };
        if access.root != target.name
            || access.member.is_empty()
            || u32::from(access.root_range.start()) != position as u32
            || !access.receiver.is_empty()
        {
            continue;
        }
        if !resolves_to_target(&bindings, u32::from(access.root_range.start()), target)
            || !seen.insert(access.member.clone())
        {
            continue;
        }
        result.push(TwigVueMember {
            name: access.member,
            range: access.member_range,
            ..Default::default()
        });
    }
    result.sort_by(|left, right| left.name.cmp(&right.name));
    result
}

/// Every use of one direct property on the same lexical binding.
pub fn twig_vue_binding_member_references(
    root: &SyntaxNode,
    content: &str,
    target: &TwigVueBinding,
    member_name: &str,
) -> Vec<TextRange> {
    twig_vue_binding_member_path_references(root, content, target, &[member_name.to_owned()])
}

/// Every use of the same safe property path on one lexical binding.
pub fn twig_vue_binding_member_path_references(
    root: &SyntaxNode,
    content: &str,
    target: &TwigVueBinding,
    member_path: &[String],
) -> Vec<TextRange> {
    if member_path.last().is_none_or(String::is_empty) {
        return Vec::new();
    }
    let bindings = twig_vue_bindings_for_target(root, content, target);
    let mut result = Vec::new();
    for access in twig_vue_expression_member_accesses(root, content) {
        if access.root != target.name
            || !equal_twig_vue_member_path(&access.member_path(), member_path)
        {
            continue;
        }
        if resolves_to_target(&bindings, u32::from(access.root_range.start()), target) {
            result.push(access.member_range);
        }
    }
    result.sort_by_key(|range| range.start());
    result
}

/// Like [`twig_vue_binding_member_path_references`] but retaining call
/// markers, so `value.method.name` and `value.method().name` differ.
pub fn twig_vue_binding_member_access_references(
    root: &SyntaxNode,
    content: &str,
    target: &TwigVueBinding,
    target_access: &TwigVueMemberAccess,
) -> Vec<TextRange> {
    if target_access.member.is_empty() {
        return Vec::new();
    }
    let bindings = twig_vue_bindings_for_target(root, content, target);
    let mut result = Vec::new();
    for access in twig_vue_expression_member_accesses(root, content) {
        if !access.same_path(target_access) {
            continue;
        }
        if resolves_to_target(&bindings, u32::from(access.root_range.start()), target) {
            result.push(access.member_range);
        }
    }
    result.sort_by_key(|range| range.start());
    result
}

pub(crate) fn equal_twig_vue_member_path(left: &[String], right: &[String]) -> bool {
    left == right
}

pub(crate) fn twig_vue_member_offset_after_root(
    content: &[u8],
    mut position: usize,
) -> Option<u32> {
    while position < content.len() && is_slot_space(content[position]) {
        position += 1;
    }
    if position < content.len() && content[position] == b'?' {
        position += 1;
    }
    if position >= content.len() || content[position] != b'.' {
        return None;
    }
    position += 1;
    while position < content.len() && is_slot_space(content[position]) {
        position += 1;
    }
    if position >= content.len() || !is_slot_identifier_start(content[position]) {
        return None;
    }
    Some(position as u32)
}

/// A root identifier inside an evaluated Vue expression and outside string
/// literals or comments.
pub fn twig_vue_expression_root_identifier_at_offset(
    root: &SyntaxNode,
    content: &str,
    offset: u32,
) -> Option<(String, TextRange)> {
    let expression_range = twig_vue_expression_range_at(root, content, offset)?;
    let (name, range) = expression_root_identifier_at_offset(content, offset)?;
    let expression_start = u32::from(expression_range.start());
    if u32::from(range.start()) < expression_start {
        return None;
    }
    let bytes = content.as_bytes();
    if vue_expression_position_in_literal_or_comment(
        &bytes[expression_start as usize..u32::from(range.start()) as usize],
    ) {
        return None;
    }
    if vue_expression_reserved_identifier(&name)
        || vue_arrow_parameter_is_local(
            &content[expression_start as usize..u32::from(expression_range.end()) as usize],
            &name,
            u32::from(range.start()) - expression_start,
        )
    {
        return None;
    }
    Some((name, range))
}

pub(crate) fn vue_expression_reserved_identifier(name: &str) -> bool {
    matches!(
        name,
        "await"
            | "break"
            | "case"
            | "catch"
            | "class"
            | "const"
            | "continue"
            | "debugger"
            | "default"
            | "delete"
            | "do"
            | "else"
            | "export"
            | "extends"
            | "false"
            | "finally"
            | "for"
            | "function"
            | "if"
            | "import"
            | "in"
            | "instanceof"
            | "let"
            | "new"
            | "null"
            | "of"
            | "return"
            | "super"
            | "switch"
            | "this"
            | "throw"
            | "true"
            | "try"
            | "typeof"
            | "var"
            | "void"
            | "while"
            | "with"
            | "yield"
    )
}

pub(crate) fn vue_arrow_parameter_is_local(expression: &str, name: &str, target: u32) -> bool {
    if name.is_empty() || !expression.contains("=>") {
        return false;
    }
    const PREFIX: &str = "const __shopwareLSPTemplateValue = ";
    let parsed = swlsp_parser_js::parse(&format!("{PREFIX}{expression};"));
    let root = parsed.root();
    let target = target + PREFIX.len() as u32;
    for arrow in jsquery::nodes(&root, &[jssyntax::JS_ARROW_FUNCTION]) {
        let arrow_range = arrow.range_trimmed_trivia();
        if target < u32::from(arrow_range.start()) || target > u32::from(arrow_range.end()) {
            continue;
        }
        let arrow_start = arrow
            .children_with_tokens()
            .filter_map(NodeOrToken::into_token)
            .find(|token| token.kind() == jssyntax::TK_ARROW)
            .map_or(0, |token| u32::from(token.text_range().start()));
        if arrow_start == 0 {
            continue;
        }
        for identifier in jsquery::nodes(&arrow, &[jssyntax::JS_IDENTIFIER]) {
            if u32::from(identifier.range_trimmed_trivia().end()) > arrow_start {
                continue;
            }
            if identifier.text().to_string().trim() == name {
                return true;
            }
        }
    }
    false
}
