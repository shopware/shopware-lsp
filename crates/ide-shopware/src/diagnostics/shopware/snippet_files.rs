//! Snippet files that drifted from the main language file beside them:
//! keys the main file defines and this one lacks, keys only this one has,
//! and values of a different type.

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_parser_json::syntax::{
    JSON_ARRAY, JSON_BOOLEAN, JSON_NULL_VALUE, JSON_NUMBER, JSON_OBJECT, JSON_STRING,
};
use swlsp_syntax::{SyntaxNode, TextRange};
use swlsp_uriutil::filepath;

use super::disk_stamp::json_directory_stamp;
use super::json_keys::{pair_key, pair_value, pairs, root_object};
use super::util::warning;
use crate::diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};

const MISSING_CODE: &str = "shopware.snippet.file.missing-key";
const UNKNOWN_CODE: &str = "shopware.snippet.file.unknown-key";
const TYPE_CODE: &str = "shopware.snippet.file.type-mismatch";

/// How many missing keys one message names.
const NAMED_KEYS: usize = 5;

#[derive(Clone, Default)]
pub struct SnippetFileAnalyzer;

impl SnippetFileAnalyzer {
    pub fn new() -> Self {
        Self
    }
}

impl DeclaredAnalyzer for SnippetFileAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "shopware.snippet_files",
        description: "Snippet files that differ from the main language file (en.json, else en-GB.json) in the same directory: missing keys, keys the main file does not define, and values of another type.",
        domain: "shopware.snippets",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Json],
        source: "shopware-lsp",
        codes: &[MISSING_CODE, TYPE_CODE, UNKNOWN_CODE],
    };
}

impl ProblemAnalyzer for SnippetFileAnalyzer {
    fn input_revision(&self, document: &TextDocument) -> Option<u64> {
        // A snippet file is compared with a sibling on disk.
        if !is_snippet_file(&filepath::to_slash(&document.uri)) {
            return Some(0);
        }
        let path = swlsp_uriutil::path(&document.uri).ok()?;
        Some(json_directory_stamp(&filepath::dir(&path)))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        if !is_snippet_file(&filepath::to_slash(&document.uri)) {
            return Ok(Vec::new());
        }
        let path = filepath::clean(&swlsp_uriutil::path(&document.uri)?);
        let Some(main_path) = main_snippet_file(&path) else {
            return Ok(Vec::new());
        };
        let Ok(main_source) = std::fs::read_to_string(&main_path) else {
            return Ok(Vec::new());
        };
        let main_root = swlsp_parser_json::parse(&main_source).root();
        let root = swlsp_parser_json::parse(document.source()).root();
        let (Some(main), Some(object)) = (root_object(&main_root), root_object(&root)) else {
            return Ok(Vec::new());
        };
        // Keys missing at the top level are reported on the first key.
        let anchor = pairs(&object)
            .find_map(|pair| pair_key(&pair))
            .map_or_else(|| object.text_range(), |(_, range)| range);
        let mut comparison = Comparison {
            ctx,
            main_name: filepath::base(&main_path),
            problems: Vec::new(),
        };
        comparison.objects(&main, &object, "", anchor);
        if ctx.is_cancelled() {
            return Ok(Vec::new());
        }
        let mut problems = comparison.problems;
        problems.sort_by_key(|problem| (problem.range.start(), problem.id.clone()));
        Ok(problems)
    }
}

/// Whether `path` is a storefront snippet file (below `Resources/snippet/`)
/// or an Administration one (in a `snippet` directory of the Administration
/// sources).
fn is_snippet_file(path: &str) -> bool {
    path.ends_with(".json")
        && (path.contains("/Resources/snippet/") || swlsp_snippet::is_admin_snippet_file(path))
}

/// The main language file beside `path`: the sibling with the same stem in
/// `en`, else `en-GB` (`storefront.de-DE.json` pairs with
/// `storefront.en-GB.json`, not with `gallery.en-GB.json`). `None` when
/// there is none, when `path` is the main file itself, or when its name
/// carries no locale.
fn main_snippet_file(path: &str) -> Option<String> {
    let directory = filepath::dir(path);
    let name = filepath::base(path);
    let (stem, locale) = split_snippet_locale(&name)?;
    let mut names: Vec<String> = std::fs::read_dir(&directory)
        .ok()?
        .flatten()
        .filter_map(|entry| entry.file_name().into_string().ok())
        .filter(|candidate| {
            split_snippet_locale(candidate).is_some_and(|(candidate_stem, candidate_locale)| {
                candidate_stem == stem && candidate_locale != locale
            })
        })
        .collect();
    names.sort();
    let main = ["en", "en-GB"].iter().find_map(|wanted| {
        names
            .iter()
            .find(|candidate| split_snippet_locale(candidate).is_some_and(|(_, l)| l == *wanted))
    })?;
    Some(filepath::join(&[directory.as_str(), main.as_str()]))
}

