//! Twig filter/function/test/tag catalog.

use std::cmp::Ordering;
use std::collections::{BTreeMap, HashSet};
use std::sync::Arc;

use serde::Serialize;
use serde_json::Value;
use swlsp_lsp::{CommandFunc, CommandProvider, Context};
use swlsp_php::PhpIndex;
use swlsp_textutil::eq_ignore_case;
use swlsp_twig::{TwigIndexer, TwigParameter};

use crate::analytics::request::{Field, RequestError, decode, present};
use crate::analytics::{Result, check, errorf, is_zero, optional_file_uri};
use swlsp_textutil::lowercase;

pub const LIST_TWIG_EXTENSIONS_COMMAND: &str = "shopware/symfony/analytics/twig/extensions";

#[derive(Clone)]
pub struct TwigExtensionCatalogProvider {
    twig: Option<Arc<TwigIndexer>>,
    php: Option<PhpIndex>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TwigExtensionCatalogRequest {
    pub search: String,
    pub include_filters: Option<bool>,
    pub include_functions: Option<bool>,
    pub include_tests: Option<bool>,
    pub include_tags: Option<bool>,
}

impl TwigExtensionCatalogRequest {
    fn decode(value: &Value) -> Result<Self, RequestError> {
        let members = decode(
            value,
            &[
                ("search", Field::String),
                ("includeFilters", Field::OptionalBool),
                ("includeFunctions", Field::OptionalBool),
                ("includeTests", Field::OptionalBool),
                ("includeTags", Field::OptionalBool),
            ],
        )?;
        Ok(Self {
            search: members.string("search"),
            include_filters: members.optional_bool("includeFilters"),
            include_functions: members.optional_bool("includeFunctions"),
            include_tests: members.optional_bool("includeTests"),
            include_tags: members.optional_bool("includeTags"),
        })
    }
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
pub struct TwigExtensionCatalogParameter {
    pub name: String,
    #[serde(rename = "type", skip_serializing_if = "String::is_empty")]
    pub ty: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub optional: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TwigExtensionCatalogEntry {
    #[serde(rename = "type")]
    pub ty: String,
    pub name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub class_name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub method_name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub callable: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub usage: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub parameters: Vec<TwigExtensionCatalogParameter>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub file_uri: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub source_line: i64,
    #[serde(skip_serializing_if = "is_zero")]
    pub deprecated: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub deprecation: String,
}

/// The fields shared by Twig functions, filters and tests.
struct Callable<'a> {
    name: &'a str,
    method: &'a str,
    usage: &'a str,
    parameters: &'a [TwigParameter],
    file_path: &'a str,
    line: i64,
    deprecated: bool,
    deprecation: &'a str,
}

macro_rules! callable {
    ($value:expr) => {
        Callable {
            name: &$value.name,
            method: &$value.method,
            usage: &$value.usage,
            parameters: &$value.parameters,
            file_path: &$value.file_path,
            line: $value.line,
            deprecated: $value.deprecated,
            deprecation: &$value.deprecation,
        }
    };
}

impl TwigExtensionCatalogProvider {
    pub fn new(twig: Option<Arc<TwigIndexer>>, php: Option<PhpIndex>) -> Self {
        Self { twig, php }
    }

    fn list(&self, ctx: &Context, args: Option<&Value>) -> Result<Value> {
        let request = match present(args) {
            Some(value) => TwigExtensionCatalogRequest::decode(value)
                .map_err(|error| errorf!("invalid Twig extension catalog request: {error}"))?,
            None => TwigExtensionCatalogRequest::default(),
        };
        Ok(serde_json::to_value(self.catalog(ctx, &request)?)?)
    }

