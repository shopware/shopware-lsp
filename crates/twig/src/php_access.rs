//! Twig attribute access resolved against the PHP semantic graph.

use std::cell::RefCell;
use std::collections::{HashMap, HashSet};
use std::rc::Rc;
use std::sync::Arc;
use swlsp_syntax::SyntaxNodeExt as _;

use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::*;
use swlsp_php::PhpIndex;
use swlsp_php_resolver::{MemberResolver, NameContext, ResolvedMember};
use swlsp_php_semantic::{Flags, Snapshot, Symbol, SymbolKind, Visibility};
use swlsp_php_types::{Kind as TypeKind, Type};
use swlsp_syntax::{Kind, SyntaxNode};
use swlsp_textutil::eq_ignore_case;

use crate::extension_parser::{callback_method_name, last_name_part};
use crate::indexer::TwigIndexer;
use crate::inference::{SetIndex, set_index};
use crate::macros::{direct_child, literal_name};
use crate::path::template_names;
use crate::template_variable::{first_token_offset, is_descendant_node};
use crate::types_tag::types_tag_declarations;

pub const TWIG_LOOP_CONTEXT_CLASS: &str = "Twig\\Runtime\\LoopContext";

const TWIG3_LOOP_VARIABLES: [&str; 8] = [
    "index",
    "index0",
    "revindex",
    "revindex0",
    "first",
    "last",
    "length",
    "parent",
];

/// The fixed loop keys exposed by Twig 3. Twig 4 replaces this array-shaped
/// value with `Twig\Runtime\LoopContext`.
pub fn twig3_loop_variables() -> Vec<String> {
    TWIG3_LOOP_VARIABLES
        .iter()
        .map(|name| (*name).to_owned())
        .collect()
}

/// Twig 4's loop object when that runtime is installed.
pub fn loop_context_type(index: Option<&PhpIndex>) -> Option<Type> {
    index?.find_class(TWIG_LOOP_CONTEXT_CLASS)?;
    Some(Type::named(TWIG_LOOP_CONTEXT_CLASS, Vec::new()))
}

/// Whether `accessor` is a direct `loop.member` access inside the body of a
/// Twig for loop. A for-else body does not expose that loop's context.
pub fn loop_accessor_in_scope(accessor: &SyntaxNode) -> bool {
    if !direct_loop_accessor(accessor) {
        return false;
    }
    let offset = accessor.text_range().start();
    for current in accessor.ancestors().skip(1) {
        if current.kind() != TWIG_FOR {
            continue;
        }
        if let Some(body) = current.children().find(|child| child.kind() == BODY)
            && body.text_range().contains(offset)
        {
            return true;
        }
    }
    false
}

fn direct_loop_accessor(accessor: &SyntaxNode) -> bool {
    if accessor.kind() != TWIG_ACCESSOR {
        return false;
    }
    let operands = direct_twig_children(accessor, TWIG_OPERAND);
    let Some(first) = operands.first() else {
        return false;
    };
    first_twig_child(first).is_some_and(|root| {
        root.kind() == TWIG_LITERAL_NAME && eq_ignore_case(root.text_string().trim(), "loop")
    })
}

/// The result of resolving one Twig attribute access.
#[derive(Clone, Debug)]
pub struct PhpMemberResolution {
    pub name: String,
    pub name_node: SyntaxNode,
    pub receiver: Type,
    pub members: Vec<ResolvedMember>,
}

/// Maps Twig's attribute syntax onto the shared PHP semantic graph,
/// independent of LSP transport so every feature resolves the same target.
#[derive(Clone)]
pub struct PhpAccessResolver<'a> {
    pub php: Option<&'a PhpIndex>,
    pub twig: Option<&'a TwigIndexer>,
    snapshot: Arc<Snapshot>,
    type_annotations: Option<(SyntaxNode, Arc<HashMap<String, Type>>)>,
    sets: Option<(SyntaxNode, Rc<SetIndex>)>,
    /// The context types of the prepared document by name: they do not
    /// depend on where a name is read.
    context_names: Rc<RefCell<HashMap<String, Type>>>,
    /// What the templates around the prepared document provide.
    pub(crate) family: Rc<RefCell<Option<Rc<crate::undefined::Family>>>>,
    /// The types the assignments of the prepared document give, by the
    /// offset of the `set` tag and the variable name.
    pub(crate) bindings: Rc<RefCell<HashMap<(u32, String), Type>>>,
    /// The known subclasses of a class, by its lowercase name.
    subtypes: Rc<RefCell<HashMap<String, Rc<Vec<String>>>>>,
}

