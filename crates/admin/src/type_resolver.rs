//! Structural TypeScript type resolution (port of Go `type_resolver.go`).

use crate::*;

pub(crate) struct VueTypeResolver<'a> {
    pub index: &'a AdminComponentIndexer,
    pub context_path: String,
    pub seen: &'a TypeSeen,
    pub substitutions: HashMap<String, String>,
    pub live_files: &'a HashMap<String, AdminTypeFile>,
}

impl VueTypeResolver<'_> {
    pub(crate) fn resolve(&self, type_expression: &str) -> IndexResult<VueTypeShape> {
        let substituted = substitute_admin_type(type_expression, &self.substitutions);
        let mut value = substituted.trim().to_owned();
        let mut result = VueTypeShape {
            type_: value.clone(),
            ..Default::default()
        };
        if value.is_empty() {
            return Ok(result);
        }
        let asserted = vue_type_assertion(&value);
        if !asserted.is_empty() {
            value = asserted;
        }
        let value = trim_admin_type_parentheses(&value).to_owned();
        result.type_ = value.clone();
        if let Some(shape) = self.resolve_wrapper(&value) {
            return shape;
        }
        let union = split_admin_type_top_level(&value, b'|');
        if union.len() > 1 {
            return self.resolve_union(&value, &union);
        }
        let intersection = split_admin_type_top_level(&value, b'&');
        if intersection.len() > 1 {
            return self.resolve_intersection(&value, &intersection);
        }
        if let Some(shape) = self.resolve_array(&value) {
            return Ok(shape);
        }
        if let Some(shape) = self.resolve_object(&value) {
            return Ok(shape);
        }
        let (name, arguments) = parse_admin_named_type(&value);
        if name.is_empty() {
            result.complete = admin_primitive_type(&value);
            return Ok(result);
        }
        if let Some(shape) = self.resolve_special(&value, &name, &arguments) {
            return shape;
        }
        self.resolve_declaration(&value, &name, &arguments)
    }

    fn resolve_wrapper(&self, value: &str) -> Option<IndexResult<VueTypeShape>> {
        for wrapper in ["Readonly", "Partial", "Required", "NonNullable", "PropType"] {
            let Some(inner) = admin_type_generic_inner(value, wrapper) else {
                continue;
            };
            return Some(self.resolve(&inner).map(|mut shape| {
                if wrapper == "Partial" || wrapper == "Required" {
                    for member in &mut shape.members {
                        member.optional = wrapper == "Partial";
                    }
                }
                shape.type_ = value.to_owned();
                shape
            }));
        }
        None
    }

    fn resolve_union(&self, value: &str, branches: &[String]) -> IndexResult<VueTypeShape> {
        let mut result = VueTypeShape {
            type_: value.to_owned(),
            ..Default::default()
        };
        let mut shapes = Vec::new();
        for branch in branches {
            let branch = branch.trim();
            if nullable_admin_type_branch(branch) {
                continue;
            }
            shapes.push(self.resolve(branch)?);
        }
        match shapes.len() {
            0 => result.complete = true,
            1 => {
                let mut shape = shapes.pop().unwrap_or_default();
                shape.type_ = value.to_owned();
                return Ok(shape);
            }
            _ => {
                result.members = union_vue_type_members(&shapes);
                result.complete = all_vue_type_shapes_complete(&shapes);
            }
        }
        Ok(result)
    }

    fn resolve_intersection(&self, value: &str, branches: &[String]) -> IndexResult<VueTypeShape> {
        let mut result = VueTypeShape {
            type_: value.to_owned(),
            complete: true,
            ..Default::default()
        };
        for branch in branches {
            let shape = self.resolve(branch)?;
            result.members = merge_twig_vue_members(&result.members, &shape.members);
            result.complete = result.complete && shape.complete;
        }
        Ok(result)
    }

    fn resolve_array(&self, value: &str) -> Option<VueTypeShape> {
        let array_value = value.strip_prefix("readonly ").unwrap_or(value).trim();
        let element = array_value.strip_suffix("[]")?;
        admin_javascript_builtin_shape(value, "Array", &[element.trim().to_owned()])
    }

    fn resolve_object(&self, value: &str) -> Option<VueTypeShape> {
        // Only a top-level object literal describes the current receiver.
        if !value.starts_with('{') || balanced_brace_end(value, 0) != Some(value.len() - 1) {
            return None;
        }
        let mut result = VueTypeShape {
            type_: value.to_owned(),
            members: vue_type_members(value),
            complete: !admin_object_type_has_index_signature(value, 0),
        };
        for member in &mut result.members {
            member.definition_path = self.context_path.clone();
        }
        Some(result)
    }

    fn resolve_special(
        &self,
        value: &str,
        name: &str,
        arguments: &[String],
    ) -> Option<IndexResult<VueTypeShape>> {
        if let Some(builtin) = admin_javascript_builtin_shape(value, name, arguments) {
            return Some(Ok(builtin));
        }
        if (name == "Pick" || name == "Omit") && arguments.len() == 2 {
            return Some(self.resolve_pick_or_omit(value, name, arguments));
        }
        if let Some(entity_name) = admin_named_string_generic(name, arguments, "Repository") {
            return Some(Ok(admin_repository_shape(value, &entity_name)));
        }
        if let Some(store_name) = admin_named_string_generic(name, arguments, "AdminStore") {
            return Some(self.index.resolve_admin_store_type(value, &store_name));
        }
        if let Some((entity_name, collection)) = admin_entity_generic(name, arguments) {
            if collection {
                return Some(Ok(admin_entity_collection_shape(value, &entity_name)));
            }
            return Some(self.index.resolve_admin_entity_type(
                value,
                &entity_name,
                &self.context_path,
                self.seen,
                self.live_files,
            ));
        }
        None
    }

    fn resolve_pick_or_omit(
        &self,
        value: &str,
        name: &str,
        arguments: &[String],
    ) -> IndexResult<VueTypeShape> {
        let mut shape = self.resolve(&arguments[0])?;
        let (keys, complete) = vue_string_literal_union_values(&substitute_admin_type(
            &arguments[1],
            &self.substitutions,
        ));
        let selected: HashSet<&str> = keys.iter().map(String::as_str).collect();
        if name == "Pick" && !complete {
            return Ok(VueTypeShape {
                type_: value.to_owned(),
                ..Default::default()
            });
        }
        let omit = name == "Omit";
        shape
            .members
            .retain(|member| selected.contains(member.name.as_str()) != omit);
        shape.type_ = value.to_owned();
        shape.complete = shape.complete && complete;
        Ok(shape)
    }

    fn resolve_declaration(
        &self,
        value: &str,
        name: &str,
        arguments: &[String],
    ) -> IndexResult<VueTypeShape> {
        let mut result = VueTypeShape {
            type_: value.to_owned(),
            ..Default::default()
        };
        let key = format!(
            "{}\x00{}\x00{}",
            filepath::clean(&self.context_path),
            name,
            arguments.join("\x00")
        );
        let Some(_guard) = SeenGuard::enter(self.seen, key) else {
            return Ok(result);
        };
        let Some((declaration, declaration_context)) =
            self.index
                .resolve_admin_type_declaration(&self.context_path, name, self.live_files)?
        else {
            result.complete = admin_primitive_type(value);
            return Ok(result);
        };
        let local_substitutions =
            declaration_substitutions(&declaration, arguments, &self.substitutions);
        result.members = substitute_vue_type_members(&declaration.members, &local_substitutions);
        result.complete = declaration.interface && !declaration.open;
        let child = VueTypeResolver {
            index: self.index,
            context_path: declaration_context,
            seen: self.seen,
            substitutions: local_substitutions,
            live_files: self.live_files,
        };
        for parent in &declaration.extends {
            let shape = child.resolve(parent)?;
            result.members = merge_twig_vue_members(&shape.members, &result.members);
            result.complete = result.complete && shape.complete;
        }
        if !declaration.alias.is_empty() {
            let shape = child.resolve(&declaration.alias)?;
            result.members = merge_twig_vue_members(&shape.members, &result.members);
            result.complete = shape.complete;
        }
        Ok(result)
    }
}

pub(crate) fn nullable_admin_type_branch(value: &str) -> bool {
    matches!(value, "null" | "undefined" | "never")
}

pub(crate) fn all_vue_type_shapes_complete(shapes: &[VueTypeShape]) -> bool {
    shapes.iter().all(|shape| shape.complete)
}

pub(crate) fn declaration_substitutions(
    declaration: &AdminTypeDeclaration,
    arguments: &[String],
    substitutions: &HashMap<String, String>,
) -> HashMap<String, String> {
    let mut result = substitutions.clone();
    for (parameter_index, parameter) in declaration.parameters.iter().enumerate() {
        if let Some(argument) = arguments.get(parameter_index) {
            result.insert(
                parameter.clone(),
                substitute_admin_type(argument, substitutions),
            );
        }
    }
    result
}
