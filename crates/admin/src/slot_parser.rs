//! Template slot and block extraction.

use swlsp_parser_twig::ast::AstNode;

use crate::*;

/// Slots and blocks extracted from a template.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TemplateParseResult {
    pub slots: Vec<VueComponentSlot>,
    pub blocks: Vec<TwigBlock>,
}

/// Slot definitions of a Twig template file.
pub fn parse_slots_from_template(template_path: &str) -> std::io::Result<Vec<VueComponentSlot>> {
    Ok(parse_template_from_file(template_path)?.slots)
}

/// Slots and blocks of a Twig template file.
pub fn parse_template_from_file(template_path: &str) -> std::io::Result<TemplateParseResult> {
    let content = std::fs::read(template_path)?;
    let content = String::from_utf8_lossy(&content);
    let mut result = parse_template_content(&content);
    set_template_source_paths(&mut result, template_path);
    Ok(result)
}

pub(crate) fn set_template_source_paths(result: &mut TemplateParseResult, template_path: &str) {
    for slot in &mut result.slots {
        slot.file_path = template_path.to_owned();
        for member in &mut slot.members {
            member.file_path = template_path.to_owned();
        }
    }
    for block in &mut result.blocks {
        block.file_path = template_path.to_owned();
        for member in &mut block.scope_members {
            member.file_path = template_path.to_owned();
        }
    }
}

/// An opening block declaration name; closing `{% endblock name %}` tokens
/// do not resolve.
pub fn twig_block_name_at(node: &SyntaxNode, token: &SyntaxToken) -> Option<String> {
    let block = twigast::TwigBlock::cast(twigquery::block_at(node)?)?;
    let name = block.name()?;
    if name.text_range() != token.text_range() || name.text().is_empty() {
        return None;
    }
    Some(name.text().to_owned())
}

/// An implicit lexical input of the enclosing extensibility block.
pub fn twig_block_scope_member_at(
    component: &VueComponent,
    node: &SyntaxNode,
    name: &str,
) -> Option<(TwigBlockScopeMember, TwigBlock)> {
    if name.is_empty() {
        return None;
    }
    let block_name = twigquery::block_at(node)
        .and_then(|block| twigquery::block_name(&block))
        .unwrap_or_default();
    let block = component.component_block(&block_name)?;
    let member = block.scope_member(name)?;
    Some((member.clone(), block.clone()))
}

/// A cursor within or immediately after an opening block name.
pub fn is_twig_block_name_completion_at(node: &SyntaxNode, offset: u32) -> bool {
    let Some(block) = twigquery::block_at(node).and_then(twigast::TwigBlock::cast) else {
        return false;
    };
    let Some(name) = block.name() else {
        return false;
    };
    let range = name.text_range();
    offset >= u32::from(range.start()) && offset <= u32::from(range.end())
}

/// Slots and blocks of template content.
pub(crate) fn parse_template_content(content: &str) -> TemplateParseResult {
    let parsed = swlsp_parser_twig::parse(content);
    parse_template_tree(&parsed.root(), content, &LineIndex::new(content))
}

fn utf16_source_range(line_index: &LineIndex, start: u32, end: u32) -> (u32, AdminSourceRange) {
    let (line, character) = line_index.position_utf16(start);
    let (end_line, end_character) = line_index.position_utf16(end);
    (
        line,
        AdminSourceRange {
            start_line: line,
            start_character: character,
            end_line,
            end_character,
            declaration: true,
            identifier: true,
            ..Default::default()
        },
    )
}

