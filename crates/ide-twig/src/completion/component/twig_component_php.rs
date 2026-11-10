//! Symfony UX component attribute completion inside component classes.

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_NAME, PHP_NAMESPACE, PHP_PARAMETER, PHP_PROPERTY_DECLARATION, TK_OPEN_TAG,
};
use swlsp_php::NameResolver;
use swlsp_php_resolver::{ImportKind, parse_use_declaration};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};
use swlsp_textutil::eq_ignore_case;

use super::twig_component::byte_range;
use crate::completion::common::{protocol_range, text_edit};
use crate::completion::component_util::{kind, markdown, request_offset};

const EXPOSE_IN_TEMPLATE_ATTRIBUTE: &str =
    "Symfony\\UX\\TwigComponent\\Attribute\\ExposeInTemplate";
pub(super) const PRE_MOUNT_ATTRIBUTE: &str = "Symfony\\UX\\TwigComponent\\Attribute\\PreMount";
const POST_MOUNT_ATTRIBUTE: &str = "Symfony\\UX\\TwigComponent\\Attribute\\PostMount";
const LIVE_PROP_ATTRIBUTE: &str = "Symfony\\UX\\LiveComponent\\Attribute\\LiveProp";
const LIVE_ACTION_ATTRIBUTE: &str = "Symfony\\UX\\LiveComponent\\Attribute\\LiveAction";
const LIVE_ARG_ATTRIBUTE: &str = "Symfony\\UX\\LiveComponent\\Attribute\\LiveArg";
const LIVE_LISTENER_ATTRIBUTE: &str = "Symfony\\UX\\LiveComponent\\Attribute\\LiveListener";
const POST_HYDRATE_ATTRIBUTE: &str = "Symfony\\UX\\LiveComponent\\Attribute\\PostHydrate";
const PRE_DEHYDRATE_ATTRIBUTE: &str = "Symfony\\UX\\LiveComponent\\Attribute\\PreDehydrate";
const PRE_RE_RENDER_ATTRIBUTE: &str = "Symfony\\UX\\LiveComponent\\Attribute\\PreReRender";

#[derive(Default)]
pub struct TwigComponentPhpCompletionProvider;

impl TwigComponentPhpCompletionProvider {
    pub fn new() -> Self {
        Self
    }
}

impl CompletionProvider for TwigComponentPhpCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let (Some(root), Some(line_index)) = (request.root.as_ref(), request.line_index()) else {
            return Vec::new();
        };
        if !eq_ignore_case(
            swlsp_uriutil::filepath::ext(&request.params.text_document.uri),
            ".php",
        ) {
            return Vec::new();
        }
        let Some(offset) = request_offset(request) else {
            return Vec::new();
        };
        let Some(replace) = php_attribute_replacement_range(request, offset) else {
            return Vec::new();
        };
        let (class, target) = component_attribute_target(root, request.node.as_ref(), offset);
        let Some(class) = class else {
            return Vec::new();
        };
        if target == TargetKind::Class {
            return Vec::new();
        }
        let resolver = NameResolver::new(root);
        let (component, live) = php_component_class_kinds(&class, &resolver);
        if !component {
            return Vec::new();
        }
        let mut items: Vec<protocol::CompletionItem> = component_attribute_specs(target, live)
            .into_iter()
            .map(|spec| {
                let (insert_name, import_edit) = component_attribute_import(request, spec.fqn);
                protocol::CompletionItem {
                    label: spec.name.to_string(),
                    filter_text: spec.name.to_string(),
                    kind: kind(protocol::CompletionItemKind::CLASS),
                    detail: format!("{} • {}", spec.detail, spec.fqn),
                    text_edit: Some(text_edit(protocol_range(replace, line_index), insert_name)),
                    documentation: markdown(spec.documentation),
                    additional_text_edits: import_edit.into_iter().collect(),
                    ..Default::default()
                }
            })
            .collect();
        items.sort_unstable_by(crate::completion::common::compare_completion_items);
        items
    }

    fn trigger_characters(&self) -> Vec<String> {
        vec!["#".to_string()]
    }
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub(super) enum TargetKind {
    Class,
    Method,
    Property,
    Parameter,
}

struct Spec {
    name: &'static str,
    fqn: &'static str,
    detail: &'static str,
    documentation: &'static str,
}

