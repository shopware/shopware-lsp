//! Scoped-slot consumers in Twig templates (port of Go `slot_scope.go`).

use swlsp_parser_twig::ast::AstNode;

use crate::*;

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TwigScopedSlot {
    pub component_name: String,
    pub slot_name: String,
    pub bindings: Vec<TwigSlotBinding>,
    pub binding_range: TextRange,
    pub template_range: TextRange,
    /// The tag carrying `v-slot`.
    pub starting_tag_range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TwigSlotBinding {
    pub member_name: String,
    pub local_name: String,
    pub whole_object: bool,
    pub member_range: TextRange,
    pub local_range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct ResolvedTwigScopedSlot {
    pub component: VueComponent,
    pub slot: VueComponentSlot,
    pub scope: TwigScopedSlot,
    /// Each concrete candidate behind a closed dynamic component selector;
    /// `slot` is their conservative common payload contract.
    pub contracts: Vec<ResolvedTwigSlotContract>,
    pub contracts_complete: bool,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct ResolvedTwigSlotContract {
    pub component: VueComponent,
    pub slot: VueComponentSlot,
}

/// Go embeds `ResolvedTwigScopedSlot`.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct ResolvedTwigSlotBinding {
    pub scoped: ResolvedTwigScopedSlot,
    pub binding: TwigSlotBinding,
    pub member: VueComponentSlotMember,
    pub members: Vec<VueComponentSlotMember>,
    pub member_found: bool,
    pub identifier: String,
    pub range: TextRange,
}

/// Go embeds `ResolvedTwigScopedSlot`.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct ResolvedTwigSlotMember {
    pub scoped: ResolvedTwigScopedSlot,
    pub binding: TwigSlotBinding,
    pub member: VueComponentSlotMember,
    pub members: Vec<VueComponentSlotMember>,
    pub member_found: bool,
    pub access: TwigVueMemberAccess,
    pub receiver_found: bool,
    pub receiver_type: String,
    pub receiver_members: Vec<TwigVueMember>,
    pub members_complete: bool,
}

/// A byte range; tolerates `end < start` by collapsing to `start`.
pub(crate) fn slot_text_range(start: u32, end: u32) -> TextRange {
    TextRange::new(TextSize::from(start), TextSize::from(end.max(start)))
}

fn range_start(range: TextRange) -> u32 {
    u32::from(range.start())
}

fn range_end(range: TextRange) -> u32 {
    u32::from(range.end())
}

/// The component start tag whose slot contract owns a slot directive (the
/// tag itself, or the parent of a direct child `<template>`).
pub fn twig_slot_owner_starting_tag(slot_start_tag: &SyntaxNode) -> Option<SyntaxNode> {
    if twig_dynamic_component_selector(slot_start_tag).is_some() {
        return Some(slot_start_tag.clone());
    }
    if static_component_name_for_tag(slot_start_tag).is_some() {
        return Some(slot_start_tag.clone());
    }
    if twigquery::html_tag_name(slot_start_tag).as_deref() != Some("template") {
        return None;
    }
    let template_tag = twigquery::closest_node_of_kind(slot_start_tag, &[twigsyntax::HTML_TAG])?;
    let parent_tag =
        twigquery::closest_node_of_kind(&template_tag.parent()?, &[twigsyntax::HTML_TAG])?;
    let parent = twigast::HtmlTag::cast(parent_tag)?;
    Some(parent.starting_tag()?.syntax().clone())
}

impl AdminComponentIndexer {
    /// Every component contract that may own a slot directive. The flag is
    /// true only for a closed static or inferred dynamic owner.
    pub fn resolve_twig_slot_consumer_components(
        &self,
        template_path: &str,
        slot_start_tag: &SyntaxNode,
        owner_component: Option<&VueComponent>,
    ) -> IndexResult<(Vec<VueComponent>, bool)> {
        let Some(owner) = twig_slot_owner_starting_tag(slot_start_tag) else {
            return Ok((Vec::new(), false));
        };
        if let Some(selector) = twig_dynamic_component_selector(&owner) {
            let (_, components, complete) = self.resolve_dynamic_component_contracts_for_owner(
                template_path,
                &selector,
                owner_component,
                Some(&owner),
            )?;
            return Ok((components, complete));
        }
        let Some(name) = static_component_name_for_tag(&owner) else {
            return Ok((Vec::new(), false));
        };
        match self.get_component_for_template_tag(template_path, &name, owner_component)? {
            Some(component) => Ok((vec![component], true)),
            None => Ok((Vec::new(), false)),
        }
    }
}

impl ResolvedTwigScopedSlot {
    pub fn qualified_name(&self) -> String {
        let mut component_name = self.scope.component_name.clone();
        if component_name.is_empty() {
            let names = self.component_names();
            component_name = match names.len() {
                0 => "<dynamic>".to_owned(),
                1 => names[0].clone(),
                _ => format!("({})", names.join(" | ")),
            };
        }
        if self.scope.slot_name.is_empty() {
            return format!("{component_name}.<dynamic>");
        }
        format!("{component_name}.{}", self.scope.slot_name)
    }

    pub fn component_names(&self) -> Vec<String> {
        let mut seen = HashSet::new();
        let mut result = Vec::with_capacity(self.contracts.len());
        for contract in &self.contracts {
            if contract.component.name.is_empty() || !seen.insert(contract.component.name.clone()) {
                continue;
            }
            result.push(contract.component.name.clone());
        }
        if result.is_empty() && !self.component.name.is_empty() {
            result.push(self.component.name.clone());
        }
        result
    }
}

/// The tag carrying the slot directive of a scope returned by
/// [`twig_scoped_slot_at_offset`].
pub fn twig_scoped_slot_starting_tag(
    root: &SyntaxNode,
    scope: &TwigScopedSlot,
) -> Option<SyntaxNode> {
    if scope.starting_tag_range.is_empty() {
        return None;
    }
    if let Some(node) = root.node_at(scope.starting_tag_range.start())
        && let Some(start_tag) = twigquery::starting_html_tag_at(&node)
        && start_tag.range_trimmed_trivia() == scope.starting_tag_range
    {
        return Some(start_tag);
    }
    twigquery::iterate_nodes(root, &[twigsyntax::HTML_STARTING_TAG])
        .find(|start_tag| start_tag.range_trimmed_trivia() == scope.starting_tag_range)
}

impl TwigScopedSlot {
    pub fn is_binding_offset(&self, offset: u32) -> bool {
        offset >= range_start(self.binding_range) && offset <= range_end(self.binding_range)
    }
}

/// Direct member usages resolving to the same whole-object scoped-slot
/// binding.
pub fn twig_scoped_slot_member_references(
    root: &SyntaxNode,
    content: &str,
    target: &ResolvedTwigSlotMember,
) -> Vec<TextRange> {
    if target.access.member.is_empty() || target.binding.local_name.is_empty() {
        return Vec::new();
    }
    let mut result = Vec::new();
    for access in twig_vue_expression_member_accesses(root, content) {
        if access.root != target.binding.local_name || !access.same_path(&target.access) {
            continue;
        }
        let Some(scope) = twig_scoped_slot_at_offset(root, range_start(access.root_range)) else {
            continue;
        };
        if scope.template_range != target.scoped.scope.template_range
            || scope.binding_range != target.scoped.scope.binding_range
        {
            continue;
        }
        let owns_root = scope
            .bindings
            .iter()
            .any(|binding| binding.whole_object && binding.local_name == access.root);
        if !owns_root {
            continue;
        }
        if let Some(vue_binding) =
            twig_vue_binding_at_offset(root, content, range_start(access.root_range))
            && vue_binding.scope_range.len() <= scope.template_range.len()
        {
            continue;
        }
        result.push(access.member_range);
    }
    result
}

/// The declaration and every lexical root reference of one scoped-slot
/// local.
pub fn twig_scoped_slot_binding_references(
    root: &SyntaxNode,
    content: &str,
    target: &ResolvedTwigSlotBinding,
) -> Vec<TextRange> {
    if target.binding.local_name.is_empty() {
        return Vec::new();
    }
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    let mut add = |range: TextRange, result: &mut Vec<TextRange>| {
        if range.is_empty() || !seen.insert(range) {
            return;
        }
        result.push(range);
    };
    add(target.binding.local_range, &mut result);
    let template_range = target.scoped.scope.template_range;
    let binding_range = target.scoped.scope.binding_range;
    for identifier in twig_vue_expression_root_identifiers(root, content) {
        let start = range_start(identifier.range);
        let end = range_end(identifier.range);
        if identifier.name != target.binding.local_name
            || start < range_start(template_range)
            || end > range_end(template_range)
            || start >= range_start(binding_range) && end <= range_end(binding_range)
        {
            continue;
        }
        let Some(scope) = twig_scoped_slot_at_offset(root, start) else {
            continue;
        };
        if scope.template_range != template_range || scope.binding_range != binding_range {
            continue;
        }
        if let Some(vue_binding) = twig_vue_binding_at_offset(root, content, start)
            && vue_binding.scope_range.len() <= scope.template_range.len()
        {
            continue;
        }
        add(identifier.range, &mut result);
    }
    result
}

/// Whether `offset` is in a context evaluated by Vue: Twig interpolation or
/// a bound/directive attribute value.
pub fn is_twig_vue_expression_at(node: &SyntaxNode, offset: u32) -> bool {
    if twigquery::closest_node_of_kind(node, &[twigsyntax::TWIG_VAR]).is_some() {
        return true;
    }
    let Some(attribute_node) = twigquery::html_attribute_at(node) else {
        return false;
    };
    let Some(attribute) = twigast::HtmlAttribute::cast(attribute_node.clone()) else {
        return false;
    };
    let name = twigquery::html_attribute_name(&attribute_node).unwrap_or_default();
    if !name.starts_with(':') && !name.starts_with('@') && !name.starts_with("v-") {
        return false;
    }
    let Some(inner) = attribute.value().and_then(|value| value.inner()) else {
        return false;
    };
    let range = inner.syntax().range_trimmed_trivia();
    offset >= range_start(range) && offset <= range_end(range)
}

/// The innermost scoped-slot template containing `offset`.
pub fn twig_scoped_slot_at_offset(root: &SyntaxNode, offset: u32) -> Option<TwigScopedSlot> {
    twig_scoped_slots_at_offset(root, offset).pop()
}

/// Every visible scoped-slot binding from the outermost template to the
/// innermost.
pub fn twig_scoped_slots_at_offset(root: &SyntaxNode, offset: u32) -> Vec<TwigScopedSlot> {
    // A scope's template range is its tag's trimmed range, so only HTML tags
    // whose range touches `offset` can contribute: collect them in document
    // (preorder) order instead of scanning every tag in the document.
    let root_range = root.text_range();
    if offset < range_start(root_range) || offset > range_end(root_range) {
        return Vec::new();
    }
    let mut tags: Vec<(u32, std::cmp::Reverse<u32>, usize, SyntaxNode)> = Vec::new();
    for token in root.token_at_offset(offset.into()) {
        let mut chain: Vec<SyntaxNode> = token
            .parent_ancestors()
            .filter(|node| node.kind() == twigsyntax::HTML_TAG)
            .collect();
        chain.reverse();
        for (depth, node) in chain.into_iter().enumerate() {
            if tags.iter().any(|(_, _, _, existing)| *existing == node) {
                continue;
            }
            let range = node.text_range();
            tags.push((
                range_start(range),
                std::cmp::Reverse(range_end(range)),
                depth,
                node,
            ));
        }
    }
    tags.sort_by_key(|left| (left.0, left.1, left.2));
    let scopes = scoped_slots_for_tags(tags.into_iter().map(|(_, _, _, node)| node));
    scopes
        .into_iter()
        .filter(|scope| {
            offset >= range_start(scope.template_range) && offset <= range_end(scope.template_range)
        })
        .collect()
}

pub(crate) fn twig_scoped_slots_at_offset_impl(
    scopes: &[TwigScopedSlot],
    offset: u32,
) -> Vec<TwigScopedSlot> {
    scopes
        .iter()
        .filter(|scope| {
            offset >= range_start(scope.template_range) && offset <= range_end(scope.template_range)
        })
        .cloned()
        .collect()
}

/// Every lexical scoped-slot declaration in outermost-to-innermost range
/// order.
pub fn twig_scoped_slots(root: &SyntaxNode) -> Vec<TwigScopedSlot> {
    scoped_slots_for_tags(twigquery::iterate_nodes(root, &[twigsyntax::HTML_TAG]))
}

/// Scoped-slot declarations of `tags` (HTML tag nodes in document order),
/// deduplicated and sorted outermost first.
fn scoped_slots_for_tags(tags: impl IntoIterator<Item = SyntaxNode>) -> Vec<TwigScopedSlot> {
    let mut result: Vec<TwigScopedSlot> = Vec::new();
    let mut seen: HashSet<(TextRange, TextRange)> = HashSet::new();
    let mut add = |scope: TwigScopedSlot, result: &mut Vec<TwigScopedSlot>| {
        if seen.insert((scope.template_range, scope.binding_range)) {
            result.push(scope);
        }
    };
    for node in tags {
        let Some(tag) = twigast::HtmlTag::cast(node.clone()) else {
            continue;
        };
        let Some(tag_name) = tag.name() else {
            continue;
        };
        let template_range = node.range_trimmed_trivia();
        let Some(starting_tag) = tag.starting_tag() else {
            continue;
        };
        let slot_start_tag = starting_tag.syntax().clone();
        let mut owner_start_tag = Some(slot_start_tag.clone());
        let mut component_name = static_component_name_for_tag(&slot_start_tag).unwrap_or_default();
        if tag_name.text() == "template" {
            owner_start_tag = twig_slot_owner_starting_tag(&slot_start_tag);
            component_name = owner_start_tag
                .as_ref()
                .and_then(static_component_name_for_tag)
                .unwrap_or_default();
            if component_name.is_empty()
                && owner_start_tag
                    .as_ref()
                    .and_then(twig_dynamic_component_selector)
                    .is_none()
            {
                owner_start_tag = None;
            }
        } else if component_name.is_empty()
            && twig_dynamic_component_selector(&slot_start_tag).is_none()
        {
            // Vue built-ins such as router-view can declare v-slot directly.
            component_name = tag_name.text().to_owned();
        }
        for attribute in starting_tag.attributes() {
            // Slot directives start with `#` or `v-slot`; skip copying the
            // text of every other attribute.
            if !matches!(first_non_space_byte(attribute.syntax()), Some(b'#' | b'v')) {
                continue;
            }
            let attribute_name =
                twigquery::html_attribute_name(attribute.syntax()).unwrap_or_default();
            let slot_name = normalize_slot_name(&attribute_name);
            if slot_name.is_empty() && !is_dynamic_vue_slot_directive(&attribute_name) {
                continue;
            }
            let Some(inner) = attribute.value().and_then(|value| value.inner()) else {
                continue;
            };
            let inner = inner.syntax();
            let binding_range = inner.range_trimmed_trivia();
            let raw_bindings = inner.text().to_string();
            let mut scope = TwigScopedSlot {
                component_name: component_name.clone(),
                slot_name,
                bindings: parse_scoped_slot_bindings_at(
                    &raw_bindings,
                    range_start(inner.text_range()),
                ),
                binding_range,
                template_range,
                ..Default::default()
            };
            if owner_start_tag.is_some() {
                scope.starting_tag_range = slot_start_tag.range_trimmed_trivia();
            }
            if scope.component_name.is_empty() && scope.starting_tag_range.is_empty() {
                continue;
            }
            add(scope, &mut result);
        }
        if let Some(scope) = dynamic_scoped_slot_from_malformed_tag(
            &node,
            &component_name,
            slot_start_tag.range_trimmed_trivia(),
        ) {
            add(scope, &mut result);
        }
    }
    result.sort_by_key(|scope| std::cmp::Reverse(scope.template_range.len()));
    result
}

pub(crate) fn is_dynamic_vue_slot_directive(attribute_name: &str) -> bool {
    let name = attribute_name.trim();
    name.starts_with("#[") || name.starts_with("v-slot:[")
}

/// Dynamic argument syntax (`#[name]`) lands in a recoverable ERROR node;
/// a small lossless scan anchored to the tag recovers explicit locals.
pub(crate) fn dynamic_scoped_slot_from_malformed_tag(
    node: &SyntaxNode,
    component_name: &str,
    starting_tag_range: TextRange,
) -> Option<TwigScopedSlot> {
    if component_name.is_empty() && starting_tag_range.is_empty() {
        return None;
    }
    // Only the opening tag is inspected; collect just that prefix instead of
    // the whole subtree text (quadratic over nested tags).
    let opening = slot_opening_tag_prefix(node)?;
    let opening = opening.as_bytes();
    let mut directive = find_bytes(opening, b"#[");
    let mut prefix_length = 2;
    if let Some(long) = find_bytes(opening, b"v-slot:[")
        && directive.is_none_or(|directive| long < directive)
    {
        directive = Some(long);
        prefix_length = "v-slot:[".len();
    }
    let directive = directive?;
    let argument_end = opening[directive + prefix_length..]
        .iter()
        .position(|byte| *byte == b']')?;
    let mut cursor = directive + prefix_length + argument_end + 1;
    while cursor < opening.len() && is_slot_space(opening[cursor]) {
        cursor += 1;
    }
    if cursor >= opening.len() || opening[cursor] != b'=' {
        return None;
    }
    cursor += 1;
    while cursor < opening.len() && is_slot_space(opening[cursor]) {
        cursor += 1;
    }
    if cursor >= opening.len() || (opening[cursor] != b'\'' && opening[cursor] != b'"') {
        return None;
    }
    let quote = opening[cursor];
    let value_start = cursor + 1;
    let mut value_end = value_start;
    let mut escaped = false;
    while value_end < opening.len() {
        let current = opening[value_end];
        if escaped {
            escaped = false;
            value_end += 1;
            continue;
        }
        if current == b'\\' {
            escaped = true;
            value_end += 1;
            continue;
        }
        if current == quote {
            break;
        }
        value_end += 1;
    }
    if value_end >= opening.len() {
        return None;
    }
    // The node text starts at the node range, which may include leading
    // trivia; Go uses the trimmed start as base (kept verbatim).
    let base = range_start(node.range_trimmed_trivia());
    let value = String::from_utf8_lossy(&opening[value_start..value_end]);
    Some(TwigScopedSlot {
        component_name: component_name.to_owned(),
        slot_name: String::new(),
        bindings: parse_scoped_slot_bindings_at(&value, base + value_start as u32),
        binding_range: slot_text_range(base + value_start as u32, base + value_end as u32),
        template_range: node.range_trimmed_trivia(),
        starting_tag_range,
    })
}

pub(crate) fn find_bytes(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    if needle.is_empty() {
        return Some(0);
    }
    haystack
        .windows(needle.len())
        .position(|window| window == needle)
}

/// The first byte of the node text that is not Unicode whitespace (as
/// `str::trim` sees it).
fn first_non_space_byte(node: &SyntaxNode) -> Option<u8> {
    node.descendants_with_tokens()
        .filter_map(|element| element.into_token())
        .find_map(|token| {
            let text = token.text().trim_start();
            text.as_bytes().first().copied()
        })
}

/// The node text up to and including the first `>` outside quotes (the end
/// of the opening tag), without materializing the rest of the subtree.
fn slot_opening_tag_prefix(node: &SyntaxNode) -> Option<String> {
    let mut result = String::new();
    let mut quote = 0u8;
    let mut escaped = false;
    for token in node
        .descendants_with_tokens()
        .filter_map(|element| element.into_token())
    {
        let text = token.text();
        for (index, &current) in text.as_bytes().iter().enumerate() {
            if quote != 0 {
                if escaped {
                    escaped = false;
                } else if current == b'\\' {
                    escaped = true;
                } else if current == quote {
                    quote = 0;
                }
                continue;
            }
            if current == b'\'' || current == b'"' {
                quote = current;
                continue;
            }
            if current == b'>' {
                result.push_str(&text[..=index]);
                return Some(result);
            }
        }
        result.push_str(text);
    }
    None
}

pub(crate) fn is_slot_space(value: u8) -> bool {
    matches!(value, b' ' | b'\t' | b'\r' | b'\n')
}

/// The JavaScript/Vue identifier touching a byte offset.
pub fn identifier_at_offset(content: &str, offset: u32) -> Option<(String, TextRange)> {
    let bytes = content.as_bytes();
    if bytes.is_empty() || offset as usize > bytes.len() {
        return None;
    }
    let mut position = offset as usize;
    if position == bytes.len() || !is_slot_identifier_continue(bytes[position]) {
        if position == 0 || !is_slot_identifier_continue(bytes[position - 1]) {
            return None;
        }
        position -= 1;
    }
    let mut start = position;
    while start > 0 && is_slot_identifier_continue(bytes[start - 1]) {
        start -= 1;
    }
    let mut end = position + 1;
    while end < bytes.len() && is_slot_identifier_continue(bytes[end]) {
        end += 1;
    }
    if start >= end || !is_slot_identifier_start(bytes[start]) {
        return None;
    }
    Some((
        content[start..end].to_owned(),
        slot_text_range(start as u32, end as u32),
    ))
}

/// [`identifier_at_offset`] constrained to a root expression: member
/// property names and explicit object-literal keys are excluded.
pub fn expression_root_identifier_at_offset(
    content: &str,
    offset: u32,
) -> Option<(String, TextRange)> {
    let (name, range) = identifier_at_offset(content, offset)?;
    let bytes = content.as_bytes();
    let mut previous = range_start(range) as usize;
    while previous > 0 && is_slot_space(bytes[previous - 1]) {
        previous -= 1;
    }
    if previous > 0 && bytes[previous - 1] == b'.' {
        return None;
    }
    let mut next = range_end(range) as usize;
    while next < bytes.len() && is_slot_space(bytes[next]) {
        next += 1;
    }
    if next < bytes.len() && bytes[next] == b':' {
        return None;
    }
    Some((name, range))
}

#[allow(dead_code)]
pub(crate) fn object_binding_names(value: &str) -> Vec<String> {
    vue_object_binding_names(value).0
}

/// Statically named keys of an object passed to `v-bind` or a scoped slot.
/// `complete` is false when additional runtime keys may be present.
pub fn vue_object_binding_names(value: &str) -> (Vec<String>, bool) {
    let value = value.trim();
    if !value.starts_with('{') {
        return (Vec::new(), false);
    }
    let Some(close) = matching_slot_delimiter(value, 0, b'{', b'}') else {
        return (Vec::new(), false);
    };
    if !value[close + 1..].trim().is_empty() {
        return (Vec::new(), false);
    }
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    let mut complete = true;
    for raw in split_slot_top_level(&value[1..close], b',') {
        let part = raw.trim();
        if part.is_empty() {
            continue;
        }
        if part.starts_with("...") {
            complete = false;
            continue;
        }
        let mut name = trim_top_level_default(part).trim();
        if let Some(colon) = index_slot_top_level(part, b':') {
            name = part[..colon].trim();
        }
        let name = name.trim_matches(|c| c == '\'' || c == '"');
        if !is_slot_property_name(name) {
            complete = false;
            continue;
        }
        if !seen.insert(name.to_owned()) {
            continue;
        }
        result.push(name.to_owned());
    }
    (result, complete)
}

#[allow(dead_code)]
pub(crate) fn parse_scoped_slot_bindings(value: &str) -> Vec<TwigSlotBinding> {
    let mut result = parse_scoped_slot_bindings_at(value, 0);
    for binding in &mut result {
        binding.member_range = TextRange::default();
        binding.local_range = TextRange::default();
    }
    result
}

fn trim_left_slot_space(value: &str) -> &str {
    value.trim_start_matches([' ', '\t', '\r', '\n'])
}

pub(crate) fn parse_scoped_slot_bindings_at(value: &str, base: u32) -> Vec<TwigSlotBinding> {
    let leading = value.len() - trim_left_slot_space(value).len();
    let value = value.trim();
    if value.is_empty() {
        return Vec::new();
    }
    if !value.starts_with('{') {
        let name = trim_top_level_default(value).trim();
        if is_slot_identifier(name) {
            let start = value.find(name).unwrap_or(0);
            return vec![TwigSlotBinding {
                local_name: name.to_owned(),
                whole_object: true,
                local_range: scoped_slot_source_range(base, Some(leading + start), name.len()),
                ..Default::default()
            }];
        }
        return Vec::new();
    }
    let close = match matching_slot_delimiter(value, 0, b'{', b'}') {
        None => value.len(),
        Some(close) => {
            if !value[close + 1..].trim().is_empty() {
                return Vec::new();
            }
            close
        }
    };
    let mut result = Vec::new();
    let body = &value[1..close];
    for source_part in split_scoped_slot_source_parts(body, b',') {
        let raw = source_part.text;
        let part_leading = raw.len() - trim_left_slot_space(raw).len();
        let part = raw.trim();
        if part.is_empty() {
            continue;
        }
        if let Some(rest) = part.strip_prefix("...") {
            let local = trim_top_level_default(rest).trim();
            if is_slot_identifier(local) {
                let local_start = part.find(local);
                result.push(TwigSlotBinding {
                    local_name: local.to_owned(),
                    whole_object: true,
                    local_range: scoped_slot_source_range(
                        base,
                        local_start
                            .map(|start| leading + 1 + source_part.start + part_leading + start),
                        local.len(),
                    ),
                    ..Default::default()
                });
            }
            continue;
        }
        let part = trim_top_level_default(part).trim();
        let colon = index_slot_top_level(part, b':');
        let mut member_name = part;
        let mut local_name = part;
        if let Some(colon) = colon {
            member_name = part[..colon].trim().trim_matches(|c| c == '\'' || c == '"');
            local_name = trim_top_level_default(&part[colon + 1..]).trim();
        }
        if !is_slot_property_name(member_name) || !is_slot_identifier(local_name) {
            continue;
        }
        let member_source = match colon {
            Some(colon) => &part[..colon],
            None => part,
        };
        let member_start = member_source.find(member_name);
        let local_search_start = colon.map_or(0, |colon| colon + 1);
        let local_start = part[local_search_start..]
            .find(local_name)
            .map(|start| start + local_search_start);
        let part_base = leading + 1 + source_part.start + part_leading;
        result.push(TwigSlotBinding {
            member_name: member_name.to_owned(),
            local_name: local_name.to_owned(),
            whole_object: false,
            member_range: scoped_slot_source_range(
                base,
                member_start.map(|start| part_base + start),
                member_name.len(),
            ),
            local_range: scoped_slot_source_range(
                base,
                local_start.map(|start| part_base + start),
                local_name.len(),
            ),
        });
    }
    result
}

#[derive(Clone, Copy, Debug)]
pub(crate) struct ScopedSlotSourcePart<'a> {
    pub text: &'a str,
    pub start: usize,
}