/// The public template contract of an existing Twig CST. Vue SFCs use this
/// so embedded nodes keep their absolute file ranges.
pub(crate) fn parse_template_tree(
    root: &SyntaxNode,
    content: &str,
    line_index: &LineIndex,
) -> TemplateParseResult {
    let mut result = TemplateParseResult::default();
    let mut slot_positions: HashMap<String, usize> = HashMap::new();
    let mut seen_blocks: HashSet<String> = HashSet::new();
    for node in twigquery::iterate_nodes(root, &[twigsyntax::HTML_STARTING_TAG]) {
        let Some(tag) = twigast::HtmlStartingTag::cast(node) else {
            continue;
        };
        let Some(tag_name) = tag.name() else {
            continue;
        };
        if tag_name.text() != "slot" {
            continue;
        }
        let Some(mut slot) = slot_declaration_identity(&tag, line_index) else {
            continue;
        };
        let (line, _) = line_index.position(u32::from(tag_name.text_range().start()));
        slot.line = line + 1;
        (slot.members, slot.members_complete) = slot_declaration_members(&tag, line_index);
        let key = slot.identity_key();
        if let Some(&position) = slot_positions.get(&key) {
            let existing = &mut result.slots[position];
            existing.members = overlay_slot_members(&existing.members, &slot.members);
            existing.members_complete = existing.members_complete && slot.members_complete;
            continue;
        }
        slot_positions.insert(key, result.slots.len());
        result.slots.push(slot);
    }
    // Lexical scopes are scanned once per template, not once per block.
    let mut scopes: Option<(Vec<TwigVueBinding>, Vec<TwigScopedSlot>)> = None;
    for node in twigquery::iterate_nodes(root, &[twigsyntax::TWIG_BLOCK]) {
        let block_name = twigquery::block_name(&node).unwrap_or_default();
        if block_name.is_empty() || !seen_blocks.insert(block_name.clone()) {
            continue;
        }
        let Some(block) = twigast::TwigBlock::cast(node.clone()) else {
            continue;
        };
        let Some(name) = block.name() else {
            continue;
        };
        let name_range = name.text_range();
        let (line, name_range_value) = utf16_source_range(
            line_index,
            u32::from(name_range.start()),
            u32::from(name_range.end()),
        );
        result.blocks.push(TwigBlock {
            name: block_name,
            deprecated: block_deprecation(&node, content),
            line: line + 1,
            scope_members: {
                let (bindings, scoped_slots) = scopes.get_or_insert_with(|| {
                    (twig_vue_bindings(root, content), twig_scoped_slots(root))
                });
                twig_block_scope_members_in(
                    bindings,
                    scoped_slots,
                    u32::from(name_range.start()),
                    line_index,
                )
            },
            name_range: name_range_value,
            ..Default::default()
        });
    }
    result
}

/// Lexical inputs of the block at `offset` over template scopes scanned
/// once; every binding is filtered by offset.
pub(crate) fn twig_block_scope_members_in(
    bindings: &[TwigVueBinding],
    scoped_slots: &[TwigScopedSlot],
    offset: u32,
    line_index: &LineIndex,
) -> Vec<TwigBlockScopeMember> {
    let mut positions: HashMap<String, usize> = HashMap::new();
    let mut result: Vec<TwigBlockScopeMember> = Vec::new();
    let mut add = |name: &str,
                   member_type: &str,
                   range: TextRange,
                   result: &mut Vec<TwigBlockScopeMember>| {
        if name.is_empty() {
            return;
        }
        let mut member = TwigBlockScopeMember {
            name: name.to_owned(),
            type_: member_type.to_owned(),
            ..Default::default()
        };
        if !range.is_empty() {
            let (line, name_range) =
                utf16_source_range(line_index, u32::from(range.start()), u32::from(range.end()));
            member.line = line + 1;
            member.name_range = name_range;
        }
        if let Some(&position) = positions.get(name) {
            result[position] = member;
            return;
        }
        positions.insert(name.to_owned(), result.len());
        result.push(member);
    };
    for binding in twig_vue_bindings_at_offset_impl(bindings, offset) {
        add(
            &binding.name,
            &binding.type_,
            binding.declaration_range,
            &mut result,
        );
    }
    for scope in twig_scoped_slots_at_offset_impl(scoped_slots, offset) {
        for binding in &scope.bindings {
            add(&binding.local_name, "", binding.local_range, &mut result);
        }
    }
    result
}

fn attribute_name_of(attribute: &twigast::HtmlAttribute) -> String {
    twigquery::html_attribute_name(attribute.syntax()).unwrap_or_default()
}