impl<'a> PhpAccessResolver<'a> {
    pub fn new(php: Option<&'a PhpIndex>, twig: Option<&'a TwigIndexer>) -> Self {
        let snapshot = match php {
            Some(php) => php.semantic_snapshot(),
            None => Snapshot::new(0, &[]),
        };
        PhpAccessResolver {
            php,
            twig,
            snapshot,
            type_annotations: None,
            sets: None,
            context_names: Rc::default(),
            family: Rc::default(),
            bindings: Rc::default(),
            subtypes: Rc::default(),
        }
    }

    pub fn snapshot(&self) -> &Arc<Snapshot> {
        &self.snapshot
    }

    /// Prepares immutable document-wide facts consulted repeatedly while
    /// resolving one Twig tree.
    pub fn for_document(mut self, root: &SyntaxNode) -> Self {
        if self
            .type_annotations
            .as_ref()
            .is_some_and(|(cached, _)| cached == root)
        {
            return self;
        }
        self.type_annotations = Some((root.clone(), Arc::new(twig_type_annotations(root))));
        self.sets = Some((root.clone(), Rc::new(set_index(root))));
        self.context_names = Rc::default();
        self.family = Rc::default();
        self.bindings = Rc::default();
        self
    }

    /// Whether [`Self::for_document`] prepared this resolver for `root`.
    pub(crate) fn is_prepared(&self, root: &SyntaxNode) -> bool {
        self.type_annotations
            .as_ref()
            .is_some_and(|(cached, _)| cached == root)
    }

    /// The `{% set %}` tags prepared by [`Self::for_document`] for `root`.
    pub(crate) fn prepared_sets(&self, root: &SyntaxNode) -> Option<Rc<SetIndex>> {
        self.sets
            .as_ref()
            .filter(|(cached, _)| cached == root)
            .map(|(_, sets)| sets.clone())
    }

    pub fn resolve_name(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        name_node: &SyntaxNode,
    ) -> Option<PhpMemberResolution> {
        if name_node.kind() != TWIG_LITERAL_NAME {
            return None;
        }
        let accessor = name_node.ancestors().skip(1).find(|current| {
            current.kind() == TWIG_ACCESSOR
                && accessor_member_name_node(current).as_ref() == Some(name_node)
        })?;
        self.resolve_accessor(template_path, root, &accessor)
    }

    pub fn resolve_accessor(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        accessor: &SyntaxNode,
    ) -> Option<PhpMemberResolution> {
        self.accessor_resolution(template_path, root, accessor)
            .filter(|resolution| !resolution.members.is_empty())
    }

    /// Enough information to distinguish a definitely missing PHP member
    /// from an unknown/dynamic Twig access. Arrays, collection-like objects,
    /// unresolved classes, broad object types and magic accessors are
    /// rejected to keep diagnostics low-noise.
    pub fn inspect_accessor(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        accessor: &SyntaxNode,
    ) -> Option<PhpMemberResolution> {
        self.accessor_resolution(template_path, root, accessor)
            .filter(|resolution| {
                definite_twig_object_receiver(&self.snapshot, &resolution.receiver)
            })
            // A value declared as a base class may be one of its subclasses
            // at runtime: a member only they have is not a mistake.
            .filter(|resolution| {
                !resolution.members.is_empty()
                    || !self.subtype_has_member(&resolution.receiver, &resolution.name)
            })
    }

    /// Whether a known subclass of a class in `receiver` has the member
    /// `name`.
    fn subtype_has_member(&self, receiver: &Type, name: &str) -> bool {
        match receiver.kind() {
            TypeKind::Union | TypeKind::Intersection => receiver
                .arguments()
                .iter()
                .any(|member| self.subtype_has_member(member, name)),
            TypeKind::Object => {
                let class = receiver.name().trim_matches('\\');
                self.subtypes(class).iter().any(|subtype| {
                    !resolve_php_members(&self.snapshot, &Type::named(subtype, Vec::new()), name)
                        .is_empty()
                })
            }
            _ => false,
        }
    }

