//! CST to formatting IR conversion.

use swlsp_syntax::{Kind, SyntaxNode, Tree};

use super::{Block, Element, GenericBlock, If, IfBranch, Node, TwigTrim};
use crate::syntax::*;

pub(super) struct Converter<'t> {
    pub(super) tree: &'t Tree,
}

impl<'t> Converter<'t> {
    fn text(&self, node: &SyntaxNode) -> &'t str {
        self.tree.node_text(node)
    }

    pub(super) fn node_list(&self, parent: Option<&SyntaxNode>) -> Vec<Node> {
        let Some(parent) = parent else {
            return Vec::new();
        };
        let mut result = Vec::new();
        for child in parent.children() {
            if let Some(prefix) = self.structural_prefix(parent, &child) {
                append_raw(&mut result, prefix);
            }
            match self.node(&child) {
                None => {}
                Some(Node::Raw(text)) => append_raw(&mut result, &text),
                Some(node) => result.push(node),
            }
        }
        for node in &mut result {
            if let Node::Raw(text) = node
                && let Some(trim) = match_parent_statement(text)
            {
                *node = Node::Parent(trim);
            }
        }
        result
    }

    fn node(&self, node: &SyntaxNode) -> Option<Node> {
        if node.text_range().is_empty() {
            return None;
        }
        let raw = || Node::Raw(self.text(node).to_owned());
        Some(match node.kind() {
            BODY | ROOT | HTML_ATTRIBUTE_LIST => Node::List(self.node_list(Some(node))),
            HTML_TAG => self.html_tag(node),
            HTML_TEXT | HTML_RAW_TEXT | HTML_DOCTYPE | ERROR => raw(),
            HTML_COMMENT => {
                Node::Comment(delimited_body(self.text(node), "<!--", "-->").to_owned())
            }
            HTML_ATTRIBUTE => return html_attribute(self.text(node)),
            TWIG_VAR => {
                let text = self.text(node);
                match parse_delimited(text, "{{", "}}") {
                    Some((body, trim)) => Node::Expression {
                        expression: body.to_owned(),
                        trim,
                    },
                    None => Node::Raw(text.to_owned()),
                }
            }
            TWIG_COMMENT => {
                let text = self.text(node);
                if let Some((body, trim, symmetric)) = parse_documentation_comment(text) {
                    Node::TwigComment {
                        body: body.to_owned(),
                        trim,
                        documentation: true,
                        symmetric,
                    }
                } else if let Some((body, trim)) = parse_delimited(text, "{#", "#}") {
                    Node::TwigComment {
                        body: body.to_owned(),
                        trim,
                        documentation: false,
                        symmetric: false,
                    }
                } else {
                    Node::Raw(text.to_owned())
                }
            }
            TWIG_BLOCK => self.twig_block(node),
            TWIG_IF => self.twig_if(node),
            TWIG_FOR => self.generic_block(node, "for", "endfor"),
            TWIG_EMBED => self.generic_block(node, "embed", "endembed"),
            TWIG_MACRO => self.generic_block(node, "macro", "endmacro"),
            TWIG_APPLY => self.generic_block(node, "apply", "endapply"),
            TWIG_WITH => self.generic_block(node, "with", "endwith"),
            TWIG_AUTOESCAPE => self.generic_block(node, "autoescape", "endautoescape"),
            TWIG_SANDBOX => self.generic_block(node, "sandbox", "endsandbox"),
            TWIG_TRANS => self.generic_block(node, "trans", "endtrans"),
            TWIG_CACHE => self.generic_block(node, "cache", "endcache"),
            TWIG_COMPONENT => self.generic_block(node, "component", "endcomponent"),
            TWIG_ASSETIC => self.generic_block(node, "", ""),
            SHOPWARE_SILENT_FEATURE_CALL => {
                self.generic_block(node, "sw_silent_feature_call", "endsw_silent_feature_call")
            }
            TWIG_SET => {
                if child_node(node, BODY).is_some() {
                    self.generic_block(node, "set", "endset")
                } else {
                    self.standalone(node)
                }
            }
            TWIG_VERBATIM => self.verbatim(node),
            TWIG_EXTENDS
            | TWIG_INCLUDE
            | TWIG_USE
            | TWIG_IMPORT
            | TWIG_FROM
            | TWIG_DO
            | TWIG_DEPRECATED
            | TWIG_FLUSH
            | TWIG_PROPS
            | TWIG_FORM_THEME
            | SHOPWARE_TWIG_SW_EXTENDS
            | SHOPWARE_TWIG_SW_INCLUDE
            | SHOPWARE_RETURN
            | SHOPWARE_ICON
            | SHOPWARE_THUMBNAILS => self.standalone(node),
            _ => raw(),
        })
    }

    fn html_tag(&self, node: &SyntaxNode) -> Node {
        let raw = || Node::Raw(self.text(node).to_owned());
        let Some(starting) = child_node(node, HTML_STARTING_TAG) else {
            return raw();
        };
        let Some(name) = tag_name(&starting) else {
            return raw();
        };
        let attributes = self.node_list(child_node(&starting, HTML_ATTRIBUTE_LIST).as_ref());
        let mut body = self.node_list(child_node(node, BODY).as_ref());
        let ending =
            child_node(node, HTML_ENDING_TAG).filter(|ending| !ending.text_range().is_empty());
        let closed = ending.is_some();
        let self_closing = self.text(&starting).trim().ends_with("/>") || is_void_element(&name);
        if !self_closing && !closed {
            // Incomplete and deliberately non-HTML text such as `<<Success>>`
            // must remain byte-identical.
            return raw();
        }
        if let Some(ending) = ending {
            let ending_text = self.text(&ending);
            if let Some(delimiter) = ending_text.find("</")
                && delimiter > 0
            {
                append_raw(&mut body, &ending_text[..delimiter]);
            }
        }
        Node::Element(Element {
            tag: name,
            attributes,
            children: body,
            self_closing,
            unclosed: !self_closing && !closed,
        })
    }

    /// Leading trivia of a `{{` or (inside `<p>`) `<` child that would
    /// otherwise be dropped by the delimiter-based conversion.
    fn structural_prefix(&self, parent: &SyntaxNode, node: &SyntaxNode) -> Option<&'t str> {
        if parent.kind() == HTML_ATTRIBUTE_LIST {
            return None;
        }
        let delimiter = match node.kind() {
            TWIG_VAR => "{",
            HTML_TAG | HTML_COMMENT if self.body_belongs_to_tag(parent, "p") => "<",
            _ => return None,
        };
        let text = self.text(node);
        match text.find(delimiter) {
            Some(index) if index > 0 => Some(&text[..index]),
            _ => None,
        }
    }

    fn body_belongs_to_tag(&self, body: &SyntaxNode, name: &str) -> bool {
        if body.kind() != BODY {
            return false;
        }
        body.parent()
            .filter(|owner| owner.kind() == HTML_TAG)
            .and_then(|owner| child_node(&owner, HTML_STARTING_TAG))
            .and_then(|starting| tag_name(&starting))
            .is_some_and(|tag| tag.eq_ignore_ascii_case(name))
    }

    fn twig_block(&self, node: &SyntaxNode) -> Node {
        let raw = || Node::Raw(self.text(node).to_owned());
        let starting = child_node(node, TWIG_STARTING_BLOCK);
        let ending = child_node(node, TWIG_ENDING_BLOCK);
        let (Some(starting), Some(ending)) = (starting, ending) else {
            return raw();
        };
        if ending.text_range().is_empty() {
            return raw();
        }
        let Some((name, args, open_trim)) = parse_statement(self.text(&starting)) else {
            return raw();
        };
        if name != "block" {
            return raw();
        }
        let Some((_, _, close_trim)) = parse_statement(self.text(&ending)) else {
            return raw();
        };
        let Some(block_name) = args.split_whitespace().next() else {
            return raw();
        };
        Node::Block(Block {
            name: block_name.to_owned(),
            children: self.node_list(child_node(node, BODY).as_ref()),
            open_trim,
            close_trim,
        })
    }

    fn twig_if(&self, node: &SyntaxNode) -> Node {
        let raw = || Node::Raw(self.text(node).to_owned());
        let children: Vec<_> = node.children().collect();
        let mut branches = Vec::new();
        let mut else_children = Vec::new();
        let mut else_trim = TwigTrim::default();
        let mut end_trim = TwigTrim::default();
        for (index, child) in children.iter().enumerate() {
            match child.kind() {
                TWIG_IF_BLOCK | TWIG_ELSE_IF_BLOCK => {
                    let Some((name, condition, trim)) = parse_statement(self.text(child)) else {
                        return raw();
                    };
                    if name != "if" && name != "elseif" {
                        return raw();
                    }
                    let body = self.node_list(next_body(&children, index));
                    branches.push(IfBranch {
                        condition: condition.to_owned(),
                        body,
                        trim,
                    });
                }
                TWIG_ELSE_BLOCK => {
                    let Some((_, _, trim)) = parse_statement(self.text(child)) else {
                        return raw();
                    };
                    else_trim = trim;
                    else_children = self.node_list(next_body(&children, index));
                }
                TWIG_ENDIF_BLOCK => {
                    let Some((_, _, trim)) = parse_statement(self.text(child)) else {
                        return raw();
                    };
                    end_trim = trim;
                }
                _ => {}
            }
        }
        if branches.is_empty() {
            return raw();
        }
        Node::If(If {
            branches,
            else_children,
            else_trim,
            end_trim,
        })
    }

    fn generic_block(&self, node: &SyntaxNode, fallback_name: &str, fallback_end: &str) -> Node {
        let raw = || Node::Raw(self.text(node).to_owned());
        let mut header = None;
        let mut ending = None;
        let mut else_header = None;
        let mut bodies = Vec::new();
        for child in node.children() {
            if child.kind() == BODY {
                bodies.push(child);
                continue;
            }
            let Some((name, _, _)) = parse_statement(self.text(&child)) else {
                continue;
            };
            if name == "else" {
                else_header = Some(child);
            } else if name.starts_with("end") {
                ending = Some(child);
            } else if header.is_none() {
                header = Some(child);
            }
        }
        let (Some(header), Some(ending)) = (header, ending) else {
            return raw();
        };
        let header_text = self.text(&header);
        let Some((name, args, open_trim)) = parse_statement(header_text) else {
            return raw();
        };
        let ending_text = self.text(&ending);
        let Some((end_name, _, close_trim)) = parse_statement(ending_text) else {
            return raw();
        };
        let name = if fallback_name.is_empty() {
            name
        } else {
            fallback_name
        };
        let end_name = if fallback_end.is_empty() {
            end_name
        } else {
            fallback_end
        };
        let body = self.node_list(bodies.first());
        let else_body = self.node_list(bodies.get(1));
        let else_trim = else_header
            .and_then(|header| parse_statement(self.text(&header)).map(|(_, _, trim)| trim))
            .unwrap_or_default();
        Node::GenericBlock(GenericBlock {
            name: name.to_owned(),
            args: args.to_owned(),
            end_tag: end_name.to_owned(),
            body,
            else_body,
            open_trim,
            else_trim,
            close_trim,
        })
    }

    fn standalone(&self, node: &SyntaxNode) -> Node {
        let text = self.text(node);
        match parse_statement(text) {
            Some((name, args, trim)) => Node::Standalone {
                name: name.to_owned(),
                args: args.to_owned(),
                trim,
            },
            None => Node::Raw(text.to_owned()),
        }
    }

    fn verbatim(&self, node: &SyntaxNode) -> Node {
        let raw = || Node::Raw(self.text(node).to_owned());
        let children: Vec<_> = node.children().collect();
        if children.len() < 3 {
            return raw();
        }
        let Some((_, _, open_trim)) = parse_statement(self.text(&children[0])) else {
            return raw();
        };
        let Some((_, _, close_trim)) = parse_statement(self.text(&children[children.len() - 1]))
        else {
            return raw();
        };
        let Some(body) = child_node(node, BODY) else {
            return raw();
        };
        Node::Verbatim {
            body: self.text(&body).to_owned(),
            open_trim,
            close_trim,
        }
    }
}

