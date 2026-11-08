//! Hover for parameters passed to included Twig templates.

use std::sync::Arc;

use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_twig::TwigIndexer;
use swlsp_uriutil::filepath;

use crate::{hover_protocol_range, markdown_hover, request_offset, request_uri};

pub struct TwigIncludeParameterHoverProvider {
    project_root: String,
    twig: Option<Arc<TwigIndexer>>,
    php: Option<PhpIndex>,
}

impl TwigIncludeParameterHoverProvider {
    pub fn new(
        project_root: impl Into<String>,
        twig: Option<Arc<TwigIndexer>>,
        php: Option<PhpIndex>,
    ) -> Self {
        Self {
            project_root: project_root.into(),
            twig,
            php,
        }
    }

    fn display_path(&self, path: &str) -> String {
        match filepath::rel(&self.project_root, path) {
            Ok(relative) => filepath::to_slash(&relative),
            Err(_) => filepath::to_slash(path),
        }
    }
}

impl HoverProvider for TwigIncludeParameterHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let Some(twig) = self.twig.as_deref() else {
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
        let Some(parameter) = swlsp_twig::include_parameter_at(root, request.node.as_ref(), offset)
        else {
            return Ok(None);
        };
        let variables = twig.find_template_variable(&parameter.name, &[&parameter.template])?;
        let mut php_type = String::new();
        let mut php_file = String::new();
        if let Some(php) = self.php.as_ref() {
            for variable in php.twig_template_variables(&[&parameter.template])? {
                if variable.name == parameter.name {
                    php_type = variable.ty;
                    php_file = variable.file;
                    break;
                }
            }
        }
        if variables.is_empty() && php_type.is_empty() {
            return Ok(None);
        }
        let mut value = format!(
            "**Twig include parameter** `{}`\n\nTarget template: `{}`",
            parameter.name, parameter.template
        );
        if !php_type.is_empty() {
            value.push_str(&format!("\n\nPHP type: `{php_type}`"));
        }
        if let Some(variable) = variables.first() {
            value.push_str(&format!(
                "\n\nRead in `{}`.",
                self.display_path(&variable.file_path)
            ));
        } else if !php_file.is_empty() {
            value.push_str(&format!(
                "\n\nProvided by `{}`.",
                self.display_path(&php_file)
            ));
        }
        Ok(Some(markdown_hover(
            value,
            Some(hover_protocol_range(parameter.range, line_index)),
        )))
    }
}
