//! Declarations visible to semantic queries.

use std::borrow::Borrow;
use std::fmt;
use std::ops::Deref;
use std::sync::Arc;

use swlsp_php_types::Type;

use crate::str::Str;
use swlsp_syntax::TextRange;

use crate::atom::empty_name;

/// Stable declaration ID; identical across index generations for the same
/// declaration. Cloning is a reference-count increment.
#[derive(Clone, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct SymbolId(Str);

impl SymbolId {
    pub fn new(value: impl Into<Str>) -> Self {
        SymbolId(value.into())
    }

    pub fn as_str(&self) -> &str {
        &self.0
    }

    pub fn name(&self) -> &Str {
        &self.0
    }

    pub fn is_empty(&self) -> bool {
        self.0.is_empty()
    }
}

impl Default for SymbolId {
    fn default() -> Self {
        SymbolId(empty_name())
    }
}

impl Deref for SymbolId {
    type Target = str;
    fn deref(&self) -> &str {
        &self.0
    }
}

impl Borrow<str> for SymbolId {
    fn borrow(&self) -> &str {
        &self.0
    }
}

impl AsRef<str> for SymbolId {
    fn as_ref(&self) -> &str {
        &self.0
    }
}

impl From<&str> for SymbolId {
    fn from(value: &str) -> Self {
        if value.is_empty() {
            SymbolId::default()
        } else {
            SymbolId(Str::from(value))
        }
    }
}

impl From<String> for SymbolId {
    fn from(value: String) -> Self {
        if value.is_empty() {
            SymbolId::default()
        } else {
            SymbolId(Str::from(value))
        }
    }
}

impl From<Str> for SymbolId {
    fn from(value: Str) -> Self {
        SymbolId(value)
    }
}

impl From<&SymbolId> for SymbolId {
    fn from(value: &SymbolId) -> Self {
        value.clone()
    }
}

impl PartialEq<str> for SymbolId {
    fn eq(&self, other: &str) -> bool {
        &*self.0 == other
    }
}

impl PartialEq<&str> for SymbolId {
    fn eq(&self, other: &&str) -> bool {
        &*self.0 == *other
    }
}

impl fmt::Display for SymbolId {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl fmt::Debug for SymbolId {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{:?}", &*self.0)
    }
}

/// The declaration kind. Values match the Go constants and are part of the
/// symbol ID format.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash)]
#[repr(u8)]
pub enum SymbolKind {
    #[default]
    Namespace = 0,
    Class,
    Interface,
    Trait,
    Enum,
    Method,
    Function,
    Closure,
    Property,
    Parameter,
    ClassConstant,
    GlobalConstant,
    EnumCase,
    Local,
    Template,
    TypeAlias,
}

impl SymbolKind {
    pub const ALL: [SymbolKind; 16] = [
        SymbolKind::Namespace,
        SymbolKind::Class,
        SymbolKind::Interface,
        SymbolKind::Trait,
        SymbolKind::Enum,
        SymbolKind::Method,
        SymbolKind::Function,
        SymbolKind::Closure,
        SymbolKind::Property,
        SymbolKind::Parameter,
        SymbolKind::ClassConstant,
        SymbolKind::GlobalConstant,
        SymbolKind::EnumCase,
        SymbolKind::Local,
        SymbolKind::Template,
        SymbolKind::TypeAlias,
    ];

    pub fn from_u8(value: u8) -> Option<SymbolKind> {
        SymbolKind::ALL.get(usize::from(value)).copied()
    }

    pub fn is_class_like(self) -> bool {
        matches!(
            self,
            SymbolKind::Class | SymbolKind::Interface | SymbolKind::Trait | SymbolKind::Enum
        )
    }

    pub fn is_function_like(self) -> bool {
        matches!(
            self,
            SymbolKind::Method | SymbolKind::Function | SymbolKind::Closure
        )
    }

    /// Kinds indexed per containing class-like declaration.
    pub fn is_member(self) -> bool {
        matches!(
            self,
            SymbolKind::Method
                | SymbolKind::Property
                | SymbolKind::ClassConstant
                | SymbolKind::EnumCase
                | SymbolKind::TypeAlias
        )
    }