/// The slot identity; `None` when the declaration must be skipped (runtime
/// forwarded names).
pub(crate) fn slot_declaration_identity(
    tag: &twigast::HtmlStartingTag,
    line_index: &LineIndex,
) -> Option<VueComponentSlot> {
    let name_token = tag.name()?;
    let mut result = VueComponentSlot {
        name: "default".to_owned(),
        name_range: source_range_at(
            line_index,
            u32::from(name_token.text_range().start()),
            u32::from(name_token.text_range().end()),
            false,
        ),
        ..Default::default()
    };
    for attribute in tag.attributes() {
        match attribute_name_of(&attribute).as_str() {
            ":name" | "v-bind:name" => {
                let (value, value_range) = static_html_attribute_value_range(&attribute)?;
                let (name, prefix, suffix) = slot_bound_name(&value)?;
                let mut start = u32::from(value_range.start());
                let mut end = u32::from(value_range.end());
                let bytes = value.as_bytes();
                if !name.is_empty()
                    && bytes.len() >= 2
                    && bytes[0] == bytes[bytes.len() - 1]
                    && matches!(bytes[0], b'\'' | b'"' | b'`')
                {
                    start += 1;
                    end -= 1;
                }
                result.name = name;
                result.name_prefix = prefix;
                result.name_suffix = suffix;
                result.name_range = source_range_at(line_index, start, end, false);
            }
            "name" => {
                let (value, value_range) = static_html_attribute_value_range(&attribute)?;
                if value.is_empty() {
                    return None;
                }
                result.name = value;
                result.name_prefix.clear();
                result.name_suffix.clear();
                result.name_range = source_range_at(
                    line_index,
                    u32::from(value_range.start()),
                    u32::from(value_range.end()),
                    false,
                );
            }
            _ => {}
        }
    }
    Some(result)
}

/// The statically known identity and payload contract of one live `<slot>`
/// tag; runtime-only forwarded names are excluded.
pub fn slot_declaration(
    tag: &twigast::HtmlStartingTag,
    line_index: &LineIndex,
) -> Option<VueComponentSlot> {
    let mut slot = slot_declaration_identity(tag, line_index)?;
    (slot.members, slot.members_complete) = slot_declaration_members(tag, line_index);
    Some(slot)
}

/// Accepts only identities whose complete static shape is known; a single
/// template interpolation becomes a prefix/suffix family. Returns
/// `(name, prefix, suffix)`.
pub(crate) fn slot_bound_name(value: &str) -> Option<(String, String, String)> {
    let value = value.trim();
    let bytes = value.as_bytes();
    if bytes.len() >= 2
        && ((bytes[0] == b'\'' && bytes[bytes.len() - 1] == b'\'')
            || (bytes[0] == b'"' && bytes[bytes.len() - 1] == b'"'))
    {
        let name = &value[1..value.len() - 1];
        return (!name.is_empty() && is_slot_name_literal(name))
            .then(|| (name.to_owned(), String::new(), String::new()));
    }
    if bytes.len() < 2 || bytes[0] != b'`' || bytes[bytes.len() - 1] != b'`' {
        return None;
    }
    let body = &value[1..value.len() - 1];
    let Some(interpolation) = body.find("${") else {
        return (!body.is_empty() && is_slot_name_literal(body))
            .then(|| (body.to_owned(), String::new(), String::new()));
    };
    let open = interpolation + 2;
    let close = matching_slot_delimiter(body, open - 1, b'{', b'}')?;
    if body[close + 1..].contains("${") {
        return None;
    }
    let prefix = &body[..interpolation];
    let suffix = &body[close + 1..];
    if (prefix.is_empty() && suffix.is_empty())
        || !is_slot_name_literal(prefix)
        || !is_slot_name_literal(suffix)
    {
        return None;
    }
    Some((String::new(), prefix.to_owned(), suffix.to_owned()))
}

pub(crate) fn is_slot_name_literal(value: &str) -> bool {
    value
        .as_bytes()
        .iter()
        .all(|current| current.is_ascii_alphanumeric() || b"_.:-".contains(current))
}