    /// The known classes below `class`, found once per class.
    fn subtypes(&self, class: &str) -> Rc<Vec<String>> {
        let key = swlsp_textutil::lowercase(class);
        if let Some(known) = self.subtypes.borrow().get(&key) {
            return known.clone();
        }
        let found: Vec<String> = self
            .snapshot
            .global_class_views()
            .into_iter()
            .map(|view| view.fully_qualified())
            .filter(|name| !eq_ignore_case(name, class) && self.snapshot.is_subtype_of(name, class))
            .map(str::to_owned)
            .collect();
        let found = Rc::new(found);
        self.subtypes.borrow_mut().insert(key, found.clone());
        found
    }

    /// An access to a member of a value that can be null where nothing
    /// around it rules null out or tolerates it. Reported only for a
    /// receiver that is otherwise a definite object with that member.
    pub fn inspect_null_access(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        accessor: &SyntaxNode,
    ) -> Option<PhpMemberResolution> {
        let resolution = self.inspect_accessor(template_path, root, accessor)?;
        if resolution.members.is_empty()
            || !crate::inference::is_nullable(&resolution.receiver)
            || crate::narrowing::tolerates_null(accessor)
        {
            return None;
        }
        Some(resolution)
    }

    fn accessor_resolution(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        accessor: &SyntaxNode,
    ) -> Option<PhpMemberResolution> {
        let name_node = accessor_member_name_node(accessor)?;
        let name = name_node.text_string().trim().to_owned();
        let receiver = self.accessor_receiver_type(template_path, root, accessor);
        if receiver.is_unknown() {
            return None;
        }
        let members = resolve_php_members(&self.snapshot, &receiver, &name);
        Some(PhpMemberResolution {
            name,
            name_node,
            receiver,
            members,
        })
    }

