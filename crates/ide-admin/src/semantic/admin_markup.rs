//! Registry-backed Vue component markup in Administration templates.
//!
//! The provider deliberately stays out of JavaScript and TypeScript, where the
//! client's native language service remains authoritative.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use swlsp_admin::{
    AdminComponentIndexer, TwigVueBinding, TwigVueMember, TwigVueMemberAccess, VueComponent,
    VueComponentMemberKind, VueDynamicComponentSelector, normalize_event_name,
    normalize_model_argument, normalize_prop_name, normalize_slot_name,
    twig_dynamic_component_selector, twig_vue_binding_at_offset_in, twig_vue_binding_references,
    twig_vue_bindings, twig_vue_document_bindings, twig_vue_expression_member_accesses,
    twig_vue_expression_root_identifiers, vue_builtin_member,
    vue_directive_reference_for_attribute, vue_object_binding_fields,
};
use swlsp_lsp::{
    BoxError, Context, ProviderResult, SemanticToken, SemanticTokensProvider, SemanticTokensRequest,
};
use swlsp_lsp_protocol::{
    SEMANTIC_TOKEN_CLASS, SEMANTIC_TOKEN_FUNCTION, SEMANTIC_TOKEN_PROPERTY, SEMANTIC_TOKEN_VARIABLE,
};
use swlsp_parser_twig::ast::{AstNode, HtmlAttribute, HtmlEndingTag, HtmlStartingTag};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{HTML_ENDING_TAG, HTML_STARTING_TAG};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};

use crate::semantic::{sort_tokens, token};

/// Gives registry-backed Vue component markup semantic meaning in Twig and
/// Vue templates below `Resources/app/administration/`.
pub struct AdminMarkupProvider {
    index: Arc<AdminComponentIndexer>,
}

impl AdminMarkupProvider {
    pub fn new(index: Arc<AdminComponentIndexer>) -> Self {
        Self { index }
    }
}

fn supports_admin_markup(uri: &str) -> bool {
    let lower = uri.to_lowercase();
    (lower.ends_with(".twig") || lower.ends_with(".vue"))
        && uri
            .replace('\\', "/")
            .contains("/Resources/app/administration/")
}

impl SemanticTokensProvider for AdminMarkupProvider {
    fn get_semantic_tokens(
        &self,
        ctx: &Context,
        request: &SemanticTokensRequest,
    ) -> ProviderResult<Vec<SemanticToken>> {
        let document = &request.document;
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        if !supports_admin_markup(&document.uri) {
            return Ok(Vec::new());
        }
        let template_path = swlsp_uriutil::path(&document.uri).map_err(BoxError::from)?;
        let owner = self.index.get_component_for_document(
            &template_path,
            Some(&root),
            document.source(),
            Some(document.line_index()),
        )?;
        let mut collector = Collector {
            ctx,
            index: &self.index,
            source: document.source(),
            template_path,
            root,
            owner,
            registered_directives: HashSet::new(),
            effective: HashMap::new(),
            result: Vec::new(),
            seen_lexical: HashSet::new(),
            vue_bindings: std::cell::OnceCell::new(),
        };
        collector.load_directives()?;
        collector.collect_markup()?;
        collector.mark_existing_ranges();
        collector.collect_lexical_bindings()?;
        collector.collect_member_accesses()?;
        collector.collect_root_identifiers()?;
        let mut result = collector.result;
        sort_tokens(&mut result);
        Ok(result)
    }
}

struct Collector<'a> {
    ctx: &'a Context,
    index: &'a AdminComponentIndexer,
    source: &'a str,
    template_path: String,
    root: SyntaxNode,
    owner: Option<VueComponent>,
    registered_directives: HashSet<String>,
    /// Resolved tag names; `None` caches a miss.
    effective: HashMap<String, Option<VueComponent>>,
    result: Vec<SemanticToken>,
    seen_lexical: HashSet<TextRange>,
    /// Lexical Vue bindings of the document, computed once per request.
    vue_bindings: std::cell::OnceCell<Vec<TwigVueBinding>>,
}

