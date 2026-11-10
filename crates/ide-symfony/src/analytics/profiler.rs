//! Local Symfony profiler request catalog.

use std::collections::{BTreeMap, BTreeSet, HashSet};
use std::io::Read;
use std::num::IntErrorKind;
use std::sync::{Arc, LazyLock};

use regex::bytes::Regex;
use serde::Serialize;
use serde_json::Value;
use swlsp_lsp::{CommandFunc, CommandProvider, Context};
use swlsp_php::PhpIndex;
use swlsp_twig::TwigIndexer;
use swlsp_uriutil::filepath;
use url::{Position, Url};

use crate::analytics::profiler_serialized::profiler_twig_components;
use crate::analytics::request::{Field, RequestError, decode, present};
use crate::analytics::{
    Result, RouteTemplateResolver, SourceLineResolver, check, errorf, is_zero, range_len,
    range_start,
};
use swlsp_textutil::lowercase;

pub const LIST_PROFILER_REQUESTS_COMMAND: &str = "shopware/symfony/analytics/profiler/requests";

const DEFAULT_PROFILER_REQUEST_LIMIT: i64 = 25;
const MAX_PROFILER_REQUEST_LIMIT: i64 = 100;
const MAX_PROFILER_INDEX_BYTES: u64 = 16 << 20;
const MAX_PROFILER_INDEX_ROW_BYTES: usize = 1 << 20;
const MAX_PROFILER_CONTENT_BYTES: u64 = 10 << 20;
const MAX_PROFILER_SCAN_ROWS: usize = 2_000;
const ANONYMOUS_COMPONENT: &str = "Symfony\\UX\\TwigComponent\\AnonymousComponent";

static TEMPLATE_PAIR_PATTERN: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r#"(?-u)s:[0-9]+:"([^"]+)";s:[0-9]+:"([^"]+)""#).unwrap());
static FALLBACK_TEMPLATE_PATTERN: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r#"(?-u)"template_paths"[\w;:{]+"([^"]+)""#).unwrap());
static LEGACY_TEMPLATE_PATTERN: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r#"(?-u)"template\.twig \(([^"]*\.html\.\w{2,4})\)""#).unwrap());
static FRONT_CONTROLLER_PATTERN: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"(?-u)/(?:app_[A-Za-z0-9_]{2,12}|index)\.php(?:/|$)").unwrap());

