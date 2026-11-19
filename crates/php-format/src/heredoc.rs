//! Heredocs and nowdocs: their lines are indented once past the line they
//! start on. PHP removes the indentation of the closing marker from every
//! line, so moving all lines together leaves the string as it is.

const INDENT: &str = "    ";

/// The lines of a heredoc: its opening line, its body and the white space
/// before and the name of its closing marker.
fn parts(text: &str) -> Option<(&str, Vec<&str>, &str, &str)> {
    if !text.starts_with("<<<") {
        return None;
    }
    let (opening, rest) = text.split_once('\n')?;
    let (body, closing) = match rest.rsplit_once('\n') {
        Some((body, closing)) => (body.split('\n').collect(), closing),
        None => (Vec::new(), rest),
    };
    let marker = closing.trim_start_matches([' ', '\t']);
    let indent = &closing[..closing.len() - marker.len()];
    Some((opening, body, indent, marker))
}

/// The text of a line without the indentation of the closing marker, as
/// PHP reads it. `None` when the line does not have that indentation.
fn content<'a>(line: &'a str, indent: &str) -> Option<&'a str> {
    let line = line.strip_suffix('\r').unwrap_or(line);
    match line.strip_prefix(indent) {
        Some(content) => Some(content),
        None if line.trim().is_empty() => Some(""),
        None => None,
    }
}

/// `text` with its lines `level` levels deep. `None` for other tokens and
/// for a heredoc that cannot be moved: one with a line that is not as deep
/// as its closing marker, as inside an interpolated expression.
pub(crate) fn indented(text: &str, level: usize) -> Option<String> {
    let (opening, body, indent, marker) = parts(text)?;
    let lines: Vec<&str> = body
        .iter()
        .map(|line| content(line, indent))
        .collect::<Option<_>>()?;
    let mut out = String::with_capacity(text.len() + (lines.len() + 1) * level * INDENT.len());
    out.push_str(opening.trim_end());
    for line in lines {
        out.push('\n');
        if !line.is_empty() {
            push_indent(&mut out, level);
            out.push_str(line);
        }
    }
    out.push('\n');
    push_indent(&mut out, level);
    out.push_str(marker);
    Some(out)
}

fn push_indent(out: &mut String, level: usize) {
    for _ in 0..level {
        out.push_str(INDENT);
    }
}

/// The string a heredoc stands for, whatever its indentation: what two
/// heredocs are compared by.
pub(crate) fn canonical(text: &str) -> Option<String> {
    let (opening, body, indent, marker) = parts(text)?;
    let mut out = String::from(opening.trim_end());
    for line in body {
        out.push('\n');
        out.push_str(content(line, indent).unwrap_or(line));
    }
    out.push('\n');
    out.push_str(marker);
    Some(out)
}
