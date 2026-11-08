//! Small Go-compatible string helpers shared by the importer and renderer.

pub use swlsp_php_types::{equal_fold, go_quote, go_to_lower};

/// Go `strings.Trim(value, "\\ ")`.
pub(crate) fn trim_class(value: &str) -> &str {
    value.trim_matches(|c| c == '\\' || c == ' ')
}

/// Go `strings.Trim(value, "\\")`.
pub(crate) fn trim_backslash(value: &str) -> &str {
    value.trim_matches('\\')
}

pub(crate) fn trim_suffix<'a>(value: &'a str, suffix: &str) -> &'a str {
    value.strip_suffix(suffix).unwrap_or(value)
}

pub(crate) fn trim_prefix<'a>(value: &'a str, prefix: &str) -> &'a str {
    value.strip_prefix(prefix).unwrap_or(value)
}

pub(crate) fn default_string(value: &str, fallback: &str) -> String {
    if value.is_empty() {
        fallback.to_owned()
    } else {
        value.to_owned()
    }
}

pub(crate) fn qualify(namespace: &str, class: &str) -> String {
    if namespace.is_empty() {
        class.to_owned()
    } else {
        format!("{namespace}\\{class}")
    }
}

pub(crate) fn namespace_of(class: &str) -> &str {
    let class = trim_backslash(class);
    match class.rfind('\\') {
        Some(index) => &class[..index],
        None => "",
    }
}

pub(crate) fn camelize_storage_name(value: &str) -> String {
    let mut result = String::with_capacity(value.len());
    for (index, part) in value.split('_').enumerate() {
        if index == 0 || part.is_empty() {
            result.push_str(part);
            continue;
        }
        // Go upper-cases the first byte with strings.ToUpper.
        let first_len = part.chars().next().map_or(0, char::len_utf8);
        let (first, rest) = part.split_at(first_len);
        result.push_str(&go_to_upper(first));
        result.push_str(rest);
    }
    result
}

fn go_upper_char(character: char) -> char {
    if character.is_ascii() {
        return character.to_ascii_uppercase();
    }
    let mut upper = character.to_uppercase();
    match (upper.next(), upper.next()) {
        (Some(value), None) => value,
        _ => character,
    }
}

pub(crate) fn go_to_upper(value: &str) -> String {
    value.chars().map(go_upper_char).collect()
}

/// Upper-cases the first rune (Go `unicode.ToUpper`).
pub(crate) fn exported(value: &str) -> String {
    let mut chars = value.chars();
    match chars.next() {
        Some(first) => {
            let mut result = String::with_capacity(value.len());
            result.push(go_upper_char(first));
            result.push_str(chars.as_str());
            result
        }
        None => String::new(),
    }
}

pub(crate) fn infer_entity_name(definition_class: &str) -> String {
    let name = trim_suffix(crate::model::short_class(definition_class), "Definition");
    let mut result = String::with_capacity(name.len() + 4);
    for (index, character) in name.char_indices() {
        if character.is_uppercase() && index > 0 {
            result.push('_');
        }
        result.push_str(&go_to_lower(character.encode_utf8(&mut [0; 4])));
    }
    result
}

pub(crate) fn quote_php(value: &str) -> String {
    format!("'{}'", escape_php_single(value))
}

pub(crate) fn escape_php_single(value: &str) -> String {
    value.replace('\\', "\\\\").replace('\'', "\\'")
}

pub(crate) fn nullable_int(value: Option<i64>) -> String {
    match value {
        Some(value) => value.to_string(),
        None => "null".to_owned(),
    }
}

pub(crate) fn with_modifiers(
    base: &str,
    before: &[String],
    flag_suffix: &str,
    after: &[String],
) -> String {
    let mut result = String::from(base);
    for modifier in before {
        result.push_str(modifier.trim());
    }
    result.push_str(flag_suffix);
    for modifier in after {
        result.push_str(modifier.trim());
    }
    result
}

/// Go `strconv.FormatFloat(value, 'g', -1, 64)`.
pub(crate) fn format_float_g(value: f64) -> String {
    if value.is_nan() {
        return "NaN".to_owned();
    }
    if value.is_infinite() {
        return if value > 0.0 { "+Inf" } else { "-Inf" }.to_owned();
    }
    if value == 0.0 {
        return if value.is_sign_negative() { "-0" } else { "0" }.to_owned();
    }
    let scientific = format!("{value:e}");
    let (mantissa, exponent) = scientific.split_once('e').unwrap_or((&scientific, "0"));
    let exponent: i32 = exponent.parse().unwrap_or(0);
    // Shortest formatting decides with precision 6 (Go ftoa.go).
    if !(-4..6).contains(&exponent) {
        let sign = if exponent < 0 { '-' } else { '+' };
        let magnitude = exponent.unsigned_abs();
        if magnitude < 10 {
            format!("{mantissa}e{sign}0{magnitude}")
        } else {
            format!("{mantissa}e{sign}{magnitude}")
        }
    } else {
        format!("{value}")
    }
}

/// Go `strings.Fields(source)` joined with single spaces.
pub(crate) fn compact_fields(source: &str) -> String {
    source.split_whitespace().collect::<Vec<_>>().join(" ")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn formats_floats_like_go() {
        assert_eq!(format_float_g(500.0), "500");
        assert_eq!(format_float_g(0.25), "0.25");
        assert_eq!(format_float_g(80.0), "80");
        assert_eq!(format_float_g(1e6), "1e+06");
        assert_eq!(format_float_g(123456.0), "123456");
        assert_eq!(format_float_g(1234567.0), "1.234567e+06");
        assert_eq!(format_float_g(0.0001), "0.0001");
        assert_eq!(format_float_g(0.00001), "1e-05");
        assert_eq!(format_float_g(1.5e300), "1.5e+300");
    }
}
