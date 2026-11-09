//! Formatting IR renderer.

use super::{Block, Element, GenericBlock, If, Node, TwigTrim};

/// How the formatter emits indentation.
#[derive(Debug, Clone, Copy)]
pub(super) struct IndentConfig {
    pub(super) space_indent: bool,
    pub(super) indent_size: usize,
    pub(super) twig_block_indent_children: bool,
}

pub(super) struct Renderer {
    config: IndentConfig,
    indent: String,
}

fn whitespace_control(control: u8) -> &'static str {
    match control {
        b'-' => "-",
        b'~' => "~",
        _ => "",
    }
}

fn open_stmt(out: &mut String, control: u8) {
    out.push_str("{%");
    out.push_str(whitespace_control(control));
}

fn close_stmt(out: &mut String, control: u8) {
    out.push_str(whitespace_control(control));
    out.push_str("%}");
}

/// `{%<l> <content> <r>%}`.
fn statement(out: &mut String, trim: TwigTrim, content: &[&str]) {
    open_stmt(out, trim.left);
    out.push(' ');
    for part in content {
        out.push_str(part);
    }
    out.push(' ');
    close_stmt(out, trim.right);
}

/// `{%<l> name[ args] <r>%}`.
fn statement_with_args(out: &mut String, trim: TwigTrim, name: &str, args: &str) {
    if args.is_empty() {
        statement(out, trim, &[name]);
    } else {
        statement(out, trim, &[name, " ", args]);
    }
}

fn trim_end_spaces_tabs(text: &str) -> &str {
    text.trim_end_matches([' ', '\t'])
}

fn is_comment(node: &Node) -> bool {
    matches!(node, Node::Comment(_))
}

fn is_template_element(node: &Node) -> bool {
    match node {
        Node::Element(element) => element.tag == "template",
        Node::Block(_) => true,
        _ => false,
    }
}

/// Whether a node renders with its own indent prefix (vs. an inline value).
fn is_structured_child(node: &Node) -> bool {
    matches!(
        node,
        Node::Block(_)
            | Node::If(_)
            | Node::GenericBlock(_)
            | Node::Standalone { .. }
            | Node::Verbatim { .. }
            | Node::TwigComment { .. }
            | Node::Parent(_)
            | Node::Comment(_)
    )
}

fn is_blank_raw(node: &Node) -> bool {
    matches!(node, Node::Raw(text) if text.trim().is_empty())
}

/// Whether a block body is inline-mixed: at least one raw node carries
/// non-whitespace text and every child is an inline type.
fn block_has_inline_mixed_content(children: &[Node]) -> bool {
    let mut has_meaningful_raw = false;
    for child in children {
        match child {
            Node::Raw(text) => has_meaningful_raw |= !text.trim().is_empty(),
            Node::Expression { .. } | Node::Comment(_) | Node::TwigComment { .. } => {}
            _ => return false,
        }
    }
    has_meaningful_raw
}

fn is_whitespace_preserving_tag(tag: &str) -> bool {
    matches!(tag.to_lowercase().as_str(), "pre" | "textarea")
}

fn raw_has_indented_content(text: &str) -> bool {
    text.contains('\n')
        && text.split('\n').any(|line| {
            let trimmed = line.trim_start_matches([' ', '\t']);
            !trimmed.is_empty() && line.len() > trimmed.len()
        })
}

#[derive(Default)]
struct ChildLayout {
    simple: bool,
    has_long_template_expression: bool,
    template_expressions: usize,
    multiple_short_expressions: bool,
}

impl Renderer {
    pub(super) fn new(config: IndentConfig) -> Self {
        let indent = if config.space_indent {
            " ".repeat(config.indent_size)
        } else {
            "\t".to_owned()
        };
        Self { config, indent }
    }

    pub(super) fn render(&self, nodes: &[Node]) -> String {
        self.dump_list(nodes, 0)
    }

    fn write_indent(&self, out: &mut String, indent: usize) {
        for _ in 0..indent {
            out.push_str(&self.indent);
        }
    }

