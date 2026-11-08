//! Environment semantics (ported from the Go environment tests; Go's
//! handle-layout assertions are replaced by equivalent semantic checks).

use swlsp_php_types::{Relations, Type};

use crate::env::{Env, SMALL_ENVIRONMENT_LIMIT, join_environments};

#[test]
fn environment_fork_copies_on_write() {
    let original = Env::new(2);
    original.set("$value", Type::INT);
    let fork = original.fork();
    assert_eq!(fork.get("$value"), Some(Type::INT));

    // Writes made after the fork remain isolated in both directions.
    original.set("$after", Type::BOOL);
    assert_eq!(fork.get("$after"), None);
    fork.set("$value", Type::STRING);
    assert_eq!(original.get("$value"), Some(Type::INT));
    assert_eq!(fork.get("$value"), Some(Type::STRING));

    // Handle copies retain reference semantics.
    let alias = original.clone();
    alias.set("$shared", Type::FLOAT);
    assert_eq!(original.get("$shared"), Some(Type::FLOAT));
    assert!(alias.same_handle(&original));

    for index in 0..=SMALL_ENVIRONMENT_LIMIT {
        original.set(&format!("$item{index}"), Type::INT);
    }
    assert!(original.is_map());
    let large_fork = original.fork();
    original.set("$item0", Type::STRING);
    assert_eq!(large_fork.get("$item0"), Some(Type::INT));
}

#[test]
fn small_environment_overwrites_and_deletes() {
    let env = Env::new(1);
    env.set("$value", Type::INT);
    let fork = env.fork();
    fork.set("$value", Type::STRING);
    assert_eq!(env.get("$value"), Some(Type::INT));
    assert_eq!(fork.get("$value"), Some(Type::STRING));
    env.set("$other", Type::BOOL);
    assert_eq!(env.len(), 2);
    env.delete_prefix("$other");
    assert_eq!(env.len(), 1);
}

#[test]
fn environment_fork_skips_noop_copies() {
    let original = Env::new(SMALL_ENVIRONMENT_LIMIT + 1);
    for index in 0..=SMALL_ENVIRONMENT_LIMIT {
        original.set(&format!("$item{index}"), Type::INT);
    }
    let unchanged = original.fork();
    unchanged.set("$item0", Type::INT);
    assert!(unchanged.storage_shared());
    let without_matching_prefix = original.fork();
    without_matching_prefix.delete_prefix("$missing->");
    assert!(without_matching_prefix.storage_shared());

    original.set("$item0", Type::STRING);
    for env in [&unchanged, &without_matching_prefix] {
        assert_eq!(env.get("$item0"), Some(Type::INT));
    }
}

#[test]
fn environment_fork_keeps_values_after_override() {
    let original = Env::new(2);
    original.set("$value", Type::INT);
    original.set("$stable", Type::BOOL);
    let fork = original.fork();
    fork.set("$value", Type::STRING);
    assert_eq!(fork.len(), 2);
    let mut visited = std::collections::BTreeMap::new();
    fork.visit(|name, value| {
        visited.insert(name.to_owned(), value.clone());
    });
    assert_eq!(
        visited,
        [
            ("$stable".to_owned(), Type::BOOL),
            ("$value".to_owned(), Type::STRING)
        ]
        .into_iter()
        .collect()
    );
    let cloned_override = fork.fork();
    fork.set("$value", Type::FLOAT);
    assert_eq!(cloned_override.get("$value"), Some(Type::STRING));
    fork.set("$added", Type::NULL);
    assert_eq!(fork.len(), 3);

    let with_derived = original.fork();
    with_derived.set("$value->name", Type::STRING);
    with_derived.delete_prefix("$value->");
    assert_eq!(with_derived.get("$value->name"), None);
    assert_eq!(original.get("$value"), Some(Type::INT));
}

#[test]
fn empty_environment_copies_share_first_write() {
    let original = Env::new(0);
    let alias = original.clone();
    alias.set("$shared", Type::STRING);
    assert_eq!(original.get("$shared"), Some(Type::STRING));
}

#[test]
fn join_keeps_one_sided_values_and_joins_shared() {
    let left = Env::new(5);
    let right = Env::new(5);
    left.set("$a", Type::INT);
    left.set("$both", Type::INT);
    right.set("$b", Type::STRING);
    right.set("$both", Type::STRING);
    let joined = join_environments(Relations::default(), &left, &right);
    assert_eq!(joined.get("$a"), Some(Type::INT));
    assert_eq!(joined.get("$b"), Some(Type::STRING));
    assert_eq!(joined.get("$both").unwrap().to_string(), "int|string");
}
