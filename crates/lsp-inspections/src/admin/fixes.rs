//! Quick fixes of the Administration inspections.

use std::sync::Arc;

use serde::{Deserialize, Serialize};
use serde_json::Value;
use swlsp_admin::{AdminComponentIndexer, camel_to_kebab, twig_dynamic_component_selector};
use swlsp_admin_twigmigration as twigmigration;
use swlsp_lsp::{
    BoxError, CommandQuickFix, Context, FixContext, FixId, FixPresentation, FixResolution,
    QuickFix, RewriteQuickFix,
};
use swlsp_lsp_diagnostics::AdminTwigMigrationPayload;
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::ast::{AstNode, HtmlTag};
use swlsp_parser_twig::{query as twigquery, syntax as twigsyntax};
use swlsp_php_types::go_to_lower;
use swlsp_rewrite::{Builder, DocumentPlan, Edit, HandleError, WorkspacePlan};
use swlsp_syntax::{SyntaxElement, SyntaxNode, SyntaxNodeExt};

use crate::{protocol_text_range, resolve_anchor};

pub(super) const ADD_ADMIN_PROP_FIX_ID: &str = "add-admin-component-prop";
pub(super) const BIND_ADMIN_PROP_FIX_ID: &str = "bind-admin-component-prop";
pub(super) const REPLACE_ADMIN_COMPONENT_TAG_FIX_ID: &str = "replace-admin-component-tag";
pub(super) const MIGRATE_ADMIN_I18N_TC_FIX_ID: &str = "migrate-admin-vue-i18n-tc";
pub(super) const MIGRATE_ADMIN_TWIG_COMPONENT_FIX_ID: &str = "migrate-admin-twig-component";
pub(super) const MIGRATE_ADMIN_SLOT_FIX_ID: &str = "migrate-admin-vue-slot";

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub(super) struct AdminPropPayload {
    #[serde(default)]
    pub component: String,
    #[serde(default)]
    pub prop: String,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub(super) struct AdminStaticPropPayload {
    #[serde(default)]
    pub attribute: String,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub(super) struct AdminComponentTagPayload {
    #[serde(default)]
    pub component: String,
    #[serde(default)]
    pub replacement: String,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub(super) struct AdminSlotReplacement {
    #[serde(default)]
    pub replacement: String,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub(super) struct AdminI18nTcReplacement {
    #[serde(default)]
    pub replacement: String,
}

fn quick_fix(title: String) -> FixPresentation {
    FixPresentation {
        title,
        kind: Some(protocol::CodeActionKind::QUICKFIX),
        preferred: true,
        resolution: FixResolution::Eager,
    }
}

/// One document plan for the fix document.
fn single_document_plan(fix: &FixContext, edits: Vec<Edit>) -> WorkspacePlan {
    let document = &fix.document;
    WorkspacePlan {
        documents: vec![
            DocumentPlan::new(
                document.uri.clone(),
                Some(document.version),
                document.source_arc().clone(),
                edits,
            )
            .with_line_index(document.line_index_arc().clone()),
        ],
        ..WorkspacePlan::default()
    }
}

/// The resolved anchor as a node (a token anchors its parent).
fn anchor_node(fix: &FixContext) -> Result<Option<SyntaxNode>, BoxError> {
    Ok(match resolve_anchor(fix)? {
        SyntaxElement::Node(node) => Some(node),
        SyntaxElement::Token(token) => token.parent(),
    })
}

/// Go `adminComponentTagFix`: replaces a misspelled component tag (and its
/// end tag) or a dynamic component candidate.
pub(super) struct AdminComponentTagFix;

impl QuickFix for AdminComponentTagFix {
    fn id(&self) -> FixId {
        FixId::from(REPLACE_ADMIN_COMPONENT_TAG_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: AdminComponentTagPayload = fix.fix_payload()?;
        if payload.component.is_empty() || payload.replacement.is_empty() {
            return Ok(None);
        }
        Ok(Some(quick_fix(format!(
            "Replace component '{}' with '{}'",
            payload.component, payload.replacement
        ))))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for AdminComponentTagFix {
    fn build(&self, _ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: AdminComponentTagPayload = fix.fix_payload()?;
        resolve_anchor(fix)?;
        let document = &fix.document;
        let Some(root) = document.root() else {
            return Err("component template is no longer available".into());
        };
        let target_range = protocol_text_range(document.line_index(), fix.diagnostic.range)?;
        let mut builder = Builder::new(document.source());
        let mut replaced = false;
        for start_tag in twigquery::iterate_nodes(&root, &[twigsyntax::HTML_STARTING_TAG]) {
            let Some(selector) = twig_dynamic_component_selector(&start_tag) else {
                continue;
            };
            if let Some(candidate) = selector.candidates.iter().find(|candidate| {
                candidate.name == payload.component && candidate.range == target_range
            }) {
                builder.replace_range(candidate.range, payload.replacement.clone())?;
                replaced = true;
                break;
            }
        }
        if !replaced {
            for node in twigquery::iterate_nodes(&root, &[twigsyntax::HTML_TAG]) {
                let Some(tag) = HtmlTag::cast(node) else {
                    continue;
                };
                let Some(name) = tag.starting_tag().and_then(|starting| starting.name()) else {
                    continue;
                };
                if name.text() != payload.component || name.text_range() != target_range {
                    continue;
                }
                builder.replace_range(name.text_range(), payload.replacement.clone())?;
                replaced = true;
                if let Some(ending) = tag.ending_tag().and_then(|ending| ending.name())
                    && ending.text() == payload.component
                {
                    builder.replace_range(ending.text_range(), payload.replacement.clone())?;
                }
                break;
            }
        }
        if !replaced {
            return Err("component tag is no longer available".into());
        }
        let edits = builder.finish()?;
        Ok(single_document_plan(fix, edits))
    }
}

/// Go `adminStaticPropFix`: binds a static attribute with `:`.
pub(super) struct AdminStaticPropFix;

impl QuickFix for AdminStaticPropFix {
    fn id(&self) -> FixId {
        FixId::from(BIND_ADMIN_PROP_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: AdminStaticPropPayload = fix.fix_payload()?;
        if payload.attribute.is_empty() {
            return Ok(None);
        }
        Ok(Some(quick_fix(format!(
            "Bind '{}' as a Vue expression",
            payload.attribute
        ))))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for AdminStaticPropFix {
    fn build(&self, _ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: AdminStaticPropPayload = fix.fix_payload()?;
        let node = anchor_node(fix)?;
        let attribute = node.as_ref().and_then(twigquery::html_attribute_at);
        let Some(attribute) = attribute.filter(|attribute| {
            twigquery::html_attribute_name(attribute).as_deref() == Some(payload.attribute.as_str())
        }) else {
            return Err("component prop attribute is no longer available".into());
        };
        let mut builder = Builder::new(fix.document.source());
        builder.insert(attribute.range_trimmed_trivia().start().into(), ":")?;
        let edits = builder.finish()?;
        Ok(single_document_plan(fix, edits))
    }
}

/// Go `adminPropFix`: inserts a missing required prop as an editor snippet.
pub(super) struct AdminPropFix {
    pub index: Option<Arc<AdminComponentIndexer>>,
}

impl QuickFix for AdminPropFix {
    fn id(&self) -> FixId {
        FixId::from(ADD_ADMIN_PROP_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: AdminPropPayload = fix.fix_payload()?;
        if payload.component.is_empty() || payload.prop.is_empty() {
            return Ok(None);
        }
        Ok(Some(quick_fix(format!(
            "Add missing prop '{}'",
            payload.prop
        ))))
    }

    fn as_command(&self) -> Option<&dyn CommandQuickFix> {
        Some(self)
    }
}

impl CommandQuickFix for AdminPropFix {
    fn build_command(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<protocol::CommandAction>, BoxError> {
        let payload: AdminPropPayload = fix.fix_payload()?;
        let node = anchor_node(fix)?;
        let Some(start_tag) = node.as_ref().and_then(twigquery::starting_html_tag_at) else {
            return Err("component start tag is no longer available".into());
        };
        let text = start_tag.text().to_string();
        let mut offset: u32 = start_tag.range_trimmed_trivia().end().into();
        if text.ends_with("/>") {
            offset -= 2;
        } else if text.ends_with('>') {
            offset -= 1;
        }
        let (line, character) = fix.document.line_index().position_utf16(offset);
        let (attribute, value) =
            admin_prop_format(self.index.as_deref(), &payload.component, &payload.prop);
        let snippet = format!(" {attribute}=\"{value}$0\"");
        Ok(Some(protocol::CommandAction {
            title: "Add missing prop".to_owned(),
            command: "shopware.editor.insertSnippetAtPosition".to_owned(),
            arguments: vec![
                Value::String(fix.document.uri.clone()),
                Value::from(line),
                Value::from(character),
                Value::String(snippet),
            ],
        }))
    }
}

/// Go `adminPropFormat`: the attribute spelling and default value inserted
/// for a prop.
fn admin_prop_format(
    index: Option<&AdminComponentIndexer>,
    component: &str,
    prop_name: &str,
) -> (String, String) {
    let attribute = camel_to_kebab(prop_name);
    let Some(index) = index else {
        return (attribute, String::new());
    };
    let components = match index.get_component_with_definition(component) {
        Ok(components) if !components.is_empty() => components,
        _ => return (attribute, String::new()),
    };
    let Some(prop) = components[0]
        .props
        .iter()
        .find(|prop| prop.name == prop_name)
    else {
        return (attribute, String::new());
    };
    let default = |fallback: &str| {
        if prop.default.is_empty() {
            fallback.to_owned()
        } else {
            prop.default.clone()
        }
    };
    match go_to_lower(&prop.type_).as_str() {
        "boolean" => (format!(":{attribute}"), default("false")),
        "number" => (format!(":{attribute}"), default("0")),
        "array" => (format!(":{attribute}"), default("[]")),
        "object" => (format!(":{attribute}"), default("{}")),
        "function" => (format!(":{attribute}"), "() => {}".to_owned()),
        _ => (attribute, String::new()),
    }
}

/// Go `adminSlotMigrationFix`.
pub(super) struct AdminSlotMigrationFix;

impl QuickFix for AdminSlotMigrationFix {
    fn id(&self) -> FixId {
        FixId::from(MIGRATE_ADMIN_SLOT_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: AdminSlotReplacement = fix.fix_payload()?;
        if payload.replacement.is_empty() {
            return Ok(None);
        }
        Ok(Some(quick_fix("Migrate to Vue v-slot syntax".to_owned())))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for AdminSlotMigrationFix {
    fn build(&self, _ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: AdminSlotReplacement = fix.fix_payload()?;
        resolve_anchor(fix)?;
        let mut builder = Builder::new(fix.document.source());
        builder.replace_range(
            protocol_text_range(fix.document.line_index(), fix.diagnostic.range)?,
            payload.replacement,
        )?;
        let edits = builder.finish()?;
        Ok(single_document_plan(fix, edits))
    }
}

/// Go `adminI18nTCFix`.
pub(super) struct AdminI18nTcFix;

impl QuickFix for AdminI18nTcFix {
    fn id(&self) -> FixId {
        FixId::from(MIGRATE_ADMIN_I18N_TC_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: AdminI18nTcReplacement = fix.fix_payload()?;
        if payload.replacement != "$t" {
            return Ok(None);
        }
        Ok(Some(quick_fix(
            "Replace deprecated $tc() with $t()".to_owned(),
        )))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for AdminI18nTcFix {
    fn build(&self, _ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: AdminI18nTcReplacement = fix.fix_payload()?;
        if payload.replacement != "$t" {
            return Err("invalid Vue I18n replacement".into());
        }
        let element = resolve_anchor(fix)?;
        let text = match &element {
            SyntaxElement::Node(node) => node.text().to_string(),
            SyntaxElement::Token(token) => token.text().to_owned(),
        };
        if text != "$tc" {
            return Err(Box::new(HandleError::Stale));
        }
        let mut builder = Builder::new(fix.document.source());
        builder.replace(&element, payload.replacement)?;
        let edits = builder.finish()?;
        Ok(single_document_plan(fix, edits))
    }
}

/// Go `adminTwigComponentMigrationFix`.
pub(super) struct AdminTwigComponentMigrationFix;

impl QuickFix for AdminTwigComponentMigrationFix {
    fn id(&self) -> FixId {
        FixId::from(MIGRATE_ADMIN_TWIG_COMPONENT_FIX_ID)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: AdminTwigMigrationPayload = fix.fix_payload()?;
        if !payload.safe
            || payload.rule.is_empty()
            || payload.source_tag.is_empty()
            || payload.target_tag.is_empty()
        {
            return Ok(None);
        }
        Ok(Some(quick_fix(format!(
            "Migrate {} to {}",
            payload.source_tag, payload.target_tag
        ))))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for AdminTwigComponentMigrationFix {
    fn build(&self, _ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: AdminTwigMigrationPayload = fix.fix_payload()?;
        if !payload.safe {
            return Err(Box::new(twigmigration::MigrationError::Unsafe(
                "administration Twig migration is unsafe".to_owned(),
            )));
        }
        let Some(rule) = twigmigration::rule_by_id(&payload.rule).filter(|rule| {
            rule.source_tag == payload.source_tag && rule.target_tag == payload.target_tag
        }) else {
            return Err("administration Twig migration rule changed".into());
        };
        let SyntaxElement::Node(node) = resolve_anchor(fix)? else {
            return Err("administration Twig migration target is unavailable".into());
        };
        let Some(tag) = node
            .ancestors()
            .find(|current| current.kind() == twigsyntax::HTML_TAG)
        else {
            return Err("administration Twig component tag is unavailable".into());
        };
        let edits = twigmigration::compile(fix.document.source(), &tag, rule)?;
        Ok(single_document_plan(fix, edits))
    }
}
