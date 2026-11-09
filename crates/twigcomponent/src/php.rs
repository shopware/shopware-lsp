//! Component declarations, props, Live actions, listeners and emitted events
//! in PHP sources.

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{PHP_MEMBER_CALL, PHP_NAME, PHP_NAMED_ARGUMENT};
use swlsp_php::NameResolver;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::config::quoted_value_range as string_value_range;
use crate::model::*;
use crate::twig::{
    range_contains_cursor, unique_live_event_argument_references, unique_live_event_references,
    unique_props,
};
use swlsp_twig::DocumentMemo;

pub(crate) const AS_TWIG_COMPONENT: &str = "Symfony\\UX\\TwigComponent\\Attribute\\AsTwigComponent";
pub(crate) const AS_LIVE_COMPONENT: &str = "Symfony\\UX\\LiveComponent\\Attribute\\AsLiveComponent";
pub(crate) const EXPOSE_IN_TEMPLATE: &str =
    "Symfony\\UX\\TwigComponent\\Attribute\\ExposeInTemplate";
const LIVE_PROP: &str = "Symfony\\UX\\LiveComponent\\Attribute\\LiveProp";
pub(crate) const LIVE_ACTION: &str = "Symfony\\UX\\LiveComponent\\Attribute\\LiveAction";
const LIVE_ARG: &str = "Symfony\\UX\\LiveComponent\\Attribute\\LiveArg";
pub(crate) const LIVE_LISTENER: &str = "Symfony\\UX\\LiveComponent\\Attribute\\LiveListener";

fn resolved_name(resolver: &NameResolver, name: &str) -> String {
    normalize_class(&resolver.resolve(name))
}

fn class_name(resolver: &NameResolver, class: &SyntaxNode) -> String {
    resolved_name(resolver, &phpquery::class_name(class))
}

fn is_class(class: &SyntaxNode) -> bool {
    !phpquery::is_interface(class) && !phpquery::is_trait(class)
}

/// Component declarations from `#[AsTwigComponent]` / `#[AsLiveComponent]`.
pub fn declarations_in_php(path: &str, root: &SyntaxNode) -> Vec<Declaration> {
    let resolver = NameResolver::new(root);
    let mut result = Vec::new();
    for class in phpquery::classes(root) {
        if !is_class(&class) {
            continue;
        }
        let class_name = class_name(&resolver, &class);
        if class_name.is_empty() {
            continue;
        }
        for attribute in phpquery::attributes(&class) {
            let resolved = resolved_name(&resolver, &phpquery::attribute_name(&attribute));
            let live = eq_ignore_case(&resolved, AS_LIVE_COMPONENT);
            if !eq_ignore_case(&resolved, AS_TWIG_COMPONENT) && !live {
                continue;
            }
            let (name, name_range) = php_attribute_string_value(&attribute, "name", 0);
            let (template, template_range) = php_attribute_string_value(&attribute, "template", 1);
            let expose_public_props = php_attribute_argument(&attribute, "exposePublicProps", 2)
                .is_none_or(|value| !eq_ignore_case(value.text().to_string().trim(), "false"));
            result.push(Declaration {
                name: normalize_component_name(&name),
                class: class_name.clone(),
                template: normalize_template(&template),
                file: path.to_owned(),
                name_range,
                class_range: php_class_name_range(&class),
                template_range,
                source: SourceKind::Attribute,
                expose_public_props,
                live,
                ..Declaration::default()
            });
        }
    }
    result
}

/// The declaration whose name or class name contains `offset`.
pub fn declaration_at_php(path: &str, root: &SyntaxNode, offset: u32) -> Option<Declaration> {
    static MEMO: DocumentMemo<Vec<Declaration>> = DocumentMemo::new(4);
    MEMO.get_or_compute(root, path, || declarations_in_php(path, root))
        .iter()
        .find(|declaration| {
            range_contains_cursor(declaration.name_range, offset)
                || range_contains_cursor(declaration.class_range, offset)
        })
        .cloned()
}