    /// Kinds retained by the compact workspace graph.
    pub fn is_workspace(self) -> bool {
        matches!(
            self,
            SymbolKind::Class
                | SymbolKind::Interface
                | SymbolKind::Trait
                | SymbolKind::Enum
                | SymbolKind::Method
                | SymbolKind::Function
                | SymbolKind::Property
                | SymbolKind::ClassConstant
                | SymbolKind::GlobalConstant
                | SymbolKind::EnumCase
                | SymbolKind::TypeAlias
        )
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash)]
#[repr(u8)]
pub enum Visibility {
    #[default]
    Public = 0,
    Protected,
    Private,
}

impl Visibility {
    pub fn from_u8(value: u8) -> Option<Visibility> {
        match value {
            0 => Some(Visibility::Public),
            1 => Some(Visibility::Protected),
            2 => Some(Visibility::Private),
            _ => None,
        }
    }
}

/// Declaration modifier bits; values match Go.
#[derive(Clone, Copy, Default, PartialEq, Eq, Hash)]
pub struct Flags(pub u32);

impl Flags {
    pub const STATIC: Flags = Flags(1 << 0);
    pub const FINAL: Flags = Flags(1 << 1);
    pub const ABSTRACT: Flags = Flags(1 << 2);
    pub const READONLY: Flags = Flags(1 << 3);
    pub const BY_REFERENCE: Flags = Flags(1 << 4);
    pub const VARIADIC: Flags = Flags(1 << 5);
    pub const PROMOTED: Flags = Flags(1 << 6);
    pub const DEPRECATED: Flags = Flags(1 << 7);
    pub const INTERNAL: Flags = Flags(1 << 8);
    pub const SYNTHETIC: Flags = Flags(1 << 9);
    pub const GENERATED_STUB: Flags = Flags(1 << 10);
    pub const CLASS_ALIAS: Flags = Flags(1 << 11);
    pub const SOFT_FINAL: Flags = Flags(1 << 12);
    /// The return type was not declared but inferred from the body, so it
    /// may depend on other files. A snapshot with return inference
    /// recomputes it on demand.
    pub const INFERRED_RETURN: Flags = Flags(1 << 13);

    pub const fn empty() -> Flags {
        Flags(0)
    }

    pub const fn has(self, flag: Flags) -> bool {
        self.0 & flag.0 != 0
    }

    pub fn insert(&mut self, flag: Flags) {
        self.0 |= flag.0;
    }

    pub fn remove(&mut self, flag: Flags) {
        self.0 &= !flag.0;
    }
}

impl std::ops::BitOr for Flags {
    type Output = Flags;
    fn bitor(self, rhs: Flags) -> Flags {
        Flags(self.0 | rhs.0)
    }
}

impl std::ops::BitOrAssign for Flags {
    fn bitor_assign(&mut self, rhs: Flags) {
        self.0 |= rhs.0;
    }
}

impl fmt::Debug for Flags {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "Flags({:#x})", self.0)
    }
}

/// A function/method parameter in declaration order.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Parameter {
    pub id: SymbolId,
    pub name: Str,
    pub ty: Type,
    pub native_type: Type,
    pub doc_type: Type,
    pub assistant_tags: Vec<Str>,
    pub attributes: Vec<Attribute>,
    pub default_value: Option<Box<AttributeValue>>,
    pub range: TextRange,
    pub selection_range: TextRange,
    pub default_range: TextRange,
    pub flags: Flags,
    pub optional: bool,
}

/// A PHPDoc generic parameter.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct TemplateParameter {
    pub name: Str,
    pub bound: Type,
    pub default: Type,
    pub covariant: bool,
    pub contravariant: bool,
}

/// The constant subset of PHP expressions retained from attribute
/// arguments without keeping syntax alive.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
#[repr(u8)]
pub enum AttributeValueKind {
    #[default]
    Unknown = 0,
    String,
    Integer,
    Float,
    Bool,
    Null,
    Constant,
    ClassConstant,
    Array,
    Expression,
}

