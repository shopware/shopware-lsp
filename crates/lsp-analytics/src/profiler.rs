//! Local Symfony profiler request catalog (port of `profiler_catalog.go`).

use std::collections::{BTreeMap, BTreeSet, HashSet};
use std::io::Read;
use std::sync::{Arc, LazyLock};

use regex::bytes::Regex;
use serde::Serialize;
use serde_json::Value;
use swlsp_lsp::{CommandFunc, CommandProvider, Context};
use swlsp_php::PhpIndex;
use swlsp_twig::TwigIndexer;
use swlsp_uriutil::filepath;
use url::{Position, Url};

use crate::profiler_serialized::profiler_twig_components;
use crate::request::{Field, decode, present};
use crate::{
    Result, RouteTemplateResolver, SourceLineResolver, check, errorf, is_zero, lower, range_len,
    range_start, trim_space,
};

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
    fn decode(value: &Value) -> Result<Self, String> {
        let members = decode(
            value,
            "ProfilerRequestCatalogRequest",
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
        let url_filter = lower(trim_space(&request.url));
        let hash_filter = lower(trim_space(&request.hash));
        let controller_filter = lower(trim_space(&request.controller));
        let route_filter = lower(trim_space(&request.route));
        let mut templates = RouteTemplateResolver::new(self.php.as_ref(), self.twig.as_deref());
        let mut lines = SourceLineResolver::default();
        let mut result = Vec::new();
        let mut seen: HashSet<String> = HashSet::with_capacity(rows.len());
        for row in rows {
            check(ctx)?;
            if !seen.insert(lower(&row.entry.hash)) {
                continue;
            }
            if !url_filter.is_empty() && !lower(&row.entry.url).contains(&url_filter) {
                continue;
            }
            if !hash_filter.is_empty() && !lower(&row.entry.hash).contains(&hash_filter) {
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
                && !lower(&entry.controller).contains(&controller_filter)
            {
                continue;
            }
            if !route_filter.is_empty() && !lower(&entry.route).contains(&route_filter) {
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
                && !swlsp_php_types::equal_fold(&component.class, ANONYMOUS_COMPONENT)
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
        let index_override = trim_space(index_override);
        if !index_override.is_empty() {
            let mut path = index_override.to_owned();
            if !filepath::is_abs(&path) {
                path = filepath::join(&[self.root.as_str(), path.as_str()]);
            }
            let path = secure_workspace_path(&self.root, &path)
                .map_err(|error| errorf!("invalid profiler index path: {error}"))?;
            let metadata = std::fs::metadata(&path).map_err(|error| {
                errorf!(
                    "read profiler index {path}: {}",
                    go_path_error("stat", &path, &error)
                )
            })?;
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

/// Go `filepath.Glob(<cache>/*/profiler/index.csv)`.
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
        let metadata = std::fs::metadata(index_path).map_err(|error| {
            errorf!(
                "read profiler index {index_path}: {}",
                go_path_error("stat", index_path, &error)
            )
        })?;
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
        let file = std::fs::File::open(index_path).map_err(|error| {
            errorf!(
                "open profiler index {index_path}: {}",
                go_path_error("open", index_path, &error)
            )
        })?;
        let mut content = Vec::new();
        file.take(MAX_PROFILER_INDEX_BYTES + 1)
            .read_to_end(&mut content)
            .map_err(|error| {
                errorf!(
                    "read profiler index {index_path}: {}",
                    go_path_error("read", index_path, &error)
                )
            })?;
        let index_file_uri = swlsp_uriutil::file_uri(index_path);
        let profiler_directory = go_dir(index_path);
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
            let line = go_lossy(line);
            let line = trim_space(&line);
            if line.is_empty() {
                continue;
            }
            let Some(record) = parse_csv_record(line) else {
                continue;
            };
            if record.len() < 4 {
                continue;
            }
            let hash = trim_space(&record[0]);
            let hash = hash.strip_prefix('\u{FEFF}').unwrap_or(hash);
            if !is_profiler_hash(hash) {
                continue;
            }
            let mut entry = ProfilerRequestCatalogEntry {
                hash: hash.to_owned(),
                method: trim_space(&record[2]).to_owned(),
                url: trim_space(&record[3]).to_owned(),
                index_file_uri: index_file_uri.clone(),
                ..ProfilerRequestCatalogEntry::default()
            };
            if record.len() > 4 {
                entry.timestamp = go_parse_int(trim_space(&record[4]));
            }
            if record.len() > 6 {
                entry.status_code = go_parse_int(trim_space(&record[6]));
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

/// Go `filepath.Dir` on Unix paths.
fn go_dir(path: &str) -> String {
    match path.rfind('/') {
        Some(index) => filepath::clean(&path[..=index]),
        None => ".".to_owned(),
    }
}

fn is_profiler_hash(hash: &str) -> bool {
    (6..=64).contains(&hash.len()) && hash.bytes().all(|byte| byte.is_ascii_hexdigit())
}

/// One record of Go `encoding/csv` (default options, `FieldsPerRecord =
/// -1`) read from a single line without its newline; `None` on a parse
/// error.
pub(crate) fn parse_csv_record(line: &str) -> Option<Vec<String>> {
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

/// Go `strconv.ParseInt(value, 10, 64)` ignoring the error: syntax errors
/// yield 0 and out-of-range values clamp.
pub(crate) fn go_parse_int(value: &str) -> i64 {
    let (negative, digits) = match value.as_bytes().first() {
        Some(b'+') => (false, &value[1..]),
        Some(b'-') => (true, &value[1..]),
        _ => (false, value),
    };
    if digits.is_empty() || !digits.bytes().all(|byte| byte.is_ascii_digit()) {
        return 0;
    }
    let mut magnitude: u128 = 0;
    for byte in digits.bytes() {
        magnitude = (magnitude * 10 + u128::from(byte - b'0')).min(u128::from(u64::MAX) + 1);
    }
    if negative {
        if magnitude > i64::MAX as u128 + 1 {
            return i64::MIN;
        }
        (magnitude as i128).wrapping_neg() as i64
    } else if magnitude > i64::MAX as u128 {
        i64::MAX
    } else {
        magnitude as i64
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

/// Go string conversion of arbitrary bytes followed by JSON encoding: every
/// byte of an invalid UTF-8 sequence becomes U+FFFD.
pub(crate) fn go_lossy(bytes: &[u8]) -> String {
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
                // SAFETY-free: the prefix was validated by `from_utf8`.
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

/// Go `profilerLengthPrefixedString`-free lookup of `"<key>";s:<n>:"<value>`.
pub(crate) fn profiler_serialized_string(content: &[u8], key: &str) -> String {
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
    go_lossy(&content[start..end])
}

pub(crate) fn profiler_rendered_templates(content: &[u8]) -> Vec<String> {
    if let Some(offset) = find_bytes(content, b"\"template_paths\"") {
        let mut segment = &content[offset..];
        if let Some(end) = segment.iter().position(|&byte| byte == 0) {
            segment = &segment[..end];
        }
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        for captures in TEMPLATE_PAIR_PATTERN.captures_iter(segment) {
            let template = trim_space(&go_lossy(&captures[1])).to_owned();
            let path = go_lossy(&captures[2]);
            if template.is_empty() || trim_space(&path).is_empty() {
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
        let template = trim_space(&go_lossy(&captures[1])).to_owned();
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
        return vec![go_lossy(&captures[1])];
    }
    Vec::new()
}

pub(crate) fn profiler_entry_view(content: &[u8], templates: &[String]) -> String {
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
        return go_lossy(&captures[1]);
    }
    String::new()
}

pub(crate) fn profiler_mail_messages(content: &[u8]) -> Vec<ProfilerMailMessage> {
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
        if !swlsp_php_types::equal_fold(trim_space(&go_lossy(name)), "Subject") {
            continue;
        }
        let mut header = &content[end..];
        if let Some(closing) = header.iter().position(|&byte| byte == b'}') {
            header = &header[..closing];
        }
        let title = profiler_serialized_string(header, "UnstructuredHeader\x00value");
        let title = trim_space(&title);
        if title.is_empty() {
            continue;
        }
        let title: String = title.chars().take(512).collect();
        if !seen.insert(lower(&title) + "\x00mailer") {
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

pub(crate) fn split_profiler_controller(controller: &str) -> Option<(String, String)> {
    let controller = trim_space(controller.strip_prefix('\\').unwrap_or(controller));
    let separator = controller.rfind("::")?;
    if separator == 0 || separator + 2 >= controller.len() {
        return None;
    }
    let class_name = trim_space(&controller[..separator]);
    let method_name = trim_space(&controller[separator + 2..]);
    if class_name.is_empty() || method_name.is_empty() {
        return None;
    }
    Some((class_name.to_owned(), method_name.to_owned()))
}

pub(crate) fn normalized_profiler_base_url(value: &str) -> Result<String> {
    let value = trim_space(value);
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

pub(crate) fn profiler_url(base_url: &str, request_url: &str, hash: &str) -> String {
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

/// Go `*os.PathError` text for an I/O error.
fn go_path_error(op: &str, path: &str, error: &std::io::Error) -> String {
    let message = match error.raw_os_error() {
        Some(code) => {
            let text = std::io::Error::from_raw_os_error(code).to_string();
            let text = text
                .rsplit_once(" (os error ")
                .map_or(text.as_str(), |(head, _)| head)
                .to_owned();
            let mut characters = text.chars();
            match characters.next() {
                Some(first) => first.to_lowercase().chain(characters).collect(),
                None => text,
            }
        }
        None => error.to_string(),
    };
    format!("{op} {path}: {message}")
}

/// Go `secureWorkspacePath`: rejects `candidate` when it is, lexically or
/// after resolving symlinks, outside `root`.
pub(crate) fn secure_workspace_path(root: &str, candidate: &str) -> Result<String, String> {
    let absolute_root = filepath::abs(root).map_err(|error| error.to_string())?;
    let absolute_candidate = filepath::abs(candidate).map_err(|error| error.to_string())?;
    let outside = |relative: &str| relative == ".." || relative.starts_with("../");
    let relative = filepath::rel(&absolute_root, &absolute_candidate)?;
    if outside(&relative) {
        return Err(format!("path {candidate} is outside workspace {root}"));
    }
    if let Ok(evaluated_root) = std::fs::canonicalize(&absolute_root)
        && let Ok(evaluated_candidate) = std::fs::canonicalize(&absolute_candidate)
    {
        let relative = filepath::rel(
            &evaluated_root.to_string_lossy(),
            &evaluated_candidate.to_string_lossy(),
        )?;
        if outside(&relative) {
            return Err(format!(
                "path {candidate} resolves outside workspace {root}"
            ));
        }
    }
    Ok(filepath::clean(&absolute_candidate))
}

#[cfg(test)]
pub(crate) mod testing {
    pub(crate) use super::{
        go_lossy, go_parse_int, parse_csv_record, profiler_mail_messages,
        profiler_serialized_string, profiler_url,
    };
}
