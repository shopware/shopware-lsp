//! Ant/LSP-style glob matching for slash-normalized project paths (port of
//! `internal/pathmatch`).
//!
//! `**/` crosses directories, `*` and `?` do not, `[abc]`/`[!abc]` are
//! character classes and `{a,b}` are alternatives.

use std::collections::HashMap;
use std::sync::{OnceLock, RwLock};

use regex::Regex;

const MAXIMUM_ANT_EXPRESSION_CACHE_ENTRIES: usize = 1024;

/// A glob compilation failure. Messages match Go for every glob-level error
/// (`pattern must not be empty`, `unclosed character class`, ...). Errors
/// raised by the underlying regular expression engine use the Rust `regex`
/// wording.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CompileError(String);

impl std::fmt::Display for CompileError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(&self.0)
    }
}

impl std::error::Error for CompileError {}

/// An immutable set of compiled Ant/LSP-style glob patterns.
#[derive(Debug, Clone, Default)]
pub struct Matcher {
    expressions: Vec<Regex>,
}

impl Matcher {
    /// Validates and compiles patterns for repeated matching (Go `Compile`).
    pub fn compile<S: AsRef<str>>(patterns: &[S]) -> Result<Matcher, CompileError> {
        let mut expressions = Vec::with_capacity(patterns.len());
        for pattern in patterns {
            let pattern = pattern.as_ref();
            let compiled = compile_ant(pattern).map_err(|error| {
                CompileError(format!("compile glob {}: {error}", quote(pattern)))
            })?;
            expressions.push(compiled);
        }
        Ok(Matcher { expressions })
    }

    /// Reports whether any compiled pattern matches `candidate` (Go `Match`).
    pub fn matches(&self, candidate: &str) -> bool {
        let candidate = normalize_candidate(candidate);
        self.expressions
            .iter()
            .any(|expression| expression.is_match(&candidate))
    }

    /// Reports whether the matcher contains no patterns.
    pub fn is_empty(&self) -> bool {
        self.expressions.is_empty()
    }
}

/// Free-function form of [`Matcher::compile`].
pub fn compile<S: AsRef<str>>(patterns: &[S]) -> Result<Matcher, CompileError> {
    Matcher::compile(patterns)
}

fn cache() -> &'static RwLock<HashMap<String, Regex>> {
    static CACHE: OnceLock<RwLock<HashMap<String, Regex>>> = OnceLock::new();
    CACHE.get_or_init(|| RwLock::new(HashMap::new()))
}

/// Matches a slash-normalized path against an Ant-style glob. Invalid
/// patterns never match. Compiled patterns are cached process-wide.
pub fn ant(pattern: &str, candidate: &str) -> bool {
    let pattern = normalize_pattern(pattern);
    let candidate = normalize_candidate(candidate);
    if let Ok(values) = cache().read()
        && let Some(compiled) = values.get(pattern.as_str())
    {
        return compiled.is_match(&candidate);
    }
    let Ok(compiled) = compile_ant(&pattern) else {
        return false;
    };
    let matched = compiled.is_match(&candidate);
    if let Ok(mut values) = cache().write()
        && !values.contains_key(pattern.as_str())
    {
        if values.len() >= MAXIMUM_ANT_EXPRESSION_CACHE_ENTRIES {
            values.clear();
        }
        values.insert(pattern, compiled);
    }
    matched
}

fn compile_ant(pattern: &str) -> Result<Regex, String> {
    let pattern = normalize_pattern(pattern);
    if pattern.is_empty() {
        return Err("pattern must not be empty".to_owned());
    }
    let mut expression = String::with_capacity(pattern.len() * 2 + 2);
    expression.push('^');
    append_ant_expression(&mut expression, &pattern)?;
    expression.push('$');
    Regex::new(&expression).map_err(|error| format!("error parsing regexp: {error}"))
}

