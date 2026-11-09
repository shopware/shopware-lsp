//! The portable counterpart of the reference plugin's Symfony command run
//! marker. Command declarations come from the current PHP document so unsaved
//! names remain executable.

use std::collections::HashSet;

use serde_json::Value;
use swlsp_lsp::{CodeLensProvider, CodeLensRequest, Context, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_textutil::lowercase;
use swlsp_uriutil::filepath;

use crate::{by_position_title, cancelled, related_protocol_range};

pub const RUN_SYMFONY_COMMAND_ID: &str = "shopware.symfony.runConsoleCommand";

pub struct ConsoleCommandCodeLensProvider {
    root: String,
}

impl ConsoleCommandCodeLensProvider {
    pub fn new(root: impl Into<String>) -> Self {
        Self { root: root.into() }
    }
}

impl CodeLensProvider for ConsoleCommandCodeLensProvider {
    fn get_code_lenses(
        &self,
        ctx: &Context,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let document = &request.document;
        let Some(tree) = document.tree() else {
            return Ok(Vec::new());
        };
        let console = filepath::join(&[self.root.as_str(), "bin", "console"]);
        match std::fs::metadata(&console) {
            Ok(info) if !info.is_dir() => {}
            _ => return Ok(Vec::new()),
        }
        let uri = &request.params.text_document.uri;
        let path = swlsp_uriutil::path(uri)?;
        if !filepath::ext(&path).eq_ignore_ascii_case(".php") {
            return Ok(Vec::new());
        }
        let commands = swlsp_console::parse_php_commands_tree(&path, Some(tree));
        let lines = document.line_index();
        let mut result = Vec::with_capacity(commands.len());
        let mut seen = HashSet::with_capacity(commands.len());
        for command in commands {
            cancelled(ctx)?;
            if command.name.is_empty() || command.name != command.canonical {
                continue;
            }
            let key = lowercase(&format!(
                "{}\0{}\0{}",
                command.class, command.method, command.name
            ));
            if !seen.insert(key) {
                continue;
            }
            result.push(protocol::CodeLens {
                range: related_protocol_range(command.range, lines),
                command: Some(protocol::Command {
                    title: format!("Run {}", command.name),
                    command: RUN_SYMFONY_COMMAND_ID.to_owned(),
                    arguments: vec![Value::String(command.name), Value::String(uri.clone())],
                }),
                data: None,
            });
        }
        result.sort_by(by_position_title);
        Ok(result)
    }

    fn resolve_code_lens(
        &self,
        _ctx: &Context,
        lens: &protocol::CodeLens,
    ) -> ProviderResult<Option<protocol::CodeLens>> {
        Ok(Some(lens.clone()))
    }
}
