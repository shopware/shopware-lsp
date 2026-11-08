//! Calls in evaluated Vue template expressions (port of Go
//! `vue_scope_calls.go`).

use swlsp_parser_twig::ast::AstNode;

use crate::*;

/// The innermost open call containing `offset`. Commas in nested calls,
/// arrays, objects and strings do not advance the active argument.
pub fn twig_vue_call_at_offset(
    root: &SyntaxNode,
    content: &str,
    offset: u32,
) -> Option<TwigVueCall> {
    let expression_range = twig_vue_expression_range_at(root, content, offset)?;
    let limit = offset.min(u32::from(expression_range.end())) as usize;
    let mut scanner = TwigVueCallScanner {
        content: content.as_bytes(),
        expression_start: u32::from(expression_range.start()) as usize,
        limit,
        stack: Vec::with_capacity(8),
        quote: 0,
        escaped: false,
        line_comment: false,
        block_comment: false,
        templates: Vec::new(),
    };
    scanner.active_call()
}

/// Shared prelude of the offset-based expression resolvers: the evaluated
/// expression range containing `offset`.
pub(crate) fn twig_vue_expression_range_at(
    root: &SyntaxNode,
    content: &str,
    offset: u32,
) -> Option<TextRange> {
    if offset as usize > content.len() {
        return None;
    }
    let mut node_offset = offset;
    if node_offset as usize == content.len() && node_offset > 0 {
        node_offset -= 1;
    }
    let node = root.node_at(TextSize::from(node_offset))?;
    if !is_twig_vue_expression_at(&node, offset) {
        return None;
    }
    let expression_range = twig_vue_expression_range(&node)?;
    if offset < u32::from(expression_range.start()) || offset > u32::from(expression_range.end()) {
        return None;
    }
    Some(expression_range)
}

#[derive(Clone, Debug, Default)]
struct TwigVueCallFrame {
    delimiter: u8,
    call: TwigVueCall,
    callable: bool,
}

struct TwigVueCallScanner<'a> {
    content: &'a [u8],
    expression_start: usize,
    limit: usize,
    stack: Vec<TwigVueCallFrame>,
    quote: u8,
    escaped: bool,
    line_comment: bool,
    block_comment: bool,
    templates: Vec<i32>,
}

impl TwigVueCallScanner<'_> {
    fn active_call(&mut self) -> Option<TwigVueCall> {
        let mut index = self.expression_start;
        while index < self.limit {
            index = self.consume(index);
            index += 1;
        }
        self.stack
            .iter()
            .rev()
            .find(|frame| frame.callable)
            .map(|frame| frame.call.clone())
    }

    fn consume(&mut self, index: usize) -> usize {
        let current = self.content[index];
        if self.line_comment {
            if current == b'\n' {
                self.line_comment = false;
            }
            return index;
        }
        if self.block_comment {
            return self.consume_block_comment(index, current);
        }
        if self.quote != 0 {
            self.consume_quote(current);
            return index;
        }
        if self.in_template_text() {
            return self.consume_template_text(index, current);
        }
        if let Some(next) = self.consume_comment_start(index, current) {
            return next;
        }
        if current == b'\'' || current == b'"' {
            self.quote = current;
            return index;
        }
        if current == b'`' {
            self.templates.push(0);
            return index;
        }
        if current == b'{'
            && let Some(top) = self.templates.last_mut()
        {
            *top += 1;
        }
        if self.consume_delimiter(index, current) {
            return index;
        }
        if current == b'}'
            && let Some(top) = self.templates.last_mut()
        {
            *top -= 1;
        }
        index
    }

    fn consume_block_comment(&mut self, index: usize, current: u8) -> usize {
        if current == b'*' && index + 1 < self.limit && self.content[index + 1] == b'/' {
            self.block_comment = false;
            return index + 1;
        }
        index
    }

    fn consume_quote(&mut self, current: u8) {
        if self.escaped {
            self.escaped = false;
            return;
        }
        if current == b'\\' {
            self.escaped = true;
            return;
        }
        if current == self.quote {
            self.quote = 0;
        }
    }

    fn in_template_text(&self) -> bool {
        self.templates.last() == Some(&0)
    }

    fn consume_template_text(&mut self, index: usize, current: u8) -> usize {
        if current == b'\\' {
            return index + 1;
        }
        if current == b'`' {
            self.templates.pop();
            return index;
        }
        if current == b'$' && index + 1 < self.limit && self.content[index + 1] == b'{' {
            if let Some(top) = self.templates.last_mut() {
                *top = 1;
            }
            self.stack.push(TwigVueCallFrame {
                delimiter: b'{',
                ..Default::default()
            });
            return index + 1;
        }
        index
    }

    fn consume_comment_start(&mut self, index: usize, current: u8) -> Option<usize> {
        if current != b'/' || index + 1 >= self.limit {
            return None;
        }
        match self.content[index + 1] {
            b'/' => {
                self.line_comment = true;
                Some(index + 1)
            }
            b'*' => {
                self.block_comment = true;
                Some(index + 1)
            }
            _ => None,
        }
    }

    fn consume_delimiter(&mut self, index: usize, current: u8) -> bool {
        match current {
            b'(' | b'[' | b'{' => {
                let mut entry = TwigVueCallFrame {
                    delimiter: current,
                    ..Default::default()
                };
                if current == b'('
                    && let Some(call) =
                        twig_vue_call_before(self.content, self.expression_start, index)
                {
                    entry.call = call;
                    entry.callable = true;
                }
                self.stack.push(entry);
            }
            b')' | b']' | b'}' => {
                match self.stack.last() {
                    Some(top) if matching_vue_call_delimiter(top.delimiter, current) => {}
                    _ => return true,
                }
                self.stack.pop();
            }
            b',' => {
                if let Some(top) = self.stack.last_mut()
                    && top.delimiter == b'('
                    && top.callable
                {
                    top.call.active_argument += 1;
                }
            }
            _ => {}
        }
        false
    }
}

