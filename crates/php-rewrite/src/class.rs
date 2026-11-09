//! Class hierarchy, attribute, and member edits.

use swlsp_parser_php::query::{class_at, class_body, direct_child};
use swlsp_parser_php::syntax::{
    PHP_CLASS_BODY, PHP_CLASS_CONST_DECLARATION, PHP_CLASS_DECLARATION, PHP_ENUM_CASE_DECLARATION,
    PHP_ENUM_DECLARATION, PHP_EXTENDS_CLAUSE, PHP_IMPLEMENTS_CLAUSE, PHP_METHOD_DECLARATION,
    PHP_NAME, PHP_PROPERTY_DECLARATION, PHP_TRAIT_USE_DECLARATION, TK_BLOCK_COMMENT,
    TK_CLOSE_BRACE, TK_LINE_COMMENT, TK_OPEN_BRACE,
};
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::eq_ignore_case;

use crate::editor::{Editor, Result, end, invalid, line_start, range, start, whitespace_prefix};
use crate::list::direct_nodes;
use crate::phpdoc::leading_phpdoc;

impl Editor<'_> {
    /// Adds or replaces the parent of a class declaration.
    pub fn set_extends(&mut self, class: &SyntaxNode, parent: &str) -> Result<()> {
        let class = class_at(class).filter(|class| class.kind() == PHP_CLASS_DECLARATION);
        let parent = parent.trim();
        let Some(class) = class else {
            return invalid("set PHP parent: class declaration is unavailable");
        };
        if parent.is_empty() {
            return invalid("set PHP parent: parent is empty");
        }
        if let Some(clause) = direct_child(&class, PHP_EXTENDS_CLAUSE) {
            if node_has_comment(&clause) {
                return invalid("set PHP parent: extends clause contains comments");
            }
            return self.replace_range(clause.range_trimmed_trivia(), format!("extends {parent}"));
        }
        let Some(anchor) = direct_child(&class, PHP_IMPLEMENTS_CLAUSE)
            .or_else(|| direct_child(&class, PHP_CLASS_BODY))
        else {
            return invalid("set PHP parent: class body is unavailable");
        };
        self.insert(
            start(anchor.range_trimmed_trivia()),
            format!("extends {parent} "),
        )
    }

    /// Removes a class parent declaration when present.
    pub fn remove_extends(&mut self, class: &SyntaxNode) -> Result<bool> {
        let Some(class) = class_at(class).filter(|class| class.kind() == PHP_CLASS_DECLARATION)
        else {
            return invalid("remove PHP parent: class declaration is unavailable");
        };
        let Some(clause) = direct_child(&class, PHP_EXTENDS_CLAUSE) else {
            return Ok(false);
        };
        if node_has_comment(&clause) {
            return invalid("remove PHP parent: extends clause contains comments");
        }
        self.replace_range(clause.text_range(), "")?;
        Ok(true)
    }

    /// Inserts a native PHP attribute before a declaration.
    pub fn add_attribute(&mut self, owner: &SyntaxNode, attribute: &str) -> Result<()> {
        let attribute = attribute.trim();
        if attribute.is_empty() {
            return invalid("add PHP attribute: attribute is empty");
        }
        let attribute = if attribute.starts_with("#[") {
            attribute.to_owned()
        } else {
            format!("#[{}]", attribute.trim_matches(['[', ']', '#']))
        };
        let declaration_start = start(owner.range_trimmed_trivia());
        let line = line_start(self.source, declaration_start);
        let indent = whitespace_prefix(self.source, line, declaration_start).unwrap_or("");
        self.insert(declaration_start, format!("{attribute}\n{indent}"))
    }

    pub fn add_implements(&mut self, class: &SyntaxNode, interface_name: &str) -> Result<()> {
        let class = class_at(class)
            .filter(|class| matches!(class.kind(), PHP_CLASS_DECLARATION | PHP_ENUM_DECLARATION));
        let interface_name = interface_name.trim();
        let Some(class) = class else {
            return invalid("add PHP interface: class declaration is unavailable");
        };
        if interface_name.is_empty() {
            return invalid("add PHP interface: interface is empty");
        }
        if let Some(clause) = direct_child(&class, PHP_IMPLEMENTS_CLAUSE) {
            if node_has_comment(&clause) {
                return invalid("add PHP interface: implements clause contains comments");
            }
            let names = direct_nodes(Some(&clause), &[PHP_NAME]);
            let Some(last) = names.last() else {
                return invalid("add PHP interface: implements clause has no names");
            };
            if names
                .iter()
                .any(|name| same_php_name(&name.text().to_string(), interface_name))
            {
                return Ok(());
            }
            return self.insert(
                end(last.range_trimmed_trivia()),
                format!(", {interface_name}"),
            );
        }
        let Some(body) = direct_child(&class, PHP_CLASS_BODY) else {
            return invalid("add PHP interface: class body is unavailable");
        };
        self.insert(
            start(body.range_trimmed_trivia()),
            format!("implements {interface_name} "),
        )
    }

    pub fn remove_implements(&mut self, class: &SyntaxNode, interface_name: &str) -> Result<bool> {
        let interface_name = interface_name.trim();
        let Some(class) = class_at(class) else {
            return invalid("remove PHP interface: class declaration is unavailable");
        };
        let Some(clause) = direct_child(&class, PHP_IMPLEMENTS_CLAUSE) else {
            return Ok(false);
        };
        if node_has_comment(&clause) {
            return invalid("remove PHP interface: implements clause contains comments");
        }
        let names = direct_nodes(Some(&clause), &[PHP_NAME]);
        let Some(matched) = names
            .iter()
            .position(|name| same_php_name(&name.text().to_string(), interface_name))
        else {
            return Ok(false);
        };
        if names.len() == 1 {
            self.replace_range(clause.text_range(), "")?;
            return Ok(true);
        }
        let current = names[matched].range_trimmed_trivia();
        if let Some(next) = names.get(matched + 1) {
            self.replace_range(
                range(start(current), start(next.range_trimmed_trivia())),
                "",
            )?;
            return Ok(true);
        }
        let previous = names[matched - 1].range_trimmed_trivia();
        self.replace_range(range(end(previous), end(current)), "")?;
        Ok(true)
    }

    /// Inserts a declaration before the closing class brace and indents every
    /// non-empty line to match the surrounding class body.
    pub fn insert_class_member(&mut self, class: &SyntaxNode, declaration: &str) -> Result<()> {
        if class_at(class).is_none() {
            return invalid("insert PHP class member: class declaration is unavailable");
        }
        let Some(body) = class_body(class) else {
            return invalid("insert PHP class member: class body is unavailable");
        };
        let (Some(open), Some(close)) = (
            body.child_token_of_kind(TK_OPEN_BRACE),
            body.child_token_of_kind(TK_CLOSE_BRACE),
        ) else {
            return invalid("insert PHP class member: class braces are unavailable");
        };
        let declaration = dedent(declaration);
        if declaration.is_empty() {
            return invalid("insert PHP class member: declaration is empty");
        }
        let source = self.source;
        let close_start = start(close.text_range());
        let close_line_start = line_start(source, close_start);
        let close_whitespace = whitespace_prefix(source, close_line_start, close_start);
        let close_indent = close_whitespace.unwrap_or("");
        let mut member_indent = format!("{close_indent}    ");
        if let Some(member) = body
            .children()
            .find(|member| is_class_member(member.kind()))
        {
            let member_start = start(member.range_trimmed_trivia());
            if let Some(indent) =
                whitespace_prefix(source, line_start(source, member_start), member_start)
            {
                member_indent = indent.to_owned();
            }
        }
        let formatted = indent_block(&declaration, &member_indent);
        if close_whitespace.is_some() && close_line_start > end(open.text_range()) {
            return self.insert(close_line_start, format!("{formatted}\n"));
        }
        self.insert(close_start, format!("\n{formatted}\n{close_indent}"))
    }

    /// Removes a direct class member with its docblock and the blank lines
    /// that separate it from the next member.
    pub fn remove_class_member(&mut self, member: &SyntaxNode) -> Result<()> {
        let body = member
            .parent()
            .filter(|parent| is_class_member(member.kind()) && parent.kind() == PHP_CLASS_BODY);
        let Some(body) = body else {
            return invalid("remove PHP class member: direct class member is unavailable");
        };
        let source = self.source;
        let trimmed = member.range_trimmed_trivia();
        let owned_start =
            leading_phpdoc(member).map_or(start(trimmed), |doc| start(doc.text_range()));
        let mut removed_start = line_start(source, owned_start);
        if whitespace_prefix(source, removed_start, owned_start).is_none() {
            removed_start = start(member.text_range());
        }
        let mut removed_end = end(trimmed);
        let mut found = false;
        for child in body
            .children()
            .filter(|child| is_class_member(child.kind()))
        {
            if found {
                let next_start = start(child.range_trimmed_trivia());
                let candidate = line_start(source, next_start);
                if whitespace_prefix(source, candidate, next_start).is_some()
                    && candidate >= removed_end
                {
                    removed_end = candidate;
                }
                break;
            }
            if &child == member {
                found = true;
            }
        }
        if found
            && removed_end == end(trimmed)
            && let Some(close) = body.child_token_of_kind(TK_CLOSE_BRACE)
        {
            let close_start = start(close.text_range());
            let candidate = line_start(source, close_start);
            if whitespace_prefix(source, candidate, close_start).is_some()
                && candidate >= removed_end
            {
                removed_end = candidate;
            }
        }
        if removed_start > removed_end {
            return invalid("remove PHP class member: invalid member range");
        }
        self.replace_range(range(removed_start, removed_end), "")
    }
}

