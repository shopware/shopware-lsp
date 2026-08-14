package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type editMode struct {
	Write bool
	Diff  bool
}

type pendingDocumentEdit struct {
	URI      string
	Path     string
	Before   string
	After    string
	Mode     os.FileMode
	Exists   bool
	Created  bool
	Modified bool
	Deleted  bool
}

func applyWorkspaceEdit(
	writer io.Writer,
	workspaceEdit *protocol.WorkspaceEdit,
	mode editMode,
) error {
	if workspaceEdit == nil {
		return fmt.Errorf("command returned no workspace edit")
	}
	applier := workspaceEditApplier{
		writer:    writer,
		mode:      mode,
		documents: make(map[string]*pendingDocumentEdit),
	}
	if err := applier.applyChanges(workspaceEdit.Changes); err != nil {
		return err
	}
	if err := applier.applyDocumentChanges(workspaceEdit.DocumentChanges); err != nil {
		return err
	}
	return applier.finish()
}

type workspaceEditApplier struct {
	writer    io.Writer
	mode      editMode
	documents map[string]*pendingDocumentEdit
	order     []string
}

func (a *workspaceEditApplier) load(
	uri string,
	create bool,
) (*pendingDocumentEdit, error) {
	if document := a.documents[uri]; document != nil {
		return document, nil
	}
	path, err := uriutil.Path(uri)
	if err != nil {
		return nil, fmt.Errorf("resolve edit URI %q: %w", uri, err)
	}
	document := &pendingDocumentEdit{
		URI: uri, Path: path, Mode: 0o644, Created: create,
	}
	content, readErr := os.ReadFile(path)
	if readErr == nil {
		document.Exists = true
		document.Before = string(content)
		document.After = document.Before
		if info, statErr := os.Stat(path); statErr == nil {
			document.Mode = info.Mode().Perm()
		}
	} else if !create && !os.IsNotExist(readErr) {
		return nil, fmt.Errorf("read edit target %s: %w", path, readErr)
	} else if !create {
		return nil, fmt.Errorf("edit target does not exist: %s", path)
	}
	a.documents[uri] = document
	a.order = append(a.order, uri)
	return document, nil
}

func (a *workspaceEditApplier) applyChanges(
	changes map[string][]protocol.TextEdit,
) error {
	for uri, edits := range changes {
		document, err := a.load(uri, false)
		if err != nil {
			return err
		}
		updated, err := applyProtocolTextEdits(document.After, edits)
		if err != nil {
			return fmt.Errorf("apply edits to %s: %w", document.Path, err)
		}
		document.After = updated
		document.Modified = document.After != document.Before
	}
	return nil
}

func (a *workspaceEditApplier) applyDocumentChanges(
	changes []protocol.DocumentChange,
) error {
	for _, change := range changes {
		if change.Kind == protocol.CreateFileOperation {
			if err := a.applyCreate(change); err != nil {
				return err
			}
			continue
		}
		if change.Kind == protocol.DeleteFileOperation {
			if err := a.applyDelete(change); err != nil {
				return err
			}
			continue
		}
		if change.TextDocument == nil {
			return fmt.Errorf("unsupported workspace edit change kind %q", change.Kind)
		}
		document, err := a.load(change.TextDocument.URI, false)
		if err != nil {
			return err
		}
		updated, err := applyProtocolTextEdits(document.After, change.Edits)
		if err != nil {
			return fmt.Errorf("apply edits to %s: %w", document.Path, err)
		}
		document.After = updated
		document.Modified = document.After != document.Before
	}
	return nil
}

func (a *workspaceEditApplier) applyDelete(change protocol.DocumentChange) error {
	if change.Options != nil && change.Options.Recursive {
		return fmt.Errorf("recursive workspace deletion is not supported")
	}
	if change.Options != nil && change.Options.IgnoreIfNotExists {
		path, err := uriutil.Path(change.URI)
		if err != nil {
			return fmt.Errorf("resolve delete URI %q: %w", change.URI, err)
		}
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil
		}
	}
	document, err := a.load(change.URI, false)
	if err != nil {
		return err
	}
	document.After = ""
	document.Modified = document.Before != ""
	document.Deleted = true
	return nil
}

