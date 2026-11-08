use super::*;
use crate::config_types::*;

fn parse(path: &str, source: &str) -> Config {
    let parse = swlsp_parser_yaml::parse(source);
    parse_config_tree(path, &parse.root())
}

fn some<T: Clone>(value: T) -> Option<T> {
    Some(value)
}

fn strings(values: &[&str]) -> Vec<String> {
    values.iter().map(|value| (*value).to_owned()).collect()
}

const ENTIRE: &str = r#"compatibility_date: "2026-09-01"
build:
  extraBundles:
    - path: src/Extra
      name: Extra
  shopwareVersionConstraint: "~6.7"
  zip:
    composer:
      enabled: false
      before_hooks: ["echo before"]
      after_hooks: ["echo after"]
      excluded_packages: [acme/dev]
    assets:
      enabled: true
      enable_asset_caching: true
      enable_es_build_for_admin: true
      enable_es_build_for_storefront: true
      disable_sass: true
      npm_strict: true
      before_hooks: ["npm run prepare"]
      after_hooks: ["npm run cleanup"]
      additional_caches:
        - path: public/built
          source_paths: [src/source]
    pack:
      excludes:
        paths: [tests, docs]
      before_hooks: ["echo pack"]
    checksum:
      ignore: [README.md]
validation:
  store_compliance: true
  php_version: "8.4"
  ignore:
    - php.lint
    - identifier: phpstan
      path: src/Example.php
      message: "Known issue"
changelog:
  enabled: true
  pattern: "^feat:"
  template: |-
    ## Changes
    {{ .Message }}
  variables: {ticket: "[A-Z]+-\\d+"}
