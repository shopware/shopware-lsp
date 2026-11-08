//! Argument and parameter list edits (port of `list.go`).

use swlsp_parser_php::query::{call_at, direct_child};
use swlsp_parser_php::syntax::{
    PHP_ARGUMENT, PHP_ARGUMENT_LIST, PHP_ARROW_FUNCTION, PHP_ATTRIBUTE, PHP_CLOSURE,
    PHP_FUNCTION_DECLARATION, PHP_METHOD_DECLARATION, PHP_NAMED_ARGUMENT, PHP_OBJECT_CREATION,
    PHP_PARAMETER, PHP_PARAMETER_LIST, TK_BLOCK_COMMENT, TK_CLOSE_PAREN, TK_COMMA, TK_LINE_COMMENT,
    TK_OPEN_PAREN,
};
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt};

use crate::editor::{Editor, Result, end, invalid, line_start, range, start, whitespace_prefix};

impl Editor<'_> {
    /// Inserts an argument before position `index` of a call, attribute, or
    /// object creation (or an argument list itself).
    pub fn insert_argument(
        &mut self,
        container: &SyntaxNode,
        index: usize,
        text: &str,
    ) -> Result<()> {
        let list = argument_list(container);
        let items = argument_items(list.as_ref());
        self.insert_comma_list_item(list.as_ref(), &items, index, text)
    }

    pub fn append_argument(&mut self, container: &SyntaxNode, text: &str) -> Result<()> {
        let list = argument_list(container);
        let items = argument_items(list.as_ref());
        self.insert_comma_list_item(list.as_ref(), &items, items.len(), text)
    }

    pub fn remove_argument(&mut self, container: &SyntaxNode, index: usize) -> Result<()> {
        let list = argument_list(container);
        let items = argument_items(list.as_ref());
        self.remove_comma_list_item(list.as_ref(), &items, index)
    }

    /// Inserts a parameter before position `index` of the function-like
    /// declaration enclosing `function_like` (or a parameter list itself).
    pub fn insert_parameter(
        &mut self,
        function_like: &SyntaxNode,
        index: usize,
        text: &str,
    ) -> Result<()> {
        let list = parameter_list(function_like);
        let items = parameter_items(list.as_ref());
        self.insert_comma_list_item(list.as_ref(), &items, index, text)
    }

    pub fn append_parameter(&mut self, function_like: &SyntaxNode, text: &str) -> Result<()> {
        let list = parameter_list(function_like);
        let items = parameter_items(list.as_ref());
        self.insert_comma_list_item(list.as_ref(), &items, items.len(), text)
    }

    pub fn remove_parameter(&mut self, function_like: &SyntaxNode, index: usize) -> Result<()> {
        let list = parameter_list(function_like);
        let items = parameter_items(list.as_ref());
        self.remove_comma_list_item(list.as_ref(), &items, index)
    }

    fn insert_comma_list_item(
        &mut self,
        list: Option<&SyntaxNode>,
        items: &[SyntaxNode],
        index: usize,
        text: &str,
    ) -> Result<()> {
        let text = text.trim();
        if text.is_empty() {
            return invalid("insert PHP list item: text is empty");
        }
        let Some(list) = list else {
            return invalid("insert PHP list item: list is unavailable");
        };
        if index > items.len() {
            return invalid(format!(
                "insert PHP list item: index {index} outside 0..{}",
                items.len()
            ));
        }
        let (Some(open), Some(close)) = (
            list.child_token_of_kind(TK_OPEN_PAREN),
            list.child_token_of_kind(TK_CLOSE_PAREN),
        ) else {
            return invalid("insert PHP list item: delimiters are unavailable");
        };
        let open_range = open.text_range();
        let close_start = start(close.text_range());
        let source = self.source;
        let multiline = source[end(open_range) as usize..close_start as usize].contains('\n');
        if index < items.len() {
            let current = items[index].range_trimmed_trivia();
            if range_has_token(
                list,
                start(items[index].text_range()),
                start(current),
                &[TK_LINE_COMMENT, TK_BLOCK_COMMENT],
            ) {
                return invalid("insert PHP list item: target item has a leading comment");
            }
            if multiline {
                let line = line_start(source, start(current));
                if let Some(indent) = whitespace_prefix(source, line, start(current))
                    && line > start(open_range)
                {
                    return self.insert(line, format!("{indent}{text},\n"));
                }
            }
            return self.insert(start(current), format!("{text}, "));
        }

        if !multiline {
            let Some(last) = items.last() else {
                return self.insert(close_start, text);
            };
            let last_end = end(last.range_trimmed_trivia());
            let suffix = &source[last_end as usize..close_start as usize];
            if range_has_token(list, last_end, close_start, &[TK_COMMA]) {
                let prefix = match suffix.bytes().last() {
                    Some(byte) if is_whitespace_byte(byte) => "",
                    _ => " ",
                };
                return self.insert(close_start, format!("{prefix}{text}"));
            }
            return self.insert(close_start, format!(", {text}"));
        }

        let close_line_start = line_start(source, close_start);
        let Some(close_indent) = whitespace_prefix(source, close_line_start, close_start)
            .filter(|_| close_line_start > start(open_range))
        else {
            return self.insert(close_start, format!(", {text}"));
        };
        let mut item_indent = format!("{close_indent}    ");
        if let Some(last) = items.last() {
            let last = last.range_trimmed_trivia();
            let last_line_start = line_start(source, start(last));
            if let Some(indent) = whitespace_prefix(source, last_line_start, start(last)) {
                item_indent = indent.to_owned();
            }
            if !range_has_token(list, end(last), close_start, &[TK_COMMA]) {
                self.insert(end(last), ",")?;
            }
        }
        self.insert(close_line_start, format!("{item_indent}{text},\n"))
    }

    fn remove_comma_list_item(
        &mut self,
        list: Option<&SyntaxNode>,
        items: &[SyntaxNode],
        index: usize,
    ) -> Result<()> {
        let Some(list) = list else {
            return invalid("remove PHP list item: list is unavailable");
        };
        if index >= items.len() {
            return invalid(format!(
                "remove PHP list item: index {index} outside 0..{}",
                items.len() as isize - 1
            ));
        }
        let (Some(open), Some(close)) = (
            list.child_token_of_kind(TK_OPEN_PAREN),
            list.child_token_of_kind(TK_CLOSE_PAREN),
        ) else {
            return invalid("remove PHP list item: delimiters are unavailable");
        };
        let open_end = end(open.text_range());
        let close_start = start(close.text_range());
        if items.len() == 1 {
            return self.replace_range(range(open_end, close_start), "");
        }
        let current = items[index].range_trimmed_trivia();
        if index + 1 < items.len() {
            let next = items[index + 1].range_trimmed_trivia();
            if range_has_token(
                list,
                end(current),
                start(next),
                &[TK_LINE_COMMENT, TK_BLOCK_COMMENT],
            ) {
                return invalid("remove PHP list item: separator contains a comment");
            }
            return self.replace_range(range(start(current), start(next)), "");
        }
        let previous = items[index - 1].range_trimmed_trivia();
        if range_has_token(
            list,
            end(previous),
            start(current),
            &[TK_LINE_COMMENT, TK_BLOCK_COMMENT],
        ) {
            return invalid("remove PHP list item: separator contains a comment");
        }
        let source = self.source;
        if source[open_end as usize..close_start as usize].contains('\n') {
            let current_line_start = line_start(source, start(current));
            let close_line_start = line_start(source, close_start);
            if whitespace_prefix(source, current_line_start, start(current)).is_some()
                && close_line_start > current_line_start
            {
                return self.replace_range(range(current_line_start, close_line_start), "");
            }
        }
        let removed_end = if range_has_token(list, end(current), close_start, &[TK_COMMA]) {
            close_start
        } else {
            end(current)
        };
        self.replace_range(range(end(previous), removed_end), "")
    }
}

