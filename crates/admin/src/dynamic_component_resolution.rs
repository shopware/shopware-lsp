//! Dynamic component selector resolution against the effective component
//! graph.

use crate::*;

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub(crate) struct DynamicComponentNames {
    pub names: Vec<String>,
    pub found: bool,
    pub complete: bool,
}

impl AdminComponentIndexer {
    /// Augments the selector syntax with component names recoverable from
    /// the owning effective Vue component. `context` is the selector's start
    /// tag, used for `<router-view>` slot bindings.
    pub fn resolve_dynamic_component_selector(
        &self,
        template_path: &str,
        selector: &VueDynamicComponentSelector,
        context: Option<&SyntaxNode>,
    ) -> IndexResult<(VueDynamicComponentSelector, bool)> {
        self.resolve_dynamic_component_selector_impl(template_path, selector, None, context)
    }

    /// Resolves an inferred selector against the request-local owner of an
    /// open Vue document.
    pub fn resolve_dynamic_component_selector_for_owner(
        &self,
        template_path: &str,
        selector: &VueDynamicComponentSelector,
        owner: Option<&VueComponent>,
        context: Option<&SyntaxNode>,
    ) -> IndexResult<(VueDynamicComponentSelector, bool)> {
        self.resolve_dynamic_component_selector_impl(template_path, selector, owner, context)
    }

    pub(crate) fn resolve_dynamic_component_selector_impl(
        &self,
        template_path: &str,
        selector: &VueDynamicComponentSelector,
        component: Option<&VueComponent>,
        context: Option<&SyntaxNode>,
    ) -> IndexResult<(VueDynamicComponentSelector, bool)> {
        if selector.complete && !selector.names().is_empty() {
            return Ok((selector.clone(), true));
        }
        let fetched;
        let component = match component {
            Some(component) => component,
            None => {
                fetched = self.get_component_by_template_path(template_path)?;
                match fetched.as_ref() {
                    Some(component) => component,
                    None => return Ok((selector.clone(), false)),
                }
            }
        };
        let mut resolved = self.resolve_dynamic_component_names(
            component,
            &selector.expression,
            &mut HashSet::new(),
        );
        if (!resolved.found || resolved.names.is_empty())
            && let Some(context) = context
        {
            let route_resolved =
                self.resolve_router_view_dynamic_component_names(component, selector, context)?;
            if route_resolved.found {
                resolved = route_resolved;
            }
        }
        if !resolved.found || resolved.names.is_empty() {
            return Ok((selector.clone(), false));
        }
        let mut result = selector.clone();
        result.candidates = resolved
            .names
            .into_iter()
            .map(|name| VueDynamicComponentCandidate {
                name,
                ..Default::default()
            })
            .collect();
        result.complete = resolved.complete;
        Ok((result, true))
    }

    /// Every concrete component behind a closed inline or inferred selector.
    pub fn resolve_dynamic_component_contracts(
        &self,
        template_path: &str,
        selector: &VueDynamicComponentSelector,
        context: Option<&SyntaxNode>,
    ) -> IndexResult<(VueDynamicComponentSelector, Vec<VueComponent>, bool)> {
        self.resolve_dynamic_component_contracts_impl(template_path, selector, None, context)
    }

    /// The live-document counterpart of
    /// [`Self::resolve_dynamic_component_contracts`].
    pub fn resolve_dynamic_component_contracts_for_owner(
        &self,
        template_path: &str,
        selector: &VueDynamicComponentSelector,
        owner: Option<&VueComponent>,
        context: Option<&SyntaxNode>,
    ) -> IndexResult<(VueDynamicComponentSelector, Vec<VueComponent>, bool)> {
        self.resolve_dynamic_component_contracts_impl(template_path, selector, owner, context)
    }

    pub(crate) fn resolve_dynamic_component_contracts_impl(
        &self,
        template_path: &str,
        selector: &VueDynamicComponentSelector,
        owner: Option<&VueComponent>,
        context: Option<&SyntaxNode>,
    ) -> IndexResult<(VueDynamicComponentSelector, Vec<VueComponent>, bool)> {
        let (resolved_selector, _) =
            self.resolve_dynamic_component_selector_impl(template_path, selector, owner, context)?;
        let (components, complete) =
            match self.resolve_dynamic_components(template_path, &resolved_selector, owner)? {
                Some(components) => (components, true),
                None => (Vec::new(), false),
            };
        Ok((resolved_selector, components, complete))
    }

