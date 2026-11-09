use swlsp_php_semantic::{Symbol, SymbolKind};
use swlsp_textutil::eq_ignore_case;

use crate::types::{ShopwareExtension, ShopwareExtensionType};

/// Whether `class` directly extends `Shopware\Core\Framework\Bundle` or
/// `Shopware\Core\Framework\Plugin`.
pub(crate) fn is_shopware_bundle(class: &Symbol) -> bool {
    if class.kind != SymbolKind::Class {
        return false;
    }
    let Some(parent) = class.extends().first() else {
        return false;
    };
    let parent = parent.strip_prefix('\\').unwrap_or(parent);
    parent == "Shopware\\Core\\Framework\\Bundle" || parent == "Shopware\\Core\\Framework\\Plugin"
}

pub(crate) fn create_bundle_from_class(class: &Symbol) -> ShopwareExtension {
    let fully_qualified: &str = &class.fully_qualified;
    let name = fully_qualified
        .rsplit('\\')
        .next()
        .unwrap_or(fully_qualified);
    ShopwareExtension {
        name: name.to_owned(),
        path: class.path.to_string(),
        kind: ShopwareExtensionType::Bundle,
        permissions: Vec::new(),
    }
}

const CORE_BUNDLES: &[&str] = &[
    "Administration.php",
    "Checkout.php",
    "DevOps.php",
    "Framework.php",
    "Plugin.php",
    "Maintenance.php",
    "Profiling.php",
    "Service.php",
    "Content.php",
    "System.php",
    "Elasticsearch.php",
    "Storefront.php",
];

/// Whether a file may declare an indexable bundle or app manifest.
pub(crate) fn is_valid_for_index(path: &str) -> bool {
    if has_excluded_bundle_path_component(path) {
        return false;
    }
    let file_name = path.rsplit('/').next().unwrap_or(path);
    if file_name.starts_with('.') || CORE_BUNDLES.contains(&file_name) {
        return false;
    }
    // Test files are skipped except TestBundle.php / TestPlugin.php.
    if contains_fold(file_name, "test")
        && !has_suffix_fold(file_name, "bundle.php")
        && !has_suffix_fold(file_name, "plugin.php")
    {
        return false;
    }
    true
}

fn has_excluded_bundle_path_component(path: &str) -> bool {
    path.split(['/', '\\']).any(|component| {
        ["tests", "test", "fixtures", "_fixture", "_fixtures"]
            .iter()
            .any(|excluded| eq_ignore_case(component, excluded))
    })
}

fn contains_fold(source: &str, needle: &str) -> bool {
    source
        .as_bytes()
        .windows(needle.len())
        .any(|window| window.eq_ignore_ascii_case(needle.as_bytes()))
}

fn has_suffix_fold(source: &str, suffix: &str) -> bool {
    source.len() >= suffix.len()
        && source.as_bytes()[source.len() - suffix.len()..].eq_ignore_ascii_case(suffix.as_bytes())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn valid_bundle_index_path() {
        let cases = [
            ("/project/src/Acme/AcmeBundle.php", true),
            ("/project/tests/FixtureBundle.php", false),
            ("/project/FiXtUrEs/FixtureBundle.php", false),
            (r"C:\project\_Fixtures\FixtureBundle.php", false),
            ("/project/src/.HiddenBundle.php", false),
            ("/project/src/ProductTest.php", false),
            ("/project/src/TestBundle.php", true),
            ("/project/src/TestPlugin.php", true),
        ];
        for (path, valid) in cases {
            assert_eq!(is_valid_for_index(path), valid, "{path}");
        }
        assert!(!is_valid_for_index("/project/te\u{17f}ts/AcmeBundle.php"));
    }
}