    fn dump(&self, node: &Node, indent: usize) -> String {
        let mut out = String::new();
        self.dump_into(&mut out, node, indent);
        out
    }

    fn dump_into(&self, out: &mut String, node: &Node, indent: usize) {
        match node {
            Node::Attribute { key, value } => {
                self.write_indent(out, indent);
                out.push_str(key);
                if !value.is_empty() {
                    out.push_str("=\"");
                    out.push_str(&value.replace('"', "&quot;"));
                    out.push('"');
                }
            }
            Node::List(nodes) => out.push_str(&self.dump_list(nodes, indent)),
            Node::Raw(text) => out.push_str(text),
            Node::Comment(text) => {
                self.write_indent(out, indent);
                out.push_str("<!-- ");
                out.push_str(text);
                out.push_str(" -->");
            }
            Node::Expression { expression, trim } => {
                out.push_str("{{");
                out.push_str(whitespace_control(trim.left));
                out.push_str(expression);
                out.push_str(whitespace_control(trim.right));
                out.push_str("}}");
            }
            Node::Element(element) => self.dump_element(out, element, indent),
            Node::Block(block) => self.dump_block(out, block, indent),
            Node::If(node) => self.dump_if(out, node, indent),
            Node::Parent(trim) => {
                self.write_indent(out, indent);
                statement(out, *trim, &["parent()"]);
            }
            Node::TwigComment {
                body,
                trim,
                documentation,
                symmetric,
            } => {
                self.write_indent(out, indent);
                out.push_str(if *documentation { "{##" } else { "{#" });
                out.push_str(whitespace_control(trim.left));
                let trimmed = body.trim();
                if trimmed.is_empty() {
                    out.push_str(body);
                } else {
                    out.push(' ');
                    out.push_str(trimmed);
                    out.push(' ');
                }
                out.push_str(whitespace_control(trim.right));
                out.push_str(if *documentation && *symmetric {
                    "##}"
                } else {
                    "#}"
                });
            }
            Node::Verbatim {
                body,
                open_trim,
                close_trim,
            } => {
                self.write_indent(out, indent);
                statement(out, *open_trim, &["verbatim"]);
                out.push_str(body);
                statement(out, *close_trim, &["endverbatim"]);
            }
            Node::GenericBlock(block) => self.dump_generic_block(out, block, indent),
            Node::Standalone { name, args, trim } => {
                self.write_indent(out, indent);
                statement_with_args(out, *trim, name, args);
            }
        }
    }

    fn dump_list(&self, nodes: &[Node], indent: usize) -> String {
        let mut out = String::new();
        for (index, node) in nodes.iter().enumerate() {
            if is_comment(node) {
                self.dump_into(&mut out, node, indent);
                out.push('\n');
                continue;
            }
            let rendered = self.dump(node, indent);
            if index > 0 && !is_comment(&nodes[index - 1]) {
                // Skip the separator when either side already supplies a
                // newline, so format passes stay idempotent.
                if !out.ends_with('\n') && !rendered.starts_with('\n') {
                    out.push('\n');
                }
                if is_template_element(node) && is_template_element(&nodes[index - 1]) {
                    out.push('\n');
                }
            }
            out.push_str(&rendered);
        }
        if !nodes.is_empty() {
            let had_newline = out.ends_with('\n');
            out.truncate(out.trim_end_matches('\n').len());
            if had_newline {
                out.push('\n');
            }
        }
        out
    }

