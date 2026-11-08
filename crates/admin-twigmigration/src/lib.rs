//! Versioned Administration Twig component migrations (port of Go
//! `internal/admin/twigmigration`).
//!
//! Immutable Twig CST nodes are compiled to lossless source edits; callers
//! decide whether to expose those edits as an LSP quick fix.

mod entities;
mod html;

use std::collections::HashMap;
use std::fmt;

use swlsp_parser_twig::ast::{AstNode, HtmlAttribute, HtmlStartingTag, HtmlTag, TwigVar};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{HTML_ATTRIBUTE_LIST, HTML_TEXT, TWIG_VAR};
use swlsp_rewrite::{Builder, Edit, RewriteError};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};

pub use html::unescape_string;

/// Why a migration could not be compiled.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum MigrationError {
    /// Go `ErrUnsafe`: the legacy API carries behavior without a
    /// deterministic target equivalent.
    Unsafe(String),
    /// The edits could not be built.
    Rewrite(RewriteError),
    /// The node or rule does not describe a valid migration.
    Invalid(String),
}

impl MigrationError {
    /// Go `errors.Is(err, ErrUnsafe)`.
    pub fn is_unsafe(&self) -> bool {
        matches!(self, Self::Unsafe(_))
    }
}

impl fmt::Display for MigrationError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Unsafe(reason) => write!(
                f,
                "administration Twig migration is not safely automatable: {reason}"
            ),
            Self::Rewrite(error) => error.fmt(f),
            Self::Invalid(message) => f.write_str(message),
        }
    }
}

impl std::error::Error for MigrationError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::Rewrite(error) => Some(error),
            _ => None,
        }
    }
}

impl From<RewriteError> for MigrationError {
    fn from(error: RewriteError) -> Self {
        Self::Rewrite(error)
    }
}

pub type Result<T, E = MigrationError> = std::result::Result<T, E>;

fn unsafe_(reason: impl Into<String>) -> MigrationError {
    MigrationError::Unsafe(reason.into())
}

/// One shopware-cli Administration Twig migration.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct Rule {
    pub id: &'static str,
    pub source_tag: &'static str,
    pub target_tag: &'static str,
    pub message: &'static str,
}

const fn rule(
    id: &'static str,
    source_tag: &'static str,
    target_tag: &'static str,
    message: &'static str,
) -> Rule {
    Rule {
        id,
        source_tag,
        target_tag,
        message,
    }
}

static RULES: &[Rule] = &[
    rule(
        "alert",
        "sw-alert",
        "mt-banner",
        "sw-alert is removed; use mt-banner and review variant changes",
    ),
    rule(
        "button",
        "sw-button",
        "mt-button",
        "sw-button is removed; use mt-button and review variant and router-link behavior",
    ),
    rule(
        "card",
        "sw-card",
        "mt-card",
        "sw-card is removed; use mt-card and review aiBadge and contentPadding",
    ),
    rule(
        "checkbox-field",
        "sw-checkbox-field",
        "mt-checkbox",
        "sw-checkbox-field is removed; use mt-checkbox and review props, events, and slots",
    ),
    rule(
        "colorpicker",
        "sw-colorpicker",
        "mt-colorpicker",
        "sw-colorpicker is removed; use mt-colorpicker and review its label",
    ),
    rule(
        "datepicker",
        "sw-datepicker",
        "mt-datepicker",
        "sw-datepicker is removed; use mt-datepicker and review its label",
    ),
    rule(
        "email-field",
        "sw-email-field",
        "mt-email-field",
        "sw-email-field is removed; use mt-email-field and review props, events, and slots",
    ),
    rule(
        "icon",
        "sw-icon",
        "mt-icon",
        "sw-icon is removed; use mt-icon with an explicit size",
    ),
    rule(
        "loader",
        "sw-loader",
        "mt-loader",
        "sw-loader is removed; use mt-loader",
    ),
    rule(
        "number-field",
        "sw-number-field",
        "mt-number-field",
        "sw-number-field is removed; use mt-number-field and review props, events, and slots",
    ),
    rule(
        "password-field",
        "sw-password-field",
        "mt-password-field",
        "sw-password-field is removed; use mt-password-field and review label and hint values",
    ),
    rule(
        "progress-bar",
        "sw-progress-bar",
        "mt-progress-bar",
        "sw-progress-bar is removed; use mt-progress-bar",
    ),
    rule(
        "select-field",
        "sw-select-field",
        "mt-select",
        "sw-select-field is removed; use mt-select and review props, options, events, and slots",
    ),
    rule(
        "skeleton-bar",
        "sw-skeleton-bar",
        "mt-skeleton-bar",
        "sw-skeleton-bar is removed; use mt-skeleton-bar",
    ),
    rule(
        "switch-field",
        "sw-switch-field",
        "mt-switch",
        "sw-switch-field is removed; use mt-switch and review props, events, and slots",
    ),
    rule(
        "text-field",
        "sw-text-field",
        "mt-text-field",
        "sw-text-field is removed; use mt-text-field and review props, events, and slots",
    ),
    rule(
        "textarea-field",
        "sw-textarea-field",
        "mt-textarea",
        "sw-textarea-field is removed; use mt-textarea and review its API",
    ),
    rule(
        "url-field",
        "sw-url-field",
        "mt-url-field",
        "sw-url-field is removed; use mt-url-field and review props, events, and slots",
    ),
    rule(
        "popover",
        "sw-popover",
        "mt-floating-ui",
        "sw-popover is removed; use mt-floating-ui",
    ),
];

