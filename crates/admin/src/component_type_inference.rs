//! Late type inference for effective components (port of Go
//! `component_type_inference.go`).

use std::sync::LazyLock;

use regex::Regex;

use crate::*;

static VUE_REPOSITORY_CREATE_EXPRESSION_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r#"^this\.repositoryFactory\.create(?:\?\.)?\(['"]([^'"]+)['"]"#)
        .expect("valid regex")
});

/// One static member, call or index operation of a JavaScript chain.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub(crate) struct VueStaticExpressionSegment {
    pub name: String,
    pub called: bool,
    pub optional: bool,
    pub indexed: bool,
    pub arguments: String,
    pub index_expression: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub(crate) struct ResolvedVueExpressionType {
    pub type_: String,
    pub context_path: String,
    pub open_runtime_shape: bool,
}

/// Go `(resolvedVueExpressionType, bool, error)`. The value is kept even
/// when not found because Go merges partial chain results for `??`.
pub(crate) type ResolvedExpression = IndexResult<(ResolvedVueExpressionType, bool)>;

fn resolved(type_: String, context_path: &str) -> ResolvedVueExpressionType {
    ResolvedVueExpressionType {
        type_,
        context_path: context_path.to_owned(),
        open_runtime_shape: false,
    }
}

fn not_found() -> ResolvedExpression {
    Ok((ResolvedVueExpressionType::default(), false))
}

fn skip_javascript_spaces_from(bytes: &[u8], mut cursor: usize) -> usize {
    while cursor < bytes.len() && is_javascript_space(bytes[cursor]) {
        cursor += 1;
    }
    cursor
}

impl AdminComponentIndexer {
    /// Resolves legacy JavaScript return expressions after inheritance and
    /// mixins have been composed.
    pub(crate) fn enrich_effective_component_member_types(
        &self,
        component: &mut VueComponent,
    ) -> IndexResult<()> {
        if component.members.is_empty() {
            return Ok(());
        }
        for _pass in 0..component.members.len() {
            let mut changed = false;
            for member_index in 0..component.members.len() {
                let member = &component.members[member_index];
                if member.source_expression.is_empty()
                    || !component_member_needs_inferred_type(member)
                {
                    continue;
                }
                let context = component_member_type_context(member, component);
                let expression = member.source_expression.clone();
                let (resolved, found) =
                    self.resolve_component_expression_type(component, &expression, &context)?;
                if !found || resolved.type_.is_empty() {
                    continue;
                }
                let member = &mut component.members[member_index];
                let mut inferred_type = resolved.type_.clone();
                if member.kind == VueComponentMemberKind::Method {
                    inferred_type = vue_callable_with_return_type(&member.type_, &resolved.type_);
                }
                let context_changed = !resolved.context_path.is_empty()
                    && member.type_context_path != resolved.context_path;
                if member.type_ == inferred_type && !context_changed {
                    continue;
                }
                member.type_ = inferred_type;
                member.open_runtime_shape =
                    member.open_runtime_shape || resolved.open_runtime_shape;
                if !resolved.context_path.is_empty() {
                    member.type_context_path = resolved.context_path;
                }
                changed = true;
            }
            let flow_changed = self.enrich_effective_component_assignment_types(component)?;
            if !(changed || flow_changed) {
                break;
            }
        }
        Ok(())
    }

    pub(crate) fn enrich_effective_component_assignment_types(
        &self,
        component: &mut VueComponent,
    ) -> IndexResult<bool> {
        if component.assignments.is_empty() {
            return Ok(false);
        }
        let mut assignments: HashMap<String, Vec<VueComponentAssignment>> = HashMap::new();
        for assignment in &component.assignments {
            if !assignment.target.is_empty() && !assignment.expression.is_empty() {
                assignments
                    .entry(assignment.target.clone())
                    .or_default()
                    .push(assignment.clone());
            }
        }
        let mut changed = false;
        for member_index in 0..component.members.len() {
            let member = component.members[member_index].clone();
            let Some(writes) = assignments.get(&member.name) else {
                continue;
            };
            if member.kind != VueComponentMemberKind::Data || writes.is_empty() {
                continue;
            }

            let mut initial_type = String::new();
            if !member.source_expression.is_empty() {
                let context = component_member_type_context(&member, component);
                let (resolved, found) = self.resolve_component_expression_type(
                    component,
                    &member.source_expression,
                    &context,
                )?;
                if found {
                    initial_type = resolved.type_;
                }
            }
            if initial_type.is_empty() && is_weak_vue_flow_seed(&member.type_) {
                initial_type = member.type_.clone();
            }
            if !member.type_.is_empty()
                && !is_weak_vue_flow_seed(&initial_type)
                && !is_weak_vue_flow_seed(&member.type_)
            {
                continue;
            }

            let mut assignment_types = Vec::with_capacity(writes.len());
            let mut context_path = String::new();
            let mut open_runtime_shape = false;
            for assignment in writes {
                let assignment_context = if assignment.file_path.is_empty() {
                    component_member_type_context(&member, component)
                } else {
                    assignment.file_path.clone()
                };
                let (resolved, found) = self.resolve_component_expression_type(
                    component,
                    &assignment.expression,
                    &assignment_context,
                )?;
                if !found || resolved.type_.is_empty() {
                    continue;
                }
                assignment_types.push(resolved.type_);
                open_runtime_shape = open_runtime_shape
                    || vue_runtime_object_literal_expression(&assignment.expression);
                if context_path.is_empty() {
                    context_path = resolved.context_path;
                }
            }
            let flow_type = merge_vue_assignment_types(&initial_type, &assignment_types);
            let member = &mut component.members[member_index];
            if open_runtime_shape && !member.open_runtime_shape {
                member.open_runtime_shape = true;
                changed = true;
            }
            if flow_type.is_empty() || flow_type == member.type_ {
                continue;
            }
            member.type_ = flow_type;
            if !context_path.is_empty() {
                member.type_context_path = context_path;
            }
            changed = true;
        }
        Ok(changed)
    }
}

