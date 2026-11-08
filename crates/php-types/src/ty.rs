//! Representation, constructors, and accessors of [`Type`].

use std::borrow::Cow;
use std::collections::HashSet;
use std::fmt;
use std::hash::{Hash, Hasher};
use std::ptr::NonNull;
use std::sync::Arc;

use smallvec::SmallVec;

use crate::kind::Kind;
use crate::render;

/// Shared immutable string used for names stored inside types.
pub type Name = Arc<str>;

/// An immutable semantic PHP type. [`Default`] is `unknown`.
///
/// A `Type` is one machine word. Payload-free builtins (and the canonical
/// defaults such as `array<mixed,mixed>` or `class-string<object>`) are
/// encoded inline as tagged words and never allocate or touch a reference
/// count. Every other type points at an `Arc`-owned immutable node, so
/// cloning is a single atomic increment.
pub struct Type {
    ptr: NonNull<Node>,
}

// SAFETY: a `Type` is either an inline tag or an `Arc<Node>`, and `Node` only
// holds immutable `Send + Sync` data.
unsafe impl Send for Type {}
// SAFETY: see above.
unsafe impl Sync for Type {}

pub(crate) struct Node {
    pub(crate) kind: Kind,
    /// Semantic name for named/literal kinds, cached canonical text for
    /// composite kinds (`None` means empty / not cached).
    pub(crate) slot: Option<Name>,
    pub(crate) args: Box<[Type]>,
    pub(crate) details: Option<Box<Details>>,
}

/// Fields used only by callables and shapes.
pub(crate) struct Details {
    pub(crate) params: Box<[CallableParameter]>,
    pub(crate) fields: Box<[ShapeField]>,
    pub(crate) result: Type,
    pub(crate) open: bool,
}

/// One parameter in a callable signature.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct CallableParameter {
    pub name: Name,
    pub ty: Type,
    pub optional: bool,
    pub variadic: bool,
    pub by_reference: bool,
}

/// One array/object shape entry.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct ShapeField {
    pub name: Name,
    pub ty: Type,
    pub optional: bool,
}

impl CallableParameter {
    pub fn new(name: &str, ty: Type) -> Self {
        CallableParameter {
            name: Name::from(name),
            ty,
            ..Default::default()
        }
    }

    /// An unnamed parameter of type `ty`.
    pub fn of(ty: Type) -> Self {
        CallableParameter {
            ty,
            ..Default::default()
        }
    }
}

impl ShapeField {
    pub fn new(name: &str, ty: Type) -> Self {
        ShapeField {
            name: Name::from(name),
            ty,
            optional: false,
        }
    }

    pub fn optional(name: &str, ty: Type) -> Self {
        ShapeField {
            name: Name::from(name),
            ty,
            optional: true,
        }
    }
}

const LITERAL_ZERO_CODE: u8 = 0x40;
const LITERAL_ONE_CODE: u8 = 0x41;

#[derive(Clone, Copy)]
pub(crate) enum Repr<'a> {
    Inline(u8),
    Node(&'a Node),
}

static MIXED_PAIR: [Type; 2] = [Type::MIXED, Type::MIXED];
static MIXED_ONE: [Type; 1] = [Type::MIXED];
static OBJECT_ONE: [Type; 1] = [Type::OBJECT];
static MIXED_STATIC: Type = Type::MIXED;
static UNKNOWN_STATIC: Type = Type::UNKNOWN;

impl Type {
    const fn inline(code: u8) -> Type {
        // SAFETY: the address is odd and therefore non-null. It is never
        // dereferenced because `repr` checks the tag bit first.
        Type {
            ptr: unsafe {
                NonNull::new_unchecked(std::ptr::without_provenance_mut(((code as usize) << 1) | 1))
            },
        }
    }

