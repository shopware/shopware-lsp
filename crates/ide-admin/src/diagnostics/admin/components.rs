//! Component tag contracts.

use std::collections::{HashMap, HashSet};

use swlsp_admin::{
    VueAttributeReference, VueComponent, VueComponentProp, VueDynamicComponentCandidate,
    VueObjectBindingField, camel_to_kebab, canonical_event_name, is_component_tag,
    is_shopware_component_tag, normalize_model_argument, normalize_prop_name,
    twig_dynamic_component_selector, vue_event_payload_type, vue_event_reference_for_attribute,
    vue_model_expression_assignable, vue_model_reference_for_attribute, vue_object_binding_fields,
    vue_object_binding_names, vue_prop_allowed_values, vue_prop_reference_for_attribute,
    vue_prop_value_type, vue_slot_reference_for_attribute, vue_static_string_literal,
    vue_types_provably_incompatible,
};
use swlsp_lsp::{BoxError, Problem};
use swlsp_lsp_protocol::{DiagnosticSeverity, DiagnosticTag};
use swlsp_parser_twig::ast::{AstNode, HtmlAttribute, HtmlStartingTag};
use swlsp_parser_twig::{query as twigquery, syntax as twigsyntax};
use swlsp_suggestion::similar;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, SyntaxToken, TextRange, TextSize};
use swlsp_textutil::lowercase;

use super::analyzer::AdminAnalyzer;
use super::twig::AdminTwigDiagnosticDocument;
use super::{payload, string_array, text};
use crate::diagnostics::common::admin_nearby_suggestions;

fn range(start: u32, end: u32) -> TextRange {
    TextRange::new(TextSize::from(start), TextSize::from(end))
}

fn attribute_name(node: &SyntaxNode) -> String {
    twigquery::html_attribute_name(node).unwrap_or_default()
}

/// The inner (unquoted) value node of an attribute.
fn attribute_inner(attribute: &HtmlAttribute) -> Option<SyntaxNode> {
    Some(attribute.value()?.inner()?.syntax().clone())
}

fn starting_tag_name(start_tag: &SyntaxNode) -> Option<SyntaxToken> {
    HtmlStartingTag::cast(start_tag.clone())?.name()
}

impl AdminAnalyzer {
    pub(super) fn check_component_props(
        &self,
        root: &SyntaxNode,
        content: &str,
        start_tag: &SyntaxNode,
        analysis: &mut AdminTwigDiagnosticDocument,
        diagnostics: &mut Vec<Problem>,
    ) -> Result<(), BoxError> {
        let tag_name = twigquery::html_tag_name(start_tag).unwrap_or_default();
        if tag_name.is_empty() {
            return Ok(());
        }
        let component_range = starting_tag_name(start_tag)
            .map(|token| token.text_range())
            .unwrap_or_default();
        if let Some(selector) = twig_dynamic_component_selector(start_tag) {
            for candidate in &selector.candidates {
                match self.diagnostic_component_for_template_tag(analysis, &candidate.name)? {
                    None => self.check_unknown_dynamic_component(candidate, diagnostics),
                    Some(component) => self.check_deprecated_component(
                        &component,
                        &candidate.name,
                        candidate.range,
                        diagnostics,
                    ),
                }
            }
            let Some(indexer) = self.indexer() else {
                return Ok(());
            };
            let (resolved_selector, components, complete) = indexer
                .resolve_dynamic_component_contracts_for_owner(
                    &analysis.template_path,
                    &selector,
                    analysis.live_owner.as_ref(),
                    Some(start_tag),
                )?;
            if !complete {
                return Ok(());
            }
            self.check_unknown_component_contract_attributes(
                start_tag,
                &components,
                &selector.attribute_name,
                &tag_name,
                diagnostics,
            );
            check_deprecated_component_contract_attributes(start_tag, &components, diagnostics);
            for component in &components {
                let mut candidate_range = component_range;
                for candidate in &resolved_selector.candidates {
                    if candidate.name == component.name {
                        if !candidate.range.is_empty() {
                            candidate_range = candidate.range;
                        } else if !resolved_selector.expression_range.is_empty() {
                            candidate_range = resolved_selector.expression_range;
                        }
                        break;
                    }
                }
                self.check_resolved_component_props(
                    root,
                    content,
                    start_tag,
                    &analysis.template_path,
                    component,
                    &component.name,
                    candidate_range,
                    true,
                    analysis.live_owner.as_ref(),
                    diagnostics,
                )?;
            }
            return Ok(());
        }

        // Skip non-component tags (standard HTML elements and template).
        if !is_component_tag(&tag_name) {
            return Ok(());
        }
        let component = match self.diagnostic_component_for_template_tag(analysis, &tag_name) {
            Ok(Some(component)) => component,
            _ => {
                self.check_unknown_component(&tag_name, start_tag, diagnostics);
                return Ok(());
            }
        };
        self.check_unknown_component_contract_attributes(
            start_tag,
            std::slice::from_ref(&component),
            "",
            &tag_name,
            diagnostics,
        );
        self.check_deprecated_component(&component, &tag_name, component_range, diagnostics);
        check_deprecated_component_contract_attributes(
            start_tag,
            std::slice::from_ref(&component),
            diagnostics,
        );
        self.check_resolved_component_props(
            root,
            content,
            start_tag,
            &analysis.template_path,
            &component,
            &tag_name,
            component_range,
            false,
            analysis.live_owner.as_ref(),
            diagnostics,
        )
    }

