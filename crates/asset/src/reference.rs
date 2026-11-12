use swlsp_parser_php::query as php;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_parser_twig::ast::{AstNode, HtmlStartingTag, HtmlString};
use swlsp_parser_twig::query as twig;
use swlsp_parser_twig::syntax::{HTML_STRING, TWIG_LITERAL_STRING};
use swlsp_php::{PhpContext, PhpIndex};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};

use crate::model::{ReferenceKind, normalize_name};
use crate::parser::shrink;
use crate::paths::ext;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum HtmlAssetType {
    #[default]
    None,
    Css,
    JavaScript,
    Image,
}

/// An asset reference in PHP (`Packages::getUrl()`), Twig (`asset()`,
/// `importmap()`, Encore/Vite functions, Assetic tags) or a static HTML
/// `link`/`script`/`img` attribute in a Twig template.
#[derive(Clone, Debug)]
pub struct Reference {
    pub name: String,
    pub package: String,
    pub asset_name: String,
    pub assetic: bool,
    pub kind: ReferenceKind,
    pub node: SyntaxNode,
    pub container: SyntaxNode,
    pub html_type: HtmlAssetType,
}

impl Reference {
    fn new(name: String, kind: ReferenceKind, node: SyntaxNode, container: SyntaxNode) -> Self {
        Self {
            name,
            package: String::new(),
            asset_name: String::new(),
            assetic: false,
            kind,
            node,
            container,
            html_type: HtmlAssetType::None,
        }
    }
}

fn lower_ext(path: &str) -> String {
    ext(path).to_lowercase()
}

pub fn reference_at(path: &str, node: &SyntaxNode) -> Option<Reference> {
    match lower_ext(path).as_str() {
        ".php" => php_reference_at(node),
        ".twig" => twig_reference_at(node).or_else(|| twig_html_reference_at(node)),
        _ => None,
    }
}

pub fn references(path: &str, root: &SyntaxNode) -> Vec<Reference> {
    match lower_ext(path).as_str() {
        ".php" => php::nodes(root, &[PHP_STRING])
            .iter()
            .filter_map(php_reference_at)
            .collect(),
        ".twig" => twig::iterate_nodes(root, &[TWIG_LITERAL_STRING])
            .filter_map(|literal| twig_reference_at(&literal))
            .chain(
                twig::iterate_nodes(root, &[HTML_STRING])
                    .filter_map(|string| twig_html_reference_at(&string)),
            )
            .collect(),
        _ => Vec::new(),
    }
}

fn twig_html_reference_at(node: &SyntaxNode) -> Option<Reference> {
    let html_string = twig::closest_node_of_kind(node, &[HTML_STRING])?;
    let attribute = twig::html_attribute_at(&html_string)?;
    let start_tag = twig::starting_html_tag_at(&attribute)?;
    let tag_name = twig::html_tag_name(&start_tag)
        .unwrap_or_default()
        .to_lowercase();
    let attribute_name = twig::html_attribute_name(&attribute)
        .unwrap_or_default()
        .to_lowercase();
    let html_type = match (tag_name.as_str(), attribute_name.as_str()) {
        ("link", "href") if html_attribute_equals(&start_tag, "rel", "stylesheet") => {
            HtmlAssetType::Css
        }
        ("script", "src") => HtmlAssetType::JavaScript,
        ("img", "src") => HtmlAssetType::Image,
        _ => return None,
    };
    let value_node = HtmlString::cast(html_string.clone())
        .and_then(|string| string.inner())
        .map_or_else(|| html_string.clone(), |inner| inner.syntax().clone());
    let value = value_node.text().to_string();
    let value = value.trim();
    if !is_local_static_html_asset(value) {
        return None;
    }
    let mut reference = Reference::new(
        normalize_html_asset_name(value),
        ReferenceKind::Asset,
        value_node,
        html_string,
    );
    reference.html_type = html_type;
    Some(reference)
}

/// Whether the first attribute named `name` has the value `value`
/// (case-insensitive).
fn html_attribute_equals(start_tag: &SyntaxNode, name: &str, value: &str) -> bool {
    let Some(tag) = HtmlStartingTag::cast(start_tag.clone()) else {
        return false;
    };
    let Some(attribute) = tag.attributes().find(|attribute| {
        twig::html_attribute_name(attribute.syntax())
            .is_some_and(|attribute_name| attribute_name.eq_ignore_ascii_case(name))
    }) else {
        return false;
    };
    attribute
        .value()
        .and_then(|string| string.inner())
        .is_some_and(|inner| {
            swlsp_textutil::eq_ignore_case(inner.syntax().text().to_string().trim(), value)
        })
}

