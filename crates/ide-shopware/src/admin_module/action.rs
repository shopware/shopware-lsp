//! "Add Administration module" on the class of an entity definition: adds
//! the entity to the module description of its plugin, creating the file
//! when the plugin has none. The inspection of the description then offers
//! to generate the module.
//!
//! The action runs [`ADD_ADMIN_MODULE_COMMAND`], which answers with the
//! edit: a provider action cannot create a file.

use std::sync::Arc;

use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};
use serde_json::Value;
use swlsp_extension::ExtensionIndexer;
use swlsp_ide::codeaction::command_action;
use swlsp_language::LanguageId;
use swlsp_lsp::{
    ActionProvider, BoxError, CodeActionRequest, CommandFunc, CommandProvider, Context,
    DocumentSnapshot, WorkspaceEditHost, resolve_optional_document,
};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_CLASS_BODY;
use swlsp_parser_yaml::query as yaml;
use swlsp_rewrite::{Builder, CreateFilePlan, DocumentPlan, WorkspacePlan};
use swlsp_shopware_adminmodule::{self as adminmodule, valid_module_name};
use swlsp_shopware_dal::Index as DalIndex;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextSize};
use swlsp_uriutil::{file_uri, filepath};

use super::plugin_root;
use crate::scaffold::paths;

/// The kind the DAL index gives an entity that is neither a mapping nor a
/// translation.
const ENTITY_KIND: &str = "entity";
const INDENT: &str = "  ";