pub(crate) fn props_in_php(path: &str, root: &SyntaxNode) -> Vec<Prop> {
    let resolver = NameResolver::new(root);
    let mut result = Vec::new();
    for class in phpquery::classes(root) {
        if !is_class(&class) {
            continue;
        }
        let class_name = class_name(&resolver, &class);
        if class_name.is_empty() || !component_class_kinds(&class, &resolver).0 {
            continue;
        }
        for property in phpquery::properties(&class) {
            let expose = resolved_attribute(&property, &resolver, EXPOSE_IN_TEMPLATE);
            let live_attribute = resolved_attribute(&property, &resolver, LIVE_PROP);
            if expose.is_none() && live_attribute.is_none() {
                continue;
            }
            for variable in phpquery::property_variables(&property) {
                let mut name = phpquery::variable_name(&variable);
                if name.is_empty() {
                    continue;
                }
                let member = name.clone();
                let mut prop_type = phpquery::property_type(&property);
                if let Some(expose) = &expose {
                    let (custom, _) = php_attribute_string_value(expose, "name", 0);
                    if !custom.is_empty() {
                        name = custom;
                    }
                    let (getter, _) = php_attribute_string_value(expose, "getter", 1);
                    if !getter.is_empty()
                        && let Some(method) = phpquery::methods(&class)
                            .into_iter()
                            .find(|method| eq_ignore_case(&phpquery::method_name(method), &getter))
                    {
                        prop_type = phpquery::method_return_type(&method);
                    }
                }
                result.push(Prop {
                    name,
                    ty: prop_type,
                    file: path.to_owned(),
                    class: class_name.clone(),
                    member,
                    range: variable.range_trimmed_trivia(),
                    live: live_attribute.is_some(),
                    writable: live_prop_writable(live_attribute.as_ref()),
                    ..Prop::default()
                });
            }
        }
        for method in phpquery::methods(&class) {
            let Some(expose) = resolved_attribute(&method, &resolver, EXPOSE_IN_TEMPLATE) else {
                continue;
            };
            let method_name = phpquery::method_name(&method);
            let mut name = exposed_method_name(&method_name);
            let (custom, _) = php_attribute_string_value(&expose, "name", 0);
            if !custom.is_empty() {
                name = custom;
            }
            if name.is_empty() {
                continue;
            }
            result.push(Prop {
                name,
                ty: phpquery::method_return_type(&method),
                file: path.to_owned(),
                class: class_name.clone(),
                range: php_method_name_range(&method),
                member: method_name,
                ..Prop::default()
            });
        }
    }
    unique_props(result)
}

pub(crate) fn live_actions_in_php(path: &str, root: &SyntaxNode) -> Vec<LiveAction> {
    let resolver = NameResolver::new(root);
    let mut result = Vec::new();
    for class in phpquery::classes(root) {
        let class_name = class_name(&resolver, &class);
        if class_name.is_empty() {
            continue;
        }
        for method in phpquery::methods(&class) {
            if resolved_attribute(&method, &resolver, LIVE_ACTION).is_none() {
                continue;
            }
            let name = phpquery::method_name(&method);
            if name.is_empty() {
                continue;
            }
            result.push(LiveAction {
                method: name.clone(),
                name,
                class: class_name.clone(),
                file: path.to_owned(),
                range: php_method_name_range(&method),
                parameters: live_parameters_in_php(&method, &resolver),
            });
        }
    }
    result
}

pub(crate) fn live_listeners_in_php_impl(path: &str, root: &SyntaxNode) -> Vec<LiveListener> {
    // The resolver and class names are only needed once a method carries an
    // attribute; most PHP files have none, so build them lazily.
    let resolver = std::cell::OnceCell::new();
    let resolver = || resolver.get_or_init(|| NameResolver::new(root));
    let mut result = Vec::new();
    for class in phpquery::classes(root) {
        let owner = std::cell::OnceCell::new();
        for method in phpquery::methods(&class) {
            let attributes = phpquery::attributes(&method);
            if attributes.is_empty() {
                continue;
            }
            let owner = owner.get_or_init(|| class_name(resolver(), &class));
            if owner.is_empty() {
                break;
            }
            let mut names: Vec<(String, TextRange)> = Vec::new();
            for attribute in attributes {
                let resolved = resolved_name(resolver(), &phpquery::attribute_name(&attribute));
                if !eq_ignore_case(&resolved, LIVE_LISTENER) {
                    continue;
                }
                let (name, range) = php_attribute_string_value(&attribute, "event", 0);
                let name = name.trim();
                if !name.is_empty() {
                    names.push((name.to_owned(), range));
                }
            }
            if names.is_empty() {
                continue;
            }
            let parameters = live_parameters_in_php(&method, resolver());
            let method_name = phpquery::method_name(&method);
            let method_range = php_method_name_range(&method);
            for (name, range) in names {
                result.push(LiveListener {
                    name,
                    class: owner.clone(),
                    method: method_name.clone(),
                    file: path.to_owned(),
                    range,
                    method_range,
                    parameters: parameters.clone(),
                });
            }
        }
    }
    result
}

