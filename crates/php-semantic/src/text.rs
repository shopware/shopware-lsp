//! Allocation-free PHP name normalization helpers.
//!
//! Class, function, and member names are case-insensitive in PHP. The
//! indexes hash and compare the lowercase character stream directly instead
//! of lowercasing query names first, so lookups never allocate. Lowercasing
//! is one character per character ([`swlsp_textutil::lowercase_char`]).

use std::cmp::Ordering;
use std::hash::{BuildHasher, Hasher};

use swlsp_textutil::lowercase_char;

/// `name` without its leading `\\` (or `$` for a member name).
pub(crate) fn trim_name(name: &str, member: bool) -> &str {
    if member {
        name.strip_prefix('$').unwrap_or(name)
    } else {
        name.strip_prefix('\\').unwrap_or(name)
    }
}

fn lower_chars(value: &str) -> impl Iterator<Item = char> + '_ {
    value.chars().map(lowercase_char)
}

/// Hash of the lowercased text of `value`.
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

/// Whether the lowercased forms of `left` and `right` are equal.
pub(crate) fn eq_lower(left: &str, right: &str) -> bool {
    if left.is_ascii() && right.is_ascii() {
        return left.eq_ignore_ascii_case(right);
    }
    lower_chars(left).eq(lower_chars(right))
}

/// Whether the lowercased form of `value` equals `lower` exactly.
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

/// Compares the lowercased forms of `left` and `right` by code point,
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

/// The lowercased name without its leading `\\` (or `$` for a member name).
pub(crate) fn normalize_symbol_name(name: &str, member: bool) -> String {
    swlsp_textutil::lowercase(trim_name(name, member))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn lowercase_helpers_agree() {
        assert_eq!(hash_lower("App\\Service"), hash_lower("app\\service"));
        assert_eq!(
            hash_lower("ÄBC"),
            hash_exact(&swlsp_textutil::lowercase("ÄBC"))
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
