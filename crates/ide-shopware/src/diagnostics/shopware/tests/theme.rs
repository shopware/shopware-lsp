use std::collections::HashMap;
use std::sync::Arc;

use serde_json::json;
use swlsp_lsp_protocol::DiagnosticSeverity;

use crate::diagnostics::shopware::*;
use crate::diagnostics::test_support::check_problems;

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

/// The message, icon and pack of a reported missing icon.
type Missing = (&'static str, &'static str, &'static str);

#[test]
fn theme_analyzer_twig_diagnostics() {
    let cases: Vec<(&str, &str, Vec<Missing>)> = vec![
        (
            "valid icon with default pack",
            "{% sw_icon 'heart' %}",
            vec![],
        ),
        (
            "invalid icon with default pack",
            "{% sw_icon {|theme.icon.missing:'nonexistent-icon'|} %}",
            vec![(
                "Icon 'nonexistent-icon' not found in pack 'default'",
                "nonexistent-icon",
                "default",
            )],
        ),
        (
            "invalid icon with custom pack",
            "{% sw_icon {|theme.icon.missing:'missing'|} {'pack': 'custom'} %}",
            vec![(
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
            "{% sw_icon {|theme.icon.missing:'missing'|} style { pack: 'solid' } %}",
            vec![(
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
            "{% sw_icon 'heart' %}\n{% sw_icon {|theme.icon.missing:'invalid-icon'|} %}\n{% sw_icon 'home' %}",
            vec![(
                "Icon 'invalid-icon' not found in pack 'default'",
                "invalid-icon",
                "default",
            )],
        ),
    ];
    for (name, fixture, expected) in cases {
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
        let (_, problems) = check_problems(&analyzer, "test.twig", fixture);
        for (problem, (message, icon, pack)) in problems.iter().zip(&expected) {
            assert_eq!(problem.message, *message, "{name}");
            assert_eq!(problem.severity, Some(DiagnosticSeverity::ERROR), "{name}");
            assert_eq!(
                problem.payload,
                Some(json!({"iconName": icon, "pack": pack})),
                "{name}"
            );
        }
    }
}
