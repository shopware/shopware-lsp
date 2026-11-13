//! The `scss.css_variable` inspection.

use std::sync::Arc;

use serde_json::{Map, Value};
use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoundFix, DiagnosticId, Inspection, InspectionDefinition, InspectionScope, ProblemDefinition,
};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_shopware::SupportedVersions;

use crate::diagnostics::{
    SCSS_PREFER_CSS_VARIABLE_CODE, ScssCssVariableAnalyzer, ScssVariableSource,
};
use crate::inspections::{BindProblems, BoundInspection, RewriteNodeFix, rewrite_node_bound_fixes};

pub fn new_scss_css_variable(
    index: Option<Arc<swlsp_style::Index>>,
    versions: impl Into<SupportedVersions>,
) -> Arc<dyn Inspection> {
    new_scss_css_variable_from(
        index.map(|index| index as Arc<dyn ScssVariableSource>),
        versions,
    )
}

pub(crate) fn new_scss_css_variable_from(
    variables: Option<Arc<dyn ScssVariableSource>>,
    versions: impl Into<SupportedVersions>,
) -> Arc<dyn Inspection> {
    let bind: BindProblems = Arc::new(
        |_: &DiagnosticId, payload: &Map<String, Value>| -> Vec<BoundFix> {
            rewrite_node_bound_fixes(payload)
        },
    );
    Arc::new(BoundInspection {
        definition: InspectionDefinition {
            id: "scss.css_variable".to_owned(),
            description: "Administration SCSS reading a variable of the Administration's variables.scss where its CSS custom property can be used.",
            domain: "scss",
            scope: InspectionScope::Framework,
            languages: vec![LanguageId::Scss],
            problems: vec![ProblemDefinition {
                id: DiagnosticId::from(SCSS_PREFER_CSS_VARIABLE_CODE),
                source: "scss".to_owned(),
                default_severity: DiagnosticSeverity::HINT,
                disabled_by_default: false,
            }],
        },
        analyzers: vec![Arc::new(ScssCssVariableAnalyzer::new(variables, versions))],
        fixes: vec![Arc::new(RewriteNodeFix)],
        bind: Some(bind),
    })
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use swlsp_lsp::BoxError;
    use swlsp_shopware::ResolvedVersion;

    use super::new_scss_css_variable_from;
    use crate::diagnostics::ScssVariableSource;
    use crate::inspections::REWRITE_NODE_FIX_ID;
    use crate::inspections::test_support::{check_diagnostics, check_fix};

    const VARIABLES: &str = "/project/vendor/shopware/administration/Resources/app/administration/src/app/assets/scss/variables.scss";
    const URI: &str =
        "file:///project/custom/plugins/Swag/src/Resources/app/administration/src/module/a/a.scss";

    /// `$color-gray-50`, `$border_radius` and `$size` of the Administration,
    /// `$color-plugin` of a plugin, and `$color-shadow` of both.
    struct Declared;

    impl ScssVariableSource for Declared {
        fn declarations(&self, name: &str) -> Result<Vec<(String, String)>, BoxError> {
            let plugin =
                "/project/custom/plugins/Swag/src/Resources/app/administration/src/variables.scss";
            Ok(match name {
                "color-gray-50" => vec![(VARIABLES.to_owned(), "color-gray-50".to_owned())],
                "border-radius" => vec![(VARIABLES.to_owned(), "border_radius".to_owned())],
                "size" => vec![(VARIABLES.to_owned(), "size".to_owned())],
                "color-plugin" => vec![(plugin.to_owned(), "color-plugin".to_owned())],
                "color-shadow" => vec![
                    (plugin.to_owned(), "color-shadow".to_owned()),
                    (VARIABLES.to_owned(), "color-shadow".to_owned()),
                ],
                _ => Vec::new(),
            })
        }
    }

    #[allow(clippy::field_reassign_with_default)]
    fn version(minor: i64) -> ResolvedVersion {
        let mut version = ResolvedVersion::default();
        version.known = true;
        version.version.major = 6;
        version.version.minor = minor;
        version
    }

    const SOURCE: &str = "@mixin box($w) { width: $w; }\n.a {\n    color: {|scss.variable.prefer-css-variable:$color-gray-50|};\n    border: 1px solid {|scss.variable.prefer-css-variable:$color-gray-50|} !important;\n    border-radius: {|scss.variable.prefer-css-variable:$border-radius|};\n    box-shadow: 0 0 4px {|scss.variable.prefer-css-variable:$color-shadow|}, 0 0 1px {|scss.variable.prefer-css-variable:$color_gray_50|};\n    background: $color-plugin;\n    outline-color: $unknown;\n    width: $size * 2;\n    margin: -$size;\n    fill: darken($color-gray-50, 10%);\n    padding: calc(#{$size} + 2px);\n    --custom: $color-gray-50;\n    --other: #{$color-gray-50};\n    @include box($size);\n    @media (min-width: $size) { height: {|scss.variable.prefer-css-variable:$size|}; }\n}\n$alias: $color-gray-50;\n";

    #[test]
    fn administration_variables_with_a_custom_property_are_reported() {
        let inspection = new_scss_css_variable_from(Some(Arc::new(Declared)), version(5));
        let (_, problems) = check_diagnostics(inspection.as_ref(), URI, SOURCE);
        assert_eq!(
            problems[0].message,
            "$color-gray-50 has the CSS custom property --color-gray-50; use var(--color-gray-50)"
        );
        // The property is named as the variable is declared.
        assert!(problems[2].message.ends_with("use var(--border_radius)"));
        assert!(problems[4].message.ends_with("use var(--color-gray-50)"));

        let plain = SOURCE
            .replace("{|scss.variable.prefer-css-variable:", "")
            .replace("|}", "");
        // Before 6.5, with an unknown version and outside of the
        // Administration there are no such properties.
        for (inspection, uri) in [
            (
                new_scss_css_variable_from(Some(Arc::new(Declared)), version(4)),
                URI,
            ),
            (
                new_scss_css_variable_from(Some(Arc::new(Declared)), ResolvedVersion::default()),
                URI,
            ),
            (
                new_scss_css_variable_from(Some(Arc::new(Declared)), version(7)),
                "file:///project/custom/plugins/Swag/src/Resources/app/storefront/src/scss/base.scss",
            ),
            (new_scss_css_variable_from(None, version(7)), URI),
        ] {
            check_diagnostics(inspection.as_ref(), uri, &plain);
        }
    }

    #[test]
    fn a_variable_the_file_binds_itself_is_left_alone() {
        let inspection = new_scss_css_variable_from(Some(Arc::new(Declared)), version(7));
        check_diagnostics(
            inspection.as_ref(),
            URI,
            "$color-gray-50: #fff;\n@mixin box($size) { width: $size; }\n.a { color: $color-gray-50; height: $size; }\n",
        );
    }

    #[test]
    fn the_fix_reads_the_custom_property() {
        let inspection = new_scss_css_variable_from(Some(Arc::new(Declared)), version(7));
        let presentation = check_fix(
            inspection.as_ref(),
            URI,
            ".a {\n    border: 1px solid {|scss.variable.prefer-css-variable:$color-gray-50|};\n}\n",
            REWRITE_NODE_FIX_ID,
            ".a {\n    border: 1px solid var(--color-gray-50);\n}\n",
        );
        assert!(presentation.preferred);
        assert_eq!(presentation.title, "Use var(--color-gray-50)");
    }
}
