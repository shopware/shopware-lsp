//! Symfony UX Twig component, prop, block, computed, and Live Action hover.

use std::fmt::Write as _;
use std::sync::Arc;

use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php_types::equal_fold;
use swlsp_syntax::{LineIndex, SyntaxNode, TextRange};
use swlsp_twigcomponent::{
    ComponentBlockUsage, Index, LiveActionArgumentReference, LiveActionReference, Prop, PropUsage,
    SourceKind,
};
use swlsp_uriutil::filepath;

use crate::{escape_markdown, hover_protocol_range, markdown_hover, request_offset, request_uri};

type HoverResult = ProviderResult<Option<protocol::Hover>>;

pub struct TwigComponentHoverProvider {
    root: String,
    index: Option<Arc<Index>>,
}

impl TwigComponentHoverProvider {
    pub fn new(root: impl Into<String>, index: Option<Arc<Index>>) -> Self {
        Self {
            root: root.into(),
            index,
        }
    }

    fn display(&self, path: &str) -> String {
        match filepath::rel(&self.root, path) {
            Ok(relative) => relative,
            Err(_) => path.to_owned(),
        }
    }

    fn live_action_argument_hover(
        &self,
        index: &Index,
        path: &str,
        reference: &LiveActionArgumentReference,
        line_index: &LineIndex,
    ) -> HoverResult {
        for action in index.live_actions_for_template(path)? {
            if !equal_fold(&action.name, &reference.action) {
                continue;
            }
            for parameter in &action.parameters {
                if !equal_fold(&parameter.name, &reference.name) {
                    continue;
                }
                let mut value = format!(
                    "**Live Action argument** `{}`\n\nAction: `{}::{}()`",
                    escape_markdown(&parameter.name),
                    escape_markdown(&action.class),
                    escape_markdown(&action.method)
                );
                if !parameter.ty.is_empty() {
                    let _ = write!(value, "\n\nPHP type: `{}`", escape_markdown(&parameter.ty));
                }
                if parameter.name != parameter.php_name {
                    let _ = write!(
                        value,
                        "\n\nMapped to PHP parameter `${}` via `#[LiveArg]`",
                        escape_markdown(&parameter.php_name)
                    );
                }
                return Ok(Some(markdown_hover(
                    value,
                    Some(hover_protocol_range(reference.range, line_index)),
                )));
            }
        }
        Ok(None)
    }

    fn live_action_hover(
        &self,
        index: &Index,
        path: &str,
        reference: &LiveActionReference,
        line_index: &LineIndex,
    ) -> HoverResult {
        for action in index.live_actions_for_template(path)? {
            if !equal_fold(&action.name, &reference.name) {
                continue;
            }
            let mut signature = format!("{}::{}(", action.class, action.method);
            for (position, parameter) in action.parameters.iter().enumerate() {
                if position != 0 {
                    signature.push_str(", ");
                }
                if !parameter.ty.is_empty() {
                    signature.push_str(&parameter.ty);
                    signature.push(' ');
                }
                let php_name = if parameter.php_name.is_empty() {
                    &parameter.name
                } else {
                    &parameter.php_name
                };
                signature.push('$');
                signature.push_str(php_name);
                if parameter.optional {
                    signature.push_str(" = …");
                }
            }
            signature.push(')');
            return Ok(Some(markdown_hover(
                format!(
                    "**Symfony UX Live Action** `{}`\n\n```php\n{}\n```",
                    escape_markdown(&action.name),
                    signature
                ),
                Some(hover_protocol_range(reference.range, line_index)),
            )));
        }
        Ok(None)
    }

    fn block_hover(
        &self,
        index: &Index,
        usage: &ComponentBlockUsage,
        line_index: &LineIndex,
    ) -> HoverResult {
        for block in index.blocks(&usage.component)? {
            if block.name != usage.name {
                continue;
            }
            let display = self.display(&block.file);
            return Ok(Some(markdown_hover(
                format!(
                    "**Twig component block** `{}.{}`\n\nDeclared in `{}`",
                    escape_markdown(&usage.component),
                    escape_markdown(&usage.name),
                    escape_markdown(&filepath::to_slash(&display))
                ),
                Some(hover_protocol_range(usage.range, line_index)),
            )));
        }
        Ok(None)
    }

