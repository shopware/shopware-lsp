use std::collections::HashMap;
use std::sync::Arc;

use serde_json::json;
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_syntax::{TextRange, TextSize};

use super::analyze;
use crate::shopware::*;
use crate::test_support::diagnostics_document;

struct MockIconProvider {
    icons: HashMap<&'static str, HashMap<&'static str, String>>,
}

impl IconProvider for MockIconProvider {
    fn icon_packs(&self) -> Vec<String> {
        self.icons.keys().map(|pack| (*pack).to_owned()).collect()
    }

    fn icons(&self, pack: &str) -> Vec<String> {
        self.icons
            .get(pack)
            .map(|icons| icons.keys().map(|icon| (*icon).to_owned()).collect())
            .unwrap_or_default()
    }

    fn icon(&self, pack: &str, icon: &str) -> String {
        self.icons
            .get(pack)
            .and_then(|icons| icons.get(icon))
            .cloned()
            .unwrap_or_default()
    }
}

struct Expected {
    range: (u32, u32),
    message: &'static str,
    icon: &'static str,
    pack: &'static str,
}

#[test]
fn theme_analyzer_twig_diagnostics() {
    let missing = |range, message, icon, pack| Expected {
        range,
        message,
        icon,
        pack,
    };
    let cases: Vec<(&str, &str, Vec<Expected>)> = vec![
        (
            "valid icon with default pack",
            "{% sw_icon 'heart' %}",
            vec![],
        ),
        (
            "invalid icon with default pack",
            "{% sw_icon 'nonexistent-icon' %}",
            vec![missing(
                (11, 29),
                "Icon 'nonexistent-icon' not found in pack 'default'",
                "nonexistent-icon",
                "default",
            )],
        ),
        (
            "invalid icon with custom pack",
            "{% sw_icon 'missing' {'pack': 'custom'} %}",
            vec![missing(
                (11, 20),
                "Icon 'missing' not found in pack 'custom'",
                "missing",
                "custom",
            )],
        ),
        (
            "valid icon with unquoted pack in style hash",
            "{% sw_icon 'arrow-medium-right' style { size: 'fluid', pack: 'solid' } %}",
            vec![],
        ),
        (
            "invalid icon with unquoted pack in style hash",
            "{% sw_icon 'missing' style { pack: 'solid' } %}",
            vec![missing(
                (11, 20),
                "Icon 'missing' not found in pack 'solid'",
                "missing",
                "solid",
            )],
        ),
        (
            "non-string pack value is skipped",
            "{% sw_icon 'nonexistent-icon' style { pack: somePack } %}",
            vec![],
        ),
        (
            "non-string pack value with quoted key is skipped",
            "{% sw_icon 'nonexistent-icon' {'pack': somePack} %}",
            vec![],
        ),
        (
            "multiple icons with mixed validity",
            "{% sw_icon 'heart' %}\n{% sw_icon 'invalid-icon' %}\n{% sw_icon 'home' %}",
            vec![missing(
                (33, 47),
                "Icon 'invalid-icon' not found in pack 'default'",
                "invalid-icon",
                "default",
            )],
        ),
    ];
    for (name, content, expected) in cases {
        let dir = tempfile::tempdir().unwrap();
        let base = dir.path().to_string_lossy().into_owned();
        let provider = MockIconProvider {
            icons: HashMap::from([
                (
                    "default",
                    HashMap::from([
                        ("heart", format!("{base}/icon/default/heart.svg")),
                        ("home", format!("{base}/icon/default/home.svg")),
                    ]),
                ),
                (
                    "solid",
                    HashMap::from([(
                        "arrow-medium-right",
                        format!("{base}/icon/solid/arrow-medium-right.svg"),
                    )]),
                ),
            ]),
        };
        let analyzer = ThemeAnalyzer::with_icon_provider(Arc::new(provider));
        let problems = analyze(&analyzer, &diagnostics_document("test.twig", content));
        assert_eq!(problems.len(), expected.len(), "{name}");
        for (problem, expected) in problems.iter().zip(&expected) {
            assert_eq!(
                problem.range,
                TextRange::new(
                    TextSize::from(expected.range.0),
                    TextSize::from(expected.range.1)
                ),
                "{name}"
            );
            assert_eq!(problem.message, expected.message, "{name}");
            assert_eq!(problem.severity, Some(DiagnosticSeverity::ERROR), "{name}");
            assert_eq!(problem.id.as_str(), "theme.icon.missing", "{name}");
            assert_eq!(
                problem.payload,
                Some(json!({"iconName": expected.icon, "pack": expected.pack})),
                "{name}"
            );
        }
    }
}
