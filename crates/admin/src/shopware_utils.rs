//! `Shopware.Utils` member intelligence.

use crate::*;

/// A statically named member rooted at `Shopware.Utils`: the receiver path
/// between `Utils` and the member, and the member name (empty after a
/// trailing dot).
pub fn javascript_shopware_utils_member(node: &SyntaxNode) -> Option<(Vec<String>, String)> {
    javascript_shopware_utils_member_impl(node, None)
}

pub(crate) fn javascript_shopware_utils_member_impl(
    node: &SyntaxNode,
    analysis: Option<&JavaScriptDocumentAnalysis>,
) -> Option<(Vec<String>, String)> {
    if let Some(result) = javascript_static_member(node, "Shopware.Utils") {
        return Some(result);
    }
    if let Some(member) = jsquery::member_expression_at(node) {
        return javascript_shopware_utils_alias_member(node, &member, analysis);
    }
    if node.kind() != jssyntax::JS_IDENTIFIER {
        return None;
    }
    let identifier = jsquery::identifier_text(node).unwrap_or_default();
    let mut path = visible_javascript_shopware_utils_binding(
        node,
        &identifier,
        &javascript_root(node),
        analysis,
    )?;
    let name = path.pop()?;
    Some((path, name))
}

pub fn javascript_shopware_utils_member_name_node(node: &SyntaxNode) -> Option<SyntaxNode> {
    javascript_shopware_utils_member_name_node_impl(node, None)
}

pub(crate) fn javascript_shopware_utils_member_name_node_impl(
    node: &SyntaxNode,
    analysis: Option<&JavaScriptDocumentAnalysis>,
) -> Option<SyntaxNode> {
    let (_, name) = javascript_shopware_utils_member_impl(node, analysis)?;
    if name.is_empty() {
        return None;
    }
    let Some(member) = jsquery::member_expression_at(node) else {
        return (node.kind() == jssyntax::JS_IDENTIFIER).then(|| node.clone());
    };
    let mut children = member.children();
    children.next()?;
    children
        .filter(|child| {
            child.kind() == jssyntax::JS_IDENTIFIER
                && javascript_trimmed_node_text(child).trim() == name
        })
        .last()
}

/// The `(operation, event name)` of the event-name argument of a public
/// EventBus `on`/`off`/`emit` call.
pub fn javascript_shopware_event_bus_event_at(node: &SyntaxNode) -> Option<(String, String)> {
    javascript_shopware_event_bus_event_at_impl(node, None)
}

pub(crate) fn javascript_shopware_event_bus_event_at_impl(
    node: &SyntaxNode,
    analysis: Option<&JavaScriptDocumentAnalysis>,
) -> Option<(String, String)> {
    let literal = jsquery::string_at(node)?;
    if jsquery::string_argument_index(&literal) != Some(0) {
        return None;
    }
    let argument = jsquery::argument(&literal, 0)?;
    let mut expression = single_javascript_expression(&argument)?;
    while expression.kind() == jssyntax::JS_PARENTHESIZED {
        expression = single_javascript_expression(&expression)?;
    }
    if expression != literal {
        return None;
    }
    let callee = jsquery::call_callee(&literal)?;
    let (receiver, member_name) = javascript_shopware_utils_member_impl(&callee, analysis)?;
    if receiver.len() != 1 || receiver[0] != "EventBus" {
        return None;
    }
    match member_name.as_str() {
        "on" | "off" | "emit" => {
            let event_name = jsquery::string_value(&literal);
            if event_name.contains("${") {
                return None;
            }
            Some((member_name, event_name))
        }
        _ => None,
    }
}

/// The only child node of `node`, if it has exactly one.
pub(crate) fn single_javascript_expression(node: &SyntaxNode) -> Option<SyntaxNode> {
    let mut children = node.children();
    let expression = children.next()?;
    children.next().is_none().then_some(expression)
}