impl AttributeValueKind {
    pub fn from_u8(value: u8) -> Option<Self> {
        use AttributeValueKind::*;
        [
            Unknown,
            String,
            Integer,
            Float,
            Bool,
            Null,
            Constant,
            ClassConstant,
            Array,
            Expression,
        ]
        .get(usize::from(value))
        .copied()
    }
}

/// An immutable PHP attribute constant expression. `expression` keeps valid
/// insertion text; `value` holds the decoded scalar or resolved constant.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct AttributeValue {
    pub kind: AttributeValueKind,
    pub value: Str,
    pub expression: Str,
    pub items: Vec<AttributeArrayItem>,
}

/// A list or keyed PHP array entry.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct AttributeArrayItem {
    pub key: AttributeValue,
    pub has_key: bool,
    pub value: AttributeValue,
}

/// An attribute argument in declaration order, optionally named.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct AttributeArgument {
    pub name: Str,
    pub value: AttributeValue,
    pub range: TextRange,
}

/// A resolved attribute with constant arguments.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Attribute {
    pub name: Str,
    pub arguments: Vec<AttributeArgument>,
    pub range: TextRange,
}

/// A top-level key/value declaration of a PHP array constant.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct ConstantArrayItem {
    pub key: Str,
    pub key_range: TextRange,
    pub value: Str,
    pub value_range: TextRange,
    pub ty: Type,
}

/// A scalar/null return expression declared directly by a function.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct LiteralReturn {
    pub value: Str,
    pub range: TextRange,
    pub ty: Type,
}

/// A class-constant return expression declared directly by a function.
/// `receiver` is a resolved class name or one of `self`, `static`, `parent`.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct ConstantReturn {
    pub receiver: Str,
    pub name: Str,
    pub range: TextRange,
}

/// A method adaptation declared by a trait use statement.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct TraitAlias {
    pub trait_name: Str,
    pub method: Str,
    pub alias: Str,
    pub visibility: Visibility,
    pub has_visibility: bool,
}

/// A PHPStan/Psalm conditional assertion made by a function.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct TypeAssertion {
    pub target: Str,
    pub ty: Type,
    pub when_true: bool,
    pub conditional: bool,
    pub negated: bool,
}

/// Infrequent function-like collections, shared immutably between the
/// workspace graph and materialized symbols.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct SignatureExtras {
    pub templates: Vec<TemplateParameter>,
    pub throws: Vec<Type>,
    pub assertions: Vec<TypeAssertion>,
    pub literal_returns: Vec<LiteralReturn>,
    pub constant_returns: Vec<ConstantReturn>,
}

impl SignatureExtras {
    fn is_empty(&self) -> bool {
        self.templates.is_empty()
            && self.throws.is_empty()
            && self.assertions.is_empty()
            && self.literal_returns.is_empty()
            && self.constant_returns.is_empty()
    }
}

/// Class hierarchy edges. Names and typed edges are kept separately because
/// PHPDoc may specialize only some parents.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct SymbolHierarchy {
    pub extends: Vec<Str>,
    pub implements: Vec<Str>,
    pub traits: Vec<Str>,
    pub extends_types: Vec<Type>,
    pub implements_types: Vec<Type>,
    pub trait_types: Vec<Type>,
    pub aliases: Vec<TraitAlias>,
}

impl SymbolHierarchy {
    fn is_empty(&self) -> bool {
        self.extends.is_empty()
            && self.implements.is_empty()
            && self.traits.is_empty()
            && self.extends_types.is_empty()
            && self.implements_types.is_empty()
            && self.trait_types.is_empty()
            && self.aliases.is_empty()
    }
}

/// Attributes, array-constant items, and the PHPDoc summary.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct SymbolMetadata {
    pub attributes: Vec<Attribute>,
    pub constant_array: Vec<ConstantArrayItem>,
    pub doc_summary: Str,
}

impl SymbolMetadata {
    fn is_empty(&self) -> bool {
        self.attributes.is_empty() && self.constant_array.is_empty() && self.doc_summary.is_empty()
    }
}

/// An optional immutable side record. Empty records are never stored, so
/// structural equality does not depend on how a symbol was built.
#[derive(Debug, PartialEq)]
pub struct Shared<T>(Option<Arc<T>>);