pub(crate) fn split_scoped_slot_source_parts(
    value: &str,
    separator: u8,
) -> Vec<ScopedSlotSourcePart<'_>> {
    let bytes = value.as_bytes();
    let mut result = Vec::new();
    let mut start = 0;
    let mut state = SlotScanState::default();
    for index in 0..bytes.len() {
        state.consume(bytes[index]);
        if bytes[index] == separator && state.top_level() {
            result.push(ScopedSlotSourcePart {
                text: &value[start..index],
                start,
            });
            start = index + 1;
        }
    }
    result.push(ScopedSlotSourcePart {
        text: &value[start..],
        start,
    });
    result
}

/// Go `scopedSlotSourceRange`; a missing (`-1`) start yields an empty range.
pub(crate) fn scoped_slot_source_range(
    base: u32,
    start: Option<usize>,
    length: usize,
) -> TextRange {
    match start {
        Some(start) if length > 0 => {
            slot_text_range(base + start as u32, base + (start + length) as u32)
        }
        _ => TextRange::default(),
    }
}

pub(crate) fn trim_top_level_default(value: &str) -> &str {
    match index_slot_top_level(value, b'=') {
        Some(index) => &value[..index],
        None => value,
    }
}

pub(crate) fn split_slot_top_level(value: &str, separator: u8) -> Vec<&str> {
    let bytes = value.as_bytes();
    let mut result = Vec::new();
    let mut start = 0;
    let mut state = SlotScanState::default();
    for index in 0..bytes.len() {
        state.consume(bytes[index]);
        if bytes[index] == separator && state.top_level() {
            result.push(&value[start..index]);
            start = index + 1;
        }
    }
    result.push(&value[start..]);
    result
}

