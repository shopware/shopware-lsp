use super::*;
use crate::services_yaml_model::parse_services_xml;

fn convert(content: &str) -> String {
    let container = parse_services_xml(content).unwrap();
    // Go additionally re-parses the output with yaml.v3; the native YAML
    // CST does not accept every tagged flow collection, so the golden
    // comparison stands in for it.
    convert_container_to_yaml(&container).unwrap()
}

#[test]
fn convert_fixtures() {
    let directory = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("testdata/convert");
    let mut entries: Vec<_> = std::fs::read_dir(&directory).unwrap().flatten().collect();
    entries.sort_by_key(|entry| entry.file_name());
    assert!(!entries.is_empty());
    for entry in entries {
        if !entry.path().is_dir() {
            continue;
        }
        let input = std::fs::read_to_string(entry.path().join("services.xml")).unwrap();
        let expected = std::fs::read_to_string(entry.path().join("expected.yaml")).unwrap();
        assert_eq!(convert(&input), expected, "fixture {:?}", entry.file_name());
    }
}

#[test]
fn convert_errors() {
    let cases: &[(&str, &str, &str)] = &[
        (
            "unknown element in container",
            r#"<container><monolog/></container>"#,
            r#"unsupported element <monolog> inside <container>"#,
        ),
        (
            "stack is not supported",
            r#"<container><services><stack id="foo"/></services></container>"#,
            r#"unsupported element <stack> inside <services>"#,
        ),
        (
            "unknown attribute on service",
            r#"<container><services><service id="foo" class="Foo" scope="prototype"/></services></container>"#,
            r#"unsupported attribute "scope" on <service>"#,
        ),
        (
            "unknown element in service",
            r#"<container><services><service id="foo" class="Foo"><something/></service></services></container>"#,
            r#"unsupported element <something> inside <service>"#,
        ),
        (
            "duplicate service id",
            r#"<container><services><service id="foo" class="Foo"/><service id="foo" class="Bar"/></services></container>"#,
            r#"service "foo" is defined multiple times"#,
        ),
        (
            "service without id",
            r#"<container><services><service class="Foo"/></services></container>"#,
            r#"<service> requires an id attribute"#,
        ),
        (
            "prototype without resource",
            r#"<container><services><prototype namespace="Foo\"/></services></container>"#,
            r#"requires a resource attribute"#,
        ),
        (
            "alias with class",
            r#"<container><services><service id="foo" alias="bar" class="Foo"/></services></container>"#,
            r#"aliases only support the public attribute"#,
        ),
        (
            "service reference without id",
            r#"<container><services><service id="foo" class="Foo"><argument type="service"/></service></services></container>"#,
            r#"argument type="service" requires an id attribute"#,
        ),
        (
            "unsupported argument type",
            r#"<container><services><service id="foo" class="Foo"><argument type="wild"/></service></services></container>"#,
            r#"unsupported argument type "wild""#,
        ),
        (
            "unsupported on-invalid",
            r#"<container><services><service id="foo" class="Foo"><argument type="service" id="bar" on-invalid="explode"/></service></services></container>"#,
            r#"unsupported on-invalid value "explode""#,
        ),
        (
            "null on-invalid cannot be expressed in YAML",
            r#"<container><services><service id="foo" class="Foo"><argument type="service" id="bar" on-invalid="null"/></service></services></container>"#,
            r#"on-invalid="null" is not supported by the YAML format"#,
        ),
        (
            "unsupported decoration-on-invalid",
            r#"<container><services><service id="foo" class="Foo" decorates="bar" decoration-on-invalid="explode"/></services></container>"#,
            r#"unsupported decoration-on-invalid value "explode""#,
        ),
        (
            "tag without name",
            r#"<container><services><service id="foo" class="Foo"><tag/></service></services></container>"#,
            r#"<tag> requires a name"#,
        ),
        (
            "call without method",
            r#"<container><services><service id="foo" class="Foo"><call/></service></services></container>"#,
            r#"<call> requires a method attribute"#,
        ),
        (
            "factory without target",
            r#"<container><services><service id="foo" class="Foo"><factory/></service></services></container>"#,
            r#"<factory> requires a class, service, function or expression attribute"#,
        ),
        (
            "inline service in factory",
            r#"<container><services><service id="foo" class="Foo"><factory method="create"><service class="Bar"/></factory></service></services></container>"#,
            r#"unsupported element <service> inside <factory>"#,
        ),
        (
            "bind without key",
            r#"<container><services><service id="foo" class="Foo"><bind>value</bind></service></services></container>"#,
            r#"<bind> requires a key attribute"#,
        ),
        (
            "when without env",
            r#"<container><when><services/></when></container>"#,
            r#"<when> requires an env attribute"#,
        ),
        (
            "multiple defaults",
            r#"<container><services><defaults autowire="true"/><defaults public="false"/></services></container>"#,
            r#"multiple <defaults> elements are not supported"#,
        ),
        (
            "namespace on regular service",
            r#"<container><services><service id="foo" class="Foo" namespace="Bar\"/></services></container>"#,
            r#"namespace, resource and exclude are only supported on <prototype>"#,
        ),
    ];
    for (name, xml, expected) in cases {
        let container = parse_services_xml(xml).unwrap_or_else(|error| panic!("{name}: {error}"));
        let error = convert_container_to_yaml(&container).expect_err(name);
        assert!(error.0.contains(expected), "{name}: {error}");
    }
}

#[test]
fn convert_invalid_xml() {
    assert!(parse_services_xml("<routes/>").is_err());
    assert!(parse_services_xml("not xml").is_err());
}

#[test]
fn format_float_matches_go() {
    for (value, expected) in [
        (1.5, "1.5"),
        (1000.0, "1000"),
        (1e6, "1e+06"),
        (123456789.0, "1.23456789e+08"),
        (0.0001, "0.0001"),
        (0.00001, "1e-05"),
        (-2.5e-7, "-2.5e-07"),
        (100000.0, "100000"),
        (1.0, "1"),
    ] {
        assert_eq!(emit::format_float(value), expected);
    }
}
