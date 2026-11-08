use std::collections::BTreeMap;
use std::path::Path;

use super::*;

fn decode_str(source: &str) -> Result<Partial, ConfigError> {
    decode(source.as_bytes())
}

fn error_of(source: &str) -> String {
    decode_str(source).unwrap_err().to_string()
}

fn write(path: &str, content: &str) {
    std::fs::create_dir_all(Path::new(path).parent().unwrap()).unwrap();
    std::fs::write(path, content).unwrap();
}

#[test]
fn resolve_layers_and_explicit_empty_arrays() {
    let project = decode_str(
        r#"{
        "version": 1,
        "php": {"extensions": ["redis"]},
        "features": {"hover": false},
        "indexing": {"exclude": ["**/generated/**"]},
        "diagnostics": {"rules": {"php.arguments": "error"}},
        "check": {"failOn": "warning"}
    }"#,
    )
    .unwrap();
    let editor = Partial {
        php: Some(PhpConfig {
            extensions: Some(Vec::new()),
            ..PhpConfig::default()
        }),
        features: BTreeMap::from([("hover".to_owned(), true)]),
        indexing: Some(IndexingConfig {
            exclude: Some(Vec::new()),
            ..IndexingConfig::default()
        }),
        diagnostics: Some(DiagnosticsConfig {
            enabled: Some(true),
            rules: BTreeMap::from([("php.arguments".to_owned(), Severity::Off)]),
            ..DiagnosticsConfig::default()
        }),
        ..Partial::default()
    };
    let effective = resolve(&project, &editor);
    assert_eq!(effective.php.extensions, Some(Vec::new()));
    assert!(effective.features["hover"]);
    assert!(effective.indexing.exclude.is_empty());
    assert_eq!(effective.diagnostics.rules["php.arguments"], Severity::Off);
    assert_eq!(effective.check.fail_on, Severity::Warning);
    assert_eq!(effective.origins["php.extensions"], "editor");
}

#[test]
fn schema_catalogs_stay_in_sync() {
    let schema: serde_json::Value = serde_json::from_str(SCHEMA).unwrap();
    let assert_catalog = |definition: &str, catalog: &[CatalogEntry]| {
        let mut actual: Vec<&str> = schema["$defs"][definition]["properties"]
            .as_object()
            .unwrap()
            .keys()
            .map(String::as_str)
            .collect();
        let mut expected: Vec<&str> = catalog.iter().map(|entry| entry.id).collect();
        actual.sort_unstable();
        expected.sort_unstable();
        assert_eq!(expected, actual, "{definition}");
    };
    assert_catalog("featureMap", FEATURE_CATALOG);
    assert_catalog("mcpToolMap", MCP_TOOL_CATALOG);
    assert_catalog("domainMap", DOMAIN_CATALOG);
}

#[test]
fn schema_top_level_properties_match_partial_fields() {
    let schema: serde_json::Value = serde_json::from_str(SCHEMA).unwrap();
    let mut properties: Vec<&str> = schema["properties"]
        .as_object()
        .unwrap()
        .keys()
        .map(String::as_str)
        .collect();
    properties.sort_unstable();
    assert_eq!(
        properties,
        [
            "$schema",
            "check",
            "diagnostics",
            "domains",
            "features",
            "indexing",
            "mcp",
            "php",
            "shopware",
            "version"
        ]
    );
    assert_eq!(
        schema["properties"]["shopware"]["properties"]["targetVersion"]["pattern"],
        r"^$|^v?[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:\.[0-9]+)?(?:[-+][0-9A-Za-z.-]+)?$"
    );
}

#[test]
fn load_uses_shopware_lsp_configuration_path() {
    let directory = tempfile::tempdir().unwrap();
    let root = directory.path().to_str().unwrap();
    assert_eq!(path(root), format!("{root}/.config/shopware/lsp.yaml"));
    write(
        &format!("{root}/.config/shopware/lsp.json"),
        r#"{"version":1}"#,
    );
    assert_eq!(load(root).unwrap(), None);

    write(&path(root), "version: 1\n");
    assert!(load(root).unwrap().is_some());

    write(&path(root), "version: 2\n");
    assert_eq!(
        load(root).unwrap_err().to_string(),
        format!("{}: unsupported configuration version 2", path(root))
    );
}

