use std::fs;
use std::path::PathBuf;

use super::*;

fn write(path: &std::path::Path, content: &str) {
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, content).unwrap();
}

fn temp_dir() -> tempfile::TempDir {
    let base = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../target/agent-semantic/tmp");
    fs::create_dir_all(&base).unwrap();
    tempfile::tempdir_in(base).unwrap()
}

#[test]
fn load_composer_model() {
    let directory = temp_dir();
    let root = absolute(directory.path());
    write(
        &root.join("composer.json"),
        r#"{
	  "require": {"php": "^8.2", "ext-json": "*", "ext-curl": "^8.0"},
	  "require-dev": {"ext-mbstring": "*"},
	  "config": {"platform": {"php": "8.3.4", "ext-imagick": false}},
  "autoload": {"psr-4": {"App\\": "src/"}, "files": ["bootstrap.php"]},
  "autoload-dev": {"psr-4": {"App\\Tests\\": ["tests/", "fixtures/"]}}
}"#,
    );
    write(
        &root.join("composer.lock"),
        r#"{
  "packages": [{
    "name": "vendor/package",
    "version": "1.0.0",
    "autoload": {"psr-4": {"Vendor\\Package\\": "src/"}}
  }]
}"#,
    );
    let model = load(&root).unwrap();
    assert_eq!(model.php_version.to_string(), "8.3.4");
    assert_eq!(model.psr4["App\\"], vec![root.join("src")]);
    assert!(model.source_roots().contains(&root.join("tests")));
    assert!(
        model
            .source_roots()
            .contains(&root.join("vendor/vendor/package/src"))
    );
    assert_eq!(model.files, vec![root.join("bootstrap.php")]);
    assert_eq!(model.required_extensions, vec!["curl", "json", "mbstring"]);
    assert_eq!(model.disabled_extensions, vec!["imagick"]);
}

#[test]
fn missing_composer_json_uses_defaults() {
    let directory = temp_dir();
    let model = load(directory.path()).unwrap();
    assert_eq!(model.php_version, Version::new(8, 2, 0));
    assert!(model.psr4.is_empty());
}

#[test]
fn psr4_mappings_for_directory_use_nearest_composer_package() {
    let directory = temp_dir();
    let root = absolute(directory.path());
    fs::create_dir_all(root.join("src")).unwrap();
    let plugin_root = root.join("custom/plugins/FroshTools");
    let command_directory = plugin_root.join("src/Command");
    fs::create_dir_all(&command_directory).unwrap();
    write(
        &root.join("composer.json"),
        r#"{"autoload":{"psr-4":{"Shopware\\":"src/"}}}"#,
    );
    write(
        &plugin_root.join("composer.json"),
        r#"{
  "autoload": {"psr-4": {"Frosh\\Tools\\": "src/"}},
  "autoload-dev": {"psr-4": {"Frosh\\Tools\\Tests\\": ["tests/"]}}
}"#,
    );
    let model = load(&root).unwrap();
    let mappings = model
        .psr4_mappings_for_directory(&command_directory)
        .unwrap();
    assert_eq!(
        mappings,
        vec![
            Psr4Mapping {
                namespace: "Frosh\\Tools\\".to_owned(),
                root: plugin_root.join("src")
            },
            Psr4Mapping {
                namespace: "Frosh\\Tools\\Tests\\".to_owned(),
                root: plugin_root.join("tests")
            },
        ]
    );
    let root_mappings = model
        .psr4_mappings_for_directory(&root.join("src"))
        .unwrap();
    assert_eq!(
        root_mappings,
        vec![Psr4Mapping {
            namespace: "Shopware\\".to_owned(),
            root: root.join("src")
        }]
    );
    assert!(
        model
            .psr4_mappings_for_directory(Path::new("/definitely/outside"))
            .is_err()
    );
}

