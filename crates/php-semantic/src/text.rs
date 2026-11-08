//! Allocation-free PHP name normalization helpers.
//!
//! Class, function, and member names are case-insensitive in PHP. Go
//! normalized query names through a bounded lowercase cache; the Rust indexes
//! instead hash and compare the lowercase character stream directly, so
//! lookups never allocate.

use std::cmp::Ordering;
use std::hash::{BuildHasher, Hasher};

/// Port of Go's per-rune `unicode.ToLower` (simple case mapping), matching
/// `swlsp_php_types::go_to_lower`.
pub(crate) fn go_lower_char(character: char) -> char {
    if character.is_ascii() {
        return character.to_ascii_lowercase();
    }
    if character == '\u{130}' {
        return 'i';
    }
    let mut lower = character.to_lowercase();
    match (lower.next(), lower.next()) {
        (Some(value), None) => value,
        _ => character,
    }
}

/// Go `strings.TrimPrefix(name, "\\")` / `"$"` used by name normalization.
pub(crate) fn trim_name(name: &str, member: bool) -> &str {
    if member {
        name.strip_prefix('$').unwrap_or(name)
    } else {
        name.strip_prefix('\\').unwrap_or(name)
    }
}

fn lower_chars(value: &str) -> impl Iterator<Item = char> + '_ {
    value.chars().map(go_lower_char)
}

/// Hash of the Go-lowercased text of `value`.
pub(crate) fn hash_lower(value: &str) -> u64 {
    let mut hasher = rustc_hash::FxBuildHasher.build_hasher();
    if value.is_ascii() {
        for byte in value.bytes() {
            hasher.write_u8(byte.to_ascii_lowercase());
        }
    } else {
        for character in lower_chars(value) {
            let mut buffer = [0u8; 4];
            for byte in character.encode_utf8(&mut buffer).bytes() {
                hasher.write_u8(byte);
            }
        }
    }
    hasher.finish()
}

/// Hash of the exact text of `value`, consistent with [`hash_lower`] for
/// already lowercase input.
pub(crate) fn hash_exact(value: &str) -> u64 {
    let mut hasher = rustc_hash::FxBuildHasher.build_hasher();
    for byte in value.bytes() {
        hasher.write_u8(byte);
    }
    hasher.finish()
}

/// Whether the Go-lowercased forms of `left` and `right` are equal.
pub(crate) fn eq_lower(left: &str, right: &str) -> bool {
    if left.is_ascii() && right.is_ascii() {
        return left.eq_ignore_ascii_case(right);
    }
    lower_chars(left).eq(lower_chars(right))
}

/// Whether the Go-lowercased form of `value` equals `lower` exactly.
pub(crate) fn lower_equals(value: &str, lower: &str) -> bool {
    if value.is_ascii() && lower.is_ascii() {
        return value.len() == lower.len()
            && value
                .bytes()
                .zip(lower.bytes())
                .all(|(left, right)| left.to_ascii_lowercase() == right);
    }
    lower_chars(value).eq(lower.chars())
}

/// Compares the Go-lowercased forms of `left` and `right` by code point,
/// which equals byte order of the lowercased UTF-8 strings.
pub(crate) fn cmp_lower(left: &str, right: &str) -> Ordering {
    if left.is_ascii() && right.is_ascii() {
        return left
            .bytes()
            .map(|byte| byte.to_ascii_lowercase())
            .cmp(right.bytes().map(|byte| byte.to_ascii_lowercase()));
    }
    lower_chars(left).cmp(lower_chars(right))
}

/// Go `strings.ToLower` after trimming the name prefix.
pub(crate) fn normalize_symbol_name(name: &str, member: bool) -> String {
    swlsp_php_types::go_to_lower(trim_name(name, member))
}

/// Go `strings.EqualFold`.
pub(crate) use swlsp_php_types::equal_fold;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn lowercase_helpers_agree() {
        assert_eq!(hash_lower("App\\Service"), hash_lower("app\\service"));
        assert_eq!(
            hash_lower("ÄBC"),
            hash_exact(&swlsp_php_types::go_to_lower("ÄBC"))
        );
        assert!(eq_lower("ÄBC", "äbc"));
        assert!(!eq_lower("ABC", "abd"));
        assert_eq!(cmp_lower("B", "a"), Ordering::Greater);
        assert_eq!(cmp_lower("Äa", "äA"), Ordering::Equal);
        assert_eq!(
            normalize_symbol_name("\\App\\Service", false),
            "app\\service"
        );
        assert_eq!(normalize_symbol_name("$Name", true), "name");
        assert_eq!(normalize_symbol_name("", false), "");
    }
}