    /// Renders whitespace-preserving content (`<pre>`, `<textarea>`) without
    /// inserting or removing whitespace.
    fn dump_verbatim(&self, out: &mut String, nodes: &[Node]) {
        for node in nodes {
            match node {
                Node::Element(element) => {
                    out.push('<');
                    out.push_str(&element.tag);
                    for attribute in &element.attributes {
                        out.push(' ');
                        self.dump_into(out, attribute, 0);
                    }
                    if element.self_closing {
                        out.push_str("/>");
                        continue;
                    }
                    out.push('>');
                    self.dump_verbatim(out, &element.children);
                    if !element.unclosed {
                        out.push_str("</");
                        out.push_str(&element.tag);
                        out.push('>');
                    }
                }
                Node::If(node) => {
                    for (index, branch) in node.branches.iter().enumerate() {
                        let keyword = if index == 0 { "if " } else { "elseif " };
                        statement(out, branch.trim, &[keyword, &branch.condition]);
                        self.dump_verbatim(out, &branch.body);
                    }
                    if !node.else_children.is_empty() {
                        statement(out, node.else_trim, &["else"]);
                        self.dump_verbatim(out, &node.else_children);
                    }
                    statement(out, node.end_trim, &["endif"]);
                }
                Node::Block(block) => {
                    statement(out, block.open_trim, &["block ", &block.name]);
                    self.dump_verbatim(out, &block.children);
                    statement(out, block.close_trim, &["endblock"]);
                }
                Node::GenericBlock(block) => {
                    statement_with_args(out, block.open_trim, &block.name, &block.args);
                    self.dump_verbatim(out, &block.body);
                    if !block.else_body.is_empty() {
                        statement(out, block.else_trim, &["else"]);
                        self.dump_verbatim(out, &block.else_body);
                    }
                    statement(out, block.close_trim, &[&block.end_tag]);
                }
                _ => self.dump_into(out, node, 0),
            }
        }
    }

    fn dump_element(&self, out: &mut String, element: &Element, indent: usize) {
        self.write_indent(out, indent);
        out.push('<');
        out.push_str(&element.tag);
        if self.dump_attributes(out, element, indent) {
            self.write_indent(out, indent);
        }
        if element.self_closing {
            out.push_str("/>");
            return;
        }
        out.push('>');
        if !element.children.is_empty() {
            if is_whitespace_preserving_tag(&element.tag) {
                self.dump_verbatim(out, &element.children);
            } else if element.tag == "p" {
                self.dump_paragraph_children(out, &element.children, indent);
            } else {
                self.dump_children(out, &element.children, indent);
            }
        }
        if !element.unclosed {
            out.push_str("</");
            out.push_str(&element.tag);
            out.push('>');
        }
    }

    fn dump_attributes(&self, out: &mut String, element: &Element, indent: usize) -> bool {
        match element.attributes.as_slice() {
            [] => false,
            [attribute] => {
                let text = self.dump(attribute, indent + 1);
                if text.len() > 80 || matches!(attribute, Node::If(_)) {
                    out.push('\n');
                    out.push_str(&text);
                    out.push('\n');
                    return true;
                }
                out.push(' ');
                self.dump_into(out, attribute, 0);
                false
            }
            attributes => {
                for attribute in attributes {
                    out.push('\n');
                    self.dump_into(out, attribute, indent + 1);
                }
                out.push('\n');
                true
            }
        }
    }

    fn dump_paragraph_children(&self, out: &mut String, children: &[Node], indent: usize) {
        if self.has_long_template_expression(children) {
            self.dump_expanded_inline_children(out, children, indent);
            return;
        }
        for child in children {
            if is_structured_child(child) {
                out.truncate(trim_end_spaces_tabs(out).len());
            }
            let child_indent = if matches!(child, Node::Element(_)) {
                0
            } else {
                indent
            };
            self.dump_into(out, child, child_indent);
        }
    }

    fn dump_children(&self, out: &mut String, children: &[Node], indent: usize) {
        let layout = self.analyze_child_layout(children, indent);
        if !layout.simple {
            self.dump_complex_children(out, children, indent);
            return;
        }
        if layout.has_long_template_expression
            || (layout.template_expressions > 1 && !layout.multiple_short_expressions)
        {
            self.dump_expanded_inline_children(out, children, indent);
            return;
        }
        for child in children {
            self.dump_into(out, child, indent);
        }
    }