fn is_weak_type_entry(entry: &str) -> bool {
    matches!(
        entry,
        "" | "any"
            | "unknown"
            | "null"
            | "undefined"
            | "Array"
            | "Object"
            | "Array<any>"
            | "Array<unknown>"
            | "ReadonlyArray<any>"
            | "ReadonlyArray<unknown>"
    )
}

pub(crate) fn is_weak_vue_flow_seed(value: &str) -> bool {
    let value = value.trim();
    if value.is_empty() {
        return true;
    }
    split_admin_type_top_level(value, b'|')
        .iter()
        .all(|entry| is_weak_type_entry(entry.trim()))
}

pub(crate) fn merge_vue_assignment_types(initial_type: &str, assignments: &[String]) -> String {
    let mut alternatives: Vec<String> = Vec::new();
    let mut seen = HashSet::new();
    let mut informative = false;
    let mut append_type = |value: &str| {
        for entry in split_admin_type_top_level(value, b'|') {
            let entry = entry.trim();
            if entry.is_empty() || !seen.insert(entry.to_owned()) {
                continue;
            }
            alternatives.push(entry.to_owned());
            if !is_weak_type_entry(entry) {
                informative = true;
            }
        }
    };
    for assignment in assignments {
        append_type(assignment);
    }
    append_type(initial_type);
    if !informative {
        return String::new();
    }
    alternatives
        .into_iter()
        .filter(|alternative| {
            !matches!(
                alternative.as_str(),
                "any"
                    | "unknown"
                    | "Array"
                    | "Object"
                    | "Array<any>"
                    | "Array<unknown>"
                    | "ReadonlyArray<any>"
                    | "ReadonlyArray<unknown>"
            )
        })
        .collect::<Vec<_>>()
        .join(" | ")
}

pub(crate) fn component_member_needs_inferred_type(member: &VueComponentMember) -> bool {
    if member.kind != VueComponentMemberKind::Method {
        return member.type_.trim().is_empty()
            || !member.source_expression.is_empty() && is_weak_vue_flow_seed(&member.type_);
    }
    member.type_.trim().is_empty() || vue_callable_return_type(&member.type_).trim() == "unknown"
}

pub(crate) fn component_member_type_context(
    member: &VueComponentMember,
    component: &VueComponent,
) -> String {
    [
        &member.type_context_path,
        &member.file_path,
        &component.definition_path,
        &component.file_path,
    ]
    .into_iter()
    .find(|candidate| !candidate.is_empty())
    .cloned()
    .unwrap_or_default()
}

pub(crate) fn vue_callable_with_return_type(signature: &str, return_type: &str) -> String {
    let signature = signature.trim();
    if signature.is_empty() {
        return format!("() => {return_type}");
    }
    if !signature.starts_with('(') {
        return signature.to_owned();
    }
    match matching_slot_delimiter(signature, 0, b'(', b')') {
        Some(close) => format!("{} => {}", signature[..close + 1].trim(), return_type),
        None => signature.to_owned(),
    }
}

fn with_awaited(
    mut value: (ResolvedVueExpressionType, bool),
    awaited: bool,
) -> (ResolvedVueExpressionType, bool) {
    if value.1 && awaited {
        value.0.type_ = vue_awaited_type(&value.0.type_);
    }
    value
}

