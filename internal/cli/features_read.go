package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/shopware/shopware-lsp/internal/app"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func (r *Runner) positionRequest(
	ctx context.Context,
	args []string,
	method string,
	result interface{},
) error {
	session, document, position, err := r.openPositionSession(
		ctx, args, method+" expects one file position",
	)
	if err != nil {
		return err
	}
	defer closeIgnoringError(session)
	return session.call(
		ctx, method, positionParams(document.URI, position), result,
	)
}

func (r *Runner) openPositionSession(
	ctx context.Context,
	args []string,
	usage string,
) (*cliSession, *cliDocument, protocol.Position, error) {
	value, err := requireOneArgument(args, usage)
	if err != nil {
		return nil, nil, protocol.Position{}, err
	}
	target, err := parsePositionTarget(value)
	if err != nil {
		return nil, nil, protocol.Position{}, err
	}
	session, err := r.connect(ctx)
	if err != nil {
		return nil, nil, protocol.Position{}, err
	}
	document, err := session.openDocument(ctx, target.Path)
	if err != nil {
		_ = session.Close()
		return nil, nil, protocol.Position{}, err
	}
	return session, document, target.Position, nil
}

func (r *Runner) documentRequest(
	ctx context.Context,
	args []string,
	method string,
	result interface{},
) error {
	path, err := requireOneArgument(args, method+" expects one file")
	if err != nil {
		return err
	}
	session, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer closeIgnoringError(session)
	document, err := session.openDocument(ctx, path)
	if err != nil {
		return err
	}
	return session.call(ctx, method, textDocumentParams(document.URI), result)
}

func (r *Runner) runCompletion(ctx context.Context, args []string) error {
	session, document, position, err := r.openPositionSession(
		ctx, args, "completion expects one file position",
	)
	if err != nil {
		return err
	}
	defer closeIgnoringError(session)
	params := positionParams(document.URI, position)
	params["context"] = map[string]int{"triggerKind": int(protocol.Invoked)}
	var result protocol.CompletionList
	if err := session.call(ctx, "textDocument/completion", params, &result); err != nil {
		return err
	}
	return writeJSON(r.out, result)
}

func (r *Runner) runDefinition(ctx context.Context, args []string) error {
	var result []protocol.Location
	if err := r.positionRequest(ctx, args, "textDocument/definition", &result); err != nil {
		return err
	}
	return writeJSON(r.out, result)
}

func (r *Runner) runImplementation(ctx context.Context, args []string) error {
	var result []protocol.Location
	if err := r.positionRequest(ctx, args, "textDocument/implementation", &result); err != nil {
		return err
	}
	return writeJSON(r.out, result)
}

func (r *Runner) runReferences(ctx context.Context, args []string) error {
	value, err := requireOneArgument(args, "references expects one file position")
	if err != nil {
		return err
	}
	target, err := parsePositionTarget(value)
	if err != nil {
		return err
	}
	session, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer closeIgnoringError(session)
	document, err := session.openDocument(ctx, target.Path)
	if err != nil {
		return err
	}
	params := positionParams(document.URI, target.Position)
	params["context"] = map[string]bool{"includeDeclaration": true}
	var result []protocol.Location
	if err := session.call(ctx, "textDocument/references", params, &result); err != nil {
		return err
	}
	return writeJSON(r.out, result)
}

func (r *Runner) runHover(ctx context.Context, args []string) error {
	var result *protocol.Hover
	if err := r.positionRequest(ctx, args, "textDocument/hover", &result); err != nil {
		return err
	}
	if r.json || result == nil {
		return writeJSON(r.out, result)
	}
	return writeFormatted(r.out, "%s\n", result.Contents.Value)
}

func (r *Runner) runSignature(ctx context.Context, args []string) error {
	var result *protocol.SignatureHelp
	if err := r.positionRequest(ctx, args, "textDocument/signatureHelp", &result); err != nil {
		return err
	}
	return writeJSON(r.out, result)
}

func (r *Runner) runCallHierarchy(ctx context.Context, args []string) error {
	session, document, position, err := r.openPositionSession(
		ctx, args, "call-hierarchy expects one file position",
	)
	if err != nil {
		return err
	}
	defer closeIgnoringError(session)
	var roots []protocol.CallHierarchyItem
	if err := session.call(
		ctx, "textDocument/prepareCallHierarchy",
		positionParams(document.URI, position), &roots,
	); err != nil {
		return err
	}
	type calls struct {
		Item     protocol.CallHierarchyItem           `json:"item"`
		Incoming []protocol.CallHierarchyIncomingCall `json:"incoming"`
		Outgoing []protocol.CallHierarchyOutgoingCall `json:"outgoing"`
	}
	result := make([]calls, 0, len(roots))
	if len(roots) == 0 {
		return writeJSON(r.out, result)
	}
	for _, root := range roots {
		entry := calls{Item: root}
		params := map[string]interface{}{"item": root}
		if err := session.call(ctx, "callHierarchy/incomingCalls", params, &entry.Incoming); err != nil {
			return err
		}
		if err := session.call(ctx, "callHierarchy/outgoingCalls", params, &entry.Outgoing); err != nil {
			return err
		}
		result = append(result, entry)
	}
	return writeJSON(r.out, result)
}

