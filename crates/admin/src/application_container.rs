//! `Shopware.Application.getContainer(...)` members.

use crate::*;

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ApplicationContainer {
    pub name: String,
    pub interface_name: String,
    pub description: String,
}

const APPLICATION_CONTAINERS: &[(&str, &str, &str)] = &[
    ("factory", "FactoryContainer", "Administration factories"),
    ("service", "ServiceContainer", "Administration services"),
    ("init", "InitContainer", "Administration initializers"),
    (
        "init-pre",
        "InitPreContainer",
        "pre-initialization services",
    ),
    (
        "init-post",
        "InitPostContainer",
        "post-initialization services",
    ),
];

fn application_container(entry: &(&str, &str, &str)) -> ApplicationContainer {
    ApplicationContainer {
        name: entry.0.to_owned(),
        interface_name: entry.1.to_owned(),
        description: entry.2.to_owned(),
    }
}

/// The statically supported `getContainer` names.
pub fn application_containers() -> Vec<ApplicationContainer> {
    APPLICATION_CONTAINERS
        .iter()
        .map(application_container)
        .collect()
}

pub fn application_container_named(name: &str) -> Option<ApplicationContainer> {
    APPLICATION_CONTAINERS
        .iter()
        .find(|entry| entry.0 == name)
        .map(application_container)
}

/// Whether the cursor is in the static name argument of
/// `Application.getContainer(...)`.
pub fn is_application_container_name_reference(node: &SyntaxNode) -> bool {
    if jsquery::string_at(node).is_none() || jsquery::string_argument_index(node) != Some(0) {
        return false;
    }
    is_application_container_call_name(&jsquery::call_name(node).unwrap_or_default())
}

/// A direct member accessed from `Application.getContainer(...)`; returns
/// the container and member names.
pub fn javascript_application_container_member(node: &SyntaxNode) -> Option<(String, String)> {
    javascript_application_container_member_impl(node, None)
}

pub(crate) fn javascript_application_container_member_impl(
    node: &SyntaxNode,
    analysis: Option<&JavaScriptDocumentAnalysis>,
) -> Option<(String, String)> {
    let member = jsquery::member_expression_at(node)?;
    let mut children = member.children();
    let receiver = children.next()?;
    if *node != member && javascript_node_within(node, &receiver) {
        return None;
    }
    let container_name = application_container_receiver_name(&receiver, analysis)?;
    let mut member_name = String::new();
    for child in children {
        if child.kind() == jssyntax::JS_IDENTIFIER {
            member_name = child.text().to_string().trim().to_owned();
        }
    }
    Some((container_name, member_name))
}

/// The identifier owned by a recognized direct container-member access.
pub fn javascript_application_container_member_name_node(node: &SyntaxNode) -> Option<SyntaxNode> {
    javascript_application_container_member_name_node_impl(node, None)
}

pub(crate) fn javascript_application_container_member_name_node_impl(
    node: &SyntaxNode,
    analysis: Option<&JavaScriptDocumentAnalysis>,
) -> Option<SyntaxNode> {
    let (_, name) = javascript_application_container_member_impl(node, analysis)?;
    if name.is_empty() {
        return None;
    }
    let member = jsquery::member_expression_at(node)?;
    let mut result = None;
    for child in member.children().skip(1) {
        if child.kind() == jssyntax::JS_IDENTIFIER && child.text().to_string().trim() == name {
            result = Some(child);
        }
    }
    result
}

pub(crate) fn javascript_node_within(node: &SyntaxNode, ancestor: &SyntaxNode) -> bool {
    node.ancestors().any(|current| current == *ancestor)
}

fn application_container_receiver_name(
    receiver: &SyntaxNode,
    analysis: Option<&JavaScriptDocumentAnalysis>,
) -> Option<String> {
    if receiver.kind() == jssyntax::JS_CALL_EXPRESSION {
        return application_container_call_value(receiver);
    }
    if receiver.kind() != jssyntax::JS_IDENTIFIER {
        return None;
    }
    let identifier = jsquery::identifier_text(receiver).unwrap_or_default();
    if identifier.is_empty() {
        return None;
    }
    let root = receiver
        .ancestors()
        .last()
        .unwrap_or_else(|| receiver.clone());
    let initializer =
        visible_javascript_const_initializer_indexed(receiver, &identifier, &root, analysis)?;
    let parsed = swlsp_parser_js::parse(&initializer).root();
    jsquery::iter_calls(&parsed, &[]).find_map(|call| application_container_call_value(&call))
}