    fn check_deprecated_component(
        &self,
        component: &VueComponent,
        display_name: &str,
        range: TextRange,
        diagnostics: &mut Vec<Problem>,
    ) {
        if range.is_empty() || component.deprecated.trim().is_empty() {
            return;
        }
        if self.suppressed_component_diagnostics.contains(display_name) {
            return;
        }
        diagnostics.push(Problem {
            range,
            message: format!(
                "Administration component '{display_name}' is deprecated: {}",
                component.deprecated
            ),
            source: "shopware".into(),
            severity: Some(DiagnosticSeverity::HINT),
            id: "admin.component.deprecated".into(),
            tags: vec![DiagnosticTag::DEPRECATED],
            ..Problem::default()
        });
    }

    fn check_unknown_component_contract_attributes(
        &self,
        start_tag: &SyntaxNode,
        components: &[VueComponent],
        selector_attribute: &str,
        tag_name: &str,
        diagnostics: &mut Vec<Problem>,
    ) {
        if components.is_empty() {
            return;
        }
        let catalog = AdminComponentContractCatalog::new(components);
        let Some(tag) = HtmlStartingTag::cast(start_tag.clone()) else {
            return;
        };
        for attribute in tag.attributes() {
            check_unknown_component_contract_attribute(
                &attribute,
                &catalog,
                selector_attribute,
                tag_name,
                diagnostics,
            );
        }
    }

    pub(super) fn check_component_slot_names(
        &self,
        start_tag: &SyntaxNode,
        template_path: &str,
        live_owner: Option<&VueComponent>,
        diagnostics: &mut Vec<Problem>,
    ) -> Result<(), BoxError> {
        let Some(indexer) = self.indexer() else {
            return Ok(());
        };
        let Some(tag) = HtmlStartingTag::cast(start_tag.clone()) else {
            return Ok(());
        };
        let mut references: Vec<VueAttributeReference> = Vec::new();
        for attribute in tag.attributes() {
            let Some(name_token) = attribute.name() else {
                continue;
            };
            if let Some(reference) = vue_slot_reference_for_attribute(
                &attribute_name(attribute.syntax()),
                name_token.text_range(),
            ) {
                references.push(reference);
            }
        }
        if references.is_empty() {
            return Ok(());
        }
        let (components, complete) =
            indexer.resolve_twig_slot_consumer_components(template_path, start_tag, live_owner)?;
        if !complete || components.is_empty() {
            return Ok(());
        }
        let mut known_names = Vec::new();
        let mut known = HashSet::new();
        let mut component_names = Vec::with_capacity(components.len());
        for component in &components {
            component_names.push(component.name.clone());
            for slot in &component.slots {
                if slot.name.is_empty() || !known.insert(slot.name.clone()) {
                    continue;
                }
                known_names.push(slot.name.clone());
            }
        }
        let owner = component_names.join(" | ");
        for reference in references {
            if components
                .iter()
                .any(|component| component.component_slot(&reference.name).is_some())
            {
                continue;
            }
            let suggestions = admin_nearby_suggestions(&reference.name, &known_names);
            if suggestions.is_empty() {
                continue;
            }
            diagnostics.push(Problem {
                range: reference.range,
                message: format!("Unknown slot '{}' on component '{owner}'", reference.name),
                source: "shopware".into(),
                severity: Some(DiagnosticSeverity::WARNING),
                id: "admin.component.unknown-slot".into(),
                payload: Some(payload(vec![
                    ("componentName", text(&owner)),
                    ("slotName", text(&reference.name)),
                    ("suggestions", string_array(suggestions)),
                ])),
                ..Problem::default()
            });
        }
        Ok(())
    }

    #[allow(clippy::too_many_arguments)]
    fn check_resolved_component_props(
        &self,
        root: &SyntaxNode,
        content: &str,
        start_tag: &SyntaxNode,
        template_path: &str,
        component: &VueComponent,
        tag_name: &str,
        component_range: TextRange,
        dynamic_component: bool,
        live_owner: Option<&VueComponent>,
        diagnostics: &mut Vec<Problem>,
    ) -> Result<(), BoxError> {
        let (mut present_attributes, has_unknown_object_binding) = tag_attributes(start_tag);
        let mut props: HashMap<String, VueComponentProp> = HashMap::new();
        for prop in &component.props {
            props.insert(prop.name.clone(), prop.clone());
        }
        if dynamic_component {
            props.remove("is");
            present_attributes.remove("is");
        }
        check_static_prop_types(tag_name, start_tag, &props, diagnostics);
        check_static_prop_values(tag_name, start_tag, &props, diagnostics);
        self.check_bound_prop_types(
            root,
            content,
            tag_name,
            start_tag,
            &props,
            template_path,
            live_owner,
            diagnostics,
        )?;
        self.check_model_bindings(
            root,
            content,
            tag_name,
            start_tag,
            component,
            template_path,
            live_owner,
            diagnostics,
        )?;
        if has_unknown_object_binding {
            // An arbitrary v-bind expression may contain any required prop.
            // Absence cannot be proven without evaluating the component
            // expression.
            return Ok(());
        }
        for prop in &component.props {
            if !prop.required {
                continue;
            }
            if is_prop_present(&prop.name, &present_attributes)
                || component_model_prop_present(component, &prop.name, &present_attributes)
            {
                continue;
            }
            if component_range.is_empty() {
                continue;
            }
            diagnostics.push(Problem {
                range: component_range,
                message: format!(
                    "Missing required prop '{}' on component '{tag_name}'",
                    prop.name
                ),
                source: "shopware".into(),
                severity: Some(DiagnosticSeverity::WARNING),
                id: "admin.component.missing-required-prop".into(),
                payload: Some(payload(vec![
                    ("componentName", text(tag_name)),
                    ("propName", text(&prop.name)),
                ])),
                ..Problem::default()
            });
        }
        Ok(())
    }