    fn analyze_child_layout(&self, children: &[Node], indent: usize) -> ChildLayout {
        let mut layout = ChildLayout {
            simple: true,
            ..ChildLayout::default()
        };
        for child in children {
            match child {
                Node::Expression { .. } => {
                    layout.template_expressions += 1;
                    layout.has_long_template_expression |= self.dump(child, 0).len() > 30;
                }
                Node::Raw(_) | Node::Comment(_) => {}
                _ => {
                    layout.simple = false;
                    return layout;
                }
            }
        }
        if let [Node::Raw(text)] = children
            && raw_has_indented_content(text)
        {
            layout.simple = false;
            return layout;
        }
        if layout.template_expressions > 1 && !layout.has_long_template_expression {
            let total: usize = children
                .iter()
                .filter(|child| matches!(child, Node::Expression { .. }))
                .map(|child| self.dump(child, indent + 1).len())
                .sum();
            layout.multiple_short_expressions = total <= 100;
        }
        layout
    }

    fn has_long_template_expression(&self, children: &[Node]) -> bool {
        children
            .iter()
            .any(|child| matches!(child, Node::Expression { .. }) && self.dump(child, 0).len() > 30)
    }

    fn dump_expanded_inline_children(&self, out: &mut String, children: &[Node], indent: usize) {
        out.push('\n');
        for child in children {
            match child {
                Node::Expression { .. } => {
                    self.write_indent(out, indent + 1);
                    self.dump_into(out, child, indent + 1);
                    out.push('\n');
                }
                Node::Raw(text) => {
                    let trimmed = text.trim();
                    if !trimmed.is_empty() {
                        self.write_indent(out, indent + 1);
                        out.push_str(trimmed);
                        out.push('\n');
                    }
                }
                _ => self.dump_into(out, child, indent + 1),
            }
        }
        self.write_indent(out, indent);
    }

    fn dump_complex_children(&self, out: &mut String, children: &[Node], indent: usize) {
        let children: Vec<&Node> = children
            .iter()
            .filter(|child| !is_blank_raw(child))
            .collect();
        for (index, child) in children.iter().enumerate() {
            out.push('\n');
            if index > 0 && is_template_element(child) && is_template_element(children[index - 1]) {
                out.push('\n');
            }
            match child {
                Node::Element(_) | Node::Block(_) => self.dump_into(out, child, indent + 1),
                Node::Raw(text) => self.dump_complex_raw(out, text, indent),
                _ => {
                    self.write_indent(out, indent + 1);
                    out.push_str(self.dump(child, indent + 1).trim());
                }
            }
        }
        out.push('\n');
        self.write_indent(out, indent);
    }

    fn dump_complex_raw(&self, out: &mut String, text: &str, indent: usize) {
        if !text.contains('\n') {
            self.write_indent(out, indent + 1);
            out.push_str(text.trim());
            return;
        }
        let lines: Vec<&str> = text
            .split('\n')
            .map(|line| line.trim_start_matches([' ', '\t']))
            .filter(|line| !line.is_empty())
            .collect();
        for (index, line) in lines.iter().enumerate() {
            self.write_indent(out, indent + 1);
            out.push_str(line);
            if index + 1 < lines.len() {
                out.push('\n');
            }
        }
    }

    /// Children rendered one per line at `indent + 1`, elements with their own
    /// indentation and everything else trimmed.
    fn dump_indented_lines(&self, out: &mut String, children: &[&Node], indent: usize) {
        for (index, child) in children.iter().enumerate() {
            if matches!(child, Node::Element(_)) {
                self.dump_into(out, child, indent + 1);
            } else {
                self.write_indent(out, indent + 1);
                out.push_str(self.dump(child, indent + 1).trim());
            }
            if index + 1 < children.len() {
                out.push('\n');
            }
        }
    }

