//! Administration component sections and component contract (prop, event,
//! slot, model) hovers on markup tags.

use std::collections::HashSet;
use std::fmt::Write as _;

use swlsp_admin::{VueComponent, VueComponentEvent, VueComponentProp, VueComponentSlot};
use swlsp_parser_twig::query as twigquery;
use swlsp_syntax::SyntaxNode;

use super::{AdminHoverProvider, HoverResult, sections_hover};

impl AdminHoverProvider {
    /// Catalog order; one section per component.
    #[doc(hidden)]
    pub fn build_hover_content(&self, components: &[VueComponent]) -> String {
        let mut result = String::new();
        for (index, component) in components.iter().enumerate() {
            if index > 0 {
                result.push_str("\n---\n\n");
            }
            self.write_component_hover(&mut result, component);
        }
        result
    }

    fn write_component_hover(&self, out: &mut String, component: &VueComponent) {
        let _ = write!(out, "## `{}`\n\n", component.name);
        if !component.deprecated.is_empty() {
            let _ = write!(out, "**Deprecated:** {}\n\n", component.deprecated);
        }
        if !component.extends_component.is_empty() {
            let _ = write!(out, "**Extends**: `{}`\n\n", component.extends_component);
        }
        write_props(out, &component.props);
        write_events(out, &component.component_events());
        write_names(out, "Methods", &component.methods, "()");
        write_names(out, "Computed", &component.computed, "");
        write_names(out, "Data", &component.data, "");
        write_names(out, "Injected services", &component.injected, "");
        write_slots(out, &component.slots);
        if !component.definition_path.is_empty() {
            let _ = writeln!(
                out,
                "*Defined in*: `{}`",
                self.make_relative_path(&component.definition_path)
            );
        } else if !component.file_path.is_empty() {
            let _ = writeln!(
                out,
                "*Registered in*: `{}`",
                self.make_relative_path(&component.file_path)
            );
        }
    }

    /// The prop section shown for template prop hovers (Go `adminPropMarkdown`).
    #[doc(hidden)]
    pub fn prop_markdown(component_name: &str, prop: &VueComponentProp) -> String {
        admin_prop_markdown(component_name, prop)
    }