    /// Joins Vue Router's `Component` slot binding with the indexed module
    /// route tree: a router-view in the component of route A renders the
    /// components of A's direct child routes.
    pub(crate) fn resolve_router_view_dynamic_component_names(
        &self,
        owner: &VueComponent,
        selector: &VueDynamicComponentSelector,
        start_tag: &SyntaxNode,
    ) -> IndexResult<DynamicComponentNames> {
        if !twig_dynamic_component_uses_router_view(start_tag, selector) {
            return Ok(DynamicComponentNames::default());
        }
        self.resolve_router_view_route_component_names(owner)
    }

    pub(crate) fn resolve_router_view_route_component_names(
        &self,
        owner: &VueComponent,
    ) -> IndexResult<DynamicComponentNames> {
        let routes = self.get_all_module_routes()?;
        let owner_name = camel_to_kebab(&owner.name);
        let host_routes: BTreeSet<&str> = routes
            .iter()
            .filter(|route| {
                camel_to_kebab(&route.component) == owner_name && !route.name.is_empty()
            })
            .map(|route| route.name.as_str())
            .collect();
        if host_routes.is_empty() {
            return Ok(DynamicComponentNames::default());
        }
        let mut result = DynamicComponentNames {
            found: true,
            complete: true,
            ..Default::default()
        };
        for route in &routes {
            if route.component.is_empty() {
                continue;
            }
            for host in &host_routes {
                let Some(suffix) = route
                    .name
                    .strip_prefix(*host)
                    .and_then(|rest| rest.strip_prefix('.'))
                else {
                    continue;
                };
                if suffix.is_empty() || suffix.contains('.') {
                    continue;
                }
                result.names = append_unique_dynamic_names(
                    result.names,
                    std::slice::from_ref(&route.component),
                );
                break;
            }
        }
        if result.names.is_empty() {
            return Ok(DynamicComponentNames::default());
        }
        result.names.sort();
        Ok(result)
    }
}

pub(crate) fn twig_dynamic_component_uses_router_view(
    start_tag: &SyntaxNode,
    selector: &VueDynamicComponentSelector,
) -> bool {
    if selector.expression_range.is_empty() {
        return false;
    }
    let Some(path) = vue_static_template_expression(&selector.expression) else {
        return false;
    };
    if path.is_empty() || path.iter().any(|segment| segment.indexed || segment.called) {
        return false;
    }
    let root = start_tag
        .ancestors()
        .last()
        .unwrap_or_else(|| start_tag.clone());
    let Some(scope) =
        twig_scoped_slot_at_offset(&root, u32::from(selector.expression_range.start()))
    else {
        return false;
    };
    scope.component_name == "router-view" && router_view_component_path(&scope, &path)
}

pub(crate) fn router_view_component_path(
    scope: &TwigScopedSlot,
    path: &[VueStaticExpressionSegment],
) -> bool {
    for binding in &scope.bindings {
        if binding.whole_object {
            if path.len() == 2 && path[0].name == binding.local_name && path[1].name == "Component"
            {
                return true;
            }
            continue;
        }
        if path.len() == 1
            && binding.member_name == "Component"
            && path[0].name == binding.local_name
        {
            return true;
        }
    }
    false
}

impl AdminComponentIndexer {
    pub(crate) fn resolve_dynamic_component_names(
        &self,
        component: &VueComponent,
        expression: &str,
        resolving: &mut HashSet<String>,
    ) -> DynamicComponentNames {
        let expression = unwrap_vue_expression_parentheses(&trim_vue_source_expression(expression));
        let expression = expression.as_str();
        if expression.is_empty() {
            return DynamicComponentNames::default();
        }
        if let Some((name, _, _)) = static_dynamic_component_string(expression) {
            return DynamicComponentNames {
                names: vec![name],
                found: true,
                complete: true,
            };
        }
        if matches!(expression, "null" | "undefined" | "false") {
            return DynamicComponentNames {
                found: true,
                complete: true,
                ..Default::default()
            };
        }
        if let Some((true_start, true_end, false_start, false_end)) =
            split_dynamic_component_conditional(expression)
        {
            let left = self.resolve_dynamic_component_names(
                component,
                &expression[true_start..true_end],
                resolving,
            );
            let right = self.resolve_dynamic_component_names(
                component,
                &expression[false_start..false_end],
                resolving,
            );
            return merge_dynamic_component_names(&left, &right);
        }
        for operator in ["??", "||"] {
            if let Some((left, right)) = split_vue_top_level_operator(expression, operator) {
                let left = self.resolve_dynamic_component_names(component, &left, resolving);
                let right = self.resolve_dynamic_component_names(component, &right, resolving);
                return merge_dynamic_component_names(&left, &right);
            }
        }
        let cms_names = self.resolve_cms_dynamic_component_names(component, expression, resolving);
        if cms_names.found {
            return cms_names;
        }
        let Some(path) = vue_static_template_expression(expression) else {
            return DynamicComponentNames::default();
        };
        if path.len() != 1 || path[0].indexed {
            return DynamicComponentNames::default();
        }
        if !path[0].called {
            for local in &component.local_components {
                if path[0].name != local.symbol
                    && path[0].name != local.name
                    && camel_to_kebab(&path[0].name) != local.name
                {
                    continue;
                }
                return DynamicComponentNames {
                    names: vec![local.name.clone()],
                    found: true,
                    complete: true,
                };
            }
        }
        let Some(member) = component.template_member(&path[0].name) else {
            return DynamicComponentNames::default();
        };
        let is_method = member.kind == VueComponentMemberKind::Method;
        if is_method && !path[0].called || !is_method && path[0].called {
            return DynamicComponentNames::default();
        }
        let key = format!("{}\x00{}", member.kind.as_str(), member.name);
        if resolving.contains(&key) {
            return DynamicComponentNames::default();
        }
        resolving.insert(key.clone());
        let result = self.resolve_dynamic_member_names(component, &member, resolving);
        resolving.remove(&key);
        result
    }

