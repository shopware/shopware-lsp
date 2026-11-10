//! Overrides an Administration Twig
//! block in a plugin through `Shopware.Component.override`.

use std::collections::{BTreeMap, HashMap};
use std::sync::Arc;
use swlsp_ide::codeaction::nullable;

use serde::{Deserialize, Serialize};
use serde_json::Value;
use swlsp_admin::AdminComponentIndexer;
use swlsp_extension::{ExtensionIndexer, ShopwareExtension, ShopwareExtensionType};
use swlsp_lsp::{
    ActionProvider, BoxError, CodeActionRequest, CommandFunc, CommandProvider, Context,
    DocumentSnapshot, WorkspaceEditHost, add_document_replacement, resolve_optional_document,
};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind, ShopwareLspError, new_lsp_error};
use swlsp_parser_js::query as jsquery;
use swlsp_parser_js::syntax::JS_IMPORT_STATEMENT;
use swlsp_parser_twig::query as twigquery;
use swlsp_rewrite::WorkspacePlan;
use swlsp_uriutil::{file_uri, filepath};

use swlsp_ide::codeaction::is_administration_twig_path;

pub const ADMIN_TWIG_OVERRIDE_ACTION: &str = "shopware.admin.overrideTwigBlock";
pub const ADMIN_TWIG_OVERRIDE_COMMAND: &str = "shopware/admin/twig/override";

/// Keeps the Administration override workflow separate from storefront
/// template inheritance. The editor owns plugin selection while component
/// resolution and all filesystem validation remain server-side.
#[derive(Clone)]
pub struct AdminTwigOverrideProvider {
    pub(crate) host: Option<Arc<dyn WorkspaceEditHost>>,
    admin_index: Option<Arc<AdminComponentIndexer>>,
    extension_index: Option<Arc<ExtensionIndexer>>,
}

impl AdminTwigOverrideProvider {
    pub fn new(
        admin_index: Option<Arc<AdminComponentIndexer>>,
        extension_index: Option<Arc<ExtensionIndexer>>,
        host: Option<Arc<dyn WorkspaceEditHost>>,
    ) -> Self {
        Self {
            host,
            admin_index,
            extension_index,
        }
    }
}

impl ActionProvider for AdminTwigOverrideProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_EXTRACT]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        let Some(admin_index) = self.admin_index.as_ref() else {
            return Vec::new();
        };
        if ctx.is_cancelled() || request.document.is_none() {
            return Vec::new();
        }
        let (Some(node), Some(token)) = (request.node.as_ref(), request.token.as_ref()) else {
            return Vec::new();
        };
        let uri = &request.params.text_document.uri;
        let Ok(document_path) = swlsp_uriutil::path(uri) else {
            return Vec::new();
        };
        if !is_administration_twig_path(&document_path) {
            return Vec::new();
        }
        let Some(block_node) = twigquery::block_at(node) else {
            return Vec::new();
        };
        let block_name = twigquery::block_name(&block_node).unwrap_or_default();
        if block_name.is_empty() || token.text() != block_name {
            return Vec::new();
        }
        let Ok(Some(component)) =
            admin_index.get_component_registration_by_template_path(&document_path)
        else {
            return Vec::new();
        };
        if component.name.is_empty() {
            return Vec::new();
        }
        vec![protocol::CodeAction {
            title: format!(
                "Override {block_name} for Administration component {} in a plugin",
                component.name
            ),
            kind: Some(CodeActionKind::REFACTOR_EXTRACT),
            command: Some(protocol::CommandAction {
                title: "Override Administration Twig block".to_owned(),
                command: ADMIN_TWIG_OVERRIDE_ACTION.to_owned(),
                arguments: vec![Value::from(uri.clone()), Value::from(block_name)],
            }),
            ..protocol::CodeAction::default()
        }]
    }
}

impl CommandProvider for AdminTwigOverrideProvider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let provider = self.clone();
        let command: CommandFunc =
            Arc::new(
                move |ctx, args| match provider.generate_admin_twig_override(ctx, args)? {
                    Ok(response) => Ok(serde_json::to_value(response)?),
                    Err(error) => Ok(serde_json::to_value(error)?),
                },
            );
        BTreeMap::from([(ADMIN_TWIG_OVERRIDE_COMMAND.to_owned(), command)])
    }
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(default)]
pub(crate) struct AdminTwigOverrideRequest {
    #[serde(rename = "textUri", deserialize_with = "nullable")]
    pub text_uri: String,
    #[serde(rename = "blockName", deserialize_with = "nullable")]
    pub block_name: String,
    #[serde(deserialize_with = "nullable")]
    pub extension: String,
}

#[derive(Debug, Clone, Default, Serialize)]
pub(crate) struct AdminTwigOverrideResponse {
    pub edit: Option<protocol::WorkspaceEdit>,
    pub uri: String,
    pub line: i64,
    pub component: String,
    #[serde(rename = "scriptUri")]
    pub script_uri: String,
}

