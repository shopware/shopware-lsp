//! Template substitution, persistence, detaching/interning, and PHPDoc
//! alias references.

use std::borrow::Borrow;
use std::collections::{BTreeMap, BTreeSet, HashMap, HashSet};
use std::hash::{BuildHasher, Hash};

use crate::kind::Kind;
use crate::parser::{ParseError, parse};
use crate::relations::Relations;
use crate::ty::{
    CallableParameter, Details, Name, Node, ShapeField, Type, valid_identifier,
    valid_qualified_name,
};

/// Lookup of template replacements by template name.
pub trait TemplateMap {
    fn is_empty(&self) -> bool;
    fn lookup(&self, name: &str) -> Option<&Type>;
}

impl<K: Borrow<str> + Hash + Eq, S: BuildHasher> TemplateMap for HashMap<K, Type, S> {
    fn is_empty(&self) -> bool {
        HashMap::is_empty(self)
    }
    fn lookup(&self, name: &str) -> Option<&Type> {
        self.get(name)
    }
}

impl<K: Borrow<str> + Ord> TemplateMap for BTreeMap<K, Type> {
    fn is_empty(&self) -> bool {
        BTreeMap::is_empty(self)
    }
    fn lookup(&self, name: &str) -> Option<&Type> {
        self.get(name)
    }
}

/// A set of template names.
pub trait NameSet {
    fn is_empty(&self) -> bool;
    fn contains_name(&self, name: &str) -> bool;
}

impl<K: Borrow<str> + Hash + Eq, S: BuildHasher> NameSet for HashSet<K, S> {
    fn is_empty(&self) -> bool {
        HashSet::is_empty(self)
    }
    fn contains_name(&self, name: &str) -> bool {
        self.contains(name)
    }
}

impl<K: Borrow<str> + Ord> NameSet for BTreeSet<K> {
    fn is_empty(&self) -> bool {
        BTreeSet::is_empty(self)
    }
    fn contains_name(&self, name: &str) -> bool {
        self.contains(name)
    }
}

/// Rebuilds `value` through the canonical constructors after mapping its
/// children with `map`.
fn rebuild(value: &Type, map: &mut dyn FnMut(&Type) -> Type, conditional_select: bool) -> Type {
    let mut args: Vec<Type> = value.arguments().iter().map(&mut *map).collect();
    let take = |args: &mut Vec<Type>, index: usize| std::mem::take(&mut args[index]);
    match value.kind() {
        Kind::Object => {
            if value.name().is_empty() {
                return Type::OBJECT;
            }
            Type::named(value.name(), args)
        }
        Kind::SelfType => Type::self_type(args),
        Kind::Static => Type::static_type(args),
        Kind::Parent => Type::parent_type(args),
        Kind::Array => {
            let value = take(&mut args, 1);
            Type::array(take(&mut args, 0), value)
        }
        Kind::NonEmptyArray => {
            let value = take(&mut args, 1);
            Type::non_empty_array(take(&mut args, 0), value)
        }
        Kind::List => Type::list(take(&mut args, 0)),
        Kind::NonEmptyList => Type::non_empty_list(take(&mut args, 0)),
        Kind::Iterable => {
            let value = take(&mut args, 1);
            Type::iterable(take(&mut args, 0), value)
        }
        Kind::ClassString => Type::class_string(take(&mut args, 0)),
        Kind::Union => Type::union(args),
        Kind::Intersection => Type::intersection(args),
        Kind::Conditional => {
            if args.len() != 4 {
                return value.clone();
            }
            let if_false = args.pop().unwrap_or_default();
            let if_true = args.pop().unwrap_or_default();
            let target = args.pop().unwrap_or_default();
            let subject = args.pop().unwrap_or_default();
            if conditional_select && !crate::ty::contains_template(&subject) {
                if Relations::default().is_subtype(&subject, &target) {
                    return if_true;
                }
                return if_false;
            }
            Type::conditional(subject, target, if_true, if_false)
        }
        Kind::Callable => {
            let parameters = value
                .parameters()
                .iter()
                .map(|parameter| CallableParameter {
                    ty: map(&parameter.ty),
                    ..parameter.clone()
                })
                .collect();
            Type::callable(parameters, map(value.result()))
        }
        Kind::ArrayShape | Kind::ObjectShape => {
            let fields = value
                .fields()
                .iter()
                .map(|field| ShapeField {
                    ty: map(&field.ty),
                    ..field.clone()
                })
                .collect();
            if value.kind() == Kind::ArrayShape {
                Type::array_shape(fields, value.is_open_shape())
            } else {
                Type::object_shape(fields, value.is_open_shape())
            }
        }
        _ => value.clone(),
    }
}

