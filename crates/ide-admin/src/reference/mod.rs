//! Find-references providers.
//!
//! Providers holding a PHP index derive the request's memoized PHP semantic
//! state with [`php_context`] for PHP documents.

mod admin;

pub use admin::AdminReferenceProvider;
// What the reference providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::reference::*;

#[cfg(test)]
mod tests;
