//! Go formatting helpers: `strconv.Quote`, `time.Duration` text and
//! parsing, `%v` of a float64 and Go's `*PathError` messages.

use std::fmt::Write as _;
use std::io;

/// Go `strconv.Quote`.
pub fn go_quote(value: &str) -> String {
    let mut result = String::with_capacity(value.len() + 2);
    result.push('"');
    for character in value.chars() {
        match character {
            '"' => result.push_str("\\\""),
            '\\' => result.push_str("\\\\"),
            '\u{7}' => result.push_str("\\a"),
            '\u{8}' => result.push_str("\\b"),
            '\u{c}' => result.push_str("\\f"),
            '\n' => result.push_str("\\n"),
            '\r' => result.push_str("\\r"),
            '\t' => result.push_str("\\t"),
            '\u{b}' => result.push_str("\\v"),
            character if (character as u32) < 0x20 || character as u32 == 0x7f => {
                let _ = write!(result, "\\x{:02x}", character as u32);
            }
            character if is_go_printable(character) => result.push(character),
            character if (character as u32) < 0x10000 => {
                let _ = write!(result, "\\u{:04x}", character as u32);
            }
            character => {
                let _ = write!(result, "\\U{:08x}", character as u32);
            }
        }
    }
    result.push('"');
    result
}

fn is_go_printable(character: char) -> bool {
    if (character as u32) < 0x80 {
        return (0x20..0x7f).contains(&(character as u32));
    }
    !(character.is_control()
        || matches!(
            character as u32,
            0x00ad | 0x0600..=0x0605 | 0x061c | 0x06dd | 0x070f | 0x180e | 0x200b..=0x200f
                | 0x2028..=0x202e | 0x2060..=0x206f | 0xfeff | 0xfff9..=0xfffb
                | 0xd800..=0xdfff | 0xe000..=0xf8ff | 0xfff0..=0xfff8 | 0xfffe | 0xffff
        )
        || (character as u32) >= 0xf0000)
}

/// Go `fmt.Sprintf("%q", []string{...})`.
pub fn go_quote_list<S: AsRef<str>>(values: &[S]) -> String {
    let quoted: Vec<String> = values
        .iter()
        .map(|value| go_quote(value.as_ref()))
        .collect();
    format!("[{}]", quoted.join(" "))
}

/// Go `time.Duration.String`.
pub fn format_duration(nanos: i64) -> String {
    if nanos == 0 {
        return "0s".into();
    }
    let negative = nanos < 0;
    let mut u = nanos.unsigned_abs();
    let mut result = String::new();
    if u < 1_000_000_000 {
        // Sub-second: use ns, µs or ms with a fraction.
        let (unit, precision) = if u < 1_000 {
            ("ns", 0)
        } else if u < 1_000_000 {
            ("µs", 3)
        } else {
            ("ms", 6)
        };
        result.push_str(&format_fraction(u, precision));
        result.push_str(unit);
    } else {
        // Seconds within the minute, with a nanosecond fraction.
        let mut text = format_fraction(u % 60_000_000_000, 9);
        text.push('s');
        u /= 1_000_000_000;
        let minutes = u / 60;
        if minutes > 0 {
            let hours = minutes / 60;
            text = format!("{}m{}", minutes % 60, text);
            if hours > 0 {
                text = format!("{hours}h{text}");
            }
        }
        result = text;
    }
    if negative {
        result.insert(0, '-');
    }
    result
}

/// Formats `value` scaled down by 10^precision, trimming trailing zeros.
fn format_fraction(value: u64, precision: u32) -> String {
    if precision == 0 {
        return value.to_string();
    }
    let scale = 10u64.pow(precision);
    let whole = value / scale;
    let fraction = value % scale;
    if fraction == 0 {
        return whole.to_string();
    }
    let digits = format!("{:0width$}", fraction, width = precision as usize);
    format!("{whole}.{}", digits.trim_end_matches('0'))
}

