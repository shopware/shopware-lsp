//! Formats every PHP file below the directories given as arguments and
//! reports what the formatter did: a development check, read-only. See
//! `docs/php-formatter.md`.
//!
//! - `LAYOUT=lines` / `LAYOUT=flat` replace the white space of each file
//!   first: every token on a line of its own, or all of them on one.
//! - `OUT=<directory>` writes the formatted files there.
//! - `SHOPWARE=1` keeps `<?php declare(strict_types=1);` on one line.
//! - `WIDTH=120` splits lines at the soft limit, not at 80 characters.
//! - `VERBOSE=1` names every file that changes or is skipped.
//! - `LONG=1` tells why the lines over the width could not be split.

use std::path::{Path, PathBuf};

use swlsp_php_format::{Skipped, try_format};

fn files(directory: &Path, found: &mut Vec<PathBuf>) {
    let Ok(entries) = std::fs::read_dir(directory) else {
        return;
    };
    for entry in entries.flatten() {
        let path = entry.path();
        if path.is_dir() {
            if !path.is_symlink() {
                files(&path, found);
            }
        } else if path.extension().is_some_and(|extension| extension == "php") {
            found.push(path);
        }
    }
}

fn relaid(source: &str, separator: &str) -> String {
    use swlsp_syntax::rowan::NodeOrToken;
    let mut out = String::new();
    let mut line_comment = false;
    let mut markup = false;
    let mut end = 0;
    for element in swlsp_parser_php::parse(source)
        .root()
        .descendants_with_tokens()
    {
        let NodeOrToken::Token(token) = element else {
            continue;
        };
        let text = token.text();
        // Markup holds its white space: nothing is put around it.
        if token.kind() == swlsp_parser_php::syntax::TK_INLINE_HTML {
            out.push_str(text);
            end = usize::from(token.text_range().end());
            markup = true;
            continue;
        }
        if text.trim().is_empty() {
            continue;
        }
        // The parts of a name stay together, as they are in the source.
        let start = usize::from(token.text_range().start());
        let name = start == end && (text == "\\" || out.ends_with('\\'));
        end = usize::from(token.text_range().end());
        if std::mem::take(&mut markup) {
            // Right after markup.
        } else if line_comment {
            out.push('\n');
        } else if !out.is_empty() && !name {
            out.push_str(separator);
        }
        out.push_str(text);
        line_comment = text.starts_with("//") || text.starts_with('#') && !text.starts_with("#[");
    }
    out.push('\n');
    out
}

/// The construct a token belongs to, as far as it decides whether its line
/// may be split.
fn owner(token: &swlsp_syntax::SyntaxToken) -> &'static str {
    use swlsp_parser_php::syntax::*;
    let Some(parent) = token.parent() else {
        return "other";
    };
    for node in parent.ancestors() {
        let kind = node.kind();
        let name = if kind == PHP_PARAMETER {
            "one parameter (4.5: type and name on one line)"
        } else if kind == PHP_CLASS_DECLARATION
            || kind == PHP_INTERFACE_DECLARATION
            || kind == PHP_TRAIT_DECLARATION
            || kind == PHP_ENUM_DECLARATION
        {
            "a class header (4.1: extends on the line of the name)"
        } else if kind == PHP_METHOD_DECLARATION || kind == PHP_FUNCTION_DECLARATION {
            "a function header (4.4 and 4.5: name and return type on one line)"
        } else if kind == PHP_PROPERTY_DECLARATION
            || kind == PHP_CLASS_CONST_DECLARATION
            || kind == PHP_CONST_DECLARATION
            || kind == PHP_ENUM_CASE_DECLARATION
        {
            "a property or constant (4.6: modifiers, type and name on one line)"
        } else if kind == PHP_STRING {
            "the end of a string over several lines"
        } else if kind == PHP_ARGUMENT || kind == PHP_NAMED_ARGUMENT || kind == PHP_ARRAY_ITEM {
            "one argument or array item"
        } else if kind == PHP_MATCH_ARM {
            "one match arm"
        } else if kind == PHP_BLOCK || kind == PHP_PROGRAM || kind == PHP_CLASS_BODY {
            "one statement"
        } else {
            continue;
        };
        return name;
    }
    "other"
}