#[derive(Clone)]
pub struct ProfilerCatalogProvider {
    root: String,
    php: Option<PhpIndex>,
    twig: Option<Arc<TwigIndexer>>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ProfilerRequestCatalogRequest {
    pub url: String,
    pub hash: String,
    pub controller: String,
    pub route: String,
    pub limit: i64,
    pub index_path: String,
    pub base_url: String,
}

impl ProfilerRequestCatalogRequest {
    fn decode(value: &Value) -> Result<Self, RequestError> {
        let members = decode(
            value,
            &[
                ("url", Field::String),
                ("hash", Field::String),
                ("controller", Field::String),
                ("route", Field::String),
                ("limit", Field::Int),
                ("indexPath", Field::String),
                ("baseUrl", Field::String),
            ],
        )?;
        Ok(Self {
            url: members.string("url"),
            hash: members.string("hash"),
            controller: members.string("controller"),
            route: members.string("route"),
            limit: members.int("limit"),
            index_path: members.string("indexPath"),
            base_url: members.string("baseUrl"),
        })
    }
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ProfilerRequestCatalogEntry {
    pub hash: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub method: String,
    pub url: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub status_code: i64,
    #[serde(skip_serializing_if = "is_zero")]
    pub timestamp: i64,
    pub profiler_url: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub controller: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub controller_file_uri: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub controller_line: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub route: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub entry_view: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub static_templates: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub rendered_templates: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub mail_messages: Vec<ProfilerMailMessage>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub twig_components: Vec<ProfilerRuntimeTwigComponent>,
    pub index_file_uri: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
pub struct ProfilerMailMessage {
    pub title: String,
    pub panel: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ProfilerRuntimeTwigComponent {
    pub name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub template: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub render_count: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub file_uri: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub source_line: i64,
}

impl ProfilerCatalogProvider {
    pub fn new(root: &str, php: Option<PhpIndex>, twig: Option<Arc<TwigIndexer>>) -> Self {
        Self {
            root: filepath::clean(root),
            php,
            twig,
        }
    }

    fn list(&self, ctx: &Context, args: Option<&Value>) -> Result<Value> {
        let request = match present(args) {
            Some(value) => ProfilerRequestCatalogRequest::decode(value)
                .map_err(|error| errorf!("invalid profiler request catalog request: {error}"))?,
            None => ProfilerRequestCatalogRequest::default(),
        };
        Ok(serde_json::to_value(self.catalog(ctx, &request)?)?)
    }

    pub fn catalog(
        &self,
        ctx: &Context,
        request: &ProfilerRequestCatalogRequest,
    ) -> Result<Vec<ProfilerRequestCatalogEntry>> {
        if self.root.is_empty() {
            return Err(errorf!("symfony profiler request catalog is unavailable"));
        }
        check(ctx)?;
        let base_url = normalized_profiler_base_url(&request.base_url)?;
        let index_paths = self.profiler_index_paths(&request.index_path)?;
        if index_paths.is_empty() {
            return Err(errorf!(
                "no local Symfony profiler index was found under {}",
                self.root
            ));
        }
        let rows = read_profiler_rows(ctx, &index_paths)?;
        if rows.is_empty() {
            return Err(errorf!(
                "no profiler requests were found in the local Symfony profiler index"
            ));
        }

        let mut limit = request.limit;
        if limit <= 0 {
            limit = DEFAULT_PROFILER_REQUEST_LIMIT;
        }
        limit = limit.min(MAX_PROFILER_REQUEST_LIMIT);
        let url_filter = lowercase(request.url.trim());
        let hash_filter = lowercase(request.hash.trim());
        let controller_filter = lowercase(request.controller.trim());
        let route_filter = lowercase(request.route.trim());
        let mut templates = RouteTemplateResolver::new(self.php.as_ref(), self.twig.as_deref());
        let mut lines = SourceLineResolver::default();
        let mut result = Vec::new();
        let mut seen: HashSet<String> = HashSet::with_capacity(rows.len());
        for row in rows {
            check(ctx)?;
            if !seen.insert(lowercase(&row.entry.hash)) {
                continue;
            }
            if !url_filter.is_empty() && !lowercase(&row.entry.url).contains(&url_filter) {
                continue;
            }
            if !hash_filter.is_empty() && !lowercase(&row.entry.hash).contains(&hash_filter) {
                continue;
            }
            let mut entry = row.entry;
            entry.profiler_url = profiler_url(&base_url, &entry.url, &entry.hash);
            // Profiler data is runtime state: files can disappear while the
            // index is being read, and one corrupt profile must not hide the
            // remaining request metadata.
            let content = read_profiler_content(&row.profiler_directory, &entry.hash);
            if let Some(content) = content.filter(|content| !content.is_empty()) {
                entry.controller = profiler_serialized_string(&content, "_controller");
                entry.route = profiler_serialized_string(&content, "_route");
                let rendered = profiler_rendered_templates(&content);
                entry.entry_view = profiler_entry_view(&content, &rendered);
                entry.rendered_templates = rendered;
                entry.rendered_templates.truncate(3);
                entry.mail_messages = profiler_mail_messages(&content);
                entry.twig_components = profiler_twig_components(&content);
            }
            if !controller_filter.is_empty()
                && !lowercase(&entry.controller).contains(&controller_filter)
            {
                continue;
            }
            if !route_filter.is_empty() && !lowercase(&entry.route).contains(&route_filter) {
                continue;
            }
            self.add_controller_details(&mut entry, &mut templates, &mut lines);
            self.add_runtime_twig_component_details(&mut entry, &mut lines);
            result.push(entry);
            if result.len() as i64 == limit {
                break;
            }
        }
        Ok(result)
    }

    fn add_runtime_twig_component_details(
        &self,
        entry: &mut ProfilerRequestCatalogEntry,
        lines: &mut SourceLineResolver,
    ) {
        for component in &mut entry.twig_components {
            if !component.class.is_empty()
                && !swlsp_textutil::eq_ignore_case(&component.class, ANONYMOUS_COMPONENT)
                && let Some(php) = &self.php
                && let Some(class) = php.find_class(&component.class)
            {
                component.file_uri = swlsp_uriutil::file_uri(&class.path);
                let offset = if range_len(class.selection_range) == 0 {
                    range_start(class.range)
                } else {
                    range_start(class.selection_range)
                };
                component.source_line = lines.line(&class.path, offset);
                continue;
            }
            let Some(twig) = &self.twig else {
                continue;
            };
            if component.template.is_empty() {
                continue;
            }
            let Ok(files) = twig.get_twig_files_by_rel_path(&component.template) else {
                continue;
            };
            let Some(first) = files
                .iter()
                .min_by(|left, right| left.path.cmp(&right.path))
            else {
                continue;
            };
            component.file_uri = swlsp_uriutil::file_uri(&first.path);
            component.source_line = 1;
        }
    }

    fn add_controller_details(
        &self,
        entry: &mut ProfilerRequestCatalogEntry,
        templates: &mut RouteTemplateResolver<'_>,
        lines: &mut SourceLineResolver,
    ) {
        let Some(php) = &self.php else {
            return;
        };
        let Some((class_name, method_name)) = split_profiler_controller(&entry.controller) else {
            return;
        };
        let mut template_set = BTreeSet::new();
        for method in php.find_methods(&class_name, &method_name) {
            if entry.controller_file_uri.is_empty() && !method.path.is_empty() {
                entry.controller_file_uri = swlsp_uriutil::file_uri(&method.path);
                let offset = if range_len(method.selection_range) == 0 {
                    range_start(method.range)
                } else {
                    range_start(method.selection_range)
                };
                entry.controller_line = lines.line(&method.path, offset);
            }
            let Ok(static_templates) = templates.for_method(&method) else {
                continue;
            };
            template_set.extend(
                static_templates
                    .into_iter()
                    .filter(|template| !template.is_empty()),
            );
        }
        entry.static_templates.extend(template_set);
        entry.static_templates.sort();
    }

    fn profiler_index_paths(&self, index_override: &str) -> Result<Vec<String>> {
        let index_override = index_override.trim();
        if !index_override.is_empty() {
            let mut path = index_override.to_owned();
            if !filepath::is_abs(&path) {
                path = filepath::join(&[self.root.as_str(), path.as_str()]);
            }
            let path = secure_workspace_path(&self.root, &path)
                .map_err(|error| errorf!("invalid profiler index path: {error}"))?;
            let metadata = std::fs::metadata(&path)
                .map_err(|source| ProfilerIndexError::new("stat", &path, source))?;
            if !metadata.is_file() {
                return Err(errorf!("profiler index {path} is not a file"));
            }
            return Ok(vec![path]);
        }

        let mut paths = Vec::new();
        for base in ["var", "app"] {
            let cache = filepath::join(&[self.root.as_str(), base, "cache"]);
            for candidate in glob_profiler_indexes(&cache) {
                let path = secure_workspace_path(&self.root, &candidate)
                    .map_err(|error| errorf!("invalid discovered profiler index path: {error}"))?;
                paths.push(path);
            }
        }
        paths.sort();
        Ok(paths)
    }
}

impl CommandProvider for ProfilerCatalogProvider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let provider = self.clone();
        let command: CommandFunc = Arc::new(move |ctx, args| provider.list(ctx, args));
        BTreeMap::from([(LIST_PROFILER_REQUESTS_COMMAND.to_owned(), command)])
    }
}

/// The `<cache>/*/profiler/index.csv` files, sorted by directory name.
fn glob_profiler_indexes(cache: &str) -> Vec<String> {
    let Ok(metadata) = std::fs::metadata(cache) else {
        return Vec::new();
    };
    if !metadata.is_dir() {
        return Vec::new();
    }
    let Ok(entries) = std::fs::read_dir(cache) else {
        return Vec::new();
    };
    let mut names: Vec<String> = entries
        .filter_map(|entry| entry.ok())
        .map(|entry| entry.file_name().to_string_lossy().into_owned())
        .collect();
    names.sort();
    let mut result = Vec::new();
    for name in names {
        let profiler = filepath::join(&[cache, name.as_str(), "profiler"]);
        if !std::fs::metadata(&profiler).is_ok_and(|metadata| metadata.is_dir()) {
            continue;
        }
        let index = filepath::join(&[profiler.as_str(), "index.csv"]);
        if std::fs::symlink_metadata(&index).is_ok() {
            result.push(index);
        }
    }
    result
}

struct ProfilerRow {
    entry: ProfilerRequestCatalogEntry,
    profiler_directory: String,
    index_modified: i128,
    row_number: usize,
}

fn read_profiler_rows(ctx: &Context, index_paths: &[String]) -> Result<Vec<ProfilerRow>> {
    let mut rows = Vec::new();
    for index_path in index_paths {
        check(ctx)?;
        let metadata = std::fs::metadata(index_path)
            .map_err(|source| ProfilerIndexError::new("stat", index_path, source))?;
        if metadata.len() > MAX_PROFILER_INDEX_BYTES {
            return Err(errorf!(
                "profiler index {index_path} exceeds the {} MiB limit",
                MAX_PROFILER_INDEX_BYTES >> 20
            ));
        }
        let modified = metadata
            .modified()
            .ok()
            .and_then(|time| time.duration_since(std::time::UNIX_EPOCH).ok())
            .map_or(0, |duration| duration.as_nanos() as i128);
        let file = std::fs::File::open(index_path)
            .map_err(|source| ProfilerIndexError::new("open", index_path, source))?;
        let mut content = Vec::new();
        file.take(MAX_PROFILER_INDEX_BYTES + 1)
            .read_to_end(&mut content)
            .map_err(|source| ProfilerIndexError::new("read", index_path, source))?;
        let index_file_uri = swlsp_uriutil::file_uri(index_path);
        let profiler_directory = filepath::dir(index_path);
        let mut valid = Vec::new();
        let mut rest = &content[..];
        let mut row_number = 0usize;
        while !rest.is_empty() {
            let (line, next) = match rest.iter().position(|&byte| byte == b'\n') {
                Some(index) => (&rest[..index], &rest[index + 1..]),
                None => (rest, &b""[..]),
            };
            if line.len() >= MAX_PROFILER_INDEX_ROW_BYTES {
                return Err(errorf!(
                    "read profiler index {index_path}: bufio.Scanner: token too long"
                ));
            }
            rest = next;
            let current_row = row_number;
            row_number += 1;
            let line = utf8_lossy_per_byte(line);
            let line = line.trim();
            if line.is_empty() {
                continue;
            }
            let Some(record) = parse_csv_record(line) else {
                continue;
            };
            if record.len() < 4 {
                continue;
            }
            let hash = record[0].trim();
            let hash = hash.strip_prefix('\u{FEFF}').unwrap_or(hash);
            if !is_profiler_hash(hash) {
                continue;
            }
            let mut entry = ProfilerRequestCatalogEntry {
                hash: hash.to_owned(),
                method: record[2].trim().to_owned(),
                url: record[3].trim().to_owned(),
                index_file_uri: index_file_uri.clone(),
                ..ProfilerRequestCatalogEntry::default()
            };
            if record.len() > 4 {
                entry.timestamp = parse_decimal_saturating(record[4].trim());
            }
            if record.len() > 6 {
                entry.status_code = parse_decimal_saturating(record[6].trim());
            }
            valid.push(ProfilerRow {
                entry,
                profiler_directory: profiler_directory.clone(),
                index_modified: modified,
                row_number: current_row,
            });
        }
        let skip = valid.len().saturating_sub(MAX_PROFILER_SCAN_ROWS);
        rows.extend(valid.into_iter().skip(skip));
    }
    rows.sort_by(|left, right| {
        right
            .entry
            .timestamp
            .cmp(&left.entry.timestamp)
            .then_with(|| right.index_modified.cmp(&left.index_modified))
            .then_with(|| left.entry.index_file_uri.cmp(&right.entry.index_file_uri))
            .then_with(|| right.row_number.cmp(&left.row_number))
    });
    Ok(rows)
}

fn is_profiler_hash(hash: &str) -> bool {
    (6..=64).contains(&hash.len()) && hash.bytes().all(|byte| byte.is_ascii_hexdigit())
}

/// One CSV record read from a single line without its newline; `None` when
/// the line is not valid CSV. Fields may be quoted (`""` escapes a quote);
/// a bare quote inside an unquoted field is an error, and records may have
/// any number of fields.
pub fn parse_csv_record(line: &str) -> Option<Vec<String>> {
    let mut fields = Vec::new();
    let mut rest = line;
    loop {
        if !rest.starts_with('"') {
            let (field, next) = match rest.find(',') {
                Some(index) => (&rest[..index], Some(&rest[index + 1..])),
                None => (rest, None),
            };
            if field.contains('"') {
                return None;
            }
            fields.push(field.to_owned());
            match next {
                Some(next) => {
                    rest = next;
                    continue;
                }
                None => return Some(fields),
            }
        }
        rest = &rest[1..];
        let mut field = String::new();
        loop {
            let index = rest.find('"')?;
            field.push_str(&rest[..index]);
            rest = &rest[index + 1..];
            if let Some(next) = rest.strip_prefix('"') {
                field.push('"');
                rest = next;
            } else if let Some(next) = rest.strip_prefix(',') {
                fields.push(field);
                rest = next;
                break;
            } else if rest.is_empty() {
                fields.push(field);
                return Some(fields);
            } else {
                return None;
            }
        }
    }
}

/// A decimal integer with an optional sign; invalid text yields 0 and
/// out-of-range values saturate at the `i64` bounds.
pub fn parse_decimal_saturating(value: &str) -> i64 {
    match value.parse::<i64>() {
        Ok(number) => number,
        Err(error) => match error.kind() {
            IntErrorKind::PosOverflow => i64::MAX,
            IntErrorKind::NegOverflow => i64::MIN,
            _ => 0,
        },
    }
}

fn read_profiler_content(directory: &str, hash: &str) -> Option<Vec<u8>> {
    if !is_profiler_hash(hash) {
        return None;
    }
    let directory = filepath::abs(directory).ok()?;
    let path = filepath::join(&[directory.as_str(), &hash[4..6], &hash[2..4], hash]);
    let path = secure_workspace_path(&directory, &path).ok()?;
    let metadata = std::fs::metadata(&path).ok()?;
    if !metadata.is_file() || metadata.len() > MAX_PROFILER_CONTENT_BYTES {
        return None;
    }
    let mut file = std::fs::File::open(&path).ok()?;
    let mut header = [0u8; 3];
    let mut count = 0;
    while count < 3 {
        match file.read(&mut header[count..]) {
            Ok(0) => break,
            Ok(read) => count += read,
            Err(_) => return None,
        }
    }
    let mut prefix = header[..count].to_vec();
    let compressed = count == 3 && header == [0x1f, 0x8b, 0x08];
    let mut content = Vec::new();
    let limit = MAX_PROFILER_CONTENT_BYTES + 1;
    if compressed {
        let reader = std::io::Cursor::new(prefix).chain(file);
        flate2::read::MultiGzDecoder::new(reader)
            .take(limit)
            .read_to_end(&mut content)
            .ok()?;
    } else {
        file.take(limit - prefix.len() as u64)
            .read_to_end(&mut content)
            .ok()?;
        prefix.extend(content);
        content = prefix;
    }
    if content.len() as u64 > MAX_PROFILER_CONTENT_BYTES {
        return None;
    }
    Some(content)
}

fn find_bytes(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    if needle.is_empty() {
        return Some(0);
    }
    haystack
        .windows(needle.len())
        .position(|window| window == needle)
}

/// `bytes` as text where every byte of an invalid UTF-8 sequence becomes
/// U+FFFD (unlike [`String::from_utf8_lossy`], which replaces each maximal
/// invalid sequence once), so the replacement count matches the bytes.
pub fn utf8_lossy_per_byte(bytes: &[u8]) -> String {
    let mut result = String::with_capacity(bytes.len());
    let mut rest = bytes;
    loop {
        match std::str::from_utf8(rest) {
            Ok(valid) => {
                result.push_str(valid);
                return result;
            }
            Err(error) => {
                let valid = error.valid_up_to();
                // The prefix was validated by `from_utf8`.
                result.push_str(std::str::from_utf8(&rest[..valid]).unwrap_or_default());
                let invalid = error.error_len().unwrap_or(rest.len() - valid);
                for _ in 0..invalid {
                    result.push('\u{FFFD}');
                }
                rest = &rest[valid + invalid..];
            }
        }
    }
}

/// The PHP-serialized string after `"<key>";s:<n>:"`: exactly `<n>` bytes,
/// or an empty string when the marker or length is missing or invalid.
pub fn profiler_serialized_string(content: &[u8], key: &str) -> String {
    let marker = [key.as_bytes(), b"\";s:"].concat();
    let Some(offset) = find_bytes(content, &marker) else {
        return String::new();
    };
    let length_start = offset + marker.len();
    let mut offset = length_start;
    while offset < content.len() && content[offset].is_ascii_digit() {
        offset += 1;
    }
    if offset == length_start
        || offset + 2 > content.len()
        || content[offset] != b':'
        || content[offset + 1] != b'"'
    {
        return String::new();
    }
    let Some(length) = std::str::from_utf8(&content[length_start..offset])
        .ok()
        .and_then(|digits| digits.parse::<usize>().ok())
    else {
        return String::new();
    };
    let start = offset + 2;
    let Some(end) = start
        .checked_add(length)
        .filter(|end| *end <= content.len())
    else {
        return String::new();
    };
    utf8_lossy_per_byte(&content[start..end])
}

pub fn profiler_rendered_templates(content: &[u8]) -> Vec<String> {
    if let Some(offset) = find_bytes(content, b"\"template_paths\"") {
        let mut segment = &content[offset..];
        if let Some(end) = segment.iter().position(|&byte| byte == 0) {
            segment = &segment[..end];
        }
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        for captures in TEMPLATE_PAIR_PATTERN.captures_iter(segment) {
            let template = utf8_lossy_per_byte(&captures[1]).trim().to_owned();
            let path = utf8_lossy_per_byte(&captures[2]);
            if template.is_empty() || path.trim().is_empty() {
                continue;
            }
            if !seen.insert(template.clone()) {
                continue;
            }
            result.push(template);
        }
        if !result.is_empty() {
            return result;
        }
    }
    let mut result = Vec::new();
    for captures in FALLBACK_TEMPLATE_PATTERN.captures_iter(content) {
        let template = utf8_lossy_per_byte(&captures[1]).trim().to_owned();
        if !template.is_empty() {
            result.push(template);
        }
    }
    if !result.is_empty() {
        let mut seen = HashSet::new();
        result.retain(|value| seen.insert(value.clone()));
        return result;
    }
    if let Some(captures) = LEGACY_TEMPLATE_PATTERN.captures(content) {
        return vec![utf8_lossy_per_byte(&captures[1])];
    }
    Vec::new()
}

pub fn profiler_entry_view(content: &[u8], templates: &[String]) -> String {
    if let Some(template) = templates
        .iter()
        .find(|template| !template.starts_with("@WebProfiler"))
    {
        return template.clone();
    }
    if let Some(first) = templates.first() {
        return first.clone();
    }
    if let Some(captures) = LEGACY_TEMPLATE_PATTERN.captures(content) {
        return utf8_lossy_per_byte(&captures[1]);
    }
    String::new()
}

pub fn profiler_mail_messages(content: &[u8]) -> Vec<ProfilerMailMessage> {
    const MAX_MAIL_MESSAGES: usize = 25;
    const NAME_MARKER: &[u8] = b"AbstractHeader\x00name\";s:";
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    let mut offset = 0;
    while offset < content.len() && result.len() < MAX_MAIL_MESSAGES {
        let Some(relative) = find_bytes(&content[offset..], NAME_MARKER) else {
            break;
        };
        let start = offset + relative + NAME_MARKER.len();
        let Some((name, end)) = profiler_length_prefixed_string(content, start) else {
            offset = start;
            continue;
        };
        offset = end;
        if !swlsp_textutil::eq_ignore_case(utf8_lossy_per_byte(name).trim(), "Subject") {
            continue;
        }
        let mut header = &content[end..];
        if let Some(closing) = header.iter().position(|&byte| byte == b'}') {
            header = &header[..closing];
        }
        let title = profiler_serialized_string(header, "UnstructuredHeader\x00value");
        let title = title.trim();
        if title.is_empty() {
            continue;
        }
        let title: String = title.chars().take(512).collect();
        if !seen.insert(lowercase(&title) + "\x00mailer") {
            continue;
        }
        result.push(ProfilerMailMessage {
            title,
            panel: "mailer".to_owned(),
        });
    }
    result
}

fn profiler_length_prefixed_string(content: &[u8], offset: usize) -> Option<(&[u8], usize)> {
    let length_start = offset;
    let mut offset = offset;
    while offset < content.len() && content[offset].is_ascii_digit() {
        offset += 1;
    }
    if offset == length_start
        || offset + 2 > content.len()
        || content[offset] != b':'
        || content[offset + 1] != b'"'
    {
        return None;
    }
    let length = std::str::from_utf8(&content[length_start..offset])
        .ok()?
        .parse::<usize>()
        .ok()?;
    let start = offset + 2;
    let end = start.checked_add(length)?;
    if end.checked_add(2)? > content.len() || content[end] != b'"' || content[end + 1] != b';' {
        return None;
    }
    Some((&content[start..end], end + 2))
}

pub fn split_profiler_controller(controller: &str) -> Option<(String, String)> {
    let controller = controller.strip_prefix('\\').unwrap_or(controller).trim();
    let separator = controller.rfind("::")?;
    if separator == 0 || separator + 2 >= controller.len() {
        return None;
    }
    let class_name = controller[..separator].trim();
    let method_name = controller[separator + 2..].trim();
    if class_name.is_empty() || method_name.is_empty() {
        return None;
    }
    Some((class_name.to_owned(), method_name.to_owned()))
}

pub fn normalized_profiler_base_url(value: &str) -> Result<String> {
    let value = value.trim();
    if value.is_empty() {
        return Ok(String::new());
    }
    let Some(parsed) = Url::parse(value).ok().filter(has_host) else {
        return Err(errorf!("profiler baseUrl must be an absolute HTTP(S) URL"));
    };
    if !is_http(&parsed) {
        return Err(errorf!("profiler baseUrl must use HTTP or HTTPS"));
    }
    if !parsed.username().is_empty() || parsed.password().is_some() {
        return Err(errorf!("profiler baseUrl must not contain credentials"));
    }
    // Without query and fragment; trailing slashes go so that
    // `{base}/_profiler/...` has exactly one separator.
    Ok(format!(
        "{}{}",
        &parsed[..Position::BeforePath],
        parsed.path().trim_end_matches('/')
    ))
}

fn has_host(url: &Url) -> bool {
    url.host_str().is_some_and(|host| !host.is_empty())
}

fn is_http(url: &Url) -> bool {
    matches!(url.scheme(), "http" | "https")
}

pub fn profiler_url(base_url: &str, request_url: &str, hash: &str) -> String {
    if !base_url.is_empty() {
        return format!("{base_url}/_profiler/{hash}");
    }
    let Some(mut parsed) = Url::parse(request_url)
        .ok()
        .filter(|url| is_http(url) && has_host(url))
    else {
        return format!("_profiler/{hash}");
    };
    let path = parsed.path();
    let prefix = FRONT_CONTROLLER_PATTERN
        .find(path.as_bytes())
        .map_or("", |found| &path[..found.end()]);
    let path = format!("{}/_profiler/{hash}", prefix.trim_end_matches('/'));
    parsed.set_path(&path);
    parsed.set_query(None);
    parsed.set_fragment(None);
    parsed.into()
}

/// A profiler index that cannot be read.
#[derive(Debug, thiserror::Error)]
#[error("cannot {operation} profiler index {path}: {source}")]
pub struct ProfilerIndexError {
    operation: &'static str,
    path: String,
    #[source]
    source: std::io::Error,
}

impl ProfilerIndexError {
    fn new(operation: &'static str, path: &str, source: std::io::Error) -> Self {
        Self {
            operation,
            path: path.to_owned(),
            source,
        }
    }
}

/// A path that is not safely inside the workspace.
#[derive(Debug, thiserror::Error)]
pub enum WorkspacePathError {
    #[error("cannot resolve {path}: {source}")]
    Resolve {
        path: String,
        #[source]
        source: std::io::Error,
    },
    #[error("cannot relate {path} to the workspace")]
    Relate {
        path: String,
        #[source]
        source: swlsp_uriutil::filepath::RelError,
    },
    #[error("path {path} is outside workspace {root}")]
    Outside { path: String, root: String },
    #[error("path {path} resolves outside workspace {root}")]
    ResolvesOutside { path: String, root: String },
}

/// `candidate` as a clean absolute path, rejected when it is, lexically or
/// after resolving symlinks, outside `root`.
pub fn secure_workspace_path(root: &str, candidate: &str) -> Result<String, WorkspacePathError> {
    let absolute = |path: &str| {
        filepath::abs(path).map_err(|source| WorkspacePathError::Resolve {
            path: path.to_owned(),
            source,
        })
    };
    let relate = |base: &str, target: &str| {
        filepath::rel(base, target).map_err(|source| WorkspacePathError::Relate {
            path: candidate.to_owned(),
            source,
        })
    };
    let absolute_root = absolute(root)?;
    let absolute_candidate = absolute(candidate)?;
    let outside = |relative: &str| relative == ".." || relative.starts_with("../");
    if outside(&relate(&absolute_root, &absolute_candidate)?) {
        return Err(WorkspacePathError::Outside {
            path: candidate.to_owned(),
            root: root.to_owned(),
        });
    }
    if let Ok(evaluated_root) = std::fs::canonicalize(&absolute_root)
        && let Ok(evaluated_candidate) = std::fs::canonicalize(&absolute_candidate)
        && outside(&relate(
            &evaluated_root.to_string_lossy(),
            &evaluated_candidate.to_string_lossy(),
        )?)
    {
        return Err(WorkspacePathError::ResolvesOutside {
            path: candidate.to_owned(),
            root: root.to_owned(),
        });
    }
    Ok(filepath::clean(&absolute_candidate))
}

#[cfg(test)]
pub mod testing {
    pub use super::{
        parse_csv_record, parse_decimal_saturating, profiler_mail_messages,
        profiler_serialized_string, profiler_url, utf8_lossy_per_byte,
    };
}
