//! Framework PHP attribute completion for controllers, Twig extensions,
//! commands and Twig components.

use std::collections::HashSet;

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_CLASS_DECLARATION;
use swlsp_php::{NameResolver, PhpIndex};
use swlsp_php_languagelevel::Feature;
use swlsp_php_semantic::Snapshot;
use swlsp_syntax::{SyntaxNode, TextRange};
use swlsp_textutil::eq_ignore_case;

use super::twig_component::byte_range;
use super::twig_component_php::{
    TargetKind, component_attribute_import, component_attribute_target,
    php_attribute_replacement_range,
};
use crate::completion::common::{protocol_range, text_edit};
use crate::completion::component_util::{kind, markdown, request_offset, snippet};
use swlsp_textutil::lowercase;

pub(super) const PHP_ROUTE_ATTRIBUTE: &str = "Symfony\\Component\\Routing\\Attribute\\Route";
const PHP_IS_GRANTED_ATTRIBUTE: &str = "Symfony\\Component\\Security\\Http\\Attribute\\IsGranted";
const PHP_CACHE_ATTRIBUTE: &str = "Symfony\\Component\\HttpKernel\\Attribute\\Cache";
const PHP_AS_CONTROLLER_ATTRIBUTE: &str = "Symfony\\Component\\HttpKernel\\Attribute\\AsController";
const PHP_AS_TWIG_FILTER_ATTRIBUTE: &str = "Twig\\Attribute\\AsTwigFilter";
const PHP_AS_TWIG_FUNCTION_ATTRIBUTE: &str = "Twig\\Attribute\\AsTwigFunction";
const PHP_AS_TWIG_TEST_ATTRIBUTE: &str = "Twig\\Attribute\\AsTwigTest";
const PHP_AS_TWIG_COMPONENT_ATTRIBUTE: &str =
    "Symfony\\UX\\TwigComponent\\Attribute\\AsTwigComponent";
const PHP_AS_COMMAND_ATTRIBUTE: &str = "Symfony\\Component\\Console\\Attribute\\AsCommand";

const PHP_ABSTRACT_CONTROLLER_CLASS: &str =
    "Symfony\\Bundle\\FrameworkBundle\\Controller\\AbstractController";
const PHP_TWIG_ABSTRACT_EXTENSION_CLASS: &str = "Twig\\Extension\\AbstractExtension";
const PHP_TWIG_EXTENSION_INTERFACE: &str = "Twig\\Extension\\ExtensionInterface";
const PHP_CONSOLE_COMMAND_CLASS: &str = "Symfony\\Component\\Console\\Command\\Command";
const PHP_INPUT_INTERFACE: &str = "Symfony\\Component\\Console\\Input\\InputInterface";
const PHP_OUTPUT_INTERFACE: &str = "Symfony\\Component\\Console\\Output\\OutputInterface";

#[derive(Clone, Copy, PartialEq, Eq)]
enum ArgumentStyle {
    None,
    Quoted,
    Arguments,
}

#[derive(Clone, Copy)]
struct Spec {
    name: &'static str,
    fqn: &'static str,
    detail: &'static str,
    documentation: &'static str,
    arguments: ArgumentStyle,
}

struct EditContext {
    replace: TextRange,
    wrap_group: bool,
    close_group: bool,
    existing_args: bool,
}

pub struct PhpAttributeCompletionProvider {
    php: Option<PhpIndex>,
}

impl PhpAttributeCompletionProvider {
    pub fn new(php: Option<PhpIndex>) -> Self {
        Self { php }
    }
}