    fn dump_block(&self, out: &mut String, block: &Block, indent: usize) {
        self.write_indent(out, indent);
        statement(out, block.open_trim, &["block ", &block.name]);

        // Inline content (text and short expressions only, e.g. JS/CSS in a
        // block): concatenate children as-is so embedded whitespace drives
        // layout.
        if block_has_inline_mixed_content(&block.children) {
            for child in &block.children {
                // Twig comments get their indent from the preceding raw node.
                let child_indent = if matches!(child, Node::TwigComment { .. }) {
                    0
                } else {
                    indent
                };
                self.dump_into(out, child, child_indent);
            }
            statement(out, block.close_trim, &["endblock"]);
            return;
        }

        // A nested block always renders non-empty, so only blank raw nodes
        // are dropped.
        let children: Vec<&Node> = block
            .children
            .iter()
            .filter(|child| !is_blank_raw(child))
            .collect();
        if !children.is_empty() {
            out.push('\n');
            let child_indent = if self.config.twig_block_indent_children {
                indent + 1
            } else {
                indent
            };
            for (index, child) in children.iter().enumerate() {
                match child {
                    Node::Element(_) => self.dump_into(out, child, child_indent),
                    Node::Expression { .. } => {
                        self.write_indent(out, child_indent);
                        self.dump_into(out, child, child_indent);
                    }
                    Node::Raw(text) => {
                        self.write_indent(out, child_indent);
                        out.push_str(text.trim());
                    }
                    _ => self.dump_into(out, child, child_indent),
                }
                if index + 1 < children.len() {
                    out.push_str(if is_comment(child) { "\n" } else { "\n\n" });
                }
            }
            out.push('\n');
            self.write_indent(out, indent);
        }
        statement(out, block.close_trim, &["endblock"]);
    }

    fn dump_if(&self, out: &mut String, node: &If, indent: usize) {
        for (index, branch) in node.branches.iter().enumerate() {
            self.write_indent(out, indent);
            let keyword = if index == 0 { "if " } else { "elseif " };
            statement(out, branch.trim, &[keyword, &branch.condition]);
            self.write_if_branch_body(out, &branch.body, indent);
        }
        if !node.else_children.is_empty() {
            self.write_indent(out, indent);
            statement(out, node.else_trim, &["else"]);
            self.write_if_branch_body(out, &node.else_children, indent);
        }
        self.write_indent(out, indent);
        statement(out, node.end_trim, &["endif"]);
    }

    fn write_if_branch_body(&self, out: &mut String, children: &[Node], indent: usize) {
        let children: Vec<&Node> = children
            .iter()
            .filter(|child| !is_blank_raw(child))
            .collect();
        if children.is_empty() {
            return;
        }
        out.push('\n');
        self.dump_indented_lines(out, &children, indent);
        out.push('\n');
    }

    fn dump_generic_block(&self, out: &mut String, block: &GenericBlock, indent: usize) {
        self.write_indent(out, indent);
        statement_with_args(out, block.open_trim, &block.name, &block.args);

        // Inline-mixed body (text + `{{ x }}` only): flow children verbatim.
        // Never taken with an `{% else %}` branch, which needs the structured
        // layout.
        if block.else_body.is_empty() && block_has_inline_mixed_content(&block.body) {
            for child in &block.body {
                let child_indent = if matches!(child, Node::TwigComment { .. }) {
                    0
                } else {
                    indent
                };
                self.dump_into(out, child, child_indent);
            }
            statement(out, block.close_trim, &[&block.end_tag]);
            return;
        }

        if !block.body.is_empty() {
            out.push('\n');
            self.dump_indented_lines(out, &block.body.iter().collect::<Vec<_>>(), indent);
            out.push('\n');
            self.write_indent(out, indent);
        }
        if !block.else_body.is_empty() {
            statement(out, block.else_trim, &["else"]);
            out.push('\n');
            self.dump_indented_lines(out, &block.else_body.iter().collect::<Vec<_>>(), indent);
            out.push('\n');
            self.write_indent(out, indent);
        }
        statement(out, block.close_trim, &[&block.end_tag]);
    }
}
