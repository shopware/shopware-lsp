use swlsp_parser_xml::query as xml;
use swlsp_syntax::{LineIndex, SyntaxNodeExt, Tree};

use crate::types::AppPermission;

/// Metadata of a Shopware app `manifest.xml`.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ManifestMeta {
    pub name: String,
    pub label: String,
    pub description: String,
    pub author: String,
    pub copyright: String,
    pub version: String,
    pub license: String,
    pub path: String,
    pub permissions: Vec<AppPermission>,
}

/// Parses a manifest with the shared error-tolerant XML parser.
pub fn parse_manifest_xml(path: &str, data: &[u8]) -> Option<ManifestMeta> {
    let parse = swlsp_parser_xml::parse(&String::from_utf8_lossy(data));
    parse_manifest_tree(path, &parse.tree)
}

/// Extracts the manifest metadata, or `None` when the tree has no
/// `<manifest><meta>`.
pub fn parse_manifest_tree(path: &str, tree: &Tree) -> Option<ManifestMeta> {
    let root = tree.root();
    let manifest = xml::elements(&root, &["manifest"]).into_iter().next()?;
    let meta = xml::child_element(&manifest, &["meta"])?;

    let mut result = ManifestMeta {
        path: path.to_owned(),
        ..ManifestMeta::default()
    };
    for child in xml::child_elements(&meta, &[]) {
        let value = xml::text_content(&child).trim().to_owned();
        let target = match xml::element_name(&child).as_deref() {
            Some("name") => &mut result.name,
            Some("label") => &mut result.label,
            Some("description") => &mut result.description,
            Some("author") => &mut result.author,
            Some("copyright") => &mut result.copyright,
            Some("version") => &mut result.version,
            Some("license") => &mut result.license,
            _ => continue,
        };
        *target = value;
    }
    if let Some(permissions) = xml::child_element(&manifest, &["permissions"]) {
        let mut lines = None;
        for permission in xml::child_elements(&permissions, &[]) {
            let operation = xml::element_name(&permission).unwrap_or_default();
            let entity = xml::text_content(&permission).trim().to_owned();
            if entity.is_empty() {
                continue;
            }
            let lines = lines.get_or_insert_with(|| LineIndex::new(tree.source_arc().clone()));
            let (line, _) = lines.position(permission.range_trimmed_trivia().start().into());
            result.permissions.push(AppPermission {
                operation,
                entity,
                line: i64::from(line) + 1,
            });
        }
    }
    Some(result)
}

#[cfg(test)]
mod tests {
    use super::*;

    const SCHEMA: &str = r#"<manifest xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
          xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/shopware/trunk/src/Core/Framework/App/Manifest/Schema/manifest-3.0.xsd">"#;

    #[test]
    fn parse_manifest_xml_cases() {
        let basic = format!(
            r#"<?xml version="1.0" encoding="UTF-8"?>
{SCHEMA}
    <meta>
        <name>TestApp</name>
        <label>Test App Label</label>
        <description>A test description</description>
        <author>Test Company Ltd.</author>
        <copyright>(c) by Test Company Ltd.</copyright>
        <version>1.0.0</version>
        <license>MIT</license>
    </meta>
</manifest>"#
        );
        let missing_name = format!(
            r#"<?xml version="1.0" encoding="UTF-8"?>
{SCHEMA}
    <meta>
        <label>Test App Label</label>
        <description>A test description</description>
        <author>Test Company Ltd.</author>
        <copyright>(c) by Test Company Ltd.</copyright>
        <version>1.0.0</version>
        <license>MIT</license>
    </meta>
</manifest>"#
        );
        let empty = format!(
            r#"<?xml version="1.0" encoding="UTF-8"?>
{SCHEMA}
    <meta>
        <name></name>
        <label></label>
        <description></description>
        <author></author>
        <copyright></copyright>
        <version></version>
        <license></license>
    </meta>
</manifest>"#
        );
        let missing_meta =
            format!("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n{SCHEMA}\n</manifest>");
        let not_manifest = r#"<?xml version="1.0" encoding="UTF-8"?>
<not-manifest>
    <something>
        <name>NotApp</name>
    </something>
</not-manifest>"#;
        type Case<'a> = (&'a str, &'a str, Option<(&'a str, &'a str)>);
        let cases: [Case; 6] = [
            ("basic", &basic, Some(("TestApp", "Test App Label"))),
            ("missing name", &missing_name, Some(("", "Test App Label"))),
            ("empty elements", &empty, Some(("", ""))),
            ("missing meta", &missing_meta, None),
            ("not a manifest", not_manifest, None),
            (
                "empty xml",
                r#"<?xml version="1.0" encoding="UTF-8"?>"#,
                None,
            ),
        ];
        for (name, source, expected) in cases {
            let manifest = parse_manifest_xml("test.xml", source.as_bytes());
            match expected {
                None => assert!(manifest.is_none(), "{name}"),
                Some((app, label)) => {
                    let manifest = manifest.expect(name);
                    assert_eq!(manifest.name, app, "{name}");
                    assert_eq!(manifest.label, label, "{name}");
                    assert_eq!(manifest.path, "test.xml", "{name}");
                }
            }
        }
    }

    #[test]
    fn parse_manifest_permissions() {
        let manifest = parse_manifest_xml(
            "/app/manifest.xml",
            br#"
<manifest>
    <meta><name>AcmeApp</name></meta>
    <permissions>
        <read>product</read>
        <create>order</create>
    </permissions>
</manifest>"#,
        )
        .unwrap();
        assert_eq!(
            manifest.permissions,
            vec![
                AppPermission {
                    operation: "read".into(),
                    entity: "product".into(),
                    line: 5
                },
                AppPermission {
                    operation: "create".into(),
                    entity: "order".into(),
                    line: 6
                },
            ]
        );
    }
}
