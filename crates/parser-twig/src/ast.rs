//! Typed AST wrappers over the untyped syntax tree.
//!
//! Every composite node kind has a zero-cost wrapper implementing
//! [`AstNode`]: `X::cast(node)` checks the kind and `x.syntax()` is the escape
//! hatch. Some wrappers add
//! typed accessors for their children.

pub use swlsp_syntax::rowan::ast::AstNode;
use swlsp_syntax::{Kind, Lang, SyntaxNode, SyntaxToken};

use crate::syntax::*;

macro_rules! ast_nodes {
    ($($name:ident => $kind:ident,)*) => {
        $(
            #[doc = concat!("Typed wrapper around a `", stringify!($kind), "` node.")]
            #[derive(Debug, Clone, PartialEq, Eq, Hash)]
            pub struct $name(SyntaxNode);

            impl AstNode for $name {
                type Language = Lang;

                fn can_cast(kind: Kind) -> bool {
                    kind == $kind
                }

                fn cast(node: SyntaxNode) -> Option<Self> {
                    Self::can_cast(node.kind()).then(|| Self(node))
                }

                fn syntax(&self) -> &SyntaxNode {
                    &self.0
                }
            }
        )*
    };
}

ast_nodes! {
    Body => BODY,
    TwigVar => TWIG_VAR,
    TwigExpression => TWIG_EXPRESSION,
    TwigBinaryExpression => TWIG_BINARY_EXPRESSION,
    TwigUnaryExpression => TWIG_UNARY_EXPRESSION,
    TwigParenthesesExpression => TWIG_PARENTHESES_EXPRESSION,
    TwigConditionalExpression => TWIG_CONDITIONAL_EXPRESSION,
    TwigOperand => TWIG_OPERAND,
    TwigAccessor => TWIG_ACCESSOR,
    TwigFilter => TWIG_FILTER,
    TwigIndexLookup => TWIG_INDEX_LOOKUP,
    TwigIndex => TWIG_INDEX,
    TwigIndexRange => TWIG_INDEX_RANGE,
    TwigFunctionCall => TWIG_FUNCTION_CALL,
    TwigArrowFunction => TWIG_ARROW_FUNCTION,
    TwigArguments => TWIG_ARGUMENTS,
    TwigNamedArgument => TWIG_NAMED_ARGUMENT,
    TwigLiteralString => TWIG_LITERAL_STRING,
    TwigLiteralStringInner => TWIG_LITERAL_STRING_INNER,
    TwigLiteralStringInterpolation => TWIG_LITERAL_STRING_INTERPOLATION,
    TwigLiteralNumber => TWIG_LITERAL_NUMBER,
    TwigLiteralArray => TWIG_LITERAL_ARRAY,
    TwigLiteralArrayInner => TWIG_LITERAL_ARRAY_INNER,
    TwigLiteralNull => TWIG_LITERAL_NULL,
    TwigLiteralBoolean => TWIG_LITERAL_BOOLEAN,
    TwigLiteralHash => TWIG_LITERAL_HASH,
    TwigLiteralHashItems => TWIG_LITERAL_HASH_ITEMS,
    TwigLiteralHashPair => TWIG_LITERAL_HASH_PAIR,
    TwigLiteralHashKey => TWIG_LITERAL_HASH_KEY,
    TwigLiteralHashValue => TWIG_LITERAL_HASH_VALUE,
    TwigLiteralName => TWIG_LITERAL_NAME,
    TwigComment => TWIG_COMMENT,
    TwigBlock => TWIG_BLOCK,
    TwigStartingBlock => TWIG_STARTING_BLOCK,
    TwigEndingBlock => TWIG_ENDING_BLOCK,
    TwigIf => TWIG_IF,
    TwigIfBlock => TWIG_IF_BLOCK,
    TwigElseIfBlock => TWIG_ELSE_IF_BLOCK,
    TwigElseBlock => TWIG_ELSE_BLOCK,
    TwigEndIfBlock => TWIG_ENDIF_BLOCK,
    TwigSet => TWIG_SET,
    TwigSetBlock => TWIG_SET_BLOCK,
    TwigEndSetBlock => TWIG_ENDSET_BLOCK,
    TwigAssignment => TWIG_ASSIGNMENT,
    TwigFor => TWIG_FOR,
    TwigForBlock => TWIG_FOR_BLOCK,
    TwigForElseBlock => TWIG_FOR_ELSE_BLOCK,
    TwigEndForBlock => TWIG_ENDFOR_BLOCK,
    TwigExtends => TWIG_EXTENDS,
    TwigInclude => TWIG_INCLUDE,
    TwigIncludeWith => TWIG_INCLUDE_WITH,
    TwigUse => TWIG_USE,
    TwigOverride => TWIG_OVERRIDE,
    TwigApply => TWIG_APPLY,
    TwigApplyStartingBlock => TWIG_APPLY_STARTING_BLOCK,
    TwigApplyEndingBlock => TWIG_APPLY_ENDING_BLOCK,
    TwigAutoescape => TWIG_AUTOESCAPE,
    TwigAutoescapeStartingBlock => TWIG_AUTOESCAPE_STARTING_BLOCK,
    TwigAutoescapeEndingBlock => TWIG_AUTOESCAPE_ENDING_BLOCK,
    TwigDeprecated => TWIG_DEPRECATED,
    TwigDo => TWIG_DO,
    TwigEmbed => TWIG_EMBED,
    TwigEmbedStartingBlock => TWIG_EMBED_STARTING_BLOCK,
    TwigEmbedEndingBlock => TWIG_EMBED_ENDING_BLOCK,
    TwigFlush => TWIG_FLUSH,
    TwigFrom => TWIG_FROM,
    TwigImport => TWIG_IMPORT,
    TwigSandbox => TWIG_SANDBOX,
    TwigSandboxStartingBlock => TWIG_SANDBOX_STARTING_BLOCK,
    TwigSandboxEndingBlock => TWIG_SANDBOX_ENDING_BLOCK,
    TwigVerbatim => TWIG_VERBATIM,
    TwigVerbatimStartingBlock => TWIG_VERBATIM_STARTING_BLOCK,
    TwigVerbatimEndingBlock => TWIG_VERBATIM_ENDING_BLOCK,
    TwigMacro => TWIG_MACRO,
    TwigMacroStartingBlock => TWIG_MACRO_STARTING_BLOCK,
    TwigMacroEndingBlock => TWIG_MACRO_ENDING_BLOCK,
    TwigWith => TWIG_WITH,
    TwigWithStartingBlock => TWIG_WITH_STARTING_BLOCK,
    TwigWithEndingBlock => TWIG_WITH_ENDING_BLOCK,
    TwigCache => TWIG_CACHE,
    TwigCacheTtl => TWIG_CACHE_TTL,
    TwigCacheTags => TWIG_CACHE_TAGS,
    TwigCacheStartingBlock => TWIG_CACHE_STARTING_BLOCK,
    TwigCacheEndingBlock => TWIG_CACHE_ENDING_BLOCK,
    TwigProps => TWIG_PROPS,
    TwigPropDeclaration => TWIG_PROP_DECLARATION,
    TwigComponent => TWIG_COMPONENT,
    TwigComponentStartingBlock => TWIG_COMPONENT_STARTING_BLOCK,
    TwigComponentEndingBlock => TWIG_COMPONENT_ENDING_BLOCK,
    TwigTrans => TWIG_TRANS,
    TwigTransStartingBlock => TWIG_TRANS_STARTING_BLOCK,
    TwigTransEndingBlock => TWIG_TRANS_ENDING_BLOCK,
    ShopwareTwigExtends => SHOPWARE_TWIG_SW_EXTENDS,
    ShopwareTwigInclude => SHOPWARE_TWIG_SW_INCLUDE,
    ShopwareSilentFeatureCall => SHOPWARE_SILENT_FEATURE_CALL,
    ShopwareSilentFeatureCallStartingBlock => SHOPWARE_SILENT_FEATURE_CALL_STARTING_BLOCK,
    ShopwareSilentFeatureCallEndingBlock => SHOPWARE_SILENT_FEATURE_CALL_ENDING_BLOCK,
    ShopwareReturn => SHOPWARE_RETURN,
    ShopwareIcon => SHOPWARE_ICON,
    ShopwareIconStyle => SHOPWARE_ICON_STYLE,
    ShopwareThumbnails => SHOPWARE_THUMBNAILS,
    ShopwareThumbnailsWith => SHOPWARE_THUMBNAILS_WITH,
    HtmlDoctype => HTML_DOCTYPE,
    HtmlAttributeList => HTML_ATTRIBUTE_LIST,
    HtmlAttribute => HTML_ATTRIBUTE,
    HtmlString => HTML_STRING,
    HtmlStringInner => HTML_STRING_INNER,
    HtmlText => HTML_TEXT,
    HtmlRawText => HTML_RAW_TEXT,
    HtmlComment => HTML_COMMENT,
    HtmlTag => HTML_TAG,
    HtmlStartingTag => HTML_STARTING_TAG,
    HtmlEndingTag => HTML_ENDING_TAG,
    LudtwigDirectiveFileIgnore => LUDTWIG_DIRECTIVE_FILE_IGNORE,
    LudtwigDirectiveIgnore => LUDTWIG_DIRECTIVE_IGNORE,
    LudtwigDirectiveRuleList => LUDTWIG_DIRECTIVE_RULE_LIST,
    Error => ERROR,
    Root => ROOT,
    TwigFormTheme => TWIG_FORM_THEME,
    TwigAssetic => TWIG_ASSETIC,
    TwigAsseticStartingBlock => TWIG_ASSETIC_STARTING_BLOCK,
    TwigAsseticEndingBlock => TWIG_ASSETIC_ENDING_BLOCK,
    TwigTypes => TWIG_TYPES,
}

