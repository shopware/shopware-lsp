use std::collections::HashMap;

use super::*;

fn reader(
    files: HashMap<&'static str, &'static str>,
) -> impl FnMut(&str) -> std::io::Result<String> {
    move |path| {
        files
            .get(path)
            .map(|content| (*content).to_owned())
            .ok_or_else(|| std::io::Error::from(std::io::ErrorKind::NotFound))
    }
}

#[test]
fn plan_services_xml_conversion_includes_local_imports() {
    let mut read = reader(HashMap::from([
        (
            "/project/config/services.xml",
            r#"<container>
    <imports>
        <import resource="packages/listeners.xml" type="xml"/>
    </imports>
    <services>
        <service id="App\Example" class="App\Example"/>
    </services>
</container>"#,
        ),
        (
            "/project/config/packages/listeners.xml",
            r#"<container>
    <services>
        <service id="App\Listener" class="App\Listener"/>
    </services>
</container>"#,
        ),
    ]));
    let plan = plan_services_xml_conversion(
        &CancelToken::new(),
        "/project/config/services.xml",
        &mut read,
        &mut |_| Ok(false),
    )
    .unwrap();
    assert_eq!(plan.len(), 2);
    assert_eq!(
        plan[0].source_path,
        "/project/config/packages/listeners.xml"
    );
    assert_eq!(
        plan[0].target_path,
        "/project/config/packages/listeners.yaml"
    );
    assert_eq!(plan[1].source_path, "/project/config/services.xml");
    assert_eq!(plan[1].target_path, "/project/config/services.yaml");
    assert!(
        plan[1]
            .content
            .contains("resource: packages/listeners.yaml")
    );
    assert!(plan[1].content.contains("type: yaml"));
    for conversion in &plan {
        assert!(
            swlsp_parser_yaml::parse(&conversion.content)
                .errors
                .is_empty()
        );
    }
}

#[test]
fn plan_services_xml_conversion_refuses_existing_target() {
    let error = plan_services_xml_conversion(
        &CancelToken::new(),
        "/project/config/services.xml",
        &mut |_| Ok("<container/>".to_owned()),
        &mut |path| Ok(path == "/project/config/services.yaml"),
    )
    .unwrap_err();
    assert!(
        error.to_string().contains("services.yaml exists already"),
        "{error}"
    );
}

#[test]
fn plan_services_xml_conversion_refuses_lossy_input() {
    let error = plan_services_xml_conversion(
        &CancelToken::new(),
        "/project/config/services.xml",
        &mut |_| Ok(r#"<container><services><stack id="app"/></services></container>"#.to_owned()),
        &mut |_| Ok(false),
    )
    .unwrap_err();
    assert!(
        error.to_string().contains("unsupported element <stack>"),
        "{error}"
    );
}

/// Dry-runs every convention-loaded plugin services.xml of a real Shopware
/// checkout. Never writes.
#[test]
#[ignore = "needs SHOPWARE_LSP_REAL_WORLD_ROOT"]
fn services_xml_conversion_on_trunk() {
    let root = std::env::var("SHOPWARE_LSP_REAL_WORLD_ROOT").unwrap_or_else(|_| {
        format!(
            "{}/Developer/sw-trunk",
            std::env::var("HOME").unwrap_or_default()
        )
    });
    if !std::path::Path::new(&root).is_dir() {
        return;
    }
    let mut checked = 0;
    let mut stack = vec![std::path::PathBuf::from(&root)];
    while let Some(directory) = stack.pop() {
        let Ok(entries) = std::fs::read_dir(&directory) else {
            continue;
        };
        for entry in entries.flatten() {
            let path = entry.path();
            if path.is_dir() {
                if !matches!(
                    entry.file_name().to_str(),
                    Some(".git" | "node_modules" | "vendor")
                ) {
                    stack.push(path);
                }
                continue;
            }
            let normalized = path.to_string_lossy().to_lowercase();
            if !normalized.ends_with("/resources/config/services.xml") {
                continue;
            }
            let plan = plan_services_xml_conversion(
                &CancelToken::new(),
                &path.to_string_lossy(),
                &mut |path| std::fs::read_to_string(path),
                &mut |path| Ok(std::path::Path::new(path).exists()),
            )
            .unwrap_or_else(|error| panic!("convert {}: {error}", path.display()));
            assert!(!plan.is_empty());
            checked += 1;
        }
    }
    assert!(
        checked > 0,
        "trunk should contain plugin services.xml fixtures"
    );
}

/// shopware-cli's file fixture: a routes.xml importing another route file.
#[test]
fn plan_routes_xml_conversion_follows_imports_like_shopware_cli() {
    let fixture = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("testdata/routes-files/routes-imports");
    let input = fixture.join("input");
    let root = input.to_string_lossy().into_owned();
    let mut read = |path: &str| std::fs::read_to_string(path);
    let plan = plan_routes_xml_conversion(
        &CancelToken::new(),
        &format!("{root}/routes.xml"),
        &mut read,
        &mut |_| Ok(false),
    )
    .unwrap();
    let converted: Vec<(String, &str)> = plan
        .iter()
        .map(|file| {
            (
                file.target_path.strip_prefix(&root).unwrap().to_owned(),
                file.content.as_str(),
            )
        })
        .collect();
    assert_eq!(converted.len(), 2);
    for (relative, content) in converted {
        let expected = std::fs::read_to_string(
            fixture
                .join("expected")
                .join(relative.trim_start_matches('/')),
        )
        .unwrap();
        assert_eq!(content, expected, "{relative}");
    }
}

#[test]
fn plan_refuses_a_file_of_the_other_format() {
    let mut read = reader(HashMap::from([(
        "/project/config/routes.xml",
        "<container><services/></container>",
    )]));
    let error = plan_routes_xml_conversion(
        &CancelToken::new(),
        "/project/config/routes.xml",
        &mut read,
        &mut |_| Ok(false),
    )
    .unwrap_err();
    assert!(
        error.to_string().contains("expected element type <routes>"),
        "{error}"
    );
}