pub(crate) fn javascript_shopware_utils_alias_member(
    node: &SyntaxNode,
    member: &SyntaxNode,
    analysis: Option<&JavaScriptDocumentAnalysis>,
) -> Option<(Vec<String>, String)> {
    let direct_receiver = member.children().next()?;
    if node != member && javascript_node_within(node, &direct_receiver) {
        return None;
    }
    let member_text = javascript_trimmed_node_text(member);
    let alias = leading_javascript_identifier(&member_text);
    if alias.is_empty() {
        return None;
    }
    let base_path = visible_javascript_shopware_utils_binding(
        member,
        &alias,
        &javascript_root(member),
        analysis,
    )?;
    let (segments, incomplete) = javascript_static_expression(&member_text, &alias)?;
    if segments.is_empty() && !incomplete {
        return None;
    }
    let names = static_expression_segment_names(&segments);
    if names.len() != segments.len() {
        return None;
    }
    let mut path = base_path;
    path.extend(names);
    if incomplete {
        return Some((path, String::new()));
    }
    let name = path.pop()?;
    Some((path, name))
}

/// One name bound by a `const`/`let`/`var` declaration.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub(crate) struct JavaScriptVariableBinding {
    pub local_name: String,
    pub source_name: String,
    pub initializer: String,
    pub constant: bool,
}

pub(crate) fn visible_javascript_shopware_utils_binding(
    use_node: &SyntaxNode,
    identifier: &str,
    root: &SyntaxNode,
    analysis: Option<&JavaScriptDocumentAnalysis>,
) -> Option<Vec<String>> {
    if !is_static_vue_identifier(identifier) {
        return None;
    }
    let use_start = use_node.range_trimmed_trivia().start();
    let mut best: Option<(JavaScriptVariableBinding, usize, TextSize)> = None;
    let declarations = match analysis {
        Some(analysis) => analysis.nodes(&[jssyntax::JS_VARIABLE_DECLARATION]),
        None => jsquery::nodes(root, &[jssyntax::JS_VARIABLE_DECLARATION]),
    };
    for declaration in declarations {
        let start = declaration.range_trimmed_trivia().start();
        if javascript_node_within(use_node, &declaration) || start >= use_start {
            continue;
        }
        let binding = match analysis {
            Some(analysis) => analysis.variable_binding(&declaration, identifier).cloned(),
            None => javascript_variable_binding_named(&declaration.text_string(), identifier),
        };
        let Some(binding) = binding else {
            continue;
        };
        let Some(distance) = javascript_binding_distance(use_node, &declaration) else {
            continue;
        };
        let better = match &best {
            None => true,
            Some((_, best_distance, best_start)) => {
                distance < *best_distance || (distance == *best_distance && start > *best_start)
            }
        };
        if better {
            best = Some((binding, distance, start));
        }
    }
    let (best, best_distance, _) = best?;
    if !best.constant || javascript_parameter_shadows_binding(use_node, identifier, best_distance) {
        return None;
    }
    let (segments, incomplete) = javascript_static_expression(&best.initializer, "Shopware.Utils")?;
    if incomplete {
        return None;
    }
    let mut path = static_expression_segment_names(&segments);
    if path.len() != segments.len() {
        return None;
    }
    if !best.source_name.is_empty() {
        path.push(best.source_name);
    }
    Some(path)
}

pub(crate) fn javascript_variable_binding_named(
    value: &str,
    identifier: &str,
) -> Option<JavaScriptVariableBinding> {
    javascript_variable_bindings(value)
        .into_iter()
        .find(|binding| binding.local_name == identifier)
}

pub(crate) fn javascript_variable_bindings(value: &str) -> Vec<JavaScriptVariableBinding> {
    let mut value = value.trim();
    let mut keyword = "";
    for candidate in ["const", "let", "var"] {
        if value.starts_with(candidate)
            && value.len() > candidate.len()
            && is_javascript_space(value.as_bytes()[candidate.len()])
        {
            keyword = candidate;
            value = value[candidate.len()..].trim();
            break;
        }
    }
    if keyword.is_empty() {
        return Vec::new();
    }
    let Some(equals) = index_slot_top_level(value, b'=') else {
        return Vec::new();
    };
    let left = value[..equals].trim();
    let right = trim_vue_source_expression(&value[equals + 1..]);
    if right.is_empty() || index_slot_top_level(&right, b',').is_some() {
        return Vec::new();
    }
    let constant = keyword == "const";
    if is_static_vue_identifier(left) {
        return vec![JavaScriptVariableBinding {
            local_name: left.to_owned(),
            initializer: right,
            constant,
            ..Default::default()
        }];
    }
    if left.len() < 2
        || !left.starts_with('{')
        || matching_slot_delimiter(left, 0, b'{', b'}') != Some(left.len() - 1)
    {
        return Vec::new();
    }
    let mut bindings = Vec::new();
    for raw in split_slot_top_level(&left[1..left.len() - 1], b',') {
        let entry = raw.trim();
        if entry.is_empty() || entry.starts_with("...") {
            continue;
        }
        let mut source_name = entry;
        let mut local_name = entry;
        if let Some(colon) = index_slot_top_level(entry, b':') {
            source_name = entry[..colon].trim();
            local_name = entry[colon + 1..].trim();
        }
        if let Some(default_at) = index_slot_top_level(local_name, b'=') {
            local_name = local_name[..default_at].trim();
        }
        if !is_static_vue_identifier(source_name) || !is_static_vue_identifier(local_name) {
            continue;
        }
        bindings.push(JavaScriptVariableBinding {
            local_name: local_name.to_owned(),
            source_name: source_name.to_owned(),
            initializer: right.clone(),
            constant,
        });
    }
    bindings
}