// -- helpers -----------------------------------------------------------------

/// First direct child node castable to `N`.
fn child<N: AstNode<Language = Lang>>(parent: &SyntaxNode) -> Option<N> {
    parent.children().find_map(N::cast)
}

/// The `n`th (0-based) direct child node castable to `N`.
fn nth_child<N: AstNode<Language = Lang>>(parent: &SyntaxNode, n: usize) -> Option<N> {
    parent.children().filter_map(N::cast).nth(n)
}

fn child_tokens(parent: &SyntaxNode) -> impl Iterator<Item = SyntaxToken> + use<> {
    parent
        .children_with_tokens()
        .filter_map(|element| element.into_token())
}

/// First direct child token of the given kind.
fn child_token(parent: &SyntaxNode, kind: Kind) -> Option<SyntaxToken> {
    child_tokens(parent).find(|token| token.kind() == kind)
}

/// First direct child token whose kind is one of `kinds`.
fn child_token_of(parent: &SyntaxNode, kinds: &[Kind]) -> Option<SyntaxToken> {
    child_tokens(parent).find(|token| kinds.contains(&token.kind()))
}

fn first_non_trivia_token(parent: &SyntaxNode) -> Option<SyntaxToken> {
    child_tokens(parent).find(|token| !token.kind().is_trivia())
}

