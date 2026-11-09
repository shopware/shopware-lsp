//! Case mapping that keeps one character per character.
//!
//! `str::to_lowercase` may expand a character into several (`İ` becomes
//! `i̇`), which shifts offsets and lengths. These helpers use a character's
//! single-character mapping and keep it unchanged when there is none, so the
//! result has as many characters as the input: identifiers can be compared
//! and indexed case-insensitively without changing their shape.

/// The lowercase form of `character`, or `character` itself when its
/// lowercase form is not a single character. `İ` (U+0130) maps to `i`.
pub fn lowercase_char(character: char) -> char {
    if character.is_ascii() {
        return character.to_ascii_lowercase();
    }
    if character == '\u{130}' {
        return 'i';
    }
    single(character.to_lowercase()).unwrap_or(character)
}

/// The uppercase form of `character`, or `character` itself when its
/// uppercase form is not a single character.
pub fn uppercase_char(character: char) -> char {
    if character.is_ascii() {
        return character.to_ascii_uppercase();
    }
    single(character.to_uppercase()).unwrap_or(character)
}

/// `value` with every character mapped by [`lowercase_char`].
pub fn lowercase(value: &str) -> String {
    if value.is_ascii() {
        return value.to_ascii_lowercase();
    }
    value.chars().map(lowercase_char).collect()
}

/// `value` with every character mapped by [`uppercase_char`].
pub fn uppercase(value: &str) -> String {
    if value.is_ascii() {
        return value.to_ascii_uppercase();
    }
    value.chars().map(uppercase_char).collect()
}

/// `value` with its first character mapped by [`uppercase_char`].
pub fn uppercase_first(value: &str) -> String {
    let mut chars = value.chars();
    match chars.next() {
        Some(first) => {
            let mut result = String::with_capacity(value.len());
            result.push(uppercase_char(first));
            result.push_str(chars.as_str());
            result
        }
        None => String::new(),
    }
}

/// Whether `left` and `right` are equal ignoring case, comparing character
/// by character through simple case folding (`ſ` matches `s`, `K` (Kelvin)
/// matches `k`).
pub fn eq_ignore_case(left: &str, right: &str) -> bool {
    if left.is_ascii() && right.is_ascii() {
        return left.eq_ignore_ascii_case(right);
    }
    let mut left = left.chars();
    let mut right = right.chars();
    loop {
        match (left.next(), right.next()) {
            (None, None) => return true,
            (Some(a), Some(b)) if a == b || fold(a) == fold(b) => {}
            _ => return false,
        }
    }
}

fn fold(character: char) -> char {
    match character {
        '\u{17f}' => 's',
        '\u{212a}' => 'k',
        '\u{2126}' => '\u{3c9}',
        _ => lowercase_char(character),
    }
}

fn single(mut mapped: impl Iterator<Item = char>) -> Option<char> {
    match (mapped.next(), mapped.next()) {
        (Some(value), None) => Some(value),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn keeps_one_character_per_character() {
        assert_eq!(lowercase("İSTANBUL"), "istanbul");
        assert_eq!(lowercase("Ärger"), "ärger");
        assert_eq!(uppercase("straße"), "STRAßE");
        assert_eq!(uppercase_first("élan"), "Élan");
        assert_eq!(uppercase_first(""), "");
    }

    #[test]
    fn compares_through_simple_folding() {
        assert!(eq_ignore_case("aſ", "AS"));
        assert!(eq_ignore_case("Ärger", "äRGER"));
        assert!(eq_ignore_case("\u{212a}elvin", "kelvin"));
        assert!(!eq_ignore_case("abc", "abd"));
        assert!(!eq_ignore_case("ab", "abc"));
    }
}
