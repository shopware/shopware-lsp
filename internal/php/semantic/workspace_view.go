package semantic

import (
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

type SymbolView struct {
	workspace *workspaceSymbol
	expanded  *Symbol
}

func workspaceView(symbol *workspaceSymbol) SymbolView {
	return SymbolView{workspace: symbol}
}

func expandedView(symbol *Symbol) SymbolView {
	return SymbolView{expanded: symbol}
}

// Materialize returns the complete public Symbol value represented by the
// view. Its nested slices remain immutable snapshot-owned data.
func (view SymbolView) Materialize() Symbol {
	if view.expanded != nil {
		return *view.expanded
	}
	return view.workspace.materialize()
}

func (view SymbolView) ID() SymbolID {
	if view.expanded != nil {
		return view.expanded.ID
	}
	if view.workspace == nil {
		return ""
	}
	return view.workspace.ID
}

func (view SymbolView) Kind() SymbolKind {
	if view.expanded != nil {
		return view.expanded.Kind
	}
	if view.workspace == nil {
		return NamespaceSymbol
	}
	return view.workspace.Kind
}

func (view SymbolView) Name() string {
	if view.expanded != nil {
		return view.expanded.Name
	}
	if view.workspace == nil {
		return ""
	}
	return view.workspace.name()
}

func (view SymbolView) FullyQualified() string {
	if view.expanded != nil {
		return view.expanded.FullyQualified
	}
	if view.workspace == nil {
		return ""
	}
	return view.workspace.fullyQualified()
}

func (view SymbolView) Container() SymbolID {
	if view.expanded != nil {
		return view.expanded.Container
	}
	if view.workspace == nil {
		return ""
	}
	return view.workspace.container()
}

func (view SymbolView) Flags() Flags {
	if view.expanded != nil {
		return view.expanded.Flags
	}
	if view.workspace == nil {
		return 0
	}
	return view.workspace.flags()
}

func (view SymbolView) Visibility() Visibility {
	if view.expanded != nil {
		return view.expanded.Visibility
	}
	if view.workspace == nil {
		return Public
	}
	return view.workspace.Visibility
}

func (view SymbolView) Path() string {
	if view.expanded != nil {
		return view.expanded.Path
	}
	if view.workspace == nil {
		return ""
	}
	return view.workspace.path()
}

func (view SymbolView) Range() cst.TextRange {
	if view.expanded != nil {
		return view.expanded.Range
	}
	if view.workspace == nil {
		return cst.TextRange{}
	}
	return view.workspace.rangeValue()
}

func (view SymbolView) SelectionRange() cst.TextRange {
	if view.expanded != nil {
		return view.expanded.SelectionRange
	}
	if view.workspace == nil {
		return cst.TextRange{}
	}
	return view.workspace.ranges().SelectionRange
}

// HierarchyNames returns the immutable trait, parent-class, and implemented
// interface names retained by the snapshot.
func (view SymbolView) HierarchyNames() (
	traits,
	extends,
	implements []string,
) {
	return view.hierarchyNames()
}

// TraitAliases returns method adaptations declared by the viewed class.
func (view SymbolView) TraitAliases() []TraitAlias {
	if view.expanded != nil {
		return view.expanded.TraitAliases
	}
	if view.workspace == nil || view.workspace.hierarchy() == nil {
		return nil
	}
	return view.workspace.hierarchy().aliases()
}

func (view SymbolView) hierarchyNames() (
	traits,
	extends,
	implements []string,
) {
	if view.expanded != nil {
		return view.expanded.Traits,
			view.expanded.Extends,
			view.expanded.Implements
	}
	if view.workspace == nil || view.workspace.hierarchy() == nil {
		return nil, nil, nil
	}
	hierarchy := view.workspace.hierarchy()
	return hierarchy.traits(),
		hierarchy.extends(),
		hierarchy.implements()
}
