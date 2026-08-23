package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"

	"github.com/sourcegraph/jsonrpc2"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/projectconfig"
)

type rpcMethodHandler func(
	context.Context,
	*jsonrpc2.Request,
) (interface{}, error)

func (s *Server) handle(
	ctx context.Context,
	conn *jsonrpc2.Conn,
	req *jsonrpc2.Request,
) (interface{}, error) {
	s.setConnection(conn)
	if req.Method == "exit" {
		log.Println("Received exit notification, exiting")
		if err := conn.Close(); err != nil {
			log.Printf("error closing connection: %v", err)
		}
		return nil, nil
	}
	if feature := featureForMethod(req.Method); feature != "" &&
		!s.featureEnabled(feature) {
		return disabledFeatureResult(req.Method), nil
	}
	if command, ok := s.commandMap[req.Method]; ok {
		if !s.featureEnabled("commands") && !isConfigurationMethod(req.Method) {
			return nil, &jsonrpc2.Error{
				Code:    jsonrpc2.CodeMethodNotFound,
				Message: "Shopware commands are disabled by configuration",
			}
		}
		return command(ctx, req.Params)
	}
	if handler, ok := s.methodHandlers[req.Method]; ok {
		return handler(ctx, req)
	}
	if req.ID == (jsonrpc2.ID{}) {
		return nil, nil
	}
	return nil, &jsonrpc2.Error{
		Code:    jsonrpc2.CodeMethodNotFound,
		Message: "Method not implemented: " + req.Method,
	}
}

func (s *Server) protocolMethodHandlers() map[string]rpcMethodHandler {
	return map[string]rpcMethodHandler{
		"initialize":                        s.handleInitialize,
		"initialized":                       s.handleInitialized,
		"textDocument/didOpen":              s.handleDidOpen,
		"textDocument/didChange":            s.handleDidChange,
		"textDocument/didClose":             s.handleDidClose,
		"textDocument/completion":           rpcValueHandler(s.completion),
		"textDocument/definition":           rpcValueHandler(s.definition),
		"textDocument/implementation":       rpcValueHandler(s.implementation),
		"textDocument/prepareTypeHierarchy": rpcValueHandler(s.prepareTypeHierarchy),
		"typeHierarchy/supertypes":          rpcValueHandler(s.handleTypeHierarchySupertypes),
		"typeHierarchy/subtypes":            rpcValueHandler(s.handleTypeHierarchySubtypes),
		"textDocument/prepareCallHierarchy": rpcResultHandler(s.prepareCallHierarchy),
		"callHierarchy/incomingCalls":       rpcResultHandler(s.handleIncomingCalls),
		"callHierarchy/outgoingCalls":       rpcResultHandler(s.handleOutgoingCalls),
		"textDocument/references":           rpcResultHandler(s.references),
		"textDocument/codeLens":             rpcResultHandler(s.codeLens),
		"textDocument/hover":                rpcResultHandler(s.hover),
		"textDocument/signatureHelp":        rpcResultHandler(s.signatureHelp),
		"textDocument/rename":               rpcResultHandler(s.rename),
		"textDocument/inlayHint":            rpcResultHandler(s.inlayHints),
		"textDocument/documentLink":         rpcResultHandler(s.documentLinks),
		"textDocument/documentSymbol":       rpcResultHandler(s.documentSymbols),
		"textDocument/documentHighlight":    rpcResultHandler(s.documentHighlights),
		"textDocument/linkedEditingRange":   rpcResultHandler(s.linkedEditingRanges),
		"textDocument/foldingRange":         rpcResultHandler(s.foldingRanges),
		"textDocument/formatting":           rpcResultHandler(s.formatting),
		"textDocument/selectionRange":       rpcResultHandler(s.selectionRanges),
		"textDocument/documentColor":        rpcResultHandler(s.documentColors),
		"textDocument/colorPresentation":    rpcResultHandler(s.colorPresentations),
		"textDocument/semanticTokens/full":  rpcResultHandler(s.semanticTokens),
		"textDocument/diagnostic":           rpcValueHandler(s.diagnostic),
		"shopware/configuration/catalog":    s.handleConfigurationCatalog,
		"shopware/configuration/effective":  s.handleEffectiveConfiguration,
		"shopware/configuration/reload":     s.handleConfigurationReload,
		"workspace/didChangeConfiguration":  s.handleDidChangeConfiguration,
		"codeLens/resolve":                  rpcResultHandler(s.resolveCodeLens),
		"textDocument/codeAction":           rpcValueHandler(s.codeAction),
		"codeAction/resolve":                rpcValueHandler(s.handleResolveCodeAction),
		"shopware/forceReindex":             s.handleForceReindex,
		"shopware/index/stats":              s.handleIndexStats,
		"shopware/commands":                 s.handleCommands,
		"workspace/executeCommand":          s.handleExecuteCommand,
		"workspace/willRenameFiles":         rpcResultHandler(s.willRenameFiles),
		"workspace/symbol":                  rpcResultHandler(s.workspaceSymbols),
		"shutdown":                          s.handleShutdown,
		"workspace/didCreateFiles":          noOpRPCHandler,
		"workspace/didRenameFiles":          noOpRPCHandler,
		"workspace/didDeleteFiles":          noOpRPCHandler,
		"workspace/didChangeWatchedFiles":   noOpRPCHandler,
	}
}

