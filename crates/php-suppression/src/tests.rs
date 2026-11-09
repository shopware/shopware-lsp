use super::*;

fn offset(source: &str, value: &str) -> u32 {
    source.find(value).unwrap() as u32
}

fn last_offset(source: &str, value: &str) -> u32 {
    source.rfind(value).unwrap() as u32
}

#[test]
fn phpstan_directives_match_specific_diagnostics() {
    let source = "<?php
/** @phpstan-ignore return.type, argument.type (known limitation) */
consume();
/** @phpstan-ignore function.deprecated */ legacy();
/** @phpstan-ignore arguments.count */ legacy();
// @phpstan-ignore-next-line
unknown();
safe(); // @phpstan-ignore-line - legacy blanket form
";
    let set = parse(source);
    assert!(set.suppresses(offset(source, "consume();"), "php.returnType"));
    assert!(set.suppresses(offset(source, "consume();"), "php.arguments"));
    assert!(!set.suppresses(offset(source, "consume();"), "php.undefined"));
    assert!(set.suppresses(offset(source, "legacy();"), "php.deprecated"));
    assert!(!set.suppresses(offset(source, "legacy();"), "php.arguments"));
    let second_legacy = last_offset(source, "legacy();");
    assert!(set.suppresses(second_legacy, "php.arguments"));
    assert!(!set.suppresses(second_legacy, "php.deprecated"));
    assert!(set.suppresses(offset(source, "unknown();"), "php.undefined"));
    assert!(set.suppresses(offset(source, "safe();"), "php.returnType"));
}

#[test]
fn block_and_no_inspection_directives_target_next_code_line() {
    let source = "<?php
/**
 * @phpstan-ignore return.type
 */

function value(): string { return 1; }
/** @noinspection PhpDeprecationInspection */
legacy();
/** @noinspection PhpParamsInspection */
legacy();
";
    let set = parse(source);
    let function_offset = offset(source, "function value");
    assert!(set.suppresses(function_offset, "php.returnType"));
    assert!(!set.suppresses(function_offset, "php.arguments"));
    let first_legacy = offset(source, "legacy();");
    assert!(set.suppresses(first_legacy, "php.deprecated"));
    assert!(!set.suppresses(first_legacy, "php.arguments"));
    let second_legacy = last_offset(source, "legacy();");
    assert!(set.suppresses(second_legacy, "php.arguments"));
    assert!(!set.suppresses(second_legacy, "php.deprecated"));
}

#[test]
fn unrelated_suppression_does_not_hide_diagnostic() {
    let source = "<?php
/** @phpstan-ignore shopware.domainException */
missing();
";
    let set = parse(source);
    let missing = offset(source, "missing");
    assert!(!set.suppresses(missing, "php.undefined"));
    assert!(!set.suppresses(missing, "php.arguments"));
}

#[test]
fn no_inspection_language_level_alias_targets_version_diagnostics() {
    let source = "<?php
/** @noinspection PhpVersionInspection */
readonly class Subject {}
";
    let set = parse(source);
    let readonly = offset(source, "readonly");
    assert!(set.suppresses(readonly, "php.version"));
    assert!(!set.suppresses(readonly, "php.undefined"));
}

#[test]
fn suppression_markers_remain_case_insensitive() {
    for marker in [
        "@phpstan-ignore",
        "@PHPSTAN-IGNORE",
        "@PhpStan-Ignore",
        "@phpstan-ignore-next-line",
        "@PHPSTAN-IGNORE-NEXT-LINE",
        "@noinspection",
        "@NOINSPECTION",
        "NoInspection",
    ] {
        let source = format!("<?php\n// {marker}\nmissing();\n");
        let set = parse(&source);
        assert!(
            set.suppresses(offset(&source, "missing"), "php.undefined"),
            "{marker}"
        );
    }
}

/// Compares the prefilter against a plain byte-window search (fuzz corpus
/// seeds plus a few extra cases).
#[test]
fn suppression_marker_prefilter_matches_window_search() {
    fn window_search(source: &str) -> bool {
        let bytes = source.as_bytes();
        ["@phpstan-ignore", "@noinspection", "noinspection"]
            .iter()
            .any(|marker| {
                bytes
                    .windows(marker.len())
                    .any(|window| window.eq_ignore_ascii_case(marker.as_bytes()))
            })
    }
    for source in [
        "",
        "<?php function nothing() {}",
        "@PHPSTAN-IGNORE",
        "@noinspection",
        "NOINSPECTION",
        "@phpſtan-ignore",
        "noinſpection",
        "😀 @phpstan-ignore",
        "@phpstan-ignor",
        "\u{FFFD}noinspection",
        "x@phpstan-ignorex",
        "nnoinspection",
    ] {
        assert_eq!(
            contains_suppression_marker(source),
            window_search(source),
            "{source:?}"
        );
    }
}

#[test]
fn empty_set_suppresses_nothing() {
    let set = parse("<?php\nmissing();\n");
    assert!(set.is_empty());
    assert!(!set.suppresses(0, "php.undefined"));
    let set = parse("<?php // @phpstan-ignore-next-line\n");
    assert!(!set.suppresses(0, ""));
}

#[test]
fn wildcard_and_identifier_aliases() {
    let source = "<?php
missing(); // @phpstan-ignore-line
other(); // @phpstan-ignore-line (reason)
third(); // @phpstan-ignore-line not.matching
fourth(); /** @noinspection PhpUnusedAliasInspection */
fifth(); // @phpstan-ignore *
";
    let set = parse(source);
    assert!(set.suppresses(offset(source, "missing"), "php.anything"));
    assert!(set.suppresses(offset(source, "other"), "php.anything"));
    assert!(!set.suppresses(offset(source, "third"), "php.undefined"));
    assert!(set.suppresses(offset(source, "fourth"), "php.unusedImport"));
    assert!(set.suppresses(offset(source, "fifth"), "php.parse"));
}

#[test]
fn inline_directive_applies_to_code_before_and_after_comment() {
    let source = "<?php
call(); /** @phpstan-ignore argument.type */
/* @phpstan-ignore argument.type */ after();
/*
 * prose
 */ /* @phpstan-ignore argument.type
 */
/* note */ target();
";
    let set = parse(source);
    assert!(set.suppresses(offset(source, "call"), "php.arguments"));
    assert!(set.suppresses(offset(source, "after"), "php.arguments"));
    assert!(set.suppresses(offset(source, "target"), "php.arguments"));
}

#[test]
fn offsets_past_the_end_use_the_last_line() {
    let source = "<?php\n// @phpstan-ignore-next-line\nmissing();";
    let set = parse(source);
    assert!(set.suppresses(u32::MAX, "php.undefined"));
}

#[test]
fn phpstan_directive_stops_at_first_non_dotted_word() {
    let directive = parse_directive(" because return.type", false);
    assert_eq!(
        directive,
        Directive {
            identifiers: Vec::new(),
            wildcard: true,
        }
    );
    let directive = parse_directive(" [Return.Type], cast.string; */", false);
    assert_eq!(directive.identifiers, ["return.type", "cast.string"]);
    let directive = parse_directive(" PhpUndefinedMethodInspection, Other", true);
    assert_eq!(
        directive.identifiers,
        ["phpundefinedmethodinspection", "other"]
    );
}