    fn check_unknown_dynamic_component(
        &self,
        candidate: &VueDynamicComponentCandidate,
        diagnostics: &mut Vec<Problem>,
    ) {
        let Some(indexer) = self.indexer() else {
            return;
        };
        if !is_shopware_component_tag(&candidate.name) {
            return;
        }
        let components = match indexer.get_all_components_view() {
            Ok(components) if !components.is_empty() => components,
            _ => return,
        };
        let mut names = Vec::with_capacity(components.len());
        let mut seen = HashSet::new();
        for component in components.iter() {
            if component.name.is_empty() || !seen.insert(component.name.as_str()) {
                continue;
            }
            names.push(component.name.clone());
        }
        diagnostics.push(Problem {
            range: candidate.range,
            message: format!(
                "Administration component '{}' is not registered",
                candidate.name
            ),
            source: "shopware".into(),
            severity: Some(DiagnosticSeverity::WARNING),
            id: "admin.component.not-found".into(),
            payload: Some(payload(vec![
                ("componentName", text(&candidate.name)),
                (
                    "suggestions",
                    string_array(similar(&candidate.name, &names)),
                ),
            ])),
            ..Problem::default()
        });
    }

    #[allow(clippy::too_many_arguments)]
    fn check_model_bindings(
        &self,
        root: &SyntaxNode,
        content: &str,
        tag_name: &str,
        start_tag: &SyntaxNode,
        component: &VueComponent,
        template_path: &str,
        live_owner: Option<&VueComponent>,
        diagnostics: &mut Vec<Problem>,
    ) -> Result<(), BoxError> {
        for attribute_node in twigquery::iterate_nodes(start_tag, &[twigsyntax::HTML_ATTRIBUTE]) {
            let name = attribute_name(&attribute_node);
            if normalize_model_argument(&name).is_none() {
                continue;
            }
            let Some(model) = component.component_model(&name) else {
                continue;
            };
            let Some(inner) =
                HtmlAttribute::cast(attribute_node.clone()).and_then(|a| attribute_inner(&a))
            else {
                continue;
            };
            let expression_range = inner.range_trimmed_trivia();
            if expression_range.is_empty() || usize::from(expression_range.end()) > content.len() {
                continue;
            }
            let inner_text = inner.text().to_string();
            let expression = inner_text.trim();
            if !vue_model_expression_assignable(expression) {
                diagnostics.push(Problem {
                    range: expression_range,
                    message: format!(
                        "Model binding '{name}' on component '{tag_name}' requires an assignable expression"
                    ),
                    source: "shopware".into(),
                    severity: Some(DiagnosticSeverity::WARNING),
                    id: "admin.component.model-not-assignable".into(),
                    payload: Some(payload(vec![
                        ("componentName", text(tag_name)),
                        ("modelName", text(&name)),
                        ("expression", text(expression)),
                    ])),
                    ..Problem::default()
                });
                continue;
            }
            let Some(indexer) = self.indexer() else {
                continue;
            };
            let Some(actual_type) = indexer.resolve_twig_vue_expression_type_for_component(
                root,
                content,
                expression,
                expression_range.start().into(),
                template_path,
                live_owner,
            )?
            else {
                continue;
            };
            let mut expected_types = vec![model.prop.type_.clone()];
            let payload_type = vue_event_payload_type(&model.event.type_);
            if !payload_type.is_empty() {
                expected_types.push(payload_type);
            }
            let Some(expected_type) = expected_types
                .iter()
                .find(|candidate| vue_types_provably_incompatible(candidate, &actual_type))
                .map(|candidate| vue_prop_value_type(candidate))
            else {
                continue;
            };
            diagnostics.push(Problem {
                range: expression_range,
                message: format!(
                    "Model binding '{name}' on component '{tag_name}' expects {expected_type}, but the expression has type {actual_type}"
                ),
                source: "shopware".into(),
                severity: Some(DiagnosticSeverity::WARNING),
                id: "admin.component.model-type".into(),
                payload: Some(payload(vec![
                    ("componentName", text(tag_name)),
                    ("modelName", text(&name)),
                    ("propName", text(&model.prop_name)),
                    ("eventName", text(&model.event_name)),
                    ("expectedType", text(&expected_type)),
                    ("actualType", text(&actual_type)),
                ])),
                ..Problem::default()
            });
        }
        Ok(())
    }