impl Collector<'_> {
    fn append(&mut self, range: TextRange, token_type: u32) {
        self.result.push(token(range, token_type));
    }

    fn load_directives(&mut self) -> ProviderResult<()> {
        for directive in self
            .index
            .get_all_directives_for_template(&self.template_path)?
        {
            self.registered_directives.insert(directive.name);
        }
        Ok(())
    }

    fn mark_existing_ranges(&mut self) {
        for token in &self.result {
            self.seen_lexical.insert(token.range);
        }
    }

    fn resolve(&mut self, name: &str) -> ProviderResult<Option<VueComponent>> {
        if let Some(component) = self.effective.get(name) {
            return Ok(component.clone());
        }
        self.effective.insert(name.to_owned(), None);
        let component = self.index.get_component_for_template_tag(
            &self.template_path,
            name,
            self.owner.as_ref(),
        )?;
        self.effective.insert(name.to_owned(), component.clone());
        Ok(component)
    }

    // --- markup ---------------------------------------------------------

    fn collect_markup(&mut self) -> ProviderResult<()> {
        let nodes = twigquery::nodes(&self.root, &[HTML_STARTING_TAG, HTML_ENDING_TAG]);
        for node in nodes {
            self.ctx.check()?;
            if node.kind() == HTML_STARTING_TAG {
                self.collect_starting_tag(&node)?;
            } else {
                self.collect_ending_tag(&node)?;
            }
        }
        Ok(())
    }

    fn collect_starting_tag(&mut self, node: &SyntaxNode) -> ProviderResult<()> {
        let Some(start) = HtmlStartingTag::cast(node.clone()) else {
            return Ok(());
        };
        let Some(name) = start.name() else {
            return Ok(());
        };
        let (component_name, dynamic_contracts, selector, dynamic) =
            self.dynamic_component_contracts(node, name.text())?;
        let component = self.resolve(&component_name)?;
        if component.is_some() && !dynamic {
            self.append(name.text_range(), SEMANTIC_TOKEN_CLASS);
        }
        let slot_components = self.slot_components(&start, node)?;
        for attribute in start.attributes() {
            self.collect_attribute(
                &attribute,
                component.as_ref(),
                &dynamic_contracts,
                &slot_components,
                &selector,
                dynamic,
            );
        }
        Ok(())
    }

    fn dynamic_component_contracts(
        &mut self,
        node: &SyntaxNode,
        fallback_name: &str,
    ) -> ProviderResult<(String, Vec<VueComponent>, VueDynamicComponentSelector, bool)> {
        let Some(selector) = twig_dynamic_component_selector(node) else {
            return Ok((
                fallback_name.to_owned(),
                Vec::new(),
                VueDynamicComponentSelector::default(),
                false,
            ));
        };
        for candidate in &selector.candidates {
            if self.resolve(&candidate.name)?.is_some() {
                self.append(candidate.range, SEMANTIC_TOKEN_CLASS);
            }
        }
        let (resolved_selector, mut contracts, complete) =
            self.index.resolve_dynamic_component_contracts_for_owner(
                &self.template_path,
                &selector,
                self.owner.as_ref(),
                Some(node),
            )?;
        if !complete {
            contracts.clear();
        }
        let mut component_name = fallback_name.to_owned();
        if contracts.len() == 1 {
            component_name = contracts[0].name.clone();
        } else {
            let names = resolved_selector.names();
            if names.len() == 1 {
                component_name = names[0].clone();
            }
        }
        Ok((component_name, contracts, selector, true))
    }

    fn slot_components(
        &self,
        start: &HtmlStartingTag,
        node: &SyntaxNode,
    ) -> ProviderResult<Vec<VueComponent>> {
        let has_slot_attribute = start
            .attributes()
            .any(|attribute| !normalize_slot_name(&attribute_name(&attribute)).is_empty());
        if !has_slot_attribute {
            return Ok(Vec::new());
        }
        let (components, complete) = self.index.resolve_twig_slot_consumer_components(
            &self.template_path,
            node,
            self.owner.as_ref(),
        )?;
        Ok(if complete { components } else { Vec::new() })
    }

    fn collect_attribute(
        &mut self,
        attribute: &HtmlAttribute,
        component: Option<&VueComponent>,
        dynamic_contracts: &[VueComponent],
        slot_components: &[VueComponent],
        selector: &VueDynamicComponentSelector,
        dynamic: bool,
    ) {
        let Some(name_token) = attribute.name() else {
            return;
        };
        let name_range = name_token.text_range();
        let name = attribute_name(attribute);
        if let Some(directive) = vue_directive_reference_for_attribute(&name, name_range)
            && self.registered_directives.contains(&directive.name)
        {
            self.append(name_range, SEMANTIC_TOKEN_FUNCTION);
        }
        if dynamic && name == selector.attribute_name {
            return;
        }
        if name == "v-bind" {
            self.collect_object_binding(attribute, component, dynamic_contracts);
        }
        if let Some(token_type) =
            attribute_token_type(&name, component, dynamic_contracts, slot_components)
        {
            self.append(name_range, token_type);
        }
    }

    fn collect_object_binding(
        &mut self,
        attribute: &HtmlAttribute,
        component: Option<&VueComponent>,
        dynamic_contracts: &[VueComponent],
    ) {
        let Some(inner) = attribute.value().and_then(|value| value.inner()) else {
            return;
        };
        let syntax = inner.syntax();
        let (fields, _) = vue_object_binding_fields(
            &syntax.text().to_string(),
            u32::from(syntax.text_range().start()),
        );
        for field in fields {
            let matched = if dynamic_contracts.len() > 1 {
                common_attribute_semantic_type(&field.name, dynamic_contracts).is_some()
            } else {
                attribute_semantic_type(&field.name, component, component).is_some()
            };
            if matched {
                self.append(field.name_range, SEMANTIC_TOKEN_PROPERTY);
            }
        }
    }

    fn collect_ending_tag(&mut self, node: &SyntaxNode) -> ProviderResult<()> {
        let Some(ending) = HtmlEndingTag::cast(node.clone()) else {
            return Ok(());
        };
        let Some(name) = ending.name() else {
            return Ok(());
        };
        if self.resolve(name.text())?.is_some() {
            self.append(name.text_range(), SEMANTIC_TOKEN_CLASS);
        }
        Ok(())
    }

    // --- expression scope -----------------------------------------------

    fn collect_lexical_bindings(&mut self) -> ProviderResult<()> {
        for binding in twig_vue_bindings(&self.root, self.source) {
            self.ctx.check()?;
            for range in twig_vue_binding_references(&self.root, self.source, &binding) {
                if !self.seen_lexical.insert(range) {
                    continue;
                }
                self.append(range, SEMANTIC_TOKEN_VARIABLE);
            }
        }
        Ok(())
    }

    fn collect_member_accesses(&mut self) -> ProviderResult<()> {
        for access in twig_vue_expression_member_accesses(&self.root, self.source) {
            self.ctx.check()?;
            let matched = self.member_access_matches_contract(&access)?;
            if !matched || self.seen_lexical.contains(&access.member_range) {
                continue;
            }
            self.seen_lexical.insert(access.member_range);
            self.append(access.member_range, SEMANTIC_TOKEN_PROPERTY);
        }
        Ok(())
    }

    fn node_at(&self, offset: TextRange) -> SyntaxNode {
        self.root
            .node_at(offset.start())
            .unwrap_or_else(|| self.root.clone())
    }

    fn member_access_matches_contract(&self, access: &TwigVueMemberAccess) -> ProviderResult<bool> {
        let node = self.node_at(access.member_range);
        let resolved_slot = self.index.resolve_twig_scoped_slot_member_for_owner(
            &self.root,
            &node,
            self.source,
            u32::from(access.member_range.start()),
            &self.template_path,
            self.owner.as_ref(),
        )?;
        let resolved_vue = self.vue_binding_at(u32::from(access.root_range.start()));
        let lexical_member = resolved_vue.as_ref().is_some_and(|vue| {
            resolved_slot
                .as_ref()
                .is_none_or(|slot| vue.scope_range.len() <= slot.scoped.scope.template_range.len())
        });
        if lexical_member {
            return Ok(true);
        }
        if resolved_slot.as_ref().is_some_and(|slot| slot.member_found) {
            return Ok(true);
        }
        let resolved_instance = self.index.resolve_twig_vue_instance_member_for_component(
            &self.root,
            self.source,
            u32::from(access.member_range.start()),
            &self.template_path,
            self.owner.as_ref(),
        )?;
        Ok(resolved_instance.is_some_and(|instance| instance.member_found))
    }

    fn collect_root_identifiers(&mut self) -> ProviderResult<()> {
        if self.owner.is_none() {
            return Ok(());
        }
        for identifier in twig_vue_expression_root_identifiers(&self.root, self.source) {
            self.ctx.check()?;
            if self.seen_lexical.contains(&identifier.range)
                || self.is_lexical_root_identifier(&identifier)
            {
                continue;
            }
            let Some(token_type) = self.root_identifier_token_type(&identifier)? else {
                continue;
            };
            self.seen_lexical.insert(identifier.range);
            self.append(identifier.range, token_type);
        }
        Ok(())
    }

    fn is_lexical_root_identifier(&self, identifier: &TwigVueMember) -> bool {
        self.vue_binding_at(u32::from(identifier.range.start()))
            .is_some()
    }

    fn vue_binding_at(&self, offset: u32) -> Option<TwigVueBinding> {
        let bindings = self
            .vue_bindings
            .get_or_init(|| twig_vue_document_bindings(&self.root, self.source));
        twig_vue_binding_at_offset_in(bindings, &self.root, self.source, offset)
    }

    fn root_identifier_token_type(
        &self,
        identifier: &TwigVueMember,
    ) -> ProviderResult<Option<u32>> {
        let node = self.node_at(identifier.range);
        let slot_binding = self.index.resolve_twig_scoped_slot_binding_for_owner(
            &self.root,
            &node,
            self.source,
            u32::from(identifier.range.start()),
            &self.template_path,
            self.owner.as_ref(),
        )?;
        if slot_binding.is_some() {
            return Ok(Some(SEMANTIC_TOKEN_VARIABLE));
        }
        let Some(owner) = self.owner.as_ref() else {
            return Ok(None);
        };
        let Some(member) = owner
            .template_member(&identifier.name)
            .or_else(|| vue_builtin_member(&identifier.name))
        else {
            return Ok(None);
        };
        Ok(Some(if member.kind == VueComponentMemberKind::Method {
            SEMANTIC_TOKEN_FUNCTION
        } else {
            SEMANTIC_TOKEN_VARIABLE
        }))
    }
}