type Outcome<T> = Result<Result<T, ShopwareLspError>, BoxError>;

impl AdminTwigOverrideProvider {
    pub(crate) fn generate_admin_twig_override(
        &self,
        ctx: &Context,
        raw: Option<&Value>,
    ) -> Outcome<AdminTwigOverrideResponse> {
        let (Some(admin_index), Some(extension_index)) =
            (self.admin_index.as_ref(), self.extension_index.as_ref())
        else {
            return Ok(Err(new_lsp_error(
                "Administration override generator is not available",
                "admin.override.unavailable",
            )));
        };
        ctx.check()?;
        let parsed = match raw {
            None => None,
            Some(Value::Null) => Some(AdminTwigOverrideRequest::default()),
            Some(value) => serde_json::from_value::<AdminTwigOverrideRequest>(value.clone()).ok(),
        };
        let Some(mut params) = parsed else {
            return Ok(Err(new_lsp_error(
                "Invalid Administration override request",
                "admin.override.invalid_request",
            )));
        };
        params.text_uri = params.text_uri.trim().to_owned();
        params.block_name = params.block_name.trim().to_owned();
        params.extension = params.extension.trim().to_owned();
        if params.text_uri.is_empty()
            || !safe_generated_name(&params.block_name, false)
            || params.extension.is_empty()
        {
            return Ok(Err(new_lsp_error(
                "The template, block, and target plugin are required",
                "admin.override.invalid_request",
            )));
        }

        let source_path = match swlsp_uriutil::path(&params.text_uri) {
            Ok(path) if is_administration_twig_path(&path) => path,
            _ => {
                return Ok(Err(new_lsp_error(
                    "The selected block is not in an Administration Twig template",
                    "admin.override.not_administration_template",
                )));
            }
        };
        let component = admin_index
            .get_component_registration_by_template_path(&source_path)
            .map_err(|error| format!("resolve Administration component: {error}"))?;
        let Some(component) =
            component.filter(|component| safe_generated_name(&component.name, true))
        else {
            return Ok(Err(new_lsp_error(
                "No Administration component owns this Twig template",
                "admin.override.component_not_found",
            )));
        };

        let target = extension_index
            .find_by_name(&params.extension)
            .map_err(|error| format!("find target plugin: {error}"))?;
        let Some(target) = target else {
            return Ok(Err(new_lsp_error(
                "Target plugin not found",
                "extension.not_found",
            )));
        };
        if target.kind != ShopwareExtensionType::Bundle {
            return Ok(Err(new_lsp_error(
                "Administration component overrides can only be generated in plugins",
                "admin.override.plugin_required",
            )));
        }

        match generate_admin_override_files(
            ctx,
            self.host.as_deref(),
            &target,
            &component.name,
            &params.block_name,
        ) {
            Ok(result) => Ok(Ok(result)),
            Err(error) => Ok(Err(new_lsp_error(
                error.to_string(),
                "admin.override.file_conflict",
            ))),
        }
    }
}

fn generate_admin_override_files(
    ctx: &Context,
    host: Option<&dyn WorkspaceEditHost>,
    target: &ShopwareExtension,
    component_name: &str,
    block_name: &str,
) -> Result<AdminTwigOverrideResponse, BoxError> {
    let Some(host) = host else {
        return Err("workspace edit host is unavailable".into());
    };
    let mut snapshots: HashMap<String, Option<DocumentSnapshot>> = HashMap::new();
    let mut read_optional = |file: &str| -> Result<Option<String>, BoxError> {
        let snapshot = resolve_optional_document(ctx, host, &file_uri(file))?;
        let content = snapshot
            .as_ref()
            .map(|snapshot| snapshot.document.source().to_owned());
        snapshots.insert(file.to_owned(), snapshot);
        Ok(content)
    };
    let administration_source = target.administration_source_path();
    let override_directory =
        filepath::join(&[administration_source.as_str(), "extension", component_name]);
    let template_name = format!("{component_name}.html.twig");
    let template_path = filepath::join(&[override_directory.as_str(), &template_name]);
    let script_path = filepath::join(&[override_directory.as_str(), "index.js"]);

    let existing_script =
        read_optional(&script_path).map_err(|error| format!("read component override: {error}"))?;
    let script_exists = existing_script.is_some();
    let script_content = match existing_script {
        Some(content) => {
            validate_admin_override_script(&content, component_name, &format!("./{template_name}"))
                .map_err(|error| format!("existing {script_path}: {error}"))?;
            content
        }
        None => format!(
            "import template from './{template_name}';\n\n\
             Shopware.Component.override('{component_name}', {{\n    template,\n}});\n"
        ),
    };

    let existing_template =
        read_optional(&template_path).map_err(|error| format!("read Twig override: {error}"))?;
    let template_exists = existing_template.is_some();
    let mut template_changed = !template_exists;
    let mut line = 0;
    let template_content = match existing_template {
        Some(content) => {
            let parsed = swlsp_twig::parse_twig(&template_path, &content);
            if let Some(block) = parsed.blocks.get(block_name) {
                line = (block.line - 1).max(0);
                content
            } else {
                let content = append_twig_override_block(&content, block_name);
                template_changed = true;
                line = twig_block_start_line(&content, block_name);
                content
            }
        }
        None => format!("{{% block {block_name} %}}\n\n{{% endblock %}}\n"),
    };

    let (entry_path, mut entry_content, mut entry_exists) =
        administration_entry(&administration_source, &mut read_optional)?;
    let entry_import = format!("./extension/{component_name}");
    if !has_js_import(&entry_content, &entry_import) {
        entry_content = format!("import '{entry_import}';\n{entry_content}");
        entry_exists = false;
    }

    let mut plan = WorkspacePlan::default();
    for (path, content, changed) in [
        (&script_path, &script_content, !script_exists),
        (&template_path, &template_content, template_changed),
        (&entry_path, &entry_content, !entry_exists),
    ] {
        if changed {
            let snapshot = snapshots.get(path.as_str()).cloned().flatten();
            add_document_replacement(&mut plan, &file_uri(path), snapshot.as_ref(), content)?;
        }
    }
    let edit = host.workspace_edit(ctx, &plan)?;

    Ok(AdminTwigOverrideResponse {
        edit: Some(edit),
        uri: file_uri(&template_path),
        line,
        component: component_name.to_owned(),
        script_uri: file_uri(&script_path),
    })
}

