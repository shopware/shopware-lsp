use crate::{LogicalLineScanner, split_tag, split_type_and_description, split_type_and_variable};

/// Visits the lexical names referenced by a PHPDoc comment, including
/// annotation classes and newer type syntax which the type algebra might not
/// understand. Unknown tags are handled conservatively rather than deleting
/// their imports.
pub fn visit_reference_names(source: &str, mut visit: impl FnMut(&str)) {
    let mut lines = LogicalLineScanner::new(source);
    while let Some(line) = lines.next_line() {
        if !line.starts_with('@') {
            continue;
        }
        let (tag, mut value) = split_tag(&line);
        let annotation = tag.contains('\\') || tag.contains('(');
        match tag.as_str() {
            "@param" | "@phpstan-param" | "@psalm-param" | "@var" | "@phpstan-var"
            | "@psalm-var" | "@property" | "@property-read" | "@property-write" => {
                value = split_type_and_variable(value).0;
            }
            "@return"
            | "@phpstan-return"
            | "@psalm-return"
            | "@throws"
            | "@extends"
            | "@implements"
            | "@use"
            | "@phpstan-extends"
            | "@phpstan-implements" => {
                value = split_type_and_description(value).0;
            }
            _ => {}
        }
        visit_doc_names(
            tag.strip_prefix('@').unwrap_or(&tag),
            &mut visit,
            annotation,
        );
        visit_doc_names(value, &mut visit, annotation);
    }
}

fn visit_doc_names(text: &str, visit: &mut dyn FnMut(&str), quoted_names: bool) {
    let bytes = text.as_bytes();
    let mut index = 0;
    while index < bytes.len() {
        let current = bytes[index];
        if current == b'\'' || current == b'"' {
            index += 1;
            let start = index;
            while index < bytes.len() {
                if bytes[index] == b'\\' {
                    index += 2;
                    continue;
                }
                if bytes[index] == current {
                    if quoted_names && let Some(quoted) = text.get(start..index) {
                        visit_doc_names(quoted, visit, false);
                    }
                    index += 1;
                    break;
                }
                index += 1;
            }
            continue;
        }
        if current == b'$' {
            index += 1;
            while index < bytes.len() && doc_name_byte(bytes[index]) {
                index += 1;
            }
            continue;
        }
        if !doc_name_byte(current) || current.is_ascii_digit() {
            index += 1;
            continue;
        }
        let start = index;
        while index < bytes.len() && doc_name_byte(bytes[index]) {
            index += 1;
        }
        if let Some(name) = text.get(start..index) {
            visit(name);
        }
    }
}

fn doc_name_byte(value: u8) -> bool {
    value == b'\\' || value == b'_' || value >= 0x80 || value.is_ascii_alphanumeric()
}