    pub fn accessor_receiver_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        accessor: &SyntaxNode,
    ) -> Type {
        if accessor.kind() != TWIG_ACCESSOR {
            return Type::unknown();
        }
        if loop_accessor_in_scope(accessor)
            && let Some(loop_type) = loop_context_type(self.php)
        {
            return loop_type;
        }
        let operands = direct_twig_children(accessor, TWIG_OPERAND);
        let Some(first) = operands.first() else {
            return Type::unknown();
        };
        self.expression_type(template_path, root, first_twig_child(first).as_ref())
    }

    pub(crate) fn expression_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        node: Option<&SyntaxNode>,
    ) -> Type {
        let Some(node) = node else {
            return Type::unknown();
        };
        let Some(_level) = crate::inference::enter() else {
            return Type::unknown();
        };
        let value = self.unnarrowed_type(template_path, root, node);
        // A condition around the expression may rule out null.
        if crate::inference::is_nullable(&value)
            && matches!(
                node.kind(),
                TWIG_LITERAL_NAME | TWIG_ACCESSOR | TWIG_FUNCTION_CALL
            )
            && let Some(path) = crate::narrowing::expression_path(node)
            && crate::narrowing::non_null_paths(node).contains(&path)
        {
            return crate::inference::without_null(&value);
        }
        value
    }

    fn unnarrowed_type(&self, template_path: &str, root: &SyntaxNode, node: &SyntaxNode) -> Type {
        match node.kind() {
            TWIG_EXPRESSION | TWIG_OPERAND | TWIG_PARENTHESES_EXPRESSION => {
                self.expression_type(template_path, root, first_twig_child(node).as_ref())
            }
            TWIG_LITERAL_NAME => {
                let name = node.text_string().trim().to_owned();
                if let Some(local) = self.loop_variable_type(template_path, root, node, &name) {
                    return local;
                }
                if let Some(local) = self.local_name_type(template_path, root, node, &name) {
                    return local;
                }
                self.context_name_type(template_path, root, node, &name)
            }
            TWIG_ACCESSOR => {
                let Some(name_node) = accessor_member_name_node(node) else {
                    return Type::unknown();
                };
                let receiver = self.accessor_receiver_type(template_path, root, node);
                let text = name_node.text_string();
                let name = text.trim();
                let value = self.attribute_type(&receiver, name);
                let value = self.framework_member_type(template_path, root, &receiver, name, value);
                // `a?.b` is null when `a` is.
                if crate::narrowing::is_null_safe(node)
                    && crate::inference::is_nullable(&receiver)
                    && !value.is_unknown()
                {
                    return Type::nullable(value);
                }
                value
            }
            TWIG_FUNCTION_CALL => {
                let operands = direct_twig_children(node, TWIG_OPERAND);
                let Some(first) = operands.first() else {
                    return Type::unknown();
                };
                let name_expression = first_twig_child(first);
                if let Some(name_expression) = &name_expression
                    && name_expression.kind() == TWIG_LITERAL_NAME
                {
                    let text = name_expression.text_string();
                    let name = text.trim();
                    return self
                        .builtin_function_type(template_path, root, node, name)
                        .unwrap_or_else(|| self.twig_function_type(name));
                }
                self.expression_type(template_path, root, name_expression.as_ref())
            }
            TWIG_FILTER => {
                let name = twigquery::filter_name(node).unwrap_or_default();
                self.builtin_filter_type(template_path, root, node, &name)
                    .unwrap_or_else(|| self.twig_filter_type(&name))
            }
            TWIG_INDEX_LOOKUP => self.index_lookup_type(template_path, root, node),
            TWIG_CONDITIONAL_EXPRESSION => self.conditional_type(template_path, root, node),
            TWIG_BINARY_EXPRESSION => self.binary_type(template_path, root, node),
            TWIG_UNARY_EXPRESSION => self.unary_type(template_path, root, node),
            TWIG_LITERAL_STRING => Type::string(),
            TWIG_LITERAL_NUMBER if node.text_string().contains('.') => Type::float(),
            TWIG_LITERAL_NUMBER => Type::int(),
            TWIG_LITERAL_BOOLEAN => Type::bool(),
            TWIG_LITERAL_NULL => Type::null(),
            TWIG_LITERAL_ARRAY => self.array_literal_type(template_path, root, node),
            TWIG_LITERAL_HASH => self.hash_literal_type(template_path, root, node),
            _ => Type::unknown(),
        }
    }

    /// The type of a name the template does not bind itself: a declared,
    /// rendered or global variable. A macro sees only its own annotations
    /// and the globals.
    pub(crate) fn context_name_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        node: &SyntaxNode,
        name: &str,
    ) -> Type {
        let in_macro = node
            .ancestors()
            .any(|ancestor| ancestor.kind() == TWIG_MACRO);
        if !in_macro {
            return self.root_name_type(template_path, root, name);
        }
        let annotated = self
            .type_annotations
            .as_ref()
            .filter(|(cached, _)| cached == root)
            .and_then(|(_, annotations)| annotations.get(name).cloned());
        annotated.unwrap_or_else(|| self.global_type(name))
    }

    pub(crate) fn root_name_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        name: &str,
    ) -> Type {
        if name.is_empty() {
            return Type::unknown();
        }
        let prepared = self
            .type_annotations
            .as_ref()
            .is_some_and(|(cached, _)| cached == root);
        if !prepared {
            return self.uncached_root_name_type(template_path, root, name);
        }
        if let Some(known) = self.context_names.borrow().get(name) {
            return known.clone();
        }
        let value = self.uncached_root_name_type(template_path, root, name);
        self.context_names
            .borrow_mut()
            .insert(name.to_owned(), value.clone());
        value
    }

    fn uncached_root_name_type(&self, template_path: &str, root: &SyntaxNode, name: &str) -> Type {
        let cached = self
            .type_annotations
            .as_ref()
            .filter(|(cached, _)| cached == root)
            .map(|(_, annotations)| annotations.clone());
        let annotations = cached.unwrap_or_else(|| Arc::new(twig_type_annotations(root)));
        if let Some(annotated) = annotations.get(name) {
            return annotated.clone();
        }
        let mut values = Vec::new();
        if let Some(php) = self.php {
            let variables = php
                .twig_template_variables(&template_names(template_path))
                .unwrap_or_default();
            for variable in variables {
                if variable.name != name {
                    continue;
                }
                if let Ok(value) = swlsp_php_types::parse(&variable.ty)
                    && !value.is_unknown()
                {
                    values.push(value);
                }
            }
        }
        if values.is_empty() {
            let global = self.global_type(name);
            if !global.is_unknown() {
                return global;
            }
            return self.inherited_name_type(template_path, root, name);
        }
        union_known_types(values)
    }

    /// The type of the Twig global `name`.
    pub(crate) fn global_type(&self, name: &str) -> Type {
        let Some(twig) = self.twig else {
            return match name {
                "app" => self.app_variable_type(),
                _ => Type::unknown(),
            };
        };
        let values = twig
            .get_globals(name)
            .unwrap_or_default()
            .into_iter()
            .filter_map(|global| swlsp_php_types::parse(&global.ty).ok())
            .filter(|value| !value.is_unknown())
            .collect();
        let global = union_known_types(values);
        if global.is_unknown() && name == "app" {
            return self.app_variable_type();
        }
        global
    }

    pub(crate) fn twig_function_type(&self, name: &str) -> Type {
        let Some(twig) = self.twig else {
            return Type::unknown();
        };
        if name.is_empty() {
            return Type::unknown();
        }
        let mut values = Vec::new();
        for definition in twig.get_twig_function(name).unwrap_or_default() {
            values.extend(self.callback_return_types(&definition.file_path, &definition.method));
        }
        union_known_types(values)
    }

    pub(crate) fn twig_filter_type(&self, name: &str) -> Type {
        let Some(twig) = self.twig else {
            return Type::unknown();
        };
        if name.is_empty() {
            return Type::unknown();
        }
        let mut values = Vec::new();
        for definition in twig.get_twig_filter(name).unwrap_or_default() {
            values.extend(self.callback_return_types(&definition.file_path, &definition.method));
        }
        union_known_types(values)
    }

    fn callback_return_types(&self, path: &str, callback: &str) -> Vec<Type> {
        if self.php.is_none() {
            return Vec::new();
        }
        let method_name = callback_method_name(callback);
        if method_name.is_empty() {
            return Vec::new();
        }
        let members = MemberResolver::new(&self.snapshot);
        let mut result = Vec::new();
        for symbol in self.snapshot.symbols_in(path) {
            if !symbol.is_class_like() || !is_semantic_twig_extension_class(&self.snapshot, &symbol)
            {
                continue;
            }
            for member in members.methods(
                &Type::named(&symbol.fully_qualified, Vec::new()),
                method_name,
            ) {
                if !member.ty.is_unknown() {
                    result.push(member.ty);
                }
            }
        }
        if !result.is_empty() {
            return result;
        }
        let receiver = callback_receiver_class(callback);
        if receiver.is_empty() {
            return Vec::new();
        }
        for class in self.snapshot.classes(&receiver) {
            for member in members.methods(
                &Type::named(&class.fully_qualified, Vec::new()),
                method_name,
            ) {
                if !member.ty.is_unknown() {
                    result.push(member.ty);
                }
            }
        }
        result
    }

    /// The method declarations a registered Twig callback points at.
    pub(crate) fn callback_method_symbols(&self, path: &str, callback: &str) -> Vec<Symbol> {
        let Some(php) = self.php else {
            return Vec::new();
        };
        let method_name = callback_method_name(callback);
        if method_name.is_empty() {
            return Vec::new();
        }
        let receiver = callback_receiver_class(callback);
        if !receiver.is_empty() {
            return php.find_methods(&receiver, method_name);
        }
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for class in self.snapshot.symbols_in(path) {
            if !class.is_class_like() {
                continue;
            }
            for method in php.find_methods(&class.fully_qualified, method_name) {
                if seen.insert(method.id.clone()) {
                    result.push(method);
                }
            }
        }
        result
    }

    /// The key and value variables introduced by enclosing Twig for loops.
    /// Only the loop body owns them.
    pub(crate) fn loop_variable_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        name_node: &SyntaxNode,
        name: &str,
    ) -> Option<Type> {
        if name.is_empty() {
            return None;
        }
        for current in name_node.ancestors().skip(1) {
            if current.kind() != TWIG_FOR || !twig_for_body_contains(&current, name_node) {
                continue;
            }
            let Some(start) = direct_child(&current, TWIG_FOR_BLOCK) else {
                continue;
            };
            let (names, collection) = twig_for_binding(&start);
            let Some(collection) = collection else {
                continue;
            };
            if names.is_empty() {
                continue;
            }
            let (key_type, value_type) =
                self.iterable_types(&self.expression_type(template_path, root, Some(&collection)));
            if names.len() == 1 && names[0] == name {
                return Some(value_type);
            }
            if names.len() > 1 && names[0] == name {
                return Some(key_type);
            }
            if names.len() > 1 && names[1] == name {
                return Some(value_type);
            }
        }
        None
    }

    /// The key and element types a value exposes in a Twig for loop.
    pub fn iterable_types(&self, value: &Type) -> (Type, Type) {
        match value.kind() {
            TypeKind::Array | TypeKind::Iterable if value.argument_count() == 2 => {
                return (value.argument(0), value.argument(1));
            }
            TypeKind::List if value.argument_count() == 1 => {
                return (Type::int(), value.argument(0));
            }
            TypeKind::ArrayShape => {
                let values = value
                    .fields()
                    .iter()
                    .map(|field| field.ty.clone())
                    .collect();
                return (Type::array_key(), union_known_types(values));
            }
            TypeKind::Object if self.twig_traversable_object(value) => match value.argument_count()
            {
                1 => return (Type::array_key(), value.argument(0)),
                2 => return (value.argument(0), value.argument(1)),
                _ => {}
            },
            TypeKind::Union | TypeKind::Intersection => {
                let mut keys = Vec::new();
                let mut values = Vec::new();
                for member in value.arguments() {
                    let (key, element) = self.iterable_types(member);
                    if !key.is_unknown() {
                        keys.push(key);
                    }
                    if !element.is_unknown() {
                        values.push(element);
                    }
                }
                return (union_known_types(keys), union_known_types(values));
            }
            _ => {}
        }
        (Type::unknown(), Type::unknown())
    }

    fn twig_traversable_object(&self, value: &Type) -> bool {
        let name = value.name().trim_matches('\\');
        if name.is_empty() {
            return false;
        }
        ["Traversable", "Iterator", "IteratorAggregate"]
            .iter()
            .any(|target| self.snapshot.is_subtype_of(name, target))
    }
}