impl CompletionProvider for PhpAttributeCompletionProvider {
    fn get_completions(
        &self,
        ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        if ctx.is_cancelled() {
            return Vec::new();
        }
        let Some(php) = self.php.as_ref() else {
            return Vec::new();
        };
        let (Some(root), Some(line_index), Some(document)) = (
            request.root.as_ref(),
            request.line_index(),
            request.document.as_deref(),
        ) else {
            return Vec::new();
        };
        if !eq_ignore_case(
            swlsp_uriutil::filepath::ext(&request.params.text_document.uri),
            ".php",
        ) {
            return Vec::new();
        }
        if let Some(model) = php.project()
            && !swlsp_php_languagelevel::supports(model.php_version, Feature::Attributes)
        {
            return Vec::new();
        }
        let Some(offset) = request_offset(request) else {
            return Vec::new();
        };
        let Some(edit_context) = php_attribute_edit_at(request, offset) else {
            return Vec::new();
        };
        let (class, target) = component_attribute_target(root, request.node.as_ref(), offset);
        let Some(class) = class.filter(|class| class.kind() == PHP_CLASS_DECLARATION) else {
            return Vec::new();
        };
        let resolver = NameResolver::new(root);
        let class_fqn = php_attribute_class_fqn(root, &class);
        let snapshot = match swlsp_lsp_phpanalysis::for_document(php, document) {
            Ok(Some(state)) => state.snapshot.clone(),
            _ => php.semantic_snapshot(),
        };
        let specs = self.attribute_specs(
            ctx, php, request, offset, &class, &class_fqn, target, &resolver, &snapshot,
        );
        if specs.is_empty() {
            return Vec::new();
        }
        let mut items = Vec::with_capacity(specs.len());
        let mut seen = HashSet::new();
        for spec in specs {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            if php.find_class(spec.fqn).is_none() {
                continue;
            }
            if !seen.insert(lowercase(spec.fqn)) {
                continue;
            }
            let (qualifier, import_edit) = component_attribute_import(request, spec.fqn);
            if qualifier.is_empty() {
                continue;
            }
            let (new_text, is_snippet) =
                php_attribute_insert_text(&qualifier, spec.arguments, &edit_context);
            items.push(protocol::CompletionItem {
                label: spec.name.to_string(),
                filter_text: spec.name.to_string(),
                kind: kind(protocol::CompletionItemKind::CLASS),
                detail: format!("{} • {}", spec.detail, spec.fqn),
                text_edit: Some(text_edit(
                    protocol_range(edit_context.replace, line_index),
                    new_text,
                )),
                insert_text_format: if is_snippet { snippet() } else { None },
                documentation: markdown(spec.documentation),
                additional_text_edits: import_edit.into_iter().collect(),
                ..Default::default()
            });
        }
        items.sort_unstable_by(crate::completion::common::compare_completion_items);
        items
    }

    fn trigger_characters(&self) -> Vec<String> {
        vec!["#".to_string()]
    }
}

impl PhpAttributeCompletionProvider {
    #[allow(clippy::too_many_arguments)]
    fn attribute_specs(
        &self,
        ctx: &Context,
        php: &PhpIndex,
        request: &CompletionRequest,
        offset: u32,
        class: &SyntaxNode,
        class_fqn: &str,
        target: TargetKind,
        resolver: &NameResolver,
        snapshot: &Snapshot,
    ) -> Vec<Spec> {
        match target {
            TargetKind::Class => {
                let mut result = Vec::new();
                if php_attribute_controller_class(class, class_fqn, resolver, snapshot) {
                    result.extend(controller_class_attribute_specs());
                }
                if twig_component_candidate(ctx, php, class_fqn) {
                    result.push(Spec {
                        name: "AsTwigComponent",
                        fqn: PHP_AS_TWIG_COMPONENT_ATTRIBUTE,
                        detail: "Declare a Symfony UX Twig component",
                        documentation: "Registers this class as a Twig component.",
                        arguments: ArgumentStyle::None,
                    });
                }
                if php_attribute_command_class(class, class_fqn, resolver, snapshot) {
                    result.push(command_attribute_spec());
                }
                result
            }
            TargetKind::Method => {
                let Some(method) =
                    php_attribute_method_at_or_after(class, request.node.as_ref(), offset)
                else {
                    return Vec::new();
                };
                if !php_public_instance_method(&method) {
                    return Vec::new();
                }
                let mut result = Vec::new();
                if php_attribute_controller_class(class, class_fqn, resolver, snapshot) {
                    result.extend(controller_method_attribute_specs());
                }
                if php_attribute_twig_extension(class, class_fqn, resolver, snapshot) {
                    result.extend(twig_extension_attribute_specs());
                }
                if php_attribute_command_class(class, class_fqn, resolver, snapshot)
                    || php_attribute_console_parameters(&method, resolver)
                {
                    result.push(command_attribute_spec());
                }
                result
            }
            TargetKind::Property | TargetKind::Parameter => Vec::new(),
        }
    }
}

fn controller_method_attribute_specs() -> [Spec; 3] {
    [
        Spec {
            name: "Route",
            fqn: PHP_ROUTE_ATTRIBUTE,
            detail: "Declare a Symfony route",
            documentation: "Maps this public controller method to a route.",
            arguments: ArgumentStyle::Quoted,
        },
        Spec {
            name: "IsGranted",
            fqn: PHP_IS_GRANTED_ATTRIBUTE,
            detail: "Restrict controller access",
            documentation: "Requires the configured security attribute before invoking this controller.",
            arguments: ArgumentStyle::Quoted,
        },
        Spec {
            name: "Cache",
            fqn: PHP_CACHE_ATTRIBUTE,
            detail: "Configure HTTP response caching",
            documentation: "Configures cache headers for this controller response.",
            arguments: ArgumentStyle::Arguments,
        },
    ]
}

