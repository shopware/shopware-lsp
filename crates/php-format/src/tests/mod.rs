//! The formatter against PER Coding Style 2.0, section by section. The
//! compliant examples are those of the specification
//! (<https://www.php-fig.org/per/coding-style/>, MIT licensed code).

mod classes;
mod control;
mod expressions;
mod files;
mod functions;
mod range;
mod safety;

use crate::{Options, Skipped, format, try_format};

/// Formats `source`, and checks that formatting the result again changes
/// nothing.
#[track_caller]
fn formatted(source: &str) -> String {
    let once = match try_format(source, Options::default()) {
        Ok(formatted) => formatted,
        Err(skipped) => panic!("not formatted ({skipped:?}):\n{source}"),
    };
    let twice = format(&once, Options::default());
    assert_eq!(twice.as_deref(), Some(once.as_str()), "second pass differs");
    once
}

/// `body` after the opening tag PER Coding Style asks for.
fn php(body: &str) -> String {
    format!("<?php\n\n{body}")
}

/// A snippet that is compliant already stays as it is.
#[track_caller]
fn stable(body: &str) {
    let source = php(body);
    assert_eq!(formatted(&source), source);
}

/// `before` is formatted to `after`.
#[track_caller]
fn fixed(before: &str, after: &str) {
    assert_eq!(formatted(&php(before)), php(after));
}

#[track_caller]
fn skipped(source: &str) -> Skipped {
    try_format(source, Options::default()).expect_err("formatted")
}