fn safe_generated_name(value: &str, allow_hyphen: bool) -> bool {
    !value.is_empty()
        && value.chars().all(|character| {
            character.is_ascii_alphanumeric()
                || character == '_'
                || (allow_hyphen && character == '-')
        })
}

/// Why an existing Administration override script cannot receive the
/// generated template.
#[derive(Debug, thiserror::Error)]
enum OverrideScriptError {
    #[error("template import must point to {0:?}")]
    TemplateImport(String),
    #[error("override for {0:?} does not use template")]
    MissingTemplate(String),
    #[error("does not override component {0:?}")]
    MissingOverride(String),
}

fn validate_admin_override_script(
    content: &str,
    component_name: &str,
    template_import: &str,
) -> Result<(), OverrideScriptError> {
    let parse = swlsp_parser_js::parse(content);
    let root = parse.tree.root();
    if jsquery::import_path(&root, "template").unwrap_or_default() != template_import {
        return Err(OverrideScriptError::TemplateImport(
            template_import.to_owned(),
        ));
    }
    for call in jsquery::iter_calls(
        &root,
        &["Shopware.Component.override", "Component.override"],
    ) {
        let name = jsquery::string_argument(&call, 0)
            .map(|argument| jsquery::string_value(&argument))
            .unwrap_or_default();
        if name != component_name {
            continue;
        }
        let definition = jsquery::object_argument(&call, 1);
        if definition
            .as_ref()
            .and_then(|definition| jsquery::property(definition, "template"))
            .is_none()
        {
            return Err(OverrideScriptError::MissingTemplate(
                component_name.to_owned(),
            ));
        }
        return Ok(());
    }
    Err(OverrideScriptError::MissingOverride(
        component_name.to_owned(),
    ))
}

fn append_twig_override_block(content: &str, block_name: &str) -> String {
    let mut result = content.to_owned();
    if !result.is_empty() && !result.ends_with('\n') {
        result.push('\n');
    }
    if !result.is_empty() {
        result.push('\n');
    }
    result.push_str(&format!("{{% block {block_name} %}}\n\n{{% endblock %}}\n"));
    result
}

fn twig_block_start_line(content: &str, block_name: &str) -> i64 {
    let parsed = swlsp_twig::parse_twig("generated.html.twig", content);
    parsed
        .blocks
        .get(block_name)
        .map_or(0, |block| (block.line - 1).max(0))
}

fn administration_entry(
    administration_source: &str,
    read_optional: &mut dyn FnMut(&str) -> Result<Option<String>, BoxError>,
) -> Result<(String, String, bool), BoxError> {
    for name in ["main.js", "main.ts"] {
        let entry_path = filepath::join(&[administration_source, name]);
        let content = read_optional(&entry_path)
            .map_err(|error| format!("read Administration entry point: {error}"))?;
        if let Some(content) = content {
            return Ok((entry_path, content, true));
        }
    }
    Ok((
        filepath::join(&[administration_source, "main.js"]),
        String::new(),
        false,
    ))
}

fn has_js_import(content: &str, import_path: &str) -> bool {
    let parse = swlsp_parser_js::parse(content);
    let root = parse.tree.root();
    let wanted = filepath::clean(import_path);
    let wanted_index = filepath::clean(&format!("{import_path}/index"));
    jsquery::nodes(&root, &[JS_IMPORT_STATEMENT])
        .iter()
        .any(|statement| {
            let current = filepath::clean(&jsquery::import_path(statement, "").unwrap_or_default());
            current == wanted || current == wanted_index
        })
}

#[cfg(test)]
#[path = "tests/admin_twig_override.rs"]
mod tests;