impl AdminComponentIndexer {
    pub(crate) fn resolve_component_expression_type(
        &self,
        component: &VueComponent,
        expression: &str,
        context_path: &str,
    ) -> ResolvedExpression {
        let mut expression = trim_vue_source_expression(expression);
        if expression.is_empty() {
            return not_found();
        }
        if let Some((left, right)) = split_vue_top_level_operator(&expression, "??") {
            let (left_resolved, left_found) =
                self.resolve_component_expression_type(component, &left, context_path)?;
            let (right_resolved, right_found) =
                self.resolve_component_expression_type(component, &right, context_path)?;
            let mut result = left_resolved.clone();
            result.type_ = merge_vue_nullish_types(&left_resolved.type_, &right_resolved.type_);
            if result.context_path.is_empty() {
                result.context_path = right_resolved.context_path;
            }
            let found = (left_found || right_found) && !result.type_.is_empty();
            return Ok((result, found));
        }
        let asserted = vue_type_assertion(&expression);
        if !asserted.is_empty() {
            return Ok((resolved(asserted, context_path), true));
        }
        let mut awaited = false;
        while let Some(rest) = expression.strip_prefix("await ") {
            awaited = true;
            expression = rest.trim().to_owned();
        }
        let transform = self.resolve_vue_object_transform_expression_type(
            &expression,
            context_path,
            Some(&|argument: &str| {
                self.resolve_component_expression_type(component, argument, context_path)
            }),
        )?;
        if transform.1 {
            return Ok(with_awaited(transform, awaited));
        }
        let repository_type = vue_repository_expression_type(&expression);
        if !repository_type.is_empty() {
            return Ok((resolved(repository_type, context_path), true));
        }
        if let Some(found) =
            self.resolve_shopware_context_expression_type(&expression, context_path)?
        {
            return Ok(with_awaited((found, true), awaited));
        }
        if let Some(found) =
            self.resolve_shopware_utils_expression_type(&expression, context_path)?
        {
            return Ok(with_awaited((found, true), awaited));
        }
        if let Some((store_name, segments)) = vue_static_store_expression(&expression) {
            let (mut result, found) = self.resolve_vue_static_type_chain(
                &format!("AdminStore<'{store_name}'>"),
                context_path,
                &segments,
                &[],
            )?;
            if !found {
                return Ok((result, found));
            }
            if awaited {
                result.type_ = vue_awaited_type(&result.type_);
            }
            result.open_runtime_shape =
                self.store_expression_has_open_runtime_shape(&store_name, &segments);
            let found = !result.type_.is_empty();
            return Ok((result, found));
        }
        if let Some(segments) = vue_static_this_expression(&expression)
            && !segments.is_empty()
        {
            let root = component
                .template_member(&segments[0].name)
                .or_else(|| vue_builtin_member(&segments[0].name));
            let Some(root) = root else {
                return not_found();
            };
            if root.type_.is_empty() {
                return not_found();
            }
            let root_context = component_member_type_context(&root, component);
            let Some(root_type) = twig_vue_receiver_type(&root.type_, segments[0].called) else {
                return not_found();
            };
            let (mut result, found) = self.resolve_vue_static_type_chain_with_optional(
                &root_type,
                &root_context,
                &segments[1..],
                segments[0].optional,
                &[],
            )?;
            if !found {
                return Ok((result, found));
            }
            if awaited {
                result.type_ = vue_awaited_type(&result.type_);
            }
            result.open_runtime_shape = root.open_runtime_shape;
            let found = !result.type_.is_empty();
            return Ok((result, found));
        }
        let inferred = vue_static_literal_type(&expression);
        if !inferred.is_empty() {
            return Ok((resolved(inferred, context_path), true));
        }
        let inferred = vue_expression_text_type(&expression, None);
        if !inferred.is_empty() {
            return Ok((resolved(inferred, context_path), true));
        }
        not_found()
    }

    /// Conservatively resolves a standalone Administration JavaScript or
    /// TypeScript expression without component-instance scope.
    pub fn resolve_javascript_expression_type(
        &self,
        expression: &str,
        context_path: &str,
    ) -> IndexResult<Option<String>> {
        let (resolved, found) = self.resolve_component_expression_type(
            &VueComponent::default(),
            expression,
            context_path,
        )?;
        Ok((found && !resolved.type_.is_empty()).then_some(resolved.type_))
    }

    pub(crate) fn store_expression_has_open_runtime_shape(
        &self,
        store_name: &str,
        segments: &[VueStaticExpressionSegment],
    ) -> bool {
        if store_name.is_empty() || segments.is_empty() || segments[0].called || segments[0].indexed
        {
            return false;
        }
        let Ok(stores) = self.get_store(store_name) else {
            return false;
        };
        stores.iter().any(|store| {
            store
                .member(&segments[0].name)
                .is_some_and(|member| member.open_runtime_shape)
        })
    }

    pub(crate) fn resolve_vue_static_type_chain(
        &self,
        root_type: &str,
        context_path: &str,
        segments: &[VueStaticExpressionSegment],
        live_files: &[AdminTypeFile],
    ) -> ResolvedExpression {
        self.resolve_vue_static_type_chain_with_optional(
            root_type,
            context_path,
            segments,
            false,
            live_files,
        )
    }

    pub(crate) fn resolve_vue_static_type_chain_with_optional(
        &self,
        root_type: &str,
        context_path: &str,
        segments: &[VueStaticExpressionSegment],
        mut optional: bool,
        live_files: &[AdminTypeFile],
    ) -> ResolvedExpression {
        let mut current = resolved(root_type.to_owned(), context_path);
        for segment in segments {
            let mut receiver_type = current.type_.clone();
            if optional || segment.optional {
                receiver_type = without_vue_nullish_type(&receiver_type);
            }
            if segment.indexed {
                let Some((indexed_type, indexed_context)) = self.resolve_vue_indexed_access_type(
                    &receiver_type,
                    &segment.index_expression,
                    &current.context_path,
                    live_files,
                )?
                else {
                    return Ok((current, false));
                };
                current.type_ = indexed_type;
                if !indexed_context.is_empty() {
                    current.context_path = indexed_context;
                }
                optional = optional || segment.optional;
                continue;
            }
            let shape = self.resolve_vue_type(&receiver_type, &current.context_path, live_files)?;
            let member = match twig_vue_member_named(&shape.members, &segment.name) {
                Some(member) if !member.type_.is_empty() => member.clone(),
                _ => return Ok((current, false)),
            };
            current.type_ = member.type_.clone();
            if !member.definition_path.is_empty() {
                current.context_path = member.definition_path.clone();
            }
            if segment.called {
                let Some(called_type) = self.resolve_vue_static_call_type(
                    &receiver_type,
                    &member,
                    segment,
                    &current.context_path,
                )?
                else {
                    return Ok((current, false));
                };
                current.type_ = called_type;
            }
            optional = optional || segment.optional;
        }
        if optional && !current.type_.is_empty() {
            current.type_ = merge_vue_types(&[&current.type_, "undefined"]);
        }
        let found = !current.type_.is_empty();
        Ok((current, found))
    }

