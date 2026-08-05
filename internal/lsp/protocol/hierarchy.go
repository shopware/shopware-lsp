package protocol

type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// ImplementationParams identifies the declaration or reference whose concrete
// implementations are requested.
type ImplementationParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// PrepareTypeHierarchyParams identifies the class-like declaration at which a
// type hierarchy should begin.
type PrepareTypeHierarchyParams = ImplementationParams

// TypeHierarchyItem is the LSP representation of a class-like declaration.
type TypeHierarchyItem struct {
	Name           string     `json:"name"`
	Kind           SymbolKind `json:"kind"`
	Detail         string     `json:"detail,omitempty"`
	URI            string     `json:"uri"`
	Range          Range      `json:"range"`
	SelectionRange Range      `json:"selectionRange"`
	Data           any        `json:"data,omitempty"`
}

type TypeHierarchySupertypesParams struct {
	Item TypeHierarchyItem `json:"item"`
}

type TypeHierarchySubtypesParams struct {
	Item TypeHierarchyItem `json:"item"`
}