    fn variable_hover(
        &self,
        index: &Index,
        path: &str,
        root: &SyntaxNode,
        name: &str,
        range: TextRange,
        line_index: &LineIndex,
    ) -> HoverResult {
        let (components, props) = index.context_for_template(path, Some(root))?;
        if components.is_empty() {
            return Ok(None);
        }
        if name == "this" {
            for component in &components {
                if component.class.is_empty() {
                    continue;
                }
                return Ok(Some(markdown_hover(
                    format!(
                        "**Twig component object** `this`\n\nPHP type: `{}`",
                        escape_markdown(&component.class)
                    ),
                    Some(hover_protocol_range(range, line_index)),
                )));
            }
            return Ok(None);
        }
        if name == "computed" {
            return Ok(Some(markdown_hover(
                "**Twig computed proxy** `computed`\n\nProvides cached access to zero-argument component getters.".to_owned(),
                Some(hover_protocol_range(range, line_index)),
            )));
        }
        for prop in &props {
            if !equal_fold(&prop.name, name) {
                continue;
            }
            return Ok(Some(component_variable_prop_hover(prop, range, line_index)));
        }
        Ok(None)
    }

    fn computed_hover(
        &self,
        index: &Index,
        path: &str,
        name: &str,
        range: TextRange,
        line_index: &LineIndex,
    ) -> HoverResult {
        for prop in index.computed_for_template(path)? {
            if !equal_fold(&prop.name, name) {
                continue;
            }
            let mut value = format!(
                "**Computed component value** `{}`",
                escape_markdown(&prop.name)
            );
            if !prop.ty.is_empty() {
                let _ = write!(value, "\n\nPHP type: `{}`", escape_markdown(&prop.ty));
            }
            value.push_str("\n\nThe getter result is cached for this component render.");
            return Ok(Some(markdown_hover(
                value,
                Some(hover_protocol_range(range, line_index)),
            )));
        }
        Ok(None)
    }

    fn prop_hover(&self, index: &Index, usage: &PropUsage, line_index: &LineIndex) -> HoverResult {
        for prop in index.props(&usage.component)? {
            if !equal_fold(&prop.name, &usage.name) {
                continue;
            }
            let mut markdown = format!(
                "**Twig component prop** `{}.{}`",
                escape_markdown(&usage.component),
                escape_markdown(&prop.name)
            );
            write_prop_details(&mut markdown, &prop, "Type");
            return Ok(Some(markdown_hover(
                markdown,
                Some(hover_protocol_range(usage.range, line_index)),
            )));
        }
        Ok(None)
    }
}

fn write_prop_details(markdown: &mut String, prop: &Prop, type_label: &str) {
    if !prop.ty.is_empty() {
        let _ = write!(
            markdown,
            "\n\n{type_label}: `{}`",
            escape_markdown(&prop.ty)
        );
    }
    if !prop.default_value.is_empty() {
        let _ = write!(
            markdown,
            "\n\nDefault: `{}`",
            escape_markdown(&prop.default_value)
        );
    }
    if !prop.description.is_empty() {
        let _ = write!(markdown, "\n\n{}", prop.description);
    }
    if prop.live {
        if prop.writable {
            markdown.push_str("\n\nLive prop: writable from the browser.");
        } else {
            markdown.push_str("\n\nLive prop: read-only.");
        }
    }
}

fn component_variable_prop_hover(
    prop: &Prop,
    range: TextRange,
    line_index: &LineIndex,
) -> protocol::Hover {
    let mut markdown = format!(
        "**Twig component variable** `{}`",
        escape_markdown(&prop.name)
    );
    write_prop_details(&mut markdown, prop, "PHP/Twig type");
    markdown_hover(markdown, Some(hover_protocol_range(range, line_index)))
}