    fn resolve_dynamic_member_names(
        &self,
        component: &VueComponent,
        member: &VueComponentMember,
        resolving: &mut HashSet<String>,
    ) -> DynamicComponentNames {
        let type_names = dynamic_component_names_from_type(&member_value_type(member));
        if type_names.found && type_names.complete {
            return type_names;
        }
        if member.kind == VueComponentMemberKind::Data {
            let mut result = DynamicComponentNames::default();
            if !member.source_expression.is_empty() {
                result = self.resolve_dynamic_component_names(
                    component,
                    &member.source_expression,
                    resolving,
                );
            }
            for assignment in &component.assignments {
                if assignment.target != member.name || assignment.expression.is_empty() {
                    continue;
                }
                let assigned = self.resolve_dynamic_component_names(
                    component,
                    &assignment.expression,
                    resolving,
                );
                result = merge_dynamic_component_names(&result, &assigned);
            }
            result.complete = false;
            if result.found {
                return result;
            }
        }
        if !member.source_expression.is_empty() {
            let resolved = self.resolve_dynamic_component_names(
                component,
                &member.source_expression,
                resolving,
            );
            if resolved.found {
                return resolved;
            }
        }
        if !member.return_expressions.is_empty() {
            let mut result = DynamicComponentNames {
                found: true,
                complete: member.returns_complete,
                ..Default::default()
            };
            for alternative in &member.return_expressions {
                let resolved =
                    self.resolve_dynamic_component_names(component, alternative, resolving);
                result.names = append_unique_dynamic_names(result.names, &resolved.names);
                if !resolved.found || !resolved.complete {
                    result.complete = false;
                }
            }
            return result;
        }
        if member.kind == VueComponentMemberKind::Prop {
            for prop in &component.props {
                if prop.name != member.name || prop.default.is_empty() {
                    continue;
                }
                let mut resolved =
                    self.resolve_dynamic_component_names(component, &prop.default, resolving);
                resolved.complete = false;
                return resolved;
            }
        }
        DynamicComponentNames::default()
    }

    pub(crate) fn resolve_cms_dynamic_component_names(
        &self,
        component: &VueComponent,
        expression: &str,
        resolving: &mut HashSet<String>,
    ) -> DynamicComponentNames {
        let Some((field, base)) = cms_component_field_expression(expression) else {
            return DynamicComponentNames::default();
        };
        let root_name = vue_expression_root_name(&base);
        if root_name.is_empty() {
            return DynamicComponentNames::default();
        }
        let Some(member) = component.template_member(&root_name) else {
            return DynamicComponentNames::default();
        };
        let Some(kind) = cms_registry_kind_from_member(component, &member, resolving) else {
            return DynamicComponentNames::default();
        };
        let registrations = match self.get_all_cms_registrations_by_kind(kind) {
            Ok(registrations) if !registrations.is_empty() => registrations,
            _ => return DynamicComponentNames::default(),
        };
        let mut result = DynamicComponentNames {
            found: true,
            complete: true,
            ..Default::default()
        };
        for registration in &registrations {
            let name = match field {
                "component" => registration.component.clone(),
                "configComponent" => registration.config_component.clone(),
                "previewComponent" => registration.preview_component.clone(),
                _ => String::new(),
            };
            result.names = append_unique_dynamic_names(result.names, &[name]);
        }
        if result.names.is_empty() {
            return DynamicComponentNames::default();
        }
        result
    }
}

