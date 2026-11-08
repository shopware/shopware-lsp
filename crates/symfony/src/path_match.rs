//! Port of Go `path/filepath.Match` (Unix separator) and the Symfony
//! resource-pattern helpers built on it.

use crate::util::{clean_path, relative_path};

/// Go `filepath.Match`: `Err(())` reports a malformed pattern.
pub(crate) fn filepath_match(pattern: &str, name: &str) -> Result<bool, ()> {
    let mut pattern = pattern.as_bytes();
    let mut name = name.as_bytes();
    'pattern: while !pattern.is_empty() {
        let (star, chunk, rest) = scan_chunk(pattern);
        pattern = rest;
        if star && chunk.is_empty() {
            // Trailing * matches the rest unless it contains a separator.
            return Ok(!name.contains(&b'/'));
        }
        let (remainder, ok) = match_chunk(chunk, name)?;
        if ok && (remainder.is_empty() || !pattern.is_empty()) {
            name = remainder;
            continue;
        }
        if star {
            let mut index = 0;
            while index < name.len() && name[index] != b'/' {
                let (remainder, ok) = match_chunk(chunk, &name[index + 1..])?;
                if ok {
                    if pattern.is_empty() && !remainder.is_empty() {
                        index += 1;
                        continue;
                    }
                    name = remainder;
                    continue 'pattern;
                }
                index += 1;
            }
        }
        // Go validates the rest of the pattern before reporting no match.
        while !pattern.is_empty() {
            let (_, chunk, rest) = scan_chunk(pattern);
            pattern = rest;
            match_chunk(chunk, b"")?;
        }
        return Ok(false);
    }
    Ok(name.is_empty())
}

fn scan_chunk(mut pattern: &[u8]) -> (bool, &[u8], &[u8]) {
    let mut star = false;
    while !pattern.is_empty() && pattern[0] == b'*' {
        pattern = &pattern[1..];
        star = true;
    }
    let mut in_range = false;
    let mut index = 0;
    while index < pattern.len() {
        match pattern[index] {
            b'\\' => {
                if index + 1 < pattern.len() {
                    index += 1;
                }
            }
            b'[' => in_range = true,
            b']' => in_range = false,
            b'*' if !in_range => break,
            _ => {}
        }
        index += 1;
    }
    (star, &pattern[..index], &pattern[index..])
}

fn decode_char(value: &[u8]) -> (char, usize) {
    for length in 1..=value.len().min(4) {
        if let Ok(text) = std::str::from_utf8(&value[..length])
            && let Some(c) = text.chars().next()
        {
            return (c, length);
        }
    }
    ('\u{FFFD}', 1)
}

fn match_chunk<'a>(mut chunk: &[u8], mut s: &'a [u8]) -> Result<(&'a [u8], bool), ()> {
    let mut failed = false;
    while !chunk.is_empty() {
        if !failed && s.is_empty() {
            failed = true;
        }
        match chunk[0] {
            b'[' => {
                let mut r = '\0';
                if !failed {
                    let (c, n) = decode_char(s);
                    r = c;
                    s = &s[n..];
                }
                chunk = &chunk[1..];
                let mut negated = false;
                if !chunk.is_empty() && chunk[0] == b'^' {
                    negated = true;
                    chunk = &chunk[1..];
                }
                let mut matched = false;
                let mut count = 0;
                loop {
                    if !chunk.is_empty() && chunk[0] == b']' && count > 0 {
                        chunk = &chunk[1..];
                        break;
                    }
                    let (lo, rest) = get_esc(chunk)?;
                    chunk = rest;
                    let mut hi = lo;
                    if chunk.first() == Some(&b'-') {
                        let (value, rest) = get_esc(&chunk[1..])?;
                        hi = value;
                        chunk = rest;
                    }
                    if lo <= r && r <= hi {
                        matched = true;
                    }
                    count += 1;
                }
                if matched == negated {
                    failed = true;
                }
            }
            b'?' => {
                if !failed {
                    if s[0] == b'/' {
                        failed = true;
                    }
                    let (_, n) = decode_char(s);
                    s = &s[n..];
                }
                chunk = &chunk[1..];
            }
            b'\\' => {
                chunk = &chunk[1..];
                if chunk.is_empty() {
                    return Err(());
                }
                if !failed {
                    if chunk[0] != s[0] {
                        failed = true;
                    }
                    s = &s[1..];
                }
                chunk = &chunk[1..];
            }
            value => {
                if !failed {
                    if value != s[0] {
                        failed = true;
                    }
                    s = &s[1..];
                }
                chunk = &chunk[1..];
            }
        }
    }
    if failed {
        return Ok((b"", false));
    }
    Ok((s, true))
}