pub(crate) fn slot_declaration_members(
    tag: &twigast::HtmlStartingTag,
    line_index: &LineIndex,
) -> (Vec<VueComponentSlotMember>, bool) {
    let mut members = Vec::new();
    let mut complete = true;
    for attribute in tag.attributes() {
        let attribute_name = attribute_name_of(&attribute);
        if attribute_name == "v-bind" {
            match static_html_attribute_value_range(&attribute) {
                Some((value, value_range)) if attribute.name().is_some() => {
                    let (fields, object_complete) =
                        vue_object_binding_fields(&value, u32::from(value_range.start()));
                    complete = complete && object_complete;
                    for field in fields {
                        let start = u32::from(field.name_range.start());
                        let (line, _) = line_index.position(start);
                        append_slot_member(
                            &mut members,
                            VueComponentSlotMember {
                                name: field.name,
                                line: line + 1,
                                name_range: source_range_at(
                                    line_index,
                                    start,
                                    u32::from(field.name_range.end()),
                                    false,
                                ),
                                ..Default::default()
                            },
                        );
                    }
                }
                _ => complete = false,
            }
            continue;
        }
        if matches!(attribute_name.as_str(), "name" | ":name" | "v-bind:name") {
            continue;
        }
        let Some(name_token) = attribute.name() else {
            continue;
        };
        let Some(prop) = vue_prop_reference_for_attribute(&attribute_name, name_token.text_range())
        else {
            continue;
        };
        if prop.name.is_empty() {
            continue;
        }
        let (line, _) = line_index.position(u32::from(name_token.text_range().start()));
        append_slot_member(
            &mut members,
            VueComponentSlotMember {
                name: prop.name,
                line: line + 1,
                name_range: source_range_at(
                    line_index,
                    u32::from(prop.range.start()),
                    u32::from(prop.range.end()),
                    false,
                ),
                ..Default::default()
            },
        );
    }
    (members, complete)
}

/// The trimmed static value of an attribute and its exact byte range.
pub(crate) fn static_html_attribute_value_range(
    attribute: &twigast::HtmlAttribute,
) -> Option<(String, TextRange)> {
    let inner = attribute.value()?.inner()?;
    let raw = inner.syntax().text().to_string();
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return None;
    }
    let start = raw.find(trimmed)?;
    let range_start = u32::from(inner.syntax().text_range().start()) + start as u32;
    Some((
        trimmed.to_owned(),
        slot_text_range(range_start, range_start + trimmed.len() as u32),
    ))
}

/// Adds `member`, or fills missing metadata of an equally named member.
pub(crate) fn append_slot_member(
    members: &mut Vec<VueComponentSlotMember>,
    member: VueComponentSlotMember,
) {
    if member.name.is_empty() {
        return;
    }
    if let Some(existing) = members
        .iter_mut()
        .find(|existing| existing.name == member.name)
    {
        if existing.type_.is_empty() {
            existing.type_ = member.type_.clone();
        }
        if existing.file_path.is_empty() {
            existing.file_path = member.file_path.clone();
            existing.line = member.line;
        }
        if existing.name_range == AdminSourceRange::default()
            && existing.file_path == member.file_path
        {
            existing.name_range = member.name_range;
        }
        return;
    }
    members.push(member);
}

pub(crate) fn overlay_slot_members(
    base: &[VueComponentSlotMember],
    overlay: &[VueComponentSlotMember],
) -> Vec<VueComponentSlotMember> {
    let mut result = base.to_vec();
    for member in overlay {
        append_slot_member(&mut result, member.clone());
    }
    result
}

/// Slot names and lines of template content (tests).
#[allow(dead_code)]
pub(crate) fn parse_slots_from_content(content: &str) -> Vec<VueComponentSlot> {
    parse_template_content(content).slots
}

/// Resolves a template import path relative to the component definition.
pub fn resolve_template_path(definition_path: &str, template_import: &str) -> String {
    if template_import.is_empty() {
        return String::new();
    }
    filepath::join(&[&filepath::dir(definition_path), template_import])
}