#[test]
fn composer_extension_selection_and_overrides() {
    let mut model = Model {
        required_extensions: vec!["json".into(), "curl".into()],
        disabled_extensions: vec!["curl".into()],
        ..Model::default()
    };
    model.configure_extensions(&["ext-Redis", "pdo-mysql", "json"], &["redis"]);
    assert_eq!(model.stub_extensions(), vec!["json", "pdo_mysql"]);
    assert_eq!(model.extension_availability("ext-pdo-mysql"), (true, true));
    assert_eq!(model.extension_availability("curl"), (false, true));
    assert!(!model.extension_availability("soap").1);

    model.required_extensions.push("dom".into());
    model.loaded_extensions = Some(vec!["json".into(), "pdo_mysql".into()]);
    assert_eq!(model.extension_availability("dom"), (false, true));
}

#[test]
fn load_discovers_phpunit_bootstrap_psr4_mappings() {
    let directory = temp_dir();
    let root = absolute(directory.path());
    let source_root = root.join("vendor-bin/tool/vendor/vendor/package/src");
    fs::create_dir_all(&source_root).unwrap();
    write(&root.join("composer.json"), "{}");
    write(
        &root.join("phpunit.xml.dist"),
        r#"<phpunit bootstrap="tests/TestBootstrap.php"/>"#,
    );
    write(
        &root.join("tests/TestBootstrap.php"),
        "<?php\n$toolSource = dirname(__DIR__) . '/vendor-bin/tool/vendor/vendor/package/src';\n$loader->addPsr4('Tool\\\\Api\\\\', $toolSource);\n",
    );
    let model = load(&root).unwrap();
    assert_eq!(model.psr4["Tool\\Api\\"], vec![source_root.clone()]);
    assert!(model.source_roots().contains(&source_root));
}

#[test]
fn parse_version_constraint_takes_first_version() {
    assert_eq!(
        parse_version_constraint("^8.2 || ^8.3"),
        Some(Version::new(8, 2, 0))
    );
    assert_eq!(
        parse_version_constraint("v7.3.2"),
        Some(Version::new(7, 3, 2))
    );
    assert_eq!(
        parse_version_constraint("2.8.x-dev"),
        Some(Version::new(2, 8, 0))
    );
    assert_eq!(parse_version_constraint("8."), Some(Version::new(8, 0, 0)));
    assert_eq!(parse_version_constraint("dev-main"), None);
}

#[test]
fn version_comparison_includes_patch() {
    let version = Version::new(6, 6, 10);
    assert!(version.at_least(6, 6));
    assert!(version.at_least_patch(6, 6, 4));
    assert!(version.at_least_patch(6, 6, 10));
    assert!(!version.at_least_patch(6, 6, 11));
    assert_eq!(version.compare(Version::new(6, 5, 99)), 1);
    assert_eq!(version.compare(Version::new(6, 6, 10)), 0);
    assert_eq!(version.compare(Version::new(6, 7, 0)), -1);
}

#[test]
fn dependency_version() {
    let model = Model {
        dependencies: vec![
            Package {
                name: "symfony/http-kernel".into(),
                version: "v7.3.2".into(),
                ..Package::default()
            },
            Package {
                name: "symfony/form".into(),
                version: "2.8.x-dev".into(),
                ..Package::default()
            },
        ],
        ..Model::default()
    };
    let version = model
        .dependency_version(&["symfony/missing", "symfony/http-kernel"])
        .unwrap();
    assert_eq!(version.to_string(), "7.3.2");
    assert!(version.at_least(2, 8));
    assert!(model.dependency_version(&["symfony/missing"]).is_none());
}

#[test]
fn clean_and_join_normalize_lexically() {
    assert_eq!(clean(Path::new("/a/./b/../c/")), PathBuf::from("/a/c"));
    assert_eq!(clean(Path::new("/../a")), PathBuf::from("/a"));
    assert_eq!(clean(Path::new("")), PathBuf::from("."));
    assert_eq!(join(Path::new("/root"), "src/"), PathBuf::from("/root/src"));
    assert_eq!(join(Path::new("/root"), "/abs"), PathBuf::from("/root/abs"));
}