func (a *workspaceEditApplier) applyCreate(change protocol.DocumentChange) error {
	document, err := a.load(change.URI, true)
	if err != nil {
		return err
	}
	if document.Exists && (change.Options == nil || !change.Options.Overwrite) {
		if change.Options != nil && change.Options.IgnoreIfExists {
			return nil
		}
		return fmt.Errorf("create target already exists: %s", document.Path)
	}
	if document.Exists && change.Options != nil && change.Options.Overwrite {
		document.After = ""
		document.Modified = document.Before != ""
	}
	return nil
}

func (a *workspaceEditApplier) finish() error {
	sort.SliceStable(a.order, func(i, j int) bool {
		return a.documents[a.order[i]].Path < a.documents[a.order[j]].Path
	})
	for _, uri := range a.order {
		document := a.documents[uri]
		if !document.Deleted && !document.Modified && (!document.Created || document.Before != "") {
			continue
		}
		if err := a.finishDocument(document); err != nil {
			return err
		}
	}
	return nil
}

func (a *workspaceEditApplier) finishDocument(
	document *pendingDocumentEdit,
) error {
	if a.mode.Diff || !a.mode.Write {
		toFile := document.Path
		if document.Deleted {
			toFile = "/dev/null"
		}
		diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
			A:        difflib.SplitLines(document.Before),
			B:        difflib.SplitLines(document.After),
			FromFile: document.Path,
			ToFile:   toFile,
			Context:  3,
		})
		if err != nil {
			return fmt.Errorf("render diff for %s: %w", document.Path, err)
		}
		if err := writeFormatted(a.writer, "%s", diff); err != nil {
			return fmt.Errorf("write diff for %s: %w", document.Path, err)
		}
	}
	if a.mode.Write {
		if document.Deleted {
			if err := os.Remove(document.Path); err != nil {
				return fmt.Errorf("delete %s: %w", document.Path, err)
			}
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(document.Path), 0o755); err != nil {
			return fmt.Errorf("create edit directory: %w", err)
		}
		if err := os.WriteFile(
			document.Path,
			[]byte(document.After),
			document.Mode,
		); err != nil {
			return fmt.Errorf("write %s: %w", document.Path, err)
		}
	}
	return nil
}

type offsetEdit struct {
	start,
	end uint32
	text string
}

func applyProtocolTextEdits(source string, edits []protocol.TextEdit) (string, error) {
	lineIndex := cst.NewLineIndex(source)
	offsets := make([]offsetEdit, 0, len(edits))
	for _, edit := range edits {
		start := lineIndex.OffsetUTF16(
			uint32(edit.Range.Start.Line), uint32(edit.Range.Start.Character),
		)
		end := lineIndex.OffsetUTF16(
			uint32(edit.Range.End.Line), uint32(edit.Range.End.Character),
		)
		if start > end || end > uint32(len(source)) {
			return "", fmt.Errorf("invalid edit range %v", edit.Range)
		}
		offsets = append(offsets, offsetEdit{start: start, end: end, text: edit.NewText})
	}
	sort.SliceStable(offsets, func(i, j int) bool {
		if offsets[i].start == offsets[j].start {
			return offsets[i].end > offsets[j].end
		}
		return offsets[i].start > offsets[j].start
	})
	lastStart := uint32(len(source))
	result := source
	for _, edit := range offsets {
		if edit.end > lastStart {
			return "", fmt.Errorf("overlapping text edits")
		}
		result = result[:edit.start] + edit.text + result[edit.end:]
		lastStart = edit.start
	}
	return strings.Clone(result), nil
}
