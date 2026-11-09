//! Component usages, props and Live references in Twig templates.

use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

use regex::Regex;
use swlsp_parser_twig::ast::{AstNode, HtmlAttribute, HtmlStartingTag, HtmlString, HtmlTag};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::*;
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::config::quoted_value_range as twig_string_value_range;
use crate::model::*;
use swlsp_twig::DocumentMemo;

/// Whitespace is ASCII only, hence the explicit classes.
static PROP_DOCUMENTATION_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(
        r"(?m)@prop[\t\n\x0C\r ]+([A-Za-z_][A-Za-z0-9_]*)[\t\n\x0C\r ]+([^\t\n\x0C\r ]+)(?:[\t\n\x0C\r ]+([^\r\n#]+))?",
    )
    .expect("valid prop documentation pattern")
});

const TWIG_PREFIX: &str = "twig:";

fn range(start: u32, end: u32) -> TextRange {
    TextRange::new(TextSize::from(start), TextSize::from(end))
}

fn start(range: TextRange) -> u32 {
    range.start().into()
}

fn function_named(call: &SyntaxNode, name: &str) -> bool {
    twigquery::function_name(call).is_some_and(|function| eq_ignore_case(&function, name))
}

/// `twig:Name` → `Name`, for a name with the `twig:` prefix (any case).
fn strip_twig_prefix(text: &str) -> Option<&str> {
    let prefix = text.get(..TWIG_PREFIX.len())?;
    prefix
        .eq_ignore_ascii_case(TWIG_PREFIX)
        .then(|| &text[TWIG_PREFIX.len()..])
}

pub(crate) fn usages_in_twig_impl(path: &str, root: &SyntaxNode) -> Vec<Usage> {
    let mut result = Vec::new();
    for call in twigquery::iterate_nodes(root, &[TWIG_FUNCTION_CALL]) {
        if !function_named(&call, "component") {
            continue;
        }
        let Some(literal) =
            twigquery::string_argument(&call, 0).filter(twigquery::string_is_static)
        else {
            continue;
        };
        let name = normalize_component_name(&twigquery::string_value(&literal).unwrap_or_default());
        if name.is_empty() {
            continue;
        }
        result.push(Usage {
            name,
            file: path.to_owned(),
            range: twig_string_value_range(&literal),
            kind: UsageKind::Function,
        });
    }
    for block in twigquery::iterate_nodes(root, &[TWIG_COMPONENT_STARTING_BLOCK]) {
        let (name, range) = block_component_name(&block);
        if name.is_empty() {
            continue;
        }
        result.push(Usage {
            name,
            file: path.to_owned(),
            range,
            kind: UsageKind::Block,
        });
    }
    for node in twigquery::iterate_nodes(root, &[HTML_STARTING_TAG]) {
        let Some(tag) = HtmlStartingTag::cast(node) else {
            continue;
        };
        if !tag.is_twig_component() {
            continue;
        }
        let Some(name) = tag.name() else {
            continue;
        };
        let text = name.text();
        let Some(rest) = strip_twig_prefix(text).filter(|rest| !rest.is_empty()) else {
            continue;
        };
        let component = normalize_component_name(rest);
        if eq_ignore_case(&component, "block") {
            continue;
        }
        let token_range = name.text_range();
        result.push(Usage {
            name: component,
            file: path.to_owned(),
            range: TextRange::new(
                token_range.start() + TextSize::of(TWIG_PREFIX),
                token_range.end(),
            ),
            kind: UsageKind::Html,
        });
    }
    unique_usages(result)
}

pub(crate) fn live_action_references_in_twig_impl(
    path: &str,
    root: &SyntaxNode,
) -> Vec<LiveActionReference> {
    let mut result = Vec::new();
    for call in twigquery::iterate_nodes(root, &[TWIG_FUNCTION_CALL]) {
        if !function_named(&call, "live_action") {
            continue;
        }
        let Some(literal) =
            twigquery::string_argument(&call, 0).filter(twigquery::string_is_static)
        else {
            continue;
        };
        let context_range = twig_string_value_range(&literal);
        result.push(LiveActionReference {
            name: twigquery::string_value(&literal)
                .unwrap_or_default()
                .trim()
                .to_owned(),
            file: path.to_owned(),
            range: context_range,
            context_range,
            kind: LiveActionReferenceKind::Helper,
        });
    }
    for node in twigquery::iterate_nodes(root, &[HTML_ATTRIBUTE]) {
        if let Some(reference) = live_action_reference_from_html_attribute(path, &node) {
            result.push(reference);
        }
    }
    unique_live_action_references(result)
}

