//! Twig/HTML document formatter over the lossless Twig CST. Its rendering
//! rules are adapted from shopware-cli's HTML formatter.
//!
//! The CST is first converted into a small private formatting IR
//! ([`converter`]) which is then rendered ([`render`]).

mod converter;
mod render;

use swlsp_syntax::Tree;

/// Indentation and Shopware template dialect options.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct Options {
    /// Indent with spaces (`tab_size` wide) instead of tabs.
    pub insert_spaces: bool,
    /// Indent width when `insert_spaces` is set; `0` means the default (4).
    pub tab_size: usize,
    /// Indent the children of `{% block %}` one level (Storefront style).
    pub twig_block_indent_children: bool,
}

/// Parses `source` with the native Twig parser and formats it. LSP callers
/// should prefer [`format_tree`] so the current editor snapshot is not parsed
/// twice.
pub fn format(source: &str, options: Options) -> String {
    format_tree(&crate::parse(source).tree, options)
}

/// Formats an immutable native Twig CST.
pub fn format_tree(tree: &Tree, options: Options) -> String {
    let config = render::IndentConfig {
        space_indent: options.insert_spaces,
        indent_size: if options.tab_size > 0 {
            options.tab_size
        } else {
            4
        },
        twig_block_indent_children: options.twig_block_indent_children,
    };
    let nodes = converter::Converter { tree }.node_list(Some(&tree.root()));
    render::Renderer::new(config).render(&nodes)
}

/// Whitespace-control modifiers on one Twig delimiter pair. `0` means no
/// modifier; `-` and `~` round-trip verbatim.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
struct TwigTrim {
    left: u8,
    right: u8,
}

/// Formatting IR derived from the CST.
#[derive(Debug, Clone, PartialEq, Eq)]
enum Node {
    /// An HTML attribute.
    Attribute {
        key: String,
        value: String,
    },
    /// A nested BODY / ROOT / attribute list that was not flattened.
    List(Vec<Node>),
    /// Verbatim source text: anything outside structured HTML/Twig syntax.
    Raw(String),
    /// An HTML `<!-- ... -->` comment (body trimmed).
    Comment(String),
    /// A `{{ ... }}` expression.
    Expression {
        expression: String,
        trim: TwigTrim,
    },
    Element(Element),
    Block(Block),
    If(If),
    /// `{% parent %}` / `{% parent() %}`.
    Parent(TwigTrim),
    /// A `{# ... #}` (or `{## ... ##}` documentation) comment.
    TwigComment {
        body: String,
        trim: TwigTrim,
        documentation: bool,
        symmetric: bool,
    },
    /// `{% verbatim %}...{% endverbatim %}` with the body preserved.
    Verbatim {
        body: String,
        open_trim: TwigTrim,
        close_trim: TwigTrim,
    },
    GenericBlock(GenericBlock),
    /// A body-less Twig tag such as `{% include 'x' %}`.
    Standalone {
        name: String,
        args: String,
        trim: TwigTrim,
    },
}

#[derive(Debug, Clone, PartialEq, Eq)]
struct Element {
    tag: String,
    attributes: Vec<Node>,
    children: Vec<Node>,
    self_closing: bool,
    /// The element opened but its closing tag lives elsewhere; `</tag>` is not
    /// emitted.
    unclosed: bool,
}

#[derive(Debug, Clone, PartialEq, Eq)]
struct Block {
    name: String,
    children: Vec<Node>,
    open_trim: TwigTrim,
    close_trim: TwigTrim,
}

/// One `if` / `elseif` branch.
#[derive(Debug, Clone, PartialEq, Eq)]
struct IfBranch {
    condition: String,
    body: Vec<Node>,
    trim: TwigTrim,
}

#[derive(Debug, Clone, PartialEq, Eq)]
struct If {
    /// `branches[0]` is the `if`, the rest are `elseif`s.
    branches: Vec<IfBranch>,
    else_children: Vec<Node>,
    else_trim: TwigTrim,
    end_trim: TwigTrim,
}

/// A block-opening Twig tag such as `{% for %}...{% endfor %}`.
#[derive(Debug, Clone, PartialEq, Eq)]
struct GenericBlock {
    name: String,
    args: String,
    end_tag: String,
    body: Vec<Node>,
    else_body: Vec<Node>,
    open_trim: TwigTrim,
    else_trim: TwigTrim,
    close_trim: TwigTrim,
}

#[cfg(test)]
mod tests;
