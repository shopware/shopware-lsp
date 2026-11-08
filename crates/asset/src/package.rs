use std::collections::HashSet;

use swlsp_parser_php::query as php;
use swlsp_parser_php::syntax::{PHP_ARRAY, PHP_FUNCTION_CALL, PHP_MEMBER_CALL, PHP_SCOPED_CALL};
use swlsp_parser_xml::query as xml;
use swlsp_parser_yaml::query as yaml;
use swlsp_php_types::go_to_lower;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};

use crate::model::{Package, normalize_name};
use crate::parser::{php_string_content_range, shrink};
use crate::paths::{base, clean, to_slash};
use crate::reference::php_static_string_value;

/// Packages from `<tag name="assets.package" package="..."/>` and Shopware's
/// `<tag name="shopware.asset" asset="..."/>`, plus the bundle's inferred
/// `@Bundle` package.
pub fn packages_in_xml(path: &str, root: &SyntaxNode) -> Vec<Package> {
    let mut result = inferred_bundle_package(path);
    for tag in xml::elements(root, &["tag"]) {
        let tag_name = xml::attribute(&tag, "name")
            .and_then(|attribute| xml::attribute_value(&attribute))
            .unwrap_or_default();
        let package_attribute = match tag_name.as_str() {
            "shopware.asset" => "asset",
            "assets.package" => "package",
            _ => continue,
        };
        let Some(attribute) = xml::attribute(&tag, package_attribute) else {
            continue;
        };
        let name = xml::attribute_value(&attribute).unwrap_or_default();
        let name = name.trim();
        if name.is_empty() {
            continue;
        }
        result.push(Package {
            name: name.to_owned(),
            file: path.to_owned(),
            range: attribute.range_trimmed_trivia(),
            ..Package::default()
        });
    }
    deduplicate_packages(result)
}

/// Packages from `->tag('assets.package', ['package' => '...'])` service
/// configurators.
pub fn packages_in_php(path: &str, root: &SyntaxNode) -> Vec<Package> {
    let mut result = Vec::new();
    for call in php::nodes(root, &[PHP_MEMBER_CALL, PHP_SCOPED_CALL, PHP_FUNCTION_CALL]) {
        if !swlsp_php_types::equal_fold(&php::call_method_name(&call), "tag") {
            continue;
        }
        let option_name =
            match php_static_string_value(php::argument_expression(&call, 0).as_ref()).as_str() {
                "shopware.asset" => "asset",
                "assets.package" => "package",
                _ => continue,
            };
        let Some(options) =
            php::argument_expression(&call, 1).filter(|options| options.kind() == PHP_ARRAY)
        else {
            continue;
        };
        for item in php::array_items(&options) {
            if php_static_string_value(php::array_item_key(&item).as_ref()) != option_name {
                continue;
            }
            let value = php::array_item_value(&item);
            let name = php_static_string_value(value.as_ref());
            if name.is_empty() {
                continue;
            }
            result.push(Package {
                name,
                file: path.to_owned(),
                range: php_string_content_range(value.as_ref()),
                ..Package::default()
            });
        }
    }
    deduplicate_packages(result)
}

/// Packages from `framework.assets.packages` with static `base_path`s.
pub fn packages_in_yaml(path: &str, root: &SyntaxNode) -> Vec<Package> {
    let mut result = inferred_bundle_package(path);
    let packages = yaml::root_value(root)
        .and_then(|document| yaml::property(&document, "framework"))
        .and_then(|framework| yaml::property(&framework, "assets"))
        .and_then(|assets| yaml::property(&assets, "packages"));
    let Some(packages) = packages.filter(yaml::is_mapping) else {
        return deduplicate_packages(result);
    };
    for pair in yaml::pairs(&packages) {
        let key = yaml::pair_key(&pair);
        let name = key.as_ref().map(yaml::scalar_value).unwrap_or_default();
        let name = name.trim();
        if name.is_empty() {
            continue;
        }
        let mut base_path = String::new();
        if let Some(config) = yaml::pair_value(&pair).filter(yaml::is_mapping) {
            let value = yaml::property(&config, "base_path")
                .map(|value| yaml::scalar_value(&value))
                .unwrap_or_default();
            if !value.contains('%') {
                base_path = normalize_name(&value);
            }
        }
        result.push(Package {
            name: name.to_owned(),
            base_path,
            file: path.to_owned(),
            range: scalar_content_range(key.as_ref()),
            inferred: false,
        });
    }
    deduplicate_packages(result)
}

/// `@AcmeBundle` → `bundles/acme` for config files below
/// `<Bundle>/Resources/config/`.
fn inferred_bundle_package(path: &str) -> Vec<Package> {
    let normalized = to_slash(&clean(path));
    const MARKER: &str = "/Resources/config/";
    let Some(index) = normalized.rfind(MARKER) else {
        return Vec::new();
    };
    let bundle = base(&normalized[..index]);
    if bundle.is_empty() || bundle == "." {
        return Vec::new();
    }
    let lower = go_to_lower(&bundle);
    let public_name = lower.strip_suffix("bundle").unwrap_or(&lower);
    if public_name.is_empty() {
        return Vec::new();
    }
    vec![Package {
        name: format!("@{bundle}"),
        base_path: format!("bundles/{public_name}"),
        file: path.to_owned(),
        range: TextRange::default(),
        inferred: true,
    }]
}

fn scalar_content_range(node: Option<&SyntaxNode>) -> TextRange {
    let Some(node) = node else {
        return TextRange::default();
    };
    let range = node.range_trimmed_trivia();
    let text = node.text().to_string();
    let text = text.trim().as_bytes();
    if text.len() >= 2 && matches!(text[0], b'\'' | b'"') && text[text.len() - 1] == text[0] {
        return shrink(range);
    }
    range
}

fn compare_packages(left: &Package, right: &Package) -> std::cmp::Ordering {
    go_to_lower(&left.name)
        .cmp(&go_to_lower(&right.name))
        .then_with(|| left.inferred.cmp(&right.inferred))
}

/// Sorted by case-folded name with explicit declarations before inferred
/// ones, then file (and range before deduplication). Explicit packages are
/// unique per name, base path, file and range; inferred ones per name and
/// base path.
pub(crate) fn deduplicate_packages(mut packages: Vec<Package>) -> Vec<Package> {
    packages.sort_by(|left, right| {
        compare_packages(left, right)
            .then_with(|| left.file.cmp(&right.file))
            .then_with(|| left.range.start().cmp(&right.range.start()))
    });
    let mut seen = HashSet::new();
    let mut result = Vec::with_capacity(packages.len());
    for mut current in packages {
        current.name = current.name.trim().to_owned();
        current.base_path = normalize_name(&current.base_path);
        if current.name.is_empty() {
            continue;
        }
        let mut key = format!(
            "{}\0{}",
            go_to_lower(&current.name),
            go_to_lower(&current.base_path)
        );
        if !current.inferred {
            key.push_str(&format!(
                "\0{}\0{}:{}",
                clean(&current.file),
                u32::from(current.range.start()),
                u32::from(current.range.end())
            ));
        }
        if seen.insert(key) {
            result.push(current);
        }
    }
    result.sort_by(|left, right| {
        compare_packages(left, right).then_with(|| left.file.cmp(&right.file))
    });
    result
}