impl HoverProvider for TwigComponentHoverProvider {
    fn get_hover(&self, _ctx: &Context, request: &HoverRequest) -> HoverResult {
        let Some(index) = self.index.as_deref() else {
            return Ok(None);
        };
        let (Some(root), Some(line_index), Some(offset)) = (
            request.root.as_ref(),
            request.line_index(),
            request_offset(request),
        ) else {
            return Ok(None);
        };
        if !request_uri(request).to_lowercase().ends_with(".twig") {
            return Ok(None);
        }
        let path = swlsp_uriutil::path(request_uri(request)).unwrap_or_default();
        if let Some(argument) =
            swlsp_twigcomponent::live_action_argument_reference_at(&path, root, offset)
        {
            let result = self.live_action_argument_hover(index, &path, &argument, line_index)?;
            if result.is_some() {
                return Ok(result);
            }
        }
        if let Some(action) = swlsp_twigcomponent::live_action_reference_at(&path, root, offset)
            && !action.name.is_empty()
        {
            let result = self.live_action_hover(index, &path, &action, line_index)?;
            if result.is_some() {
                return Ok(result);
            }
        }
        if let Some(node) = request.node.as_ref() {
            if let Some((accessor_root, member, range)) =
                swlsp_twigcomponent::accessor_member_at(node)
                && accessor_root == "computed"
            {
                let result = self.computed_hover(index, &path, &member, range, line_index)?;
                if result.is_some() {
                    return Ok(result);
                }
            }
            if let Some((name, range)) = swlsp_twigcomponent::variable_at(node) {
                let result = self.variable_hover(index, &path, root, &name, range, line_index)?;
                if result.is_some() {
                    return Ok(result);
                }
            }
            if let Some(block) = swlsp_twigcomponent::block_usage_at(node, offset) {
                return self.block_hover(index, &block, line_index);
            }
        }
        let Some(usage) = swlsp_twigcomponent::usage_at(&path, root, offset) else {
            let Some(node) = request.node.as_ref() else {
                return Ok(None);
            };
            let Some(prop) = swlsp_twigcomponent::prop_usage_at(root, node, offset) else {
                return Ok(None);
            };
            return self.prop_hover(index, &prop, line_index);
        };
        let components = index.find(&usage.name)?;
        if components.is_empty() {
            return Ok(None);
        }
        let mut classes: Vec<String> = Vec::new();
        let mut templates: Vec<String> = Vec::new();
        let mut template_methods: Vec<String> = Vec::new();
        let mut compiled = false;
        for component in &components {
            compiled = compiled || component.source == SourceKind::CompiledContainer;
            if !component.class.is_empty() && !classes.contains(&component.class) {
                classes.push(component.class.clone());
            }
            if !component.template.is_empty() && !templates.contains(&component.template) {
                templates.push(component.template.clone());
            }
            if !component.template_from_method.is_empty()
                && !template_methods.contains(&component.template_from_method)
            {
                template_methods.push(component.template_from_method.clone());
            }
        }
        classes.sort();
        templates.sort();
        template_methods.sort();
        let props = index.props(&usage.name).unwrap_or_default();
        let usages = index.usages(&usage.name).unwrap_or_default();

        let mut markdown = format!("**Twig component** `{}`", escape_markdown(&usage.name));
        if components.iter().any(|component| component.live) {
            markdown.push_str("\n\nLive component");
        }
        if compiled {
            markdown.push_str("\n\nRuntime metadata: compiled Symfony container");
        }
        for class in &classes {
            let _ = write!(markdown, "\n\nPHP class: `{}`", escape_markdown(class));
        }
        for template in &templates {
            let _ = write!(markdown, "\n\nTemplate: `{}`", escape_markdown(template));
        }
        for method in &template_methods {
            let _ = write!(
                markdown,
                "\n\nDynamic template method: `{}`",
                escape_markdown(method)
            );
        }
        if !props.is_empty() {
            let _ = write!(markdown, "\n\n{} prop(s)", props.len());
        }
        if !usages.is_empty() {
            let _ = write!(markdown, "\n\n{} indexed use(s)", usages.len());
        }
        if components.len() == 1 && !components[0].file.is_empty() {
            let display = self.display(&components[0].file);
            let _ = write!(
                markdown,
                "\n\nDiscovered from `{}`",
                escape_markdown(&filepath::to_slash(&display))
            );
        }
        Ok(Some(markdown_hover(
            markdown,
            Some(hover_protocol_range(usage.range, line_index)),
        )))
    }
}
