//! Symfony Console command, argument, and option hover.

use std::fmt::Write as _;
use std::sync::Arc;

use swlsp_console::{Index, InputKind, ReferenceRole};
use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::SyntaxNodeExt;
use swlsp_uriutil::filepath;

use crate::hover::{
    hover_protocol_range, lower_ext, markdown_hover, php_context, rel_or, request_uri,
};

pub struct ConsoleHoverProvider {
    root: String,
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
}

impl ConsoleHoverProvider {
    /// `php` supplies the request's PHP semantic context.
    pub fn new(root: impl Into<String>, index: Option<Arc<Index>>, php: Option<PhpIndex>) -> Self {
        Self {
            root: root.into(),
            index,
            php,
        }
    }
}

impl HoverProvider for ConsoleHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let Some(index) = self.index.as_ref() else {
            return Ok(None);
        };
        let Some(node) = request.node.as_ref() else {
            return Ok(None);
        };
        if lower_ext(request_uri(request)) != ".php" {
            return Ok(None);
        }
        let Some(reference) = swlsp_console::reference_at(node) else {
            return Ok(None);
        };
        let context = php_context(self.php.as_ref(), request);
        if !swlsp_console::validate_reference(context.as_ref(), &reference) {
            return Ok(None);
        }
        let mut markdown = String::new();
        if reference.role == ReferenceRole::Command {
            let commands = index.get_command(&reference.name)?;
            let Some(command) = commands.first() else {
                return Ok(None);
            };
            let _ = write!(markdown, "**Symfony command** `{}`\n\n", command.name);
            if !command.description.is_empty() {
                let _ = write!(markdown, "{}\n\n", command.description);
            }
            let mut target = command.class.clone();
            if !command.method.is_empty() {
                target.push_str("::");
                target.push_str(&command.method);
                target.push_str("()");
            }
            let _ = write!(markdown, "Handler: `{target}`");
            if !command.arguments.is_empty() || !command.options.is_empty() {
                let _ = write!(
                    markdown,
                    "\n\n{} arguments · {} options",
                    command.arguments.len(),
                    command.options.len()
                );
            }
        } else {
            let inputs = swlsp_console::inputs_for_reference(context.as_ref(), index, &reference)?;
            let Some(input) = inputs.iter().find(|input| {
                input.name == reference.name
                    || input.kind == InputKind::Option && input.shortcut == reference.name
            }) else {
                return Ok(None);
            };
            let kind = if input.kind == InputKind::Option {
                "option"
            } else {
                "argument"
            };
            let _ = writeln!(markdown, "**Symfony command {kind}** `{}`", input.name);
            if !input.shortcut.is_empty() {
                let _ = write!(markdown, "\nShortcut: `-{}`\n", input.shortcut);
            }
            if !input.description.is_empty() {
                let _ = write!(markdown, "\n{}\n", input.description);
            }
            if !input.mode.is_empty() {
                let _ = write!(markdown, "\nMode: `{}`\n", input.mode);
            }
            if !input.default.is_empty() {
                let _ = write!(markdown, "\nDefault: `{}`\n", input.default);
            }
            let display = rel_or(&self.root, &input.file);
            let _ = write!(markdown, "\nDefined in `{}`", filepath::to_slash(&display));
        }
        let Some(line_index) = request.line_index() else {
            return Ok(None);
        };
        Ok(Some(markdown_hover(
            markdown,
            Some(hover_protocol_range(
                reference.node.range_trimmed_trivia(),
                line_index,
            )),
        )))
    }
}
