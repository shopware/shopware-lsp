//! Integer literals with a base prefix, and counts for people.

/// A count with thousands separators (`18,412`).
pub fn grouped(count: usize) -> String {
    let digits = count.to_string();
    let mut text = String::with_capacity(digits.len() + digits.len() / 3);
    for (index, digit) in digits.chars().enumerate() {
        if index > 0 && (digits.len() - index).is_multiple_of(3) {
            text.push(',');
        }
        text.push(digit);
    }
    text
}

/// Parses an integer literal: an optional sign, then decimal digits, `0x`
/// hexadecimal, `0o` or `0` octal, or `0b` binary digits (prefixes are case
/// insensitive). Out-of-range values and anything else are `None`.
pub fn parse_int_literal(text: &str) -> Option<i64> {
    let (negative, digits) = match text.as_bytes().first()? {
        b'-' => (true, &text[1..]),
        b'+' => (false, &text[1..]),
        _ => (false, text),
    };
    let magnitude = i128::from(parse_uint_literal(digits)?);
    i64::try_from(if negative { -magnitude } else { magnitude }).ok()
}

/// [`parse_int_literal`] without a sign.
pub fn parse_uint_literal(text: &str) -> Option<u64> {
    let prefix = text.get(..2).map(str::to_ascii_lowercase);
    let (radix, digits) = match prefix.as_deref() {
        Some("0x") => (16, &text[2..]),
        Some("0o") => (8, &text[2..]),
        Some("0b") => (2, &text[2..]),
        _ if text.len() > 1 && text.starts_with('0') => (8, &text[1..]),
        _ => (10, text),
    };
    if digits.is_empty() || !digits.chars().all(|c| c.is_digit(radix)) {
        return None;
    }
    u64::from_str_radix(digits, radix).ok()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_prefixed_literals() {
        assert_eq!(parse_int_literal("42"), Some(42));
        assert_eq!(parse_int_literal("-0x1F"), Some(-31));
        assert_eq!(parse_int_literal("0o17"), Some(15));
        assert_eq!(parse_int_literal("017"), Some(15));
        assert_eq!(parse_int_literal("0B101"), Some(5));
        assert_eq!(parse_int_literal("+7"), Some(7));
        assert_eq!(parse_int_literal("-9223372036854775808"), Some(i64::MIN));
        assert_eq!(parse_int_literal("9223372036854775808"), None);
        assert_eq!(parse_int_literal("0x"), None);
        assert_eq!(parse_int_literal("1_000"), None);
        assert_eq!(parse_int_literal("--1"), None);
        assert_eq!(parse_int_literal(""), None);
    }
}