    pub const UNKNOWN: Type = Type::inline(Kind::Unknown as u8);
    pub const ERROR: Type = Type::inline(Kind::Error as u8);
    pub const NEVER: Type = Type::inline(Kind::Never as u8);
    pub const MIXED: Type = Type::inline(Kind::Mixed as u8);
    pub const VOID: Type = Type::inline(Kind::Void as u8);
    pub const NULL: Type = Type::inline(Kind::Null as u8);
    pub const BOOL: Type = Type::inline(Kind::Bool as u8);
    pub const TRUE: Type = Type::inline(Kind::True as u8);
    pub const FALSE: Type = Type::inline(Kind::False as u8);
    pub const INT: Type = Type::inline(Kind::Int as u8);
    pub const FLOAT: Type = Type::inline(Kind::Float as u8);
    pub const STRING: Type = Type::inline(Kind::String as u8);
    /// The bare `object` type.
    pub const OBJECT: Type = Type::inline(Kind::Object as u8);
    pub const RESOURCE: Type = Type::inline(Kind::Resource as u8);
    pub const ARRAY_KEY: Type = Type::inline(Kind::ArrayKey as u8);
    /// `array<mixed,mixed>`.
    pub const ARRAY: Type = Type::inline(Kind::Array as u8);
    pub const NON_EMPTY_ARRAY: Type = Type::inline(Kind::NonEmptyArray as u8);
    /// `list<mixed>`.
    pub const LIST: Type = Type::inline(Kind::List as u8);
    pub const NON_EMPTY_LIST: Type = Type::inline(Kind::NonEmptyList as u8);
    pub const ITERABLE: Type = Type::inline(Kind::Iterable as u8);
    /// `callable` without parameters returning mixed.
    pub const CALLABLE: Type = Type::inline(Kind::Callable as u8);
    /// `class-string<object>`.
    pub const CLASS_STRING: Type = Type::inline(Kind::ClassString as u8);
    pub const SELF: Type = Type::inline(Kind::SelfType as u8);
    pub const STATIC: Type = Type::inline(Kind::Static as u8);
    pub const PARENT: Type = Type::inline(Kind::Parent as u8);
    const EMPTY_STRING: Type = Type::inline(Kind::LiteralString as u8);
    const ZERO: Type = Type::inline(LITERAL_ZERO_CODE);
    const ONE: Type = Type::inline(LITERAL_ONE_CODE);

