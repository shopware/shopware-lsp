//! Path glob matching for slash-normalized paths, built on `globset`.
//!
//! Two dialects share one engine:
//!
//! * Ant/LSP globs ([`ant`], [`Matcher`]):
//!   `**/` crosses directories, `*` and `?` do not, `[abc]`/`[!abc]` are
//!   character classes and `{a,b}` are alternatives. This is the syntax users
//!   write in `.config/shopware/lsp.yaml`, so it is validated strictly.
//! * Path-component globs ([`path_glob`]): `*`, `?`, `[...]`/`[^...]`
//!   classes and `\` escapes; braces are literal. `**` is either a plain `*` or, for
//!   Symfony resource patterns, any run of characters including `/`.
//!
//! Both are translated into `globset` syntax with a literal separator, so
//! only the dialect-specific parsing lives here. globset matches bytes: `?`
//! and character classes consume one byte, so they do not match a non-ASCII
//! character as a whole.

use std::collections::HashMap;
use std::sync::{Arc, OnceLock, RwLock};

use globset::{Glob, GlobBuilder, GlobMatcher, GlobSet, GlobSetBuilder};

const MAXIMUM_CACHE_ENTRIES: usize = 1024;

/// globset syntax for "any run of characters, `/` included": either no
/// separator at all, or one or more directories in between.
const ANY: &str = "{*,*/**/*}";

/// A glob compilation failure. Syntax errors found while translating an
/// Ant pattern have their own messages (`pattern must not be empty`,
/// `unclosed character class`, ...); errors raised by the underlying glob
/// engine use the `globset` wording.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CompileError(String);

impl std::fmt::Display for CompileError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(&self.0)
    }
}

impl std::error::Error for CompileError {}

/// An immutable set of compiled Ant/LSP-style glob patterns.
#[derive(Debug, Clone)]
pub struct Matcher {
    set: GlobSet,
}

impl Default for Matcher {
    fn default() -> Self {
        Matcher {
            set: GlobSet::empty(),
        }
    }
}

impl Matcher {
    /// Validates and compiles patterns for repeated matching.
    pub fn compile<S: AsRef<str>>(patterns: &[S]) -> Result<Matcher, CompileError> {
        let mut builder = GlobSetBuilder::new();
        for pattern in patterns {
            let pattern = pattern.as_ref();
            let glob = compile_ant(pattern).map_err(|error| {
                CompileError(format!("compile glob {}: {error}", quote(pattern)))
            })?;
            builder.add(glob);
        }
        let set = builder
            .build()
            .map_err(|error| CompileError(error.to_string()))?;
        Ok(Matcher { set })
    }

    /// Reports whether any compiled pattern matches `candidate`.
    pub fn matches(&self, candidate: &str) -> bool {
        self.set.is_match(normalize_candidate(candidate))
    }

    /// Reports whether the matcher contains no patterns.
    pub fn is_empty(&self) -> bool {
        self.set.is_empty()
    }
}

/// Free-function form of [`Matcher::compile`].
pub fn compile<S: AsRef<str>>(patterns: &[S]) -> Result<Matcher, CompileError> {
    Matcher::compile(patterns)
}

/// Matches a slash-normalized path against an Ant-style glob. Invalid
/// patterns never match. Compiled patterns are cached process-wide.
pub fn ant(pattern: &str, candidate: &str) -> bool {
    let pattern = normalize_pattern(pattern);
    cached(Dialect::Ant, pattern, |pattern| {
        compile_ant(pattern).ok().map(|glob| glob.compile_matcher())
    })
    .is_some_and(|matcher| matcher.is_match(normalize_candidate(candidate)))
}