fn twig_for_body_contains(loop_node: &SyntaxNode, node: &SyntaxNode) -> bool {
    loop_node
        .children()
        .find(|child| child.kind() == BODY)
        .is_some_and(|body| is_descendant_node(node, &body))
}

fn twig_for_binding(start: &SyntaxNode) -> (Vec<String>, Option<SyntaxNode>) {
    let Some(in_offset) = first_token_offset(start, TK_IN) else {
        return (Vec::new(), None);
    };
    let mut names = Vec::new();
    let mut collection = None;
    for child in start.children() {
        let child_start = u32::from(child.text_range().start());
        match child.kind() {
            TWIG_LITERAL_NAME if child_start < in_offset => {
                let (name, _) = literal_name(&child);
                if !name.is_empty() {
                    names.push(name);
                }
            }
            TWIG_EXPRESSION if child_start >= in_offset && collection.is_none() => {
                collection = Some(child)
            }
            _ => {}
        }
    }
    (names, collection)
}

fn definite_twig_object_receiver(snapshot: &Snapshot, receiver: &Type) -> bool {
    if receiver.is_unknown() {
        return false;
    }
    let mut found_object = false;
    fn inspect(snapshot: &Snapshot, value: &Type, found_object: &mut bool) -> bool {
        match value.kind() {
            TypeKind::Null => true,
            TypeKind::Union | TypeKind::Intersection => value
                .arguments()
                .iter()
                .all(|argument| inspect(snapshot, argument, found_object)),
            TypeKind::Object => {
                let name = value.name().trim_matches('\\');
                if name.is_empty()
                    || snapshot.classes(name).is_empty()
                    || eq_ignore_case(name, "stdClass")
                    || snapshot.is_subtype_of(name, "ArrayAccess")
                    || snapshot.is_subtype_of(name, "Traversable")
                    || snapshot.is_subtype_of(name, "Iterator")
                    || snapshot.is_subtype_of(name, "IteratorAggregate")
                    || twig_class_has_magic_access(snapshot, name)
                {
                    return false;
                }
                *found_object = true;
                true
            }
            _ => false,
        }
    }
    inspect(snapshot, receiver, &mut found_object) && found_object
}