/// Appends raw text, merging it into a preceding raw node.
pub(super) fn append_raw(nodes: &mut Vec<Node>, text: &str) {
    if text.is_empty() {
        return;
    }
    if let Some(Node::Raw(previous)) = nodes.last_mut() {
        previous.push_str(text);
        return;
    }
    nodes.push(Node::Raw(text.to_owned()));
}

fn html_attribute(text: &str) -> Option<Node> {
    let text = text.trim();
    if text.is_empty() {
        return None;
    }
    let Some(equal) = text.find('=') else {
        return Some(Node::Attribute {
            key: text.to_owned(),
            value: String::new(),
        });
    };
    let key = text[..equal].trim();
    let mut value = text[equal + 1..].trim();
    let bytes = value.as_bytes();
    if bytes.len() >= 2
        && ((bytes[0] == b'"' && bytes[bytes.len() - 1] == b'"')
            || (bytes[0] == b'\'' && bytes[bytes.len() - 1] == b'\''))
    {
        value = &value[1..value.len() - 1];
    }
    Some(Node::Attribute {
        key: key.to_owned(),
        value: value.to_owned(),
    })
}

/// Splits `{% name args %}` into its name, trimmed arguments and trim flags.
fn parse_statement(text: &str) -> Option<(&str, &str, TwigTrim)> {
    let (body, trim) = parse_delimited(text, "{%", "%}")?;
    let body = body.trim();
    if body.is_empty() {
        return None;
    }
    match body.find([' ', '\t', '\r', '\n']) {
        Some(end) => Some((&body[..end], body[end..].trim(), trim)),
        None => Some((body, "", trim)),
    }
}