    /// Go `(type, context, found, error)`; `None` when not found.
    pub(crate) fn resolve_vue_indexed_access_type(
        &self,
        receiver_type: &str,
        index_expression: &str,
        context_path: &str,
        live_files: &[AdminTypeFile],
    ) -> IndexResult<Option<(String, String)>> {
        self.resolve_vue_indexed_access_type_with_index_type(
            receiver_type,
            index_expression,
            &vue_static_index_expression_type(index_expression),
            context_path,
            live_files,
        )
    }

    pub(crate) fn resolve_vue_indexed_access_type_with_index_type(
        &self,
        receiver_type: &str,
        index_expression: &str,
        index_type: &str,
        context_path: &str,
        live_files: &[AdminTypeFile],
    ) -> IndexResult<Option<(String, String)>> {
        let receiver_type = receiver_type.trim();
        let index_expression = index_expression.trim();
        let index_type = index_type.trim();
        if receiver_type.is_empty() || index_expression.is_empty() {
            return Ok(None);
        }
        let union = split_admin_type_top_level(receiver_type, b'|');
        if union.len() > 1 {
            let mut result_type = String::new();
            let mut result_context = String::new();
            for branch in &union {
                let branch = branch.trim();
                if matches!(branch, "null" | "undefined" | "never") {
                    continue;
                }
                let Some((branch_type, branch_context)) = self
                    .resolve_vue_indexed_access_type_with_index_type(
                        branch,
                        index_expression,
                        index_type,
                        context_path,
                        live_files,
                    )?
                else {
                    return Ok(None);
                };
                result_type = merge_vue_types(&[&result_type, &branch_type]);
                if result_context.is_empty() {
                    result_context = branch_context;
                } else if branch_context != result_context {
                    result_context = context_path.to_owned();
                }
            }
            return Ok((!result_type.is_empty()).then_some((result_type, result_context)));
        }

        let value = normalize_vue_iterable_type(receiver_type);
        let (name, arguments) = parse_admin_named_type(&value);
        if arguments.len() == 2 {
            let short_name = name
                .rsplit_once('.')
                .map_or(name.as_str(), |(_, short)| short);
            if short_name == "Record" {
                return Ok(Some((
                    arguments[1].trim().to_owned(),
                    context_path.to_owned(),
                )));
            }
        }
        if value.len() >= 2
            && value.as_bytes()[0] == b'['
            && matching_slot_delimiter(&value, 0, b'[', b']') == Some(value.len() - 1)
        {
            let Ok(position) = index_expression.parse::<i64>() else {
                return Ok(None);
            };
            if position < 0 {
                return Ok(None);
            }
            let items = split_slot_top_level(&value[1..value.len() - 1], b',');
            let Some(item) = items.get(position as usize) else {
                return Ok(None);
            };
            return Ok(Some((item.trim().to_owned(), context_path.to_owned())));
        }
        if index_expression.parse::<i64>().is_ok()
            || without_vue_nullish_type(index_type) == "number"
        {
            if value == "string" {
                return Ok(Some(("string".to_owned(), context_path.to_owned())));
            }
            let element_type = vue_iterable_element_type(&value);
            if !element_type.is_empty() {
                return Ok(Some((element_type, context_path.to_owned())));
            }
        }
        let key = admin_type_string_literal(index_expression);
        if key.is_empty() {
            return Ok(None);
        }
        let shape = self.resolve_vue_type(&value, context_path, live_files)?;
        let Some(member) = twig_vue_member_named(&shape.members, &key) else {
            return Ok(None);
        };
        if member.type_.is_empty() {
            return Ok(None);
        }
        let context = if member.definition_path.is_empty() {
            context_path.to_owned()
        } else {
            member.definition_path.clone()
        };
        Ok(Some((member.type_.clone(), context)))
    }
}

pub(crate) fn vue_static_index_expression_type(expression: &str) -> String {
    let expression = unwrap_vue_expression_parentheses(expression.trim());
    if expression.is_empty() {
        return String::new();
    }
    if expression.parse::<i64>().is_ok() {
        return "number".to_owned();
    }
    if !admin_type_string_literal(&expression).is_empty() {
        return "string".to_owned();
    }
    if expression.starts_with('+') || expression.starts_with('-') {
        return "number".to_owned();
    }
    for operator in ["-", "*", "/", "%", "<<", ">>", ">>>"] {
        if split_vue_top_level_operator(&expression, operator).is_some() {
            return "number".to_owned();
        }
    }
    String::new()
}

/// Callback resolving the argument of an object transform.
pub(crate) type ResolveArgument<'a> = &'a dyn Fn(&str) -> ResolvedExpression;

