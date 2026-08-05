package protocol

type LinkedEditingRangeParams = ImplementationParams

// LinkedEditingRanges identifies document ranges that an editor should update
// together while the user edits any one of them.
type LinkedEditingRanges struct {
	Ranges      []Range `json:"ranges"`
	WordPattern string  `json:"wordPattern,omitempty"`
}