/// Complete calls in evaluated template expressions in source order.
pub fn twig_vue_calls(root: &SyntaxNode, content: &str) -> Vec<TwigVueCallSite> {
    let bytes = content.as_bytes();
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    for open in 0..bytes.len() {
        if bytes[open] != b'(' {
            continue;
        }
        let Some(node) = root.node_at(TextSize::from(open as u32)) else {
            continue;
        };
        if !is_twig_vue_expression_at(&node, open as u32) {
            continue;
        }
        let Some(expression_range) = twig_vue_expression_range(&node) else {
            continue;
        };
        if (open as u32) < u32::from(expression_range.start())
            || open as u32 >= u32::from(expression_range.end())
        {
            continue;
        }
        let Some(candidate) =
            twig_vue_call_before(bytes, u32::from(expression_range.start()) as usize, open)
        else {
            continue;
        };
        if seen.contains(&candidate.name_range) {
            continue;
        }
        let Some(active) = twig_vue_call_at_offset(root, content, open as u32 + 1) else {
            continue;
        };
        if active.name_range != candidate.name_range || active.filter != candidate.filter {
            continue;
        }
        let (arguments, end) =
            twig_vue_call_argument_ranges(bytes, open, u32::from(expression_range.end()) as usize);
        seen.insert(candidate.name_range);
        result.push(TwigVueCallSite {
            range: slot_text_range(u32::from(candidate.name_range.start()), end as u32),
            call: candidate,
            open_paren: open as u32,
            arguments,
        });
    }
    result
}

pub(crate) fn twig_vue_call_argument_ranges(
    content: &[u8],
    open: usize,
    expression_end: usize,
) -> (Vec<TextRange>, usize) {
    if open >= content.len() || content[open] != b'(' {
        return (Vec::new(), open);
    }
    let expression_end = expression_end.min(content.len());
    if expression_end <= open {
        return (Vec::new(), open + 1);
    }
    let mut stack = vec![b'('];
    let mut argument_start = open + 1;
    let mut result = Vec::new();
    let mut quote = 0u8;
    let mut escaped = false;
    let mut line_comment = false;
    let mut block_comment = false;
    let append_argument = |result: &mut Vec<TextRange>, argument_start: usize, mut end: usize| {
        let mut start = argument_start;
        while start < end && is_slot_space(content[start]) {
            start += 1;
        }
        while end > start && is_slot_space(content[end - 1]) {
            end -= 1;
        }
        if start < end {
            result.push(slot_text_range(start as u32, end as u32));
        }
    };
    let mut index = open + 1;
    while index < expression_end {
        let current = content[index];
        if line_comment {
            if current == b'\n' {
                line_comment = false;
            }
            index += 1;
            continue;
        }
        if block_comment {
            if current == b'*' && index + 1 < expression_end && content[index + 1] == b'/' {
                block_comment = false;
                index += 1;
            }
            index += 1;
            continue;
        }
        if quote != 0 {
            if escaped {
                escaped = false;
            } else if current == b'\\' {
                escaped = true;
            } else if current == quote {
                quote = 0;
            }
            index += 1;
            continue;
        }
        if current == b'/' && index + 1 < expression_end {
            match content[index + 1] {
                b'/' => {
                    line_comment = true;
                    index += 2;
                    continue;
                }
                b'*' => {
                    block_comment = true;
                    index += 2;
                    continue;
                }
                _ => {}
            }
        }
        if current == b'\'' || current == b'"' || current == b'`' {
            quote = current;
            index += 1;
            continue;
        }
        match current {
            b'(' | b'[' | b'{' => stack.push(current),
            b')' | b']' | b'}' => {
                if stack
                    .last()
                    .is_some_and(|top| matching_vue_call_delimiter(*top, current))
                {
                    if stack.len() == 1 {
                        append_argument(&mut result, argument_start, index);
                        return (result, index + 1);
                    }
                    stack.pop();
                }
            }
            b',' if stack.len() == 1 => {
                append_argument(&mut result, argument_start, index);
                argument_start = index + 1;
            }
            _ => {}
        }
        index += 1;
    }
    append_argument(&mut result, argument_start, expression_end);
    (result, expression_end)
}