/// Every migration rule in declaration order.
pub fn rules() -> &'static [Rule] {
    RULES
}

pub fn rule_by_id(id: &str) -> Option<&'static Rule> {
    RULES.iter().find(|rule| rule.id == id)
}

pub fn rule_for_tag(tag: &str) -> Option<&'static Rule> {
    RULES.iter().find(|rule| rule.source_tag == tag)
}

/// Creates a local, lossless migration for one complete HTML tag. Returns
/// [`MigrationError::Unsafe`] when the legacy API carries behavior for which
/// the target component has no deterministic equivalent.
pub fn compile(source: &str, node: &SyntaxNode, rule: &Rule) -> Result<Vec<Edit>> {
    let Some(tag) = HtmlTag::cast(node.clone()) else {
        return Err(MigrationError::Invalid(format!(
            "compile {}: expected complete HTML tag",
            rule.id
        )));
    };
    let name_matches = tag
        .starting_tag()
        .and_then(|starting| starting.name())
        .is_some_and(|name| name.text() == rule.source_tag);
    if !name_matches {
        return Err(MigrationError::Invalid(format!(
            "compile {}: source tag changed",
            rule.id
        )));
    }
    let mut editor = TagEditor::new(source, tag)?;
    editor.rename_tag(rule.target_tag)?;
    compile_rule(&mut editor, rule.id)?;
    editor.finish()
}

fn compile_rule(editor: &mut TagEditor<'_>, id: &str) -> Result<()> {
    match id {
        "alert" => {
            if editor.has_any(&[":variant", "v-bind:variant"]) {
                return Err(unsafe_("a bound alert variant may need a value migration"));
            }
            editor.map_static_value(
                "variant",
                &[
                    ("success", "positive"),
                    ("error", "critical"),
                    ("warning", "attention"),
                ],
            )
        }
        "button" => migrate_button(editor),
        "card" => migrate_card(editor),
        "checkbox-field" => {
            editor.rename_many(&[
                (":value", ":checked"),
                ("v-model", "v-model:checked"),
                ("v-model:value", "v-model:checked"),
                ("@update:value", "@update:checked"),
                ("partlyChecked", "partial"),
            ])?;
            editor.delete_many(&["id", "ghostValue", "padded"])?;
            editor.migrate_slot_props(&[
                ("label", SlotPolicy::ToProp),
                ("hint", SlotPolicy::MustBeEmpty),
            ])
        }
        "colorpicker" | "datepicker" => {
            migrate_model_value(editor, false)?;
            editor.migrate_slot_props(&[("label", SlotPolicy::ToProp)])
        }
        "email-field" | "text-field" => {
            if editor.has_any(&["isInvalid", "aiBadge", "@base-field-mounted"]) {
                return Err(unsafe_(
                    "isInvalid, aiBadge, or base-field-mounted behavior needs manual migration",
                ));
            }
            migrate_model_value(editor, true)?;
            editor.map_static_value("size", &[("medium", "default")])?;
            if id == "text-field" {
                for name in [
                    ":copyable",
                    ":copyableTooltip",
                    ":copyable-tooltip",
                    ":disabled",
                    ":required",
                    ":isInherited",
                    ":is-inherited",
                    ":disableInheritanceToggle",
                    ":disable-inheritance-toggle",
                ] {
                    editor.boolean_binding_to_shorthand(name)?;
                }
            }
            editor.migrate_slot_props(&[("label", SlotPolicy::ToProp)])
        }
        "icon" => migrate_icon(editor),
        "loader" | "skeleton-bar" => Ok(()),
        "number-field" => {
            migrate_model_value(editor, false)?;
            editor.migrate_slot_props(&[("label", SlotPolicy::ToProp)])
        }
        "password-field" => {
            if editor.has_any(&["isInvalid", "@base-field-mounted"]) {
                return Err(unsafe_(
                    "isInvalid or base-field-mounted behavior needs manual migration",
                ));
            }
            migrate_model_value(editor, true)?;
            editor.map_static_value("size", &[("medium", "default")])?;
            editor
                .migrate_slot_props(&[("label", SlotPolicy::ToProp), ("hint", SlotPolicy::ToProp)])
        }
        "progress-bar" | "url-field" => {
            migrate_model_value(editor, true)?;
            if id == "url-field" {
                return editor.migrate_slot_props(&[("label", SlotPolicy::ToProp)]);
            }
            Ok(())
        }
        "select-field" => migrate_select(editor),
        "switch-field" => migrate_switch(editor),
        "textarea-field" => {
            migrate_model_value(editor, true)?;
            editor.migrate_slot_props(&[("label", SlotPolicy::ToProp)])
        }
        "popover" => migrate_popover(editor),
        _ => Err(MigrationError::Invalid(format!(
            "unknown Administration Twig migration {}",
            go_quote(id)
        ))),
    }
}