fn last_non_trivia_token(parent: &SyntaxNode) -> Option<SyntaxToken> {
    child_tokens(parent)
        .filter(|token| !token.kind().is_trivia())
        .last()
}

/// First non-trivia token before the first child node (the inner string).
fn opening_quote(parent: &SyntaxNode) -> Option<SyntaxToken> {
    parent
        .children_with_tokens()
        .take_while(|element| element.as_node().is_none())
        .filter_map(|element| element.into_token())
        .find(|token| !token.kind().is_trivia())
}

/// First non-trivia token after the first child node (the inner string).
fn closing_quote(parent: &SyntaxNode) -> Option<SyntaxToken> {
    parent
        .children_with_tokens()
        .skip_while(|element| element.as_node().is_none())
        .filter_map(|element| element.into_token())
        .find(|token| !token.kind().is_trivia())
}

// -- whitespace control ------------------------------------------------------

/// A Twig whitespace-control modifier on a delimiter token.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum Trim {
    /// No modifier.
    #[default]
    None,
    /// `-`: removes all surrounding whitespace including newlines.
    All,
    /// `~`: removes surrounding whitespace except newlines.
    KeepNewlines,
}

/// The modifier on the opening delimiter of a tag-like node.
pub fn leading_trim(node: &SyntaxNode) -> Trim {
    match first_non_trivia_token(node).map(|token| token.kind()) {
        Some(TK_CURLY_PERCENT_MINUS | TK_OPEN_CURLY_CURLY_MINUS | TK_OPEN_CURLY_HASHTAG_MINUS) => {
            Trim::All
        }
        Some(TK_CURLY_PERCENT_TILDE | TK_OPEN_CURLY_CURLY_TILDE | TK_OPEN_CURLY_HASHTAG_TILDE) => {
            Trim::KeepNewlines
        }
        _ => Trim::None,
    }
}

