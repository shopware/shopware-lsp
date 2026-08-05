// Package rewrite builds validated, lossless source edits from immutable CST
// elements. It deliberately does not mutate syntax trees: callers resolve an
// element in one document snapshot and compile the requested change to byte
// edits for that same snapshot.
package rewrite

import (
	"errors"
	"fmt"
	"sort"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

var (
	ErrInvalidRange = errors.New("rewrite range is outside the source")
	ErrOverlap      = errors.New("rewrite edits overlap")
)

// Edit replaces Range with NewText. Range uses byte offsets into the source
// snapshot supplied to Builder.
type Edit struct {
	Range   cst.TextRange
	NewText string
	order   int
}

// Builder collects edits for one immutable source snapshot.
type Builder struct {
	sourceLength uint32
	edits        []Edit
	nextOrder    int
}

func NewBuilder(source string) *Builder {
	return &Builder{sourceLength: uint32(len(source))}
}

func (b *Builder) Replace(element cst.Element, text string) error {
	if element == nil {
		return fmt.Errorf("replace element: %w", ErrInvalidRange)
	}
	return b.ReplaceRange(element.Range(), text)
}

func (b *Builder) Delete(element cst.Element) error {
	return b.Replace(element, "")
}

func (b *Builder) InsertBefore(element cst.Element, text string) error {
	if element == nil {
		return fmt.Errorf("insert before element: %w", ErrInvalidRange)
	}
	return b.Insert(element.Range().Start, text)
}

func (b *Builder) InsertAfter(element cst.Element, text string) error {
	if element == nil {
		return fmt.Errorf("insert after element: %w", ErrInvalidRange)
	}
	return b.Insert(element.Range().End, text)
}

func (b *Builder) Insert(offset uint32, text string) error {
	return b.ReplaceRange(cst.TextRange{Start: offset, End: offset}, text)
}

func (b *Builder) ReplaceRange(rng cst.TextRange, text string) error {
	if rng.Start > rng.End || rng.End > b.sourceLength {
		return fmt.Errorf("%w: %s for source length %d", ErrInvalidRange, rng, b.sourceLength)
	}
	b.edits = append(b.edits, Edit{Range: rng, NewText: text, order: b.nextOrder})
	b.nextOrder++
	return nil
}

// Finish validates conflicts and returns edits in ascending source order.
// Insertions at the same offset are coalesced in declaration order.
func (b *Builder) Finish() ([]Edit, error) {
	edits := append([]Edit(nil), b.edits...)
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].Range.Start != edits[j].Range.Start {
			return edits[i].Range.Start < edits[j].Range.Start
		}
		if edits[i].Range.End != edits[j].Range.End {
			return edits[i].Range.End < edits[j].Range.End
		}
		return edits[i].order < edits[j].order
	})

	coalesced := make([]Edit, 0, len(edits))
	for _, edit := range edits {
		if len(coalesced) != 0 {
			previous := &coalesced[len(coalesced)-1]
			if previous.Range.Start == previous.Range.End &&
				edit.Range.Start == edit.Range.End &&
				previous.Range.Start == edit.Range.Start {
				previous.NewText += edit.NewText
				continue
			}
			if editsConflict(*previous, edit) {
				return nil, fmt.Errorf("%w: %s and %s", ErrOverlap, previous.Range, edit.Range)
			}
		}
		coalesced = append(coalesced, edit)
	}
	return coalesced, nil
}

func editsConflict(left, right Edit) bool {
	if left.Range.Start == left.Range.End {
		return left.Range.Start > right.Range.Start && left.Range.Start < right.Range.End
	}
	if right.Range.Start == right.Range.End {
		return right.Range.Start > left.Range.Start && right.Range.Start < left.Range.End
	}
	return left.Range.Start < right.Range.End && right.Range.Start < left.Range.End
}

// Apply applies validated edits without mutating the input snapshot.
func Apply(source string, edits []Edit) (string, error) {
	builder := NewBuilder(source)
	for _, edit := range edits {
		if err := builder.ReplaceRange(edit.Range, edit.NewText); err != nil {
			return "", err
		}
	}
	validated, err := builder.Finish()
	if err != nil {
		return "", err
	}
	result := source
	for index := len(validated) - 1; index >= 0; index-- {
		edit := validated[index]
		result = result[:edit.Range.Start] + edit.NewText + result[edit.Range.End:]
	}
	return result, nil
}