/// The text between the first `open` and the last `close`, without trim
/// modifiers.
fn parse_delimited<'a>(text: &'a str, open: &str, close: &str) -> Option<(&'a str, TwigTrim)> {
    let start = text.find(open)?;
    let end = text.rfind(close)?;
    if end < start + open.len() {
        return None;
    }
    let bytes = text.as_bytes();
    let mut body_start = start + open.len();
    let mut body_end = end;
    let mut trim = TwigTrim::default();
    if body_start < bytes.len() && matches!(bytes[body_start], b'-' | b'~') {
        trim.left = bytes[body_start];
        body_start += 1;
    }
    if body_end > body_start && matches!(bytes[body_end - 1], b'-' | b'~') {
        trim.right = bytes[body_end - 1];
        body_end -= 1;
    }
    Some((&text[body_start..body_end], trim))
}

/// Parses a `{## ... ##}` documentation comment: body, trim flags, and whether
/// the closing delimiter is the symmetric `##}`.
fn parse_documentation_comment(text: &str) -> Option<(&str, TwigTrim, bool)> {
    let start = text.find("{#")?;
    if !text[start..].starts_with("{##") {
        return None;
    }
    let bytes = text.as_bytes();
    let mut body_start = start + "{##".len();
    let mut trim = TwigTrim::default();
    if body_start < bytes.len() && matches!(bytes[body_start], b'-' | b'~') {
        trim.left = bytes[body_start];
        body_start += 1;
    }
    const CLOSINGS: [(&str, u8, bool); 6] = [
        ("-##}", b'-', true),
        ("~##}", b'~', true),
        ("##}", 0, true),
        ("-#}", b'-', false),
        ("~#}", b'~', false),
        ("#}", 0, false),
    ];
    for (closing, control, symmetric) in CLOSINGS {
        let Some(end) = text.rfind(closing) else {
            continue;
        };
        if end < body_start {
            continue;
        }
        trim.right = control;
        return Some((&text[body_start..end], trim, symmetric));
    }
    None
}