/// The number of parent steps from `use_node` to the scope owning
/// `declaration`; `None` when the declaration is not visible.
pub(crate) fn javascript_binding_distance(
    use_node: &SyntaxNode,
    declaration: &SyntaxNode,
) -> Option<usize> {
    let declaration_function = closest_javascript_function_scope(declaration);
    if let Some(function) = &declaration_function
        && !javascript_node_within(use_node, function)
    {
        return None;
    }
    let declaration_block =
        closest_javascript_block_scope(declaration, declaration_function.as_ref());
    if let Some(block) = &declaration_block
        && !javascript_node_within(use_node, block)
    {
        return None;
    }
    let scope = declaration_block
        .or(declaration_function)
        .unwrap_or_else(|| javascript_root(declaration));
    use_node.ancestors().position(|current| current == scope)
}

pub(crate) fn javascript_parameter_shadows_binding(
    use_node: &SyntaxNode,
    identifier: &str,
    binding_distance: usize,
) -> bool {
    use_node.ancestors().take(binding_distance).any(|current| {
        matches!(
            current.kind(),
            jssyntax::JS_METHOD | jssyntax::JS_FUNCTION | jssyntax::JS_ARROW_FUNCTION
        ) && javascript_function_binds_parameter(&current, identifier)
    })
}

pub(crate) fn javascript_function_binds_parameter(function: &SyntaxNode, identifier: &str) -> bool {
    if !is_static_vue_identifier(identifier) {
        return false;
    }
    let full = function.text_string();
    let text = full.trim();
    let parameters: String;
    if function.kind() == jssyntax::JS_ARROW_FUNCTION {
        let Some(arrow) = text.find("=>") else {
            return false;
        };
        let mut value = text[..arrow].trim();
        value = value.strip_prefix("async ").unwrap_or(value).trim();
        if value.starts_with('(')
            && matching_slot_delimiter(value, 0, b'(', b')') == Some(value.len() - 1)
        {
            value = &value[1..value.len() - 1];
        }
        parameters = value.to_owned();
    } else if let Some((parsed, _)) = vue_method_header(text) {
        parameters = parsed;
    } else {
        return false;
    }
    for parameter in split_slot_top_level(&parameters, b',') {
        let parameter = parameter.trim();
        let parameter = parameter.strip_prefix("...").unwrap_or(parameter).trim();
        if is_static_vue_identifier(parameter) && parameter == identifier {
            return true;
        }
        if parameter.len() >= 2
            && parameter.starts_with('{')
            && let Some(close) =
                matching_slot_delimiter(parameter, 0, b'{', b'}').filter(|close| *close > 0)
        {
            for entry in split_slot_top_level(&parameter[1..close], b',') {
                let mut entry = entry.trim();
                if let Some(colon) = index_slot_top_level(entry, b':') {
                    entry = entry[colon + 1..].trim();
                }
                if let Some(default_at) = index_slot_top_level(entry, b'=') {
                    entry = entry[..default_at].trim();
                }
                if entry == identifier {
                    return true;
                }
            }
        }
    }
    false
}

pub(crate) fn leading_javascript_identifier(value: &str) -> String {
    let bytes = value.trim().as_bytes();
    if bytes.is_empty() || !is_vue_identifier_start(bytes[0]) {
        return String::new();
    }
    let mut cursor = 1;
    while cursor < bytes.len() && is_vue_identifier_part(bytes[cursor]) {
        cursor += 1;
    }
    String::from_utf8_lossy(&bytes[..cursor]).into_owned()
}

