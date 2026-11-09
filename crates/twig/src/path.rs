//! Template names and loader roots.

/// The compiled storefront asset directory Shopware's
/// `TwigLoaderConfigCompilerPass` registers as an extra path of the owning
/// bundle's Twig namespace, alongside `Resources/views`.
const STOREFRONT_DIST_MARKER: &str = "/Resources/app/storefront/dist/";

/// The directories a Twig loader is pointed at. Anything below one of them is
/// addressable as a template.
const TEMPLATE_ROOT_MARKERS: [&str; 3] =
    ["/Resources/views/", "/templates/", STOREFRONT_DIST_MARKER];

pub const VERSION_COMMENT_PREFIX: &str = "shopware-block:";

/// `path` with `\` separators replaced by `/` on Windows; unchanged
/// elsewhere.
pub(crate) fn to_slash(path: &str) -> std::borrow::Cow<'_, str> {
    if cfg!(windows) && path.contains('\\') {
        std::borrow::Cow::Owned(path.replace('\\', "/"))
    } else {
        std::borrow::Cow::Borrowed(path)
    }
}

/// The namespace-relative name of a file below a bundle's storefront dist
/// root, without the bundle namespace itself.
pub(crate) fn storefront_dist_relative(normalized: &str) -> Option<&str> {
    let index = normalized.rfind(STOREFRONT_DIST_MARKER)?;
    Some(&normalized[index + STOREFRONT_DIST_MARKER.len()..])
}

/// Whether a directory is a storefront dist root or sits below one. The
/// scanner skips every `dist` directory, so the Twig indexer uses this to
/// reopen only the dist roots a loader serves.
pub fn is_storefront_dist_directory(directory: &str) -> bool {
    let normalized = to_slash(directory);
    normalized.ends_with(STOREFRONT_DIST_MARKER.trim_end_matches('/'))
        || normalized.contains(STOREFRONT_DIST_MARKER)
}

/// Whether a file lives under a template root while carrying an extension
/// other than `.twig`. Twig loaders serve whatever sits below their paths, so
/// whether a template exists must not depend on it being parseable Twig.
pub fn is_template_asset_path(template_path: &str) -> bool {
    let normalized = to_slash(template_path);
    let extension = ext(&normalized);
    // An extension-less entry is a directory, a symlink to one, or a tooling
    // file; reading one as a template fails the whole index run.
    if extension.is_empty()
        || extension.eq_ignore_ascii_case(".twig")
        || base(&normalized).starts_with('.')
        || is_excluded_twig_path(&normalized)
    {
        return false;
    }
    TEMPLATE_ROOT_MARKERS
        .iter()
        .any(|marker| normalized.contains(marker))
}

pub fn convert_to_relative_path(twig_path: &str) -> String {
    let normalized = to_slash(twig_path);
    if let Some(relative) = storefront_dist_relative(&normalized) {
        if relative.is_empty() {
            return String::new();
        }
        let bundle = bundle_name_by_path(&normalized);
        if !bundle.is_empty() && bundle != "unknown" {
            return format!("@{bundle}/{relative}");
        }
        return String::new();
    }

    if let Some(index) = normalized.find("Resources/views") {
        let rest = &normalized[index..];
        let rest = rest.strip_prefix("Resources/views").unwrap_or(rest);
        let path = rest.strip_prefix('/').unwrap_or(rest);
        if path.is_empty() {
            return String::new();
        }
        return format!("@Storefront/{path}");
    }

    // Files below no loader root have no portable template name.
    if is_absolute_path(&normalized) {
        return String::new();
    }
    let path = normalized.strip_prefix('/').unwrap_or(&normalized);
    if path.is_empty() {
        return String::new();
    }
    format!("@Storefront/{path}")
}

fn is_absolute_path(normalized: &str) -> bool {
    if normalized.starts_with('/') {
        return true;
    }
    let bytes = normalized.as_bytes();
    bytes.len() >= 3 && bytes[1] == b':' && bytes[2] == b'/' && bytes[0].is_ascii_alphabetic()
}

/// Whether a file uses Twig syntax without being a template the Symfony
/// loader can resolve: administration sources, migration fixtures, and
/// phpDocumentor's own templates.
pub fn is_excluded_twig_path(template_path: &str) -> bool {
    let normalized = to_slash(template_path);
    normalized.contains("Resources/app/administration")
        || normalized.contains("Migration/Fixtures")
        || normalized.contains(".phpdoc/template")
}