func rpcValueHandler[Params, Result any](
	handle func(context.Context, *Params) Result,
) rpcMethodHandler {
	return func(ctx context.Context, request *jsonrpc2.Request) (interface{}, error) {
		params, err := decodeRPCParams[Params](request)
		if err != nil {
			return nil, err
		}
		return handle(ctx, params), nil
	}
}

func rpcResultHandler[Params, Result any](
	handle func(context.Context, *Params) (Result, error),
) rpcMethodHandler {
	return func(ctx context.Context, request *jsonrpc2.Request) (interface{}, error) {
		params, err := decodeRPCParams[Params](request)
		if err != nil {
			return nil, err
		}
		return handle(ctx, params)
	}
}

func decodeRPCParams[Params any](request *jsonrpc2.Request) (*Params, error) {
	var params Params
	if request == nil || request.Params == nil {
		return nil, fmt.Errorf("missing request parameters")
	}
	if err := json.Unmarshal(*request.Params, &params); err != nil {
		return nil, err
	}
	return &params, nil
}

func noOpRPCHandler(
	context.Context,
	*jsonrpc2.Request,
) (interface{}, error) {
	// fsnotify is the single source of index file events. Accepting client
	// notifications as well would process the same change twice.
	return nil, nil
}

func (s *Server) handleInitialize(
	ctx context.Context,
	request *jsonrpc2.Request,
) (interface{}, error) {
	params, err := decodeRPCParams[protocol.InitializeParams](request)
	if err != nil {
		return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeParseError, Message: err.Error()}
	}
	result, err := s.initialize(ctx, params)
	if err != nil {
		return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeInvalidParams, Message: err.Error()}
	}
	return result, nil
}

func (s *Server) handleInitialized(
	ctx context.Context,
	_ *jsonrpc2.Request,
) (interface{}, error) {
	s.notifyConfigurationError(ctx)
	if s.fileScanner == nil || !s.EffectiveConfiguration().Indexing.Enabled {
		s.notifyDisabledCLIIndexing(ctx)
		return nil, nil
	}
	s.startBackground(func(ctx context.Context) {
		forceReindex := s.workspace != nil && s.workspace.InitialForceReindex()
		if err := s.indexAll(ctx, forceReindex); err != nil {
			log.Printf("Error indexing: %v", err)
			s.notifyIndexingFailed(ctx, err)
			if ctx.Err() != nil {
				return
			}
		}
		if ctx.Err() != nil || s.initializationOptions.CLIMode {
			return
		}
		if err := s.fileScanner.StartWatcher(); err != nil {
			log.Printf("Error starting file watcher: %v", err)
		} else {
			log.Println("File watcher started successfully")
		}
	})
	return nil, nil
}