    #[inline]
    pub(crate) fn repr(&self) -> Repr<'_> {
        let bits = self.ptr.as_ptr().addr();
        if bits & 1 == 1 {
            Repr::Inline((bits >> 1) as u8)
        } else {
            // SAFETY: untagged pointers come from `Arc::into_raw` and the
            // reference count is kept alive by `self`.
            Repr::Node(unsafe { self.ptr.as_ref() })
        }
    }

    pub(crate) fn node(&self) -> Option<&Node> {
        match self.repr() {
            Repr::Node(node) => Some(node),
            Repr::Inline(_) => None,
        }
    }

    pub(crate) fn from_node(node: Node) -> Type {
        Type::from_arc(Arc::new(node))
    }

    fn from_arc(node: Arc<Node>) -> Type {
        let raw = Arc::into_raw(node) as *mut Node;
        Type {
            // SAFETY: `Arc::into_raw` never returns null.
            ptr: unsafe { NonNull::new_unchecked(raw) },
        }
    }

    /// Reports whether both values share one representation (identical
    /// inline tag or the same node).
    pub fn ptr_eq(&self, other: &Type) -> bool {
        self.ptr == other.ptr
    }

    pub(crate) fn node_address(&self) -> Option<usize> {
        match self.repr() {
            Repr::Node(_) => Some(self.ptr.as_ptr().addr()),
            Repr::Inline(_) => None,
        }
    }

    // ----- builtin constructors (mirroring Go's functions) -----

    pub fn unknown() -> Type {
        Type::UNKNOWN
    }
    pub fn error() -> Type {
        Type::ERROR
    }
    pub fn never() -> Type {
        Type::NEVER
    }
    pub fn mixed() -> Type {
        Type::MIXED
    }
    pub fn void() -> Type {
        Type::VOID
    }
    pub fn null() -> Type {
        Type::NULL
    }
    pub fn bool() -> Type {
        Type::BOOL
    }
    pub fn true_type() -> Type {
        Type::TRUE
    }
    pub fn false_type() -> Type {
        Type::FALSE
    }
    pub fn int() -> Type {
        Type::INT
    }
    pub fn float() -> Type {
        Type::FLOAT
    }
    pub fn string() -> Type {
        Type::STRING
    }
    pub fn object() -> Type {
        Type::OBJECT
    }
    pub fn resource() -> Type {
        Type::RESOURCE
    }
    pub fn array_key() -> Type {
        Type::ARRAY_KEY
    }

    fn generic_special(kind: Kind, inline: Type, arguments: Vec<Type>) -> Type {
        if arguments.is_empty() {
            return inline;
        }
        new_type(kind, None, arguments)
    }

    /// `self`, optionally with generic arguments.
    pub fn self_type(arguments: Vec<Type>) -> Type {
        Type::generic_special(Kind::SelfType, Type::SELF, arguments)
    }

    /// `static`, optionally with generic arguments.
    pub fn static_type(arguments: Vec<Type>) -> Type {
        Type::generic_special(Kind::Static, Type::STATIC, arguments)
    }

    /// `parent`, optionally with generic arguments.
    pub fn parent_type(arguments: Vec<Type>) -> Type {
        Type::generic_special(Kind::Parent, Type::PARENT, arguments)
    }

    /// A named class-like type. Invalid names collapse to `unknown`.
    pub fn named(name: &str, arguments: Vec<Type>) -> Type {
        let name = name.trim();
        if !valid_qualified_name(name.strip_prefix('\\').unwrap_or(name)) {
            return Type::UNKNOWN;
        }
        new_type(Kind::Object, Some(Name::from(name)), arguments)
    }

    /// A template variable. Invalid names collapse to `unknown`.
    pub fn template(name: &str) -> Type {
        let name = name.trim();
        let identifier = name.strip_prefix('$').unwrap_or(name);
        if !valid_identifier(identifier) {
            return Type::UNKNOWN;
        }
        new_type(Kind::Template, Some(Name::from(name)), Vec::new())
    }

    fn container2(kind: Kind, canonical: Type, key: Type, value: Type) -> Type {
        let key = if key.is_unknown() { Type::MIXED } else { key };
        let value = if value.is_unknown() {
            Type::MIXED
        } else {
            value
        };
        if key.kind() == Kind::Mixed && value.kind() == Kind::Mixed {
            return canonical;
        }
        new_type(kind, None, vec![key, value])
    }

    fn container1(kind: Kind, canonical: Type, value: Type) -> Type {
        let value = if value.is_unknown() {
            Type::MIXED
        } else {
            value
        };
        if value.kind() == Kind::Mixed {
            return canonical;
        }
        new_type(kind, None, vec![value])
    }

    pub fn array(key: Type, value: Type) -> Type {
        Type::container2(Kind::Array, Type::ARRAY, key, value)
    }

    pub fn non_empty_array(key: Type, value: Type) -> Type {
        Type::container2(Kind::NonEmptyArray, Type::NON_EMPTY_ARRAY, key, value)
    }

    pub fn list(value: Type) -> Type {
        Type::container1(Kind::List, Type::LIST, value)
    }

    pub fn non_empty_list(value: Type) -> Type {
        Type::container1(Kind::NonEmptyList, Type::NON_EMPTY_LIST, value)
    }

    pub fn iterable(key: Type, value: Type) -> Type {
        Type::container2(Kind::Iterable, Type::ITERABLE, key, value)
    }

    pub fn callable(parameters: Vec<CallableParameter>, result: Type) -> Type {
        let result = if result.is_unknown() {
            Type::MIXED
        } else {
            result
        };
        if parameters.is_empty() && result.kind() == Kind::Mixed {
            return Type::CALLABLE;
        }
        finish(Node {
            kind: Kind::Callable,
            slot: None,
            args: Box::default(),
            details: Some(Box::new(Details {
                params: parameters.into_boxed_slice(),
                fields: Box::default(),
                result,
                open: false,
            })),
        })
    }

    /// PHPDoc's `(subject is target ? ifTrue : ifFalse)` conditional type.
    pub fn conditional(subject: Type, target: Type, if_true: Type, if_false: Type) -> Type {
        new_type(
            Kind::Conditional,
            None,
            vec![subject, target, if_true, if_false],
        )
    }

    pub fn class_string(of: Type) -> Type {
        if of.is_unknown() || of == Type::OBJECT {
            return Type::CLASS_STRING;
        }
        new_type(Kind::ClassString, None, vec![of])
    }

    /// An integer literal; invalid literals collapse to `int`.
    pub fn literal_int(value: &str) -> Type {
        let value = value.trim();
        if !valid_integer_literal(value) {
            return Type::INT;
        }
        match value {
            "0" => Type::ZERO,
            "1" => Type::ONE,
            _ => new_type(Kind::LiteralInt, Some(Name::from(value)), Vec::new()),
        }
    }

    /// A float literal; invalid literals collapse to `float`.
    pub fn literal_float(value: &str) -> Type {
        let value = value.trim();
        if !valid_float_literal(&value.replace('_', "")) {
            return Type::FLOAT;
        }
        new_type(Kind::LiteralFloat, Some(Name::from(value)), Vec::new())
    }

    pub fn literal_string(value: &str) -> Type {
        if value.is_empty() {
            return Type::EMPTY_STRING;
        }
        new_type(Kind::LiteralString, Some(Name::from(value)), Vec::new())
    }

    pub fn array_shape(fields: Vec<ShapeField>, open: bool) -> Type {
        shape(Kind::ArrayShape, fields, open)
    }

    pub fn object_shape(fields: Vec<ShapeField>, open: bool) -> Type {
        shape(Kind::ObjectShape, fields, open)
    }

    /// Canonicalizes and deduplicates a union. Nested unions are flattened.
    pub fn union<I: IntoIterator<Item = Type>>(values: I) -> Type {
        union_impl(values, true)
    }

    /// Canonicalizes and deduplicates an intersection.
    pub fn intersection<I: IntoIterator<Item = Type>>(values: I) -> Type {
        let mut flat: SmallVec<[Type; 8]> = SmallVec::new();
        let mut seen: Option<HashSet<String>> = None;
        for value in values {
            match value.kind() {
                Kind::Unknown => return Type::UNKNOWN,
                Kind::Never => return Type::NEVER,
                Kind::Mixed => continue,
                Kind::Intersection => {
                    for member in value.arguments() {
                        append_unique(&mut flat, &mut seen, member.clone());
                    }
                }
                _ => append_unique(&mut flat, &mut seen, value),
            }
        }
        match flat.len() {
            0 => Type::MIXED,
            1 => flat.pop().unwrap_or_default(),
            _ => {
                flat.sort_by(canonical_compare);
                new_type(Kind::Intersection, None, flat.into_vec())
            }
        }
    }

    /// `value|null`.
    pub fn nullable(value: Type) -> Type {
        Type::union([value, Type::NULL])
    }

    // ----- accessors -----

    #[inline]
    pub fn kind(&self) -> Kind {
        match self.repr() {
            Repr::Inline(LITERAL_ZERO_CODE | LITERAL_ONE_CODE) => Kind::LiteralInt,
            Repr::Inline(code) => Kind::from_u8(code),
            Repr::Node(node) => node.kind,
        }
    }

    /// The semantic name of named, template, and literal types; empty for
    /// every other kind.
    pub fn name(&self) -> &str {
        match self.repr() {
            Repr::Inline(LITERAL_ZERO_CODE) => "0",
            Repr::Inline(LITERAL_ONE_CODE) => "1",
            Repr::Inline(_) => "",
            Repr::Node(node) if node.kind.has_semantic_name() => node.slot.as_deref().unwrap_or(""),
            Repr::Node(_) => "",
        }
    }

    /// Generic or composite type arguments.
    pub fn arguments(&self) -> &[Type] {
        match self.repr() {
            Repr::Inline(code) if code >= LITERAL_ZERO_CODE => &[],
            Repr::Inline(code) => match Kind::from_u8(code) {
                Kind::Array | Kind::NonEmptyArray | Kind::Iterable => &MIXED_PAIR,
                Kind::List | Kind::NonEmptyList => &MIXED_ONE,
                Kind::ClassString => &OBJECT_ONE,
                _ => &[],
            },
            Repr::Node(node) => &node.args,
        }
    }

    pub fn argument_count(&self) -> usize {
        self.arguments().len()
    }

    /// One argument; out-of-range indexes return `unknown`.
    pub fn argument(&self, index: usize) -> Type {
        self.arguments().get(index).cloned().unwrap_or_default()
    }

    fn details(&self) -> Option<&Details> {
        self.node().and_then(|node| node.details.as_deref())
    }

    /// Callable parameters.
    pub fn parameters(&self) -> &[CallableParameter] {
        self.details().map_or(&[], |details| &details.params)
    }

    pub fn parameter_count(&self) -> usize {
        self.parameters().len()
    }

    pub fn parameter(&self, index: usize) -> Option<&CallableParameter> {
        self.parameters().get(index)
    }

    /// Callable result; `unknown` for non-callables.
    pub fn result(&self) -> &Type {
        if let Some(details) = self.details() {
            return &details.result;
        }
        if self.kind() == Kind::Callable {
            return &MIXED_STATIC;
        }
        &UNKNOWN_STATIC
    }

    /// Shape fields, sorted by name.
    pub fn fields(&self) -> &[ShapeField] {
        self.details().map_or(&[], |details| &details.fields)
    }

    pub fn field_count(&self) -> usize {
        self.fields().len()
    }

    pub fn field(&self, index: usize) -> Option<&ShapeField> {
        self.fields().get(index)
    }

    pub fn is_open_shape(&self) -> bool {
        self.details().is_some_and(|details| details.open)
    }

    #[inline]
    pub fn is_unknown(&self) -> bool {
        self.kind() == Kind::Unknown
    }

    /// Cached canonical text of composite nodes.
    pub(crate) fn cached_text(&self) -> Option<&str> {
        match self.repr() {
            Repr::Node(node) if !node.kind.has_semantic_name() => node.slot.as_deref(),
            _ => None,
        }
    }

    /// The stable canonical serialization used for equality, persistence,
    /// interning, and deterministic tests. Same as [`Type::text`].
    pub fn key(&self) -> Cow<'_, str> {
        self.text()
    }

    /// The canonical display text (Go's `String()`).
    pub fn text(&self) -> Cow<'_, str> {
        if let Some(text) = self.cached_text() {
            return Cow::Borrowed(text);
        }
        match self.kind() {
            Kind::Unknown
            | Kind::Error
            | Kind::Never
            | Kind::Mixed
            | Kind::Void
            | Kind::Null
            | Kind::Bool
            | Kind::True
            | Kind::False
            | Kind::Int
            | Kind::Float
            | Kind::String
            | Kind::Resource
            | Kind::ArrayKey => Cow::Borrowed(self.kind().as_str()),
            Kind::LiteralInt | Kind::LiteralFloat | Kind::Template => Cow::Borrowed(self.name()),
            Kind::Object if self.arguments().is_empty() => {
                if self.name().is_empty() {
                    Cow::Borrowed("object")
                } else {
                    Cow::Borrowed(self.name())
                }
            }
            _ => Cow::Owned(render::render(self)),
        }
    }
}