impl AdminComponentIndexer {
    pub(crate) fn resolve_vue_object_transform_expression_type(
        &self,
        expression: &str,
        context_path: &str,
        resolve_argument: Option<ResolveArgument<'_>>,
    ) -> ResolvedExpression {
        let Some((method, argument, segments)) = vue_static_object_transform_expression(expression)
        else {
            return not_found();
        };
        let mut context_path = context_path.to_owned();
        let mut argument_type = String::new();
        if let Some(resolve_argument) = resolve_argument {
            let (resolved, found) = resolve_argument(&argument)?;
            if found {
                argument_type = resolved.type_;
                if !resolved.context_path.is_empty() {
                    context_path = resolved.context_path;
                }
            }
        }
        let result_type =
            self.vue_object_transform_result_type(&method, &argument_type, &context_path)?;
        if result_type.is_empty() {
            return not_found();
        }
        if segments.is_empty() {
            return Ok((resolved(result_type, &context_path), true));
        }
        self.resolve_vue_static_type_chain(&result_type, &context_path, &segments, &[])
    }

    pub(crate) fn vue_object_transform_result_type(
        &self,
        method: &str,
        argument_type: &str,
        context_path: &str,
    ) -> IndexResult<String> {
        match method {
            "keys" => Ok("Array<string>".to_owned()),
            "values" | "entries" => {
                let mut value_type = vue_iterable_element_type(argument_type);
                if value_type.is_empty() && !argument_type.is_empty() {
                    let shape = self.resolve_vue_type(argument_type, context_path, &[])?;
                    for member in &shape.members {
                        value_type = merge_vue_types(&[&value_type, &member.type_]);
                    }
                }
                if value_type.is_empty() {
                    value_type = "unknown".to_owned();
                }
                if method == "values" {
                    return Ok(format!("Array<{value_type}>"));
                }
                Ok(format!("Array<[string, {value_type}]>"))
            }
            "fromEntries" => {
                let mut key_type = "string | number | symbol".to_owned();
                let mut value_type = "unknown".to_owned();
                let entry_type =
                    normalize_vue_iterable_type(&vue_iterable_element_type(argument_type));
                if entry_type.len() >= 2
                    && entry_type.as_bytes()[0] == b'['
                    && matching_slot_delimiter(&entry_type, 0, b'[', b']')
                        == Some(entry_type.len() - 1)
                {
                    let items = split_slot_top_level(&entry_type[1..entry_type.len() - 1], b',');
                    if items.len() == 2 {
                        key_type = items[0].trim().to_owned();
                        value_type = items[1].trim().to_owned();
                    }
                }
                Ok(format!("Record<{key_type}, {value_type}>"))
            }
            _ => Ok(String::new()),
        }
    }
}

/// Go `vueStaticObjectTransformExpression`: `(method, argument, segments)`.
pub(crate) fn vue_static_object_transform_expression(
    expression: &str,
) -> Option<(String, String, Vec<VueStaticExpressionSegment>)> {
    let expression = unwrap_vue_expression_parentheses(expression);
    let bytes = expression.as_bytes();
    for method in ["values", "keys", "entries", "fromEntries"] {
        let prefix = format!("Object.{method}");
        if !expression.starts_with(&prefix) {
            continue;
        }
        let mut cursor = skip_javascript_spaces_from(bytes, prefix.len());
        if cursor < bytes.len() && bytes[cursor] == b'<' {
            let close = matching_slot_delimiter(&expression, cursor, b'<', b'>')?;
            cursor = skip_javascript_spaces_from(bytes, close + 1);
        }
        if cursor >= bytes.len() || bytes[cursor] != b'(' {
            return None;
        }
        let close = matching_slot_delimiter(&expression, cursor, b'(', b')')?;
        let arguments = split_slot_top_level(&expression[cursor + 1..close], b',');
        if arguments.len() != 1 || arguments[0].trim().is_empty() {
            return None;
        }
        let (segments, end) = vue_static_expression_segments(&expression, close + 1)?;
        if !expression[end..].trim().is_empty() {
            return None;
        }
        return Some((method.to_owned(), arguments[0].trim().to_owned(), segments));
    }
    None
}

impl AdminComponentIndexer {
    /// Specializes statically known collection callbacks before falling back
    /// to the indexed method signature.
    pub(crate) fn resolve_vue_static_call_type(
        &self,
        receiver_type: &str,
        member: &TwigVueMember,
        segment: &VueStaticExpressionSegment,
        context_path: &str,
    ) -> IndexResult<Option<String>> {
        if segment.name == "map" {
            let element_type = vue_iterable_element_type(receiver_type);
            if !element_type.is_empty()
                && let Some(result_type) = self.resolve_vue_collection_callback_type(
                    &segment.arguments,
                    &element_type,
                    context_path,
                )?
                && !result_type.is_empty()
            {
                return Ok(Some(format!("Array<{result_type}>")));
            }
        }
        let result_type = vue_callable_return_type(&member.type_);
        Ok((!result_type.is_empty()).then_some(result_type))
    }

    pub(crate) fn resolve_vue_collection_callback_type(
        &self,
        arguments: &str,
        element_type: &str,
        context_path: &str,
    ) -> IndexResult<Option<String>> {
        let parts = split_slot_top_level(arguments, b',');
        let Some(first) = parts.first() else {
            return Ok(None);
        };
        let Some((parameter, body, declared_return)) = vue_static_arrow_callback(first) else {
            return Ok(None);
        };
        if parameter.is_empty() || body.is_empty() {
            return Ok(None);
        }
        let (result_type, resolved) =
            self.resolve_vue_bound_expression_type(&body, &parameter, element_type, context_path)?;
        if resolved && !result_type.is_empty() {
            return Ok(Some(result_type));
        }
        if !declared_return.is_empty() {
            return Ok(Some(declared_return));
        }
        Ok(None)
    }
}