fn controller_class_attribute_specs() -> [Spec; 3] {
    let method = controller_method_attribute_specs();
    [
        method[0],
        Spec {
            name: "AsController",
            fqn: PHP_AS_CONTROLLER_ATTRIBUTE,
            detail: "Declare a service as a controller",
            documentation: "Marks this class as a Symfony controller service.",
            arguments: ArgumentStyle::None,
        },
        method[1],
    ]
}

fn twig_extension_attribute_specs() -> [Spec; 3] {
    [
        Spec {
            name: "AsTwigFilter",
            fqn: PHP_AS_TWIG_FILTER_ATTRIBUTE,
            detail: "Expose this method as a Twig filter",
            documentation: "Registers this public method as a Twig filter.",
            arguments: ArgumentStyle::Quoted,
        },
        Spec {
            name: "AsTwigFunction",
            fqn: PHP_AS_TWIG_FUNCTION_ATTRIBUTE,
            detail: "Expose this method as a Twig function",
            documentation: "Registers this public method as a Twig function.",
            arguments: ArgumentStyle::Quoted,
        },
        Spec {
            name: "AsTwigTest",
            fqn: PHP_AS_TWIG_TEST_ATTRIBUTE,
            detail: "Expose this method as a Twig test",
            documentation: "Registers this public method as a Twig test.",
            arguments: ArgumentStyle::Quoted,
        },
    ]
}

fn command_attribute_spec() -> Spec {
    Spec {
        name: "AsCommand",
        fqn: PHP_AS_COMMAND_ATTRIBUTE,
        detail: "Declare a Symfony console command",
        documentation: "Registers this class or invokable action as a console command.",
        arguments: ArgumentStyle::Quoted,
    }
}

fn php_attribute_edit_at(request: &CompletionRequest, offset: u32) -> Option<EditContext> {
    let source = request.source();
    if let Some(replace) = php_attribute_replacement_range(request, offset) {
        let end = (u32::from(replace.end()) as usize).min(source.len());
        let rest = &source[end..];
        let trimmed = rest.trim_start_matches([' ', '\t']);
        let existing_args = trimmed.starts_with('(');
        let mut close_group = false;
        if !existing_args {
            let line = match trimmed.find(['\r', '\n']) {
                Some(line_end) => &trimmed[..line_end],
                None => trimmed,
            };
            close_group = !line.contains(']');
        }
        return Some(EditContext {
            replace,
            wrap_group: false,
            close_group,
            existing_args,
        });
    }
    let offset_usize = offset as usize;
    if offset_usize > source.len() || !source.is_char_boundary(offset_usize) {
        return None;
    }
    let line_start = source[..offset_usize]
        .rfind('\n')
        .map_or(0, |index| index + 1);
    let line = &source[line_start..offset_usize];
    if line.trim() != "#" {
        return None;
    }
    let hash = line.rfind('#')?;
    Some(EditContext {
        replace: byte_range((line_start + hash) as u32, offset),
        wrap_group: true,
        close_group: false,
        existing_args: false,
    })
}

fn php_attribute_insert_text(
    qualifier: &str,
    arguments: ArgumentStyle,
    edit_context: &EditContext,
) -> (String, bool) {
    let mut text = qualifier.to_string();
    let mut is_snippet = false;
    if !edit_context.existing_args {
        match arguments {
            ArgumentStyle::Quoted => {
                text.push_str("('${1}')");
                is_snippet = true;
            }
            ArgumentStyle::Arguments => {
                text.push_str("(${1})");
                is_snippet = true;
            }
            ArgumentStyle::None => {}
        }
    }
    if edit_context.wrap_group {
        text = format!("#[{text}]");
    } else if edit_context.close_group {
        text.push(']');
    }
    if is_snippet {
        text.push_str("$0");
    }
    (text, is_snippet)
}

fn php_attribute_class_fqn(root: &SyntaxNode, class: &SyntaxNode) -> String {
    let name = phpquery::class_name(class);
    let namespace = phpquery::namespace(root);
    if namespace.is_empty() {
        name
    } else {
        format!("{namespace}\\{name}")
    }
}

fn php_attribute_controller_class(
    class: &SyntaxNode,
    class_fqn: &str,
    resolver: &NameResolver,
    snapshot: &Snapshot,
) -> bool {
    if phpquery::class_name(class).ends_with("Controller")
        || class_fqn.contains("\\Controller\\")
        || php_has_attribute(
            class,
            resolver,
            &[PHP_ROUTE_ATTRIBUTE, PHP_AS_CONTROLLER_ATTRIBUTE],
        )
        || snapshot.is_subtype_of(class_fqn, PHP_ABSTRACT_CONTROLLER_CLASS)
    {
        return true;
    }
    phpquery::methods(class).iter().any(|method| {
        php_public_instance_method(method)
            && php_has_attribute(method, resolver, &[PHP_ROUTE_ATTRIBUTE])
    })
}

