//! Translation placeholder extraction.

use std::collections::BTreeSet;
use std::sync::LazyLock;

use regex::Regex;
use swlsp_indexer::Result;

use crate::Index;

// Whitespace and word characters are ASCII only.
static PLACEHOLDER_PATTERNS: LazyLock<[Regex; 4]> = LazyLock::new(|| {
    [
        r"%[^%\t\n\x0C\r ]+%",
        r"\{\{[\t\n\x0C\r ]*[^{}]+?[\t\n\x0C\r ]*\}\}",
        r"[@!%][^\t\n\x0C\r ][0-9A-Za-z_-]*",
        r"\{[\t\n\x0C\r ]*[^{}]+?[\t\n\x0C\r ]*\}",
    ]
    .map(|pattern| Regex::new(pattern).expect("valid placeholder pattern"))
});

static ICU_ARGUMENT_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"\{[\t\n\x0C\r ]*([A-Za-z_][A-Za-z0-9_.-]*)[\t\n\x0C\r ]*,")
        .expect("valid ICU argument pattern")
});

/// Sorted, distinct placeholders of a translation text: `%name%`,
/// `{{ name }}`, `@name`/`!name`/`%name`, `{name}` (plus the bare name) and
/// ICU argument names.
pub fn placeholders(text: &str) -> Vec<String> {
    let mut unique = BTreeSet::new();
    for pattern in PLACEHOLDER_PATTERNS.iter() {
        for found in pattern.find_iter(text) {
            let found = found.as_str().trim();
            if found.is_empty() {
                continue;
            }
            unique.insert(found.to_owned());
            if found.starts_with('{') && found.ends_with('}') && !found.starts_with("{{") {
                let inner = found.strip_prefix('{').unwrap_or(found);
                let inner = inner.strip_suffix('}').unwrap_or(inner);
                let mut name = inner.trim();
                if let Some(comma) = name.find(',') {
                    name = name[..comma].trim();
                }
                if !name.is_empty() {
                    unique.insert(name.to_owned());
                }
            }
        }
    }
    for captures in ICU_ARGUMENT_PATTERN.captures_iter(text) {
        unique.insert(captures[1].to_owned());
    }
    unique.into_iter().collect()
}

impl Index {
    /// Distinct placeholders across every message of `key` in `domain`.
    pub fn placeholders(&self, domain: &str, key: &str) -> Result<Vec<String>> {
        let mut unique = BTreeSet::new();
        for message in self.messages_for(domain, key)? {
            unique.extend(placeholders(&message.text));
        }
        Ok(unique.into_iter().collect())
    }
}