/// Go `vueStaticArrowCallback`: `(parameter, body, declaredReturn)`.
pub(crate) fn vue_static_arrow_callback(value: &str) -> Option<(String, String, String)> {
    let value = value.trim();
    let arrow = index_vue_top_level_operator(value, "=>")?;
    let mut header = value[..arrow].trim();
    if let Some(rest) = header.strip_prefix("async ") {
        header = rest.trim();
    }
    if header.is_empty() {
        return None;
    }
    let mut declared_return = String::new();
    let mut parameters = header;
    if header.starts_with('(') {
        let close = matching_slot_delimiter(header, 0, b'(', b')')?;
        parameters = &header[1..close];
        let suffix = header[close + 1..].trim();
        if let Some(rest) = suffix.strip_prefix(':') {
            declared_return = rest.trim().to_owned();
        } else if !suffix.is_empty() {
            return None;
        }
    }
    let parameter_parts = split_slot_top_level(parameters, b',');
    let first = parameter_parts.first()?;
    let mut parameter = first.trim();
    if let Some(equals) = index_slot_top_level(parameter, b'=') {
        parameter = parameter[..equals].trim();
    }
    if let Some(colon) = index_slot_top_level(parameter, b':') {
        parameter = parameter[..colon].trim();
    }
    let parameter = parameter.strip_suffix('?').unwrap_or(parameter);
    if !is_slot_identifier(parameter) {
        return None;
    }
    let mut body = trim_vue_source_expression(&value[arrow + 2..]);
    if body.starts_with('{') {
        let parsed = swlsp_parser_js::parse(&format!("const __callback = {value};"));
        let arrows = jsquery::nodes(&parsed.root(), &[jssyntax::JS_ARROW_FUNCTION]);
        if arrows.len() != 1 {
            return None;
        }
        body = vue_method_return_expression(&arrows[0]);
    }
    if body.is_empty() {
        return None;
    }
    Some((parameter.to_owned(), body, declared_return))
}

impl AdminComponentIndexer {
    /// Go `(type, found, error)`.
    pub(crate) fn resolve_vue_bound_expression_type(
        &self,
        expression: &str,
        parameter: &str,
        parameter_type: &str,
        context_path: &str,
    ) -> IndexResult<(String, bool)> {
        let expression = unwrap_vue_expression_parentheses(expression);
        if expression.is_empty() {
            return Ok((String::new(), false));
        }
        let asserted = vue_type_assertion(&expression);
        if !asserted.is_empty() {
            return Ok((asserted, true));
        }
        if let Some((left, right)) = split_vue_top_level_operator(&expression, "??") {
            let (left_type, left_found) = self.resolve_vue_bound_expression_type(
                &left,
                parameter,
                parameter_type,
                context_path,
            )?;
            let (right_type, right_found) = self.resolve_vue_bound_expression_type(
                &right,
                parameter,
                parameter_type,
                context_path,
            )?;
            return Ok((
                merge_vue_nullish_types(&left_type, &right_type),
                left_found || right_found,
            ));
        }
        if let Some((left, right)) = split_vue_top_level_operator(&expression, "||") {
            let (left_type, left_found) = self.resolve_vue_bound_expression_type(
                &left,
                parameter,
                parameter_type,
                context_path,
            )?;
            let (right_type, right_found) = self.resolve_vue_bound_expression_type(
                &right,
                parameter,
                parameter_type,
                context_path,
            )?;
            return Ok((
                merge_vue_types(&[&left_type, &right_type]),
                left_found || right_found,
            ));
        }
        if expression.starts_with('{') {
            return self.resolve_vue_projection_object_type(
                &expression,
                parameter,
                parameter_type,
                context_path,
            );
        }
        if expression.starts_with('[') {
            return self.resolve_vue_projection_array_type(
                &expression,
                parameter,
                parameter_type,
                context_path,
            );
        }
        if expression.starts_with(parameter)
            && (expression.len() == parameter.len()
                || !is_vue_identifier_part(expression.as_bytes()[parameter.len()]))
        {
            if expression.len() == parameter.len() {
                return Ok((parameter_type.to_owned(), true));
            }
            if let Some((segments, end)) =
                vue_static_expression_segments(&expression, parameter.len())
                && expression[end..].trim().is_empty()
            {
                let (resolved, found) = self.resolve_vue_static_type_chain(
                    parameter_type,
                    context_path,
                    &segments,
                    &[],
                )?;
                return Ok((resolved.type_, found));
            }
        }
        let inferred = vue_expression_text_type(&expression, None);
        if !inferred.is_empty() {
            return Ok((inferred, true));
        }
        Ok((String::new(), false))
    }