fn get_esc(mut chunk: &[u8]) -> Result<(char, &[u8]), ()> {
    if chunk.is_empty() || chunk[0] == b'-' || chunk[0] == b']' {
        return Err(());
    }
    if chunk[0] == b'\\' {
        chunk = &chunk[1..];
        if chunk.is_empty() {
            return Err(());
        }
    }
    let (r, n) = decode_char(chunk);
    if r == '\u{FFFD}' && n == 1 {
        return Err(());
    }
    chunk = &chunk[n..];
    if chunk.is_empty() {
        return Err(());
    }
    Ok((r, chunk))
}

/// Whether `candidate` lies inside or equals a Symfony resource pattern.
pub(crate) fn path_matches_config_pattern(candidate: &str, pattern: &str) -> bool {
    let candidate = clean_path(candidate);
    for expanded in expand_path_braces(&clean_path(pattern)) {
        if !has_path_meta(&expanded) {
            if candidate == clean_path(&expanded) || path_within(&candidate, &expanded) {
                return true;
            }
            continue;
        }
        if filepath_match(&expanded, &candidate) == Ok(true) {
            return true;
        }
        if expanded.contains("**") && double_star_match(&expanded, &candidate) {
            return true;
        }
    }
    false
}

fn double_star_match(pattern: &str, candidate: &str) -> bool {
    let bytes = pattern.as_bytes();
    let mut expression = String::from("^");
    let mut index = 0;
    while index < bytes.len() {
        match bytes[index] {
            b'*' => {
                if index + 1 < bytes.len() && bytes[index + 1] == b'*' {
                    expression.push_str(".*");
                    index += 1;
                } else {
                    expression.push_str("[^/]*");
                }
            }
            b'?' => expression.push_str("[^/]"),
            _ => {
                // Quote one byte; multi-byte characters are copied whole.
                let start = index;
                let mut end = index + 1;
                while end < bytes.len() && !pattern.is_char_boundary(end) {
                    end += 1;
                }
                expression.push_str(&regex::escape(&pattern[start..end]));
                index = end - 1;
            }
        }
        index += 1;
    }
    expression.push('$');
    regex::Regex::new(&expression).is_ok_and(|regex| regex.is_match(candidate))
}

pub(crate) fn expand_path_braces(pattern: &str) -> Vec<String> {
    let Some(open) = pattern.find('{') else {
        return vec![pattern.to_owned()];
    };
    let Some(close) = pattern[open + 1..].find('}') else {
        return vec![pattern.to_owned()];
    };
    let close = close + open + 1;
    let mut expanded = Vec::new();
    for alternative in pattern[open + 1..close].split(',') {
        let value = format!(
            "{}{}{}",
            &pattern[..open],
            alternative,
            &pattern[close + 1..]
        );
        expanded.extend(expand_path_braces(&value));
    }
    expanded
}

fn has_path_meta(path: &str) -> bool {
    path.contains(['*', '?', '['])
}

fn path_within(candidate: &str, directory: &str) -> bool {
    match relative_path(directory, candidate) {
        None => false,
        Some(relative) => relative != "." && relative != ".." && !relative.starts_with("../"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn matches_like_go() {
        assert_eq!(filepath_match("/a/*/c", "/a/b/c"), Ok(true));
        assert_eq!(filepath_match("/a/*", "/a/b/c"), Ok(false));
        assert_eq!(filepath_match("/a/[bc]d", "/a/cd"), Ok(true));
        assert_eq!(filepath_match("/a/[^bc]d", "/a/cd"), Ok(false));
        assert_eq!(filepath_match("a?c", "abc"), Ok(true));
        assert_eq!(filepath_match("[", "a"), Err(()));
        assert!(path_matches_config_pattern("/p/src/A.php", "/p/src/"));
        assert!(path_matches_config_pattern(
            "/p/src/X/A.php",
            "/p/src/**/*.php"
        ));
        assert!(path_matches_config_pattern(
            "/p/src/A.php",
            "/p/{lib,src}/*.php"
        ));
        assert!(!path_matches_config_pattern("/p/srcx/A.php", "/p/src"));
    }
}