/// The client command of the code action: `(uri, className)`.
pub const ADD_ADMIN_MODULE_ACTION: &str = "shopware.addAdminModule";
/// The server command that builds the edit.
pub const ADD_ADMIN_MODULE_COMMAND: &str = "shopware/admin/module/describe";

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminModuleRequest {
    /// The file of the entity definition.
    pub uri: String,
    /// The definition class; the first entity of the file when empty.
    #[serde(rename = "className")]
    pub class_name: String,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct AdminModuleResponse {
    pub edit: Option<protocol::WorkspaceEdit>,
    /// The description.
    #[serde(rename = "primaryFileUri")]
    pub primary_file_uri: String,
    /// Whether the description is new.
    pub created: bool,
    /// The name of the module.
    pub module: String,
}

/// The module an entity gets and the description it goes to.
struct Target {
    name: String,
    entity: String,
    uri: String,
    existing: Option<DocumentSnapshot>,
    plan: WorkspacePlan,
}

#[derive(Clone)]
pub struct AdminModuleActionProvider {
    dal: Arc<DalIndex>,
    extensions: Arc<ExtensionIndexer>,
    host: Arc<dyn WorkspaceEditHost>,
}

impl AdminModuleActionProvider {
    pub fn new(
        dal: Arc<DalIndex>,
        extensions: Arc<ExtensionIndexer>,
        host: Arc<dyn WorkspaceEditHost>,
    ) -> Self {
        Self {
            dal,
            extensions,
            host,
        }
    }

    /// The description of the plugin that owns `path`.
    fn description(&self, path: &str) -> Option<String> {
        let extensions = self.extensions.get_all().ok()?;
        let owner = extensions
            .iter()
            .map(|extension| extension.root_path())
            .filter(|root| paths::rel_within(root, path).is_some())
            .max_by_key(String::len)?;
        let description = filepath::join(&[
            owner.as_str(),
            "Resources",
            "shopware-lsp",
            "admin-modules.yaml",
        ]);
        // Only where the generator finds it again.
        plugin_root(&description).map(|_| description)
    }
}

/// The name a module of `entity` gets: `acme_review` is `acme-review`.
pub(crate) fn module_name(entity: &str) -> Option<String> {
    let name = entity.replace('_', "-");
    valid_module_name(&name).then_some(name)
}

/// The module as the lines of a description, `indent` before its name.
fn module_source(indent: &str, name: &str, entity: &str) -> String {
    format!("{indent}{name}:\n{indent}{indent}entity: {entity}")
}

/// The end of the last token of `node` that is not white space.
fn content_end(node: &SyntaxNode) -> Option<TextSize> {
    node.descendants_with_tokens()
        .filter_map(|element| element.into_token())
        .filter(|token| !token.text().trim().is_empty())
        .last()
        .map(|token| token.text_range().end())
}

/// Where the module goes in an existing description and what is written
/// there; `None` when the entity has a module or the description is not
/// one this can extend.
pub(crate) fn addition(source: &str, name: &str, entity: &str) -> Option<(TextSize, String)> {
    let config = adminmodule::parse(source).ok()?;
    if config.modules.contains_key(name)
        || config
            .modules
            .values()
            .any(|module| module.entity == entity)
    {
        return None;
    }
    let tree = swlsp_parser_yaml::parse(source);
    let root = tree.root();
    let end = TextSize::of(source);
    let line_break = if source.is_empty() || source.ends_with('\n') {
        ""
    } else {
        "\n"
    };
    let appended = |text: String| Some((end, format!("{line_break}{text}\n")));
    let Some(mapping) = yaml::root_value(&root).filter(yaml::is_mapping) else {
        // Nothing but comments.
        let blank = source
            .lines()
            .all(|line| line.trim().is_empty() || line.trim_start().starts_with('#'));
        return blank
            .then(|| appended(format!("modules:\n{}", module_source(INDENT, name, entity))))
            .flatten();
    };
    let Some(pair) = yaml::property_pair(&mapping, "modules") else {
        return appended(format!("modules:\n{}", module_source(INDENT, name, entity)));
    };
    let Some(modules) = yaml::pair_value(&pair).filter(yaml::is_mapping) else {
        // `modules:` without a value.
        let empty =
            yaml::pair_value(&pair).is_none_or(|value| value.text().to_string().trim().is_empty());
        if !empty {
            return None;
        }
        return Some((
            content_end(&pair)?,
            format!("\n{}", module_source(INDENT, name, entity)),
        ));
    };
    let first = yaml::pairs(&modules).next()?;
    let key = yaml::pair_key(&first).and_then(|key| yaml::scalar_token(&key))?;
    let start = usize::from(key.text_range().start());
    let line_start = source[..start].rfind('\n').map_or(0, |index| index + 1);
    let indent = &source[line_start..start];
    // A flow mapping (`{ … }`) has no lines to add one to.
    if indent.is_empty() || !indent.chars().all(|character| character == ' ') {
        return None;
    }
    Some((
        content_end(&modules)?,
        format!("\n{}", module_source(indent, name, entity)),
    ))
}

/// The edit that adds the module to the description at `uri`.
fn plan(
    uri: &str,
    existing: Option<&DocumentSnapshot>,
    name: &str,
    entity: &str,
) -> Option<WorkspacePlan> {
    let mut plan = WorkspacePlan::default();
    let Some(snapshot) = existing else {
        plan.creates.push(CreateFilePlan {
            uri: uri.to_owned(),
            content: format!("modules:\n{}\n", module_source(INDENT, name, entity)),
        });
        return Some(plan);
    };
    let document = &snapshot.document;
    let (offset, text) = addition(document.source(), name, entity)?;
    let mut builder = Builder::new(document.source());
    builder.insert(u32::from(offset), text).ok()?;
    plan.documents.push(
        DocumentPlan::new(
            uri,
            snapshot.version,
            document.source_arc().clone(),
            builder.finish().ok()?,
        )
        .with_line_index(document.line_index_arc().clone()),
    );
    Some(plan)
}

impl AdminModuleActionProvider {
    /// What the entity `class_name` (any entity when empty) of the file at
    /// `path` adds to the description of its plugin; `None` when there is
    /// nothing to add.
    fn target(&self, ctx: &Context, path: &str, class_name: &str) -> Option<Target> {
        let definition = self
            .dal
            .definitions_in_file(path)
            .unwrap_or_default()
            .into_iter()
            .find(|definition| {
                definition.kind == ENTITY_KIND
                    && (class_name.is_empty() || definition.class == class_name)
            })?;
        let name = module_name(&definition.name)?;
        let uri = file_uri(&self.description(path)?);
        let existing = resolve_optional_document(ctx, self.host.as_ref(), &uri).ok()?;
        let plan = plan(&uri, existing.as_ref(), &name, &definition.name)?;
        Some(Target {
            name,
            entity: definition.name,
            uri,
            existing,
            plan,
        })
    }

    pub fn describe(
        &self,
        ctx: &Context,
        args: Option<&Value>,
    ) -> Result<AdminModuleResponse, BoxError> {
        ctx.check()?;
        let request: AdminModuleRequest = crate::scaffold::decode_args(
            args,
            "missing Administration module request",
            "invalid Administration module request",
        )?;
        let path = filepath::clean(&swlsp_uriutil::path(&request.uri)?);
        // The class may be named with its namespace.
        let class_name = request.class_name.rsplit('\\').next().unwrap_or_default();
        let target = self.target(ctx, &path, class_name).ok_or(
            "no entity of a plugin without an Administration module was found in the file",
        )?;
        ctx.check()?;
        Ok(AdminModuleResponse {
            edit: Some(self.host.workspace_edit(ctx, &target.plan)?),
            primary_file_uri: target.uri,
            created: target.existing.is_none(),
            module: target.name,
        })
    }
}

impl ActionProvider for AdminModuleActionProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_REWRITE]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        let (Some(document), Some(node)) = (request.document.as_ref(), request.node.as_ref())
        else {
            return Vec::new();
        };
        if document.language != Some(LanguageId::Php) {
            return Vec::new();
        }
        let Some(class) = phpquery::class_at(node) else {
            return Vec::new();
        };
        // Offered on the head of the class, not on every line of its body.
        let in_body = phpquery::direct_child(&class, PHP_CLASS_BODY).is_some_and(|body| {
            node.range_trimmed_trivia().start() >= body.range_trimmed_trivia().start()
        });
        if in_body {
            return Vec::new();
        }
        let Ok(path) = swlsp_uriutil::path(&document.uri) else {
            return Vec::new();
        };
        let class_name = phpquery::class_name(&class);
        let Some(target) = self.target(ctx, &filepath::clean(&path), &class_name) else {
            return Vec::new();
        };
        vec![command_action(
            &format!(
                "Shopware: Add Administration module {} for {}",
                target.name, target.entity
            ),
            CodeActionKind::REFACTOR_REWRITE,
            "Add Administration module",
            ADD_ADMIN_MODULE_ACTION,
            vec![Value::from(document.uri.clone()), Value::from(class_name)],
        )]
    }
}

impl CommandProvider for AdminModuleActionProvider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let provider = self.clone();
        let describe: CommandFunc =
            Arc::new(move |ctx, args| Ok(serde_json::to_value(provider.describe(ctx, args)?)?));
        BTreeMap::from([(ADD_ADMIN_MODULE_COMMAND.to_owned(), describe)])
    }
}

#[cfg(test)]
#[path = "action_tests.rs"]
mod tests;