fn attribute_name(node: &SyntaxNode) -> String {
    twigquery::html_attribute_name(node).unwrap_or_default()
}

fn live_action_reference_from_html_attribute(
    path: &str,
    node: &SyntaxNode,
) -> Option<LiveActionReference> {
    if !eq_ignore_case(&attribute_name(node), "data-live-action-param") {
        return None;
    }
    let value = HtmlAttribute::cast(node.clone())?.value()?;
    let (context_range, text) = html_string_static_value(&value)?;
    let (name, range) = live_action_directive_name(&text, context_range);
    Some(LiveActionReference {
        name,
        file: path.to_owned(),
        range,
        context_range,
        kind: LiveActionReferenceKind::Attribute,
    })
}

pub fn live_action_references_in_twig(path: &str, root: &SyntaxNode) -> Vec<LiveActionReference> {
    live_action_references_in_twig_impl(path, root)
}

pub fn live_action_reference_at(
    path: &str,
    root: &SyntaxNode,
    offset: u32,
) -> Option<LiveActionReference> {
    static MEMO: DocumentMemo<Vec<LiveActionReference>> = DocumentMemo::new(4);
    MEMO.get_or_compute(root, path, || {
        live_action_references_in_twig_impl(path, root)
    })
    .iter()
    .find(|reference| {
        range_contains_cursor(reference.range, offset)
            || (reference.range.is_empty() && offset == start(reference.range))
    })
    .cloned()
}

pub fn live_action_argument_references_in_twig(
    path: &str,
    root: &SyntaxNode,
) -> Vec<LiveActionArgumentReference> {
    let mut result = Vec::new();
    for node in twigquery::iterate_nodes(root, &[HTML_STARTING_TAG]) {
        let Some(tag) = HtmlStartingTag::cast(node) else {
            continue;
        };
        let mut action = String::new();
        let mut arguments = Vec::new();
        for attribute in tag.attributes() {
            let attribute = attribute.syntax();
            if let Some(reference) = live_action_reference_from_html_attribute(path, attribute) {
                action = reference.name;
                continue;
            }
            if let Some((name, range)) = live_action_argument_attribute(attribute) {
                arguments.push(LiveActionArgumentReference {
                    action: String::new(),
                    name,
                    file: path.to_owned(),
                    range,
                });
            }
        }
        if action.is_empty() {
            continue;
        }
        for mut argument in arguments {
            argument.action = action.clone();
            result.push(argument);
        }
    }
    for call in twigquery::iterate_nodes(root, &[TWIG_FUNCTION_CALL]) {
        if !function_named(&call, "live_action") {
            continue;
        }
        let action = twigquery::string_argument(&call, 0)
            .and_then(|literal| twigquery::string_value(&literal))
            .unwrap_or_default()
            .trim()
            .to_owned();
        if action.is_empty() {
            continue;
        }
        for key in twigquery::iterate_nodes(&call, &[TWIG_LITERAL_HASH_KEY]) {
            let Some(hash) = twigquery::hash_at(&key) else {
                continue;
            };
            if twigquery::function_call_at(&hash).as_ref() != Some(&call)
                || twigquery::function_argument_index(&hash) != Some(1)
            {
                continue;
            }
            let (name, range) = twig_hash_key_name_range(&key);
            if name.is_empty() {
                continue;
            }
            result.push(LiveActionArgumentReference {
                action: action.clone(),
                name,
                file: path.to_owned(),
                range,
            });
        }
    }
    unique_live_action_argument_references(result)
}