pub(crate) fn twig_vue_call_before(
    content: &[u8],
    expression_start: usize,
    open: usize,
) -> Option<TwigVueCall> {
    let mut cursor = open;
    while cursor > expression_start && is_slot_space(content[cursor - 1]) {
        cursor -= 1;
    }
    if cursor >= expression_start + 2 && content[cursor - 2] == b'?' && content[cursor - 1] == b'.'
    {
        cursor -= 2;
        while cursor > expression_start && is_slot_space(content[cursor - 1]) {
            cursor -= 1;
        }
    }
    let end = cursor;
    while cursor > expression_start && is_slot_identifier_continue(content[cursor - 1]) {
        cursor -= 1;
    }
    if cursor == end || !is_slot_identifier_start(content[cursor]) {
        return None;
    }
    let mut previous = cursor;
    while previous > expression_start && is_slot_space(content[previous - 1]) {
        previous -= 1;
    }
    let mut filter = previous > expression_start && content[previous - 1] == b'|';
    if filter && previous >= expression_start + 2 && content[previous - 2] == b'|' {
        filter = false;
    }
    Some(TwigVueCall {
        name: String::from_utf8_lossy(&content[cursor..end]).into_owned(),
        name_range: slot_text_range(cursor as u32, end as u32),
        active_argument: 0,
        filter,
    })
}

pub(crate) fn matching_vue_call_delimiter(open: u8, close: u8) -> bool {
    open == b'(' && close == b')' || open == b'[' && close == b']' || open == b'{' && close == b'}'
}

/// The evaluated expression range around `node`: a Vue attribute value or a
/// Twig interpolation.
pub(crate) fn twig_vue_expression_range(node: &SyntaxNode) -> Option<TextRange> {
    if let Some(attribute_node) = twigquery::html_attribute_at(node) {
        let attribute = twigast::HtmlAttribute::cast(attribute_node)?;
        let inner = attribute.value()?.inner()?;
        return Some(inner.syntax().range_trimmed_trivia());
    }
    twigquery::closest_node_of_kind(node, &[twigsyntax::TWIG_VAR])
        .map(|variable| variable.range_trimmed_trivia())
}

/// Whether the end of `prefix` lies inside a string, comment or template
/// literal text.
pub(crate) fn vue_expression_position_in_literal_or_comment(prefix: &[u8]) -> bool {
    let mut quote = 0u8;
    let mut escaped = false;
    let mut line_comment = false;
    let mut block_comment = false;
    // One entry per template literal: zero is template text, positive values
    // are the brace depth of its current ${...} expression.
    let mut templates: Vec<i32> = Vec::new();
    let mut index = 0;
    while index < prefix.len() {
        let current = prefix[index];
        if line_comment {
            if current == b'\n' {
                line_comment = false;
            }
            index += 1;
            continue;
        }
        if block_comment {
            if current == b'*' && index + 1 < prefix.len() && prefix[index + 1] == b'/' {
                block_comment = false;
                index += 1;
            }
            index += 1;
            continue;
        }
        if quote != 0 {
            if escaped {
                escaped = false;
            } else if current == b'\\' {
                escaped = true;
            } else if current == quote {
                quote = 0;
            }
            index += 1;
            continue;
        }
        if templates.last() == Some(&0) {
            if current == b'\\' {
                index += 2;
                continue;
            }
            if current == b'`' {
                templates.pop();
                index += 1;
                continue;
            }
            if current == b'$' && index + 1 < prefix.len() && prefix[index + 1] == b'{' {
                if let Some(top) = templates.last_mut() {
                    *top = 1;
                }
                index += 1;
            }
            index += 1;
            continue;
        }
        if current == b'/' && index + 1 < prefix.len() {
            match prefix[index + 1] {
                b'/' => {
                    line_comment = true;
                    index += 2;
                    continue;
                }
                b'*' => {
                    block_comment = true;
                    index += 2;
                    continue;
                }
                _ => {}
            }
        }
        if current == b'\'' || current == b'"' {
            quote = current;
            index += 1;
            continue;
        }
        if current == b'`' {
            templates.push(0);
            index += 1;
            continue;
        }
        if let Some(top) = templates.last_mut() {
            match current {
                b'{' => *top += 1,
                b'}' => *top -= 1,
                _ => {}
            }
        }
        index += 1;
    }
    quote != 0 || line_comment || block_comment || templates.last() == Some(&0)
}