#[test]
fn resolve_cascades_disabled_dependencies() {
    let project = decode_str(r#"{"version":1,"domains":{"php":false}}"#).unwrap();
    let effective = resolve(&project, &Partial::default());
    assert!(!effective.domains["symfony.services"]);
    assert!(!effective.domains["twig"]);
    assert!(effective.disabled_reason["twig"].contains("requires"));
    assert_eq!(
        effective.disabled_reason["shopware.entitySchema"],
        "requires shopware.dal"
    );
    assert!(effective.domains["scss"]);
}

#[test]
fn decode_rejects_unknown_and_invalid_values() {
    for (source, expected) in [
        (
            r#"{"version":1,"domains":{"symfony.stimulus":false}}"#,
            "unknown domain",
        ),
        (
            r#"{"version":1,"domains":{"symfony.forms":false}}"#,
            r#"unknown domain "symfony.forms""#,
        ),
        (
            r#"{"version":1,"features":{"future":false}}"#,
            "unknown feature",
        ),
        (
            r#"{"version":1,"diagnostics":{"rules":{"php.arguments":"loud"}}}"#,
            "invalid severity",
        ),
        (r#"{"version":1,"unknown":true}"#, "field unknown not found"),
        (
            r#"{"version":1,"shopware":{"targetVersion":"next"}}"#,
            "invalid Shopware target version",
        ),
        (
            r#"{"version":1,"mcp":{"tools":{"future_tool":false}}}"#,
            "unknown MCP tool",
        ),
        (
            r#"{"version":1,"diagnostics":{"overrides":[{"files":[],"enabled":false}]}}"#,
            "files must not be empty",
        ),
        (
            r#"{"version":1,"diagnostics":{"overrides":[{"files":["../outside/**"],"enabled":false}]}}"#,
            "must not escape",
        ),
    ] {
        let error = error_of(source);
        assert!(error.contains(expected), "{source}: {error}");
    }
}

/// Expected messages were produced by the Go implementation.
#[test]
fn decode_errors_match_go() {
    for (source, expected) in [
        ("", "decode configuration: EOF"),
        ("# only\n", "decode configuration: EOF"),
        ("---\n", "configuration version is required"),
        (
            "version: 1\n---\nversion: 1\n",
            "decode configuration: multiple JSON values",
        ),
        ("version: 2\n", "unsupported configuration version 2"),
        (
            "version: x\n",
            "decode configuration: yaml: unmarshal errors:\n  line 1: cannot unmarshal !!str `x` into int",
        ),
        (
            "version: 1\nfeatures: [1]\n",
            "decode configuration: yaml: unmarshal errors:\n  line 2: cannot unmarshal !!seq into map[string]bool",
        ),
        (
            "version: 1\nunknown: 1\nother: 2\n",
            "decode configuration: yaml: unmarshal errors:\n  line 2: field unknown not found in type projectconfig.Partial\n  line 3: field other not found in type projectconfig.Partial",
        ),
        (
            "version: 1\nfeatures:\n  hover: maybe\n",
            "decode configuration: yaml: unmarshal errors:\n  line 3: cannot unmarshal !!str `maybe` into bool",
        ),
        (
            "version: 1\nindexing:\n  exclude: ['/abs']\n",
            r#"indexing.exclude: pattern "/abs" must be relative"#,
        ),
        (
            "version: 1\nindexing:\n  exclude: ['C:/x']\n",
            r#"indexing.exclude: pattern "C:/x" must be relative"#,
        ),
        (
            "version: 1\nindexing:\n  exclude: ['src/[broken']\n",
            r#"indexing.exclude: compile glob "src/[broken": unclosed character class"#,
        ),
        (
            "version: 1\ncheck:\n  severity: off\n",
            r#"invalid check severity "off""#,
        ),
        (
            "version: 1\ncheck:\n  failOn: loud\n",
            r#"invalid check failOn severity "loud""#,
        ),
        (
            "version: 1\nphp:\n  extensions: ['  ']\n",
            "php.extensions must not contain an empty extension",
        ),
        (
            "version: 1\ndiagnostics:\n  overrides:\n    - files: ['x']\n",
            "diagnostics.overrides[0] must configure enabled, inspections, or rules",
        ),
        (
            "version: 1\ndiagnostics:\n  rules:\n    ' ': error\n",
            "diagnostic rule ID must not be empty",
        ),
        (
            "5",
            "decode configuration: yaml: unmarshal errors:\n  line 1: cannot unmarshal !!int `5` into projectconfig.Partial",
        ),
        (
            "version: 1\ndiagnostics:\n  overrides: [{files: ['a'], rules: {x: bad}}]\n",
            r#"diagnostics.overrides[0]: invalid severity "bad" for diagnostic "x""#,
        ),
    ] {
        assert_eq!(error_of(source), expected, "{source:?}");
    }
    // Syntax errors and duplicate keys are rejected; the wording comes from
    // the YAML library and differs from yaml.v3.
    assert!(error_of("version: 1\na: [\n").starts_with("decode configuration: yaml: line "));
    assert!(error_of("version: 1\nversion: 1\n").starts_with("decode configuration: "));
}

#[test]
fn decode_keeps_scalar_text_for_strings() {
    let value = decode_str("version: 1\nshopware:\n  targetVersion: 6.5\n").unwrap();
    assert_eq!(
        serde_json::to_string(&value).unwrap(),
        r#"{"version":1,"shopware":{"targetVersion":"6.5"}}"#
    );
}

#[test]
fn decode_yaml_indexing_exclusions() {
    let project = decode_str(
        "version: 1\nindexing:\n  exclude:\n    - \"**/generated/**\"\n    - custom/plugins/{Legacy,Archived}/**/*.php\n",
    )
    .unwrap();
    let effective = resolve(&project, &Partial::default());
    assert_eq!(
        effective.indexing.exclude,
        [
            "**/generated/**",
            "custom/plugins/{Legacy,Archived}/**/*.php"
        ]
    );
}

#[test]
fn resolve_normalizes_lists() {
    let project = decode_str(
        "version: 1\nphp:\n  extensions: [' redis ', Redis, apcu]\nindexing:\n  exclude: ['./a/**', 'a/**', 'b\\c']\n",
    )
    .unwrap();
    let effective = resolve(&project, &Partial::default());
    assert_eq!(effective.php.extensions.unwrap(), ["apcu", "redis"]);
    assert_eq!(effective.indexing.exclude, ["a/**", "b/c"]);
}

#[test]
fn resolve_indexing_max_file_size() {
    assert_eq!(Effective::default().indexing.max_file_size_mib, 8);
    let project = decode_str("version: 1\nindexing:\n  maxFileSizeMiB: 64\n").unwrap();
    let effective = resolve(&project, &Partial::default());
    assert_eq!(effective.indexing.max_file_size_mib, 64);
    assert_eq!(effective.origins["indexing.maxFileSizeMiB"], "project");

    let editor = Partial {
        indexing: Some(IndexingConfig {
            max_file_size_mib: Some(0),
            ..IndexingConfig::default()
        }),
        ..Partial::default()
    };
    let effective = resolve(&project, &editor);
    assert_eq!(effective.indexing.max_file_size_mib, 0);
    assert_eq!(effective.origins["indexing.maxFileSizeMiB"], "editor");
}

#[test]
fn decode_rejects_invalid_indexing_max_file_size() {
    for size in ["-1", "4096"] {
        let error = error_of(&format!(
            "version: 1\nindexing:\n  maxFileSizeMiB: {size}\n"
        ));
        assert!(error.contains("indexing.maxFileSizeMiB"), "{error}");
    }
}

#[test]
fn decode_rejects_unsafe_or_malformed_indexing_exclusions() {
    for source in [
        "version: 1\nindexing:\n  exclude: ['../outside/**']\n",
        "version: 1\nindexing:\n  exclude: ['src/[broken']\n",
        "version: 1\nindexing:\n  exclude: ['']\n",
    ] {
        assert!(decode_str(source).is_err(), "{source}");
    }
}

#[test]
fn resolve_mcp_tool_overrides() {
    let project =
        decode_str(r#"{"version": 1, "mcp": {"tools": {"shopware_scaffold": false}}}"#).unwrap();
    let editor = Partial {
        mcp: Some(McpConfig {
            tools: BTreeMap::from([
                ("shopware_scaffold".to_owned(), true),
                ("shopware_hover".to_owned(), false),
            ]),
        }),
        ..Partial::default()
    };
    let effective = resolve(&project, &editor);
    assert!(effective.mcp_tool_enabled("shopware_scaffold"));
    assert!(!effective.mcp_tool_enabled("shopware_hover"));
    assert!(effective.mcp_tool_enabled("shopware_diagnostics"));
    assert_eq!(effective.origins["mcp.tools.shopware_scaffold"], "editor");
}

#[test]
fn effective_accessors() {
    let mut effective = Effective::default();
    assert!(effective.feature_enabled("hover"));
    assert!(!effective.feature_enabled("unknown"));
    assert!(effective.domain_enabled("php"));
    assert!(effective.inspection_enabled("anything"));
    effective
        .diagnostics
        .inspections
        .insert("x".to_owned(), false);
    assert!(!effective.inspection_enabled("x"));
    effective.diagnostics.enabled = false;
    assert!(!effective.inspection_enabled("anything"));
    assert_eq!(effective.rule_severity("x"), None);
    effective
        .diagnostics
        .rules
        .insert("x".to_owned(), Severity::Hint);
    assert_eq!(effective.rule_severity("x"), Some(&Severity::Hint));
}

#[test]
fn apply_diagnostics_uses_ordered_path_overrides() {
    let configuration = DiagnosticsConfig {
        rules: BTreeMap::from([("php.arguments".to_owned(), Severity::Warning)]),
        overrides: vec![
            DiagnosticOverride {
                files: vec!["src/**".to_owned()],
                enabled: Some(false),
                ..DiagnosticOverride::default()
            },
            DiagnosticOverride {
                files: vec![r"src\Generated\Keep.php".to_owned()],
                enabled: Some(true),
                rules: BTreeMap::from([("php.arguments".to_owned(), Severity::Off)]),
                ..DiagnosticOverride::default()
            },
        ],
        ..DiagnosticsConfig::default()
    };
    let mut policy = DiagnosticPolicy::default();
    apply_diagnostics(&mut policy, Some(&configuration), "src/Generated/Keep.php");
    assert!(policy.enabled);
    assert_eq!(policy.rules["php.arguments"], Severity::Off);

    let mut other = DiagnosticPolicy::default();
    apply_diagnostics(&mut other, Some(&configuration), "tests/Example.php");
    assert!(other.enabled);
    assert_eq!(other.rules["php.arguments"], Severity::Warning);

    let mut disabled = DiagnosticPolicy::default();
    apply_diagnostics(&mut disabled, Some(&configuration), "./src/Other.php");
    assert!(!disabled.enabled);
    apply_diagnostics(&mut disabled, None, "src/Other.php");
    assert!(!disabled.enabled);
}

#[test]
fn load_scopes_discovers_nested_diagnostics_configurations() {
    let directory = tempfile::tempdir().unwrap();
    let root = directory.path().to_str().unwrap();
    let plugin = format!("{root}/custom/plugins/Example");
    write(
        &path(&plugin),
        r#"{"version":1,"diagnostics":{"rules":{"php.arguments":"off"}}}"#,
    );
    let invalid = format!("{root}/custom/plugins/Invalid");
    write(&path(&invalid), r#"{"version":1,"domains":{"php":false}}"#);
    // Skipped directories and the workspace root itself are ignored.
    write(&path(&format!("{root}/vendor/acme/lib")), "version: 1\n");
    write(&path(root), "version: 1\n");

    let scopes = load_scopes(root).unwrap();
    assert_eq!(scopes.len(), 2);
    assert_eq!(scopes[0].root, plugin);
    assert!(scopes[0].error.is_empty());
    assert_eq!(
        scopes[0].configuration.diagnostics.as_ref().unwrap().rules["php.arguments"],
        Severity::Off
    );
    assert_eq!(scopes[1].root, invalid);
    let error = scope_errors(&scopes).unwrap_err().to_string();
    assert!(
        error.contains("nested configuration may only contain diagnostics"),
        "{error}"
    );
    assert!(scope_errors(&scopes[..1]).is_ok());
}

#[cfg(unix)]
#[test]
fn load_scopes_follows_symlinks_without_loops() {
    let directory = tempfile::tempdir().unwrap();
    let root = directory.path().to_str().unwrap();
    let plugin = format!("{root}/plugins/Example");
    write(
        &path(&plugin),
        "version: 1\ndiagnostics:\n  enabled: false\n",
    );
    std::os::unix::fs::symlink(format!("{root}/plugins"), format!("{root}/linked")).unwrap();
    std::os::unix::fs::symlink(root, format!("{root}/plugins/loop")).unwrap();

    let scopes = load_scopes(root).unwrap();
    let roots: Vec<&str> = scopes.iter().map(|scope| scope.root.as_str()).collect();
    // Like Go, only symlink targets are deduplicated: the real directory and
    // its symlinked alias are both walked, while the loop back to the root
    // is skipped.
    assert_eq!(
        roots,
        [
            format!("{root}/linked/Example"),
            format!("{root}/plugins/Example")
        ]
    );
}

#[test]
fn scopes_are_sorted_by_depth_then_path() {
    let directory = tempfile::tempdir().unwrap();
    let root = directory.path().to_str().unwrap();
    for nested in ["b", "a/deep", "a"] {
        write(
            &path(&format!("{root}/{nested}")),
            "version: 1\ndiagnostics:\n  enabled: false\n",
        );
    }
    let scopes = load_scopes(root).unwrap();
    let roots: Vec<String> = scopes.into_iter().map(|scope| scope.root).collect();
    assert_eq!(
        roots,
        [
            format!("{root}/a"),
            format!("{root}/b"),
            format!("{root}/a/deep")
        ]
    );
}

#[test]
fn contains_checks_lexical_ownership() {
    assert!(contains("/workspace", "/workspace/src/a.php"));
    assert!(contains("/workspace/", "/workspace"));
    assert!(!contains("/workspace", "/workspace2/a.php"));
    assert!(!contains("/workspace/src", "/workspace/a.php"));
    assert!(contains("/workspace", "/workspace/..data/a.php"));
}

#[test]
fn validate_nested_rejects_structural_settings() {
    assert!(validate_nested(&Partial::default()).is_ok());
    let value = Partial {
        check: Some(CheckConfig::default()),
        ..Partial::default()
    };
    assert!(validate_nested(&value).is_err());
}

#[test]
fn structural_fingerprint_ignores_live_settings() {
    let first = Effective::default();
    let mut second = Effective::default();
    second.features.insert("hover".to_owned(), false);
    second.diagnostics.enabled = false;
    assert_eq!(
        first.structural_fingerprint(),
        second.structural_fingerprint()
    );
    second.domains.insert("scss".to_owned(), false);
    assert_ne!(
        first.structural_fingerprint(),
        second.structural_fingerprint()
    );
    let mut second = Effective::default();
    second.indexing.exclude = vec!["**/generated/**".to_owned()];
    assert_ne!(
        first.structural_fingerprint(),
        second.structural_fingerprint()
    );
    let mut second = Effective::default();
    second.indexing.max_file_size_mib += 1;
    assert_ne!(
        first.structural_fingerprint(),
        second.structural_fingerprint()
    );
}

/// Digests were produced by the Go implementation.
#[test]
fn structural_fingerprint_matches_go() {
    let mut value = Effective::default();
    assert_eq!(
        value.structural_fingerprint(),
        "831ab52afd850603f2860379dd3ddfdc8e710a4b5aa7c6c68a882584a5bb98d1"
    );
    value.indexing.exclude = vec!["a<&>b/**".to_owned()];
    value.php.extensions = Some(Vec::new());
    assert_eq!(
        value.structural_fingerprint(),
        "2164e3f81b92ca769cbbe732fb4e95f272601b7b7568d1f9fdff28725c24a6d2"
    );
}

#[test]
fn effective_json_matches_go() {
    let encoded = serde_json::to_string(&Effective::default()).unwrap();
    assert!(encoded.starts_with(
        r#"{"php":{"extensions":null,"disabledExtensions":null},"shopware":{"targetVersion":""},"features":{"callHierarchy":true,"#
    ));
    assert!(encoded.contains(r#""indexing":{"enabled":true,"maxFileSizeMiB":8},"#));
    assert!(encoded.ends_with(
        r#""twig":true},"diagnostics":{"enabled":true,"inspections":{},"rules":{}},"check":{"severity":"warning","failOn":"off"}}"#
    ));
}

#[test]
fn partial_json_round_trip_tolerates_nulls() {
    let value: Partial = serde_json::from_str(
        r#"{"version":1,"features":null,"php":{"extensions":null},"diagnostics":{"rules":{"x":"error"},"overrides":null},"check":{"failOn":"loud"},"extra":true}"#,
    )
    .unwrap();
    assert_eq!(value.php, Some(PhpConfig::default()));
    assert_eq!(
        value.check.as_ref().unwrap().fail_on,
        Some(Severity::Other("loud".to_owned()))
    );
    assert_eq!(
        serde_json::to_string(&value).unwrap(),
        r#"{"version":1,"php":{},"diagnostics":{"rules":{"x":"error"}},"check":{"failOn":"loud"}}"#
    );
}

#[test]
fn catalog_entries_serialize_like_go() {
    let twig = DOMAIN_CATALOG
        .iter()
        .find(|entry| entry.id == "twig")
        .unwrap();
    assert_eq!(
        serde_json::to_string(twig).unwrap(),
        r#"{"id":"twig","label":"Twig","dependsOn":["php","symfony.services"]}"#
    );
    assert_eq!(
        serde_json::to_string(&FEATURE_CATALOG[0]).unwrap(),
        r#"{"id":"completion","label":"Completion"}"#
    );
}