pub fn live_action_argument_reference_at(
    path: &str,
    root: &SyntaxNode,
    offset: u32,
) -> Option<LiveActionArgumentReference> {
    static MEMO: DocumentMemo<Vec<LiveActionArgumentReference>> = DocumentMemo::new(4);
    MEMO.get_or_compute(root, path, || {
        live_action_argument_references_in_twig(path, root)
    })
    .iter()
    .find(|reference| range_contains_cursor(reference.range, offset))
    .cloned()
}

/// The action name and present argument keys when `node` is inside the
/// argument hash of `live_action()`.
pub fn live_action_argument_context(node: &SyntaxNode) -> Option<(String, Vec<String>)> {
    let hash = twigquery::hash_at(node)?;
    let call = twigquery::function_call_at(&hash)?;
    if !function_named(&call, "live_action") || twigquery::function_argument_index(&hash) != Some(1)
    {
        return None;
    }
    if twigquery::closest_node_of_kind(node, &[TWIG_LITERAL_HASH_PAIR]).is_some()
        && twigquery::hash_key_at(node).is_none()
    {
        return None;
    }
    let action = twigquery::string_argument(&call, 0)
        .and_then(|literal| twigquery::string_value(&literal))
        .unwrap_or_default()
        .trim()
        .to_owned();
    if action.is_empty() {
        return None;
    }
    let present = twigquery::iterate_nodes(&hash, &[TWIG_LITERAL_HASH_KEY])
        .map(|key| twig_hash_key_name_range(&key).0)
        .filter(|name| !name.is_empty())
        .collect();
    Some((action, present))
}

pub(crate) fn live_event_references_in_twig_impl(
    path: &str,
    root: &SyntaxNode,
) -> (Vec<LiveEventReference>, Vec<LiveEventArgumentReference>) {
    let mut references = Vec::new();
    let mut arguments = Vec::new();
    for node in twigquery::iterate_nodes(root, &[HTML_STARTING_TAG]) {
        let Some(tag) = HtmlStartingTag::cast(node) else {
            continue;
        };
        let mut action_kind = None;
        let mut event_name = String::new();
        let mut event_range = TextRange::default();
        let mut context_range = TextRange::default();
        let mut current_arguments = Vec::new();
        for attribute in tag.attributes() {
            let node = attribute.syntax();
            match lowercase(&attribute_name(node)).as_str() {
                "data-action" => {
                    let Some(value) = attribute.value() else {
                        continue;
                    };
                    let Some((_, text)) = html_string_static_value(&value) else {
                        continue;
                    };
                    action_kind = live_event_attribute_action_kind(&text);
                }
                "data-live-event-param" => {
                    let Some(value) = attribute.value() else {
                        continue;
                    };
                    let Some((value_range, text)) = html_string_static_value(&value) else {
                        continue;
                    };
                    context_range = value_range;
                    (event_name, event_range) = live_action_directive_name(&text, value_range);
                }
                _ => {
                    if let Some((name, range)) = live_event_argument_attribute(node) {
                        current_arguments.push(LiveEventArgumentReference {
                            event: String::new(),
                            name,
                            file: path.to_owned(),
                            range,
                        });
                    }
                }
            }
        }
        let Some(kind) = action_kind else {
            continue;
        };
        if event_name.is_empty() {
            continue;
        }
        references.push(LiveEventReference {
            name: event_name.clone(),
            file: path.to_owned(),
            class: String::new(),
            range: event_range,
            context_range,
            kind,
        });
        for mut argument in current_arguments {
            argument.event = event_name.clone();
            arguments.push(argument);
        }
    }
    (
        unique_live_event_references(references),
        unique_live_event_argument_references(arguments),
    )
}

fn live_event_attribute_action_kind(value: &str) -> Option<LiveEventReferenceKind> {
    let lower = lowercase(value);
    if lower.contains("live#emitself") {
        Some(LiveEventReferenceKind::EmitSelf)
    } else if lower.contains("live#emitup") {
        Some(LiveEventReferenceKind::EmitUp)
    } else if lower.contains("live#emit") {
        Some(LiveEventReferenceKind::Attribute)
    } else {
        None
    }
}

fn live_event_argument_attribute(node: &SyntaxNode) -> Option<(String, TextRange)> {
    live_action_argument_attribute(node).filter(|(name, _)| !eq_ignore_case(name, "event"))
}

