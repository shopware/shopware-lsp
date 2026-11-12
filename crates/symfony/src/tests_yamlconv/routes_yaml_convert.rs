use super::*;

fn convert(content: &str) -> String {
    convert_routes_to_yaml(&parse_routes_xml(content).unwrap()).unwrap()
}

/// The fixtures of shopware-cli's routes conversion: both implementations
/// must produce the same YAML.
#[test]
fn convert_route_fixtures() {
    let directory = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("testdata/routes");
    let mut entries: Vec<_> = std::fs::read_dir(&directory).unwrap().flatten().collect();
    entries.sort_by_key(|entry| entry.file_name());
    assert!(!entries.is_empty());
    for entry in entries {
        let input = std::fs::read_to_string(entry.path().join("routes.xml")).unwrap();
        let expected = std::fs::read_to_string(entry.path().join("expected.yaml")).unwrap();
        assert_eq!(convert(&input), expected, "fixture {:?}", entry.file_name());
    }
}

#[test]
fn convert_route_errors() {
    for (name, xml, expected) in [
        (
            "route without id",
            r#"<routes><route path="/foo"/></routes>"#,
            r#"<route> requires an id attribute"#,
        ),
        (
            "route without path",
            r#"<routes><route id="foo"/></routes>"#,
            r#"requires a path attribute or <path> child elements"#,
        ),
        (
            "path attribute and child elements",
            r#"<routes><route id="foo" path="/foo"><path locale="en">/en/foo</path></route></routes>"#,
            r#"must not have both a path attribute and <path> child elements"#,
        ),
        (
            "controller attribute and _controller default",
            r#"<routes><route id="foo" path="/foo" controller="Foo"><default key="_controller">Bar</default></route></routes>"#,
            r#"must not specify both the controller attribute and the _controller default"#,
        ),
        (
            "duplicate route id",
            r#"<routes><route id="foo" path="/foo"/><route id="foo" path="/bar"/></routes>"#,
            r#"route "foo" is defined multiple times"#,
        ),
        (
            "unknown element",
            r#"<routes><something/></routes>"#,
            r#"unsupported element <something> inside <routes>"#,
        ),
        (
            "unknown attribute on route",
            r#"<routes><route id="foo" path="/foo" bogus="1"/></routes>"#,
            r#"unsupported attribute "bogus" on <route>"#,
        ),
        (
            "unknown element in route",
            r#"<routes><route id="foo" path="/foo"><something/></route></routes>"#,
            r#"unsupported element <something> inside <route>"#,
        ),
        (
            "import without resource",
            r#"<routes><import type="attribute"/></routes>"#,
            r#"<import> requires a resource attribute"#,
        ),
        (
            "exclude attribute mixed with exclude elements",
            r#"<routes><import resource="foo" exclude="bar"><exclude>baz</exclude></import></routes>"#,
            r#"mixes the exclude attribute with <exclude> elements"#,
        ),
        (
            "prefix attribute and child elements",
            r#"<routes><import resource="foo" prefix="/en"><prefix locale="en">/en</prefix></import></routes>"#,
            r#"must not have both a prefix attribute and <prefix> child elements"#,
        ),
        (
            "host attribute and child elements",
            r#"<routes><route id="foo" path="/foo" host="example.com"><host locale="en">en.example.com</host></route></routes>"#,
            r#"must not have both a host attribute and <host> child elements"#,
        ),
        (
            "when without env",
            r#"<routes><when><route id="foo" path="/foo"/></when></routes>"#,
            r#"<when> requires an env attribute"#,
        ),
        (
            "nested when",
            r#"<routes><when env="dev"><when env="test"><route id="foo" path="/foo"/></when></when></routes>"#,
            r#"<when> elements cannot be nested"#,
        ),
        (
            "duplicate when env",
            r#"<routes><when env="dev"><route id="a" path="/a"/></when><when env="dev"><route id="b" path="/b"/></when></routes>"#,
            r#"environment "dev" is configured multiple times"#,
        ),
        (
            "requirement without key",
            r#"<routes><route id="foo" path="/foo"><requirement>\d+</requirement></route></routes>"#,
            r#"<requirement> requires a key attribute"#,
        ),
        (
            "option without key",
            r#"<routes><route id="foo" path="/foo"><option>true</option></route></routes>"#,
            r#"<option> requires a key attribute"#,
        ),
        (
            "default without key",
            r#"<routes><route id="foo" path="/foo"><default>1</default></route></routes>"#,
            r#"<default> requires a key attribute"#,
        ),
        (
            "multiple typed values in one default",
            r#"<routes><route id="foo" path="/foo"><default key="x"><bool>true</bool><int>1</int></default></route></routes>"#,
            r#"only one typed value element is allowed"#,
        ),
        (
            "unsupported typed value element",
            r#"<routes><route id="foo" path="/foo"><default key="x"><money>5</money></default></route></routes>"#,
            r#"unsupported typed value element <money>"#,
        ),
        (
            "invalid int value",
            r#"<routes><route id="foo" path="/foo"><default key="x"><int>abc</int></default></route></routes>"#,
            r#"invalid <int> value "abc""#,
        ),
        (
            "map entry without key",
            r#"<routes><route id="foo" path="/foo"><default key="x"><map><string>v</string></map></default></route></routes>"#,
            r#"<map> entries require a key attribute"#,
        ),
        (
            "localized path without locale",
            r#"<routes><route id="foo"><path>/foo</path></route></routes>"#,
            r#"<path> child elements require a locale attribute"#,
        ),
    ] {
        let error = parse_routes_xml(xml)
            .and_then(|routes| convert_routes_to_yaml(&routes))
            .expect_err(name);
        assert!(error.0.contains(expected), "{name}: {}", error.0);
    }
}

#[test]
fn parse_rejects_other_documents() {
    let error = parse_routes_xml("<container/>").unwrap_err();
    assert!(
        error.0.contains("expected element type <routes>"),
        "{}",
        error.0
    );
    assert!(parse_routes_xml("<routes>").is_err());
}