/// The root of the tree containing `node`.
pub(crate) fn javascript_root(node: &SyntaxNode) -> SyntaxNode {
    node.ancestors().last().unwrap_or_else(|| node.clone())
}

fn named_member(members: &[TwigVueMember], name: &str) -> Option<TwigVueMember> {
    twig_vue_member_named(members, name)
}

impl AdminComponentIndexer {
    /// Resolves one receiver against the indexed default export of
    /// `core/service/util.service`.
    pub fn resolve_shopware_utils(
        &self,
        receiver: &str,
        context_path: &str,
    ) -> IndexResult<VueTypeShape> {
        let Some(segments) = static_expression_path(receiver) else {
            return Ok(VueTypeShape::default());
        };
        let names = static_expression_segment_names(&segments);
        if names.len() != segments.len() {
            return Ok(VueTypeShape::default());
        }
        let live_files = self.live_type_file_overlays(&[]);
        let Some((declaration, declaration_context)) =
            self.resolve_admin_type_declaration(context_path, SHOPWARE_UTILS_TYPE, &live_files)?
        else {
            return Ok(VueTypeShape {
                type_: SHOPWARE_UTILS_TYPE.to_owned(),
                ..Default::default()
            });
        };
        let (mut shape, mut current_context, _) = self.resolve_shopware_utility_declaration(
            &declaration,
            &declaration_context,
            &mut HashSet::new(),
            &live_files,
        )?;
        for name in &names {
            shape =
                self.materialize_shopware_utility_shape(shape, &current_context, &live_files)?;
            let Some(member) = named_member(&shape.members, name) else {
                return Ok(VueTypeShape::default());
            };
            if !member.nested_members.is_empty() || member.nested_complete {
                shape = VueTypeShape {
                    type_: member.type_,
                    members: member.nested_members,
                    complete: member.nested_complete,
                };
                continue;
            }
            let (next, next_context, resolved) = self.resolve_shopware_utility_value(
                &member.type_,
                &current_context,
                &mut HashSet::new(),
                &live_files,
            )?;
            if !resolved {
                return Ok(VueTypeShape::default());
            }
            shape = next;
            current_context = next_context;
        }
        self.materialize_shopware_utility_shape(shape, &current_context, &live_files)
    }

    /// The explicitly declared `Events` map behind
    /// `Shopware.Utils.EventBus`.
    pub fn resolve_shopware_event_bus_events(
        &self,
        context_path: &str,
    ) -> IndexResult<VueTypeShape> {
        let live_files = self.live_type_file_overlays(&[]);
        let Some((declaration, declaration_context)) =
            self.resolve_admin_type_declaration(context_path, SHOPWARE_UTILS_TYPE, &live_files)?
        else {
            return Ok(VueTypeShape::default());
        };
        let (root_shape, root_context, resolved) = self.resolve_shopware_utility_declaration(
            &declaration,
            &declaration_context,
            &mut HashSet::new(),
            &live_files,
        )?;
        if !resolved {
            return Ok(VueTypeShape::default());
        }
        let Some(event_bus) = named_member(&root_shape.members, "EventBus") else {
            return Ok(VueTypeShape::default());
        };
        if event_bus.type_.is_empty() {
            return Ok(VueTypeShape::default());
        }
        let (event_bus_shape, event_bus_context, resolved) = self.resolve_shopware_utility_value(
            &event_bus.type_,
            &root_context,
            &mut HashSet::new(),
            &live_files,
        )?;
        if !resolved {
            return Ok(VueTypeShape::default());
        }
        let (type_name, arguments) = parse_admin_named_type(&event_bus_shape.type_);
        if type_name != "Emitter" || arguments.len() != 1 {
            return Ok(VueTypeShape::default());
        }
        self.resolve_vue_type_impl(
            &arguments[0],
            &event_bus_context,
            &mut HashSet::new(),
            &HashMap::new(),
            &live_files,
        )
    }

    pub fn resolve_shopware_event_bus_event(
        &self,
        name: &str,
        context_path: &str,
    ) -> IndexResult<Option<TwigVueMember>> {
        let shape = self.resolve_shopware_event_bus_events(context_path)?;
        Ok(named_member(&shape.members, name))
    }

