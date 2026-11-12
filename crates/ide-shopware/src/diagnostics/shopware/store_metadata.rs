//! The composer.json metadata a Store release needs beyond the localized
//! texts: name, description, license, version, authors, plugin class,
//! autoloading and the extension icon.

use serde_json::{Map, Value};
use swlsp_lsp::Problem;
use swlsp_syntax::SyntaxNode;
use swlsp_uriutil::filepath;

use super::json_keys::nearest_key_range;
use super::store_icon::{IconIssue, icon_finding};
use super::store_license::is_valid_license;
use super::util::warning;

const DEFAULT_ICON: &str = "src/Resources/config/plugin.png";

pub(crate) fn metadata_problems(
    root: &SyntaxNode,
    composer: &Map<String, Value>,
    extension_root: &str,
) -> Vec<Problem> {
    let problem = |id: &str, path: &[&str], message: String| {
        warning(id, nearest_key_range(root, path), message)
    };
    let text = |key: &str| composer.get(key).and_then(Value::as_str).map(str::trim);
    let mut result = Vec::new();
    if text("name").is_none_or(str::is_empty) {
        result.push(problem(
            "shopware.store.name",
            &["name"],
            "Store: composer.json requires a name".to_owned(),
        ));
    }
    if text("description").is_none_or(str::is_empty) {
        result.push(problem(
            "shopware.store.composer-description",
            &["description"],
            "Store: composer.json requires a description".to_owned(),
        ));
    }
    if let Some(message) = license_problem(composer.get("license")) {
        result.push(problem("shopware.store.license", &["license"], message));
    }
    match text("version") {
        Some(version) if is_version(version) => {}
        Some(version) if !version.is_empty() => result.push(problem(
            "shopware.store.version",
            &["version"],
            format!("Store: version {version} is not a valid version number"),
        )),
        _ => result.push(problem(
            "shopware.store.version",
            &["version"],
            "Store: composer.json requires a version".to_owned(),
        )),
    }
    if !matches!(composer.get("authors"), Some(Value::Array(authors)) if !authors.is_empty()) {
        result.push(problem(
            "shopware.store.authors",
            &["authors"],
            "Store: composer.json requires authors".to_owned(),
        ));
    }
    let extra = composer.get("extra").and_then(Value::as_object);
    let extra_text = |key: &str| {
        extra
            .and_then(|extra| extra.get(key))
            .and_then(Value::as_str)
            .map(str::trim)
            .filter(|value| !value.is_empty())
    };
    if extra_text("shopware-plugin-class").is_none() {
        result.push(problem(
            "shopware.store.plugin-class",
            &["extra", "shopware-plugin-class"],
            "Store: extra.shopware-plugin-class is required".to_owned(),
        ));
    }
    let autoloads = |standard: &str| {
        matches!(
            composer.get("autoload").and_then(|autoload| autoload.get(standard)),
            Some(Value::Object(namespaces)) if !namespaces.is_empty()
        )
    };
    if !autoloads("psr-4") && !autoloads("psr-0") {
        result.push(problem(
            "shopware.store.autoload",
            &["autoload"],
            "Store: autoload requires psr-4 or psr-0 namespaces".to_owned(),
        ));
    }
    let icon = icon_path(composer);
    if let Some((issue, message)) = icon_finding(&filepath::join(&[extension_root, icon]), icon) {
        let code = match issue {
            IconIssue::Unusable => "shopware.store.icon",
            IconIssue::Size => "shopware.store.icon-size",
        };
        result.push(problem(code, &["extra", "plugin-icon"], message));
    }
    result
}

/// The icon the plugin names in `extra.plugin-icon`, or the default one;
/// relative to the extension root.
pub(crate) fn icon_path(composer: &Map<String, Value>) -> &str {
    composer
        .get("extra")
        .and_then(|extra| extra.get("plugin-icon"))
        .and_then(Value::as_str)
        .map(str::trim)
        .filter(|icon| !icon.is_empty())
        .unwrap_or(DEFAULT_ICON)
}

/// What is wrong with the `license` value: a string, or a list of
/// alternatives.
fn license_problem(license: Option<&Value>) -> Option<String> {
    let licenses: Vec<&str> = match license {
        Some(Value::String(license)) => vec![license.as_str()],
        Some(Value::Array(licenses)) => licenses.iter().filter_map(Value::as_str).collect(),
        _ => Vec::new(),
    };
    if licenses.iter().all(|license| license.trim().is_empty()) {
        return Some("Store: composer.json requires a license".to_owned());
    }
    licenses
        .into_iter()
        .find(|license| !is_valid_license(license))
        .map(|license| {
            format!("Store: license {license} is neither proprietary nor a valid SPDX license")
        })
}

/// `1`, `1.2`, `1.2.3` or `1.2.3.4`, optionally prefixed with `v` and
/// followed by a `-` or `+` suffix.
fn is_version(version: &str) -> bool {
    let version = version.strip_prefix('v').unwrap_or(version);
    let (numbers, suffix) = match version.find(['-', '+']) {
        Some(at) => (&version[..at], &version[at + 1..]),
        None => (version, "x"),
    };
    let parts: Vec<&str> = numbers.split('.').collect();
    !suffix.is_empty()
        && parts.len() <= 4
        && parts
            .iter()
            .all(|part| !part.is_empty() && part.bytes().all(|byte| byte.is_ascii_digit()))
}