    pub(crate) fn resolve_vue_projection_object_type(
        &self,
        expression: &str,
        parameter: &str,
        parameter_type: &str,
        context_path: &str,
    ) -> IndexResult<(String, bool)> {
        let Some(close) = matching_slot_delimiter(expression, 0, b'{', b'}') else {
            return Ok((String::new(), false));
        };
        if close != expression.len() - 1 {
            return Ok((String::new(), false));
        }
        let mut fields = Vec::new();
        let mut spreads = Vec::new();
        for raw in split_slot_top_level(&expression[1..close], b',') {
            let raw = raw.trim();
            if raw.is_empty() {
                continue;
            }
            if let Some(spread) = raw.strip_prefix("...") {
                let (spread_type, found) = self.resolve_vue_bound_expression_type(
                    spread.trim(),
                    parameter,
                    parameter_type,
                    context_path,
                )?;
                if found && !spread_type.is_empty() {
                    spreads.push(spread_type);
                }
                continue;
            }
            let Some(colon) = index_slot_top_level(raw, b':') else {
                continue;
            };
            let name = raw[..colon].trim().trim_matches(['\'', '"']);
            if name.is_empty() || name.starts_with('[') {
                continue;
            }
            let (mut field_type, found) = self.resolve_vue_bound_expression_type(
                raw[colon + 1..].trim(),
                parameter,
                parameter_type,
                context_path,
            )?;
            if !found || field_type.is_empty() {
                field_type = "unknown".to_owned();
            }
            fields.push(format!("{name}: {field_type}"));
        }
        if fields.is_empty() && spreads.is_empty() {
            return Ok(("Object".to_owned(), true));
        }
        if !fields.is_empty() {
            spreads.push(format!("{{ {} }}", fields.join("; ")));
        }
        Ok((spreads.join(" & "), true))
    }

    pub(crate) fn resolve_vue_projection_array_type(
        &self,
        expression: &str,
        parameter: &str,
        parameter_type: &str,
        context_path: &str,
    ) -> IndexResult<(String, bool)> {
        let Some(close) = matching_slot_delimiter(expression, 0, b'[', b']') else {
            return Ok((String::new(), false));
        };
        if close != expression.len() - 1 {
            return Ok((String::new(), false));
        }
        let mut element_type = String::new();
        for raw in split_slot_top_level(&expression[1..close], b',') {
            let (item_type, found) = self.resolve_vue_bound_expression_type(
                raw.trim(),
                parameter,
                parameter_type,
                context_path,
            )?;
            if found {
                element_type = merge_vue_types(&[&element_type, &item_type]);
            }
        }
        if element_type.is_empty() {
            return Ok(("Array".to_owned(), true));
        }
        Ok((format!("Array<{element_type}>"), true))
    }
}

pub(crate) fn unwrap_vue_expression_parentheses(value: &str) -> String {
    let mut value = value.trim();
    while value.len() >= 2
        && value.as_bytes()[0] == b'('
        && matching_slot_delimiter(value, 0, b'(', b')') == Some(value.len() - 1)
    {
        value = value[1..value.len() - 1].trim();
    }
    value.to_owned()
}

pub(crate) fn vue_static_literal_type(expression: &str) -> String {
    let expression = unwrap_vue_expression_parentheses(expression);
    let Some(&first) = expression.as_bytes().first() else {
        return String::new();
    };
    if !b"[{\"'`-0123456789".contains(&first) {
        return String::new();
    }
    let parsed = swlsp_parser_js::parse(&format!("__shopwareLSPType({expression});"));
    if !parsed.errors.is_empty() {
        return String::new();
    }
    let calls = jsquery::calls(&parsed.root(), &["__shopwareLSPType"]);
    if calls.len() != 1 {
        return String::new();
    }
    jsquery::argument_expression(&calls[0], 0)
        .map(|argument| vue_expression_type(&argument, None))
        .unwrap_or_default()
}

pub(crate) fn index_vue_top_level_operator(value: &str, operator: &str) -> Option<usize> {
    let bytes = value.as_bytes();
    let operator = operator.as_bytes();
    let mut state = SlotScanState::default();
    let mut index = 0;
    while index + operator.len() <= bytes.len() {
        if state.top_level() && bytes[index..].starts_with(operator) {
            return Some(index);
        }
        state.consume(bytes[index]);
        index += 1;
    }
    None
}

/// Go `splitVueTopLevelOperator`; `None` unless both sides are non-empty.
pub(crate) fn split_vue_top_level_operator(
    value: &str,
    operator: &str,
) -> Option<(String, String)> {
    let index = index_vue_top_level_operator(value, operator)?;
    let left = value[..index].trim();
    let right = value[index + operator.len()..].trim();
    (!left.is_empty() && !right.is_empty()).then(|| (left.to_owned(), right.to_owned()))
}

pub(crate) fn without_vue_nullish_type(value: &str) -> String {
    let mut result = String::new();
    for branch in split_admin_type_top_level(value, b'|') {
        let branch = branch.trim();
        if branch.is_empty() || branch == "null" || branch == "undefined" {
            continue;
        }
        result = merge_vue_types(&[&result, branch]);
    }
    result
}

pub(crate) fn merge_vue_nullish_types(left: &str, right: &str) -> String {
    let left = without_vue_nullish_type(left);
    if left.is_empty() {
        return right.to_owned();
    }
    if right.is_empty() || is_weak_vue_flow_seed(right) && !is_weak_vue_flow_seed(&left) {
        return left;
    }
    merge_vue_types(&[&left, right])
}

/// Joins the distinct top-level union branches of all values.
pub(crate) fn merge_vue_types(values: &[&str]) -> String {
    let mut result: Vec<String> = Vec::new();
    let mut seen = HashSet::new();
    for value in values {
        for branch in split_admin_type_top_level(value, b'|') {
            let branch = branch.trim();
            if branch.is_empty() || !seen.insert(branch.to_owned()) {
                continue;
            }
            result.push(branch.to_owned());
        }
    }
    result.join(" | ")
}