fn php_attribute_twig_extension(
    class: &SyntaxNode,
    class_fqn: &str,
    resolver: &NameResolver,
    snapshot: &Snapshot,
) -> bool {
    if phpquery::class_name(class).ends_with("TwigExtension")
        || snapshot.is_subtype_of(class_fqn, PHP_TWIG_ABSTRACT_EXTENSION_CLASS)
        || snapshot.is_subtype_of(class_fqn, PHP_TWIG_EXTENSION_INTERFACE)
    {
        return true;
    }
    phpquery::methods(class).iter().any(|method| {
        php_public_instance_method(method)
            && php_has_attribute(
                method,
                resolver,
                &[
                    PHP_AS_TWIG_FILTER_ATTRIBUTE,
                    PHP_AS_TWIG_FUNCTION_ATTRIBUTE,
                    PHP_AS_TWIG_TEST_ATTRIBUTE,
                ],
            )
    })
}

fn php_attribute_command_class(
    class: &SyntaxNode,
    class_fqn: &str,
    resolver: &NameResolver,
    snapshot: &Snapshot,
) -> bool {
    if phpquery::class_name(class).ends_with("Command")
        || class_fqn.contains("\\Command\\")
        || snapshot.is_subtype_of(class_fqn, PHP_CONSOLE_COMMAND_CLASS)
    {
        return true;
    }
    phpquery::methods(class).iter().any(|method| {
        phpquery::method_name(method) == "__invoke"
            && php_attribute_console_parameters(method, resolver)
    })
}

fn php_attribute_console_parameters(method: &SyntaxNode, resolver: &NameResolver) -> bool {
    phpquery::parameters(method).any(|parameter| {
        phpquery::parameter_type(&parameter)
            .split(['|', '&', '?', '(', ')'])
            .filter(|candidate| !candidate.is_empty())
            .any(|candidate| {
                let resolved = resolver.resolve(candidate);
                let resolved = resolved.trim_matches('\\');
                eq_ignore_case(resolved, PHP_INPUT_INTERFACE)
                    || eq_ignore_case(resolved, PHP_OUTPUT_INTERFACE)
            })
    })
}

fn php_has_attribute(node: &SyntaxNode, resolver: &NameResolver, targets: &[&str]) -> bool {
    phpquery::attributes(node).iter().any(|attribute| {
        let resolved = resolver.resolve(&phpquery::attribute_name(attribute));
        let name = resolved.trim_matches('\\');
        targets
            .iter()
            .any(|target| eq_ignore_case(name, target.trim_matches('\\')))
    })
}

fn php_public_instance_method(method: &SyntaxNode) -> bool {
    let visibility = phpquery::declaration_visibility(method);
    if !visibility.is_empty() && visibility != "public" {
        return false;
    }
    !method
        .children_with_tokens()
        .filter_map(|element| element.into_token())
        .any(|token| eq_ignore_case(token.text(), "static"))
}

fn php_attribute_method_at_or_after(
    class: &SyntaxNode,
    node: Option<&SyntaxNode>,
    offset: u32,
) -> Option<SyntaxNode> {
    if let Some(method) = node.and_then(phpquery::method_at) {
        return Some(method);
    }
    let start = |node: &SyntaxNode| u32::from(node.text_range().start());
    let mut nearest: Option<SyntaxNode> = None;
    for method in phpquery::methods(class) {
        if start(&method) < offset {
            continue;
        }
        if nearest
            .as_ref()
            .is_none_or(|nearest| start(&method) < start(nearest))
        {
            nearest = Some(method);
        }
    }
    nearest
}

fn twig_component_candidate(ctx: &Context, php: &PhpIndex, class_fqn: &str) -> bool {
    let Some(separator) = class_fqn.rfind('\\') else {
        return false;
    };
    let namespace = &class_fqn[..separator];
    if namespace == "Components"
        || namespace.contains("\\Components\\")
        || namespace.ends_with("\\Components")
    {
        return true;
    }
    for symbol in php.class_symbols_view().iter() {
        if ctx.is_cancelled() {
            return false;
        }
        let other: &str = &symbol.fully_qualified;
        let Some(separator) = other.rfind('\\') else {
            continue;
        };
        if !eq_ignore_case(namespace, &other[..separator]) {
            continue;
        }
        if symbol.attributes().iter().any(|attribute| {
            eq_ignore_case(
                attribute.name.trim_matches('\\'),
                PHP_AS_TWIG_COMPONENT_ATTRIBUTE,
            )
        }) {
            return true;
        }
    }
    false
}