/// Replaces template variables recursively.
pub fn substitute<M: TemplateMap + ?Sized>(value: &Type, templates: &M) -> Type {
    if templates.is_empty() {
        return value.clone();
    }
    if value.kind() == Kind::Template {
        return templates
            .lookup(value.name())
            .cloned()
            .unwrap_or_else(|| value.clone());
    }
    rebuild(value, &mut |child| substitute(child, templates), true)
}

/// Turns unqualified object names back into template variables using
/// declaration context recovered alongside a persisted type.
pub fn restore_templates<S: NameSet + ?Sized>(value: &Type, templates: &S) -> Type {
    if templates.is_empty() {
        return value.clone();
    }
    if value.kind() == Kind::Object
        && !value.name().contains('\\')
        && templates.contains_name(value.name())
    {
        return Type::template(value.name());
    }
    rebuild(
        value,
        &mut |child| restore_templates(child, templates),
        false,
    )
}

fn collect_template_names<'a>(value: &'a Type, result: &mut BTreeSet<&'a str>) {
    match value.kind() {
        Kind::Template => {
            result.insert(value.name());
        }
        Kind::Callable => {
            for parameter in value.parameters() {
                collect_template_names(&parameter.ty, result);
            }
            collect_template_names(value.result(), result);
        }
        Kind::ArrayShape | Kind::ObjectShape => {
            for field in value.fields() {
                collect_template_names(&field.ty, result);
            }
        }
        _ => {
            for argument in value.arguments() {
                collect_template_names(argument, result);
            }
        }
    }
}

pub(crate) const PERSISTED_TEMPLATE_PREFIX: &str = "\0shopware-lsp-templates:";

/// Errors restoring a persisted type.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum PersistedError {
    MissingTemplateSeparator,
    Parse(ParseError),
}

impl std::fmt::Display for PersistedError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            PersistedError::MissingTemplateSeparator => {
                formatter.write_str("parse persisted PHP type: missing template separator")
            }
            PersistedError::Parse(error) => error.fmt(formatter),
        }
    }
}

impl std::error::Error for PersistedError {}

impl Type {
    /// Canonical text plus a private header preserving template identity:
    /// template variables and class names render alike in PHPDoc.
    pub fn persisted_string(&self) -> String {
        let mut templates = BTreeSet::new();
        collect_template_names(self, &mut templates);
        if templates.is_empty() {
            return self.text().into_owned();
        }
        let mut out = String::from(PERSISTED_TEMPLATE_PREFIX);
        for (index, name) in templates.iter().enumerate() {
            if index > 0 {
                out.push(',');
            }
            out.push_str(name);
        }
        out.push('\0');
        out.push_str(&self.text());
        out
    }
}

/// Restores a type written by [`Type::persisted_string`]. Also accepts the
/// canonical strings emitted by older caches.
pub fn parse_persisted(text: &str) -> Result<Type, PersistedError> {
    let Some(payload) = text.strip_prefix(PERSISTED_TEMPLATE_PREFIX) else {
        return parse(text).map_err(PersistedError::Parse);
    };
    let Some((names, source)) = payload.split_once('\0') else {
        return Err(PersistedError::MissingTemplateSeparator);
    };
    let names: HashSet<&str> = names.split(',').filter(|name| !name.is_empty()).collect();
    let value = parse(source).map_err(PersistedError::Parse)?;
    Ok(restore_templates(&value, &names))
}

impl serde::Serialize for Type {
    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        serializer.serialize_str(&self.persisted_string())
    }
}

impl<'de> serde::Deserialize<'de> for Type {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        struct Visitor;

        impl serde::de::Visitor<'_> for Visitor {
            type Value = Type;

            fn expecting(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
                formatter.write_str("a persisted PHP type string")
            }

            fn visit_str<E: serde::de::Error>(self, text: &str) -> Result<Type, E> {
                parse_persisted(text).map_err(|error| {
                    E::custom(format_args!("parse persisted PHP type {text:?}: {error}"))
                })
            }