pub(crate) fn index_slot_top_level(value: &str, needle: u8) -> Option<usize> {
    let mut state = SlotScanState::default();
    for (index, &current) in value.as_bytes().iter().enumerate() {
        if current == needle && state.top_level() {
            return Some(index);
        }
        state.consume(current);
    }
    None
}

#[derive(Clone, Copy, Debug, Default)]
pub(crate) struct SlotScanState {
    quote: u8,
    escaped: bool,
    braces: i32,
    brackets: i32,
    parens: i32,
}

impl SlotScanState {
    pub(crate) fn consume(&mut self, value: u8) {
        if self.quote != 0 {
            if self.escaped {
                self.escaped = false;
                return;
            }
            if value == b'\\' {
                self.escaped = true;
                return;
            }
            if value == self.quote {
                self.quote = 0;
            }
            return;
        }
        match value {
            b'\'' | b'"' | b'`' => self.quote = value,
            b'{' => self.braces += 1,
            b'}' => self.braces -= 1,
            b'[' => self.brackets += 1,
            b']' => self.brackets -= 1,
            b'(' => self.parens += 1,
            b')' => self.parens -= 1,
            _ => {}
        }
    }

    pub(crate) fn top_level(&self) -> bool {
        self.quote == 0 && self.braces == 0 && self.brackets == 0 && self.parens == 0
    }
}