func (r *Runner) runTypeHierarchy(ctx context.Context, args []string) error {
	session, document, position, err := r.openPositionSession(
		ctx, args, "type-hierarchy expects one file position",
	)
	if err != nil {
		return err
	}
	defer closeIgnoringError(session)
	var roots []protocol.TypeHierarchyItem
	if err := session.call(
		ctx, "textDocument/prepareTypeHierarchy",
		positionParams(document.URI, position), &roots,
	); err != nil {
		return err
	}
	type hierarchy struct {
		Item       protocol.TypeHierarchyItem   `json:"item"`
		Supertypes []protocol.TypeHierarchyItem `json:"supertypes"`
		Subtypes   []protocol.TypeHierarchyItem `json:"subtypes"`
	}
	result := make([]hierarchy, 0, len(roots))
	if len(roots) == 0 {
		return writeJSON(r.out, result)
	}
	for _, root := range roots {
		entry := hierarchy{Item: root}
		params := map[string]interface{}{"item": root}
		if err := session.call(ctx, "typeHierarchy/supertypes", params, &entry.Supertypes); err != nil {
			return err
		}
		if err := session.call(ctx, "typeHierarchy/subtypes", params, &entry.Subtypes); err != nil {
			return err
		}
		result = append(result, entry)
	}
	return writeJSON(r.out, result)
}

func (r *Runner) runSymbols(ctx context.Context, args []string) error {
	var result []protocol.DocumentSymbol
	if err := r.documentRequest(ctx, args, "textDocument/documentSymbol", &result); err != nil {
		return err
	}
	return writeJSON(r.out, result)
}

func (r *Runner) runWorkspaceSymbols(ctx context.Context, args []string) error {
	fresh := false
	if len(args) > 0 && args[0] == "--fresh" {
		fresh = true
		args = args[1:]
	}
	if len(args) != 1 {
		return usageError("workspace-symbol expects [--fresh] <query>")
	}
	if !fresh {
		result, available, err := r.cachedWorkspaceSymbols(ctx, args[0])
		if err != nil {
			return err
		}
		if available {
			return writeJSON(r.out, result)
		}
	}
	session, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer closeIgnoringError(session)
	var result []protocol.SymbolInformation
	if err := session.call(
		ctx, "workspace/symbol", map[string]string{"query": args[0]}, &result,
	); err != nil {
		return err
	}
	return writeJSON(r.out, result)
}

func (r *Runner) cachedWorkspaceSymbols(
	ctx context.Context,
	query string,
) ([]protocol.SymbolInformation, bool, error) {
	root, err := r.workspaceRoot()
	if err != nil {
		return nil, false, err
	}
	if err := r.requireSupportedProject(root); err != nil {
		return nil, false, err
	}
	cacheDir, err := app.ProjectCacheFolder(root)
	if err != nil {
		return nil, false, err
	}
	current, err := indexer.CacheVersionCurrent(cacheDir)
	if err != nil {
		return nil, false, fmt.Errorf("check workspace symbol cache version: %w", err)
	}
	if !current {
		return nil, false, nil
	}
	catalog, err := indexer.OpenWorkspaceSymbolCatalog(
		filepath.Join(cacheDir, "indexes.db"),
	)
	if err != nil {
		// An absent catalog is a normal cache-upgrade path. Connecting the
		// production session below will initialize and populate it.
		return nil, false, nil
	}
	defer func() { _ = catalog.Close() }()
	ready, err := catalog.Ready(ctx)
	if err != nil {
		return nil, false, err
	}
	if !ready {
		return nil, false, nil
	}
	symbols, err := catalog.Query(ctx, query, 500)
	if err != nil {
		return nil, false, err
	}
	if len(symbols) == 0 {
		// Framework-only domains still use the production providers until all
		// of their indexers implement WorkspaceSymbolContributor.
		return nil, false, nil
	}
	result := make([]protocol.SymbolInformation, 0, len(symbols))
	for _, symbol := range symbols {
		result = append(result, protocol.SymbolInformation{
			Name:          symbol.Name,
			Kind:          protocol.SymbolKind(symbol.Kind),
			ContainerName: symbol.ContainerName,
			Location: protocol.Location{
				URI: uriutil.FileURI(symbol.Path),
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      symbol.Range.Start.Line,
						Character: symbol.Range.Start.Character,
					},
					End: protocol.Position{
						Line:      symbol.Range.End.Line,
						Character: symbol.Range.End.Character,
					},
				},
			},
		})
	}
	return result, true, nil
}