type LiveEventScan = (Vec<LiveEventReference>, Vec<LiveEventArgumentReference>);

/// The live event scan of `root`, memoized per tree snapshot.
fn live_event_scan_twig(path: &str, root: &SyntaxNode) -> std::sync::Arc<LiveEventScan> {
    static MEMO: DocumentMemo<LiveEventScan> = DocumentMemo::new(4);
    MEMO.get_or_compute(root, path, || {
        live_event_references_in_twig_impl(path, root)
    })
}

pub fn live_event_references_in_twig(path: &str, root: &SyntaxNode) -> Vec<LiveEventReference> {
    live_event_scan_twig(path, root).0.clone()
}

pub fn live_event_reference_at_twig(
    path: &str,
    root: &SyntaxNode,
    offset: u32,
) -> Option<LiveEventReference> {
    live_event_scan_twig(path, root)
        .0
        .iter()
        .find(|reference| {
            range_contains_cursor(reference.range, offset)
                || (reference.range.is_empty() && start(reference.range) == offset)
        })
        .cloned()
}

pub fn live_event_argument_references_in_twig(
    path: &str,
    root: &SyntaxNode,
) -> Vec<LiveEventArgumentReference> {
    live_event_scan_twig(path, root).1.clone()
}

pub fn live_event_argument_reference_at_twig(
    path: &str,
    root: &SyntaxNode,
    offset: u32,
) -> Option<LiveEventArgumentReference> {
    live_event_scan_twig(path, root)
        .1
        .iter()
        .find(|reference| range_contains_cursor(reference.range, offset))
        .cloned()
}

fn twig_hash_key_name_range(key: &SyntaxNode) -> (String, TextRange) {
    if let Some(literal) = twigquery::literal_string_at(key) {
        let name = twigquery::string_value(&literal).unwrap_or_default();
        let name = name.trim();
        if !name.is_empty() {
            return (name.to_owned(), twig_string_value_range(&literal));
        }
    }
    let text = key.text().to_string();
    let text = text.trim();
    let name = text.strip_suffix(':').unwrap_or(text).trim();
    let name = name.trim_matches(['\'', '"', '`']);
    if name.is_empty() {
        return (String::new(), TextRange::default());
    }
    let mut result = key.range_trimmed_trivia();
    if let Some(offset) = text.find(name) {
        let begin = start(result) + offset as u32;
        result = range(begin, begin + name.len() as u32);
    }
    (name.to_owned(), result)
}

fn live_action_argument_attribute(node: &SyntaxNode) -> Option<(String, TextRange)> {
    const PREFIX: &str = "data-live-";
    const SUFFIX: &str = "-param";
    let raw = lowercase(&attribute_name(node));
    if !raw.starts_with(PREFIX) || !raw.ends_with(SUFFIX) || raw.len() < PREFIX.len() + SUFFIX.len()
    {
        return None;
    }
    let middle = &raw[PREFIX.len()..raw.len() - SUFFIX.len()];
    if middle.is_empty() || middle == "action" || middle == "event" {
        return None;
    }
    if !middle.bytes().all(|byte| {
        byte == b'-' || byte == b'_' || byte.is_ascii_lowercase() || byte.is_ascii_digit()
    }) {
        return None;
    }
    let text = node.text().to_string();
    let offset = lowercase(text.trim()).find(&raw)?;
    let begin = start(node.range_trimmed_trivia()) + (offset + PREFIX.len()) as u32;
    Some((
        live_argument_name(middle),
        range(begin, begin + middle.len() as u32),
    ))
}

/// `item-name` / `item_name` → `itemName`.
fn live_argument_name(value: &str) -> String {
    let mut segments = value
        .split(['-', '_'])
        .filter(|segment| !segment.is_empty());
    let Some(first) = segments.next() else {
        return String::new();
    };
    let mut result = first.to_ascii_lowercase();
    for segment in segments {
        result.push_str(&segment[..1].to_ascii_uppercase());
        result.push_str(&segment[1..].to_ascii_lowercase());
    }
    result
}