fn append_ant_expression(expression: &mut String, pattern: &str) -> Result<(), String> {
    let bytes = pattern.as_bytes();
    let mut index = 0;
    while index < bytes.len() {
        match bytes[index] {
            b'*' => {
                if index + 1 < bytes.len() && bytes[index + 1] == b'*' {
                    index += 1;
                    if index + 1 < bytes.len() && bytes[index + 1] == b'/' {
                        expression.push_str("(?:.*/)?");
                        index += 1;
                    } else {
                        expression.push_str(".*");
                    }
                } else {
                    expression.push_str("[^/]*");
                }
            }
            b'?' => expression.push_str("[^/]"),
            b'[' => {
                let Some(relative) = pattern[index + 1..].find(']') else {
                    return Err("unclosed character class".to_owned());
                };
                let closing = index + 1 + relative;
                let mut content = &pattern[index + 1..closing];
                if content.is_empty() || content == "!" {
                    return Err("empty character class".to_owned());
                }
                if content.contains('/') {
                    return Err("character class must not match a path separator".to_owned());
                }
                expression.push('[');
                match content.as_bytes()[0] {
                    b'!' => {
                        expression.push('^');
                        content = &content[1..];
                    }
                    b'^' => expression.push('\\'),
                    _ => {}
                }
                for character in content.chars() {
                    match character {
                        // RE2 treats these literally inside a class; the
                        // Rust engine gives them class-set meaning.
                        '\\' | '[' | '&' | '~' => {
                            expression.push('\\');
                            expression.push(character);
                        }
                        character => expression.push(character),
                    }
                }
                expression.push(']');
                index = closing;
            }
            b'{' => {
                let Some(relative) = pattern[index + 1..].find('}') else {
                    return Err("unclosed alternative group".to_owned());
                };
                let closing = index + 1 + relative;
                let alternatives: Vec<&str> = pattern[index + 1..closing].split(',').collect();
                if alternatives.len() < 2 {
                    return Err("alternative group must contain a comma".to_owned());
                }
                expression.push_str("(?:");
                for (alternative_index, alternative) in alternatives.iter().enumerate() {
                    if alternative.is_empty() {
                        return Err("alternative group must not contain an empty value".to_owned());
                    }
                    if alternative_index > 0 {
                        expression.push('|');
                    }
                    append_ant_expression(expression, alternative)?;
                }
                expression.push(')');
                index = closing;
            }
            byte @ (b']' | b'}') => {
                return Err(format!("unexpected '{}'", byte as char));
            }
            _ => {
                let character = pattern[index..].chars().next().unwrap_or_default();
                expression.push_str(&regex::escape(character.encode_utf8(&mut [0; 4])));
                index += character.len_utf8();
                continue;
            }
        }
        index += 1;
    }
    Ok(())
}

fn normalize_pattern(pattern: &str) -> String {
    let pattern = pattern.trim().replace('\\', "/");
    match pattern.strip_prefix("./") {
        Some(rest) => rest.to_owned(),
        None => pattern,
    }
}

fn normalize_candidate(candidate: &str) -> String {
    let candidate = candidate.replace('\\', "/");
    match candidate.strip_prefix("./") {
        Some(rest) => rest.to_owned(),
        None => candidate,
    }
}

fn quote(value: &str) -> String {
    format!("{value:?}")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn ant_matches_project_relative_paths() {
        assert!(ant(
            "src/**/ProductController.php",
            "src/ProductController.php"
        ));
        assert!(ant(
            "src/**/ProductController.php",
            "src/Admin/Catalog/ProductController.php"
        ));
        assert!(ant(
            r"src\*\ProductController.php",
            "src/Admin/ProductController.php"
        ));
        assert!(!ant(
            "src/**/ProductController.php",
            "tests/ProductController.php"
        ));
        assert!(!ant("", "src/ProductController.php"));
    }

    #[test]
    fn compile_matches_lsp_glob_syntax() {
        let matcher = Matcher::compile(&[
            "**/generated/**",
            "custom/plugins/{Legacy,Archived}/**/*.[pt]hp",
            "assets/file[!0-9].js",
        ])
        .unwrap();
        assert!(matcher.matches("generated/cache.php"));
        assert!(matcher.matches("src/generated/deep/cache.php"));
        assert!(matcher.matches("custom/plugins/Legacy/src/file.php"));
        assert!(matcher.matches("custom/plugins/Archived/src/file.thp"));
        assert!(matcher.matches("assets/filea.js"));
        assert!(!matcher.matches("assets/file1.js"));
        assert!(!matcher.matches("custom/plugins/Current/src/file.php"));
    }

    #[test]
    fn compile_rejects_malformed_patterns() {
        for pattern in ["", "src/[abc", "src/{one}"] {
            assert!(Matcher::compile(&[pattern]).is_err(), "{pattern}");
        }
    }

    #[test]
    fn class_contents_are_literal_like_re2() {
        let matcher = Matcher::compile(&["a[[&~^]b", "c[^x]d"]).unwrap();
        assert!(matcher.matches("a[b"));
        assert!(matcher.matches("a&b"));
        assert!(matcher.matches("a^b"));
        assert!(matcher.matches("c^d"));
        assert!(!matcher.matches("cyd"));
        assert!(
            Matcher::compile(&["a]b"])
                .unwrap_err()
                .to_string()
                .contains("unexpected ']'")
        );
        assert!(ant("./src/é?.php", "src/éa.php"));
        assert!(ant("a.b+c", "a.b+c"));
        assert!(!ant("a.b", "axb"));
    }
}