    /// Recognizes the typed key that owns a Shopware EventBus event; a
    /// same-named property in an unrelated interface is rejected by its
    /// resolved declaration source and range.
    pub(crate) fn shopware_event_bus_event_at_definition_range(
        &self,
        file_path: &str,
        range: TextRange,
        line_index: &LineIndex,
    ) -> IndexResult<Option<TwigVueMember>> {
        if file_path.is_empty() || range.end() <= range.start() {
            return Ok(None);
        }
        let live_files = self.live_type_file_overlays(&[]);
        let Some(type_file) = self.admin_type_file_for_resolution(file_path, &live_files)? else {
            return Ok(None);
        };
        for declaration in &type_file.declarations {
            for candidate in &declaration.members {
                if !admin_source_range_overlaps_text_range(
                    &candidate.definition_range,
                    range,
                    line_index,
                ) {
                    continue;
                }
                let Some(event) =
                    self.resolve_shopware_event_bus_event(&candidate.name, file_path)?
                else {
                    continue;
                };
                if normalize_definition_path(&event.definition_path)
                    != normalize_definition_path(file_path)
                    || event.definition_range != candidate.definition_range
                {
                    continue;
                }
                return Ok(Some(event));
            }
        }
        Ok(None)
    }

    pub(crate) fn materialize_shopware_utility_shape(
        &self,
        shape: VueTypeShape,
        context_path: &str,
        live_files: &HashMap<String, AdminTypeFile>,
    ) -> IndexResult<VueTypeShape> {
        let mut result = shape;
        for member in &mut result.members {
            if member.type_.is_empty()
                || !member.nested_members.is_empty()
                || member.nested_complete
            {
                continue;
            }
            let (resolved, _, found) = self.resolve_shopware_utility_value(
                &member.type_,
                context_path,
                &mut HashSet::new(),
                live_files,
            )?;
            if found && !resolved.type_.is_empty() {
                member.type_ = resolved.type_;
            }
        }
        Ok(result)
    }

    /// `(shape, context_path, found)` of a `Shopware.Utils` value.
    pub(crate) fn resolve_shopware_utility_value(
        &self,
        value: &str,
        context_path: &str,
        seen: &mut HashSet<String>,
        live_files: &HashMap<String, AdminTypeFile>,
    ) -> IndexResult<(VueTypeShape, String, bool)> {
        let value = value.trim();
        let shape_of = |type_: &str| VueTypeShape {
            type_: type_.to_owned(),
            ..Default::default()
        };
        if value.is_empty() {
            return Ok((VueTypeShape::default(), context_path.to_owned(), false));
        }
        if vue_callable_signature(value).is_some() {
            return Ok((shape_of(value), context_path.to_owned(), true));
        }
        let (named_type, type_arguments) = parse_admin_named_type(value);
        if !named_type.is_empty()
            && !type_arguments.is_empty()
            && let Some(builtin) =
                admin_javascript_builtin_shape(value, &named_type, &type_arguments)
        {
            return Ok((builtin, context_path.to_owned(), true));
        }
        let parts: Vec<&str> = value.split('.').collect();
        if !is_admin_type_identifier(parts[0]) {
            return Ok((shape_of(value), context_path.to_owned(), true));
        }
        let key = format!("{}\x00utility\x00{}", filepath::clean(context_path), value);
        if seen.contains(&key) {
            return Ok((shape_of(value), context_path.to_owned(), false));
        }
        seen.insert(key.clone());
        let result = self.resolve_shopware_utility_value_unseen(
            value,
            context_path,
            &parts,
            &named_type,
            &type_arguments,
            seen,
            live_files,
        );
        seen.remove(&key);
        result
    }