/// `(field, base)` for `base.component`, `base?.configComponent`, ...
pub(crate) fn cms_component_field_expression(expression: &str) -> Option<(&'static str, String)> {
    let expression = unwrap_vue_expression_parentheses(&trim_vue_source_expression(expression));
    for field in ["configComponent", "previewComponent", "component"] {
        for separator in ["?.", "."] {
            let suffix = format!("{separator}{field}");
            if let Some(base) = expression.strip_suffix(&suffix) {
                let base = base.trim();
                if base.is_empty() {
                    return None;
                }
                return Some((field, base.to_owned()));
            }
        }
    }
    None
}

pub(crate) fn vue_expression_root_name(expression: &str) -> String {
    let expression = expression.trim();
    let expression = expression.strip_prefix("this.").unwrap_or(expression);
    let bytes = expression.as_bytes();
    let mut end = 0;
    while end < bytes.len() {
        let character = bytes[end];
        if end == 0 && !is_vue_identifier_start(character)
            || end > 0 && !is_vue_identifier_part(character)
        {
            break;
        }
        end += 1;
    }
    expression[..end].to_owned()
}

pub(crate) fn cms_registry_kind_from_member(
    component: &VueComponent,
    member: &VueComponentMember,
    resolving: &mut HashSet<String>,
) -> Option<AdminCMSRegistrationKind> {
    if !member.cms_registry_kind.is_unset() {
        return Some(member.cms_registry_kind);
    }
    let key = format!("cms\x00{}\x00{}", member.kind.as_str(), member.name);
    if resolving.contains(&key) {
        return None;
    }
    resolving.insert(key.clone());
    let result = (|| {
        let expressions =
            std::iter::once(&member.source_expression).chain(member.return_expressions.iter());
        for expression in expressions {
            if let Some(kind) = cms_registry_kind_from_expression(expression) {
                return Some(kind);
            }
            let root_name = vue_expression_root_name(expression);
            if root_name.is_empty() || root_name == member.name {
                continue;
            }
            let Some(root) = component.template_member(&root_name) else {
                continue;
            };
            if let Some(kind) = cms_registry_kind_from_member(component, &root, resolving) {
                return Some(kind);
            }
        }
        None
    })();
    resolving.remove(&key);
    result
}

pub(crate) fn cms_registry_kind_from_expression(
    expression: &str,
) -> Option<AdminCMSRegistrationKind> {
    if expression.contains("getCmsElementConfigByName(")
        || expression.contains("getCmsElementRegistry(")
        || expression.contains(".elementRegistry")
    {
        return Some(AdminCMSRegistrationKind::Element);
    }
    if expression.contains("getCmsBlockConfigByName(")
        || expression.contains("getCmsBlockRegistry(")
        || expression.contains(".blockRegistry")
    {
        return Some(AdminCMSRegistrationKind::Block);
    }
    None
}

pub(crate) fn member_value_type(member: &VueComponentMember) -> String {
    if member.kind == VueComponentMemberKind::Method {
        return vue_callable_return_type(&member.type_);
    }
    member.type_.clone()
}

pub(crate) fn dynamic_component_names_from_type(value: &str) -> DynamicComponentNames {
    let value = value.trim();
    if value.is_empty() {
        return DynamicComponentNames::default();
    }
    let mut result = DynamicComponentNames {
        found: true,
        complete: true,
        ..Default::default()
    };
    for alternative in split_admin_type_top_level(value, b'|') {
        let alternative = alternative.trim();
        let name = admin_type_string_literal(alternative);
        if !name.is_empty() {
            result.names = append_unique_dynamic_names(result.names, &[name]);
            continue;
        }
        match alternative {
            "null" | "undefined" | "false" | "never" => continue,
            _ => return DynamicComponentNames::default(),
        }
    }
    result
}

pub(crate) fn merge_dynamic_component_names(
    left: &DynamicComponentNames,
    right: &DynamicComponentNames,
) -> DynamicComponentNames {
    DynamicComponentNames {
        names: append_unique_dynamic_names(left.names.clone(), &right.names),
        found: left.found || right.found,
        complete: left.found && right.found && left.complete && right.complete,
    }
}

pub(crate) fn append_unique_dynamic_names(
    mut values: Vec<String>,
    additions: &[String],
) -> Vec<String> {
    let mut seen: HashSet<String> = values.iter().cloned().collect();
    for addition in additions {
        if addition.is_empty() || seen.contains(addition) {
            continue;
        }
        seen.insert(addition.clone());
        values.push(addition.clone());
    }
    values
}
