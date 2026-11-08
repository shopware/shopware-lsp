use crate::{parameter_references, parse_service_reference};

#[test]
fn parse_service_reference_cases() {
    let cases: &[(&str, Option<(&str, bool)>)] = &[
        ("@app.service", Some(("app.service", false))),
        ("@?app.optional", Some(("app.optional", true))),
        ("@!app.optional", Some(("app.optional", true))),
        ("@@escaped", None),
        ("@=service('dynamic')", None),
        ("plain", None),
    ];
    for (value, expected) in cases {
        let actual = parse_service_reference(value);
        assert_eq!(
            actual
                .as_ref()
                .map(|(name, optional)| (name.as_str(), *optional)),
            *expected,
            "{value}"
        );
    }
}

#[test]
fn parameter_references_skip_escapes_and_env() {
    assert_eq!(
        parameter_references("%kernel.project_dir%/config %% escaped %env(APP_ENV)% %app.mode%"),
        vec!["kernel.project_dir", "app.mode"]
    );
}