impl Default for Type {
    fn default() -> Self {
        Type::UNKNOWN
    }
}

impl Clone for Type {
    #[inline]
    fn clone(&self) -> Self {
        if let Repr::Node(_) = self.repr() {
            // SAFETY: the pointer came from `Arc::into_raw` and is alive.
            unsafe { Arc::increment_strong_count(self.ptr.as_ptr() as *const Node) };
        }
        Type { ptr: self.ptr }
    }
}

impl Drop for Type {
    #[inline]
    fn drop(&mut self) {
        if let Repr::Node(_) = self.repr() {
            // SAFETY: balanced with `from_node`/`clone`.
            unsafe { Arc::decrement_strong_count(self.ptr.as_ptr() as *const Node) };
        }
    }
}

impl PartialEq for Type {
    fn eq(&self, other: &Type) -> bool {
        equal_type(self, other)
    }
}

impl Eq for Type {}

fn equal_type(left: &Type, right: &Type) -> bool {
    if left.ptr == right.ptr {
        return true;
    }
    if left.kind() != right.kind() || left.name() != right.name() {
        return false;
    }
    let left_args = left.arguments();
    let right_args = right.arguments();
    if left_args.len() != right_args.len() {
        return false;
    }
    if !left_args
        .iter()
        .zip(right_args)
        .all(|(left, right)| equal_type(left, right))
    {
        return false;
    }
    left.is_open_shape() == right.is_open_shape()
        && left.parameters() == right.parameters()
        && left.fields() == right.fields()
        && left.result() == right.result()
}

