//! Port of `member_links_test.go`.

use std::sync::Arc;

use swlsp_php_binder::link;
use swlsp_php_semantic::{Document, NameKind, Snapshot, SymbolKind};

use super::{analyzer, bind, parse, snapshot};
use crate::link_members;

fn analyze_and_link(path: &str, source: &str) -> (Document, Arc<Snapshot>) {
    let parsed = parse(source);
    let document = bind(path, &parsed);
    let snapshot = snapshot(std::slice::from_ref(&document));
    let document = link(&document, &snapshot);
    let document = analyzer(&snapshot, Vec::new()).analyze(&document, &parsed.root);
    (link_members(&document, &snapshot, &parsed.root), snapshot)
}

#[test]
fn link_members_uses_inferred_receiver_type() {
    let source = r#"<?php
namespace App;
class Service {
    public function value(): string { return 'ok'; }
}
class Consumer {
    public function __construct(private Service $service) {}
    public function run(): string { return $this->service->value(); }
}"#;
    let (document, snapshot) = analyze_and_link("/project/member.php", source);
    let offset = source.rfind("value").unwrap() as u32;
    let mut found = false;
    for reference in &document.references {
        if reference.range.contains(offset.into()) && !reference.resolved.is_empty() {
            let symbol = snapshot
                .symbol(&reference.resolved)
                .expect("resolved symbol");
            assert_eq!(symbol.kind, SymbolKind::Method);
            assert_eq!(&*symbol.name, "value");
            assert!(reference.candidate_ids().is_empty());
            found = true;
        }
    }
    assert!(found);
}

#[test]
fn link_members_infers_receiver_inside_member_assignment() {
    let source = r#"<?php
class Service {
    public private(set) string $value = '';
}
function update(Service $service): void {
    $service->value = 'changed';
}"#;
    let (document, _) = analyze_and_link("/project/member-write.php", source);
    let reference = document
        .references
        .iter()
        .find(|reference| reference.kind == NameKind::Member && &*reference.name == "value")
        .expect("member assignment reference was not linked");
    assert!(reference.write);
    assert_eq!(reference.receiver.to_string(), "Service");
    assert!(!reference.resolved.is_empty());
}

#[test]
fn link_members_keeps_narrowed_receiver_for_repeated_call() {
    let source = r#"<?php
abstract class Aggregation {}
class Sorting {
    public function direction(): string {}
}
class TermsAggregation extends Aggregation {
    public function getSorting(): ?Sorting {}
}
function serializeAggregation(Aggregation $aggregation): ?string {
    if ($aggregation instanceof TermsAggregation) {
        if ($aggregation->getSorting()) {
            return $aggregation->getSorting()->direction();
        }
    }
    return null;
}"#;
    let (document, _) = analyze_and_link("/project/repeated-call.php", source);
    let mut count = 0;
    for reference in &document.references {
        if reference.kind != NameKind::Member || &*reference.name != "getSorting" {
            continue;
        }
        count += 1;
        assert_eq!(reference.receiver.to_string(), "TermsAggregation");
        assert!(!reference.resolved.is_empty());
    }
    assert_eq!(count, 2);
}

#[test]
fn link_members_narrows_receiver_inside_while_condition() {
    let source = r#"<?php
class Expr {}
class Identifier {}
class MethodCall extends Expr {
    public Identifier $name;
    public Expr $var;
}
function inspect(Expr $current): void {
    while ($current instanceof MethodCall) {
        $current->name;
        $current = $current->var;
    }
}"#;
    let (document, _) = analyze_and_link("/project/while-narrowing.php", source);
    let mut narrowed = 0;
    for reference in &document.references {
        if reference.kind != NameKind::Member
            || (&*reference.name != "name" && &*reference.name != "var")
        {
            continue;
        }
        narrowed += 1;
        assert_eq!(reference.receiver.to_string(), "MethodCall");
        assert!(!reference.resolved.is_empty());
    }
    assert_eq!(narrowed, 2);
}

#[test]
fn analyzer_reports_return_and_argument_type_mismatches() {
    let source = r#"<?php
namespace App;
class Service {
    public function value(string $input): int { return 'wrong'; }
    public function run(): int { return $this->value(42); }
}"#;
    let parsed = parse(source);
    let document = bind("/project/issues.php", &parsed);
    let snapshot = snapshot(std::slice::from_ref(&document));
    let document = link(&document, &snapshot);
    let document = analyzer(&snapshot, Vec::new()).analyze(&document, &parsed.root);
    let codes: Vec<&str> = document.issues.iter().map(|issue| &*issue.code).collect();
    assert!(codes.contains(&"php.returnType"), "{codes:?}");
    assert!(codes.contains(&"php.arguments"), "{codes:?}");
}

#[test]
fn link_members_distinguishes_static_properties_and_constants() {
    let source = r#"<?php
class Service {
    public static string $name;
    public const string KIND = 'service';
}
function values(): array {
    return [Service::$name, Service::KIND];
}"#;
    let (document, _) = analyze_and_link("/project/static.php", source);
    let (mut property, mut constant) = (false, false);
    for reference in &document.references {
        if reference.kind != NameKind::Member {
            continue;
        }
        match &*reference.name {
            "$name" => {
                property =
                    reference.target_kind == SymbolKind::Property && !reference.resolved.is_empty();
            }
            "KIND" => {
                constant = reference.target_kind == SymbolKind::ClassConstant
                    && !reference.resolved.is_empty();
            }
            _ => {}
        }
    }
    assert!(property, "{:?}", document.references);
    assert!(constant, "{:?}", document.references);
}

#[test]
fn link_members_treats_dynamic_static_class_as_property() {
    let source = r#"<?php
class Kernel {}
class Factory {
    /** @var class-string<Kernel> */
    public static string $kernelClass = Kernel::class;
    public static function create(): Kernel {
        return new static::$kernelClass();
    }
}"#;
    let (document, _) = analyze_and_link("/project/dynamic-static-class.php", source);
    for issue in &document.issues {
        assert_ne!(&*issue.code, "php.returnType", "{}", issue.message);
        assert_ne!(&*issue.code, "php.arguments", "{}", issue.message);
    }
    let reference = document
        .references
        .iter()
        .find(|reference| reference.kind == NameKind::Member && &*reference.name == "$kernelClass")
        .expect("dynamic static class property was not linked");
    assert_eq!(reference.target_kind, SymbolKind::Property);
    assert!(!reference.resolved.is_empty());
}
