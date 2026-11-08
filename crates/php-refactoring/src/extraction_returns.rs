//! Tagged results transport returns across the new callable boundary
//! without confusing an ordinary fallthrough with `return null` or a void
//! return.

use swlsp_parser_php::syntax::TK_SEMICOLON;
use swlsp_rewrite::Builder;
use swlsp_syntax::{SyntaxNode, TextRange};

use crate::extract_method::{MethodFlow, flow_output};
use crate::extraction_reindent::reindent_extraction_offsets;
use crate::{RefactorError, Result, child_tokens};

pub(crate) fn control_return_call(
    source: &str,
    call: &str,
    flow: &MethodFlow,
    newline: &str,
    indent: &str,
) -> String {
    let mut name = "$extractedResult".to_owned();
    let mut suffix = 2;
    while source.contains(&name[1..]) {
        name = format!("$extractedResult{suffix}");
        suffix += 1;
    }
    let mut lines = vec![format!("{name} = {call}")];
    if flow.value_return {
        lines.push(format!("if ({name}[0] === 1) {{ return {name}[1]; }}"));
    }
    if flow.void_return {
        lines.push(format!("if ({name}[0] === 2) {{ return; }}"));
    }
    if !flow.outputs.is_empty() {
        lines.push(format!("{} = {name}[1];", flow_output(&flow.outputs)));
    }
    lines.push(format!("{name} = null;"));
    lines.join(&format!("{newline}{indent}"))
}

pub(crate) fn reindent_control_returns(
    source: &str,
    range: TextRange,
    from: &str,
    to: &str,
    statements: &[SyntaxNode],
    returns: &[SyntaxNode],
) -> Result<String> {
    struct Marker {
        start: u32,
        end: u32,
        bare: bool,
    }
    let mut markers = Vec::with_capacity(returns.len());
    let mut positions = Vec::with_capacity(returns.len() * 2);
    for ret in returns {
        let mut marker = Marker {
            start: 0,
            end: 0,
            bare: ret.first_child().is_none(),
        };
        for token in child_tokens(ret) {
            if token.text() == "return" {
                marker.start = token.text_range().end().into();
            }
            if token.kind() == TK_SEMICOLON {
                marker.end = token.text_range().start().into();
            }
        }
        if marker.start == 0 || marker.end == 0 {
            return Err(RefactorError::UnstableReturn);
        }
        positions.extend([marker.start, marker.end]);
        markers.push(marker);
    }
    let (body, offsets) =
        reindent_extraction_offsets(source, range, from, to, statements, &mut positions);
    let offset = |position: u32| offsets.get(&position).copied().unwrap_or(0);
    let mut builder = Builder::new(&body);
    for marker in &markers {
        let (prefix, suffix) = if marker.bare {
            (" [2,", "null]")
        } else {
            (" [1,", "]")
        };
        builder.insert(offset(marker.start), prefix)?;
        builder.insert(offset(marker.end), suffix)?;
    }
    let edits = builder.finish()?;
    Ok(swlsp_rewrite::apply(&body, &edits)?)
}
