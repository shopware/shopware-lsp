package rewrite

import (
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

var (
	ErrStaleHandle   = errors.New("syntax element handle is stale")
	ErrMissingHandle = errors.New("syntax element handle cannot be resolved")
)

// ElementHandle is a compact, serializable reference to an immutable CST
// element. It is valid only for the recorded document version.
type ElementHandle struct {
	URI      string        `json:"uri"`
	Version  int           `json:"version"`
	Language language.ID   `json:"language"`
	Kind     cst.Kind      `json:"kind"`
	Range    cst.TextRange `json:"range"`
	// Encode the 64-bit hash as a string because diagnostic/code-action data
	// commonly passes through JavaScript clients, whose JSON numbers cannot
	// represent every uint64 without rounding.
	TextHash uint64 `json:"textHash,string"`
}

func NewElementHandle(
	uri string,
	version int,
	languageID language.ID,
	element cst.Element,
) (ElementHandle, error) {
	if element == nil {
		return ElementHandle{}, ErrMissingHandle
	}
	return ElementHandle{
		URI:      uri,
		Version:  version,
		Language: languageID,
		Kind:     element.Kind(),
		Range:    element.Range(),
		TextHash: hashText(element.Text()),
	}, nil
}

func (h ElementHandle) Resolve(
	uri string,
	version int,
	languageID language.ID,
	tree *cst.Tree,
) (cst.Element, error) {
	if h.URI != uri || h.Version != version || h.Language != languageID {
		return nil, ErrStaleHandle
	}
	if tree == nil || tree.Root == nil || h.Range.Start > h.Range.End ||
		h.Range.End > tree.Root.Range().End {
		return nil, ErrMissingHandle
	}
	candidate := tree.Root.DescendantForRange(h.Range)
	for candidate != nil {
		if candidate.Kind() == h.Kind && candidate.Range() == h.Range &&
			hashText(candidate.Text()) == h.TextHash {
			return candidate, nil
		}
		candidate = candidate.Parent()
	}
	return nil, fmt.Errorf("%w: %s kind %s", ErrStaleHandle, h.Range, h.Kind)
}

func hashText(value string) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(value))
	return hash.Sum64()
}