fn migrate_model_value(editor: &mut TagEditor<'_>, static_value: bool) -> Result<()> {
    let mut renames = vec![
        (":value", ":model-value"),
        ("v-model:value", "v-model"),
        ("@update:value", "@update:model-value"),
    ];
    if static_value {
        renames.push(("value", "model-value"));
    }
    editor.rename_many(&renames)
}

fn migrate_button(editor: &mut TagEditor<'_>) -> Result<()> {
    if editor.has_any(&[":variant", "v-bind:variant"]) {
        return Err(unsafe_("a bound button variant may need a value migration"));
    }
    if let Some(variant) = editor.attribute("variant") {
        let Some(value) = attribute_value(&variant) else {
            return Err(unsafe_("button variant has no static value"));
        };
        match go_to_lower(&value).as_str() {
            "ghost" => {
                editor.delete("variant")?;
                editor.add_boolean("ghost");
            }
            "danger" => editor.set_attribute_value("variant", "critical")?,
            "ghost-danger" => {
                editor.set_attribute_value("variant", "critical")?;
                editor.add_boolean("ghost");
            }
            "contrast" | "context" => editor.delete("variant")?,
            _ => {}
        }
    }
    for (name, bound) in [
        ("router-link", false),
        (":router-link", true),
        ("v-bind:router-link", true),
    ] {
        let Some(attribute) = editor.attribute(name) else {
            continue;
        };
        if editor.has_any(&["@click", "v-on:click"]) {
            return Err(unsafe_(
                "router-link cannot be merged with an existing click handler",
            ));
        }
        let value = match attribute_value(&attribute) {
            Some(value) if !value.trim().is_empty() => value,
            _ => return Err(unsafe_("router-link has no migratable value")),
        };
        let expression = if bound {
            value.trim().to_owned()
        } else {
            format!("'{}'", escape_javascript_single_quoted(&value))
        };
        editor.rename_attribute(name, "@click")?;
        return editor.set_attribute_value("@click", &format!("$router.push({expression})"));
    }
    Ok(())
}

fn migrate_card(editor: &mut TagEditor<'_>) -> Result<()> {
    if editor.has_any(&["aiBadge", ":aiBadge", ":ai-badge"]) {
        return Err(unsafe_(
            "aiBadge augments the old title and needs a hand-written mt-card title slot",
        ));
    }
    if let Some(attribute) = editor.attribute("contentPadding") {
        if let Some(value) = attribute_value(&attribute)
            && !value.trim().eq_ignore_ascii_case("true")
        {
            return Err(unsafe_(
                "contentPadding=false has no lossless automatic mt-card equivalent",
            ));
        }
        editor.delete("contentPadding")?;
    }
    if editor.has_any(&[":contentPadding", ":content-padding"]) {
        return Err(unsafe_(
            "bound contentPadding behavior needs manual migration",
        ));
    }
    Ok(())
}

