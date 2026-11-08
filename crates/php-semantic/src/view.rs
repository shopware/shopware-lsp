//! Lightweight immutable views of declarations owned by a snapshot.

use swlsp_php_types::Type;

use crate::str::Str;
use swlsp_syntax::TextRange;

use crate::packed::PackedDocument;
use crate::symbol::{Attribute, Flags, Symbol, SymbolKind, TraitAlias, Visibility};

/// A borrowed view of a declaration. Materialize it only when the complete
/// [`Symbol`] value is needed.
#[derive(Clone, Copy)]
pub struct SymbolView<'a>(ViewRepr<'a>);

#[derive(Clone, Copy)]
enum ViewRepr<'a> {
    Packed(&'a PackedDocument, u32),
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
        SymbolView(ViewRepr::Packed(document, index))
    }

    pub(crate) fn expanded(symbol: &'a Symbol) -> Self {
        SymbolView(ViewRepr::Expanded(symbol))
    }

    /// The complete public symbol. Nested collections are shared immutable
    /// snapshot data.
    pub fn materialize(&self) -> Symbol {
        match self.0 {
            ViewRepr::Packed(document, index) => document.materialize_symbol(index),
            ViewRepr::Expanded(symbol) => symbol.clone(),
        }
    }

    pub fn id(&self) -> &'a str {
        match self.0 {
            ViewRepr::Packed(document, index) => document.symbol(index).id().as_str(),
            ViewRepr::Expanded(symbol) => &symbol.id,
        }
    }

    /// The declaration ID as an owned handle. Packed declarations return
    /// a non-counted interned string, so this never allocates for them.
    /// [`SymbolView::id`] and this intern a derived packed ID on first use.
    pub fn symbol_id(&self) -> crate::SymbolId {
        match self.0 {
            ViewRepr::Packed(document, index) => {
                crate::SymbolId::from(document.symbol(index).id().name())
            }
            ViewRepr::Expanded(symbol) => symbol.id.clone(),
        }
    }

    pub fn kind(&self) -> SymbolKind {
        match self.0 {
            ViewRepr::Packed(document, index) => document.symbol(index).kind(),
            ViewRepr::Expanded(symbol) => symbol.kind,
        }
    }

    pub fn name(&self) -> &'a str {
        match self.0 {
            ViewRepr::Packed(document, index) => document.symbol(index).name.as_str(),
            ViewRepr::Expanded(symbol) => &symbol.name,
        }
    }

    pub fn fully_qualified(&self) -> &'a str {
        match self.0 {
            ViewRepr::Packed(document, index) => document.symbol(index).fully_qualified().as_str(),
            ViewRepr::Expanded(symbol) => &symbol.fully_qualified,
        }
    }

    pub fn container(&self) -> &'a str {
        match self.0 {
            ViewRepr::Packed(document, index) => document.symbol(index).container.as_str(),
            ViewRepr::Expanded(symbol) => &symbol.container,
        }
    }

    pub fn flags(&self) -> Flags {
        match self.0 {
            ViewRepr::Packed(document, index) => document.symbol(index).flags(),
            ViewRepr::Expanded(symbol) => symbol.flags,
        }
    }

    pub fn visibility(&self) -> Visibility {
        match self.0 {
            ViewRepr::Packed(document, index) => document.symbol(index).visibility(),
            ViewRepr::Expanded(symbol) => symbol.visibility,
        }
    }

    /// Attributes without materializing the full symbol. Lazily restored
    /// summaries load their details on demand.
    pub fn attributes(&self) -> Vec<Attribute> {
        match self.0 {
            ViewRepr::Packed(document, index) => document
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
            ViewRepr::Packed(document, _) => &document.path,
            ViewRepr::Expanded(symbol) => &symbol.path,
        }
    }

    pub fn range(&self) -> TextRange {
        match self.0 {
            ViewRepr::Packed(document, index) => document.range(index),
            ViewRepr::Expanded(symbol) => symbol.range,
        }
    }

    pub fn selection_range(&self) -> TextRange {
        match self.0 {
            ViewRepr::Packed(document, index) => document.ranges(index)[1],
            ViewRepr::Expanded(symbol) => symbol.selection_range,
        }
    }

    /// Trait, parent-class, and implemented-interface names. Resident
    /// summary data; never triggers a detail load.
    pub fn hierarchy_names(&self) -> (&'a [Str], &'a [Str], &'a [Str]) {
        match self.0 {
            ViewRepr::Packed(document, index) => match &document.symbol(index).hierarchy {
                Some(hierarchy) => (&hierarchy.traits, &hierarchy.extends, &hierarchy.implements),
                None => (&EMPTY_NAMES, &EMPTY_NAMES, &EMPTY_NAMES),
            },
            ViewRepr::Expanded(symbol) => (symbol.traits(), symbol.extends(), symbol.implements()),
        }
    }

    /// Typed parent-class and implemented-interface edges.
    pub fn hierarchy_types(&self) -> (&'a [Type], &'a [Type]) {
        match self.0 {
            ViewRepr::Packed(document, index) => match &document.symbol(index).hierarchy {
                Some(hierarchy) => (&hierarchy.extends_types, &hierarchy.implements_types),
                None => (&EMPTY_TYPES, &EMPTY_TYPES),
            },
            ViewRepr::Expanded(symbol) => (symbol.extends_types(), symbol.implements_types()),
        }
    }

    /// Method adaptations declared by the viewed class.
    pub fn trait_aliases(&self) -> &'a [TraitAlias] {
        match self.0 {
            ViewRepr::Packed(document, index) => document
                .symbol(index)
                .hierarchy
                .as_deref()
                .map_or(&[], |hierarchy| &hierarchy.aliases),
            ViewRepr::Expanded(symbol) => symbol.trait_aliases(),
        }
    }
}
