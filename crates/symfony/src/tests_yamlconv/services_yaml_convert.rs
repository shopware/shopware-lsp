use super::*;
use crate::services_yaml_model::parse_services_xml;

fn convert(content: &str) -> String {
    let container = parse_services_xml(content).unwrap();
    // The native YAML CST does not accept every tagged flow collection, so
    // the golden comparison stands in for re-parsing the output.
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
            "null on-invalid in a method call",
            r#"<container><services><service id="foo" class="Foo"><call method="set"><argument type="service" id="bar" on-invalid="null"/></call></service></services></container>"#,
            r#"on-invalid="null" in a method call, a property or a collection cannot be expressed in YAML"#,
        ),
        (
            "null on-invalid in a property",
            r#"<container><services><service id="foo" class="Foo"><property name="bar" type="service" id="bar" on-invalid="null"/></service></services></container>"#,
            r#"on-invalid="null" in a method call, a property or a collection cannot be expressed in YAML"#,
        ),
        (
            "null on-invalid in a collection",
            r#"<container><services><service id="foo" class="Foo"><argument type="collection"><argument type="service" id="bar" on-invalid="null"/></argument></service></services></container>"#,
            r#"on-invalid="null" in a method call, a property or a collection cannot be expressed in YAML"#,
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
fn format_float_uses_shortest_digits_with_exponent_outside_small_range() {
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

/// For a constructor argument Symfony resolves a missing `@?` service to
/// null, which is what `on-invalid="null"` asks for.
#[test]
fn null_on_invalid_constructor_arguments_become_optional_references() {
    let yaml = convert(
        r#"<container><services>
<service id="foo" class="Foo">
    <argument type="service" id="bar" on-invalid="null"/>
    <argument key="$baz" type="service" id="baz" on-invalid="null"/>
    <argument type="service"><service class="Inline"><argument type="service" id="qux" on-invalid="null"/></service></argument>
</service>
</services></container>"#,
    );
    assert!(yaml.contains("0: '@?bar'"), "{yaml}");
    assert!(yaml.contains("$baz: '@?baz'"), "{yaml}");
    assert!(yaml.contains("- '@?qux'"), "{yaml}");
}

/// A block scalar cannot keep a leading line break or leading indentation.
#[test]
fn strings_starting_with_whitespace_stay_exact() {
    let yaml = convert(
        "<container><services><service id=\"a\" class=\"Foo\">\n    <argument type=\"string\">\n                some value</argument>\n    <argument type=\"string\">\na</argument>\n    <argument type=\"string\">  two\nlines</argument>\n    <argument type=\"string\">first\n  second</argument>\n</service></services></container>",
    );
    assert!(
        yaml.contains(r#"- "\n                some value""#),
        "{yaml}"
    );
    assert!(yaml.contains(r#"- "\na""#), "{yaml}");
    assert!(yaml.contains(r#"- "  two\nlines""#), "{yaml}");
    // Indentation after the first line is no problem for a block scalar.
    assert!(
        yaml.contains("- |-\n              first\n                second"),
        "{yaml}"
    );
}