/// Go `time.Duration.Round`.
pub fn round_duration(nanos: i64, multiple: i64) -> i64 {
    if multiple <= 0 {
        return nanos;
    }
    let mut remainder = nanos % multiple;
    if nanos < 0 {
        remainder = -remainder;
        if remainder + remainder < multiple {
            return nanos + remainder;
        }
        return nanos.saturating_sub(multiple - remainder);
    }
    if remainder + remainder < multiple {
        return nanos - remainder;
    }
    nanos.saturating_add(multiple - remainder)
}

/// Go `time.ParseDuration`; `None` on any error.
pub fn parse_duration(input: &str) -> Option<i64> {
    let mut s = input;
    let mut negative = false;
    if let Some(rest) = s.strip_prefix('-') {
        negative = true;
        s = rest;
    } else if let Some(rest) = s.strip_prefix('+') {
        s = rest;
    }
    if s == "0" {
        return Some(0);
    }
    if s.is_empty() {
        return None;
    }
    let mut total: f64 = 0.0;
    while !s.is_empty() {
        let bytes = s.as_bytes();
        if !(bytes[0] == b'.' || bytes[0].is_ascii_digit()) {
            return None;
        }
        let number_end = s
            .find(|character: char| !(character.is_ascii_digit() || character == '.'))
            .unwrap_or(s.len());
        let number = &s[..number_end];
        if number == "." || number.matches('.').count() > 1 {
            return None;
        }
        let value: f64 = number.parse().ok()?;
        s = &s[number_end..];
        let unit_end = s
            .find(|character: char| character == '.' || character.is_ascii_digit())
            .unwrap_or(s.len());
        let unit = &s[..unit_end];
        s = &s[unit_end..];
        let scale = match unit {
            "ns" => 1.0,
            "us" | "µs" | "μs" => 1e3,
            "ms" => 1e6,
            "s" => 1e9,
            "m" => 60e9,
            "h" => 3600e9,
            _ => return None,
        };
        total += value * scale;
    }
    if total > i64::MAX as f64 {
        return None;
    }
    let nanos = total as i64;
    Some(if negative { -nanos } else { nanos })
}

/// Go `fmt.Sprint(float64)` (`%v`, shortest `%g` with exponent threshold 6).
pub fn format_float_v(value: f64) -> String {
    if value.is_nan() {
        return "NaN".into();
    }
    if value.is_infinite() {
        return if value > 0.0 {
            "+Inf".into()
        } else {
            "-Inf".into()
        };
    }
    if value == 0.0 {
        return if value.is_sign_negative() {
            "-0".into()
        } else {
            "0".into()
        };
    }
    let (negative, digits, exponent) = shortest_digits(value);
    let mut result = String::new();
    if negative {
        result.push('-');
    }
    if !(-4..6).contains(&exponent) {
        result.push_str(&exponent_form(&digits, exponent));
    } else {
        result.push_str(&fixed_form(&digits, exponent));
    }
    result
}

/// Go `encoding/json` float64 encoding.
pub fn format_float_json(value: f64) -> String {
    if value == 0.0 {
        return "0".into();
    }
    let (negative, digits, exponent) = shortest_digits(value);
    let mut result = String::new();
    if negative {
        result.push('-');
    }
    let absolute = value.abs();
    if !(1e-6..1e21).contains(&absolute) {
        let mut text = exponent_form(&digits, exponent);
        // Go cleans e-09 to e-9.
        if let Some(index) = text.find("e-0") {
            text.replace_range(index..index + 3, "e-");
        }
        result.push_str(&text);
    } else {
        result.push_str(&fixed_form(&digits, exponent));
    }
    result
}

/// Shortest round-trip decimal digits and the decimal exponent of the first
/// digit.
fn shortest_digits(value: f64) -> (bool, String, i32) {
    let text = format!("{:e}", value.abs());
    let (mantissa, exponent) = text.split_once('e').unwrap_or((&text, "0"));
    let digits: String = mantissa
        .chars()
        .filter(|character| *character != '.')
        .collect();
    (
        value.is_sign_negative(),
        digits,
        exponent.parse().unwrap_or(0),
    )
}

