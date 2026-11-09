//! Small string helpers shared by the importer and renderer.

pub(crate) use swlsp_textutil::{eq_ignore_case, lowercase, uppercase_first};

/// `value` without leading and trailing backslashes and spaces.
pub(crate) fn trim_class(value: &str) -> &str {
    value.trim_matches(|c| c == '\\' || c == ' ')
}

/// `value` without leading and trailing backslashes.
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
        result.push_str(&uppercase_first(part));
    }
    result
}

pub(crate) fn infer_entity_name(definition_class: &str) -> String {
    let name = trim_suffix(crate::model::short_class(definition_class), "Definition");
    let mut result = String::with_capacity(name.len() + 4);
    for (index, character) in name.char_indices() {
        if character.is_uppercase() && index > 0 {
            result.push('_');
        }
        result.push(swlsp_textutil::lowercase_char(character));
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

/// The shortest representation of `value` that parses back to it, in `%g`
/// style: scientific notation (`1e+06`, `1e-05`, two-digit minimum exponent)
/// when the decimal exponent is below -4 or at least 6, plain decimal
/// otherwise; `NaN`, `+Inf` and `-Inf` for special values.
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

/// `source` with every whitespace run collapsed to one space and no
/// leading or trailing whitespace.
pub(crate) fn compact_fields(source: &str) -> String {
    source.split_whitespace().collect::<Vec<_>>().join(" ")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn formats_floats_shortest_in_g_style() {
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