fn migrate_icon(editor: &mut TagEditor<'_>) -> Result<()> {
    let has_small = editor.attribute("small").is_some();
    let has_large = editor.attribute("large").is_some();
    let has_size = editor.attribute("size").is_some() || editor.attribute(":size").is_some();
    if (has_small && has_large) || (has_size && (has_small || has_large)) {
        return Err(unsafe_(
            "conflicting icon size properties need manual migration",
        ));
    }
    if has_small {
        return editor.replace_attribute("small", r#"size="16px""#, "size");
    }
    if has_large {
        return editor.replace_attribute("large", r#"size="32px""#, "size");
    }
    if !has_size {
        editor.add(r#"size="24px""#, "size");
    }
    Ok(())
}

fn migrate_select(editor: &mut TagEditor<'_>) -> Result<()> {
    if editor.has_any(&[":aside", "aside"]) {
        return Err(unsafe_(
            "select aside content has no deterministic mt-select equivalent",
        ));
    }
    migrate_model_value(editor, false)?;
    editor.migrate_slot_props(&[("label", SlotPolicy::ToProp)])?;
    editor.migrate_select_options()
}

fn migrate_switch(editor: &mut TagEditor<'_>) -> Result<()> {
    editor.rename_many(&[
        ("noMarginTop", "removeTopMargin"),
        ("value", "model-value"),
        (":value", ":model-value"),
        ("v-model:value", "v-model"),
        ("@update:value", "@update:model-value"),
    ])?;
    editor.delete_many(&["size", "id", "ghostValue", "padded", "partlyChecked"])?;
    editor.migrate_slot_props(&[
        ("label", SlotPolicy::ToProp),
        ("hint", SlotPolicy::MustBeEmpty),
    ])
}

fn migrate_popover(editor: &mut TagEditor<'_>) -> Result<()> {
    if editor.has_any(&[":zIndex", ":z-index", "zIndex", "z-index"]) {
        return Err(unsafe_(
            "popover z-index behavior has no deterministic mt-floating-ui equivalent",
        ));
    }
    editor.rename_many(&[
        (":resizeWidth", ":match-reference-width"),
        (":resize-width", ":match-reference-width"),
    ])?;
    if editor.has_any(&[":isOpened", ":is-opened"]) {
        return editor.rename_attribute(":isOpened", ":is-opened");
    }
    // v-if remains structural. mt-floating-ui still needs to be opened while
    // it exists, so retaining v-if and adding the target prop preserves
    // behavior.
    editor.add(r#":is-opened="true""#, ":is-opened");
    Ok(())
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum SlotPolicy {
    ToProp,
    MustBeEmpty,
}

struct TagEditor<'s> {
    source: &'s str,
    tag: HtmlTag,
    starting: HtmlStartingTag,
    builder: Builder,
    attrs: HashMap<String, HtmlAttribute>,
    additions: Vec<String>,
    added: Vec<String>,
}

impl<'s> TagEditor<'s> {
    fn new(source: &'s str, tag: HtmlTag) -> Result<Self> {
        let Some(starting) = tag.starting_tag() else {
            return Err(MigrationError::Invalid(
                "administration Twig migration has no starting tag".to_owned(),
            ));
        };
        let mut attrs = HashMap::new();
        for attribute in starting.attributes() {
            let Some(name) = twigquery::html_attribute_name(attribute.syntax()) else {
                continue;
            };
            if name.is_empty() {
                continue;
            }
            if attrs.contains_key(&name) {
                return Err(unsafe_(format!("duplicate attribute {name}")));
            }
            attrs.insert(name, attribute);
        }
        Ok(Self {
            source,
            tag,
            starting,
            builder: Builder::new(source),
            attrs,
            additions: Vec::new(),
            added: Vec::new(),
        })
    }

    fn rename_tag(&mut self, target: &str) -> Result<()> {
        let Some(name) = self.starting.name() else {
            return Err(MigrationError::Invalid(
                "administration Twig migration tag name is unavailable".to_owned(),
            ));
        };
        self.builder.replace_range(name.text_range(), target)?;
        if let Some(name) = self.tag.ending_tag().and_then(|ending| ending.name()) {
            self.builder.replace_range(name.text_range(), target)?;
        }
        Ok(())
    }

    fn attribute(&self, name: &str) -> Option<HtmlAttribute> {
        self.attrs.get(name).cloned()
    }

    fn has_any(&self, names: &[&str]) -> bool {
        names.iter().any(|name| {
            self.attrs.contains_key(*name) || self.added.iter().any(|added| added == name)
        })
    }

    fn rename_many(&mut self, values: &[(&str, &str)]) -> Result<()> {
        for (old, replacement) in values {
            self.rename_attribute(old, replacement)?;
        }
        Ok(())
    }

    fn rename_attribute(&mut self, old: &str, replacement: &str) -> Result<()> {
        if old == replacement {
            return Ok(());
        }
        let Some(attribute) = self.attrs.get(old).cloned() else {
            return Ok(());
        };
        if self.attrs.contains_key(replacement) {
            return Err(unsafe_(format!("both {old} and {replacement} are present")));
        }
        if self.added.iter().any(|added| added == replacement) {
            return Err(unsafe_(format!(
                "attribute {replacement} would be duplicated"
            )));
        }
        let Some(name) = attribute.name() else {
            return Err(MigrationError::Invalid(
                "administration Twig attribute name is unavailable".to_owned(),
            ));
        };
        self.builder.replace_range(name.text_range(), replacement)?;
        self.attrs.remove(old);
        self.attrs.insert(replacement.to_owned(), attribute);
        Ok(())
    }

    fn replace_attribute(
        &mut self,
        old: &str,
        replacement: &str,
        replacement_name: &str,
    ) -> Result<()> {
        let Some(attribute) = self.attrs.get(old).cloned() else {
            return Ok(());
        };
        if old != replacement_name && self.has_any(&[replacement_name]) {
            return Err(unsafe_(format!(
                "attribute {replacement_name} would be duplicated"
            )));
        }
        self.builder
            .replace_range(attribute.syntax().range_trimmed_trivia(), replacement)?;
        self.attrs.remove(old);
        self.attrs.insert(replacement_name.to_owned(), attribute);
        Ok(())
    }

    fn delete(&mut self, name: &str) -> Result<()> {
        let Some(attribute) = self.attrs.remove(name) else {
            return Ok(());
        };
        self.builder.delete(attribute.syntax())?;
        Ok(())
    }

    fn delete_many(&mut self, names: &[&str]) -> Result<()> {
        for name in names {
            self.delete(name)?;
        }
        Ok(())
    }

    fn add(&mut self, text: &str, name: &str) {
        if self.has_any(&[name]) {
            return;
        }
        self.additions.push(text.to_owned());
        self.added.push(name.to_owned());
    }

    fn add_boolean(&mut self, name: &str) {
        self.add(name, name);
    }

    fn set_attribute_value(&mut self, name: &str, value: &str) -> Result<()> {
        let Some(attribute) = self.attribute(name) else {
            return Err(MigrationError::Invalid(format!(
                "attribute {name} is unavailable"
            )));
        };
        let Some(string_value) = attribute.value() else {
            let end = u32::from(attribute.syntax().range_trimmed_trivia().end());
            self.builder
                .insert(end, format!("=\"{}\"", escape_attribute(value)))?;
            return Ok(());
        };
        if let Some(inner) = string_value.inner() {
            self.builder
                .replace_range(inner.syntax().text_range(), escape_attribute(value))?;
            return Ok(());
        }
        self.builder.replace_range(
            string_value.syntax().text_range(),
            format!("\"{}\"", escape_attribute(value)),
        )?;
        Ok(())
    }

    fn map_static_value(&mut self, name: &str, values: &[(&str, &str)]) -> Result<()> {
        let Some(attribute) = self.attribute(name) else {
            return Ok(());
        };
        let Some(value) = attribute_value(&attribute) else {
            return Err(unsafe_(format!("{name} has no static value")));
        };
        let key = go_to_lower(value.trim());
        let Some((_, replacement)) = values.iter().find(|(candidate, _)| *candidate == key) else {
            return Ok(());
        };
        self.set_attribute_value(name, replacement)
    }

    fn boolean_binding_to_shorthand(&mut self, name: &str) -> Result<()> {
        let Some(attribute) = self.attribute(name) else {
            return Ok(());
        };
        match attribute_value(&attribute) {
            Some(value) if value.trim().eq_ignore_ascii_case("true") => {}
            _ => return Ok(()),
        }
        let replacement = name.strip_prefix(':').unwrap_or(name);
        self.replace_attribute(name, replacement, replacement)
    }

    fn migrate_slot_props(&mut self, policies: &[(&str, SlotPolicy)]) -> Result<()> {
        let Some(body) = self.tag.body() else {
            return Ok(());
        };
        for child in body.syntax().children() {
            let Some(template) = HtmlTag::cast(child) else {
                continue;
            };
            if template.name().is_none_or(|name| name.text() != "template") {
                continue;
            }
            let name = template_slot_name(&template);
            let Some(&(_, policy)) = policies.iter().find(|(candidate, _)| *candidate == name)
            else {
                continue;
            };
            let slot = template_slot_value(&template);
            match policy {
                SlotPolicy::MustBeEmpty => {
                    if !slot.as_ref().is_some_and(|slot| slot.empty) {
                        return Err(unsafe_(format!(
                            "{name} slot content has no deterministic target equivalent"
                        )));
                    }
                }
                SlotPolicy::ToProp => {
                    let Some(slot) = slot else {
                        return Err(unsafe_(format!(
                            "{name} slot is too complex to convert to a prop"
                        )));
                    };
                    if !slot.empty {
                        let attribute_name = if slot.expression {
                            format!(":{name}")
                        } else {
                            name.clone()
                        };
                        if self.has_any(&[&name, &format!(":{name}"), &format!("v-bind:{name}")]) {
                            return Err(unsafe_(format!("{name} prop and slot are both present")));
                        }
                        self.add(
                            &format!("{attribute_name}=\"{}\"", escape_attribute(&slot.value)),
                            &attribute_name,
                        );
                    }
                }
            }
            self.builder.delete(template.syntax())?;
        }
        Ok(())
    }

    fn migrate_select_options(&mut self) -> Result<()> {
        let Some(body) = self.tag.body() else {
            return Ok(());
        };
        let mut options = Vec::new();
        let mut option_nodes = Vec::new();
        for child in body.syntax().children() {
            let Some(option) = HtmlTag::cast(child) else {
                continue;
            };
            if option.name().is_none_or(|name| name.text() != "option") {
                continue;
            }
            let value = option_value(&option);
            let label = template_slot_value(&option);
            let (Some(value), Some(label)) = (value, label) else {
                return Err(unsafe_(
                    "select option cannot be represented as a label/value object",
                ));
            };
            if label.empty {
                return Err(unsafe_(
                    "select option cannot be represented as a label/value object",
                ));
            }
            let label_text = if label.expression {
                label.value
            } else {
                format!(
                    "'{}'",
                    escape_javascript_single_quoted(&unescape_string(&label.value))
                )
            };
            options.push(format!("{{ label: {label_text}, value: {value} }}"));
            option_nodes.push(option.syntax().clone());
        }
        if options.is_empty() {
            return Ok(());
        }
        if self.has_any(&[":options", "v-bind:options", "options"]) {
            return Err(unsafe_(
                "select has both option children and an options prop",
            ));
        }
        for option in &option_nodes {
            self.builder.delete(option)?;
        }
        let expression = format!("[{}]", options.join(", "));
        self.add(
            &format!(":options=\"{}\"", escape_attribute(&expression)),
            ":options",
        );
        Ok(())
    }

    fn finish(mut self) -> Result<Vec<Edit>> {
        if !self.additions.is_empty() {
            let Some(list) = self
                .starting
                .syntax()
                .child_node_of_kind(HTML_ATTRIBUTE_LIST)
            else {
                return Err(MigrationError::Invalid(
                    "administration Twig attribute list is unavailable".to_owned(),
                ));
            };
            let separator = if self.starting.syntax().text().contains_char('\n') {
                format!("\n{}", self.attribute_indent())
            } else {
                " ".to_owned()
            };
            let text = format!("{separator}{}", self.additions.join(&separator));
            self.builder
                .insert(u32::from(list.text_range().end()), text)?;
        }
        Ok(self.builder.finish()?)
    }

    fn attribute_indent(&self) -> String {
        if let Some(attribute) = self.starting.attributes().last() {
            let syntax = attribute.syntax();
            let start = usize::from(syntax.text_range().start());
            let trimmed = usize::from(syntax.range_trimmed_trivia().start());
            if let Some(prefix) = self.source.get(start..trimmed)
                && let Some(newline) = prefix.rfind('\n')
            {
                let indent = &prefix[newline + 1..];
                if indent.trim().is_empty() {
                    return indent.to_owned();
                }
            }
        }
        "    ".to_owned()
    }
}

/// The static attribute value: `None` without a value, an empty string for a
/// value without inner text.
fn attribute_value(attribute: &HtmlAttribute) -> Option<String> {
    let value = attribute.value()?;
    Some(
        value
            .inner()
            .map(|inner| inner.syntax().text().to_string())
            .unwrap_or_default(),
    )
}

fn template_slot_name(tag: &HtmlTag) -> String {
    for attribute in tag.attributes() {
        let name = twigquery::html_attribute_name(attribute.syntax()).unwrap_or_default();
        if let Some(rest) = name.strip_prefix('#') {
            return rest.to_owned();
        }
        if let Some(rest) = name.strip_prefix("v-slot:") {
            return rest.to_owned();
        }
    }
    String::new()
}

struct SlotValue {
    value: String,
    expression: bool,
    empty: bool,
}

/// Go `templateSlotValue`; `None` when the content is too complex.
fn template_slot_value(tag: &HtmlTag) -> Option<SlotValue> {
    let empty = SlotValue {
        value: String::new(),
        expression: false,
        empty: true,
    };
    let Some(body) = tag.body() else {
        return Some(empty);
    };
    let body_text = body.syntax().text().to_string();
    let text = body_text.trim();
    if text.is_empty() {
        return Some(empty);
    }
    let mut twig_vars = twigquery::iterate_nodes(body.syntax(), &[TWIG_VAR]);
    let first = twig_vars.next();
    let multiple = twig_vars.next().is_some();
    if !multiple
        && let Some(twig_var) = first
        && twig_var.text().to_string().trim() == text
    {
        let expression = TwigVar::cast(twig_var)?.expression()?;
        return Some(SlotValue {
            value: expression.syntax().text().to_string().trim().to_owned(),
            expression: true,
            empty: false,
        });
    }
    for node in body.syntax().descendants() {
        if &node == body.syntax() {
            continue;
        }
        if node.kind() != HTML_TEXT {
            return None;
        }
    }
    let unescaped = unescape_string(text);
    let plain = unescaped.split_whitespace().collect::<Vec<_>>().join(" ");
    Some(SlotValue {
        value: plain,
        expression: false,
        empty: false,
    })
}

fn option_value(option: &HtmlTag) -> Option<String> {
    for attribute in option.attributes() {
        let name = twigquery::html_attribute_name(attribute.syntax()).unwrap_or_default();
        let Some(value) = attribute_value(&attribute) else {
            continue;
        };
        match name.as_str() {
            "value" => {
                return Some(format!(
                    "'{}'",
                    escape_javascript_single_quoted(&unescape_string(&value))
                ));
            }
            ":value" | "v-bind:value" | "v-model:value" => {
                let trimmed = value.trim();
                return (!trimmed.is_empty()).then(|| trimmed.to_owned());
            }
            _ => {}
        }
    }
    None
}

fn escape_attribute(value: &str) -> String {
    let mut result = String::with_capacity(value.len());
    for character in value.chars() {
        match character {
            '&' => result.push_str("&amp;"),
            '"' => result.push_str("&quot;"),
            '<' => result.push_str("&lt;"),
            '>' => result.push_str("&gt;"),
            other => result.push(other),
        }
    }
    result
}

fn escape_javascript_single_quoted(value: &str) -> String {
    let mut result = String::with_capacity(value.len());
    for character in value.chars() {
        match character {
            '\\' => result.push_str("\\\\"),
            '\'' => result.push_str("\\'"),
            '\n' => result.push_str("\\n"),
            '\r' => result.push_str("\\r"),
            other if other.is_control() => {}
            other => result.push(other),
        }
    }
    result
}

/// Go `strings.ToLower`: simple per-character lower-case mapping.
fn go_to_lower(value: &str) -> String {
    value
        .chars()
        .map(|character| {
            if character == '\u{130}' {
                return 'i';
            }
            let mut lower = character.to_lowercase();
            match (lower.next(), lower.next()) {
                (Some(single), None) => single,
                _ => character,
            }
        })
        .collect()
}

/// Go `%q` for the plain ASCII identifiers used as rule ids.
fn go_quote(value: &str) -> String {
    format!("{value:?}")
}

#[cfg(test)]
mod tests;