/// The unquoted value of an HTML attribute string and its range, unless it
/// contains Twig syntax.
fn html_string_static_value(value: &HtmlString) -> Option<(TextRange, String)> {
    let node = value.syntax();
    let mut result = node.range_trimmed_trivia();
    let text = node.text().to_string();
    let mut text = text.trim();
    let bytes = text.as_bytes();
    if bytes.len() >= 2
        && (bytes[0] == b'\'' || bytes[0] == b'"')
        && bytes[bytes.len() - 1] == bytes[0]
    {
        result = range(start(result) + 1, u32::from(result.end()) - 1);
        text = &text[1..text.len() - 1];
    }
    if text.contains("{{") || text.contains("{%") || text.contains("{#") {
        return None;
    }
    Some((result, text.to_owned()))
}

/// The action or event name after the last `|` modifier separator; empty
/// when it is not an identifier.
fn live_action_directive_name(value: &str, value_range: TextRange) -> (String, TextRange) {
    let bytes = value.as_bytes();
    let is_space = |byte: u8| matches!(byte, b' ' | b'\t' | b'\r' | b'\n');
    let mut begin = value.rfind('|').map_or(0, |separator| separator + 1);
    while begin < bytes.len() && is_space(bytes[begin]) {
        begin += 1;
    }
    let mut end = bytes.len();
    while end > begin && is_space(bytes[end - 1]) {
        end -= 1;
    }
    let result = range(
        start(value_range) + begin as u32,
        start(value_range) + end as u32,
    );
    let name = &value[begin..end];
    let valid = name.chars().enumerate().all(|(index, char)| {
        char == '_' || char.is_ascii_alphabetic() || (index > 0 && char.is_ascii_digit())
    });
    if name.is_empty() || !valid {
        return (String::new(), result);
    }
    (name.to_owned(), result)
}

/// The `<twig:block name="...">` usage at `offset`, with its enclosing
/// component.
pub fn block_usage_at(node: &SyntaxNode, offset: u32) -> Option<ComponentBlockUsage> {
    let attribute = twigquery::html_attribute_at(node)?;
    let name = attribute_name(&attribute);
    if !eq_ignore_case(name.strip_prefix(':').unwrap_or(&name), "name") {
        return None;
    }
    let tag_node = twigquery::starting_html_tag_at(&attribute)?;
    let tag = HtmlStartingTag::cast(tag_node.clone())?;
    if !eq_ignore_case(tag.name()?.text(), "twig:block") {
        return None;
    }
    let value = HtmlString::cast(first_descendant(&attribute, HTML_STRING)?)?;
    let inner = value.inner()?;
    let inner = inner.syntax();
    let result = inner.range_trimmed_trivia();
    if !range_contains_cursor(result, offset) {
        return None;
    }
    let component = enclosing_html_component(&tag_node);
    if component.is_empty() {
        return None;
    }
    Some(ComponentBlockUsage {
        component,
        name: inner.text().to_string(),
        range: result,
    })
}

pub fn block_usages_in_twig(root: &SyntaxNode) -> Vec<ComponentBlockUsage> {
    let mut result = Vec::new();
    for attribute in twigquery::iterate_nodes(root, &[HTML_ATTRIBUTE]) {
        let Some(inner) = first_descendant(&attribute, HTML_STRING)
            .and_then(HtmlString::cast)
            .and_then(|value| value.inner())
        else {
            continue;
        };
        if let Some(usage) = block_usage_at(&attribute, start(inner.syntax().text_range())) {
            result.push(usage);
        }
    }
    result
}

/// The name of the closest enclosing `<twig:Component>` other than
/// `<twig:block>`.
pub fn enclosing_html_component(node: &SyntaxNode) -> String {
    for current in node.ancestors().skip(1) {
        let Some(tag) = HtmlTag::cast(current) else {
            continue;
        };
        let Some(start) = tag.starting_tag() else {
            continue;
        };
        if !start.is_twig_component() {
            continue;
        }
        let Some(name) = start.name() else {
            continue;
        };
        let Some(rest) = strip_twig_prefix(name.text()) else {
            continue;
        };
        let name = normalize_component_name(rest);
        if !eq_ignore_case(&name, "block") {
            return name;
        }
    }
    String::new()
}

