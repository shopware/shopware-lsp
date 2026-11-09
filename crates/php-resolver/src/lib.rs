//! PHP name, hierarchy, member, and signature resolution over semantic
//! snapshots.
//!
//! - [`NameContext`] resolves class, function, and constant names and nested
//!   type names against a namespace, `use` imports, and PHPDoc aliases.
//! - [`parse_use_declaration`] parses `use` declaration text.
//! - [`MemberResolver`] finds effective methods, properties, constants, and
//!   enum cases across traits, trait aliases, parents, and interfaces,
//!   specializing class templates for the receiver.
//! - [`resolve_signature`] maps call arguments to parameters, infers
//!   templates, validates bounds, and specializes the return type, including
//!   `ReturnTypeContract` attributes and `.phpstorm.meta.php` contracts.

mod call_contracts;
mod imports;
mod members;
mod names;
mod signatures;

#[cfg(test)]
mod tests;

pub use call_contracts::{effective_call_return_type, iterable_types};
pub use imports::{Import, ImportKind, parse_use_declaration};
pub use members::{MemberResolver, ResolvedMember, TemplateBindings, bind_class_templates};
pub use names::NameContext;
pub use signatures::{
    Argument, ResolvedSignature, infer_templates, resolve_signature,
    resolve_signature_with_contracts,
};
