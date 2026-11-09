//! Symfony `@service` and `%parameter%` reference syntax.

/// Parses Symfony's `@service` syntax. Escaped `@@` values and
/// expression-language references are not static service uses.
pub fn normalize_service_reference(value: &str) -> Option<String> {
    parse_service_reference(value).map(|(name, _)| name)
}

/// Returns the referenced service ID and whether the reference is optional
/// (`@?id` or `@!id`).
pub fn parse_service_reference(value: &str) -> Option<(String, bool)> {
    let value = value.trim();
    if !value.starts_with('@') || value.starts_with("@@") {
        return None;
    }
    let mut value = &value[1..];
    let mut optional = false;
    if value.starts_with('?') || value.starts_with('!') {
        optional = true;
        value = &value[1..];
    }
    if value.is_empty() || value.starts_with('=') || value.contains(['%', '$', '{', '}']) {
        return None;
    }
    Some((value.to_owned(), optional))
}

/// Extracts `%parameter%` references, excluding escaped `%%` markers and
/// `env()`/`expr()` placeholders that are resolved dynamically.
pub fn parameter_references(value: &str) -> Vec<String> {
    let bytes = value.as_bytes();
    let mut result = Vec::new();
    let mut seen = std::collections::HashSet::new();
    let mut offset = 0;
    while offset < bytes.len() {
        let Some(start) = bytes[offset..].iter().position(|&b| b == b'%') else {
            break;
        };
        let start = start + offset;
        if start + 1 < bytes.len() && bytes[start + 1] == b'%' {
            offset = start + 2;
            continue;
        }
        let Some(end) = bytes[start + 1..].iter().position(|&b| b == b'%') else {
            break;
        };
        let end = end + start + 1;
        let name = value[start + 1..end].trim();
        offset = end + 1;
        if name.is_empty() || name.starts_with("env(") || name.starts_with("expr(") {
            continue;
        }
        if !seen.insert(name.to_lowercase()) {
            continue;
        }
        result.push(name.to_owned());
    }
    result
}