impl Hash for Type {
    fn hash<H: Hasher>(&self, state: &mut H) {
        (self.kind() as u8).hash(state);
        self.name().hash(state);
        self.arguments().hash(state);
        if self.details().is_some() || self.kind() == Kind::Callable {
            self.is_open_shape().hash(state);
            self.parameters().hash(state);
            self.fields().hash(state);
            self.result().hash(state);
        }
    }
}

impl fmt::Display for Type {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.text())
    }
}

impl fmt::Debug for Type {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "Type({:?}, {:?})", self.kind(), self.text())
    }
}

// ----- construction helpers -----

pub(crate) fn new_type(kind: Kind, name: Option<Name>, arguments: Vec<Type>) -> Type {
    finish(Node {
        kind,
        slot: name,
        args: arguments.into_boxed_slice(),
        details: None,
    })
}

fn cache_text(node: &Node) -> bool {
    if node.kind.has_semantic_name() {
        return false;
    }
    if !node.args.is_empty()
        || node
            .details
            .as_ref()
            .is_some_and(|details| !details.params.is_empty() || !details.fields.is_empty())
    {
        return true;
    }
    matches!(
        node.kind,
        Kind::Union | Kind::Intersection | Kind::Conditional
    )
}

pub(crate) fn finish(node: Node) -> Type {
    if !cache_text(&node) {
        return Type::from_node(node);
    }
    let mut shared = Arc::new(node);
    let text = render::render(&Type::from_arc(Arc::clone(&shared)));
    match Arc::get_mut(&mut shared) {
        Some(node) => node.slot = Some(Name::from(text)),
        None => unreachable!("temporary render view was dropped"),
    }
    Type::from_arc(shared)
}