// DocumentPlan is the complete edit set for one source snapshot.
type DocumentPlan struct {
	URI       string
	Version   *int
	Source    string
	LineIndex *cst.LineIndex
	Edits     []Edit
}

func NewDocumentPlan(uri string, version *int, source string, edits []Edit) DocumentPlan {
	return DocumentPlan{
		URI:       uri,
		Version:   version,
		Source:    source,
		LineIndex: cst.NewLineIndex(source),
		Edits:     edits,
	}
}

func (p DocumentPlan) Apply() (string, error) {
	return Apply(p.Source, p.Edits)
}

// WorkspacePlan groups edits that the client must apply as one workspace edit.
type WorkspacePlan struct {
	Documents []DocumentPlan
	Creates   []CreateFilePlan
}

// CreateFilePlan creates a new workspace file before any document edits are
// applied. Content is optional; non-empty content is inserted as a versionless
// text-document edit immediately after creation.
type CreateFilePlan struct {
	URI     string
	Content string
}

func (p WorkspacePlan) WorkspaceEdit() (*protocol.WorkspaceEdit, error) {
	result := &protocol.WorkspaceEdit{}
	seen := make(map[string]struct{}, len(p.Creates)+len(p.Documents))
	for _, created := range p.Creates {
		if created.URI == "" {
			return nil, errors.New("rewrite create-file URI is empty")
		}
		if _, exists := seen[created.URI]; exists {
			return nil, fmt.Errorf("rewrite contains duplicate document %q", created.URI)
		}
		seen[created.URI] = struct{}{}
		result.DocumentChanges = append(result.DocumentChanges, protocol.DocumentChange{
			Kind: protocol.CreateFileOperation,
			URI:  created.URI,
			Options: &protocol.CreateFileOptions{
				Overwrite:      false,
				IgnoreIfExists: false,
			},
		})
		if created.Content != "" {
			result.DocumentChanges = append(result.DocumentChanges, protocol.DocumentChange{
				TextDocument: &protocol.OptionalVersionedTextDocumentIdentifier{
					URI: created.URI,
				},
				Edits: []protocol.TextEdit{{
					Range:   protocol.Range{},
					NewText: created.Content,
				}},
			})
		}
	}
	for _, document := range p.Documents {
		if document.URI == "" {
			return nil, errors.New("rewrite document URI is empty")
		}
		if _, exists := seen[document.URI]; exists {
			return nil, fmt.Errorf("rewrite contains duplicate document %q", document.URI)
		}
		seen[document.URI] = struct{}{}
		builder := NewBuilder(document.Source)
		for _, edit := range document.Edits {
			if err := builder.ReplaceRange(edit.Range, edit.NewText); err != nil {
				return nil, fmt.Errorf("rewrite %s: %w", document.URI, err)
			}
		}
		edits, err := builder.Finish()
		if err != nil {
			return nil, fmt.Errorf("rewrite %s: %w", document.URI, err)
		}
		lineIndex := document.LineIndex
		if lineIndex == nil {
			lineIndex = cst.NewLineIndex(document.Source)
		}
		wireEdits := make([]protocol.TextEdit, 0, len(edits))
		for _, edit := range edits {
			wireEdits = append(wireEdits, protocol.TextEdit{
				Range:   protocolRange(lineIndex, edit.Range),
				NewText: edit.NewText,
			})
		}
		result.DocumentChanges = append(result.DocumentChanges, protocol.DocumentChange{
			TextDocument: &protocol.OptionalVersionedTextDocumentIdentifier{
				URI:     document.URI,
				Version: document.Version,
			},
			Edits: wireEdits,
		})
	}
	return result, nil
}

func protocolRange(lineIndex *cst.LineIndex, rng cst.TextRange) protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Range{
		Start: protocol.Position{Line: int(startLine), Character: int(startCharacter)},
		End:   protocol.Position{Line: int(endLine), Character: int(endCharacter)},
	}
}
