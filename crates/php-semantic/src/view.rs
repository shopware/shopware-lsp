//! Lightweight immutable views of declarations owned by a snapshot.

use swlsp_php_types::Type;

use crate::str::Str;
use swlsp_syntax::TextRange;

use crate::lazy_returns::LazyReturns;
use crate::packed::PackedDocument;
use crate::symbol::{Attribute, Flags, Symbol, SymbolKind, TraitAlias, Visibility};

/// A borrowed view of a declaration. Materialize it only when the complete
/// [`Symbol`] value is needed.
#[derive(Clone, Copy)]
pub struct SymbolView<'a>(ViewRepr<'a>);

#[derive(Clone, Copy)]
enum ViewRepr<'a> {
    /// A packed declaration and, when its generation infers return types on
    /// demand, the generation's memo.
    Packed(&'a PackedDocument, u32, Option<&'a LazyReturns>),
    Expanded(&'a Symbol),
}

impl std::fmt::Debug for SymbolView<'_> {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("SymbolView")
            .field("id", &self.id())
            .field("kind", &self.kind())
            .field("path", &self.path())
            .finish()
    }
}

static EMPTY_NAMES: [Str; 0] = [];
static EMPTY_TYPES: [Type; 0] = [];

impl<'a> SymbolView<'a> {
    pub(crate) fn packed(document: &'a PackedDocument, index: u32) -> Self {
        SymbolView(ViewRepr::Packed(document, index, None))
    }

    pub(crate) fn expanded(symbol: &'a Symbol) -> Self {
        SymbolView(ViewRepr::Expanded(symbol))
    }

    /// The complete public symbol. Nested collections are shared immutable
    /// snapshot data. A return type inferred from the body is the one of
    /// the view's generation (see [`crate::ReturnInference`]), which can
    /// analyze the declaring file on first use.
    pub fn materialize(&self) -> Symbol {
        match self.0 {
            ViewRepr::Packed(document, index, returns) => {
                let mut symbol = document.materialize_symbol(index);
                if let Some(returns) = returns
                    && symbol.flags.has(Flags::INFERRED_RETURN)
                    && let Some(inferred) = returns.return_type(&symbol.path, &symbol.id)
                {
                    symbol.return_type = inferred;
                }
                symbol
            }
            ViewRepr::Expanded(symbol) => symbol.clone(),
        }
    }

    /// [`SymbolView::materialize`] with the return type stored at indexing
    /// time. Never analyzes other files; use it to list many declarations.
    pub fn materialize_stored(&self) -> Symbol {
        match self.0 {
            ViewRepr::Packed(document, index, _) => document.materialize_symbol(index),
            ViewRepr::Expanded(symbol) => symbol.clone(),
        }
    }

    /// This view materializing inferred return types through `returns`.
    pub(crate) fn with_returns(self, returns: Option<&'a LazyReturns>) -> Self {
        match self.0 {
            ViewRepr::Packed(document, index, _) => {
                SymbolView(ViewRepr::Packed(document, index, returns))
            }
            ViewRepr::Expanded(_) => self,
        }
    }