    fn resolve_shopware_utility_value_unseen(
        &self,
        value: &str,
        context_path: &str,
        parts: &[&str],
        named_type: &str,
        type_arguments: &[String],
        seen: &mut HashSet<String>,
        live_files: &HashMap<String, AdminTypeFile>,
    ) -> IndexResult<(VueTypeShape, String, bool)> {
        let Some((declaration, declaration_context)) =
            self.resolve_admin_type_declaration(context_path, parts[0], live_files)?
        else {
            if !named_type.is_empty()
                && let Some(builtin) =
                    admin_javascript_builtin_shape(value, named_type, type_arguments)
            {
                return Ok((builtin, context_path.to_owned(), true));
            }
            if self.shopware_utility_imported_value(context_path, parts[0], live_files) {
                return Ok((
                    VueTypeShape {
                        type_: "Function".to_owned(),
                        ..Default::default()
                    },
                    context_path.to_owned(),
                    true,
                ));
            }
            return Ok((
                VueTypeShape {
                    type_: value.to_owned(),
                    ..Default::default()
                },
                context_path.to_owned(),
                false,
            ));
        };
        let (mut shape, mut resolved_context, resolved) = self
            .resolve_shopware_utility_declaration(
                &declaration,
                &declaration_context,
                seen,
                live_files,
            )?;
        if !resolved {
            return Ok((shape, resolved_context, resolved));
        }
        for part in &parts[1..] {
            shape =
                self.materialize_shopware_utility_shape(shape, &resolved_context, live_files)?;
            let Some(member) = named_member(&shape.members, part) else {
                return Ok((VueTypeShape::default(), resolved_context, false));
            };
            if !member.nested_members.is_empty() || member.nested_complete {
                shape = VueTypeShape {
                    type_: member.type_,
                    members: member.nested_members,
                    complete: member.nested_complete,
                };
                continue;
            }
            let (next, next_context, resolved) = self.resolve_shopware_utility_value(
                &member.type_,
                &resolved_context,
                seen,
                live_files,
            )?;
            if !resolved {
                return Ok((next, next_context, resolved));
            }
            shape = next;
            resolved_context = next_context;
        }
        Ok((shape, resolved_context, true))
    }

    pub(crate) fn resolve_shopware_utility_declaration(
        &self,
        declaration: &AdminTypeDeclaration,
        context_path: &str,
        seen: &mut HashSet<String>,
        live_files: &HashMap<String, AdminTypeFile>,
    ) -> IndexResult<(VueTypeShape, String, bool)> {
        if !declaration.alias.is_empty() {
            return self.resolve_shopware_utility_value(
                &declaration.alias,
                context_path,
                seen,
                live_files,
            );
        }
        Ok((
            VueTypeShape {
                type_: declaration.name.clone(),
                members: declaration.members.clone(),
                complete: declaration.interface && !declaration.open,
            },
            context_path.to_owned(),
            true,
        ))
    }

    pub(crate) fn shopware_utility_imported_value(
        &self,
        context_path: &str,
        name: &str,
        live_files: &HashMap<String, AdminTypeFile>,
    ) -> bool {
        match self.admin_type_file_for_resolution(context_path, live_files) {
            Ok(Some(type_file)) => type_file
                .imports
                .iter()
                .any(|import| import.local_name == name),
            _ => false,
        }
    }

    pub(crate) fn resolve_shopware_utils_expression_type(
        &self,
        expression: &str,
        context_path: &str,
    ) -> IndexResult<Option<ResolvedVueExpressionType>> {
        let Some((segments, incomplete)) =
            javascript_static_expression(expression, "Shopware.Utils")
        else {
            return Ok(None);
        };
        let Some((last, receiver_segments)) = segments.split_last() else {
            return Ok(None);
        };
        if incomplete {
            return Ok(None);
        }
        if receiver_segments
            .iter()
            .any(|segment| segment.called || segment.indexed || segment.name.is_empty())
        {
            return Ok(None);
        }
        if last.indexed || last.name.is_empty() {
            return Ok(None);
        }
        let receiver: Vec<&str> = receiver_segments
            .iter()
            .map(|segment| segment.name.as_str())
            .collect();
        let shape = self.resolve_shopware_utils(&receiver.join("."), context_path)?;
        let Some(member) = named_member(&shape.members, &last.name) else {
            return Ok(None);
        };
        if member.type_.is_empty() {
            return Ok(None);
        }
        let mut member_type = member.type_;
        if last.called {
            member_type = vue_callable_return_type(&member_type);
            if member_type.is_empty() {
                return Ok(None);
            }
        }
        Ok(Some(ResolvedVueExpressionType {
            type_: member_type,
            context_path: member.definition_path,
            open_runtime_shape: false,
        }))
    }
}

/// Whether a persisted UTF-16 range overlaps a byte range of the document.
pub(crate) fn admin_source_range_overlaps_text_range(
    source_range: &AdminSourceRange,
    text_range: TextRange,
    line_index: &LineIndex,
) -> bool {
    if text_range.end() <= text_range.start() {
        return false;
    }
    let start = line_index.offset_utf16(source_range.start_line, source_range.start_character);
    let end = line_index.offset_utf16(source_range.end_line, source_range.end_character);
    start < u32::from(text_range.end()) && end > u32::from(text_range.start())
}
