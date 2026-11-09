//! Line-based declaration parsers for `.env`, Dockerfile and Docker Compose
//! files. All cursors are byte offsets; they only stop on ASCII bytes, so
//! every slice lies on a character boundary.

use crate::model::{Occurrence, OccurrenceKind, SourceKind, range};

struct SourceLine<'a> {
    start: usize,
    text: &'a str,
}

pub(crate) fn parse_dot_env(source: &str) -> Vec<Occurrence> {
    let mut result = Vec::new();
    for line in source_lines(source) {
        let text = line.text;
        let bytes = text.as_bytes();
        let mut cursor = skip_space(text, 0);
        if text[cursor..].starts_with("export") {
            let next = cursor + "export".len();
            if next < bytes.len() && is_space(bytes[next]) {
                cursor = skip_space(text, next);
            }
        }
        let name_start = cursor;
        if cursor >= bytes.len() || !is_name_start(bytes[cursor]) {
            continue;
        }
        cursor += 1;
        while cursor < bytes.len() && is_name_part(bytes[cursor]) {
            cursor += 1;
        }
        let name_end = cursor;
        cursor = skip_space(text, cursor);
        if cursor >= bytes.len() || bytes[cursor] != b'=' {
            continue;
        }
        cursor = skip_space(text, cursor + 1);
        result.push(Occurrence {
            kind: OccurrenceKind::Declaration,
            source: SourceKind::DotEnv,
            name: text[name_start..name_end].to_owned(),
            value: dotenv_value(&text[cursor..]).to_owned(),
            range: range(line.start + name_start, line.start + text.len()),
            name_range: range(line.start + name_start, line.start + name_end),
            ..Occurrence::default()
        });
    }
    result
}

fn dotenv_value(value: &str) -> &str {
    let value = value.trim();
    let bytes = value.as_bytes();
    let Some(&first) = bytes.first() else {
        return "";
    };
    if first == b'\'' || first == b'"' {
        let quote = first;
        let mut escaped = false;
        for (index, &current) in bytes.iter().enumerate().skip(1) {
            if escaped {
                escaped = false;
            } else if current == b'\\' && quote == b'"' {
                escaped = true;
            } else if current == quote {
                return &value[1..index];
            }
        }
        return &value[1..];
    }
    for (index, &current) in bytes.iter().enumerate() {
        if current == b'#' && (index == 0 || is_space(bytes[index - 1])) {
            return value[..index].trim();
        }
    }
    value
}

pub(crate) fn parse_dockerfile(source: &str) -> Vec<Occurrence> {
    let mut result = Vec::new();
    for line in source_lines(source) {
        let text = line.text;
        let bytes = text.as_bytes();
        let mut cursor = skip_space(text, 0);
        if bytes.len() - cursor < 3 || !bytes[cursor..cursor + 3].eq_ignore_ascii_case(b"ENV") {
            continue;
        }
        cursor += 3;
        if cursor >= bytes.len() || !is_space(bytes[cursor]) {
            continue;
        }
        cursor = skip_space(text, cursor);
        let tokens = shell_tokens(&text[cursor..], cursor);
        let Some(first) = tokens.first() else {
            continue;
        };
        if first.value.contains('=') {
            for token in &tokens {
                let Some(equals) = token.value.find('=').filter(|equals| *equals > 0) else {
                    continue;
                };
                let name = &token.value[..equals];
                if !valid_name(name) {
                    continue;
                }
                result.push(Occurrence {
                    kind: OccurrenceKind::Declaration,
                    source: SourceKind::Dockerfile,
                    name: name.to_owned(),
                    value: trim_quotes(&token.value[equals + 1..]).to_owned(),
                    range: range(line.start + token.start, line.start + token.end),
                    name_range: range(
                        line.start + token.start,
                        line.start + token.start + name.len(),
                    ),
                    ..Occurrence::default()
                });
            }
            continue;
        }
        let name = trim_quotes(first.value);
        if !valid_name(name) {
            continue;
        }
        let value = if first.end < text.len() {
            trim_quotes(text[first.end..].trim())
        } else {
            ""
        };
        result.push(Occurrence {
            kind: OccurrenceKind::Declaration,
            source: SourceKind::Dockerfile,
            name: name.to_owned(),
            value: value.to_owned(),
            range: range(line.start + first.start, line.start + text.len()),
            name_range: range(
                line.start + first.start,
                line.start + first.start + name.len(),
            ),
            ..Occurrence::default()
        });
    }
    result
}