fn is_local_static_html_asset(value: &str) -> bool {
    let lower = value.trim().to_lowercase();
    !value.contains("{{")
        && !value.contains("{%")
        && !value.contains("{#")
        && !value.contains("://")
        && !value.contains(['?', '#'])
        && !value.starts_with("//")
        && !lower.starts_with("data:")
        && !lower.starts_with("mailto:")
        && !lower.starts_with("tel:")
}

fn normalize_html_asset_name(value: &str) -> String {
    let mut value = value.trim();
    while let Some(rest) = value.strip_prefix("./") {
        value = rest;
    }
    normalize_name(value)
}

/// Whether an asset name's extension fits the HTML context it is used in.
pub fn matches_html_asset_type(name: &str, asset_type: HtmlAssetType) -> bool {
    let extension = lower_ext(name);
    let accepted: &[&str] = match asset_type {
        HtmlAssetType::Css => &[".css", ".less", ".sass", ".scss"],
        HtmlAssetType::JavaScript => &[".js", ".mjs", ".coffee", ".dart"],
        HtmlAssetType::Image => &[
            ".avif", ".bmp", ".gif", ".ico", ".jpeg", ".jpg", ".png", ".svg", ".webp",
        ],
        HtmlAssetType::None => return true,
    };
    accepted.contains(&extension.as_str())
}

fn php_reference_at(node: &SyntaxNode) -> Option<Reference> {
    let literal = php::string_at(node)?;
    let call = php::call_at(&literal)?;
    if !matches!(
        php::call_method_name(&call).to_lowercase().as_str(),
        "geturl" | "getversion"
    ) {
        return None;
    }
    let argument = php::argument_index(&call, &literal)?;
    let argument_name = php::argument_name(&literal).to_lowercase();
    if php::argument_expression(&call, argument).as_ref() != Some(&literal) {
        return None;
    }
    if argument_name == "path" || argument_name.is_empty() && argument == 0 {
        let package_node = php_string_call_argument(&call, 1, &["packagename", "package"]);
        let mut reference = Reference::new(
            normalize_name(&php::string_value(&literal)),
            ReferenceKind::Asset,
            literal,
            call,
        );
        reference.package = php_static_string_value(package_node.as_ref());
        return Some(reference);
    }
    if argument_name == "packagename"
        || argument_name == "package"
        || argument_name.is_empty() && argument == 1
    {
        let asset_node = php_string_call_argument(&call, 0, &["path"]);
        let value = php_static_string_value(Some(&literal));
        let mut reference =
            Reference::new(value.clone(), ReferenceKind::AssetPackage, literal, call);
        reference.package = value;
        reference.asset_name = normalize_name(&php_static_string_value(asset_node.as_ref()));
        return Some(reference);
    }
    None
}

fn twig_reference_at(node: &SyntaxNode) -> Option<Reference> {
    let literal = twig::literal_string_at(node)?;
    if !twig::string_is_static(&literal) {
        return None;
    }
    if let Some(reference) = twig_assetic_reference_at(&literal) {
        return Some(reference);
    }
    let call = twig::function_call_at(&literal)?;
    let kind = match twig::function_name(&call).as_deref() {
        Some("asset") => ReferenceKind::Asset,
        Some("importmap") => ReferenceKind::Importmap,
        Some(
            "encore_entry_link_tags"
            | "encore_entry_script_tags"
            | "encore_entry_exists"
            | "encore_entry_js_files"
            | "encore_entry_css_files",
        ) => ReferenceKind::EncoreEntry,
        Some("vite_entry_link_tags" | "vite_entry_script_tags") => ReferenceKind::ViteEntry,
        _ => return None,
    };
    let argument = twig::function_argument_index(&literal)?;
    if twig::string_argument(&call, argument).as_ref() != Some(&literal) {
        return None;
    }
    if kind == ReferenceKind::Asset && argument == 1 {
        let value = twig::string_value(&literal).unwrap_or_default();
        let path_node = twig::string_argument(&call, 0);
        let mut reference =
            Reference::new(value.clone(), ReferenceKind::AssetPackage, literal, call);
        reference.package = value;
        reference.asset_name = normalize_name(&twig_static_string_value(path_node.as_ref()));
        return Some(reference);
    }
    if argument != 0 {
        return None;
    }
    let value = twig::string_value(&literal).unwrap_or_default();
    // `asset('js/')` names a directory to build paths from, not a file.
    if value.ends_with('/') {
        return None;
    }
    let package = if kind == ReferenceKind::Asset {
        twig_static_string_value(twig::string_argument(&call, 1).as_ref())
    } else {
        String::new()
    };
    let mut reference = Reference::new(normalize_name(&value), kind, literal, call);
    reference.package = package;
    Some(reference)
}

