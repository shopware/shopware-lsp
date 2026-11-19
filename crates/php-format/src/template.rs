//! Documents with markup outside PHP tags. PER Coding Style lays out PHP
//! files; of a template it only asks that the header blocks stand first.
//! So the lines of a template stay as they are, with their indentation,
//! and the formatter sets the spaces inside each line: around operators,
//! after commas and keywords, inside parentheses.

use swlsp_parser_php::syntax::*;

use crate::spacing;
use crate::tokens::Doc;

pub(crate) fn emit(doc: &Doc) -> String {
    let mut out = String::with_capacity(doc.source.len() + 16);
    let mut previous: Option<usize> = None;
    for index in 0..doc.toks.len() {
        let tok = &doc.toks[index];
        if let Some(previous) = previous {
            let markup = doc.kind(index) == TK_INLINE_HTML || doc.kind(previous) == TK_INLINE_HTML;
            if markup {
                // The markup holds the white space around it.
            } else if tok.nl > 0 {
                // The line break and the indentation of the source.
                let line = doc.source[..tok.start].rfind('\n').map_or(0, |at| at + 1);
                out.truncate(out.trim_end_matches([' ', '\t']).len());
                out.push('\n');
                if tok.nl > 1 {
                    out.push('\n');
                }
                out.push_str(&doc.source[line..tok.start]);
            } else if space(doc, previous, index) {
                out.push(' ');
            }
        }
        if doc.is_comment(index) {
            out.push_str(doc.source_text(index).trim_end());
        } else {
            out.push_str(doc.text(index));
        }
        previous = Some(index);
    }
    // A template ends as its markup does; PHP ends with one line feed.
    if previous.is_some_and(|last| doc.kind(last) != TK_INLINE_HTML) {
        out.truncate(out.trim_end().len());
        out.push('\n');
    }
    out
}

/// Whether a space stands between two tokens on one line.
fn space(doc: &Doc, previous: usize, index: usize) -> bool {
    if doc.kind(previous) == TK_OPEN_TAG || doc.kind(index) == TK_CLOSE_TAG {
        return true;
    }
    if doc.is_comment(index) || doc.is_comment(previous) {
        return true;
    }
    spacing::space(doc, previous, index)
}
