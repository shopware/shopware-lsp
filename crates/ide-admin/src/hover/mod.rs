//! Hover providers.
//!
//! Every provider builds Markdown from byte ranges and converts byte
//! ranges to UTF-16 positions only when building the protocol range.

mod admin;

pub use admin::AdminHoverProvider;
// What the hover providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::hover::*;