fn twig_assetic_reference_at(literal: &SyntaxNode) -> Option<Reference> {
    let tag = twig::tag_at(literal)?;
    let tag_name = twig::tag_name(&tag).unwrap_or_default();
    if tag_name != "stylesheets" && tag_name != "javascripts"
        || !twig::string_in_tag(literal, &[tag_name.as_str()])
    {
        return None;
    }
    let html_type = if tag_name == "stylesheets" {
        HtmlAssetType::Css
    } else {
        HtmlAssetType::JavaScript
    };
    let value = twig::string_value(literal).unwrap_or_default();
    // A leading @ without a bundle Resources/public path is an Assetic named
    // formula resolved from the compiled container.
    let (name, package, kind) = if value.starts_with('@') && !value.contains("/Resources/public/") {
        let name = value.trim();
        let name = name.strip_prefix('@').unwrap_or(name);
        (name.to_owned(), String::new(), ReferenceKind::AsseticNamed)
    } else {
        let (name, package) = assetic_logical_name(&value);
        (name, package, ReferenceKind::Asset)
    };
    if name.is_empty() {
        return None;
    }
    let mut reference = Reference::new(name, kind, literal.clone(), tag);
    reference.package = package;
    reference.assetic = true;
    reference.html_type = html_type;
    Some(reference)
}

/// `@AcmeBundle/Resources/public/js/app.js` → (`js/app.js`, `@AcmeBundle`).
fn assetic_logical_name(value: &str) -> (String, String) {
    let value = value.trim().replace('\\', "/");
    const PUBLIC_MARKER: &str = "/Resources/public/";
    if value.starts_with('@')
        && let Some(marker) = value.find(PUBLIC_MARKER)
        && marker > 1
    {
        return (
            normalize_name(&value[marker + PUBLIC_MARKER.len()..]),
            value[..marker].to_owned(),
        );
    }
    (normalize_name(&value), String::new())
}

const ASSET_PACKAGE_CLASSES: &[&str] = &[
    "Symfony\\Component\\Asset\\Packages",
    "Symfony\\Component\\Asset\\Package",
    "Symfony\\Component\\Asset\\PackageInterface",
    "Symfony\\Component\\Asset\\Context\\ContextualPackage",
    "Symfony\\Component\\Asset\\PathPackage",
    "Symfony\\Component\\Asset\\UrlPackage",
];

/// Whether a PHP reference's receiver is a Symfony Asset package (or
/// `Packages` for a package-name argument).
pub fn validate_php_reference(
    context: Option<&PhpContext>,
    reference: &Reference,
    index: &PhpIndex,
) -> bool {
    match reference.kind {
        ReferenceKind::AssetPackage => index.is_method_called_on_class(
            context,
            &reference.container,
            "Symfony\\Component\\Asset\\Packages",
        ),
        ReferenceKind::Asset => ASSET_PACKAGE_CLASSES
            .iter()
            .any(|class| index.is_method_called_on_class(context, &reference.container, class)),
        _ => false,
    }
}

fn php_string_call_argument(
    call: &SyntaxNode,
    fallback: usize,
    names: &[&str],
) -> Option<SyntaxNode> {
    if let Some(index) = php::arguments(call).position(|argument| {
        let name = php::argument_name(&argument).to_lowercase();
        names
            .iter()
            .any(|candidate| name == candidate.to_lowercase())
    }) {
        return php::argument_expression(call, index);
    }
    let argument = php::argument(call, fallback)?;
    if !php::argument_name(&argument).is_empty() {
        return None;
    }
    php::argument_expression(call, fallback)
}

/// The value of a quoted PHP string without interpolation, else empty.
pub(crate) fn php_static_string_value(node: Option<&SyntaxNode>) -> String {
    let Some(node) = node.filter(|node| node.kind() == PHP_STRING) else {
        return String::new();
    };
    let text = node.text().to_string();
    let bytes = text.trim().as_bytes();
    if bytes.len() < 2
        || !matches!(bytes[0], b'\'' | b'"')
        || bytes[bytes.len() - 1] != bytes[0]
        || bytes[0] == b'"' && bytes[1..bytes.len() - 1].contains(&b'$')
    {
        return String::new();
    }
    php::string_value(node)
}

fn twig_static_string_value(node: Option<&SyntaxNode>) -> String {
    node.filter(|node| twig::string_is_static(node))
        .and_then(twig::string_value)
        .unwrap_or_default()
}

/// The reference node's range without surrounding quotes.
pub fn reference_range(reference: &Reference) -> TextRange {
    let range = reference.node.range_trimmed_trivia();
    let text = reference.node.text().to_string();
    let text = text.trim().as_bytes();
    if text.len() >= 2 && matches!(text[0], b'\'' | b'"' | b'`') && text[text.len() - 1] == text[0]
    {
        return shrink(range);
    }
    range
}