pub fn usages_in_twig(path: &str, root: &SyntaxNode) -> Vec<Usage> {
    usages_in_twig_impl(path, root)
}

pub fn usage_at(path: &str, root: &SyntaxNode, offset: u32) -> Option<Usage> {
    static MEMO: DocumentMemo<Vec<Usage>> = DocumentMemo::new(4);
    MEMO.get_or_compute(root, path, || usages_in_twig_impl(path, root))
        .iter()
        .find(|usage| range_contains_cursor(usage.range, offset))
        .cloned()
}

/// The prop attribute of a `<twig:Component>` tag at `offset`.
pub fn prop_usage_at(_root: &SyntaxNode, node: &SyntaxNode, offset: u32) -> Option<PropUsage> {
    let attribute = twigquery::html_attribute_at(node)?;
    let tag = HtmlStartingTag::cast(twigquery::starting_html_tag_at(&attribute)?)?;
    if !tag.is_twig_component() {
        return None;
    }
    let tag_name = tag.name()?;
    let rest = strip_twig_prefix(tag_name.text())?;
    let raw_name = attribute_name(&attribute);
    let name = raw_name.strip_prefix(':').unwrap_or(&raw_name);
    if name.is_empty() {
        return None;
    }
    let mut result = attribute.range_trimmed_trivia();
    let text = attribute.text().to_string();
    if let Some(offset) = text.trim().find(&raw_name) {
        let begin = start(result) + offset as u32;
        result = range(begin, begin + raw_name.len() as u32);
    }
    if !range_contains_cursor(result, offset) {
        return None;
    }
    Some(PropUsage {
        component: normalize_component_name(rest),
        name: name.to_owned(),
        range: result,
        dynamic: raw_name.starts_with(':'),
    })
}

/// The root variable name at `node` (`foo` in `foo.bar`, not `bar`).
pub fn variable_at(node: &SyntaxNode) -> Option<(String, TextRange)> {
    let name_node = twigquery::closest_node_of_kind(node, &[TWIG_LITERAL_NAME])?;
    twigquery::closest_node_of_kind(&name_node, &[TWIG_VAR])?;
    if let Some(accessor) = twigquery::closest_node_of_kind(&name_node, &[TWIG_ACCESSOR]) {
        let first = twigquery::iterate_nodes(&accessor, &[TWIG_LITERAL_NAME]).next();
        if first.as_ref() != Some(&name_node) {
            return None;
        }
    }
    let name = name_node.text().to_string().trim().to_owned();
    (!name.is_empty()).then(|| (name, name_node.range_trimmed_trivia()))
}

/// The root and first member of a simple accessor such as `computed.total`.
/// Deeper members are not attributed to the component object because their
/// receiver is the getter result.
pub fn accessor_member_at(node: &SyntaxNode) -> Option<(String, String, TextRange)> {
    let name_node = twigquery::closest_node_of_kind(node, &[TWIG_LITERAL_NAME])?;
    let accessor = twigquery::closest_node_of_kind(&name_node, &[TWIG_ACCESSOR])?;
    twigquery::closest_node_of_kind(&accessor, &[TWIG_VAR])?;
    let mut names = twigquery::iterate_nodes(&accessor, &[TWIG_LITERAL_NAME]);
    let (first, second) = (names.next()?, names.next()?);
    if second != name_node {
        return None;
    }
    let root = first.text().to_string().trim().to_owned();
    let member = second.text().to_string().trim().to_owned();
    (!root.is_empty() && !member.is_empty()).then(|| (root, member, second.range_trimmed_trivia()))
}

pub(crate) fn props_in_twig_impl(path: &str, root: &SyntaxNode) -> Vec<Prop> {
    let documentation = prop_documentation(&root.text().to_string());
    let mut result = Vec::new();
    for declaration in twigquery::iterate_nodes(root, &[TWIG_PROP_DECLARATION]) {
        let Some(name_node) = first_descendant(&declaration, TWIG_LITERAL_NAME) else {
            continue;
        };
        let name = name_node.text().to_string().trim().to_owned();
        if name.is_empty() {
            continue;
        }
        let mut prop = Prop {
            range: name_node.range_trimmed_trivia(),
            file: path.to_owned(),
            ..Prop::default()
        };
        if let Some((ty, description)) = documentation.get(&name) {
            prop.ty = ty.clone();
            prop.description = description.clone();
        }
        prop.name = name;
        let text = declaration.text().to_string();
        if let Some(equal) = text.find('=') {
            prop.default_value = text[equal + 1..].trim().to_owned();
        }
        result.push(prop);
    }
    unique_props(result)
}