fn component_attribute_specs(target: TargetKind, live: bool) -> Vec<Spec> {
    let expose = Spec {
        name: "ExposeInTemplate",
        fqn: EXPOSE_IN_TEMPLATE_ATTRIBUTE,
        detail: "Expose this member in the component template",
        documentation: "Exposes a property or method to Twig. `name`, `getter`, and `destruct` can customize the mapping.",
    };
    match target {
        TargetKind::Property => {
            let mut result = vec![expose];
            if live {
                result.push(Spec {
                    name: "LiveProp",
                    fqn: LIVE_PROP_ATTRIBUTE,
                    detail: "Synchronize this Live Component property",
                    documentation: "Marks a property as live state. Use `writable: true` (or writable paths) to allow browser-side updates.",
                });
            }
            result
        }
        TargetKind::Parameter => {
            if !live {
                return Vec::new();
            }
            vec![Spec {
                name: "LiveArg",
                fqn: LIVE_ARG_ATTRIBUTE,
                detail: "Bind a Live Action or listener argument",
                documentation: "Maps this method parameter from a Live Action argument or emitted Live event payload.",
            }]
        }
        TargetKind::Method => {
            let mut result = vec![
                expose,
                Spec {
                    name: "PostMount",
                    fqn: POST_MOUNT_ATTRIBUTE,
                    detail: "Run after component props are mounted",
                    documentation: "Lifecycle hook invoked after mounting component props. Supports `priority`.",
                },
                Spec {
                    name: "PreMount",
                    fqn: PRE_MOUNT_ATTRIBUTE,
                    detail: "Transform props before component mount",
                    documentation: "Lifecycle hook invoked before mounting component props. Supports `priority`.",
                },
            ];
            if live {
                result.extend([
                    Spec {
                        name: "LiveAction",
                        fqn: LIVE_ACTION_ATTRIBUTE,
                        detail: "Expose this method as a Live Action",
                        documentation: "Makes a public component method callable by Live Component actions.",
                    },
                    Spec {
                        name: "LiveListener",
                        fqn: LIVE_LISTENER_ATTRIBUTE,
                        detail: "Listen for a Live Component event",
                        documentation: "Invokes this method when the configured Live Component event is emitted.",
                    },
                    Spec {
                        name: "PostHydrate",
                        fqn: POST_HYDRATE_ATTRIBUTE,
                        detail: "Run after Live Component hydration",
                        documentation: "Lifecycle hook invoked after live state has been hydrated.",
                    },
                    Spec {
                        name: "PreDehydrate",
                        fqn: PRE_DEHYDRATE_ATTRIBUTE,
                        detail: "Run before Live Component dehydration",
                        documentation: "Lifecycle hook invoked before live state is dehydrated.",
                    },
                    Spec {
                        name: "PreReRender",
                        fqn: PRE_RE_RENDER_ATTRIBUTE,
                        detail: "Run before a Live Component re-render",
                        documentation: "Lifecycle hook invoked immediately before a live re-render.",
                    },
                ]);
            }
            result
        }
        TargetKind::Class => Vec::new(),
    }
}

fn start(node: &SyntaxNode) -> u32 {
    node.text_range().start().into()
}

/// The class and the member kind an attribute
/// at `offset` decorates.
pub(super) fn component_attribute_target(
    root: &SyntaxNode,
    node: Option<&SyntaxNode>,
    offset: u32,
) -> (Option<SyntaxNode>, TargetKind) {
    if let Some(node) = node {
        if let Some(parameter) = closest(node, PHP_PARAMETER) {
            return (phpquery::class_at(&parameter), TargetKind::Parameter);
        }
        if let Some(property) = closest(node, PHP_PROPERTY_DECLARATION) {
            return (phpquery::class_at(&property), TargetKind::Property);
        }
        if let Some(method) = phpquery::method_at(node) {
            return (phpquery::class_at(&method), TargetKind::Method);
        }
    }
    let mut class = node.and_then(phpquery::class_at);
    if class.is_none() {
        let mut nearest_class: Option<SyntaxNode> = None;
        for candidate in phpquery::classes(root) {
            if let Some(body) = phpquery::class_body(&candidate) {
                let range = body.text_range();
                if offset >= u32::from(range.start()) && offset <= u32::from(range.end()) {
                    class = Some(candidate);
                    break;
                }
            }
            if start(&candidate) >= offset
                && nearest_class
                    .as_ref()
                    .is_none_or(|nearest| start(&candidate) < start(nearest))
            {
                nearest_class = Some(candidate);
            }
        }
        if class.is_none() && nearest_class.is_some() {
            return (nearest_class, TargetKind::Class);
        }
    }
    let Some(class) = class else {
        return (None, TargetKind::Class);
    };
    let Some(body) = phpquery::class_body(&class) else {
        return (Some(class), TargetKind::Class);
    };
    if offset < start(&body) {
        return (Some(class), TargetKind::Class);
    }
    let mut nearest: Option<SyntaxNode> = None;
    for candidate in phpquery::properties(&class)
        .into_iter()
        .chain(phpquery::methods(&class))
    {
        if start(&candidate) < offset {
            continue;
        }
        if nearest
            .as_ref()
            .is_none_or(|nearest| start(&candidate) < start(nearest))
        {
            nearest = Some(candidate);
        }
    }
    match nearest {
        None => (Some(class), TargetKind::Class),
        Some(nearest) if nearest.kind() == PHP_PROPERTY_DECLARATION => {
            (Some(class), TargetKind::Property)
        }
        Some(_) => (Some(class), TargetKind::Method),
    }
}