fn twig_class_has_magic_access(snapshot: &Snapshot, class_name: &str) -> bool {
    let members = MemberResolver::new(snapshot);
    let receiver = Type::named(class_name, Vec::new());
    ["__get", "__call"]
        .iter()
        .any(|name| !members.methods(&receiver, name).is_empty())
}

pub fn resolve_php_members(
    snapshot: &Snapshot,
    receiver: &Type,
    name: &str,
) -> Vec<ResolvedMember> {
    if receiver.is_unknown() || name.is_empty() {
        return Vec::new();
    }
    MemberResolver::new(snapshot)
        .all(receiver)
        .into_iter()
        .filter(|member| {
            let symbol = &member.symbol;
            if symbol.visibility != Visibility::Public || symbol.flags.has(Flags::STATIC) {
                return false;
            }
            match symbol.kind {
                SymbolKind::Property => {
                    eq_ignore_case(symbol.name.strip_prefix('$').unwrap_or(&symbol.name), name)
                }
                SymbolKind::ClassConstant | SymbolKind::EnumCase => {
                    eq_ignore_case(&symbol.name, name)
                }
                SymbolKind::Method => {
                    eq_ignore_case(&symbol.name, name)
                        || eq_ignore_case(&twig_attribute_name(&symbol.name), name)
                }
                _ => false,
            }
        })
        .collect()
}

