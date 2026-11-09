//! `check` with the store-compliance inspections and extension configuration.

use super::common::{path, run, temp_dir, write};

fn check(root: &str, target: &str) -> String {
    let run = run(&[
        "--root",
        root,
        "--allow-unsupported-project",
        "--json",
        "check",
        "--fail-on",
        "off",
        target,
    ]);
    assert!(run.result.is_ok(), "{:?} {}", run.result, run.err);
    run.out
}

#[test]
fn check_store_compliance_opt_in() {
    for (name, setting, configuration, want) in [
        (
            "explicit true even for a local path package",
            "validation:\n  store_compliance: true\n",
            r#"{"version":1,"domains":{"php":false}}"#,
            true,
        ),
        (
            "missing extension configuration",
            "",
            r#"{"version":1}"#,
            false,
        ),
        (
            "explicit false",
            "validation:\n  store_compliance: false\n",
            r#"{"version":1}"#,
            false,
        ),
        (
            "quoted true",
            "validation:\n  store_compliance: 'true'\n",
            r#"{"version":1}"#,
            false,
        ),
        (
            "Store domain disabled",
            "validation:\n  store_compliance: true\n",
            r#"{"version":1,"domains":{"shopware.store":false}}"#,
            false,
        ),
        (
            "individual Store rule disabled",
            "validation:\n  store_compliance: true\n",
            r#"{"version":1,"diagnostics":{"rules":{"shopware.store.require-core":"off"}}}"#,
            true,
        ),
    ] {
        let root = temp_dir();
        let directory = path(root.path(), "custom/plugins/Local");
        let plugin = format!("{directory}/composer.json");
        write(
            &path(root.path(), ".config/shopware/lsp.yaml"),
            configuration,
        );
        write(
            &plugin,
            r#"{"name":"acme/local","type":"shopware-platform-plugin"}"#,
        );
        write(
            &path(root.path(), "composer.json"),
            r#"{"require":{"acme/local":"*"},"repositories":[{"type":"path","url":"custom/plugins/*"}]}"#,
        );
        if !setting.is_empty() {
            write(&format!("{directory}/.shopware-extension.yml"), setting);
        }
        // Both cold indexing and cached restarts must find the setting.
        for _ in 0..2 {
            let output = check(root.path().to_str().unwrap(), &plugin);
            assert_eq!(output.contains("shopware.store."), want, "{name}: {output}");
            if name == "individual Store rule disabled" {
                assert!(
                    !output.contains("shopware.store.require-core"),
                    "{name}: {output}"
                );
            }
        }
    }
}

#[test]
fn check_extension_config_locations() {
    for location in [
        ".config/shopware-extension.yml",
        ".shopware-extension.yml",
        ".shopware-extension.yaml",
    ] {
        let root = temp_dir();
        let root_path = root.path().to_str().unwrap();
        let config = path(root.path(), location);
        write(&config, "validation: {store_compliance: true}");
        let composer = path(root.path(), "composer.json");
        write(&composer, r#"{"type":"shopware-platform-plugin"}"#);
        for _ in 0..2 {
            let output = check(root_path, &composer);
            assert!(output.contains("shopware.store."), "{location}: {output}");
        }
        if location == ".config/shopware-extension.yml" {
            write(&config, "validation: {store_compliance: false}");
            write(
                &path(root.path(), ".shopware-extension.yml"),
                "validation: {store_compliance: true}",
            );
            let output = check(root_path, &composer);
            assert!(!output.contains("shopware.store."), "{location}: {output}");
        }
    }
}
