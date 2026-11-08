//! Storefront theme configuration (`theme.json`) and icon packs (port of Go
//! `internal/theme`).

mod icon_provider;
mod indexer;
mod json;
mod parser;
mod types;

pub use icon_provider::IconProvider;
pub use indexer::{ThemeConfigIndexer, is_theme_file};
pub use parser::{parse_theme_config, parse_theme_config_tree};
pub use types::ThemeConfigField;
