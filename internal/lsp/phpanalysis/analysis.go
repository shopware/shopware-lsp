// Package phpanalysis shares linked PHP semantic state between LSP features
// operating on the same immutable text document.
package phpanalysis

import (
	"fmt"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// State contains the document-local semantic graph and the workspace snapshot
// against which it was linked.
type State struct {
	Document *semantic.Document
	Snapshot *semantic.Snapshot
}

// ForDocument performs at most one semantic analysis for a PHP index, text
// document, and workspace revision. The TextDocument cache is shared by all
// diagnostic inspections participating in the same request.
func ForDocument(
	index *php.PHPIndex,
	document *lsp.TextDocument,
) (*State, error) {
	if index == nil || document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil {
		return nil, nil
	}
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, err
	}
	base := index.SemanticSnapshot()
	value := document.MemoizedAnalysis(index, base.Revision, func() any {
		semanticDocument := index.AnalyzeDocument(
			path,
			document.Version,
			document.SyntaxTree.Root,
		)
		return &State{
			Document: semanticDocument,
			Snapshot: index.SemanticSnapshot().WithDocument(semanticDocument),
		}
	})
	state, ok := value.(*State)
	if !ok || state == nil {
		return nil, fmt.Errorf("memoized PHP analysis has unexpected type %T", value)
	}
	return state, nil
}