    #[allow(clippy::too_many_arguments)]
    fn check_bound_prop_types(
        &self,
        root: &SyntaxNode,
        content: &str,
        tag_name: &str,
        start_tag: &SyntaxNode,
        props: &HashMap<String, VueComponentProp>,
        template_path: &str,
        live_owner: Option<&VueComponent>,
        diagnostics: &mut Vec<Problem>,
    ) -> Result<(), BoxError> {
        let Some(indexer) = self.indexer() else {
            return Ok(());
        };
        for attribute_node in twigquery::iterate_nodes(start_tag, &[twigsyntax::HTML_ATTRIBUTE]) {
            let name = attribute_name(&attribute_node);
            if name == "v-bind" {
                self.check_object_bound_prop_types(
                    root,
                    content,
                    tag_name,
                    &attribute_node,
                    props,
                    template_path,
                    live_owner,
                    diagnostics,
                )?;
                continue;
            }
            if !name.starts_with(':') && !name.starts_with("v-bind:") {
                continue;
            }
            let Some(prop) = props.get(&normalize_prop_name(&name)) else {
                continue;
            };
            let Some(inner) =
                HtmlAttribute::cast(attribute_node.clone()).and_then(|a| attribute_inner(&a))
            else {
                continue;
            };
            let expression_range = inner.range_trimmed_trivia();
            if expression_range.is_empty() || usize::from(expression_range.end()) > content.len() {
                continue;
            }
            let inner_text = inner.text().to_string();
            let expression = inner_text.trim();
            if let Some((actual, start, end)) = vue_static_string_literal(&inner_text) {
                let inner_start: u32 = inner.text_range().start().into();
                let literal_range = range(inner_start + start, inner_start + end);
                if let Some(problem) =
                    invalid_admin_prop_value_problem(tag_name, prop, &actual, literal_range)
                {
                    diagnostics.push(problem);
                    continue;
                }
            }
            if prop.type_.trim().is_empty() {
                continue;
            }
            let Some(actual_type) = indexer.resolve_twig_vue_expression_type_for_component(
                root,
                content,
                expression,
                expression_range.start().into(),
                template_path,
                live_owner,
            )?
            else {
                continue;
            };
            if !vue_types_provably_incompatible(&prop.type_, &actual_type) {
                continue;
            }
            let expected_type = vue_prop_value_type(&prop.type_);
            diagnostics.push(bound_prop_type_problem(
                tag_name,
                prop,
                &expected_type,
                &actual_type,
                expression_range,
                "bound expression",
            ));
        }
        Ok(())
    }

    #[allow(clippy::too_many_arguments)]
    fn check_object_bound_prop_types(
        &self,
        root: &SyntaxNode,
        content: &str,
        tag_name: &str,
        attribute_node: &SyntaxNode,
        props: &HashMap<String, VueComponentProp>,
        template_path: &str,
        live_owner: Option<&VueComponent>,
        diagnostics: &mut Vec<Problem>,
    ) -> Result<(), BoxError> {
        let Some(indexer) = self.indexer() else {
            return Ok(());
        };
        let Some(inner) =
            HtmlAttribute::cast(attribute_node.clone()).and_then(|a| attribute_inner(&a))
        else {
            return Ok(());
        };
        let (fields, _) =
            vue_object_binding_fields(&inner.text().to_string(), inner.text_range().start().into());
        for field in &fields {
            let Some(prop) = props.get(&normalize_prop_name(&field.name)) else {
                continue;
            };
            if field.expression.is_empty() || field.expression_range.is_empty() {
                continue;
            }
            if let Some((actual, start, end)) = vue_static_string_literal(&field.expression) {
                let field_start: u32 = field.expression_range.start().into();
                let literal_range = range(field_start + start, field_start + end);
                if let Some(problem) =
                    invalid_admin_prop_value_problem(tag_name, prop, &actual, literal_range)
                {
                    diagnostics.push(problem);
                    continue;
                }
            }
            if prop.type_.trim().is_empty() {
                continue;
            }
            let Some(actual_type) = indexer.resolve_twig_vue_expression_type_for_component(
                root,
                content,
                &field.expression,
                field.expression_range.start().into(),
                template_path,
                live_owner,
            )?
            else {
                continue;
            };
            if !vue_types_provably_incompatible(&prop.type_, &actual_type) {
                continue;
            }
            let expected_type = vue_prop_value_type(&prop.type_);
            diagnostics.push(bound_prop_type_problem(
                tag_name,
                prop,
                &expected_type,
                &actual_type,
                field.expression_range,
                "v-bind field",
            ));
        }
        Ok(())
    }

    fn check_unknown_component(
        &self,
        tag_name: &str,
        start_tag: &SyntaxNode,
        diagnostics: &mut Vec<Problem>,
    ) {
        let Some(indexer) = self.indexer() else {
            return;
        };
        if !is_shopware_component_tag(tag_name)
            || self.suppressed_component_diagnostics.contains(tag_name)
        {
            return;
        }
        let components = match indexer.get_all_components_view() {
            // During an initial or partial index there is not enough
            // evidence to claim that a registry-owned tag is missing.
            Ok(components) if !components.is_empty() => components,
            _ => return,
        };
        let mut names = Vec::with_capacity(components.len());
        let mut seen = HashSet::new();
        for component in components.iter() {
            let name = component.name.trim();
            if name.is_empty() || !seen.insert(name) {
                continue;
            }
            names.push(name.to_owned());
        }
        let Some(name_token) = starting_tag_name(start_tag) else {
            return;
        };
        diagnostics.push(Problem {
            range: name_token.text_range(),
            message: format!("Administration component '{tag_name}' is not registered"),
            source: "shopware".into(),
            severity: Some(DiagnosticSeverity::WARNING),
            id: "admin.component.not-found".into(),
            payload: Some(payload(vec![
                ("componentName", text(tag_name)),
                ("suggestions", string_array(similar(tag_name, &names))),
            ])),
            ..Problem::default()
        });
    }
}