store:
  availabilities: [German, International]
  default_locale: de_DE
  localizations: [de_DE, en_GB]
  type: extension
  icon: src/Resources/icon.png
  automatic_bugfix_version_compatibility: false
  meta_title: {de: "Titel", en: "Title"}
  meta_description: {de: "Beschreibung", en: "Description"}
  description:
    en: |
      First line
      Second line
  installation_manual: {en: "Install it"}
  tags: {en: [shop, catalog]}
  videos: {en: [https://example.com/video]}
  highlights: {en: [Fast]}
  features: {en: [Search]}
  faq:
    en:
      - question: How?
        answer: Easily
        position: 2
  images:
    - file: screenshots/product.png
      activate: {de: true, en: true}
      preview: {de: false, en: true}
      position: 1
      priority: 2
  image_directory: screenshots
  demo_shops:
    - type: frontend
      link: https://example.com
      localization: en_GB
      login_name: demo
      login_password: example
"#;

#[test]
fn parse_entire_extension_config() {
    let actual = parse("/plugin/.config/shopware-extension.yml", ENTIRE);
    let translated = |de: Option<&str>, en: Option<&str>| ConfigTranslated {
        german: de.map(str::to_owned),
        english: en.map(str::to_owned),
    };
    let list = |en: &[&str]| ConfigTranslated {
        german: None,
        english: some(strings(en)),
    };
    let expected = Config {
        compatibility_date: "2026-09-01".into(),
        storage_location: "/plugin/.config/shopware-extension.yml".into(),
        decode_error: String::new(),
        build: ConfigBuild {
            extra_bundles: vec![ConfigExtraBundle {
                path: "src/Extra".into(),
                name: "Extra".into(),
            }],
            shopware_version_constraint: "~6.7".into(),
            zip: ConfigBuildZip {
                composer: ConfigBuildZipComposer {
                    enabled: false,
                    before_hooks: strings(&["echo before"]),
                    after_hooks: strings(&["echo after"]),
                    excluded_packages: strings(&["acme/dev"]),
                },
                assets: ConfigBuildZipAssets {
                    enabled: true,
                    enable_asset_caching: true,
                    before_hooks: strings(&["npm run prepare"]),
                    after_hooks: strings(&["npm run cleanup"]),
                    enable_es_build_for_admin: true,
                    enable_es_build_for_storefront: true,
                    disable_sass: true,
                    npm_strict: true,
                    additional_caches: vec![ConfigBuildZipAssetsAdditionalCache {
                        path: "public/built".into(),
                        source_paths: strings(&["src/source"]),
                    }],
                },
                pack: ConfigBuildZipPack {
                    excludes: ConfigBuildZipPackExcludes {
                        paths: strings(&["tests", "docs"]),
                    },
                    before_hooks: strings(&["echo pack"]),
                },
                checksum: ConfigBuildZipChecksum {
                    ignore: strings(&["README.md"]),
                },
            },
        },
        validation: ConfigValidation {
            store_compliance: true,
            php_version: "8.4".into(),
            ignore: ConfigValidationList(vec![
                ConfigValidationIgnore {
                    identifier: "php.lint".into(),
                    ..Default::default()
                },
                ConfigValidationIgnore {
                    identifier: "phpstan".into(),
                    path: "src/Example.php".into(),
                    message: "Known issue".into(),
                },
            ]),
        },
        changelog: ConfigChangelog {
            enabled: true,
            pattern: "^feat:".into(),
            template: "## Changes\n{{ .Message }}".into(),
            variables: [("ticket".to_owned(), "[A-Z]+-\\d+".to_owned())].into(),
            vcs_url: String::new(),
        },
        store: ConfigStore {
            availabilities: some(strings(&["German", "International"])),
            default_locale: some("de_DE".into()),
            localizations: some(strings(&["de_DE", "en_GB"])),
            kind: some("extension".into()),
            icon: some("src/Resources/icon.png".into()),
            automatic_bugfix_version_compatibility: some(false),
            meta_title: translated(Some("Titel"), Some("Title")),
            meta_description: translated(Some("Beschreibung"), Some("Description")),
            description: translated(None, Some("First line\nSecond line\n")),
            installation_manual: translated(None, Some("Install it")),
            tags: list(&["shop", "catalog"]),
            videos: list(&["https://example.com/video"]),
            highlights: list(&["Fast"]),
            features: list(&["Search"]),
            faq: ConfigTranslated {
                german: None,
                english: some(vec![ConfigStoreFaq {
                    question: "How?".into(),
                    answer: "Easily".into(),
                    position: 2,
                }]),
            },
            images: some(vec![ConfigStoreImage {
                file: "screenshots/product.png".into(),
                activate: ConfigStoreImageActivate {
                    german: true,
                    english: true,
                },
                preview: ConfigStoreImagePreview {
                    german: false,
                    english: true,
                },
                position: 1,
                priority: 2,
            }]),
            image_directory: some("screenshots".into()),
            demo_shops: some(vec![ConfigStoreDemoShop {
                kind: "frontend".into(),
                link: "https://example.com".into(),
                localization: "en_GB".into(),
                login_name: "demo".into(),
                login_password: "example".into(),
            }]),
        },
    };
    assert_eq!(actual, expected);
}

#[test]
fn extension_configuration_locations() {
    let root = "/tmp/project";
    for location in CONFIG_LOCATIONS {
        assert_eq!(
            configuration_root(&format!("{root}/{location}")).as_deref(),
            Some(root)
        );
    }
    for location in [
        "shopware-extension.yml",
        ".config/shopware-extension.yaml",
        "other/shopware-extension.yml",
    ] {
        assert_eq!(
            configuration_root(&format!("{root}/{location}")),
            None,
            "{location}"
        );
    }
}

#[test]
fn parse_extension_config_retains_partial_fields() {
    let config = parse(
        "/plugin/.shopware-extension.yml",
        "build:\n  shopwareVersionConstraint: '~6.7'\nvalidation:\n  store_compliance: \"true\"\n",
    );
    assert_eq!(
        config.decode_error,
        "yaml: unmarshal errors:\n  line 0: cannot unmarshal !!str `true` into bool"
    );
    assert_eq!(config.build.shopware_version_constraint, "~6.7");
    assert!(!config.validation.store_compliance);
    assert!(config.build.zip.assets.enabled);
    let config = parse(
        "/plugin/.shopware-extension.yml",
        "validation:\n  store_compliance: true\nbuild:\n  extraBundles: [",
    );
    assert!(config.validation.store_compliance);
}

#[test]
fn parse_store_compliance() {
    for (source, enabled) in [
        ("validation:\n  store_compliance: true\n", true),
        ("validation: {store_compliance: true}", true),
        (
            "validation:\n  store_compliance: true # Store checks\n",
            true,
        ),
        ("validation:\n  store_compliance: false\n", false),
        ("validation:\n  store_compliance: \"true\"\n", false),
        ("validation:\n  store_compliance: \n", false),
        ("validation:\n  other: true\n", false),
        ("store_compliance: true\n", false),
        ("", false),
    ] {
        assert_eq!(
            parse(".shopware-extension.yml", source)
                .validation
                .store_compliance,
            enabled,
            "{source:?}"
        );
    }
}

#[test]
fn yaml_v3_typed_semantics() {
    let config = parse(
        "x.yml",
        "validation:\n  php_version: 8.0\n  ignore: [a, ~, {identifier: [x]}, {identifier: b}]\nstore:\n  icon: {a: b}\n  tags: {en: [one, ~, two]}\n  faq: {en: [{position: 2.9}]}\nbuild:\n  zip:\n    composer:\n      enabled: yes\n",
    );
    assert_eq!(config.validation.php_version, "8.0");
    assert_eq!(
        config
            .validation
            .ignore
            .iter()
            .map(|ignore| ignore.identifier.as_str())
            .collect::<Vec<_>>(),
        ["a", "b"]
    );
    assert_eq!(config.store.icon.as_deref(), Some(""));
    assert_eq!(config.store.tags.english, some(strings(&["one", "two"])));
    assert_eq!(config.store.faq.english.as_ref().unwrap()[0].position, 2);
    assert!(config.build.zip.composer.enabled);
    assert_eq!(
        config.decode_error,
        "yaml: unmarshal errors:\n  line 0: cannot unmarshal !!seq into string\n  line 0: cannot unmarshal !!map into string"
    );
}

#[test]
fn yaml_v3_duplicates_merges_and_fatal_errors() {
    let config = parse(
        "x.yml",
        "build:\n  shopwareVersionConstraint: a\n  shopwareVersionConstraint: b\n",
    );
    assert_eq!(config.build.shopware_version_constraint, "");
    assert_eq!(
        config.decode_error,
        "yaml: unmarshal errors:\n  line 0: mapping key \"shopwareVersionConstraint\" already defined at line 0"
    );

    let config = parse(
        "x.yml",
        "base: &base\n  enabled: true\n  pattern: merged\nchangelog:\n  <<: *base\n  pattern: own\n",
    );
    assert!(config.changelog.enabled);
    assert_eq!(config.changelog.pattern, "own");
    assert_eq!(config.decode_error, "");

    let config = parse(
        "x.yml",
        "build:\n  shopwareVersionConstraint: a\n  extraBundles: [{path: !!int x}]\n",
    );
    assert_eq!(config.build.shopware_version_constraint, "a");
    assert_eq!(
        config.decode_error,
        "yaml: cannot decode !!str `x` as a !!int"
    );
    assert_eq!(
        config.build.extra_bundles,
        vec![ConfigExtraBundle::default()]
    );
}