fn live_parameters_in_php(
    method: &SyntaxNode,
    resolver: &NameResolver,
) -> Vec<LiveActionParameter> {
    let mut result = Vec::new();
    for parameter in phpquery::parameters(method) {
        let raw = phpquery::parameter_name(&parameter);
        let php_name = raw.strip_prefix('$').unwrap_or(&raw).to_owned();
        if php_name.is_empty() {
            continue;
        }
        let attribute = resolved_attribute(&parameter, resolver, LIVE_ARG);
        let mut name = php_name.clone();
        if let Some(attribute) = &attribute {
            let (alias, _) = php_attribute_string_value(attribute, "name", 0);
            if !alias.is_empty() {
                name = alias;
            }
        }
        result.push(LiveActionParameter {
            name,
            php_name,
            ty: phpquery::parameter_type(&parameter),
            optional: phpquery::parameter_optional(&parameter)
                || phpquery::parameter_variadic(&parameter),
            live_arg: attribute.is_some(),
            range: parameter.range_trimmed_trivia(),
        });
    }
    result
}

pub(crate) fn live_event_references_in_php_impl(
    path: &str,
    root: &SyntaxNode,
) -> (Vec<LiveEventReference>, Vec<LiveEventArgumentReference>) {
    // Only needed for the class of a found reference; most files have none.
    let resolver = std::cell::OnceCell::new();
    let mut references = Vec::new();
    let mut arguments = Vec::new();
    for call in phpquery::calls(root, &[]) {
        let Some(kind) = live_event_call_kind(Some(&call)) else {
            continue;
        };
        let Some(literal) = php_call_argument_expression(&call, &["event", "eventName"], 0)
            .and_then(|expression| phpquery::string_at(&expression))
            .filter(php_static_string)
        else {
            continue;
        };
        let name = phpquery::string_value(&literal).trim().to_owned();
        if name.is_empty() {
            continue;
        }
        let range = string_value_range(&literal);
        let class = phpquery::class_at(&call)
            .map(|class| class_name(resolver.get_or_init(|| NameResolver::new(root)), &class))
            .unwrap_or_default();
        references.push(LiveEventReference {
            name: name.clone(),
            file: path.to_owned(),
            class,
            range,
            context_range: range,
            kind,
        });
        let data = php_call_argument_expression(&call, &["data"], 1)
            .and_then(|expression| phpquery::array_at(&expression));
        let Some(data) = data else {
            continue;
        };
        for item in phpquery::array_items(&data) {
            let Some(key) = phpquery::array_item_key(&item)
                .and_then(|key| phpquery::string_at(&key))
                .filter(php_static_string)
            else {
                continue;
            };
            let argument = phpquery::string_value(&key).trim().to_owned();
            if argument.is_empty() {
                continue;
            }
            arguments.push(LiveEventArgumentReference {
                event: name.clone(),
                name: argument,
                file: path.to_owned(),
                range: string_value_range(&key),
            });
        }
    }
    (
        unique_live_event_references(references),
        unique_live_event_argument_references(arguments),
    )
}

fn live_event_call_kind(call: Option<&SyntaxNode>) -> Option<LiveEventReferenceKind> {
    let call = call?;
    if call.kind() != PHP_MEMBER_CALL {
        return None;
    }
    // The method name is checked first: it rejects almost every call
    // without copying the receiver text.
    let kind = match lowercase(&phpquery::call_method_name(call)).as_str() {
        "emit" => LiveEventReferenceKind::Emit,
        "emitup" => LiveEventReferenceKind::EmitUp,
        "emitself" => LiveEventReferenceKind::EmitSelf,
        _ => return None,
    };
    let receiver = phpquery::call_receiver(call)?;
    if !eq_ignore_case(receiver.text().to_string().trim(), "$this") {
        return None;
    }
    Some(kind)
}