fn argument_list(container: &SyntaxNode) -> Option<SyntaxNode> {
    if container.kind() == PHP_ARGUMENT_LIST {
        return Some(container.clone());
    }
    if let Some(call) = call_at(container) {
        return direct_child(&call, PHP_ARGUMENT_LIST);
    }
    match container.kind() {
        PHP_ATTRIBUTE | PHP_OBJECT_CREATION => direct_child(container, PHP_ARGUMENT_LIST),
        _ => None,
    }
}

fn parameter_list(function_like: &SyntaxNode) -> Option<SyntaxNode> {
    if function_like.kind() == PHP_PARAMETER_LIST {
        return Some(function_like.clone());
    }
    let declaration = function_like.ancestors().find(|current| {
        matches!(
            current.kind(),
            PHP_METHOD_DECLARATION | PHP_FUNCTION_DECLARATION | PHP_CLOSURE | PHP_ARROW_FUNCTION
        )
    })?;
    direct_child(&declaration, PHP_PARAMETER_LIST)
}

fn argument_items(list: Option<&SyntaxNode>) -> Vec<SyntaxNode> {
    direct_nodes(list, &[PHP_ARGUMENT, PHP_NAMED_ARGUMENT])
}

fn parameter_items(list: Option<&SyntaxNode>) -> Vec<SyntaxNode> {
    direct_nodes(list, &[PHP_PARAMETER])
}

pub(crate) fn direct_nodes(parent: Option<&SyntaxNode>, kinds: &[Kind]) -> Vec<SyntaxNode> {
    parent
        .map(|parent| {
            parent
                .children()
                .filter(|child| kinds.contains(&child.kind()))
                .collect()
        })
        .unwrap_or_default()
}

/// Whether a token of one of `kinds` lies completely within `[start, end]`.
fn range_has_token(list: &SyntaxNode, from: u32, to: u32, kinds: &[Kind]) -> bool {
    if from > to {
        return false;
    }
    list.descendants_with_tokens()
        .filter_map(|element| element.into_token())
        .any(|token| {
            let token_range = token.text_range();
            start(token_range) >= from && end(token_range) <= to && kinds.contains(&token.kind())
        })
}

fn is_whitespace_byte(value: u8) -> bool {
    matches!(value, b' ' | b'\t' | b'\r' | b'\n')
}