fn shape(kind: Kind, mut fields: Vec<ShapeField>, open: bool) -> Type {
    for field in &mut fields {
        if let Cow::Owned(name) = render::shape_field_name(&field.name) {
            field.name = Name::from(name);
        }
    }
    fields.sort_by(|left, right| left.name.as_bytes().cmp(right.name.as_bytes()));
    finish(Node {
        kind,
        slot: None,
        args: Box::default(),
        details: Some(Box::new(Details {
            params: Box::default(),
            fields: fields.into_boxed_slice(),
            result: Type::UNKNOWN,
            open,
        })),
    })
}

pub(crate) fn union_impl<I: IntoIterator<Item = Type>>(values: I, cache: bool) -> Type {
    let mut flat: SmallVec<[Type; 8]> = SmallVec::new();
    let mut seen: Option<HashSet<String>> = None;
    let mut has_bool = false;
    for value in values {
        match value.kind() {
            Kind::Unknown => return Type::UNKNOWN,
            Kind::Mixed => return Type::MIXED,
            Kind::Never => continue,
            Kind::Union => {
                for member in value.arguments() {
                    if member.kind() == Kind::Bool {
                        has_bool = true;
                    }
                    append_unique(&mut flat, &mut seen, member.clone());
                }
            }
            kind => {
                if kind == Kind::Bool {
                    has_bool = true;
                }
                append_unique(&mut flat, &mut seen, value);
            }
        }
    }
    if has_bool {
        flat.retain(|value| !matches!(value.kind(), Kind::True | Kind::False));
    }
    match flat.len() {
        0 => Type::NEVER,
        1 => flat.pop().unwrap_or_default(),
        _ => {
            flat.sort_by(canonical_compare);
            let node = Node {
                kind: Kind::Union,
                slot: None,
                args: flat.into_vec().into_boxed_slice(),
                details: None,
            };
            if cache {
                finish(node)
            } else {
                Type::from_node(node)
            }
        }
    }
}