fn php_call_argument_expression(
    call: &SyntaxNode,
    names: &[&str],
    position: usize,
) -> Option<SyntaxNode> {
    let mut positional = 0;
    for (index, argument) in phpquery::arguments(call).enumerate() {
        let name = phpquery::argument_name(&argument);
        if !name.is_empty() {
            if names
                .iter()
                .any(|candidate| eq_ignore_case(&name, candidate))
            {
                return phpquery::argument_expression(call, index);
            }
            continue;
        }
        if positional == position {
            return phpquery::argument_expression(call, index);
        }
        positional += 1;
    }
    None
}

fn php_static_string(node: &SyntaxNode) -> bool {
    let text = node.text().to_string();
    let text = text.trim().as_bytes();
    if text.len() < 2 {
        return false;
    }
    let quote = text[0];
    if !matches!(quote, b'\'' | b'"' | b'`') || text[text.len() - 1] != quote {
        return false;
    }
    quote == b'\'' || !text[1..text.len() - 1].contains(&b'$')
}

pub fn live_listeners_in_php(path: &str, root: &SyntaxNode) -> Vec<LiveListener> {
    live_listeners_in_php_impl(path, root)
}

pub fn live_listener_at_php(path: &str, root: &SyntaxNode, offset: u32) -> Option<LiveListener> {
    static MEMO: DocumentMemo<Vec<LiveListener>> = DocumentMemo::new(4);
    MEMO.get_or_compute(root, path, || live_listeners_in_php_impl(path, root))
        .iter()
        .find(|listener| {
            range_contains_cursor(listener.range, offset)
                || range_contains_cursor(listener.method_range, offset)
        })
        .cloned()
}

type LiveEventScan = (Vec<LiveEventReference>, Vec<LiveEventArgumentReference>);

/// The live event scan of `root`, memoized per tree snapshot.
fn live_event_scan_php(path: &str, root: &SyntaxNode) -> std::sync::Arc<LiveEventScan> {
    static MEMO: DocumentMemo<LiveEventScan> = DocumentMemo::new(4);
    MEMO.get_or_compute(root, path, || live_event_references_in_php_impl(path, root))
}

pub fn live_event_references_in_php(path: &str, root: &SyntaxNode) -> Vec<LiveEventReference> {
    live_event_scan_php(path, root).0.clone()
}

pub fn live_event_reference_at_php(
    path: &str,
    root: &SyntaxNode,
    offset: u32,
) -> Option<LiveEventReference> {
    live_event_scan_php(path, root)
        .0
        .iter()
        .find(|reference| {
            range_contains_cursor(reference.range, offset)
                || (reference.range.is_empty() && u32::from(reference.range.start()) == offset)
        })
        .cloned()
}

pub fn live_event_argument_references_in_php(
    path: &str,
    root: &SyntaxNode,
) -> Vec<LiveEventArgumentReference> {
    live_event_scan_php(path, root).1.clone()
}

pub fn live_event_argument_reference_at_php(
    path: &str,
    root: &SyntaxNode,
    offset: u32,
) -> Option<LiveEventArgumentReference> {
    live_event_scan_php(path, root)
        .1
        .iter()
        .find(|reference| range_contains_cursor(reference.range, offset))
        .cloned()
}

/// The event name and present data keys when `node` is inside the `data`
/// array of a `$this->emit*()` call.
pub fn live_event_argument_context_php(node: &SyntaxNode) -> Option<(String, Vec<String>)> {
    let array = phpquery::array_at(node)?;
    let call = phpquery::call_at(&array);
    live_event_call_kind(call.as_ref())?;
    let call = call?;
    let data = php_call_argument_expression(&call, &["data"], 1)
        .and_then(|expression| phpquery::array_at(&expression));
    if data.as_ref() != Some(&array) {
        return None;
    }
    let event = php_call_argument_expression(&call, &["event", "eventName"], 0)
        .and_then(|expression| phpquery::string_at(&expression));
    let name = event
        .map(|event| phpquery::string_value(&event))
        .unwrap_or_default()
        .trim()
        .to_owned();
    if name.is_empty() {
        return None;
    }
    let present = phpquery::array_items(&array)
        .iter()
        .filter_map(|item| phpquery::array_item_key(item).and_then(|key| phpquery::string_at(&key)))
        .map(|key| phpquery::string_value(&key))
        .collect();
    Some((name, present))
}

