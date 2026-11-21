//! Administration CRUD modules generated from a description and the fields
//! of a DAL entity: text in, files out. Nothing here reads an index or
//! writes a file.

pub mod config;
mod entity;
mod render;
mod resolve;

pub use config::{CONFIG_RELATIVE_PATH, Config, ConfigError, VERSION, parse};
pub use entity::{Entity, EntityField, Kind};
pub use render::{GENERATED_DIRECTORY, GeneratedFile, Style, render};
pub use resolve::{Module, Problem, resolve};

#[cfg(test)]
mod tests;
