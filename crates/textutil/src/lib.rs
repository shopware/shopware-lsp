//! Small, allocation-conscious text transforms shared by language frontends
//! and semantic analysis.

use std::borrow::Cow;

pub mod case;
pub mod number;

pub use case::{
    eq_ignore_case, lowercase, lowercase_char, uppercase, uppercase_char, uppercase_first,
};
pub use number::{parse_int_literal, parse_uint_literal};

#[inline]
fn is_compact_whitespace(byte: u8) -> bool {
    matches!(byte, b' ' | b'\t' | b'\r' | b'\n')
}

/// Removes ASCII space, tab, carriage-return, and line-feed bytes. Borrows
/// `source` unchanged when no compaction is necessary.
pub fn compact_whitespace(source: &str) -> Cow<'_, str> {
    let bytes = source.as_bytes();
    let Some(first) = bytes.iter().position(|&byte| is_compact_whitespace(byte)) else {
        return Cow::Borrowed(source);
    };
    let mut result = String::with_capacity(source.len());
    result.push_str(&source[..first]);
    let mut start = first + 1;
    for (index, &byte) in bytes.iter().enumerate().skip(first + 1) {
        if is_compact_whitespace(byte) {
            result.push_str(&source[start..index]);
            start = index + 1;
        }
    }
    result.push_str(&source[start..]);
    Cow::Owned(result)
}

#[derive(Clone)]
struct FoldAsciiState {
    next: [u16; 128],
    terminal: bool,
}

impl Default for FoldAsciiState {
    fn default() -> Self {
        Self {
            next: [0; 128],
            terminal: false,
        }
    }
}

/// Searches case-insensitively for any of a fixed set of ASCII patterns
/// without normalizing or allocating a copy of the source (Aho-Corasick).
#[derive(Clone, Default)]
pub struct FoldAsciiMatcher {
    states: Vec<FoldAsciiState>,
    matches_empty: bool,
}

impl FoldAsciiMatcher {
    /// Builds a matcher.
    ///
    /// # Panics
    /// Panics when a pattern contains non-ASCII bytes or the automaton would
    /// exceed 65 536 states.
    pub fn new<S: AsRef<str>>(patterns: &[S]) -> Self {
        let capacity = 1 + patterns
            .iter()
            .map(|value| value.as_ref().len())
            .sum::<usize>();
        let mut states = Vec::with_capacity(capacity);
        states.push(FoldAsciiState::default());
        let mut matches_empty = false;
        for pattern in patterns {
            let pattern = pattern.as_ref();
            if pattern.is_empty() {
                matches_empty = true;
                continue;
            }
            let mut state = 0usize;
            for &byte in pattern.as_bytes() {
                let value = byte.to_ascii_lowercase();
                assert!(value < 128, "folded ASCII matcher patterns must be ASCII");
                let mut next = states[state].next[value as usize];
                if next == 0 {
                    assert!(
                        states.len() < 1 << 16,
                        "folded ASCII matcher has too many states"
                    );
                    states.push(FoldAsciiState::default());
                    next = (states.len() - 1) as u16;
                    states[state].next[value as usize] = next;
                }
                state = next as usize;
            }
            states[state].terminal = true;
        }

        let mut failures = vec![0u16; states.len()];
        let mut queue: Vec<u16> = Vec::with_capacity(states.len().saturating_sub(1));
        for value in 0..128 {
            let state = states[0].next[value];
            if state != 0 {
                queue.push(state);
            }
        }
        let mut head = 0;
        while head < queue.len() {
            let state = queue[head] as usize;
            head += 1;
            let failure = failures[state] as usize;
            for value in 0..128 {
                let next = states[state].next[value];
                if next == 0 {
                    states[state].next[value] = states[failure].next[value];
                    continue;
                }
                queue.push(next);
                let next_failure = states[failure].next[value];
                failures[next as usize] = next_failure;
                let inherited = states[next_failure as usize].terminal;
                states[next as usize].terminal |= inherited;
            }
        }
        Self {
            states,
            matches_empty,
        }
    }

    /// Reports whether `source` contains any pattern, ignoring ASCII case.
    pub fn contains_str(&self, source: &str) -> bool {
        self.contains_bytes(source.as_bytes())
    }

    /// Reports whether `source` contains any pattern, ignoring ASCII case.
    pub fn contains_bytes(&self, source: &[u8]) -> bool {
        if self.matches_empty {
            return true;
        }
        if self.states.is_empty() {
            return false;
        }
        let mut state = 0usize;
        for &value in source {
            if value >= 128 {
                state = 0;
                continue;
            }
            state = self.states[state].next[value.to_ascii_lowercase() as usize] as usize;
            if self.states[state].terminal {
                return true;
            }
        }
        false
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn compact_whitespace_removes_ascii_whitespace() {
        assert_eq!(compact_whitespace(" \tFoo\\\r\nBar "), "Foo\\Bar");
        assert_eq!(compact_whitespace("Grö ße"), "Größe");
    }

    #[test]
    fn compact_whitespace_does_not_allocate_for_compact_input() {
        assert!(matches!(
            compact_whitespace("Shopware\\Core\\Product"),
            Cow::Borrowed(_)
        ));
    }

    #[test]
    fn fold_ascii_matcher_finds_any_pattern() {
        let matcher = FoldAsciiMatcher::new(&["render", "template", "stream"]);
        assert!(matcher.contains_str("$this->ReNdErView()"));
        assert!(matcher.contains_bytes(b"#[TEMPLATE]"));
        assert!(matcher.contains_str("upstream response"));
        assert!(!matcher.contains_str("ordinary application source"));
        assert!(!matcher.contains_bytes(b"temp\xfflate"));

        let overlapping = FoldAsciiMatcher::new(&["he", "she", "his", "hers"]);
        assert!(overlapping.contains_str("ushers"));
        assert!(overlapping.contains_str("HISTORY"));

        let empty = FoldAsciiMatcher::new(&[""]);
        assert!(empty.contains_str(""));
        assert!(empty.contains_bytes(&[]));
    }

    #[test]
    fn fold_ascii_matcher_scans_without_allocation() {
        // Scanning takes `&self` and only reads the precomputed automaton, so
        // it cannot allocate.
        let matcher = FoldAsciiMatcher::new(&["generate", "redirecttoroute"]);
        assert!(matcher.contains_str("$this->ReDiReCtToRoUtE('route')"));
        assert!(!matcher.contains_bytes(b"ordinary application source"));
    }

    #[test]
    fn default_matcher_matches_nothing() {
        assert!(!FoldAsciiMatcher::default().contains_str("anything"));
        assert!(!FoldAsciiMatcher::new::<&str>(&[]).contains_str("anything"));
    }
}

/// Escapes `<`, `>`, `&`, `'` and `"` for HTML text and attributes.
pub fn html_escape(value: &str) -> String {
    let mut result = String::with_capacity(value.len());
    for character in value.chars() {
        match character {
            '<' => result.push_str("&lt;"),
            '>' => result.push_str("&gt;"),
            '&' => result.push_str("&amp;"),
            '\'' => result.push_str("&#39;"),
            '"' => result.push_str("&#34;"),
            other => result.push(other),
        }
    }
    result
}

#[cfg(test)]
mod html_escape_tests {
    #[test]
    fn escapes_markup_and_quotes() {
        assert_eq!(
            super::html_escape(r#"<a href="x">Tom & 'Jerry'</a>"#),
            "&lt;a href=&#34;x&#34;&gt;Tom &amp; &#39;Jerry&#39;&lt;/a&gt;"
        );
    }
}