fn delimited_body<'a>(text: &'a str, open: &str, close: &str) -> &'a str {
    let (Some(start), Some(end)) = (text.find(open), text.rfind(close)) else {
        return text;
    };
    if end < start + open.len() {
        return text;
    }
    text[start + open.len()..end].trim()
}

/// Matches `^\s*\{%([-~]?)\s*parent(?:\(\))?\s*([-~]?)%\}\s*$` (RE2 `\s`).
fn match_parent_statement(text: &str) -> Option<TwigTrim> {
    fn is_space(byte: u8) -> bool {
        matches!(byte, b'\t' | b'\n' | b'\x0c' | b'\r' | b' ')
    }
    fn skip_space(bytes: &[u8], mut index: usize) -> usize {
        while index < bytes.len() && is_space(bytes[index]) {
            index += 1;
        }
        index
    }
    fn control(bytes: &[u8], index: &mut usize) -> u8 {
        match bytes.get(*index) {
            Some(&byte @ (b'-' | b'~')) => {
                *index += 1;
                byte
            }
            _ => 0,
        }
    }
    let bytes = text.as_bytes();
    let mut index = skip_space(bytes, 0);
    if !bytes[index..].starts_with(b"{%") {
        return None;
    }
    index += 2;
    let left = control(bytes, &mut index);
    index = skip_space(bytes, index);
    if !bytes[index..].starts_with(b"parent") {
        return None;
    }
    index += "parent".len();
    if bytes[index..].starts_with(b"()") {
        index += 2;
    }
    index = skip_space(bytes, index);
    let right = control(bytes, &mut index);
    if !bytes[index..].starts_with(b"%}") {
        return None;
    }
    index += 2;
    (skip_space(bytes, index) == bytes.len()).then_some(TwigTrim { left, right })
}

