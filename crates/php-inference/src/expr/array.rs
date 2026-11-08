//! Array literals and array access (port of `expression_array.go`).

use std::collections::HashMap;

use swlsp_parser_php::syntax::*;
use swlsp_php_types::{Joiner, Kind, ShapeField, Type};
use swlsp_syntax::SyntaxNode;

use crate::analyzer::Fs;
use crate::env::Env;
use crate::syntax::{class_constant_receiver, direct_nodes, has_direct_token, trim_quotes};
use crate::types_util::{
    MAX_INFERRED_TUPLE_FIELDS, array_literal_key, array_shape_iterable_types,
    array_type_known_non_empty, join_types, parse_go_int,
};

pub(crate) const SHAPE_FIELD_LINEAR_LIMIT: usize = 16;

#[derive(Clone, Copy, Default)]
pub(crate) struct ArrayLiteralStructure {
    pub(crate) item_count: usize,
    pub(crate) implicit_list: bool,
}

pub(crate) fn inspect_array_literal(node: &SyntaxNode) -> ArrayLiteralStructure {
    let mut structure = ArrayLiteralStructure {
        item_count: 0,
        implicit_list: true,
    };
    for item in node.children() {
        if item.kind() != PHP_ARRAY_ITEM {
            continue;
        }
        let count = item.children().count();
        if count == 0 {
            continue;
        }
        structure.item_count += 1;
        if has_direct_token(&item, TK_ELLIPSIS) || count > 1 {
            structure.implicit_list = false;
        }
    }
    structure
}

#[cfg(test)]
pub(crate) fn is_implicit_list_array(node: &SyntaxNode) -> bool {
    inspect_array_literal(node).implicit_list
}

/// Inserts or replaces a shape field, switching to an index map once the
/// linear scan would become expensive.
pub(crate) fn upsert_shape_field(
    fields: &mut Vec<ShapeField>,
    indices: &mut Option<HashMap<String, usize>>,
    field: ShapeField,
) {
    if let Some(indices) = indices.as_mut() {
        if let Some(&index) = indices.get(&*field.name) {
            fields[index] = field;
            return;
        }
        indices.insert(field.name.to_string(), fields.len());
        fields.push(field);
        return;
    }
    if let Some(existing) = fields
        .iter_mut()
        .find(|existing| existing.name == field.name)
    {
        *existing = field;
        return;
    }
    if fields.len() == SHAPE_FIELD_LINEAR_LIMIT {
        let mut map = HashMap::with_capacity(SHAPE_FIELD_LINEAR_LIMIT * 2);
        for (index, existing) in fields.iter().enumerate() {
            map.insert(existing.name.to_string(), index);
        }
        map.insert(field.name.to_string(), fields.len());
        *indices = Some(map);
    }
    fields.push(field);
}

struct ArrayLiteralInference<'r> {
    key_types: Joiner<'r>,
    value_types: Joiner<'r>,
    shape_fields: Vec<ShapeField>,
    shape_field_indices: Option<HashMap<String, usize>>,
    next_key: i64,
    shape_candidate: bool,
    list_candidate: bool,
    known_non_empty: bool,
}