/// Matches `name` against a path-component glob: `*` and `?` never match
/// `/`, classes negate with `^`, `\` escapes and `{`/`}` are literal. With
/// `double_star`, `**` matches any run of characters including `/`;
/// otherwise it is a plain `*`. Malformed patterns never match.
pub fn path_glob(pattern: &str, name: &str, double_star: bool) -> bool {
    let dialect = if double_star {
        Dialect::PathDoubleStar
    } else {
        Dialect::Path
    };
    cached(dialect, pattern.to_owned(), |pattern| {
        let mut translated = String::with_capacity(pattern.len() + 8);
        path_to_glob(pattern, double_star, &mut translated).ok()?;
        build(&translated).ok().map(|glob| glob.compile_matcher())
    })
    .is_some_and(|matcher| matcher.is_match(name))
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
enum Dialect {
    Ant,
    Path,
    PathDoubleStar,
}

type Cache = RwLock<HashMap<(Dialect, String), Option<Arc<GlobMatcher>>>>;

/// Returns the cached matcher for a pattern, compiling it on first use.
/// Invalid patterns are cached as `None`.
fn cached(
    dialect: Dialect,
    pattern: String,
    compile: impl FnOnce(&str) -> Option<GlobMatcher>,
) -> Option<Arc<GlobMatcher>> {
    static CACHE: OnceLock<Cache> = OnceLock::new();
    let cache = CACHE.get_or_init(Cache::default);
    let key = (dialect, pattern);
    if let Ok(values) = cache.read()
        && let Some(compiled) = values.get(&key)
    {
        return compiled.clone();
    }
    let compiled = compile(&key.1).map(Arc::new);
    if let Ok(mut values) = cache.write() {
        if values.len() >= MAXIMUM_CACHE_ENTRIES {
            values.clear();
        }
        values.insert(key, compiled.clone());
    }
    compiled
}

fn build(glob: &str) -> Result<Glob, String> {
    GlobBuilder::new(glob)
        .literal_separator(true)
        .backslash_escape(true)
        .build()
        .map_err(|error| error.kind().to_string())
}

fn compile_ant(pattern: &str) -> Result<Glob, String> {
    let pattern = normalize_pattern(pattern);
    if pattern.is_empty() {
        return Err("pattern must not be empty".to_owned());
    }
    let mut translated = String::with_capacity(pattern.len() + 8);
    ant_to_glob(&pattern, true, &mut translated)?;
    build(&translated)
}

/// Validates an Ant pattern and appends its globset form. `top_level` is
/// false inside a `{...}` alternative.
fn ant_to_glob(pattern: &str, top_level: bool, out: &mut String) -> Result<(), String> {
    let mut index = 0;
    while let Some(character) = pattern[index..].chars().next() {
        let next = index + character.len_utf8();
        match character {
            '*' if pattern[next..].starts_with('*') => {
                // globset only treats `**` as recursive at a component
                // boundary; elsewhere spell out the Ant meaning.
                let at_boundary = index == 0 || pattern[..index].ends_with('/');
                let after = next + 1;
                if pattern[after..].starts_with('/') {
                    out.push_str(if at_boundary { "**/" } else { "{**/}" });
                    index = after + 1;
                } else {
                    let whole_suffix = top_level && at_boundary && after == pattern.len();
                    out.push_str(if whole_suffix { "**" } else { ANY });
                    index = after;
                }
                continue;
            }
            '[' => {
                let close = pattern[next..]
                    .find(']')
                    .ok_or("unclosed character class")?
                    + next;
                let content = &pattern[next..close];
                if content.is_empty() || content == "!" {
                    return Err("empty character class".to_owned());
                }
                if content.contains('/') {
                    return Err("character class must not match a path separator".to_owned());
                }
                push_ant_class(content, out);
                index = close + 1;
                continue;
            }
            '{' => {
                let close = pattern[next..]
                    .find('}')
                    .ok_or("unclosed alternative group")?
                    + next;
                let alternatives: Vec<&str> = pattern[next..close].split(',').collect();
                if alternatives.len() < 2 {
                    return Err("alternative group must contain a comma".to_owned());
                }
                out.push('{');
                for (alternative_index, alternative) in alternatives.iter().enumerate() {
                    if alternative.is_empty() {
                        return Err("alternative group must not contain an empty value".to_owned());
                    }
                    if alternative_index > 0 {
                        out.push(',');
                    }
                    ant_to_glob(alternative, false, out)?;
                }
                out.push('}');
                index = close + 1;
                continue;
            }
            ']' | '}' => return Err(format!("unexpected '{character}'")),
            character => out.push(character),
        }
        index = next;
    }
    Ok(())
}

/// Appends an Ant character class. Only a leading `!` negates; a leading
/// `^` is literal, but globset would read it as negation, so leading `^`/`!`
/// literals become escaped alternatives next to the remaining class.
fn push_ant_class(content: &str, out: &mut String) {
    if let Some(rest) = content.strip_prefix('!') {
        out.push_str("[!");
        out.push_str(rest);
        out.push(']');
        return;
    }
    let class = content.trim_start_matches(['^', '!']);
    let literals = &content[..content.len() - class.len()];
    if literals.is_empty() {
        out.push('[');
        out.push_str(class);
        out.push(']');
        return;
    }
    out.push('{');
    for (literal_index, literal) in literals.chars().enumerate() {
        if literal_index > 0 {
            out.push(',');
        }
        out.push('\\');
        out.push(literal);
    }
    if !class.is_empty() {
        out.push_str(",[");
        out.push_str(class);
        out.push(']');
    }
    out.push('}');
}

/// Appends the globset form of a [`path_glob`] pattern.
fn path_to_glob(pattern: &str, double_star: bool, out: &mut String) -> Result<(), String> {
    let mut characters = pattern.chars().peekable();
    let mut in_class = false;
    while let Some(character) = characters.next() {
        match character {
            '\\' => {
                let escaped = characters.next().ok_or("trailing backslash")?;
                // globset classes take their contents literally.
                if !in_class {
                    out.push('\\');
                }
                out.push(escaped);
            }
            '[' if !in_class => {
                in_class = true;
                out.push('[');
                if characters.next_if_eq(&'^').is_some() {
                    out.push('!');
                }
            }
            ']' if in_class => {
                in_class = false;
                out.push(']');
            }
            '*' if !in_class => {
                let mut stars = 1;
                while characters.next_if_eq(&'*').is_some() {
                    stars += 1;
                }
                out.push_str(if double_star && stars > 1 { ANY } else { "*" });
            }
            '{' | '}' if !in_class => {
                out.push('\\');
                out.push(character);
            }
            character => out.push(character),
        }
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

    #[test]
    fn double_star_outside_a_component_boundary_still_crosses_directories() {
        // `**` not followed by `/` means "anything", wherever it appears.
        assert!(ant("vendor/**.php", "vendor/a/b/c.php"));
        assert!(ant("**.php", "a/b.php"));
        assert!(ant("src/a**b", "src/a/x/b"));
        assert!(ant("src/a**b", "src/ab"));
        assert!(!ant("src/a**b", "src/a/x/c"));
        // `**/` after a non-separator means "optionally any prefix ending in /".
        assert!(ant("src/a**/b", "src/ab"));
        assert!(ant("src/a**/b", "src/ax/y/b"));
        assert!(!ant("src/a**/b", "src/axb"));
        // Inside alternatives too.
        assert!(ant("{src/**,lib}", "src/a/b"));
        assert!(ant("x/{**/a.php,b}", "x/a.php"));
        assert!(ant("x/{**/a.php,b}", "x/y/z/a.php"));
        assert!(ant("x/{**,b}", "x/y/z"));
        assert!(ant("**", "a/b"));
        assert!(ant("src/**", "src/a/b"));
        assert!(!ant("src/**", "lib/a"));
    }

    #[test]
    fn single_wildcards_and_classes_do_not_cross_directories() {
        assert!(!ant("src/*", "src/a/b"));
        assert!(!ant("src/?", "src/"));
        assert!(!ant("a?b", "a/b"));
        assert!(ant("a[!x]b", "ayb"));
        assert!(!ant("a[!x]b", "axb"));
        assert!(ant("a*b", "aééb"));
        assert!(ant("é/*.php", "é/ü.php"));
    }

    #[test]
    fn single_character_wildcards_match_one_byte() {
        // globset matches bytes, so `?` and classes see one byte of a
        // multi-byte UTF-8 character.
        assert!(!ant("a?b", "aéb"));
        assert!(ant("a??b", "aéb"));
        assert!(!ant("a[é]b", "aéb"));
        assert!(!ant("a[!x]b", "aéb"));
    }

    #[test]
    fn leading_caret_and_bang_are_literal_after_negation_or_caret() {
        assert!(ant("a[^]b", "a^b"));
        assert!(!ant("a[^]b", "axb"));
        assert!(ant("a[^!x]b", "a!b"));
        assert!(ant("a[^!x]b", "axb"));
        assert!(!ant("a[^!x]b", "ayb"));
        assert!(ant("a[!^]b", "axb"));
        assert!(!ant("a[!^]b", "a^b"));
    }

    #[test]
    fn rejects_malformed_patterns_with_specific_messages() {
        for (pattern, message) in [
            ("a[]b", "empty character class"),
            ("a[!]b", "empty character class"),
            ("a[x/]b", "character class must not match a path separator"),
            ("a{b,c", "unclosed alternative group"),
            ("a{b,}", "alternative group must not contain an empty value"),
            ("a{b,{c,d}}", "unclosed alternative group"),
            ("a}b", "unexpected '}'"),
            ("  ", "pattern must not be empty"),
        ] {
            let error = Matcher::compile(&[pattern]).unwrap_err().to_string();
            assert!(error.contains(message), "{pattern}: {error}");
        }
        assert!(Matcher::compile(&["a[z-a]b"]).is_err());
        assert!(Matcher::default().is_empty());
        assert!(!Matcher::default().matches("a"));
    }

    #[test]
    fn path_glob_matches_within_path_components() {
        for (pattern, name, expected) in [
            ("css/*.css", "css/app.css", true),
            ("css/*.css", "css/sub/app.css", false),
            ("css/a?p.css", "css/app.css", true),
            ("css/[a-c]pp.css", "css/bpp.css", true),
            ("css/[^a]pp.css", "css/app.css", false),
            ("css/[^a]pp.css", "css/bpp.css", true),
            ("*", "a/b", false),
            ("a*b*c", "axxbyyc", true),
            ("[", "a", false),
            ("/a/*/c", "/a/b/c", true),
            ("/a/*", "/a/b/c", false),
            ("/a/[bc]d", "/a/cd", true),
            ("/a/[^bc]d", "/a/cd", false),
            (r"a\*b", "a*b", true),
            (r"a\*b", "axb", false),
            ("a{b,c}", "a{b,c}", true),
            ("a{b,c}", "ab", false),
            ("a\\", "a", false),
            ("a/**/b", "a/x/b", true),
            ("a/**/b", "a/x/y/b", false),
        ] {
            assert_eq!(
                path_glob(pattern, name, false),
                expected,
                "{pattern} {name}"
            );
        }
    }

    #[test]
    fn path_glob_double_star_crosses_directories() {
        assert!(path_glob("/p/src/**/*.php", "/p/src/X/A.php", true));
        assert!(path_glob("/p/src/**/*.php", "/p/src/X/Y/A.php", true));
        // `**` is `.*`, so the surrounding separators stay mandatory.
        assert!(!path_glob("/p/src/**/*.php", "/p/src/A.php", true));
        assert!(path_glob("/p/src/**.php", "/p/src/X/A.php", true));
        assert!(!path_glob("/p/src/*.php", "/p/src/X/A.php", true));
    }
}