fn bound_prop_type_problem(
    tag_name: &str,
    prop: &VueComponentProp,
    expected_type: &str,
    actual_type: &str,
    range: TextRange,
    subject: &str,
) -> Problem {
    Problem {
        range,
        message: format!(
            "Prop '{}' on component '{tag_name}' expects {expected_type}, but the {subject} has type {actual_type}",
            prop.name
        ),
        source: "shopware".into(),
        severity: Some(DiagnosticSeverity::WARNING),
        id: "admin.component.bound-prop-type".into(),
        payload: Some(payload(vec![
            ("componentName", text(tag_name)),
            ("propName", text(&prop.name)),
            ("expectedType", text(expected_type)),
            ("actualType", text(actual_type)),
        ])),
        ..Problem::default()
    }
}

fn check_deprecated_component_contract_attributes(
    start_tag: &SyntaxNode,
    components: &[VueComponent],
    diagnostics: &mut Vec<Problem>,
) {
    if components.is_empty() {
        return;
    }
    let owner = components
        .iter()
        .map(|component| component.name.as_str())
        .collect::<Vec<_>>()
        .join(" | ");
    let Some(tag) = HtmlStartingTag::cast(start_tag.clone()) else {
        return;
    };
    for attribute in tag.attributes() {
        let Some(name_token) = attribute.name() else {
            continue;
        };
        let name = attribute_name(attribute.syntax());
        if name == "v-bind" {
            let Some(inner) = attribute_inner(&attribute) else {
                continue;
            };
            let (fields, _) = vue_object_binding_fields(
                &inner.text().to_string(),
                inner.text_range().start().into(),
            );
            for field in fields {
                let prop_name = normalize_prop_name(&field.name);
                let message = common_deprecated_admin_prop(components, &prop_name);
                if !message.is_empty() {
                    append_deprecated_admin_prop_problem(
                        diagnostics,
                        &owner,
                        &prop_name,
                        &message,
                        field.name_range,
                    );
                }
            }
            continue;
        }
        if normalize_model_argument(&name).is_some() {
            let (message, prop_name) = common_deprecated_admin_model(components, &name);
            if !message.is_empty() {
                append_deprecated_admin_prop_problem(
                    diagnostics,
                    &owner,
                    &prop_name,
                    &message,
                    name_token.text_range(),
                );
            }
            continue;
        }
        let Some(reference) = vue_prop_reference_for_attribute(&name, name_token.text_range())
        else {
            continue;
        };
        let message = common_deprecated_admin_prop(components, &reference.name);
        if !message.is_empty() {
            append_deprecated_admin_prop_problem(
                diagnostics,
                &owner,
                &reference.name,
                &message,
                reference.range,
            );
        }
    }
}

fn common_deprecated_admin_prop(components: &[VueComponent], name: &str) -> String {
    let mut messages: Vec<String> = Vec::new();
    for component in components {
        let Some(prop) = component.component_prop(name) else {
            return String::new();
        };
        if prop.deprecated.trim().is_empty() {
            return String::new();
        }
        if !messages.contains(&prop.deprecated) {
            messages.push(prop.deprecated.clone());
        }
    }
    messages.join(" / ")
}

fn common_deprecated_admin_model(
    components: &[VueComponent],
    attribute_name: &str,
) -> (String, String) {
    let mut messages: Vec<String> = Vec::new();
    let mut prop_name = String::new();
    for component in components {
        let Some(model) = component.component_model(attribute_name) else {
            return (String::new(), String::new());
        };
        if model.prop.deprecated.trim().is_empty() {
            return (String::new(), String::new());
        }
        if prop_name.is_empty() {
            prop_name = model.prop_name.clone();
        }
        if !messages.contains(&model.prop.deprecated) {
            messages.push(model.prop.deprecated.clone());
        }
    }
    (messages.join(" / "), prop_name)
}

fn append_deprecated_admin_prop_problem(
    diagnostics: &mut Vec<Problem>,
    owner: &str,
    prop_name: &str,
    message: &str,
    range: TextRange,
) {
    if range.is_empty() {
        return;
    }
    diagnostics.push(Problem {
        range,
        message: format!(
            "Prop '{prop_name}' on Administration component '{owner}' is deprecated: {message}"
        ),
        source: "shopware".into(),
        severity: Some(DiagnosticSeverity::HINT),
        id: "admin.component.deprecated-prop".into(),
        tags: vec![DiagnosticTag::DEPRECATED],
        ..Problem::default()
    });
}

struct AdminComponentContractCatalog {
    props: HashSet<String>,
    prop_names: Vec<String>,
    events: HashSet<String>,
    event_names: Vec<String>,
    models: HashSet<String>,
    model_names: Vec<String>,
}

