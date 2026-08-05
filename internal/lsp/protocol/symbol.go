package protocol

type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

type SymbolKind int

const (
	SymbolFile SymbolKind = 1 + iota
	SymbolModule
	SymbolNamespace
	SymbolPackage
	SymbolClass
	SymbolMethod
	SymbolProperty
	SymbolField
	SymbolConstructor
	SymbolEnum
	SymbolInterface
	SymbolFunction
	SymbolVariable
	SymbolConstant
	SymbolString
	SymbolNumber
	SymbolBoolean
	SymbolArray
	SymbolObject
	SymbolKey
	SymbolNull
	SymbolEnumMember
	SymbolStruct
	SymbolEvent
	SymbolOperator
	SymbolTypeParameter
)

type SymbolInformation struct {
	Name          string     `json:"name"`
	Kind          SymbolKind `json:"kind"`
	Location      Location   `json:"location"`
	ContainerName string     `json:"containerName,omitempty"`
	// Priority is an internal ranking hint and is intentionally not sent over
	// LSP. Persisted catalogs use it to break equally relevant text matches.
	Priority int `json:"-"`
}