/// The index of the delimiter closing the one at `start`, skipping quoted
/// text (Go `matchingSlotDelimiter`, `-1` -> `None`).
pub(crate) fn matching_slot_delimiter(
    value: &str,
    start: usize,
    open: u8,
    close: u8,
) -> Option<usize> {
    let bytes = value.as_bytes();
    let mut depth = 0i32;
    let mut state = SlotScanState::default();
    for (index, &current) in bytes.iter().enumerate().skip(start) {
        if state.quote != 0 {
            state.consume(current);
            continue;
        }
        if current == b'\'' || current == b'"' || current == b'`' {
            state.consume(current);
            continue;
        }
        if current == open {
            depth += 1;
        } else if current == close {
            depth -= 1;
            if depth == 0 {
                return Some(index);
            }
        }
    }
    None
}

pub(crate) fn is_slot_identifier(value: &str) -> bool {
    let bytes = value.as_bytes();
    if bytes.is_empty() || !is_slot_identifier_start(bytes[0]) {
        return false;
    }
    bytes[1..]
        .iter()
        .all(|byte| is_slot_identifier_continue(*byte))
}

pub(crate) fn is_slot_property_name(value: &str) -> bool {
    !value.is_empty()
        && value
            .as_bytes()
            .iter()
            .all(|byte| is_slot_identifier_continue(*byte) || *byte == b'-')
}

pub(crate) fn is_slot_identifier_start(value: u8) -> bool {
    value == b'_' || value == b'$' || value.is_ascii_alphabetic()
}

pub(crate) fn is_slot_identifier_continue(value: u8) -> bool {
    is_slot_identifier_start(value) || value.is_ascii_digit()
}