    pub(crate) fn components_for_markup_tag(
        &self,
        start_tag: Option<&SyntaxNode>,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> swlsp_indexer::Result<Vec<VueComponent>> {
        let Some(start_tag) = start_tag else {
            return Ok(Vec::new());
        };
        if let Some(selector) = swlsp_admin::twig_dynamic_component_selector(start_tag) {
            let (_, components, complete) =
                self.index.resolve_dynamic_component_contracts_for_owner(
                    template_path,
                    &selector,
                    owner,
                    Some(start_tag),
                )?;
            if !complete {
                return Ok(Vec::new());
            }
            return Ok(components);
        }
        let Some(name) = swlsp_admin::static_component_name_for_tag(start_tag) else {
            return Ok(Vec::new());
        };
        Ok(self
            .index
            .get_component_for_template_tag(template_path, &name, owner)?
            .into_iter()
            .collect())
    }

    pub(crate) fn component_event_hover(
        &self,
        attribute: &SyntaxNode,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> HoverResult {
        let event_name = swlsp_admin::normalize_event_name(
            &twigquery::html_attribute_name(attribute).unwrap_or_default(),
        );
        let start_tag = twigquery::starting_html_tag_at(attribute);
        let components =
            self.components_for_markup_tag(start_tag.as_ref(), template_path, owner)?;
        let mut sections = Vec::new();
        for component in &components {
            let Some(event) = component.component_event(&event_name) else {
                continue;
            };
            let mut value = format!(
                "**event** `{}`",
                swlsp_admin::canonical_event_name(&event.name)
            );
            if !event.type_.is_empty() {
                let _ = write!(value, ": `{}`", event.type_);
            }
            let _ = write!(
                value,
                "\n\nEmitted by Administration component `{}`.",
                component.name
            );
            let documentation = event.documentation.trim();
            if !documentation.is_empty() {
                let _ = write!(value, "\n\n{documentation}");
            }
            if !event.file_path.is_empty() {
                let path = self.make_relative_path(&event.file_path);
                if event.line > 0 {
                    let _ = write!(value, "\n\nDefined in `{path}:{}`.", event.line);
                } else {
                    let _ = write!(value, "\n\nDefined in `{path}`.");
                }
            }
            sections.push(value);
        }
        Ok(sections_hover(sections))
    }

    pub(crate) fn component_slot_hover(
        &self,
        attribute: &SyntaxNode,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> HoverResult {
        let attribute_name = twigquery::html_attribute_name(attribute).unwrap_or_default();
        let slot_name = swlsp_admin::normalize_slot_name(&attribute_name);
        let Some(start_tag) = twigquery::starting_html_tag_at(attribute) else {
            return Ok(None);
        };
        if slot_name.is_empty() {
            return Ok(None);
        }
        let (components, complete) =
            self.index
                .resolve_twig_slot_consumer_components(template_path, &start_tag, owner)?;
        if !complete {
            return Ok(None);
        }
        let mut sections = Vec::new();
        let mut seen = HashSet::new();
        for component in &components {
            let Some(slot) = component.component_slot(&slot_name) else {
                continue;
            };
            let key = format!("{}\x00{}\x00{}", component.name, slot.file_path, slot.name);
            if !seen.insert(key) {
                continue;
            }
            let mut value = format!(
                "**slot** `{slot_name}`\n\nProvided by Administration component `{}`.",
                component.name
            );
            if slot.is_dynamic_name() {
                let _ = write!(value, "\n\nDynamic slot family: `{}`.", slot.display_name());
            }
            if !slot.members.is_empty() {
                value.push_str("\n\nScoped payload:");
                for member in &slot.members {
                    let _ = write!(value, "\n\n- `{}`", member.name);
                    if !member.type_.is_empty() {
                        let _ = write!(value, ": `{}`", member.type_);
                    }
                }
            } else if !slot.payload_type.is_empty() {
                let _ = write!(value, "\n\nScoped payload: `{}`", slot.payload_type);
            }
            if !slot.file_path.is_empty() {
                let _ = write!(
                    value,
                    "\n\nDefined in `{}:{}`.",
                    self.make_relative_path(&slot.file_path),
                    slot.line
                );
            }
            sections.push(value);
        }
        Ok(sections_hover(sections))
    }

    pub(crate) fn component_prop_hover(
        &self,
        attribute: &SyntaxNode,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> HoverResult {
        let name = swlsp_admin::normalize_prop_name(
            &twigquery::html_attribute_name(attribute).unwrap_or_default(),
        );
        if name.is_empty() {
            return Ok(None);
        }
        let start_tag = twigquery::starting_html_tag_at(attribute);
        self.component_prop_hover_by_name(start_tag.as_ref(), &name, template_path, owner)
    }

    pub(crate) fn component_prop_hover_by_name(
        &self,
        start_tag: Option<&SyntaxNode>,
        name: &str,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> HoverResult {
        if name.is_empty() || start_tag.is_none() {
            return Ok(None);
        }
        let components = self.components_for_markup_tag(start_tag, template_path, owner)?;
        let mut sections = Vec::new();
        for component in &components {
            for prop in &component.props {
                if prop.name == name {
                    sections.push(admin_prop_markdown(&component.name, prop));
                }
            }
        }
        Ok(sections_hover(sections))
    }

    pub(crate) fn component_model_hover(
        &self,
        attribute: &SyntaxNode,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> HoverResult {
        let start_tag = twigquery::starting_html_tag_at(attribute);
        let components =
            self.components_for_markup_tag(start_tag.as_ref(), template_path, owner)?;
        let attribute_name = twigquery::html_attribute_name(attribute).unwrap_or_default();
        let mut sections = Vec::new();
        for component in &components {
            let Some(model) = component.component_model(&attribute_name) else {
                continue;
            };
            let mut value = format!("**model binding** `{}`", model.attribute_name);
            let value_type = swlsp_admin::vue_prop_value_type(&model.prop.type_);
            if !value_type.is_empty() {
                let _ = write!(value, ": `{value_type}`");
            }
            let _ = write!(
                value,
                "\n\nReads prop `{}` and writes through event `{}` on Administration component `{}`.",
                model.prop_name, model.event_name, component.name
            );
            if !model.prop.deprecated.is_empty() {
                let _ = write!(value, "\n\n**Deprecated:** {}", model.prop.deprecated);
            }
            if !model.prop.file_path.is_empty() {
                let _ = write!(
                    value,
                    "\n\nProp defined in `{}:{}`.",
                    self.make_relative_path(&model.prop.file_path),
                    model.prop.line
                );
            }
            if !model.event.file_path.is_empty() {
                let _ = write!(
                    value,
                    "\n\nEvent defined in `{}:{}`.",
                    self.make_relative_path(&model.event.file_path),
                    model.event.line
                );
            }
            sections.push(value);
        }
        Ok(sections_hover(sections))
    }
}

pub(crate) fn admin_prop_markdown(component_name: &str, prop: &VueComponentProp) -> String {
    let mut value = format!("**prop** `{}`", prop.name);
    if !prop.type_.is_empty() {
        let _ = write!(value, ": `{}`", prop.type_);
    }
    let _ = write!(value, "\n\nComponent: `{component_name}`");
    if !prop.deprecated.is_empty() {
        let _ = write!(value, "\n\n**Deprecated:** {}", prop.deprecated);
    }
    let documentation = prop.documentation.trim();
    if !documentation.is_empty() {
        let _ = write!(value, "\n\n{documentation}");
    }
    if prop.required {
        value.push_str("\n\nRequired.");
    }
    if !prop.default.is_empty() {
        let _ = write!(value, "\n\nDefault: `{}`", prop.default);
    }
    let (values, complete) = swlsp_admin::vue_prop_allowed_values(prop);
    if !values.is_empty() {
        let label = if complete {
            "Allowed values: "
        } else {
            "Known values: "
        };
        let formatted: Vec<String> = values
            .iter()
            .map(|allowed| {
                let display = if allowed.is_empty() {
                    "(empty)"
                } else {
                    allowed.as_str()
                };
                format!("`{}`", display.replace('`', "\\`"))
            })
            .collect();
        let _ = write!(value, "\n\n{label}{}", formatted.join(", "));
    }
    value
}

fn write_props(out: &mut String, props: &[VueComponentProp]) {
    if props.is_empty() {
        return;
    }
    out.push_str("### Props\n\n");
    for prop in props {
        let _ = write!(out, "- `{}`", prop.name);
        if !prop.type_.is_empty() {
            let _ = write!(out, ": **{}**", prop.type_);
        }
        if prop.required {
            out.push_str(" *(required)*");
        }
        if !prop.deprecated.is_empty() {
            out.push_str(" *(deprecated)*");
        }
        if !prop.default.is_empty() {
            let _ = write!(out, " = `{}`", prop.default);
        }
        out.push('\n');
    }
    out.push('\n');
}

fn write_events(out: &mut String, events: &[VueComponentEvent]) {
    if events.is_empty() {
        return;
    }
    out.push_str("### Events\n\n");
    for event in events {
        let _ = write!(
            out,
            "- `{}`",
            swlsp_admin::canonical_event_name(&event.name)
        );
        if !event.type_.is_empty() {
            let _ = write!(out, ": `{}`", event.type_);
        }
        out.push('\n');
    }
    out.push('\n');
}

fn write_names(out: &mut String, heading: &str, names: &[String], suffix: &str) {
    if names.is_empty() {
        return;
    }
    let _ = write!(out, "### {heading}\n\n");
    for name in names {
        let _ = writeln!(out, "- `{name}{suffix}`");
    }
    out.push('\n');
}

fn write_slots(out: &mut String, slots: &[VueComponentSlot]) {
    if slots.is_empty() {
        return;
    }
    out.push_str("### Slots\n\n");
    for slot in slots {
        let _ = writeln!(out, "- `{}`", slot.display_name());
    }
    out.push('\n');
}