func (s *Server) notifyConfigurationError(ctx context.Context) {
	configurationErr := s.configurationError()
	if configurationErr == nil {
		return
	}
	if conn := s.connection(); conn != nil {
		_ = conn.Notify(ctx, "window/showMessage", map[string]interface{}{
			"type":    1,
			"message": "Invalid Shopware LSP configuration: " + configurationErr.Error(),
		})
	}
}

func (s *Server) notifyDisabledCLIIndexing(ctx context.Context) {
	if !s.initializationOptions.CLIMode {
		return
	}
	if conn := s.connection(); conn != nil {
		_ = conn.Notify(ctx, "shopware/indexingCompleted", map[string]interface{}{
			"message": "Indexing disabled", "timeInSeconds": 0,
		})
	}
}

func (s *Server) handleDidOpen(
	_ context.Context,
	request *jsonrpc2.Request,
) (interface{}, error) {
	params, err := decodeRPCParams[didOpenParams](request)
	if err != nil {
		return nil, err
	}
	document := params.TextDocument
	s.documentManager.OpenDocument(document.URI, document.Text, document.Version)
	if !s.initializationOptions.CLIMode {
		s.scheduleDiagnostics(document.URI, document.Version, 0)
	}
	return nil, nil
}

type didOpenParams struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Text    string `json:"text"`
		Version int    `json:"version"`
	} `json:"textDocument"`
}

func (s *Server) handleDidChange(
	_ context.Context,
	request *jsonrpc2.Request,
) (interface{}, error) {
	params, err := decodeRPCParams[didChangeParams](request)
	if err != nil {
		return nil, err
	}
	if len(params.ContentChanges) == 0 {
		return nil, nil
	}
	document := params.TextDocument
	s.documentManager.UpdateDocument(
		document.URI,
		params.ContentChanges[0].Text,
		document.Version,
	)
	if !s.initializationOptions.CLIMode {
		s.scheduleDiagnostics(document.URI, document.Version, diagnosticsDebounce)
	}
	return nil, nil
}

