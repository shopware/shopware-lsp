//! Symfony environment variable hover.

use std::fmt::Write as _;
use std::sync::Arc;

use swlsp_environment::{Index, Variable};
use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_uriutil::filepath;

use crate::hover::{
    escape_markdown, hover_protocol_range, markdown_hover, request_offset, request_uri,
};

pub struct EnvironmentHoverProvider {
    root: String,
    index: Option<Arc<Index>>,
}

impl EnvironmentHoverProvider {
    pub fn new(root: impl Into<String>, index: Option<Arc<Index>>) -> Self {
        Self {
            root: root.into(),
            index,
        }
    }
}

impl HoverProvider for EnvironmentHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let Some(index) = self.index.as_deref() else {
            return Ok(None);
        };
        let (Some(line_index), Some(offset)) = (request.line_index(), request_offset(request))
        else {
            return Ok(None);
        };
        let path = swlsp_uriutil::path(request_uri(request)).unwrap_or_default();
        let current = request
            .node
            .as_ref()
            .and_then(|node| swlsp_environment::php_occurrence_at(node, offset))
            .or_else(|| swlsp_environment::occurrence_at(&path, request.source(), offset));
        let Some(current) = current else {
            return Ok(None);
        };
        let variable = index.variable(&current.name)?.unwrap_or_else(|| Variable {
            name: current.name.clone(),
            ..Variable::default()
        });
        let mut markdown = format!(
            "**Symfony environment variable** `{}`",
            escape_markdown(&current.name)
        );
        let _ = write!(
            markdown,
            "\n\n{} declaration(s) · {} reference(s)",
            variable.declarations.len(),
            variable.references.len()
        );
        if !current.processors.is_empty() {
            let _ = write!(
                markdown,
                "\n\nProcessors: `{}`",
                escape_markdown(&current.processors.join(" → "))
            );
        }
        for declaration in &variable.declarations {
            let _ = write!(
                markdown,
                "\n\n- `{}`",
                escape_markdown(&environment_relative_path(&self.root, &declaration.file))
            );
            if !declaration.value.is_empty() {
                let _ = write!(
                    markdown,
                    ": `{}`",
                    escape_markdown(&environment_display_value(
                        &current.name,
                        &declaration.value
                    ))
                );
            }
        }
        Ok(Some(markdown_hover(
            markdown,
            Some(hover_protocol_range(current.name_range, line_index)),
        )))
    }
}

pub(crate) fn environment_display_value(name: &str, value: &str) -> String {
    let upper = name.to_uppercase();
    for sensitive in [
        "SECRET",
        "PASSWORD",
        "PASSWD",
        "TOKEN",
        "KEY",
        "AUTH",
        "CREDENTIAL",
        "DSN",
        "DATABASE_URL",
        "PRIVATE_KEY",
        "API_KEY",
    ] {
        if upper.contains(sensitive) {
            return "••••••••".to_owned();
        }
    }
    if value.contains("://") && value.contains('@') {
        return "••••••••".to_owned();
    }
    value.to_owned()
}

fn environment_relative_path(root: &str, path: &str) -> String {
    if root.is_empty() {
        return path.to_owned();
    }
    match filepath::rel(root, path) {
        Ok(relative) if !relative.starts_with("..") => filepath::to_slash(&relative),
        _ => path.to_owned(),
    }
}

#[cfg(test)]
mod tests {
    use super::environment_display_value;

    #[test]
    fn environment_display_value_redacts_credential_bearing_values() {
        for (name, value) in [
            ("DATABASE_URL", "mysql://user:password@localhost/app"),
            ("MAILER_DSN", "smtp://localhost"),
            ("ADMIN_AUTH", "admin:password"),
            ("AWS_ACCESS_KEY_ID", "access-key"),
            ("SERVICE_URL", "https://token@example.com"),
        ] {
            assert_eq!(environment_display_value(name, value), "••••••••");
        }
        assert_eq!(
            environment_display_value("SERVICE_URL", "https://example.com"),
            "https://example.com"
        );
    }
}