fn child_node(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.children().find(|child| child.kind() == kind)
}

fn next_body(children: &[SyntaxNode], index: usize) -> Option<&SyntaxNode> {
    children.get(index + 1).filter(|child| child.kind() == BODY)
}

fn tag_name(starting: &SyntaxNode) -> Option<String> {
    let token_of = |kind| {
        starting
            .children_with_tokens()
            .filter_map(|element| element.into_token())
            .find(|token| token.kind() == kind)
            .map(|token| token.text().to_owned())
    };
    token_of(TK_WORD)
        .or_else(|| token_of(TK_TWIG_COMPONENT_NAME))
        .filter(|name| !name.is_empty())
}

fn is_void_element(name: &str) -> bool {
    matches!(
        name.to_lowercase().as_str(),
        "area"
            | "base"
            | "br"
            | "col"
            | "command"
            | "embed"
            | "hr"
            | "img"
            | "input"
            | "keygen"
            | "link"
            | "meta"
            | "param"
            | "source"
            | "track"
            | "wbr"
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parent_statement_pattern() {
        assert_eq!(
            match_parent_statement("{% parent %}"),
            Some(TwigTrim::default())
        );
        assert_eq!(
            match_parent_statement("\n  {%- parent() ~%}\n"),
            Some(TwigTrim {
                left: b'-',
                right: b'~'
            })
        );
        assert_eq!(
            match_parent_statement("{%parent%}"),
            Some(TwigTrim::default())
        );
        assert_eq!(match_parent_statement("{% parent %} x"), None);
        assert_eq!(match_parent_statement("{% parents %}"), None);
        assert_eq!(match_parent_statement("{% parent( %}"), None);
        assert_eq!(match_parent_statement(""), None);
    }

    #[test]
    fn delimited_parsing() {
        assert_eq!(
            parse_statement("{%- for a in b ~%}"),
            Some((
                "for",
                "a in b",
                TwigTrim {
                    left: b'-',
                    right: b'~'
                }
            ))
        );
        assert_eq!(parse_statement("{% %}"), None);
        assert_eq!(
            parse_delimited("{{-}}", "{{", "}}"),
            Some((
                "",
                TwigTrim {
                    left: b'-',
                    right: 0
                }
            ))
        );
        assert_eq!(parse_delimited("}}{{", "{{", "}}"), None);
        assert_eq!(
            parse_documentation_comment("{##- doc -##}"),
            Some((
                " doc ",
                TwigTrim {
                    left: b'-',
                    right: b'-'
                },
                true
            ))
        );
        assert_eq!(
            parse_documentation_comment("{## doc #}"),
            Some((" doc ", TwigTrim::default(), false))
        );
        assert_eq!(parse_documentation_comment("{# doc #}"), None);
    }
}