pub(crate) fn application_container_const_alias_names(
    root: &SyntaxNode,
    analysis: Option<&JavaScriptDocumentAnalysis>,
) -> HashSet<String> {
    let mut result = HashSet::new();
    let declarations = match analysis {
        Some(analysis) => analysis.nodes(&[jssyntax::JS_VARIABLE_DECLARATION]),
        None => jsquery::nodes(root, &[jssyntax::JS_VARIABLE_DECLARATION]),
    };
    for declaration in &declarations {
        let initializer = match analysis {
            Some(analysis) => analysis.const_initializer(declaration),
            None => direct_component_const_initializer(&declaration.text().to_string()),
        };
        let Some((name, initializer)) = initializer else {
            continue;
        };
        if !initializer.contains("getContainer") {
            continue;
        }
        let parsed = swlsp_parser_js::parse(&initializer).root();
        if jsquery::iter_calls(&parsed, &[])
            .any(|call| application_container_call_value(&call).is_some())
        {
            result.insert(name);
        }
    }
    result
}

pub(crate) fn potential_application_container_member(
    member: &SyntaxNode,
    aliases: &HashSet<String>,
) -> bool {
    if member.kind() != jssyntax::JS_MEMBER_EXPRESSION {
        return false;
    }
    let Some(receiver) = member.children().next() else {
        return false;
    };
    if receiver.kind() == jssyntax::JS_CALL_EXPRESSION {
        return is_application_container_call_name(
            &jsquery::call_name(&receiver).unwrap_or_default(),
        );
    }
    receiver.kind() == jssyntax::JS_IDENTIFIER
        && aliases.contains(&jsquery::identifier_text(&receiver).unwrap_or_default())
}

pub(crate) fn application_container_call_value(call: &SyntaxNode) -> Option<String> {
    if !is_application_container_call_name(&jsquery::call_name(call).unwrap_or_default()) {
        return None;
    }
    let name = jsquery::string_argument(call, 0)
        .map(|literal| jsquery::string_value(&literal))
        .unwrap_or_default();
    application_container_named(&name)?;
    Some(name)
}

pub(crate) fn is_application_container_call_name(name: &str) -> bool {
    matches!(
        name,
        "Application.getContainer"
            | "Shopware.Application.getContainer"
            | "this.Application.getContainer"
            | "this.getContainer"
    )
}

impl AdminComponentIndexer {
    /// Composes all ambient declarations for a Shopware container, merging
    /// TypeScript declaration-merged interfaces.
    pub fn resolve_application_container(
        &self,
        container_name: &str,
        _context_path: &str,
    ) -> IndexResult<VueTypeShape> {
        let Some(container) = application_container_named(container_name) else {
            return Ok(VueTypeShape::default());
        };
        let mut result = VueTypeShape {
            type_: container.interface_name.clone(),
            complete: true,
            ..Default::default()
        };
        let live_files = self.live_type_file_overlays(&[]);
        let files = self.all_admin_type_files(&live_files)?;
        let mut declarations = 0;
        let mut seen_declarations = HashSet::new();
        for file in &files {
            let path = filepath::clean(&file.file_path);
            if path == "." {
                continue;
            }
            for (declaration_index, declaration) in file.declarations.iter().enumerate() {
                if declaration.name != container.interface_name || !declaration.interface {
                    continue;
                }
                if !seen_declarations.insert(format!("{path}\x00{declaration_index}")) {
                    continue;
                }
                declarations += 1;
                let mut overlay_declarations = vec![declaration.clone()];
                for (other_index, other) in file.declarations.iter().enumerate() {
                    if other_index == declaration_index || other.name == container.interface_name {
                        continue;
                    }
                    overlay_declarations.push(other.clone());
                }
                let shape = self.resolve_vue_type(
                    &container.interface_name,
                    &file.file_path,
                    &[AdminTypeFile {
                        file_path: file.file_path.clone(),
                        imports: file.imports.clone(),
                        declarations: overlay_declarations,
                    }],
                )?;
                result.members = merge_twig_vue_members(&result.members, &shape.members);
                result.complete = result.complete && shape.complete;
            }
        }
        if declarations == 0 {
            result.complete = false;
            result.members = application_container_base_members(container_name);
        }
        // Runtime registrations supplement projects which have not augmented
        // ServiceContainer; typed declarations remain authoritative.
        if container_name == "service" {
            for service in self.get_all_services()? {
                result.members = merge_twig_vue_members(
                    &result.members,
                    &[TwigVueMember {
                        name: service.name.clone(),
                        type_: service.implementation_name.clone(),
                        definition_path: service.file_path.clone(),
                        definition_line: service.line,
                        ..Default::default()
                    }],
                );
            }
            // Plugins can register services at runtime.
            result.complete = false;
        }
        result
            .members
            .sort_by(|left, right| left.name.cmp(&right.name));
        Ok(result)
    }
}

pub(crate) fn application_container_base_members(container_name: &str) -> Vec<TwigVueMember> {
    let member = |name: &str, type_: String| TwigVueMember {
        name: name.to_owned(),
        type_,
        ..Default::default()
    };
    vec![
        member(
            "$decorator",
            "(name: string, decorator: Function) => unknown".to_owned(),
        ),
        member("$list", format!("() => (keyof {container_name})[]")),
        member("$register", "(object: unknown) => unknown".to_owned()),
    ]
}