            fn visit_bytes<E: serde::de::Error>(self, bytes: &[u8]) -> Result<Type, E> {
                // Older caches stored the canonical text as a byte string.
                self.visit_str(&String::from_utf8_lossy(bytes))
            }
        }

        deserializer.deserialize_str(Visitor)
    }
}

/// Deduplicates strings so equal names share one allocation.
#[derive(Default)]
pub struct Interner {
    names: HashSet<Name>,
}

impl Interner {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn intern(&mut self, value: &str) -> Name {
        if let Some(existing) = self.names.get(value) {
            return existing.clone();
        }
        let name = Name::from(value);
        self.names.insert(name.clone());
        name
    }

    pub fn len(&self) -> usize {
        self.names.len()
    }

    pub fn is_empty(&self) -> bool {
        self.names.is_empty()
    }
}

/// Rebuilds type graphs with interned names while preserving node sharing
/// within one semantic document. Inline builtins are returned unchanged.
///
/// Go uses this to copy source-backed substrings; in Rust strings are already
/// owned, so the remaining value is name interning and node deduplication.
pub struct Detacher<'a> {
    intern: Box<dyn FnMut(&str) -> Name + Send + 'a>,
    /// Keyed by source node address; the source is retained so the address
    /// cannot be reused while the detacher lives.
    nodes: HashMap<usize, (Type, Type)>,
}

impl Default for Detacher<'_> {
    fn default() -> Self {
        let mut interner = Interner::new();
        Detacher::with_interner(move |value| interner.intern(value))
    }
}

impl<'a> Detacher<'a> {
    /// A detacher with its own private [`Interner`].
    pub fn new() -> Self {
        Self::default()
    }

    pub fn with_interner(intern: impl FnMut(&str) -> Name + Send + 'a) -> Self {
        Detacher {
            intern: Box::new(intern),
            nodes: HashMap::new(),
        }
    }

    pub fn detach(&mut self, value: &Type) -> Type {
        let (Some(address), Some(node)) = (value.node_address(), value.node()) else {
            return value.clone();
        };
        if let Some((_, detached)) = self.nodes.get(&address) {
            return detached.clone();
        }
        let args = node
            .args
            .iter()
            .map(|argument| self.detach(argument))
            .collect();
        let details = node.details.as_ref().map(|details| {
            Box::new(Details {
                params: details
                    .params
                    .iter()
                    .map(|parameter| CallableParameter {
                        name: (self.intern)(&parameter.name),
                        ty: self.detach(&parameter.ty),
                        ..parameter.clone()
                    })
                    .collect(),
                fields: details
                    .fields
                    .iter()
                    .map(|field| ShapeField {
                        name: (self.intern)(&field.name),
                        ty: self.detach(&field.ty),
                        optional: field.optional,
                    })
                    .collect(),
                result: self.detach(&details.result),
                open: details.open,
            })
        });
        let detached = Type::from_node(Node {
            kind: node.kind,
            slot: node.slot.as_deref().map(|slot| (self.intern)(slot)),
            args,
            details,
        });
        self.nodes
            .insert(address, (value.clone(), detached.clone()));
        detached
    }
}

const PHPDOC_ALIAS_PREFIX: &str = "__ShopwareLSPPHPDocAlias\\";

/// A stable nominal reference to a PHPDoc type alias. Relations expand it
/// through [`crate::Hierarchy::resolve_type_alias`].
pub fn phpdoc_alias(class_name: &str, alias: &str) -> Type {
    let class_name = class_name.trim().trim_matches('\\');
    let alias = alias.trim();
    if !valid_qualified_name(class_name) || !valid_identifier(alias) {
        return Type::UNKNOWN;
    }
    Type::named(
        &format!("{PHPDOC_ALIAS_PREFIX}{class_name}\\{alias}"),
        Vec::new(),
    )
}

/// The declaring class and alias name encoded in a PHPDoc alias reference.
pub fn phpdoc_alias_parts(value: &Type) -> Option<(&str, &str)> {
    if value.kind() != Kind::Object {
        return None;
    }
    let name = value.name();
    let name = name.strip_prefix('\\').unwrap_or(name);
    let payload = name.strip_prefix(PHPDOC_ALIAS_PREFIX)?;
    let separator = payload.rfind('\\')?;
    if separator == 0 || separator + 1 >= payload.len() {
        return None;
    }
    Some((&payload[..separator], &payload[separator + 1..]))
}