/// The portable names by which Twig can address a file: Shopware's
/// `@Storefront` convention, standard Symfony `templates/` paths and bundle
/// namespaces.
pub fn template_names(twig_path: &str) -> Vec<String> {
    let normalized = to_slash(twig_path);
    let mut names: Vec<String> = Vec::new();
    fn push(names: &mut Vec<String>, name: &str) {
        let name = name.strip_prefix('/').unwrap_or(name);
        if !name.is_empty() && !names.iter().any(|existing| existing == name) {
            names.push(name.to_owned());
        }
    }
    macro_rules! add {
        ($name:expr) => {
            push(&mut names, $name)
        };
    }

    // The dist root is registered only under its bundle namespace.
    if let Some(relative) = storefront_dist_relative(&normalized) {
        if !relative.is_empty() {
            let bundle = bundle_name_by_path(&normalized);
            if !bundle.is_empty() && bundle != "unknown" {
                add!(&format!("@{bundle}/{relative}"));
            }
        }
        return names;
    }

    let marker = "/Resources/views/";
    if let Some(index) = normalized.rfind(marker) {
        let relative = &normalized[index + marker.len()..];
        add!(relative);
        add!(&convert_to_relative_path(&normalized));
        let bundle = bundle_name_by_path(&normalized);
        if !bundle.is_empty() && bundle != "unknown" {
            add!(&format!("@{bundle}/{relative}"));
            add!(&legacy_bundle_template_name(&bundle, relative));
        }
    }
    let marker = "/templates/";
    if let Some(index) = normalized.rfind(marker) {
        add!(&normalized[index + marker.len()..]);
    }
    if names.is_empty() {
        add!(&convert_to_relative_path(&normalized));
    }
    names
}

fn legacy_bundle_template_name(bundle: &str, relative: &str) -> String {
    let relative = relative.strip_prefix('/').unwrap_or(relative);
    let directory = dir(relative);
    let file = base(relative);
    if directory == "." || directory.is_empty() {
        return format!("{bundle}::{file}");
    }
    format!("{bundle}:{directory}:{file}")
}

pub(crate) fn bundle_name_by_path(twig_path: &str) -> String {
    let normalized = to_slash(twig_path);
    for marker in ["/Resources/views/", STOREFRONT_DIST_MARKER] {
        if let Some(index) = normalized.find(marker) {
            return bundle_name_before(&normalized, index);
        }
    }
    "unknown".to_owned()
}

/// The bundle, plugin, app, or theme name owning a Resources root: the
/// directory directly above `Resources`, or the one above `src`.
fn bundle_name_before(normalized: &str, index: usize) -> String {
    let possible = normalized[..index].trim_matches('/');
    if base(possible) == "src" {
        return base(&dir(possible)).to_owned();
    }
    base(possible).to_owned()
}

pub fn is_storefront_template(uri: &str) -> bool {
    let uri = to_slash(uri);
    uri.contains("/src/Storefront/Resources/views/storefront/")
        || uri.contains("/vendor/shopware/storefront/Resources/views/storefront/")
}

pub fn is_upstream_template(uri: &str) -> bool {
    let normalized = to_slash(uri);
    is_storefront_template(uri)
        || normalized.contains("/vendor/") && normalized.contains("/Resources/views/")
}

pub fn format_version_comment(hash: &str, version: &str) -> String {
    let mut comment = format!("{{# {VERSION_COMMENT_PREFIX} {hash}");
    let version = version.trim();
    if !version.is_empty() {
        comment.push('@');
        comment.push_str(version);
    }
    comment.push_str(" #}\n");
    comment
}

// --- Lexical helpers for slash-separated paths ---

/// The extension of the last element including its dot; empty without one.
pub(crate) fn ext(path: &str) -> &str {
    for (index, byte) in path.bytes().enumerate().rev() {
        if byte == b'/' {
            break;
        }
        if byte == b'.' {
            return &path[index..];
        }
    }
    ""
}

/// The last element, ignoring trailing slashes; `.` for an empty path and
/// `/` for the root.
pub(crate) fn base(path: &str) -> &str {
    if path.is_empty() {
        return ".";
    }
    let trimmed = path.trim_end_matches('/');
    if trimmed.is_empty() {
        return "/";
    }
    match trimmed.rfind('/') {
        Some(index) => &trimmed[index + 1..],
        None => trimmed,
    }
}

/// The cleaned directory part; `.` without one.
pub(crate) fn dir(path: &str) -> String {
    let directory = match path.rfind('/') {
        Some(index) => &path[..=index],
        None => "",
    };
    clean(directory)
}

/// The shortest lexically equivalent path: `.` and empty elements dropped,
/// `..` resolved where possible; `.` for an empty result.
pub(crate) fn clean(path: &str) -> String {
    if path.is_empty() {
        return ".".to_owned();
    }
    let rooted = path.starts_with('/');
    let mut parts: Vec<&str> = Vec::new();
    for part in path.split('/') {
        match part {
            "" | "." => {}
            ".." => {
                if parts.last().is_some_and(|last| *last != "..") {
                    parts.pop();
                } else if !rooted {
                    parts.push("..");
                }
            }
            other => parts.push(other),
        }
    }
    let joined = parts.join("/");
    if rooted {
        format!("/{joined}")
    } else if joined.is_empty() {
        ".".to_owned()
    } else {
        joined
    }
}

pub(crate) fn same_path(left: &str, right: &str) -> bool {
    clean(left) == clean(right)
}
