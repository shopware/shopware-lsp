use std::collections::HashMap;

use super::*;

#[test]
fn feature_version_boundaries() {
    for definition in all() {
        assert!(supports(
            Version::new(definition.major, definition.minor, 0),
            definition.feature
        ));
        let (mut previous_major, mut previous_minor) = (definition.major, definition.minor - 1);
        if previous_minor < 0 {
            previous_major -= 1;
            previous_minor = 99;
        }
        assert!(!supports(
            Version::new(previous_major, previous_minor, 0),
            definition.feature
        ));
    }
    assert!(!supports_id(Version::new(99, 0, 0), "missing"));
    assert!(supports_id(Version::new(8, 4, 0), "property-hooks"));
}

#[test]
fn feature_ids_round_trip() {
    for feature in Feature::ALL {
        assert_eq!(Feature::from_id(feature.id()), Some(feature));
    }
    assert_eq!(
        all().first().map(|definition| definition.feature),
        Some(Feature::Attributes)
    );
    assert_eq!(
        all().last().map(|definition| definition.feature),
        Some(Feature::PropertyHooks)
    );
}

#[test]
fn unsupported_message_formats_versions() {
    assert_eq!(
        unsupported_message(&lookup(Feature::Enums), Version::new(8, 0, 3)),
        "Enums require PHP 8.1; the project is configured for PHP 8.0"
    );
}

#[test]
fn detect_modern_php_features() {
    let source = r#"<?php
#[Example]
readonly class Modern {
    public const string KIND = 'modern';
    public private(set) string $name {
        get => $this->name;
        set(string $value) { $this->name = $value; }
    }
    public function __construct(public readonly string $id) {}
    public function run(A&B $both, (A&B)|C $dnf): A|B {
        $value = $this?->factory(name: 'x');
        return match ($value) { null => throw new Error(), default => $value };
    }
}
enum State { case Ready; }
"#;
    let parsed = swlsp_parser_php::parse(source);
    assert!(parsed.errors.is_empty());
    let occurrences = detect(&parsed.root());
    let mut counts: HashMap<Feature, usize> = HashMap::new();
    for occurrence in &occurrences {
        *counts.entry(occurrence.feature).or_default() += 1;
        assert!(occurrence.range.end() > occurrence.range.start());
        assert!(usize::from(occurrence.range.end()) <= source.len());
    }
    for feature in Feature::ALL {
        assert_eq!(counts.get(&feature), Some(&1), "{feature:?}");
    }
}

#[test]
fn detect_ignores_ordinary_declarations() {
    let parsed = swlsp_parser_php::parse(
        "<?php\nclass Legacy {\n    public const KIND = 'legacy';\n    private string $name;\n    public function __construct(string $id) {}\n}",
    );
    assert!(parsed.errors.is_empty());
    assert!(detect(&parsed.root()).is_empty());
}