/// `("storefront", "de-DE")` for `storefront.de-DE.json`, `("", "de")`
/// for `de.json`; `None` without a locale-shaped last segment.
fn split_snippet_locale(name: &str) -> Option<(&str, &str)> {
    let stem = name.strip_suffix(".json")?;
    let (prefix, locale) = match stem.rsplit_once('.') {
        Some((prefix, locale)) => (prefix, locale),
        None => ("", stem),
    };
    let is_locale = match locale.split_once('-') {
        Some((language, region)) => {
            (2..=3).contains(&language.len())
                && language.bytes().all(|byte| byte.is_ascii_lowercase())
                && (2..=4).contains(&region.len())
                && region.bytes().all(|byte| byte.is_ascii_alphanumeric())
        }
        None => {
            (2..=3).contains(&locale.len()) && locale.bytes().all(|byte| byte.is_ascii_lowercase())
        }
    };
    is_locale.then_some((prefix, locale))
}

struct Comparison<'a> {
    ctx: &'a Context,
    main_name: String,
    problems: Vec<Problem>,
}

impl Comparison<'_> {
    /// Compares `object` with the main file's `main` object; `anchor` is
    /// where keys missing from `object` are reported.
    fn objects(&mut self, main: &SyntaxNode, object: &SyntaxNode, prefix: &str, anchor: TextRange) {
        if self.ctx.is_cancelled() {
            return;
        }
        let dotted = |key: &str| match prefix {
            "" => key.to_owned(),
            prefix => format!("{prefix}.{key}"),
        };
        let mut own: HashMap<String, (TextRange, Option<SyntaxNode>)> = HashMap::new();
        for pair in pairs(object) {
            if let Some((key, range)) = pair_key(&pair) {
                own.insert(key, (range, pair_value(&pair)));
            }
        }
        let mut missing = Vec::new();
        let mut defined = Vec::new();
        for pair in pairs(main) {
            let Some((key, _)) = pair_key(&pair) else {
                continue;
            };
            let Some((range, value)) = own.get(&key) else {
                missing.push(dotted(&key));
                continue;
            };
            if let (Some(main_value), Some(value)) = (pair_value(&pair), value) {
                let (expected, actual) = (type_name(&main_value), type_name(value));
                if expected != actual {
                    self.problems.push(warning(
                        TYPE_CODE,
                        *range,
                        format!(
                            "Snippet \"{}\" is {actual} here but {expected} in {}",
                            dotted(&key),
                            self.main_name
                        ),
                    ));
                } else if main_value.kind() == JSON_OBJECT {
                    self.objects(&main_value, value, &dotted(&key), *range);
                }
            }
            defined.push(key);
        }
        for key in &defined {
            own.remove(key);
        }
        for (key, (range, _)) in own {
            self.problems.push(warning(
                UNKNOWN_CODE,
                range,
                format!(
                    "Snippet \"{}\" is not defined in {}",
                    dotted(&key),
                    self.main_name
                ),
            ));
        }
        if missing.is_empty() {
            return;
        }
        let named = missing
            .iter()
            .take(NAMED_KEYS)
            .map(|key| format!("\"{key}\""))
            .collect::<Vec<_>>()
            .join(", ");
        let more = match missing.len().saturating_sub(NAMED_KEYS) {
            0 => String::new(),
            more => format!(" and {more} more"),
        };
        self.problems.push(warning(
            MISSING_CODE,
            anchor,
            format!(
                "Missing snippets defined in {}: {named}{more}",
                self.main_name
            ),
        ));
    }
}

fn type_name(value: &SyntaxNode) -> &'static str {
    match value.kind() {
        JSON_OBJECT => "an object",
        JSON_ARRAY => "an array",
        JSON_STRING => "a string",
        JSON_NUMBER => "a number",
        JSON_BOOLEAN => "a boolean",
        JSON_NULL_VALUE => "null",
        _ => "a value",
    }
}