fn closest(node: &SyntaxNode, kind: swlsp_syntax::Kind) -> Option<SyntaxNode> {
    node.ancestors().find(|current| current.kind() == kind)
}

fn php_component_class_kinds(class: &SyntaxNode, resolver: &NameResolver) -> (bool, bool) {
    let (mut component, mut live) = (false, false);
    for attribute in phpquery::attributes(class) {
        let resolved = resolver.resolve(&phpquery::attribute_name(&attribute));
        let name = resolved.trim_matches('\\');
        if eq_ignore_case(
            name,
            "Symfony\\UX\\TwigComponent\\Attribute\\AsTwigComponent",
        ) {
            component = true;
        } else if eq_ignore_case(
            name,
            "Symfony\\UX\\LiveComponent\\Attribute\\AsLiveComponent",
        ) {
            component = true;
            live = true;
        }
    }
    (component, live)
}

/// The range of the attribute name being typed.
pub(super) fn php_attribute_replacement_range(
    request: &CompletionRequest,
    offset: u32,
) -> Option<TextRange> {
    if let Some(attribute) = request.node.as_ref().and_then(phpquery::attribute_at)
        && let Some(child) = attribute.children().find(|child| child.kind() == PHP_NAME)
    {
        let name_range = child.range_trimmed_trivia();
        if offset >= u32::from(name_range.start()) && offset <= u32::from(name_range.end()) {
            return Some(name_range);
        }
        return None;
    }
    let source = request.source();
    let offset = (offset as usize).min(source.len());
    let bytes = source.as_bytes();
    let line_start = bytes[..offset]
        .iter()
        .rposition(|byte| *byte == b'\n')
        .map_or(0, |index| index + 1);
    let line = &bytes[line_start..offset];
    let open = line.windows(2).rposition(|pair| pair == b"#[")?;
    let name_start = line_start + open + 2;
    for byte in &bytes[name_start..offset] {
        let c = char::from(*byte);
        if !c.is_alphabetic() && !c.is_ascii_digit() && c != '_' && c != '\\' {
            return None;
        }
    }
    Some(byte_range(name_start as u32, offset as u32))
}

/// The name to insert and an optional `use`
/// edit.
pub(super) fn component_attribute_import(
    request: &CompletionRequest,
    fqn: &str,
) -> (String, Option<protocol::TextEdit>) {
    let short = &fqn[fqn.rfind('\\').map_or(0, |index| index + 1)..];
    let Some(root) = request.root.as_ref() else {
        return (short.to_string(), None);
    };
    let declarations = phpquery::use_declarations(root);
    let mut conflict = false;
    for declaration in &declarations {
        for imported in parse_use_declaration(&declaration.text().to_string()) {
            if imported.kind != ImportKind::Class {
                continue;
            }
            if eq_ignore_case(imported.target.trim_matches('\\'), fqn) {
                return (imported.alias, None);
            }
            if eq_ignore_case(&imported.alias, short) {
                conflict = true;
            }
        }
    }
    if conflict {
        return (format!("\\{fqn}"), None);
    }
    let Some(line_index) = request.line_index() else {
        return (short.to_string(), None);
    };
    let offset = php_import_insertion_offset(root);
    let new_text = if declarations.is_empty() {
        format!("\n\nuse {fqn};")
    } else {
        format!("\nuse {fqn};")
    };
    let edit = protocol::TextEdit {
        range: protocol_range(byte_range(offset, offset), line_index),
        new_text,
        ..Default::default()
    };
    (short.to_string(), Some(edit))
}

fn php_import_insertion_offset(root: &SyntaxNode) -> u32 {
    let result = phpquery::use_declarations(root)
        .iter()
        .map(|declaration| u32::from(declaration.text_range().end()))
        .max()
        .unwrap_or(0);
    if result != 0 {
        return result;
    }
    if let Some(namespace) = phpquery::nodes(root, &[PHP_NAMESPACE]).first() {
        let text = namespace.text().to_string();
        if let Some(end) = text.find([';', '{']) {
            return start(namespace) + end as u32 + 1;
        }
    }
    if let Some(open_tag) = root.first_token()
        && open_tag.kind() == TK_OPEN_TAG
    {
        return open_tag.text_range().end().into();
    }
    0
}