impl AdminComponentContractCatalog {
    fn new(components: &[VueComponent]) -> Self {
        let mut result = Self {
            props: HashSet::new(),
            prop_names: Vec::new(),
            events: HashSet::new(),
            event_names: Vec::new(),
            models: HashSet::new(),
            model_names: Vec::new(),
        };
        for component in components {
            for prop in &component.props {
                let name = prop.name.trim();
                if !name.is_empty() && result.props.insert(name.to_owned()) {
                    result.prop_names.push(name.to_owned());
                }
                let attribute = format!("v-model:{}", camel_to_kebab(&prop.name));
                if component.component_model(&attribute).is_some()
                    && result.models.insert(prop.name.clone())
                {
                    result.model_names.push(prop.name.clone());
                }
            }
            for event in component.component_events() {
                let name = canonical_event_name(&event.name);
                if !name.is_empty() && result.events.insert(name.clone()) {
                    result.event_names.push(name);
                }
            }
        }
        result
    }
}

fn check_unknown_component_contract_attribute(
    attribute: &HtmlAttribute,
    catalog: &AdminComponentContractCatalog,
    selector_attribute: &str,
    tag_name: &str,
    diagnostics: &mut Vec<Problem>,
) {
    let Some(name_token) = attribute.name() else {
        return;
    };
    let name = attribute_name(attribute.syntax());
    if name.is_empty() || name == selector_attribute {
        return;
    }
    if name == "v-bind" {
        let Some(inner) = attribute_inner(attribute) else {
            return;
        };
        let (fields, _) =
            vue_object_binding_fields(&inner.text().to_string(), inner.text_range().start().into());
        for field in &fields {
            check_unknown_component_object_binding_field(
                field,
                &catalog.props,
                &catalog.prop_names,
                tag_name,
                diagnostics,
            );
        }
        return;
    }
    if let Some(reference) = vue_model_reference_for_attribute(&name, name_token.text_range()) {
        append_unknown_admin_contract_problem(
            diagnostics,
            &reference,
            tag_name,
            "model",
            &catalog.models,
            &catalog.model_names,
            true,
        );
        return;
    }
    if let Some(reference) = vue_event_reference_for_attribute(&name, name_token.text_range()) {
        if !is_native_administration_event(&reference.name) {
            append_unknown_admin_contract_problem(
                diagnostics,
                &reference,
                tag_name,
                "event",
                &catalog.events,
                &catalog.event_names,
                false,
            );
        }
        return;
    }
    let Some(reference) = vue_prop_reference_for_attribute(&name, name_token.text_range()) else {
        return;
    };
    if is_administration_fallthrough_attribute(&name, &reference.name) {
        return;
    }
    append_unknown_admin_contract_problem(
        diagnostics,
        &reference,
        tag_name,
        "prop",
        &catalog.props,
        &catalog.prop_names,
        true,
    );
}

fn append_unknown_admin_contract_problem(
    diagnostics: &mut Vec<Problem>,
    reference: &VueAttributeReference,
    tag_name: &str,
    kind: &str,
    known: &HashSet<String>,
    names: &[String],
    kebab_suggestions: bool,
) {
    if known.contains(&reference.name) {
        return;
    }
    let mut suggestions = admin_nearby_suggestions(&reference.name, names);
    if kebab_suggestions {
        for suggestion in &mut suggestions {
            *suggestion = camel_to_kebab(suggestion);
        }
    }
    if suggestions.is_empty() {
        return;
    }
    let name_key = format!("{kind}Name");
    diagnostics.push(Problem {
        range: reference.range,
        message: format!(
            "Unknown {kind} '{}' on component '{tag_name}'",
            reference.name
        ),
        source: "shopware".into(),
        severity: Some(DiagnosticSeverity::WARNING),
        id: format!("admin.component.unknown-{kind}").as_str().into(),
        payload: Some(payload(vec![
            ("componentName", text(tag_name)),
            (name_key.as_str(), text(&reference.name)),
            ("suggestions", string_array(suggestions)),
        ])),
        ..Problem::default()
    });
}

fn check_unknown_component_object_binding_field(
    field: &VueObjectBindingField,
    known_props: &HashSet<String>,
    prop_names: &[String],
    tag_name: &str,
    diagnostics: &mut Vec<Problem>,
) {
    let name = normalize_prop_name(&field.name);
    if name.is_empty()
        || known_props.contains(&name)
        || is_administration_fallthrough_attribute(&field.name, &name)
    {
        return;
    }
    let mut suggestions = admin_nearby_suggestions(&name, prop_names);
    if field.name.contains('-') {
        for suggestion in &mut suggestions {
            *suggestion = camel_to_kebab(suggestion);
        }
    }
    if suggestions.is_empty() {
        return;
    }
    diagnostics.push(Problem {
        range: field.name_range,
        message: format!("Unknown v-bind prop '{name}' on component '{tag_name}'"),
        source: "shopware".into(),
        severity: Some(DiagnosticSeverity::WARNING),
        id: "admin.component.unknown-prop".into(),
        payload: Some(payload(vec![
            ("componentName", text(tag_name)),
            ("propName", text(&name)),
            ("suggestions", string_array(suggestions)),
        ])),
        ..Problem::default()
    });
}