fn attribute_name(attribute: &HtmlAttribute) -> String {
    twigquery::html_attribute_name(attribute.syntax()).unwrap_or_default()
}

fn attribute_token_type(
    name: &str,
    component: Option<&VueComponent>,
    dynamic_contracts: &[VueComponent],
    slot_components: &[VueComponent],
) -> Option<u32> {
    if !normalize_slot_name(name).is_empty() {
        match slot_components {
            [] => {}
            [single] => return attribute_semantic_type(name, None, Some(single)),
            _ => return common_attribute_semantic_type(name, slot_components),
        }
    }
    if dynamic_contracts.len() > 1 {
        return common_attribute_semantic_type(name, dynamic_contracts);
    }
    attribute_semantic_type(name, component, component)
}

fn common_attribute_semantic_type(name: &str, components: &[VueComponent]) -> Option<u32> {
    let mut common = None;
    for component in components {
        let token_type = attribute_semantic_type(name, Some(component), Some(component))?;
        if common.is_some_and(|common| common != token_type) {
            return None;
        }
        common = Some(token_type);
    }
    common
}

fn attribute_semantic_type(
    name: &str,
    component: Option<&VueComponent>,
    slot_component: Option<&VueComponent>,
) -> Option<u32> {
    let slot_name = normalize_slot_name(name);
    if !slot_name.is_empty()
        && let Some(slot_component) = slot_component
    {
        return slot_component
            .component_slot(&slot_name)
            .map(|_| SEMANTIC_TOKEN_PROPERTY);
    }
    let component = component?;
    if normalize_model_argument(name).is_some() {
        return component
            .component_model(name)
            .map(|_| SEMANTIC_TOKEN_PROPERTY);
    }
    let event = normalize_event_name(name);
    if !event.is_empty() {
        return component
            .component_event(&event)
            .map(|_| SEMANTIC_TOKEN_FUNCTION);
    }
    let name = match name.find('.') {
        Some(modifier) => &name[..modifier],
        None => name,
    };
    let prop_name = normalize_prop_name(name);
    component
        .props
        .iter()
        .any(|prop| prop.name == prop_name)
        .then_some(SEMANTIC_TOKEN_PROPERTY)
}