fn node_has_comment(node: &SyntaxNode) -> bool {
    node.descendants_with_tokens()
        .filter_map(|element| element.into_token())
        .any(|token| matches!(token.kind(), TK_LINE_COMMENT | TK_BLOCK_COMMENT))
}

fn same_php_name(left: &str, right: &str) -> bool {
    fn normalize(value: &str) -> String {
        value.replace([' ', '\t'], "").trim_matches('\\').to_owned()
    }
    eq_ignore_case(&normalize(left), &normalize(right))
}

fn is_class_member(kind: Kind) -> bool {
    matches!(
        kind,
        PHP_METHOD_DECLARATION
            | PHP_PROPERTY_DECLARATION
            | PHP_CLASS_CONST_DECLARATION
            | PHP_ENUM_CASE_DECLARATION
            | PHP_TRAIT_USE_DECLARATION
    )
}

fn dedent(source: &str) -> String {
    let mut lines: Vec<&str> = source.trim_matches(['\r', '\n']).split('\n').collect();
    while lines.first().is_some_and(|line| line.trim().is_empty()) {
        lines.remove(0);
    }
    while lines.last().is_some_and(|line| line.trim().is_empty()) {
        lines.pop();
    }
    let minimum = lines
        .iter()
        .filter(|line| !line.trim().is_empty())
        .map(|line| line.len() - line.trim_start_matches([' ', '\t']).len())
        .min()
        .unwrap_or(0);
    let lines: Vec<&str> = lines
        .into_iter()
        .map(|line| {
            if minimum > 0 && line.len() >= minimum {
                // Indentation bytes are ASCII; a shorter-indented blank line
                // would cut inside a character, so it is kept intact.
                line.get(minimum..).unwrap_or(line)
            } else {
                line
            }
        })
        .collect();
    lines.join("\n").trim().to_owned()
}

fn indent_block(source: &str, indent: &str) -> String {
    source
        .split('\n')
        .map(|line| {
            if line.trim().is_empty() {
                line.to_owned()
            } else {
                format!("{indent}{line}")
            }
        })
        .collect::<Vec<_>>()
        .join("\n")
}