struct ShellToken<'a> {
    value: &'a str,
    start: usize,
    end: usize,
}

fn shell_tokens(value: &str, base: usize) -> Vec<ShellToken<'_>> {
    let bytes = value.as_bytes();
    let mut result = Vec::new();
    let mut cursor = 0;
    while cursor < bytes.len() {
        cursor = skip_space(value, cursor);
        if cursor >= bytes.len() || bytes[cursor] == b'#' {
            break;
        }
        let start = cursor;
        let mut quote = 0u8;
        let mut escaped = false;
        while cursor < bytes.len() {
            let current = bytes[cursor];
            if escaped {
                escaped = false;
            } else if current == b'\\' {
                escaped = true;
            } else if quote != 0 && current == quote {
                quote = 0;
            } else if quote == 0 && (current == b'\'' || current == b'"') {
                quote = current;
            } else if quote == 0 && is_space(current) {
                break;
            }
            cursor += 1;
        }
        result.push(ShellToken {
            value: &value[start..cursor],
            start: base + start,
            end: base + cursor,
        });
    }
    result
}

pub(crate) fn parse_docker_compose(source: &str) -> Vec<Occurrence> {
    let mut result = Vec::new();
    let mut environment_indent: Option<usize> = None;
    for line in source_lines(source) {
        let trimmed = line.text.trim();
        if trimmed.is_empty() || trimmed.starts_with('#') {
            continue;
        }
        let indent = line.text.len() - line.text.trim_start_matches([' ', '\t']).len();
        if let Some(header_indent) = environment_indent {
            if indent <= header_indent {
                environment_indent = None;
            } else if let Some(occurrence) = compose_declaration(&line, indent) {
                result.push(occurrence);
                continue;
            }
        }
        let header = strip_yaml_comment(line.text).trim();
        if header.eq_ignore_ascii_case("environment:") {
            environment_indent = Some(indent);
        }
    }
    result
}

fn compose_declaration(line: &SourceLine<'_>, indent: usize) -> Option<Occurrence> {
    let text = strip_yaml_comment(line.text);
    let bytes = text.as_bytes();
    let cursor = indent;
    if cursor >= bytes.len() {
        return None;
    }
    if bytes[cursor] == b'-' {
        let value_start = skip_space(text, cursor + 1);
        let value_end = text.trim_end_matches([' ', '\t']).len();
        if value_start >= value_end {
            return None;
        }
        let raw = trim_quotes(&text[value_start..value_end]);
        let (name, value) = match raw.find('=') {
            Some(equals) => (&raw[..equals], &raw[equals + 1..]),
            None => (raw, ""),
        };
        if !valid_name(name) {
            return None;
        }
        let quote_adjustment = usize::from(matches!(bytes[value_start], b'\'' | b'"'));
        let name_start = value_start + quote_adjustment;
        return Some(declaration(
            line,
            name,
            value,
            name_start,
            name_start + name.len(),
            value_start,
            value_end,
        ));
    }

    let colon = cursor + yaml_mapping_colon(&text[cursor..])?;
    let raw_name = text[cursor..colon].trim();
    let name = trim_quotes(raw_name);
    if !valid_name(name) {
        return None;
    }
    let name_start = cursor + text[cursor..colon].find(name)?;
    let value = trim_quotes(text[colon + 1..].trim());
    Some(declaration(
        line,
        name,
        value,
        name_start,
        name_start + name.len(),
        cursor,
        text.trim_end_matches([' ', '\t']).len(),
    ))
}

fn declaration(
    line: &SourceLine<'_>,
    name: &str,
    value: &str,
    name_start: usize,
    name_end: usize,
    start: usize,
    end: usize,
) -> Occurrence {
    Occurrence {
        kind: OccurrenceKind::Declaration,
        source: SourceKind::DockerCompose,
        name: name.to_owned(),
        value: value.to_owned(),
        range: range(line.start + start, line.start + end),
        name_range: range(line.start + name_start, line.start + name_end),
        ..Occurrence::default()
    }
}