/// The modifier on the closing delimiter of a tag-like node.
pub fn trailing_trim(node: &SyntaxNode) -> Trim {
    match last_non_trivia_token(node).map(|token| token.kind()) {
        Some(
            TK_MINUS_PERCENT_CURLY | TK_MINUS_CLOSE_CURLY_CURLY | TK_MINUS_HASHTAG_CLOSE_CURLY,
        ) => Trim::All,
        Some(
            TK_TILDE_PERCENT_CURLY | TK_TILDE_CLOSE_CURLY_CURLY | TK_TILDE_HASHTAG_CLOSE_CURLY,
        ) => Trim::KeepNewlines,
        _ => Trim::None,
    }
}

// -- accessors ---------------------------------------------------------------

impl TwigBlock {
    /// The block name token, via the starting block.
    pub fn name(&self) -> Option<SyntaxToken> {
        self.starting_block()?.name()
    }

    pub fn starting_block(&self) -> Option<TwigStartingBlock> {
        child(&self.0)
    }

    pub fn body(&self) -> Option<Body> {
        child(&self.0)
    }

    pub fn ending_block(&self) -> Option<TwigEndingBlock> {
        child(&self.0)
    }
}

impl TwigStartingBlock {
    /// The first `TK_WORD` token (the block name).
    pub fn name(&self) -> Option<SyntaxToken> {
        child_token(&self.0, TK_WORD)
    }

    pub fn twig_block(&self) -> Option<TwigBlock> {
        self.0.parent().and_then(TwigBlock::cast)
    }
}

impl TwigEndingBlock {
    pub fn twig_block(&self) -> Option<TwigBlock> {
        self.0.parent().and_then(TwigBlock::cast)
    }
}

/// Attributes held by an `HTML_ATTRIBUTE_LIST` child of a starting tag.
fn attributes_of(starting_tag: Option<SyntaxNode>) -> impl Iterator<Item = HtmlAttribute> {
    starting_tag
        .and_then(|tag| {
            tag.children()
                .find(|child| child.kind() == HTML_ATTRIBUTE_LIST)
        })
        .into_iter()
        .flat_map(|list| list.children())
        .filter_map(HtmlAttribute::cast)
}

impl HtmlTag {
    /// The tag name token, via the starting tag.
    pub fn name(&self) -> Option<SyntaxToken> {
        self.starting_tag()?.name()
    }

    /// Whether the tag has no ending tag.
    pub fn is_self_closing(&self) -> bool {
        self.ending_tag().is_none()
    }

    pub fn attributes(&self) -> impl Iterator<Item = HtmlAttribute> + use<> {
        attributes_of(
            self.0
                .children()
                .find(|child| child.kind() == HTML_STARTING_TAG),
        )
    }

    /// Whether the tag is a twig component such as `<twig:my:component />`.
    pub fn is_twig_component(&self) -> bool {
        self.starting_tag()
            .is_some_and(|tag| tag.is_twig_component())
    }

    pub fn starting_tag(&self) -> Option<HtmlStartingTag> {
        child(&self.0)
    }

    pub fn body(&self) -> Option<Body> {
        child(&self.0)
    }

    pub fn ending_tag(&self) -> Option<HtmlEndingTag> {
        child(&self.0)
    }
}

impl HtmlStartingTag {
    /// The first `TK_WORD` or `TK_TWIG_COMPONENT_NAME` child token.
    pub fn name(&self) -> Option<SyntaxToken> {
        child_token_of(&self.0, &[TK_WORD, TK_TWIG_COMPONENT_NAME])
    }

    pub fn attributes(&self) -> impl Iterator<Item = HtmlAttribute> + use<> {
        attributes_of(Some(self.0.clone()))
    }

    pub fn html_tag(&self) -> Option<HtmlTag> {
        self.0.parent().and_then(HtmlTag::cast)
    }

    pub fn is_twig_component(&self) -> bool {
        child_token(&self.0, TK_TWIG_COMPONENT_NAME).is_some()
    }
}

