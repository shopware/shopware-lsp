//! One-based CLI positions (`file[:line[:column]]`) and code-action
//! selections, converted to zero-based LSP positions at this boundary.

use std::sync::OnceLock;

use regex::Regex;
use swlsp_lsp_protocol::{Position, Range};

use crate::CliError;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PositionTarget {
    pub path: String,
    pub position: Position,
}

fn line_column_suffix() -> &'static Regex {
    static PATTERN: OnceLock<Regex> = OnceLock::new();
    PATTERN.get_or_init(|| Regex::new(r"^(?s)(.*):(\d+):(\d+)$").expect("valid pattern"))
}

fn line_suffix() -> &'static Regex {
    static PATTERN: OnceLock<Regex> = OnceLock::new();
    PATTERN.get_or_init(|| Regex::new(r"^(?s)(.*):(\d+)$").expect("valid pattern"))
}

/// Go `parsePositionTarget`.
pub fn parse_position_target(value: &str) -> Result<PositionTarget, CliError> {
    if value.is_empty() {
        return Err(CliError::fail("empty file position"));
    }
    let mut target = PositionTarget {
        path: value.to_owned(),
        position: Position::default(),
    };
    let captures = line_column_suffix()
        .captures(value)
        .or_else(|| line_suffix().captures(value));
    let Some(captures) = captures else {
        return Ok(target);
    };
    let line = captures[2].parse::<i64>().ok().filter(|line| *line >= 1);
    let Some(line) = line else {
        return Err(CliError::fail("line must be at least 1"));
    };
    let mut column = 1;
    if let Some(value) = captures.get(3) {
        match value
            .as_str()
            .parse::<i64>()
            .ok()
            .filter(|column| *column >= 1)
        {
            Some(value) => column = value,
            None => return Err(CliError::fail("column must be at least 1")),
        }
    }
    target.path = captures[1].to_owned();
    target.position = Position {
        line: (line - 1) as u32,
        character: (column - 1) as u32,
    };
    Ok(target)
}

/// Go `codeActionSelection`: an exclusive one-based end position that
/// defaults to the start line / first column.
pub fn code_action_selection(
    start: Position,
    end_line: i64,
    end_column: i64,
) -> Result<Range, CliError> {
    if end_line < 0 || end_column < 0 {
        return Err(CliError::fail(
            "selection endpoints must be positive one-based positions",
        ));
    }
    let mut end = start;
    if end_line != 0 || end_column != 0 {
        let end_line = if end_line == 0 {
            i64::from(start.line) + 1
        } else {
            end_line
        };
        let end_column = if end_column == 0 { 1 } else { end_column };
        end = Position {
            line: (end_line - 1) as u32,
            character: (end_column - 1) as u32,
        };
    }
    if end.line < start.line || (end.line == start.line && end.character < start.character) {
        return Err(CliError::fail("selection end precedes start"));
    }
    Ok(Range { start, end })
}

pub fn require_one_argument(args: &[String], usage: &str) -> Result<String, CliError> {
    if args.len() != 1 {
        return Err(CliError::usage(usage));
    }
    Ok(args[0].clone())
}