pub fn resolve_php_member_type(snapshot: &Snapshot, receiver: &Type, name: &str) -> Type {
    let values = resolve_php_members(snapshot, receiver, name)
        .into_iter()
        .map(|member| {
            resolve_relative_twig_member_type(snapshot, receiver, &member.symbol, &member.ty)
        })
        .filter(|value| !value.is_unknown())
        .collect();
    union_known_types(values)
}

fn resolve_relative_twig_member_type(
    snapshot: &Snapshot,
    receiver: &Type,
    symbol: &Symbol,
    value: &Type,
) -> Type {
    match value.kind() {
        TypeKind::SelfType | TypeKind::Static => receiver.clone(),
        TypeKind::Parent => {
            let Some(container) = snapshot.symbol(&symbol.container) else {
                return Type::unknown();
            };
            match container.extends().first() {
                Some(parent) => Type::named(parent, Vec::new()),
                None => Type::unknown(),
            }
        }
        TypeKind::Union => union_known_types(
            value
                .arguments()
                .iter()
                .map(|argument| {
                    resolve_relative_twig_member_type(snapshot, receiver, symbol, argument)
                })
                .collect(),
        ),
        TypeKind::Intersection => Type::intersection(
            value
                .arguments()
                .iter()
                .map(|argument| {
                    resolve_relative_twig_member_type(snapshot, receiver, symbol, argument)
                })
                .collect::<Vec<_>>(),
        ),
        _ => value.clone(),
    }
}

/// The Twig attribute name of a PHP getter/isser/hasser (`getName` →
/// `name`).
pub fn twig_attribute_name(method: &str) -> String {
    for prefix in ["get", "is", "has"] {
        let bytes = method.as_bytes();
        if bytes.len() <= prefix.len()
            || !bytes[..prefix.len()].eq_ignore_ascii_case(prefix.as_bytes())
        {
            continue;
        }
        // Only an ASCII upper-case letter after the prefix starts a valid
        // attribute name.
        let next = bytes[prefix.len()];
        if !next.is_ascii_uppercase() {
            continue;
        }
        let suffix = &method[prefix.len()..];
        return format!("{}{}", suffix[..1].to_ascii_lowercase(), &suffix[1..]);
    }
    String::new()
}

/// Type and documentation metadata declared in a Twig template through a
/// types tag or the portable `@var` compatibility syntax.
#[derive(Clone, Debug, PartialEq)]
pub struct TwigTypeDeclaration {
    pub ty: Type,
    pub optional: bool,
    pub documentation: String,
    pub from_types_tag: bool,
}

/// Twig's native types tag together with the portable
/// `{# @var variable \Fully\Qualified\Type #}` form.
pub fn twig_type_declarations(root: &SyntaxNode) -> HashMap<String, TwigTypeDeclaration> {
    let mut result = HashMap::new();
    for comment in twigquery::iterate_nodes(root, &[TWIG_COMMENT]) {
        let text = comment.text_string();
        let mut source = text.as_str();
        while let Some(index) = source.find("@var") {
            source = &source[index + "@var".len()..];
            let line = match source.find(['\r', '\n', '#']) {
                Some(end) => &source[..end],
                None => source,
            };
            let fields: Vec<&str> = line.split_whitespace().collect();
            if fields.len() >= 2 {
                let (mut name, mut type_name) = (fields[0], fields[1]);
                if fields[1].starts_with('$')
                    || twig_var_annotation_type_first(fields[0], fields[1])
                {
                    (name, type_name) = (fields[1], fields[0]);
                }
                let name = name.strip_prefix('$').unwrap_or(name);
                let type_name = type_name.strip_prefix('\\').unwrap_or(type_name);
                if let Some(value) = parse_twig_annotation_type(type_name)
                    && !value.is_unknown()
                    && !name.is_empty()
                {
                    result.insert(
                        name.to_owned(),
                        TwigTypeDeclaration {
                            ty: value,
                            optional: false,
                            documentation: String::new(),
                            from_types_tag: false,
                        },
                    );
                }
            }
            match source.find(['\r', '\n']) {
                Some(end) => source = &source[end + 1..],
                None => break,
            }
        }
    }
    for declaration in types_tag_declarations(root.text_string().as_bytes()) {
        if declaration.name.is_empty() || declaration.ty.is_empty() {
            continue;
        }
        if let Some(value) = parse_twig_annotation_type(&declaration.ty)
            && !value.is_unknown()
        {
            result.insert(
                declaration.name,
                TwigTypeDeclaration {
                    ty: value,
                    optional: declaration.optional,
                    documentation: declaration.documentation,
                    from_types_tag: true,
                },
            );
        }
    }
    result
}