impl<T> Clone for Shared<T> {
    fn clone(&self) -> Self {
        Shared(self.0.clone())
    }
}

impl<T> Default for Shared<T> {
    fn default() -> Self {
        Shared(None)
    }
}

impl<T> Shared<T> {
    /// The record, absent when empty.
    pub fn get(&self) -> Option<&T> {
        self.0.as_deref()
    }

    pub(crate) fn arc(&self) -> Option<&Arc<T>> {
        self.0.as_ref()
    }

    pub(crate) fn from_arc(value: Option<Arc<T>>) -> Self {
        Shared(value)
    }
}

/// A side record that is dropped when empty.
pub trait SideRecord: Clone + Default {
    fn is_empty_record(&self) -> bool;
}

impl SideRecord for SignatureExtras {
    fn is_empty_record(&self) -> bool {
        self.is_empty()
    }
}

impl SideRecord for SymbolHierarchy {
    fn is_empty_record(&self) -> bool {
        self.is_empty()
    }
}

impl SideRecord for SymbolMetadata {
    fn is_empty_record(&self) -> bool {
        self.is_empty()
    }
}

impl<T: SideRecord> Shared<T> {
    pub(crate) fn new(value: T) -> Self {
        if value.is_empty_record() {
            Shared(None)
        } else {
            Shared(Some(Arc::new(value)))
        }
    }

    fn update(&mut self, change: impl FnOnce(&mut T)) {
        let mut value = self.get().cloned().unwrap_or_default();
        change(&mut value);
        *self = Shared::new(value);
    }
}

/// A declaration visible to semantic queries.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Symbol {
    pub id: SymbolId,
    pub kind: SymbolKind,
    pub name: Str,
    pub fully_qualified: Str,
    pub container: SymbolId,
    pub path: Str,
    pub range: TextRange,
    pub selection_range: TextRange,
    pub body_range: TextRange,
    pub visibility: Visibility,
    pub write_visibility: Visibility,
    pub has_write_visibility: bool,
    pub flags: Flags,
    pub ty: Type,
    pub native_type: Type,
    pub doc_type: Type,
    pub return_type: Type,
    pub parameters: Vec<Parameter>,
    /// Shared templates, throws, assertions, and literal/constant returns;
    /// use the accessors and setters.
    pub signature: Shared<SignatureExtras>,
    /// Shared hierarchy edges; use the accessors and setters.
    pub hierarchy: Shared<SymbolHierarchy>,
    /// Shared attributes, array-constant items, and summary; use the
    /// accessors and setters.
    pub metadata: Shared<SymbolMetadata>,
}

macro_rules! accessor {
    ($getter:ident, $setter:ident, $group:ident, $field:ident, $item:ty) => {
        pub fn $getter(&self) -> &[$item] {
            self.$group.get().map_or(&[], |group| &group.$field)
        }

        pub fn $setter(&mut self, values: Vec<$item>) {
            self.$group.update(|group| group.$field = values);
        }
    };
}

impl Symbol {
    accessor!(
        templates,
        set_templates,
        signature,
        templates,
        TemplateParameter
    );
    accessor!(throws, set_throws, signature, throws, Type);
    accessor!(
        assertions,
        set_assertions,
        signature,
        assertions,
        TypeAssertion
    );
    accessor!(
        literal_returns,
        set_literal_returns,
        signature,
        literal_returns,
        LiteralReturn
    );
    accessor!(
        constant_returns,
        set_constant_returns,
        signature,
        constant_returns,
        ConstantReturn
    );
    accessor!(extends, set_extends, hierarchy, extends, Str);
    accessor!(implements, set_implements, hierarchy, implements, Str);
    accessor!(traits, set_traits, hierarchy, traits, Str);
    accessor!(
        extends_types,
        set_extends_types,
        hierarchy,
        extends_types,
        Type
    );
    accessor!(
        implements_types,
        set_implements_types,
        hierarchy,
        implements_types,
        Type
    );
    accessor!(trait_types, set_trait_types, hierarchy, trait_types, Type);
    accessor!(
        trait_aliases,
        set_trait_aliases,
        hierarchy,
        aliases,
        TraitAlias
    );
    accessor!(attributes, set_attributes, metadata, attributes, Attribute);
    accessor!(
        constant_array,
        set_constant_array,
        metadata,
        constant_array,
        ConstantArrayItem
    );