/// Whether a class carries a component attribute, and whether it is live.
pub(crate) fn component_class_kinds(class: &SyntaxNode, resolver: &NameResolver) -> (bool, bool) {
    let mut component = false;
    let mut live = false;
    for attribute in phpquery::attributes(class) {
        let resolved = resolved_name(resolver, &phpquery::attribute_name(&attribute));
        if eq_ignore_case(&resolved, AS_TWIG_COMPONENT) {
            component = true;
        } else if eq_ignore_case(&resolved, AS_LIVE_COMPONENT) {
            component = true;
            live = true;
        }
    }
    (component, live)
}

fn resolved_attribute(
    node: &SyntaxNode,
    resolver: &NameResolver,
    name: &str,
) -> Option<SyntaxNode> {
    phpquery::attributes(node).into_iter().find(|attribute| {
        eq_ignore_case(
            &resolved_name(resolver, &phpquery::attribute_name(attribute)),
            name,
        )
    })
}

fn live_prop_writable(attribute: Option<&SyntaxNode>) -> bool {
    attribute
        .and_then(|attribute| php_attribute_argument(attribute, "writable", 0))
        .is_some_and(|value| !eq_ignore_case(value.text().to_string().trim(), "false"))
}

fn php_method_name_range(method: &SyntaxNode) -> TextRange {
    named_child_range(method, &phpquery::method_name(method))
}

fn php_class_name_range(class: &SyntaxNode) -> TextRange {
    named_child_range(class, &phpquery::class_name(class))
}

fn named_child_range(node: &SyntaxNode, name: &str) -> TextRange {
    node.children()
        .find(|child| child.kind() == PHP_NAME && phpquery::name_value(child) == name)
        .map_or_else(
            || node.range_trimmed_trivia(),
            |child| child.range_trimmed_trivia(),
        )
}

/// The expression of a named argument (case-insensitive) or of the
/// `position`-th positional argument.
fn php_attribute_argument(
    attribute: &SyntaxNode,
    name: &str,
    position: usize,
) -> Option<SyntaxNode> {
    let mut positional = 0;
    for argument in phpquery::arguments(attribute) {
        let argument_name = phpquery::argument_name(&argument);
        if eq_ignore_case(&argument_name, name) {
            return php_argument_expression(&argument);
        }
        if argument_name.is_empty() {
            if positional == position {
                return php_argument_expression(&argument);
            }
            positional += 1;
        }
    }
    None
}

fn php_argument_expression(argument: &SyntaxNode) -> Option<SyntaxNode> {
    let named = argument.kind() == PHP_NAMED_ARGUMENT;
    argument
        .children()
        .find(|child| !(named && child.kind() == PHP_NAME))
}

fn php_attribute_string_value(
    attribute: &SyntaxNode,
    name: &str,
    position: usize,
) -> (String, TextRange) {
    let Some(literal) = php_attribute_argument(attribute, name, position)
        .and_then(|value| phpquery::string_at(&value))
    else {
        return (String::new(), TextRange::default());
    };
    (
        phpquery::string_value(&literal),
        string_value_range(&literal),
    )
}

pub(crate) fn exposed_method_name(name: &str) -> String {
    computed_method_name(name).unwrap_or_else(|| name.to_owned())
}

pub(crate) fn computed_method_name(name: &str) -> Option<String> {
    for prefix in ["get", "is", "has"] {
        if let Some(value) = name.strip_prefix(prefix)
            && !value.is_empty()
        {
            return Some(lower_first_byte(value));
        }
    }
    None
}

/// `value` with its first byte lowercased when it is ASCII; a multi-byte
/// first character is left as is.
fn lower_first_byte(value: &str) -> String {
    let mut result = value.to_owned();
    if let Some(first) = result.get_mut(..1) {
        first.make_ascii_lowercase();
    }
    result
}