fn append_unique(
    values: &mut SmallVec<[Type; 8]>,
    seen: &mut Option<HashSet<String>>,
    value: Type,
) {
    const MAX_LINEAR_DEDUPLICATION: usize = 16;
    if seen.is_none() && values.len() < MAX_LINEAR_DEDUPLICATION {
        if values.contains(&value) {
            return;
        }
        values.push(value);
        return;
    }
    let set = seen.get_or_insert_with(|| {
        values
            .iter()
            .map(|existing| existing.key().into_owned())
            .collect()
    });
    if set.insert(value.key().into_owned()) {
        values.push(value);
    }
}

pub(crate) fn canonical_compare(left: &Type, right: &Type) -> std::cmp::Ordering {
    use std::cmp::Ordering;
    if left.kind() == Kind::LiteralString {
        if right.kind() == Kind::LiteralString {
            return left.name().as_bytes().cmp(right.name().as_bytes());
        }
        // Quoted literal strings sort before every valid PHP type name and
        // numeric literal in the canonical serialization.
        return Ordering::Less;
    }
    if right.kind() == Kind::LiteralString {
        return Ordering::Greater;
    }
    left.key().as_bytes().cmp(right.key().as_bytes())
}

// ----- validation -----

pub(crate) fn valid_qualified_name(name: &str) -> bool {
    !name.is_empty() && name.split('\\').all(valid_identifier)
}

pub(crate) fn valid_identifier(name: &str) -> bool {
    if name.is_empty() {
        return false;
    }
    for (index, value) in name.chars().enumerate() {
        if !value.is_ascii() || value == '_' {
            continue;
        }
        let valid = if index == 0 {
            value.is_ascii_alphabetic()
        } else {
            value.is_ascii_alphanumeric()
        };
        if !valid {
            return false;
        }
    }
    true
}

pub(crate) fn valid_integer_literal(value: &str) -> bool {
    let bytes = value.as_bytes();
    if bytes.is_empty() {
        return false;
    }
    let mut position = 0;
    if bytes[0] == b'+' || bytes[0] == b'-' {
        position += 1;
        if position == bytes.len() {
            return false;
        }
    }
    let mut base = 10;
    if position + 2 <= bytes.len() && bytes[position] == b'0' {
        match bytes[position + 1] {
            b'x' | b'X' => {
                base = 16;
                position += 2;
            }
            b'b' | b'B' => {
                base = 2;
                position += 2;
            }
            b'o' | b'O' => {
                base = 8;
                position += 2;
            }
            _ => {}
        }
    }
    let mut digits = 0;
    for &current in &bytes[position..] {
        if current == b'_' {
            continue;
        }
        let valid = match base {
            2 => current == b'0' || current == b'1',
            8 => (b'0'..=b'7').contains(&current),
            16 => current.is_ascii_hexdigit(),
            _ => current.is_ascii_digit(),
        };
        if !valid {
            return false;
        }
        digits += 1;
    }
    digits > 0
}