impl ArrayLiteralInference<'_> {
    fn upsert(&mut self, field: ShapeField) {
        upsert_shape_field(&mut self.shape_fields, &mut self.shape_field_indices, field);
    }

    fn disable_shape(&mut self) {
        self.shape_candidate = false;
        self.shape_fields.clear();
        self.shape_field_indices = None;
    }

    fn add_spread_shape(&mut self, source: &Type) {
        if source.kind() == Kind::ArrayShape && !source.is_open_shape() {
            for field in source.fields() {
                let mut field = field.clone();
                let name = trim_quotes(&field.name).to_owned();
                if parse_go_int(&name).is_some() {
                    field.name = self.next_key.to_string().as_str().into();
                    self.next_key += 1;
                } else {
                    field.name = name.as_str().into();
                }
                self.upsert(field);
            }
        } else if matches!(source.kind(), Kind::Array | Kind::NonEmptyArray)
            && source.argument_count() == 2
            && source.argument(1).kind() == Kind::Never
        {
        } else {
            self.disable_shape();
        }
    }

    fn advance_next_key(&mut self, key: &Type) {
        if key.kind() != Kind::LiteralInt {
            return;
        }
        if let Some(numeric) = parse_go_int(key.name())
            && numeric >= self.next_key
        {
            self.next_key = numeric + 1;
        }
    }

    fn result(mut self) -> Type {
        let key_type = self.key_types.value();
        let value_type = self.value_types.value();
        if value_type.kind() == Kind::Never {
            return Type::array(Type::ARRAY_KEY, Type::NEVER);
        }
        if self.shape_candidate {
            return Type::array_shape(self.shape_fields, false);
        }
        if self.list_candidate {
            if self.known_non_empty {
                return Type::non_empty_list(value_type);
            }
            return Type::list(value_type);
        }
        if self.known_non_empty {
            return Type::non_empty_array(key_type, value_type);
        }
        Type::array(key_type, value_type)
    }
}

/// `arrays_iterable` is the array-spread projection of `arrayIterableTypes`.
pub(crate) fn arrays_iterable(
    value: &Type,
    relations: swlsp_php_types::Relations<'_>,
) -> (Type, Type) {
    match value.kind() {
        Kind::List | Kind::NonEmptyList => (Type::INT, value.argument(0)),
        Kind::Array | Kind::NonEmptyArray | Kind::Iterable => {
            (value.argument(0), value.argument(1))
        }
        Kind::ArrayShape => array_shape_iterable_types(value, relations),
        Kind::Union => {
            let mut keys = Joiner::new(relations, Type::NEVER);
            let mut values = Joiner::new(relations, Type::NEVER);
            for member in value.arguments() {
                let (key, element) = arrays_iterable(member, relations);
                keys.add(key);
                values.add(element);
            }
            (keys.value(), values.value())
        }
        _ => (Type::ARRAY_KEY, Type::UNKNOWN),
    }
}