/// Why a line of formatted text is longer than `width`: what on it cannot
/// be split.
fn long_line(
    text: &str,
    width: usize,
    counts: &mut std::collections::BTreeMap<&'static str, usize>,
    samples: &mut Vec<String>,
) {
    use swlsp_parser_php::syntax::*;
    use swlsp_syntax::rowan::NodeOrToken;
    let mut starts = Vec::new();
    for (offset, character) in text.char_indices() {
        if character == '\n' {
            starts.push(offset + 1);
        }
    }
    starts.insert(0, 0);
    let line_of = |offset: usize| starts.partition_point(|start| *start <= offset) - 1;
    // The widest token starting on each line, and whether tokens start there.
    let mut widest: Vec<(usize, Option<swlsp_syntax::Kind>, usize)> =
        vec![(0, None, 0); starts.len()];
    // What the first token of each line belongs to.
    let mut owners: Vec<Option<&'static str>> = vec![None; starts.len()];
    for element in swlsp_parser_php::parse(text)
        .root()
        .descendants_with_tokens()
    {
        let NodeOrToken::Token(token) = element else {
            continue;
        };
        let kind = token.kind();
        if kind == TK_WHITESPACE || kind == TK_LINE_BREAK {
            continue;
        }
        let line = line_of(usize::from(token.text_range().start()));
        let first = token
            .text()
            .lines()
            .next()
            .unwrap_or_default()
            .chars()
            .count();
        if owners[line].is_none() && kind != TK_LINE_COMMENT && kind != TK_BLOCK_COMMENT {
            owners[line] = Some(owner(&token));
        }
        let entry = &mut widest[line];
        entry.2 += 1;
        if first > entry.0 {
            entry.0 = first;
            entry.1 = Some(kind);
        }
    }
    for (number, line) in text.lines().enumerate() {
        if line.chars().count() <= width {
            continue;
        }
        let indent = line.len() - line.trim_start().len();
        let (wide, kind, tokens) = widest[number];
        let name =
            line.trim_start().starts_with("use ") || line.trim_start().starts_with("namespace ");
        let reason = if tokens == 0 {
            "inside a string, heredoc or comment over several lines"
        } else if kind == Some(TK_LINE_COMMENT) || kind == Some(TK_BLOCK_COMMENT) {
            "a comment"
        } else if indent + wide + 4 > width {
            "one token that does not fit a line (a string, a name)"
        } else if name {
            "an import or a namespace"
        } else {
            let owner = owners[number].unwrap_or("other");
            samples.push(format!(
                "[{owner}] {}",
                line.trim().chars().take(140).collect::<String>()
            ));
            owner
        };
        *counts.entry(reason).or_default() += 1;
    }
}

fn main() {
    let options = swlsp_php_format::Options {
        declare_on_opening_tag: std::env::var_os("SHOPWARE").is_some(),
        line_width: std::env::var("WIDTH")
            .ok()
            .and_then(|width| width.parse().ok())
            .unwrap_or(swlsp_php_format::RECOMMENDED_LINE_WIDTH),
    };
    let verbose = std::env::var_os("VERBOSE").is_some();
    let mut found = Vec::new();
    for argument in std::env::args().skip(1) {
        let path = PathBuf::from(argument);
        if path.is_dir() {
            files(&path, &mut found);
        } else {
            found.push(path);
        }
    }
    found.sort();
    let (mut clean, mut changed, mut syntax, mut unsupported, mut broken, mut unstable) =
        (0, 0, 0, 0, 0, 0);
    let mut written = 0;
    let mut long = std::collections::BTreeMap::new();
    let mut samples = Vec::new();
    for path in &found {
        let Ok(source) = std::fs::read_to_string(path) else {
            continue;
        };
        // The same code with other white space: every token on a line of
        // its own, or all of them on one.
        let source = match std::env::var("LAYOUT").as_deref() {
            Ok("lines") => relaid(&source, "\n"),
            Ok("flat") => relaid(&source, " "),
            _ => source,
        };
        match try_format(&source, options) {
            Ok(formatted) => {
                if std::env::var_os("LONG").is_some() {
                    long_line(&formatted, options.line_width, &mut long, &mut samples);
                }
                // The formatted files, for a check with other tools.
                if let Some(out) = std::env::var_os("OUT") {
                    let target = PathBuf::from(out).join(format!("{written}.php"));
                    std::fs::write(&target, &formatted).expect("write formatted file");
                    println!("wrote {} {}", target.display(), path.display());
                    written += 1;
                }
                if formatted == source {
                    clean += 1;
                } else {
                    changed += 1;
                    if verbose {
                        println!("changed {}", path.display());
                    }
                }
                if try_format(&formatted, options).ok().as_deref() != Some(formatted.as_str()) {
                    unstable += 1;
                    println!("unstable {}", path.display());
                }
            }
            Err(Skipped::SyntaxError) => {
                syntax += 1;
                if verbose {
                    println!("syntax {}", path.display());
                }
            }
            Err(Skipped::Unsupported) => {
                unsupported += 1;
                if verbose {
                    println!("unsupported {}", path.display());
                }
            }
            Err(Skipped::Unsafe) => {
                broken += 1;
                println!("unsafe {}", path.display());
            }
        }
    }
    if !long.is_empty() {
        println!("lines over {} characters: {long:?}", options.line_width);
        for sample in samples.iter().step_by(samples.len() / 40 + 1) {
            println!("  other: {sample}");
        }
    }
    println!(
        "{} files: {clean} clean, {changed} changed, {syntax} syntax errors, {unsupported} unsupported, {broken} unsafe, {unstable} unstable",
        found.len()
    );
}