fn strip_yaml_comment(value: &str) -> &str {
    let mut quote = 0u8;
    let mut escaped = false;
    for (index, &current) in value.as_bytes().iter().enumerate() {
        if escaped {
            escaped = false;
        } else if current == b'\\' && quote == b'"' {
            escaped = true;
        } else if quote != 0 && current == quote {
            quote = 0;
        } else if quote == 0 && (current == b'\'' || current == b'"') {
            quote = current;
        } else if quote == 0 && current == b'#' {
            return value[..index].trim_end_matches([' ', '\t']);
        }
    }
    value
}

fn yaml_mapping_colon(value: &str) -> Option<usize> {
    let mut quote = 0u8;
    for (index, &current) in value.as_bytes().iter().enumerate() {
        if quote != 0 && current == quote {
            quote = 0;
        } else if quote == 0 && (current == b'\'' || current == b'"') {
            quote = current;
        } else if quote == 0 && current == b':' {
            return Some(index);
        }
    }
    None
}

/// Lines without their `\n` and one trailing `\r`; a trailing newline yields
/// a final empty line.
fn source_lines(source: &str) -> impl Iterator<Item = SourceLine<'_>> {
    let mut start = 0;
    let mut done = false;
    std::iter::from_fn(move || {
        if done {
            return None;
        }
        let end = match source[start..].find('\n') {
            Some(relative) => start + relative,
            None => {
                done = true;
                source.len()
            }
        };
        let raw = &source[start..end];
        let line = SourceLine {
            start,
            text: raw.strip_suffix('\r').unwrap_or(raw),
        };
        start = end + 1;
        Some(line)
    })
}

fn trim_quotes(value: &str) -> &str {
    value.trim_matches(['"', '\''])
}

pub(crate) fn is_dot_env_path(path: &str) -> bool {
    let base = base_name(path);
    base.eq_ignore_ascii_case(".env")
        || has_prefix_fold(base, ".env.")
        || extension(base).eq_ignore_ascii_case(".env")
}

pub(crate) fn is_dockerfile_path(path: &str) -> bool {
    let base = base_name(path);
    base.eq_ignore_ascii_case("dockerfile") || has_prefix_fold(base, "dockerfile.")
}

pub(crate) fn is_docker_compose_path(path: &str) -> bool {
    let base = base_name(path);
    let extension = extension(base);
    if !extension.eq_ignore_ascii_case(".yaml") && !extension.eq_ignore_ascii_case(".yml") {
        return false;
    }
    let stem = &base[..base.len() - extension.len()];
    stem.eq_ignore_ascii_case("compose")
        || has_prefix_fold(stem, "compose.")
        || stem.eq_ignore_ascii_case("docker-compose")
        || has_prefix_fold(stem, "docker-compose.")
}

/// The last element of a slash-separated path, ignoring trailing slashes;
/// `.` for an empty path and `/` for the root.
pub(crate) fn base_name(path: &str) -> &str {
    let trimmed = path.trim_end_matches('/');
    if trimmed.is_empty() {
        return if path.is_empty() { "." } else { "/" };
    }
    match trimmed.rfind('/') {
        Some(separator) => &trimmed[separator + 1..],
        None => trimmed,
    }
}

/// The suffix of the last slash-separated element from its final dot
/// (`.env`); empty when there is none.
pub(crate) fn extension(path: &str) -> &str {
    let base_start = path.rfind('/').map_or(0, |separator| separator + 1);
    match path[base_start..].rfind('.') {
        Some(dot) => &path[base_start + dot..],
        None => "",
    }
}

fn has_prefix_fold(source: &str, prefix: &str) -> bool {
    source.len() >= prefix.len()
        && source.as_bytes()[..prefix.len()].eq_ignore_ascii_case(prefix.as_bytes())
}

fn skip_space(value: &str, mut cursor: usize) -> usize {
    let bytes = value.as_bytes();
    while cursor < bytes.len() && is_space(bytes[cursor]) {
        cursor += 1;
    }
    cursor
}

pub(crate) fn is_space(value: u8) -> bool {
    value == b' ' || value == b'\t'
}

fn is_name_start(value: u8) -> bool {
    value == b'_' || value.is_ascii_alphabetic()
}

pub(crate) fn is_name_part(value: u8) -> bool {
    is_name_start(value) || value.is_ascii_digit()
}