pub fn props_in_twig(path: &str, root: &SyntaxNode) -> Vec<Prop> {
    props_in_twig_impl(path, root)
}

fn block_component_name(block: &SyntaxNode) -> (String, TextRange) {
    let literal = first_descendant(block, TWIG_LITERAL_STRING);
    let name = first_descendant(block, TWIG_LITERAL_NAME);
    if let Some(literal) = &literal
        && name
            .as_ref()
            .is_none_or(|name| literal.text_range().start() < name.text_range().start())
    {
        return (
            normalize_component_name(&twigquery::string_value(literal).unwrap_or_default()),
            twig_string_value_range(literal),
        );
    }
    match name {
        Some(name) => (
            normalize_component_name(&name.text().to_string()),
            name.range_trimmed_trivia(),
        ),
        None => (String::new(), TextRange::default()),
    }
}

fn first_descendant(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.descendants().find(|child| child.kind() == kind)
}

/// `@prop name type description` documentation; later entries win.
fn prop_documentation(source: &str) -> HashMap<String, (String, String)> {
    PROP_DOCUMENTATION_PATTERN
        .captures_iter(source)
        .map(|captures| {
            let description = captures
                .get(3)
                .map_or("", |description| description.as_str().trim());
            (
                captures[1].to_owned(),
                (captures[2].to_owned(), description.to_owned()),
            )
        })
        .collect()
}

fn unique_by<T>(values: Vec<T>, key: impl Fn(&T) -> String) -> Vec<T> {
    let mut seen = HashSet::with_capacity(values.len());
    values
        .into_iter()
        .filter(|value| seen.insert(key(value)))
        .collect()
}

fn unique_usages(usages: Vec<Usage>) -> Vec<Usage> {
    unique_by(usages, |usage| {
        format!("{}\0{}\0{}", usage.file, range_key(usage.range), usage.name)
    })
}

pub(crate) fn unique_props(props: Vec<Prop>) -> Vec<Prop> {
    unique_by(props, |prop| {
        format!("{}\0{}\0{}", prop.file, prop.name, range_key(prop.range))
    })
}

fn unique_live_action_references(references: Vec<LiveActionReference>) -> Vec<LiveActionReference> {
    unique_by(references, |reference| {
        format!(
            "{}\0{}\0{}",
            reference.file,
            range_key(reference.range),
            reference.name
        )
    })
}

fn unique_live_action_argument_references(
    references: Vec<LiveActionArgumentReference>,
) -> Vec<LiveActionArgumentReference> {
    unique_by(references, |reference| {
        format!(
            "{}\0{}\0{}\0{}",
            reference.file,
            range_key(reference.range),
            reference.action,
            reference.name
        )
    })
}

pub(crate) fn unique_live_event_references(
    references: Vec<LiveEventReference>,
) -> Vec<LiveEventReference> {
    unique_by(references, |reference| {
        format!(
            "{}\0{}\0{}",
            reference.file,
            range_key(reference.range),
            reference.name
        )
    })
}

pub(crate) fn unique_live_event_argument_references(
    references: Vec<LiveEventArgumentReference>,
) -> Vec<LiveEventArgumentReference> {
    unique_by(references, |reference| {
        format!(
            "{}\0{}\0{}\0{}",
            reference.file,
            range_key(reference.range),
            reference.event,
            reference.name
        )
    })
}

/// Whether `offset` is inside `range` or at the end of a non-empty range.
pub(crate) fn range_contains_cursor(range: TextRange, offset: u32) -> bool {
    let (begin, end) = (start(range), u32::from(range.end()));
    (offset >= begin && offset < end) || (offset == end && end > begin)
}