fn is_administration_fallthrough_attribute(raw: &str, normalized: &str) -> bool {
    let raw = lowercase(raw.trim().trim_start_matches(':'));
    let raw = raw.strip_prefix("v-bind:").unwrap_or(&raw);
    let normalized_bytes = normalized.as_bytes();
    if raw.starts_with("data-")
        || raw.starts_with("aria-")
        || normalized.starts_with("on")
            && normalized_bytes.len() > 2
            && normalized_bytes[2].is_ascii_uppercase()
    {
        return true;
    }
    matches!(
        lowercase(normalized).as_str(),
        "accept"
            | "accesskey"
            | "action"
            | "alt"
            | "autocomplete"
            | "autofocus"
            | "checked"
            | "class"
            | "contenteditable"
            | "controls"
            | "crossorigin"
            | "disabled"
            | "draggable"
            | "enctype"
            | "for"
            | "form"
            | "height"
            | "hidden"
            | "href"
            | "id"
            | "inert"
            | "inputmode"
            | "is"
            | "key"
            | "lang"
            | "loop"
            | "max"
            | "maxlength"
            | "method"
            | "min"
            | "minlength"
            | "multiple"
            | "muted"
            | "name"
            | "nonce"
            | "novalidate"
            | "part"
            | "pattern"
            | "placeholder"
            | "playsinline"
            | "poster"
            | "preload"
            | "readonly"
            | "ref"
            | "rel"
            | "required"
            | "role"
            | "rows"
            | "selected"
            | "size"
            | "slot"
            | "spellcheck"
            | "src"
            | "step"
            | "style"
            | "tabindex"
            | "target"
            | "title"
            | "translate"
            | "type"
            | "value"
            | "width"
            | "wrap"
    )
}

fn is_native_administration_event(name: &str) -> bool {
    matches!(
        lowercase(name.trim()).as_str(),
        "abort"
            | "animationcancel"
            | "animationend"
            | "animationiteration"
            | "animationstart"
            | "auxclick"
            | "beforeinput"
            | "beforetoggle"
            | "blur"
            | "cancel"
            | "canplay"
            | "canplaythrough"
            | "change"
            | "click"
            | "close"
            | "compositionend"
            | "compositionstart"
            | "compositionupdate"
            | "contextmenu"
            | "copy"
            | "cuechange"
            | "cut"
            | "dblclick"
            | "drag"
            | "dragend"
            | "dragenter"
            | "dragleave"
            | "dragover"
            | "dragstart"
            | "drop"
            | "durationchange"
            | "emptied"
            | "ended"
            | "error"
            | "focus"
            | "focusin"
            | "focusout"
            | "formdata"
            | "fullscreenchange"
            | "fullscreenerror"
            | "gotpointercapture"
            | "input"
            | "invalid"
            | "keydown"
            | "keypress"
            | "keyup"
            | "load"
            | "loadeddata"
            | "loadedmetadata"
            | "loadstart"
            | "lostpointercapture"
            | "mousedown"
            | "mouseenter"
            | "mouseleave"
            | "mousemove"
            | "mouseout"
            | "mouseover"
            | "mouseup"
            | "paste"
            | "pause"
            | "play"
            | "playing"
            | "pointercancel"
            | "pointerdown"
            | "pointerenter"
            | "pointerleave"
            | "pointermove"
            | "pointerout"
            | "pointerover"
            | "pointerup"
            | "progress"
            | "ratechange"
            | "reset"
            | "resize"
            | "scroll"
            | "scrollend"
            | "securitypolicyviolation"
            | "seeked"
            | "seeking"
            | "select"
            | "selectionchange"
            | "selectstart"
            | "slotchange"
            | "stalled"
            | "submit"
            | "suspend"
            | "timeupdate"
            | "toggle"
            | "touchcancel"
            | "touchend"
            | "touchmove"
            | "touchstart"
            | "transitioncancel"
            | "transitionend"
            | "transitionrun"
            | "transitionstart"
            | "volumechange"
            | "waiting"
            | "wheel"
    )
}

fn component_model_prop_present(
    component: &VueComponent,
    prop_name: &str,
    attributes: &HashSet<String>,
) -> bool {
    for attribute in attributes {
        let Some(argument) = normalize_model_argument(attribute) else {
            continue;
        };
        if !argument.is_empty() {
            if component
                .component_prop(&argument)
                .is_some_and(|prop| prop.name == prop_name)
            {
                return true;
            }
            continue;
        }
        let mut model_prop_name = component.model_prop.as_str();
        if model_prop_name.is_empty() && !component.model_event.is_empty() {
            model_prop_name = "value";
        }
        if model_prop_name.is_empty() {
            if component.component_prop("modelValue").is_some() {
                model_prop_name = "modelValue";
            } else if component.component_prop("value").is_some() {
                model_prop_name = "value";
            }
        }
        if component
            .component_prop(model_prop_name)
            .is_some_and(|prop| prop.name == prop_name)
        {
            return true;
        }
    }
    false
}

fn check_static_prop_types(
    tag_name: &str,
    start_tag: &SyntaxNode,
    props: &HashMap<String, VueComponentProp>,
    diagnostics: &mut Vec<Problem>,
) {
    for attribute in twigquery::iterate_nodes(start_tag, &[twigsyntax::HTML_ATTRIBUTE]) {
        let name = attribute_name(&attribute);
        if name.starts_with(':') || name.starts_with("v-bind:") {
            continue;
        }
        let Some(prop) = props.get(&normalize_prop_name(&name)) else {
            continue;
        };
        if !static_prop_needs_binding(prop, &name, &attribute.text().to_string()) {
            continue;
        }
        diagnostics.push(Problem {
            range: attribute.range_trimmed_trivia(),
            message: format!(
                "Prop '{}' on component '{tag_name}' expects {}; bind the value with ':'",
                prop.name, prop.type_
            ),
            source: "shopware".into(),
            severity: Some(DiagnosticSeverity::WARNING),
            id: "admin.component.static-prop-type".into(),
            payload: Some(payload(vec![
                ("componentName", text(tag_name)),
                ("propName", text(&prop.name)),
                ("attributeName", text(&name)),
            ])),
            ..Problem::default()
        });
    }
}