pub(crate) fn valid_name(value: &str) -> bool {
    let bytes = value.as_bytes();
    !bytes.is_empty()
        && is_name_start(bytes[0])
        && bytes[1..].iter().all(|byte| is_name_part(*byte))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn summary(occurrences: &[Occurrence], source: &str) -> Vec<(String, String, String)> {
        occurrences
            .iter()
            .map(|occurrence| {
                let name_range = occurrence.name_range;
                let range = occurrence.range;
                assert_eq!(&source[name_range], occurrence.name);
                (
                    occurrence.name.clone(),
                    occurrence.value.clone(),
                    source[range].to_owned(),
                )
            })
            .collect()
    }

    #[test]
    fn dot_env_values() {
        let source = "\nAPP_ENV=dev\nexport DATABASE_URL = \"mysql://localhost/app\" # local\nEMPTY=\nQUOTED='a#b'\nHASH=a#b # c\r\n# comment\n1BAD=x\nexport=1\n";
        assert_eq!(
            summary(&parse_dot_env(source), source),
            vec![
                ("APP_ENV".into(), "dev".into(), "APP_ENV=dev".into()),
                (
                    "DATABASE_URL".into(),
                    "mysql://localhost/app".into(),
                    "DATABASE_URL = \"mysql://localhost/app\" # local".into()
                ),
                ("EMPTY".into(), "".into(), "EMPTY=".into()),
                ("QUOTED".into(), "a#b".into(), "QUOTED='a#b'".into()),
                ("HASH".into(), "a#b".into(), "HASH=a#b # c".into()),
                ("export".into(), "1".into(), "export=1".into()),
            ]
        );
        assert_eq!(dotenv_value("\"unterminated"), "unterminated");
        assert_eq!(dotenv_value("\"a\\\"b\" rest"), "a\\\"b");
    }

    #[test]
    fn dockerfile_values() {
        let source = "ENV APP_RUNTIME Runtime\nenv PHP_MEMORY_LIMIT=512M APP_DEBUG=\"1\" # c\nENVX=1\nENV =bad A-B=1\n";
        assert_eq!(
            summary(&parse_dockerfile(source), source),
            vec![
                (
                    "APP_RUNTIME".into(),
                    "Runtime".into(),
                    "APP_RUNTIME Runtime".into()
                ),
                (
                    "PHP_MEMORY_LIMIT".into(),
                    "512M".into(),
                    "PHP_MEMORY_LIMIT=512M".into()
                ),
                ("APP_DEBUG".into(), "1".into(), "APP_DEBUG=\"1\"".into()),
            ]
        );
    }

    #[test]
    fn docker_compose_values() {
        let source = "services:\n  app:\n    environment:\n      DATABASE_HOST: database # c\n      - APP_SECRET=not-secret\n      - \"QUOTED=1\"\n      - WORKER_ENABLED\n      'KEY': \"v\"\n    labels:\n      example: value\n";
        assert_eq!(
            summary(&parse_docker_compose(source), source),
            vec![
                (
                    "DATABASE_HOST".into(),
                    "database".into(),
                    "DATABASE_HOST: database".into()
                ),
                (
                    "APP_SECRET".into(),
                    "not-secret".into(),
                    "APP_SECRET=not-secret".into()
                ),
                ("QUOTED".into(), "1".into(), "\"QUOTED=1\"".into()),
                ("WORKER_ENABLED".into(), "".into(), "WORKER_ENABLED".into()),
                ("KEY".into(), "v".into(), "'KEY': \"v\"".into()),
            ]
        );
    }

    #[test]
    fn base_name_and_extension_use_the_last_path_element() {
        assert_eq!(base_name("/a/b/.env"), ".env");
        assert_eq!(base_name("/a/b/"), "b");
        assert_eq!(extension("/a/.env"), ".env");
        assert_eq!(extension("/a.b/c"), "");
        assert_eq!(extension("x.tar.gz"), ".gz");
        assert!(is_dot_env_path("/project/.ENV.local"));
        assert!(is_dockerfile_path("/project/DOCKERFILE.dev"));
        assert!(is_docker_compose_path(
            "/project/Docker-Compose.override.YAML"
        ));
        assert!(!is_docker_compose_path("/project/compose-x.yaml"));
    }
}