impl Fs<'_, '_, '_> {
    pub(crate) fn infer_array(&mut self, node: &SyntaxNode, env: &Env) -> Type {
        let structure = inspect_array_literal(node);
        if structure.implicit_list {
            if structure.item_count > 0 && structure.item_count <= MAX_INFERRED_TUPLE_FIELDS {
                let mut fields = Vec::with_capacity(structure.item_count);
                for item in node.children() {
                    if item.kind() != PHP_ARRAY_ITEM {
                        continue;
                    }
                    let Some(value) = item.children().next() else {
                        continue;
                    };
                    let ty = self.infer(&value, env);
                    fields.push(ShapeField::new(&fields.len().to_string(), ty));
                }
                return Type::array_shape(fields, false);
            }
            let mut value_types = Joiner::new(self.relations, Type::NEVER);
            for item in node.children() {
                if item.kind() != PHP_ARRAY_ITEM {
                    continue;
                }
                let Some(value) = item.children().next() else {
                    continue;
                };
                value_types.add(self.infer(&value, env));
            }
            let value_type = value_types.value();
            if value_type.kind() == Kind::Never {
                return Type::array(Type::ARRAY_KEY, Type::NEVER);
            }
            return Type::non_empty_list(value_type);
        }
        let relations = self.relations;
        let mut inference = ArrayLiteralInference {
            key_types: Joiner::new(relations, Type::NEVER),
            value_types: Joiner::new(relations, Type::NEVER),
            shape_fields: Vec::with_capacity(structure.item_count),
            shape_field_indices: None,
            next_key: 0,
            shape_candidate: true,
            list_candidate: true,
            known_non_empty: false,
        };
        for item in node.children() {
            if item.kind() != PHP_ARRAY_ITEM {
                continue;
            }
            let values = direct_nodes(&item);
            if values.is_empty() {
                continue;
            }
            if has_direct_token(&item, TK_ELLIPSIS) {
                let source = self.infer(&values[values.len() - 1], env);
                inference.known_non_empty =
                    inference.known_non_empty || array_type_known_non_empty(&source);
                let (key, value) = arrays_iterable(&source, relations);
                if !self.array_spread_produces_list(&source) {
                    inference.list_candidate = false;
                }
                if inference.shape_candidate {
                    inference.add_spread_shape(&source);
                }
                inference.key_types.add(key);
                inference.value_types.add(value);
                continue;
            }
            inference.known_non_empty = true;
            if values.len() > 1 {
                inference.list_candidate = false;
                let key = self.infer(&values[0], env);
                let value = self.infer(&values[values.len() - 1], env);
                match array_literal_key(&key) {
                    Some(name) => {
                        if inference.shape_candidate {
                            inference.upsert(ShapeField::new(name, value.clone()));
                            inference.advance_next_key(&key);
                        }
                    }
                    None => inference.disable_shape(),
                }
                inference.key_types.add(key);
                inference.value_types.add(value);
                continue;
            }
            let name = inference.next_key.to_string();
            let key = Type::literal_int(&name);
            let value = self.infer(&values[0], env);
            if inference.shape_candidate {
                inference.upsert(ShapeField::new(&name, value.clone()));
            }
            inference.next_key += 1;
            inference.key_types.add(key);
            inference.value_types.add(value);
        }
        inference.result()
    }

    /// The literal field name of an array key expression, including
    /// `Foo::class`.
    pub(crate) fn array_field_name(&mut self, node: &SyntaxNode, env: &Env) -> Option<String> {
        if let Some(value) = swlsp_php_literal::type_of(node) {
            return array_literal_key(&value).map(str::to_owned);
        }
        let receiver_node = class_constant_receiver(self.src(), Some(node))?;
        let receiver = self.infer_receiver(&receiver_node, env, true);
        if receiver.kind() != Kind::Object || receiver.name().is_empty() {
            return None;
        }
        Some(receiver.name().to_owned())
    }

    fn array_spread_produces_list(&self, value: &Type) -> bool {
        if value.kind() == Kind::Union {
            return value.argument_count() > 0
                && value
                    .arguments()
                    .iter()
                    .all(|member| self.array_spread_produces_list(member));
        }
        let (key, element) = arrays_iterable(value, self.relations);
        element.kind() == Kind::Never
            || !key.is_unknown() && self.relations.is_subtype(&key, &Type::INT)
    }

    pub(crate) fn infer_array_access(&mut self, node: &SyntaxNode, env: &Env) -> Type {
        let nodes = direct_nodes(node);
        if nodes.is_empty() {
            return Type::UNKNOWN;
        }
        let receiver = self.infer(&nodes[0], env);
        let key = if nodes.len() > 1 {
            self.infer(&nodes[nodes.len() - 1], env)
        } else {
            Type::UNKNOWN
        };
        self.array_access_type(&receiver, &key)
    }

    pub(crate) fn array_access_type(&self, receiver: &Type, key: &Type) -> Type {
        match receiver.kind() {
            // Chained access through a definitely absent element stays
            // impossible so isset() can discard that branch.
            Kind::Never => Type::NEVER,
            Kind::Union => {
                let members: Vec<Type> = receiver
                    .arguments()
                    .iter()
                    .map(|alternative| self.array_access_type(alternative, key))
                    .filter(|value| !value.is_unknown())
                    .collect();
                join_types(self.relations, &members)
            }
            Kind::Array | Kind::NonEmptyArray | Kind::Iterable
                if receiver.argument_count() == 2 =>
            {
                receiver.argument(1)
            }
            Kind::List | Kind::NonEmptyList if receiver.argument_count() == 1 => {
                receiver.argument(0)
            }
            Kind::String | Kind::LiteralString => Type::STRING,
            Kind::ArrayShape if matches!(key.kind(), Kind::LiteralString | Kind::LiteralInt) => {
                for field in receiver.fields() {
                    if trim_quotes(&field.name) == key.name() {
                        if field.optional {
                            return Type::nullable(field.ty.clone());
                        }
                        return field.ty.clone();
                    }
                }
                Type::UNKNOWN
            }
            _ => Type::UNKNOWN,
        }
    }
}
