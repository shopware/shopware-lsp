//! Hover providers.
//!
//! Every provider builds Markdown from byte ranges and converts byte
//! ranges to UTF-16 positions only when building the protocol range.

mod dal;
mod snippet;

pub use dal::DalHoverProvider;
pub use snippet::{SnippetHoverProvider, extract_locale_from_path};
// What the hover providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::hover::*;