    pub fn catalog(
        &self,
        ctx: &Context,
        request: &TwigExtensionCatalogRequest,
    ) -> Result<Vec<TwigExtensionCatalogEntry>> {
        let Some(twig) = &self.twig else {
            return Err(errorf!("twig extension catalog is unavailable"));
        };
        check(ctx)?;
        let search = lowercase(request.search.trim());
        let mut result = Vec::new();
        if request.include_filters.unwrap_or(true) {
            for filter in twig.get_all_twig_filters()? {
                check(ctx)?;
                if name_matches(&filter.name, &search) {
                    result.push(self.callable_entry("filter", &callable!(filter)));
                }
            }
        }
        if request.include_functions.unwrap_or(true) {
            for function in twig.get_all_twig_functions()? {
                check(ctx)?;
                if name_matches(&function.name, &search) {
                    result.push(self.callable_entry("function", &callable!(function)));
                }
            }
        }
        if request.include_tests.unwrap_or(true) {
            for test in twig.get_all_twig_tests()? {
                check(ctx)?;
                if name_matches(&test.name, &search) {
                    result.push(self.callable_entry("test", &callable!(test)));
                }
            }
        }
        if request.include_tags.unwrap_or(true) {
            for tag in twig.get_all_twig_tags()? {
                check(ctx)?;
                if !name_matches(&tag.name, &search) {
                    continue;
                }
                result.push(TwigExtensionCatalogEntry {
                    ty: "tag".to_owned(),
                    name: tag.name.clone(),
                    class_name: tag
                        .class
                        .strip_prefix('\\')
                        .unwrap_or(&tag.class)
                        .to_owned(),
                    file_uri: optional_file_uri(&tag.file_path),
                    source_line: tag.line,
                    deprecated: tag.deprecated,
                    deprecation: tag.deprecation.clone(),
                    ..TwigExtensionCatalogEntry::default()
                });
            }
        }
        let mut result = unique_entries(result);
        result.sort_by(|left, right| {
            type_order(&left.ty)
                .cmp(&type_order(&right.ty))
                .then_with(|| {
                    if eq_ignore_case(&left.name, &right.name) {
                        Ordering::Equal
                    } else {
                        lowercase(&left.name).cmp(&lowercase(&right.name))
                    }
                })
                .then_with(|| left.class_name.cmp(&right.class_name))
                .then_with(|| left.file_uri.cmp(&right.file_uri))
                .then_with(|| left.source_line.cmp(&right.source_line))
        });
        Ok(result)
    }

    fn callable_entry(&self, kind: &str, callable: &Callable<'_>) -> TwigExtensionCatalogEntry {
        let (class_name, method_name) = self.resolve_callable(callable.method, callable.file_path);
        TwigExtensionCatalogEntry {
            ty: kind.to_owned(),
            name: callable.name.to_owned(),
            class_name,
            method_name,
            callable: callable.method.to_owned(),
            usage: callable.usage.to_owned(),
            parameters: parameters(callable.parameters),
            file_uri: optional_file_uri(callable.file_path),
            source_line: callable.line,
            deprecated: callable.deprecated,
            deprecation: callable.deprecation.to_owned(),
        }
    }

    fn resolve_callable(&self, callable: &str, path: &str) -> (String, String) {
        if let Some(separator) = callable.rfind("::") {
            let class = &callable[..separator];
            return (
                class.strip_prefix('\\').unwrap_or(class).to_owned(),
                callable[separator + 2..].to_owned(),
            );
        }
        if let Some(separator) = callable.rfind("->") {
            let method = &callable[separator + 2..];
            if let Some(php) = &self.php
                && !path.is_empty()
            {
                for class in php.class_symbols_in(path) {
                    if php
                        .find_methods(&class.fully_qualified, method)
                        .iter()
                        .any(|candidate| candidate.path.as_str() == path)
                    {
                        return (class.fully_qualified.to_string(), method.to_owned());
                    }
                }
            }
            return (String::new(), method.to_owned());
        }
        (String::new(), callable.to_owned())
    }
}

impl CommandProvider for TwigExtensionCatalogProvider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let provider = self.clone();
        let command: CommandFunc = Arc::new(move |ctx, args| provider.list(ctx, args));
        BTreeMap::from([(LIST_TWIG_EXTENSIONS_COMMAND.to_owned(), command)])
    }
}

fn parameters(parameters: &[TwigParameter]) -> Vec<TwigExtensionCatalogParameter> {
    parameters
        .iter()
        .filter_map(|parameter| {
            let name = parameter.name.trim();
            let name = name.strip_prefix('$').unwrap_or(name);
            if name.is_empty() || name.starts_with('_') {
                return None;
            }
            Some(TwigExtensionCatalogParameter {
                name: name.to_owned(),
                ty: parameter.ty.clone(),
                optional: parameter.optional,
            })
        })
        .collect()
}

fn name_matches(name: &str, search: &str) -> bool {
    search.is_empty() || lowercase(name).contains(search)
}

fn unique_entries(entries: Vec<TwigExtensionCatalogEntry>) -> Vec<TwigExtensionCatalogEntry> {
    let mut seen = HashSet::with_capacity(entries.len());
    entries
        .into_iter()
        .filter(|entry| {
            seen.insert(
                [
                    entry.ty.clone(),
                    lowercase(&entry.name),
                    entry.callable.clone(),
                    entry.class_name.clone(),
                    entry.file_uri.clone(),
                    entry.source_line.to_string(),
                ]
                .join("\x00"),
            )
        })
        .collect()
}

fn type_order(kind: &str) -> u8 {
    match kind {
        "filter" => 0,
        "function" => 1,
        "test" => 2,
        _ => 3,
    }
}