fn exponent_form(digits: &str, exponent: i32) -> String {
    let mut result = String::new();
    result.push_str(&digits[..1]);
    if digits.len() > 1 {
        result.push('.');
        result.push_str(&digits[1..]);
    }
    let sign = if exponent < 0 { '-' } else { '+' };
    let _ = write!(result, "e{sign}{:02}", exponent.abs());
    result
}

fn fixed_form(digits: &str, exponent: i32) -> String {
    if exponent < 0 {
        let zeros = (-exponent - 1) as usize;
        return format!("0.{}{}", "0".repeat(zeros), digits);
    }
    let point = exponent as usize + 1;
    if digits.len() <= point {
        format!("{}{}", digits, "0".repeat(point - digits.len()))
    } else {
        format!("{}.{}", &digits[..point], &digits[point..])
    }
}

/// Go's text for a syscall error (`syscall.Errno.Error`).
pub fn errno_text(error: &io::Error) -> String {
    #[cfg(unix)]
    if let Some(code) = error.raw_os_error() {
        // SAFETY: strerror returns a pointer to a static or thread-local
        // NUL-terminated string.
        let text = unsafe { std::ffi::CStr::from_ptr(libc::strerror(code)) }
            .to_string_lossy()
            .into_owned();
        return lowercase_first(&text);
    }
    match error.kind() {
        io::ErrorKind::NotFound => "no such file or directory".into(),
        io::ErrorKind::PermissionDenied => "permission denied".into(),
        _ => error.to_string(),
    }
}

#[cfg(unix)]
fn lowercase_first(text: &str) -> String {
    let mut characters = text.chars();
    match characters.next() {
        Some(first) => first.to_lowercase().collect::<String>() + characters.as_str(),
        None => String::new(),
    }
}

/// Go `*fs.PathError` text: `op path: errno`.
pub fn path_error(op: &str, path: &str, error: &io::Error) -> String {
    format!("{op} {path}: {}", errno_text(error))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn formats_durations_like_go() {
        assert_eq!(format_duration(0), "0s");
        assert_eq!(format_duration(2_000_000), "2ms");
        assert_eq!(format_duration(1_234_000_000), "1.234s");
        assert_eq!(format_duration(125_000_000_000), "2m5s");
        assert_eq!(
            format_duration(3 * 3_600_000_000_000 + 1_000_000),
            "3h0m0.001s"
        );
        assert_eq!(format_duration(1_500), "1.5µs");
        assert_eq!(round_duration(1_500_000, 1_000_000), 2_000_000);
        assert_eq!(parse_duration("1.5s"), Some(1_500_000_000));
        assert_eq!(parse_duration("1h2m"), Some(3_720_000_000_000));
        assert_eq!(parse_duration("x"), None);
    }

    #[test]
    fn formats_floats_like_go() {
        let cases = [
            (1e6, "1e+06", "1000000"),
            (123456.0, "123456", "123456"),
            (1234567.0, "1.234567e+06", "1234567"),
            (1e21, "1e+21", "1e+21"),
            (1.5, "1.5", "1.5"),
            (0.0001, "0.0001", "0.0001"),
            (0.00001, "1e-05", "0.00001"),
            (1e-7, "1e-07", "1e-7"),
            (12.345678901234, "12.345678901234", "12.345678901234"),
        ];
        for (value, v, json) in cases {
            assert_eq!(format_float_v(value), v, "{value}");
            assert_eq!(format_float_json(value), json, "{value}");
        }
    }

    #[test]
    fn quotes_like_go() {
        assert_eq!(go_quote("a\"b\n\u{1}é"), "\"a\\\"b\\n\\x01é\"");
        assert_eq!(go_quote_list(&["a", "b\"c"]), "[\"a\" \"b\\\"c\"]");
    }
}