pub(crate) fn vue_repository_expression_type(expression: &str) -> String {
    let compact = compact_vue_expression(expression);
    let Some(captures) = VUE_REPOSITORY_CREATE_EXPRESSION_PATTERN.captures(&compact) else {
        return String::new();
    };
    let Some(open) = compact.find('(') else {
        return String::new();
    };
    if matching_slot_delimiter(&compact, open, b'(', b')') != Some(compact.len() - 1) {
        return String::new();
    }
    format!("Repository<'{}'>", &captures[1])
}

pub(crate) fn vue_static_this_expression(
    expression: &str,
) -> Option<Vec<VueStaticExpressionSegment>> {
    let expression = expression.trim();
    if !expression.starts_with("this")
        || expression.len() > "this".len()
            && is_vue_identifier_part(expression.as_bytes()["this".len()])
    {
        return None;
    }
    let (segments, end) = vue_static_expression_segments(expression, "this".len())?;
    expression[end..].trim().is_empty().then_some(segments)
}

/// Go `vueStaticStoreExpression`: `(storeName, segments)`.
pub(crate) fn vue_static_store_expression(
    expression: &str,
) -> Option<(String, Vec<VueStaticExpressionSegment>)> {
    let expression = expression.trim();
    let bytes = expression.as_bytes();
    let prefix = ["Shopware.Store.get", "Store.get"]
        .into_iter()
        .find(|prefix| expression.starts_with(prefix))?;
    let start = skip_javascript_spaces_from(bytes, prefix.len());
    if start >= bytes.len() || bytes[start] != b'(' {
        return None;
    }
    let close = matching_slot_delimiter(expression, start, b'(', b')')?;
    let argument = expression[start + 1..close].trim();
    let store_name = admin_type_string_literal(argument);
    if store_name.is_empty() {
        return None;
    }
    let (segments, end) = vue_static_expression_segments(expression, close + 1)?;
    if !expression[end..].trim().is_empty() {
        return None;
    }
    Some((store_name, segments))
}

/// Go `vueStaticExpressionSegments`: `(segments, end)` when `ok`.
pub(crate) fn vue_static_expression_segments(
    value: &str,
    cursor: usize,
) -> Option<(Vec<VueStaticExpressionSegment>, usize)> {
    let bytes = value.as_bytes();
    let mut cursor = cursor;
    let mut result = Vec::new();
    loop {
        cursor = skip_javascript_spaces_from(bytes, cursor);
        if cursor >= bytes.len() {
            return Some((result, cursor));
        }
        let mut optional = false;
        if bytes[cursor..].starts_with(b"?.[") {
            optional = true;
            cursor += 2;
        }
        if cursor < bytes.len() && bytes[cursor] == b'[' {
            let close = matching_slot_delimiter(value, cursor, b'[', b']')?;
            result.push(VueStaticExpressionSegment {
                optional,
                indexed: true,
                index_expression: value[cursor + 1..close].trim().to_owned(),
                ..Default::default()
            });
            cursor = close + 1;
            continue;
        }
        if bytes[cursor..].starts_with(b"?.") {
            optional = true;
            cursor += 2;
        } else if bytes[cursor] == b'.' {
            cursor += 1;
        } else {
            let ok = !result.is_empty() || cursor == bytes.len();
            return ok.then_some((result, cursor));
        }
        cursor = skip_javascript_spaces_from(bytes, cursor);
        let start = cursor;
        if start >= bytes.len() || !is_vue_identifier_start(bytes[start]) {
            return None;
        }
        cursor += 1;
        while cursor < bytes.len() && is_vue_identifier_part(bytes[cursor]) {
            cursor += 1;
        }
        let mut segment = VueStaticExpressionSegment {
            name: value[start..cursor].to_owned(),
            optional,
            ..Default::default()
        };
        cursor = skip_javascript_spaces_from(bytes, cursor);
        if cursor < bytes.len() && bytes[cursor] == b'!' {
            cursor += 1;
        }
        cursor = skip_javascript_spaces_from(bytes, cursor);
        let mut call_open = None;
        if bytes[cursor..].starts_with(b"?.(") {
            segment.optional = true;
            call_open = Some(cursor + 2);
        } else if cursor < bytes.len() && bytes[cursor] == b'(' {
            call_open = Some(cursor);
        }
        if let Some(call_open) = call_open {
            let close = matching_slot_delimiter(value, call_open, b'(', b')')?;
            segment.called = true;
            segment.arguments = value[call_open + 1..close].to_owned();
            cursor = close + 1;
        }
        result.push(segment);
    }
}

pub(crate) fn is_vue_identifier_start(value: u8) -> bool {
    value == b'_' || value == b'$' || value.is_ascii_alphabetic()
}

pub(crate) fn is_vue_identifier_part(value: u8) -> bool {
    is_vue_identifier_start(value) || value.is_ascii_digit()
}

pub(crate) fn vue_awaited_type(value: &str) -> String {
    let value = value.trim();
    let (name, arguments) = parse_admin_named_type(value);
    if (name == "Promise" || name == "PromiseLike") && arguments.len() == 1 {
        return arguments[0].trim().to_owned();
    }
    value.to_owned()
}
