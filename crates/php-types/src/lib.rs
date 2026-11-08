//! Immutable semantic PHP type algebra (port of `internal/php/types`).
//!
//! The crate deliberately has no dependency on the PHP index or CST. Syntax
//! frontends produce unresolved type expressions, binders resolve names, and
//! workspace services provide hierarchy information to [`Relations`].
//!
//! # Representation
//!
//! [`Type`] is a single tagged machine word (`Option<Type>` is one word too):
//!
//! - Payload-free builtins (`int`, `mixed`, bare `object`, bare
//!   `self`/`static`/`parent`, …), the canonical defaults
//!   (`array<mixed,mixed>`, `list<mixed>`, `iterable<mixed,mixed>`,
//!   `callable`, `class-string<object>`), and the literals `""`, `0`, `1` are
//!   encoded inline. They never allocate, and cloning/dropping them never
//!   touches a shared reference count, so hot builtins cause no cache-line
//!   contention between indexing threads.
//! - Every other type points at an `Arc`-owned immutable node. Cloning is one
//!   atomic increment. A node stores its kind, one string slot (the semantic
//!   name of named/literal kinds or the eagerly cached canonical text of
//!   composites, exactly like Go's shared slot), its generic/composite
//!   arguments as an exact-length boxed slice, and an optional boxed details
//!   record used only by callables and shapes.
//!
//! Equality and hashing are structural. Canonical text is cached on composite
//! nodes at construction, so [`Type::text`]/[`Type::key`] are free for them.
//!
//! # Names and interning
//!
//! Names are [`Name`] (`Arc<str>`). Constructors do not intern: interning on
//! every construction would add a global lock to inference hot paths. Instead,
//! persistent snapshots run types through a [`Detacher`], which interns every
//! name through an [`Interner`] (or a caller-supplied interner) and shares
//! structurally identical source nodes, mirroring Go's `Detacher`.
//!
//! # Differences from Go
//!
//! - Accessors return borrowed slices instead of defensive copies;
//!   `field`/`parameter` return `Option` instead of a zero value.
//! - Bytes produced by `\x`/octal escapes in quoted literals that do not form
//!   valid UTF-8 are replaced with U+FFFD (Go strings may hold raw bytes).
//! - Unicode classification (`strconv.IsPrint`, `strings.EqualFold`) uses the
//!   Unicode tables of the `unicode-general-category` crate and Rust's case
//!   mapping, which may differ from Go's for rarely used code points.

mod gostr;
mod kind;
mod parser;
mod relations;
mod render;
mod transform;
mod ty;

#[cfg(test)]
mod tests;

pub use gostr::{equal_fold, go_quote, go_to_lower, go_unquote};
pub use kind::Kind;
pub use parser::{ParseError, must_parse, parse, parse_native};
pub use relations::{Hierarchy, Joiner, Relations, Variance};
pub use transform::{
    Detacher, Interner, NameSet, PersistedError, TemplateMap, parse_persisted, phpdoc_alias,
    phpdoc_alias_parts, restore_templates, substitute,
};
pub use ty::{CallableParameter, Name, ShapeField, Type, contains_template, contains_uncertain};