type didChangeParams struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Version int    `json:"version"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

func (s *Server) handleDidClose(
	ctx context.Context,
	request *jsonrpc2.Request,
) (interface{}, error) {
	params, err := decodeRPCParams[didCloseParams](request)
	if err != nil {
		return nil, err
	}
	uri := params.TextDocument.URI
	s.cancelDiagnostics(uri)
	s.documentManager.CloseDocument(uri)
	s.clearPublishedDiagnostics(ctx, uri)
	return nil, nil
}

type didCloseParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

func (s *Server) handleTypeHierarchySupertypes(
	ctx context.Context,
	params *protocol.TypeHierarchySupertypesParams,
) []protocol.TypeHierarchyItem {
	return s.typeHierarchySupertypes(ctx, params.Item)
}

func (s *Server) handleTypeHierarchySubtypes(
	ctx context.Context,
	params *protocol.TypeHierarchySubtypesParams,
) []protocol.TypeHierarchyItem {
	return s.typeHierarchySubtypes(ctx, params.Item)
}

func (s *Server) handleIncomingCalls(
	ctx context.Context,
	params *protocol.CallHierarchyIncomingCallsParams,
) ([]protocol.CallHierarchyIncomingCall, error) {
	return s.callHierarchyIncomingCalls(ctx, params.Item)
}

func (s *Server) handleOutgoingCalls(
	ctx context.Context,
	params *protocol.CallHierarchyOutgoingCallsParams,
) ([]protocol.CallHierarchyOutgoingCall, error) {
	return s.callHierarchyOutgoingCalls(ctx, params.Item)
}

func (s *Server) handleConfigurationCatalog(
	context.Context,
	*jsonrpc2.Request,
) (interface{}, error) {
	return s.configurationCatalog(), nil
}

func (s *Server) handleEffectiveConfiguration(
	context.Context,
	*jsonrpc2.Request,
) (interface{}, error) {
	return s.EffectiveConfiguration(), nil
}

func (s *Server) handleConfigurationReload(
	ctx context.Context,
	_ *jsonrpc2.Request,
) (interface{}, error) {
	return s.reloadProjectConfiguration(ctx), nil
}

func (s *Server) handleDidChangeConfiguration(
	ctx context.Context,
	request *jsonrpc2.Request,
) (interface{}, error) {
	params, err := decodeRPCParams[struct {
		Settings projectconfig.Partial `json:"settings"`
	}](request)
	if err != nil {
		return nil, err
	}
	result := s.replaceEditorConfiguration(ctx, params.Settings)
	if result.Error == "" {
		return nil, nil
	}
	log.Printf("Invalid editor configuration: %s", result.Error)
	if conn := s.connection(); conn != nil {
		_ = conn.Notify(ctx, "window/showMessage", map[string]interface{}{
			"type":    1,
			"message": "Invalid Shopware LSP configuration: " + result.Error,
		})
	}
	return nil, nil
}

func (s *Server) handleResolveCodeAction(
	ctx context.Context,
	action *protocol.CodeAction,
) protocol.CodeAction {
	return s.resolveCodeAction(ctx, *action)
}

func (s *Server) handleForceReindex(
	ctx context.Context,
	_ *jsonrpc2.Request,
) (interface{}, error) {
	if !s.EffectiveConfiguration().Indexing.Enabled {
		return nil, fmt.Errorf("indexing is disabled by configuration")
	}
	s.startBackground(func(ctx context.Context) {
		if err := s.indexAll(ctx, true); err != nil {
			log.Printf("Error force reindexing: %v", err)
			s.notifyIndexingFailed(ctx, err)
		}
	})
	return map[string]interface{}{"message": "Force reindexing started"}, nil
}

func (s *Server) handleIndexStats(
	ctx context.Context,
	_ *jsonrpc2.Request,
) (interface{}, error) {
	if s.fileScanner == nil {
		return nil, fmt.Errorf("workspace is not initialized")
	}
	return s.fileScanner.Stats(ctx)
}

func (s *Server) handleCommands(
	context.Context,
	*jsonrpc2.Request,
) (interface{}, error) {
	commands := make([]string, 0, len(s.commandMap))
	for command := range s.commandMap {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	return commands, nil
}

func (s *Server) handleExecuteCommand(
	ctx context.Context,
	request *jsonrpc2.Request,
) (interface{}, error) {
	params, err := decodeRPCParams[protocol.CommandRequest](request)
	if err != nil {
		return nil, err
	}
	command, found := s.commandMap[params.Command]
	if !found {
		return nil, &jsonrpc2.Error{
			Code:    jsonrpc2.CodeMethodNotFound,
			Message: "Unknown Shopware command: " + params.Command,
		}
	}
	if len(params.Arguments) > 1 {
		return nil, &jsonrpc2.Error{
			Code:    jsonrpc2.CodeInvalidParams,
			Message: "Shopware commands accept at most one JSON argument",
		}
	}
	raw := json.RawMessage("{}")
	if len(params.Arguments) == 1 {
		raw = params.Arguments[0]
	}
	return command(ctx, &raw)
}

func (s *Server) handleShutdown(
	context.Context,
	*jsonrpc2.Request,
) (interface{}, error) {
	if err := s.CloseAll(); err != nil {
		log.Printf("Error closing indexers: %v", err)
	}
	log.Println("Received shutdown request, waiting for exit notification")
	return nil, nil
}