fn check_static_prop_values(
    tag_name: &str,
    start_tag: &SyntaxNode,
    props: &HashMap<String, VueComponentProp>,
    diagnostics: &mut Vec<Problem>,
) {
    for attribute_node in twigquery::iterate_nodes(start_tag, &[twigsyntax::HTML_ATTRIBUTE]) {
        let name = attribute_name(&attribute_node);
        if name.is_empty()
            || name.starts_with(':')
            || name.starts_with("v-")
            || name.starts_with('@')
            || name.starts_with('#')
        {
            continue;
        }
        let Some(prop) = props.get(&normalize_prop_name(&name)) else {
            continue;
        };
        let (allowed, complete) = vue_prop_allowed_values(prop);
        if !complete || allowed.is_empty() {
            continue;
        }
        let Some(value) = HtmlAttribute::cast(attribute_node.clone()).and_then(|a| a.value())
        else {
            continue;
        };
        let (actual, value_range) = match value.inner() {
            Some(inner) => (
                inner.syntax().text().to_string(),
                inner.syntax().text_range(),
            ),
            None => {
                let (Some(opening), Some(closing)) = (value.opening_quote(), value.closing_quote())
                else {
                    continue;
                };
                (
                    String::new(),
                    TextRange::new(opening.text_range().end(), closing.text_range().start()),
                )
            }
        };
        if actual.contains("{{") || actual.contains("{%") || actual.contains("{#") {
            continue;
        }
        if allowed.contains(&actual) {
            continue;
        }
        if let Some(problem) =
            invalid_admin_prop_value_problem(tag_name, prop, &actual, value_range)
        {
            diagnostics.push(problem);
        }
    }
}

fn invalid_admin_prop_value_problem(
    tag_name: &str,
    prop: &VueComponentProp,
    actual: &str,
    range: TextRange,
) -> Option<Problem> {
    let (allowed, complete) = vue_prop_allowed_values(prop);
    if !complete || allowed.is_empty() || allowed.iter().any(|candidate| candidate == actual) {
        return None;
    }
    Some(Problem {
        range,
        message: format!(
            "Prop '{}' on component '{tag_name}' does not accept value {:?}",
            prop.name, actual
        ),
        source: "shopware".into(),
        severity: Some(DiagnosticSeverity::WARNING),
        id: "admin.component.invalid-prop-value".into(),
        payload: Some(payload(vec![
            ("componentName", text(tag_name)),
            ("propName", text(&prop.name)),
            ("value", text(actual)),
            ("suggestions", string_array(similar(actual, &allowed))),
            ("allowedValues", string_array(allowed)),
        ])),
        ..Problem::default()
    })
}

fn static_prop_needs_binding(
    prop: &VueComponentProp,
    attribute_name: &str,
    attribute_text: &str,
) -> bool {
    let prop_type = lowercase(prop.type_.trim());
    if prop_type.is_empty() || prop_type.contains("string") {
        return false;
    }
    let Some(equals) = attribute_text.find('=') else {
        return false;
    };
    if prop_type.contains("boolean") {
        let value = attribute_text[equals + 1..]
            .trim()
            .trim_matches(|character| character == '\'' || character == '"');
        return !value.is_empty() && value != attribute_name;
    }
    prop_type.contains("number")
        || prop_type.contains("array")
        || prop_type.contains("object")
        || prop_type.contains("function")
}

/// Present attribute names and whether a `v-bind` object could not be fully
/// enumerated.
fn tag_attributes(start_tag: &SyntaxNode) -> (HashSet<String>, bool) {
    let mut attributes = HashSet::new();
    let mut has_unknown_object_binding = false;
    for attribute in twigquery::iterate_nodes(start_tag, &[twigsyntax::HTML_ATTRIBUTE]) {
        let name = attribute_name(&attribute);
        if !name.is_empty() {
            attributes.insert(name.clone());
        }
        if name != "v-bind" {
            continue;
        }
        let Some(inner) = HtmlAttribute::cast(attribute.clone()).and_then(|a| attribute_inner(&a))
        else {
            continue;
        };
        let (names, complete) = vue_object_binding_names(inner.text().to_string().trim());
        attributes.extend(names);
        if !complete {
            has_unknown_object_binding = true;
        }
    }
    (attributes, has_unknown_object_binding)
}

/// The prop under its camelCase or kebab-case name, directly or through
/// `:`/`v-bind:` bindings.
fn is_prop_present(prop_name: &str, attributes: &HashSet<String>) -> bool {
    let kebab_name = camel_to_kebab(prop_name);
    [prop_name, kebab_name.as_str()].iter().any(|name| {
        attributes.contains(*name)
            || attributes.contains(&format!(":{name}"))
            || attributes.contains(&format!("v-bind:{name}"))
    })
}