    /// The declaration's PHPDoc summary.
    pub fn doc_summary(&self) -> &str {
        self.metadata
            .get()
            .map_or("", |metadata| &metadata.doc_summary)
    }

    pub fn set_doc_summary(&mut self, summary: impl Into<Str>) {
        let summary = summary.into();
        self.metadata
            .update(|metadata| metadata.doc_summary = summary);
    }

    /// Replaces all infrequent function-like collections at once.
    pub fn set_signature_extras(
        &mut self,
        templates: Vec<TemplateParameter>,
        throws: Vec<Type>,
        assertions: Vec<TypeAssertion>,
        literal_returns: Vec<LiteralReturn>,
        constant_returns: Vec<ConstantReturn>,
    ) {
        self.signature = Shared::<SignatureExtras>::new(SignatureExtras {
            templates,
            throws,
            assertions,
            literal_returns,
            constant_returns,
        });
    }

    /// Replaces all hierarchy collections at once.
    #[allow(clippy::too_many_arguments)]
    pub fn set_hierarchy(
        &mut self,
        extends: Vec<Str>,
        implements: Vec<Str>,
        traits: Vec<Str>,
        extends_types: Vec<Type>,
        implements_types: Vec<Type>,
        trait_types: Vec<Type>,
        aliases: Vec<TraitAlias>,
    ) {
        self.hierarchy = Shared::<SymbolHierarchy>::new(SymbolHierarchy {
            extends,
            implements,
            traits,
            extends_types,
            implements_types,
            trait_types,
            aliases,
        });
    }

    /// Replaces all metadata at once.
    pub fn set_metadata(
        &mut self,
        attributes: Vec<Attribute>,
        constant_array: Vec<ConstantArrayItem>,
        doc_summary: impl Into<Str>,
    ) {
        self.metadata = Shared::<SymbolMetadata>::new(SymbolMetadata {
            attributes,
            constant_array,
            doc_summary: doc_summary.into(),
        });
    }

    pub fn is_class_like(&self) -> bool {
        self.kind.is_class_like()
    }

    pub fn is_function_like(&self) -> bool {
        self.kind.is_function_like()
    }
}

/// The internal, workspace-unique name of an anonymous class declaration.
pub fn anonymous_class_name(path: &str, start: u32) -> String {
    format!("{{anonymous@{path}:{start}}}")
}

/// A deterministic declaration ID. The source offset disambiguates
/// anonymous/local declarations; named declarations stay stable when
/// unrelated declarations change elsewhere in the file.
pub fn new_symbol_id(kind: SymbolKind, fully_qualified: &str, path: &str, start: u32) -> SymbolId {
    let mut id = String::with_capacity(4 + fully_qualified.len().max(path.len() + 11));
    if fully_qualified.is_empty() {
        id.push_str(itoa(kind as u8).as_str());
        id.push(':');
        id.push_str(path);
        id.push(':');
        id.push_str(&start.to_string());
    } else {
        write_named_symbol_id(&mut id, kind, fully_qualified);
    }
    SymbolId::from(id)
}

/// Appends the ID [`new_symbol_id`] derives for a declaration with a
/// non-empty fully qualified name.
pub(crate) fn write_named_symbol_id(target: &mut String, kind: SymbolKind, fully_qualified: &str) {
    let kind = kind as u8;
    if kind >= 10 {
        target.push(char::from(b'0' + kind / 10));
    }
    target.push(char::from(b'0' + kind % 10));
    target.push(':');
    write_lower_identifier(
        target,
        fully_qualified
            .strip_prefix('\\')
            .unwrap_or(fully_qualified),
    );
}

fn itoa(value: u8) -> String {
    value.to_string()
}

pub(crate) fn write_lower_identifier(target: &mut String, value: &str) {
    if value.is_ascii() {
        target.extend(
            value
                .bytes()
                .map(|byte| char::from(byte.to_ascii_lowercase())),
        );
    } else {
        target.push_str(&swlsp_php_types::go_to_lower(value));
    }
}