/// Mirrors Go's `strconv.ParseFloat(value, 64) == nil`.
fn valid_float_literal(value: &str) -> bool {
    let (signed, unsigned) = match value.strip_prefix(['+', '-']) {
        Some(rest) => (true, rest),
        None => (false, value),
    };
    if unsigned.starts_with(['+', '-']) {
        return false;
    }
    let lower = unsigned.to_ascii_lowercase();
    if matches!(lower.as_str(), "inf" | "infinity") {
        return true;
    }
    // Go accepts a sign before "inf" but not before "nan".
    if lower == "nan" {
        return !signed;
    }
    if let Some(hex) = lower.strip_prefix("0x") {
        return valid_hex_float(hex);
    }
    // Rust accepts the same decimal grammar but spells special values
    // differently; those were handled above.
    if !unsigned
        .bytes()
        .all(|byte| byte.is_ascii_digit() || matches!(byte, b'.' | b'e' | b'E' | b'+' | b'-'))
    {
        return false;
    }
    match unsigned.parse::<f64>() {
        // Go reports overflow to infinity as a range error.
        Ok(parsed) => parsed.is_finite(),
        Err(_) => false,
    }
}

/// Go's hexadecimal float syntax: a mantissa with at least one hex digit and
/// a mandatory binary exponent. Values overflowing `f64` are range errors.
fn valid_hex_float(value: &str) -> bool {
    let Some((mantissa, exponent)) = value.split_once('p') else {
        return false;
    };
    let (integer, fraction) = mantissa.split_once('.').unwrap_or((mantissa, ""));
    if integer.is_empty() && fraction.is_empty() {
        return false;
    }
    if !integer
        .bytes()
        .chain(fraction.bytes())
        .all(|byte| byte.is_ascii_hexdigit())
    {
        return false;
    }
    let (negative, digits) = match exponent.strip_prefix(['+', '-']) {
        Some(rest) => (exponent.starts_with('-'), rest),
        None => (false, exponent),
    };
    if digits.is_empty() || !digits.bytes().all(|byte| byte.is_ascii_digit()) {
        return false;
    }
    // Binary magnitude of the mantissa: position of its most significant
    // set bit relative to the radix point.
    let significant = integer.trim_start_matches('0');
    let magnitude = if !significant.is_empty() {
        let leading = significant.as_bytes()[0];
        let bits = 8 - (char::from(leading).to_digit(16).unwrap_or(0) as u8).leading_zeros() as i64;
        (significant.len() as i64 - 1) * 4 + bits
    } else {
        let zeros = fraction.bytes().take_while(|&byte| byte == b'0').count();
        let Some(&leading) = fraction.as_bytes().get(zeros) else {
            return true; // zero mantissa never overflows
        };
        let bits = 8 - (char::from(leading).to_digit(16).unwrap_or(0) as u8).leading_zeros() as i64;
        -(zeros as i64) * 4 - 4 + bits
    };
    if negative {
        return true;
    }
    let exponent = digits.trim_start_matches('0').parse::<i64>().unwrap_or(
        if digits.trim_start_matches('0').is_empty() {
            0
        } else {
            i64::MAX / 2
        },
    );
    // The value lies in [2^(magnitude+exponent-1), 2^(magnitude+exponent)).
    // f64 overflows at 2^1024 (ignoring rounding at the very boundary).
    magnitude.saturating_add(exponent) <= 1024
}

/// Reports whether any part of a type is unknown, mixed, or an unresolved
/// template.
pub fn contains_uncertain(value: &Type) -> bool {
    if matches!(value.kind(), Kind::Unknown | Kind::Mixed | Kind::Template) {
        return true;
    }
    if value.kind() == Kind::Callable {
        return value
            .parameters()
            .iter()
            .any(|parameter| contains_uncertain(&parameter.ty))
            || contains_uncertain(value.result());
    }
    value
        .fields()
        .iter()
        .any(|field| contains_uncertain(&field.ty))
        || value.arguments().iter().any(contains_uncertain)
}

/// Reports whether a type contains a template variable.
pub fn contains_template(value: &Type) -> bool {
    if value.kind() == Kind::Template {
        return true;
    }
    if value.kind() == Kind::Callable {
        return value
            .parameters()
            .iter()
            .any(|parameter| contains_template(&parameter.ty))
            || contains_template(value.result());
    }
    value.arguments().iter().any(contains_template)
}