func (r *Runner) runHighlights(ctx context.Context, args []string) error {
	var result []protocol.DocumentHighlight
	if err := r.positionRequest(ctx, args, "textDocument/documentHighlight", &result); err != nil {
		return err
	}
	return writeJSON(r.out, result)
}

func (r *Runner) runFoldingRanges(ctx context.Context, args []string) error {
	var result []protocol.FoldingRange
	if err := r.documentRequest(ctx, args, "textDocument/foldingRange", &result); err != nil {
		return err
	}
	return writeJSON(r.out, result)
}

func (r *Runner) runLinks(ctx context.Context, args []string) error {
	var result []protocol.DocumentLink
	if err := r.documentRequest(ctx, args, "textDocument/documentLink", &result); err != nil {
		return err
	}
	return writeJSON(r.out, result)
}

type decodedSemanticToken struct {
	Line      uint32 `json:"line"`
	Character uint32 `json:"character"`
	Length    uint32 `json:"length"`
	Type      string `json:"type"`
	Modifiers uint32 `json:"modifiers,omitempty"`
}

func (r *Runner) runSemanticTokens(ctx context.Context, args []string) error {
	var encoded protocol.SemanticTokens
	if err := r.documentRequest(ctx, args, "textDocument/semanticTokens/full", &encoded); err != nil {
		return err
	}
	tokens := make([]decodedSemanticToken, 0, len(encoded.Data)/5)
	var line, character uint32
	for index := 0; index+4 < len(encoded.Data); index += 5 {
		lineDelta, characterDelta := encoded.Data[index], encoded.Data[index+1]
		line += lineDelta
		if lineDelta == 0 {
			character += characterDelta
		} else {
			character = characterDelta
		}
		typeIndex := encoded.Data[index+3]
		typeName := fmt.Sprintf("unknown(%d)", typeIndex)
		if int(typeIndex) < len(protocol.SemanticTokenTypes) {
			typeName = protocol.SemanticTokenTypes[typeIndex]
		}
		tokens = append(tokens, decodedSemanticToken{
			Line: line, Character: character, Length: encoded.Data[index+2],
			Type: typeName, Modifiers: encoded.Data[index+4],
		})
	}
	return writeJSON(r.out, tokens)
}

func (r *Runner) runInlayHints(ctx context.Context, args []string) error {
	path, err := requireOneArgument(args, "inlay-hints expects one file")
	if err != nil {
		return err
	}
	session, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer closeIgnoringError(session)
	document, err := session.openDocument(ctx, path)
	if err != nil {
		return err
	}
	lineIndex := cst.NewLineIndex(document.Source)
	line, character := lineIndex.PositionUTF16(uint32(len(document.Source)))
	params := textDocumentParams(document.URI)
	params["range"] = protocol.Range{
		End: protocol.Position{Line: int(line), Character: int(character)},
	}
	var result []protocol.InlayHint
	if err := session.call(ctx, "textDocument/inlayHint", params, &result); err != nil {
		return err
	}
	return writeJSON(r.out, result)
}

func (r *Runner) runCodeLens(ctx context.Context, args []string) error {
	path, err := requireOneArgument(args, "codelens expects one file")
	if err != nil {
		return err
	}
	session, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer closeIgnoringError(session)
	document, err := session.openDocument(ctx, path)
	if err != nil {
		return err
	}
	var result []protocol.CodeLens
	if err := session.call(
		ctx, "textDocument/codeLens", textDocumentParams(document.URI), &result,
	); err != nil {
		return err
	}
	for index := range result {
		if result[index].Command != nil || result[index].Data == nil {
			continue
		}
		var resolved protocol.CodeLens
		if err := session.call(ctx, "codeLens/resolve", result[index], &resolved); err != nil {
			return err
		}
		result[index] = resolved
	}
	return writeJSON(r.out, result)
}

func (r *Runner) runSelectionRanges(ctx context.Context, args []string) error {
	value, err := requireOneArgument(args, "selection-ranges expects one file position")
	if err != nil {
		return err
	}
	target, err := parsePositionTarget(value)
	if err != nil {
		return err
	}
	session, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer closeIgnoringError(session)
	document, err := session.openDocument(ctx, target.Path)
	if err != nil {
		return err
	}
	params := textDocumentParams(document.URI)
	params["positions"] = []protocol.Position{target.Position}
	var result []protocol.SelectionRange
	if err := session.call(ctx, "textDocument/selectionRange", params, &result); err != nil {
		return err
	}
	return writeJSON(r.out, result)
}

func (r *Runner) runLinkedEditing(ctx context.Context, args []string) error {
	var result *protocol.LinkedEditingRanges
	if err := r.positionRequest(ctx, args, "textDocument/linkedEditingRange", &result); err != nil {
		return err
	}
	return writeJSON(r.out, result)
}

func (r *Runner) runColors(ctx context.Context, args []string) error {
	var result []protocol.ColorInformation
	if err := r.documentRequest(ctx, args, "textDocument/documentColor", &result); err != nil {
		return err
	}
	return writeJSON(r.out, result)
}