/// The type-only view used by inference consumers.
pub fn twig_type_annotations(root: &SyntaxNode) -> HashMap<String, Type> {
    twig_type_declarations(root)
        .into_iter()
        .map(|(name, declaration)| (name, declaration.ty))
        .collect()
}

/// Twig tooling treats the postfix `T[]` annotation spelling as a list,
/// while PHPDoc models it as an array with an array-key.
fn parse_twig_annotation_type(source: &str) -> Option<Type> {
    let mut source = source.trim();
    let mut list_depth = 0;
    while let Some(stripped) = source.strip_suffix("[]") {
        list_depth += 1;
        source = stripped.trim();
    }
    let mut value = swlsp_php_types::parse(source).ok()?;
    for _ in 0..list_depth {
        value = Type::list(value);
    }
    Some(NameContext::new("").resolve_type(&value))
}

pub(crate) fn twig_var_annotation_type_first(first: &str, second: &str) -> bool {
    if first.starts_with('\\')
        || first.contains('\\')
        || first.contains(['|', '&', '?', '[', ']', '<', '>'])
    {
        return true;
    }
    crate::php_usage::is_non_class_twig_type(first)
        && !crate::php_usage::is_non_class_twig_type(second)
}

pub(crate) fn accessor_member_name_node(accessor: &SyntaxNode) -> Option<SyntaxNode> {
    if accessor.kind() != TWIG_ACCESSOR {
        return None;
    }
    let operands = direct_twig_children(accessor, TWIG_OPERAND);
    direct_twig_children(operands.get(1)?, TWIG_LITERAL_NAME)
        .into_iter()
        .next()
}

pub(crate) fn direct_twig_children(node: &SyntaxNode, kind: Kind) -> Vec<SyntaxNode> {
    node.children()
        .filter(|child| child.kind() == kind)
        .collect()
}

pub(crate) fn first_twig_child(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.children().next()
}

pub(crate) fn union_known_types(values: Vec<Type>) -> Type {
    let mut seen = HashSet::with_capacity(values.len());
    let mut result = Vec::with_capacity(values.len());
    for value in values {
        if value.is_unknown() || value.kind() == TypeKind::Error {
            continue;
        }
        if !seen.insert(value.key().into_owned()) {
            continue;
        }
        result.push(value);
    }
    match result.len() {
        0 => Type::unknown(),
        1 => result.pop().expect("one value"),
        _ => Type::union(result),
    }
}

fn callback_receiver_class(callback: &str) -> String {
    let Some(index) = callback.rfind("::") else {
        return String::new();
    };
    let receiver = &callback[..index];
    let receiver = receiver.strip_prefix('\\').unwrap_or(receiver);
    let receiver = receiver.strip_suffix("::class").unwrap_or(receiver);
    match swlsp_textutil::lowercase(receiver).as_str() {
        "" | "self" | "static" | "parent" | "self::class" | "static::class" => String::new(),
        _ => receiver.to_owned(),
    }
}

fn is_semantic_twig_extension_class(snapshot: &Snapshot, class: &Symbol) -> bool {
    if class
        .extends()
        .iter()
        .chain(class.implements())
        .any(|parent| {
            matches!(
                last_name_part(parent),
                "AbstractExtension"
                    | "ExtensionInterface"
                    | "Twig_Extension"
                    | "Twig_ExtensionInterface"
            )
        })
    {
        return true;
    }
    [
        "Twig\\Extension\\AbstractExtension",
        "Twig\\Extension\\ExtensionInterface",
        "Twig_Extension",
        "Twig_ExtensionInterface",
    ]
    .iter()
    .any(|target| snapshot.is_subtype_of(&class.fully_qualified, target))
}