impl HtmlAttribute {
    /// The attribute name token (left of the equal sign).
    pub fn name(&self) -> Option<SyntaxToken> {
        child_token(&self.0, TK_WORD)
    }

    pub fn value(&self) -> Option<HtmlString> {
        child(&self.0)
    }

    /// The starting tag (the grandparent; the parent is the attribute list).
    pub fn html_tag(&self) -> Option<HtmlStartingTag> {
        self.0.parent()?.parent().and_then(HtmlStartingTag::cast)
    }
}

impl HtmlEndingTag {
    pub fn name(&self) -> Option<SyntaxToken> {
        child_token_of(&self.0, &[TK_WORD, TK_TWIG_COMPONENT_NAME])
    }

    pub fn html_tag(&self) -> Option<HtmlTag> {
        self.0.parent().and_then(HtmlTag::cast)
    }

    pub fn is_twig_component(&self) -> bool {
        child_token(&self.0, TK_TWIG_COMPONENT_NAME).is_some()
    }
}

impl TwigBinaryExpression {
    /// The first non-trivia direct child token.
    pub fn operator(&self) -> Option<SyntaxToken> {
        first_non_trivia_token(&self.0)
    }

    pub fn lhs_expression(&self) -> Option<TwigExpression> {
        nth_child(&self.0, 0)
    }

    pub fn rhs_expression(&self) -> Option<TwigExpression> {
        nth_child(&self.0, 1)
    }
}

impl LudtwigDirectiveRuleList {
    /// Texts of all `TK_WORD` child tokens.
    pub fn rule_names(&self) -> Vec<String> {
        child_tokens(&self.0)
            .filter(|token| token.kind() == TK_WORD)
            .map(|token| token.text().to_owned())
            .collect()
    }
}

impl LudtwigDirectiveFileIgnore {
    pub fn rules(&self) -> Vec<String> {
        child::<LudtwigDirectiveRuleList>(&self.0)
            .map(|list| list.rule_names())
            .unwrap_or_default()
    }
}

impl LudtwigDirectiveIgnore {
    pub fn rules(&self) -> Vec<String> {
        child::<LudtwigDirectiveRuleList>(&self.0)
            .map(|list| list.rule_names())
            .unwrap_or_default()
    }
}

impl TwigLiteralString {
    pub fn inner(&self) -> Option<TwigLiteralStringInner> {
        child(&self.0)
    }

    pub fn opening_quote(&self) -> Option<SyntaxToken> {
        opening_quote(&self.0)
    }

    pub fn closing_quote(&self) -> Option<SyntaxToken> {
        closing_quote(&self.0)
    }
}

impl TwigLiteralStringInner {
    pub fn interpolations(&self) -> impl Iterator<Item = TwigLiteralStringInterpolation> + use<> {
        self.0
            .children()
            .filter_map(TwigLiteralStringInterpolation::cast)
    }
}

impl HtmlString {
    pub fn inner(&self) -> Option<HtmlStringInner> {
        child(&self.0)
    }

    pub fn opening_quote(&self) -> Option<SyntaxToken> {
        opening_quote(&self.0)
    }

    pub fn closing_quote(&self) -> Option<SyntaxToken> {
        closing_quote(&self.0)
    }
}

impl TwigExtends {
    pub fn extends_keyword(&self) -> Option<SyntaxToken> {
        child_token(&self.0, TK_EXTENDS)
    }
}

impl TwigVar {
    pub fn expression(&self) -> Option<TwigExpression> {
        child(&self.0)
    }
}

impl TwigLiteralName {
    pub fn name(&self) -> Option<SyntaxToken> {
        child_token(&self.0, TK_WORD)
    }
}

impl TwigFilter {
    /// The operand on the left side of the pipe.
    pub fn operand(&self) -> Option<TwigOperand> {
        nth_child(&self.0, 0)
    }

    /// The operand on the right side of the pipe (a name or function call).
    pub fn filter(&self) -> Option<TwigOperand> {
        nth_child(&self.0, 1)
    }
}

impl TwigFunctionCall {
    /// The operand holding the function name.
    pub fn name_operand(&self) -> Option<TwigOperand> {
        child(&self.0)
    }

    pub fn arguments(&self) -> Option<TwigArguments> {
        child(&self.0)
    }
}

#[cfg(test)]
mod tests;
