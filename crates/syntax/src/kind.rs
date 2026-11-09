//! Global syntax-kind registry.
//!
//! Every language reserves a contiguous, non-overlapping range of `u16` kind
//! values by submitting a [`LanguageSpec`] with [`inventory::submit!`]. The
//! registry materializes flat lookup tables on first use so `Kind::name`,
//! `Kind::token_text`, `Kind::is_trivia` and `Kind::is_token` are O(1).
//!
//! Kind values and debug names are persisted in caches and CST dumps, so they
//! must stay stable.

use std::fmt;
use std::sync::OnceLock;

/// A globally unique syntax kind shared across every registered language.
#[derive(Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Default)]
#[repr(transparent)]
pub struct Kind(pub u16);

impl Kind {
    /// Sentinel for "no kind" (for example reaching EOF in a parse error).
    pub const NONE: Kind = Kind(0xFFFF);

    #[inline]
    pub const fn raw(self) -> u16 {
        self.0
    }

    /// Reports whether the kind is trivia (whitespace or line break).
    #[inline]
    pub fn is_trivia(self) -> bool {
        tables()
            .trivia
            .get(self.0 as usize)
            .copied()
            .unwrap_or(false)
    }

    /// Reports whether the kind is a token (as opposed to a composite node).
    #[inline]
    pub fn is_token(self) -> bool {
        tables()
            .token
            .get(self.0 as usize)
            .copied()
            .unwrap_or(false)
    }

    /// The SCREAMING_SNAKE debug name. `NONE` for [`Kind::NONE`] and
    /// `KIND(<n>)` for unregistered kinds.
    pub fn name(self) -> std::borrow::Cow<'static, str> {
        if let Some(name) = tables().names.get(self.0 as usize).copied().flatten() {
            return std::borrow::Cow::Borrowed(name);
        }
        if self == Kind::NONE {
            return std::borrow::Cow::Borrowed("NONE");
        }
        std::borrow::Cow::Owned(format!("KIND({})", self.0))
    }

    /// The human display text used in diagnostics. Falls back to [`Kind::name`].
    pub fn token_text(self) -> std::borrow::Cow<'static, str> {
        if let Some(text) = tables().texts.get(self.0 as usize).copied().flatten() {
            return std::borrow::Cow::Borrowed(text);
        }
        self.name()
    }
}

impl fmt::Debug for Kind {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.name())
    }
}

impl fmt::Display for Kind {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.name())
    }
}

/// The kind inventory of one language.
///
/// Kind `base + i` has debug name `names[i]` and diagnostic text `texts[i]`
/// (an empty text means "use the name"). Kinds in `[base, first_node)` are
/// tokens; kinds in `[first_node, base + names.len())` are nodes.
pub struct LanguageSpec {
    pub name: &'static str,
    pub base: u16,
    pub names: &'static [&'static str],
    pub texts: &'static [&'static str],
    pub first_node: u16,
    pub trivia: &'static [u16],
}

inventory::collect!(LanguageSpec);

struct Tables {
    names: Vec<Option<&'static str>>,
    texts: Vec<Option<&'static str>>,
    trivia: Vec<bool>,
    token: Vec<bool>,
}

static TABLES: OnceLock<Tables> = OnceLock::new();

fn tables() -> &'static Tables {
    TABLES.get_or_init(build_tables)
}

fn build_tables() -> Tables {
    let mut specs: Vec<&LanguageSpec> = inventory::iter::<LanguageSpec>.into_iter().collect();
    specs.sort_by_key(|spec| spec.base);
    let end = specs
        .iter()
        .map(|spec| spec.base as usize + spec.names.len())
        .max()
        .unwrap_or(0);
    let mut tables = Tables {
        names: vec![None; end],
        texts: vec![None; end],
        trivia: vec![false; end],
        token: vec![false; end],
    };
    let mut previous_end = 0usize;
    let mut previous_name = "";
    for spec in specs {
        validate(spec, previous_end, previous_name);
        let base = spec.base as usize;
        for (index, name) in spec.names.iter().enumerate() {
            let kind = base + index;
            if !name.is_empty() {
                tables.names[kind] = Some(name);
            }
            if let Some(text) = spec.texts.get(index).filter(|text| !text.is_empty()) {
                tables.texts[kind] = Some(text);
            }
            tables.token[kind] = (kind as u16) < spec.first_node;
        }
        for trivia in spec.trivia {
            tables.trivia[*trivia as usize] = true;
        }
        previous_end = base + spec.names.len();
        previous_name = spec.name;
    }
    tables
}

fn validate(spec: &LanguageSpec, previous_end: usize, previous_name: &str) {
    assert!(!spec.name.is_empty(), "syntax: language with empty name");
    assert!(
        !spec.names.is_empty(),
        "syntax: language {} has no kinds",
        spec.name
    );
    let base = spec.base as usize;
    let end = base + spec.names.len();
    assert!(
        end <= Kind::NONE.0 as usize,
        "syntax: language {} range reaches the reserved NONE kind",
        spec.name
    );
    assert!(
        base >= previous_end,
        "syntax: language {} range [{base}..{end}) overlaps language {previous_name}",
        spec.name
    );
    let first_node = spec.first_node as usize;
    assert!(
        (base..=end).contains(&first_node),
        "syntax: language {} first node is outside its range",
        spec.name
    );
    for trivia in spec.trivia {
        assert!(
            (base..end).contains(&(*trivia as usize)),
            "syntax: language {} trivia kind outside its range",
            spec.name
        );
    }
}

/// Declares a language's kinds as `pub const` values and registers the
/// language. Kind values are assigned sequentially from `base`, tokens first.
///
/// ```ignore
/// swlsp_syntax::language! {
///     name: "json", base: 4096,
///     trivia: [TK_WHITESPACE, TK_LINE_BREAK],
///     tokens: { TK_WHITESPACE = ("JSON_WHITESPACE", "whitespace"), ... },
///     nodes: { JSON_DOCUMENT = "JSON_DOCUMENT", ... },
/// }
/// ```
#[macro_export]
macro_rules! language {
    (
        name: $lang:literal, base: $base:expr,
        trivia: [$($trivia:ident),* $(,)?],
        tokens: { $($tok:ident = ($tok_name:literal, $tok_text:literal)),* $(,)? },
        nodes: { $($node:ident = $node_name:literal),* $(,)? } $(,)?
    ) => {
        #[allow(non_camel_case_types, dead_code, clippy::upper_case_acronyms)]
        #[repr(u16)]
        enum __KindIndex { $($tok,)* $($node,)* __Count }

        $(pub const $tok: $crate::Kind = $crate::Kind($base + __KindIndex::$tok as u16);)*
        $(pub const $node: $crate::Kind = $crate::Kind($base + __KindIndex::$node as u16);)*

        pub const KIND_BASE: u16 = $base;
        pub const KIND_COUNT: u16 = __KindIndex::__Count as u16;

        static __NAMES: &[&str] = &[$($tok_name,)* $($node_name,)*];
        static __TEXTS: &[&str] = &[$($tok_text,)* $({ let _ = $node_name; "" },)*];
        static __TRIVIA: &[u16] = &[$($trivia.0,)*];

        const __FIRST_NODE: u16 = {
            const TOKENS: &[&str] = &[$($tok_name,)*];
            $base + TOKENS.len() as u16
        };

        $crate::inventory::submit! {
            $crate::LanguageSpec {
                name: $lang,
                base: $base,
                names: __NAMES,
                texts: __TEXTS,
                first_node: __FIRST_NODE,
                trivia: __TRIVIA,
            }
        }
    };
}