    pub fn id(&self) -> &'a str {
        match self.0 {
            ViewRepr::Packed(document, index, _) => document.symbol(index).id().as_str(),
            ViewRepr::Expanded(symbol) => &symbol.id,
        }
    }

    /// The declaration ID as an owned handle. Packed declarations return
    /// a non-counted interned string, so this never allocates for them.
    /// [`SymbolView::id`] and this intern a derived packed ID on first use.
    pub fn symbol_id(&self) -> crate::SymbolId {
        match self.0 {
            ViewRepr::Packed(document, index, _) => {
                crate::SymbolId::from(document.symbol(index).id().name())
            }
            ViewRepr::Expanded(symbol) => symbol.id.clone(),
        }
    }

    pub fn kind(&self) -> SymbolKind {
        match self.0 {
            ViewRepr::Packed(document, index, _) => document.symbol(index).kind(),
            ViewRepr::Expanded(symbol) => symbol.kind,
        }
    }

    pub fn name(&self) -> &'a str {
        match self.0 {
            ViewRepr::Packed(document, index, _) => document.symbol(index).name.as_str(),
            ViewRepr::Expanded(symbol) => &symbol.name,
        }
    }

    pub fn fully_qualified(&self) -> &'a str {
        match self.0 {
            ViewRepr::Packed(document, index, _) => {
                document.symbol(index).fully_qualified().as_str()
            }
            ViewRepr::Expanded(symbol) => &symbol.fully_qualified,
        }
    }

    pub fn container(&self) -> &'a str {
        match self.0 {
            ViewRepr::Packed(document, index, _) => document.symbol(index).container.as_str(),
            ViewRepr::Expanded(symbol) => &symbol.container,
        }
    }

    pub fn flags(&self) -> Flags {
        match self.0 {
            ViewRepr::Packed(document, index, _) => document.symbol(index).flags(),
            ViewRepr::Expanded(symbol) => symbol.flags,
        }
    }

    pub fn visibility(&self) -> Visibility {
        match self.0 {
            ViewRepr::Packed(document, index, _) => document.symbol(index).visibility(),
            ViewRepr::Expanded(symbol) => symbol.visibility,
        }
    }

    /// Attributes without materializing the full symbol. Lazily restored
    /// summaries load their details on demand.
    pub fn attributes(&self) -> Vec<Attribute> {
        match self.0 {
            ViewRepr::Packed(document, index, _) => document
                .detail(index)
                .and_then(|detail| {
                    detail
                        .metadata
                        .get()
                        .map(|metadata| metadata.attributes.clone())
                })
                .unwrap_or_default(),
            ViewRepr::Expanded(symbol) => symbol.attributes().to_vec(),
        }
    }

    pub fn path(&self) -> &'a str {
        match self.0 {
            ViewRepr::Packed(document, _, _) => &document.path,
            ViewRepr::Expanded(symbol) => &symbol.path,
        }
    }

    pub fn range(&self) -> TextRange {
        match self.0 {
            ViewRepr::Packed(document, index, _) => document.range(index),
            ViewRepr::Expanded(symbol) => symbol.range,
        }
    }

    pub fn selection_range(&self) -> TextRange {
        match self.0 {
            ViewRepr::Packed(document, index, _) => document.ranges(index)[1],
            ViewRepr::Expanded(symbol) => symbol.selection_range,
        }
    }

    /// Trait, parent-class, and implemented-interface names. Resident
    /// summary data; never triggers a detail load.
    pub fn hierarchy_names(&self) -> (&'a [Str], &'a [Str], &'a [Str]) {
        match self.0 {
            ViewRepr::Packed(document, index, _) => match &document.symbol(index).hierarchy {
                Some(hierarchy) => (&hierarchy.traits, &hierarchy.extends, &hierarchy.implements),
                None => (&EMPTY_NAMES, &EMPTY_NAMES, &EMPTY_NAMES),
            },
            ViewRepr::Expanded(symbol) => (symbol.traits(), symbol.extends(), symbol.implements()),
        }
    }

    /// Typed parent-class and implemented-interface edges.
    pub fn hierarchy_types(&self) -> (&'a [Type], &'a [Type]) {
        match self.0 {
            ViewRepr::Packed(document, index, _) => match &document.symbol(index).hierarchy {
                Some(hierarchy) => (&hierarchy.extends_types, &hierarchy.implements_types),
                None => (&EMPTY_TYPES, &EMPTY_TYPES),
            },
            ViewRepr::Expanded(symbol) => (symbol.extends_types(), symbol.implements_types()),
        }
    }

    /// Method adaptations declared by the viewed class.
    pub fn trait_aliases(&self) -> &'a [TraitAlias] {
        match self.0 {
            ViewRepr::Packed(document, index, _) => document
                .symbol(index)
                .hierarchy
                .as_deref()
                .map_or(&[], |hierarchy| &hierarchy.aliases),
            ViewRepr::Expanded(symbol) => symbol.trait_aliases(),
        }
    }
}
