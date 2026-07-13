package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	jsonrpc "github.com/gumeniukcom/golang-jsonrpc2/v2"
	"github.com/gumeniukcom/golang-jsonrpc2/v2/jsonrpcstdio"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

// defaultRequestTimeout bounds a single LSP request. Indexing-heavy requests
// (e.g. the first textDocument/didOpen on a large project) can be slow, so
// this is deliberately generous.
const defaultRequestTimeout = 15 * time.Minute

// maxInboundFrameSize bounds a single inbound frame. The server uses
// full-document sync, so didOpen/didChange frames carry whole files; the
// pre-migration codec accepted them unbounded, and breaching the transport
// limit is fatal to the stream, so the cap must sit far above any real
// document an editor will sync.
const maxInboundFrameSize = 256 << 20 // 256 MiB

// Server represents the LSP server
type Server struct {
	rootPath             string
	completionProviders  []CompletionProvider
	definitionProviders  []GotoDefinitionProvider
	referencesProviders  []ReferencesProvider
	codeLensProviders    []CodeLensProvider
	diagnosticsProviders []DiagnosticsProvider
	codeActionProviders  []CodeActionProvider
	hoverProviders       []HoverProvider
	commandProviders     []CommandProvider
	indexers             map[string]indexer.Indexer
	commandMap           map[string]CommandFunc
	indexerMu            sync.RWMutex
	documentManager      *DocumentManager
	fileScanner          *indexer.FileScanner
	cacheDir             string
	version              string

	// pusher sends server-initiated notifications to the client. It is
	// captured from the first request's context (the transport injects one
	// per connection) and stays valid for the whole connection, so
	// background goroutines may use it after the capturing request returned.
	pusherMu sync.RWMutex
	pusher   jsonrpc.Pusher

	// serveCtx lives for the whole connection; background work spawned by
	// handlers must use it instead of the request context, which is
	// canceled when the handler returns. serveCancel stops Serve — the
	// "exit" notification triggers it.
	serveCtx    context.Context
	serveCancel context.CancelFunc
}

// NewServer creates a new LSP server
func NewServer(filescanner *indexer.FileScanner, cacheDir, version string) *Server {
	s := &Server{
		completionProviders:  make([]CompletionProvider, 0),
		definitionProviders:  make([]GotoDefinitionProvider, 0),
		referencesProviders:  make([]ReferencesProvider, 0),
		codeLensProviders:    make([]CodeLensProvider, 0),
		diagnosticsProviders: make([]DiagnosticsProvider, 0),
		codeActionProviders:  make([]CodeActionProvider, 0),
		hoverProviders:       make([]HoverProvider, 0),
		commandProviders:     make([]CommandProvider, 0),
		indexers:             make(map[string]indexer.Indexer),
		commandMap:           make(map[string]CommandFunc),
		documentManager:      NewDocumentManager(),
		fileScanner:          filescanner,
		cacheDir:             cacheDir,
		version:              version,
	}

	// Set the update callback to publish diagnostics
	s.fileScanner.SetOnUpdate(func() {
		log.Printf("Publishing diagnostics to all open files")
		go s.PublishDiagnostics(context.Background(), nil)
	})

	return s
}

// RegisterCompletionProvider registers a completion provider with the server
func (s *Server) RegisterCompletionProvider(provider CompletionProvider) {
	s.completionProviders = append(s.completionProviders, provider)
}

// RegisterDefinitionProvider registers a definition provider with the server
func (s *Server) RegisterDefinitionProvider(provider GotoDefinitionProvider) {
	s.definitionProviders = append(s.definitionProviders, provider)
}

// RegisterReferencesProvider registers a references provider with the server
func (s *Server) RegisterReferencesProvider(provider ReferencesProvider) {
	s.referencesProviders = append(s.referencesProviders, provider)
}

// RegisterCodeLensProvider registers a code lens provider with the server
func (s *Server) RegisterCodeLensProvider(provider CodeLensProvider) {
	s.codeLensProviders = append(s.codeLensProviders, provider)
}

// RegisterCodeActionProvider registers a code action provider with the server
func (s *Server) RegisterCodeActionProvider(provider CodeActionProvider) {
	s.codeActionProviders = append(s.codeActionProviders, provider)
}

// RegisterHoverProvider registers a hover provider with the server
func (s *Server) RegisterHoverProvider(provider HoverProvider) {
	s.hoverProviders = append(s.hoverProviders, provider)
}

// RegisterCommandProvider registers a command provider with the server
func (s *Server) RegisterCommandProvider(provider CommandProvider) {
	s.commandProviders = append(s.commandProviders, provider)
}

// RegisterIndexer adds an indexer to the registry
func (s *Server) RegisterIndexer(indexer indexer.Indexer, err error) {
	s.indexerMu.Lock()
	defer s.indexerMu.Unlock()
	s.indexers[indexer.ID()] = indexer
	s.fileScanner.AddIndexer(indexer)
}

// GetIndexer retrieves an indexer by ID
func (s *Server) GetIndexer(id string) (indexer.Indexer, bool) {
	s.indexerMu.RLock()
	defer s.indexerMu.RUnlock()
	indexer, ok := s.indexers[id]
	return indexer, ok
}

// shouldForceReindex checks if the current version differs from the last run
// and updates the stored version file
func (s *Server) shouldForceReindex() (bool, error) {
	if s.cacheDir == "" || s.version == "" || s.version == "dev" {
		return false, nil
	}

	versionFile := filepath.Join(s.cacheDir, "version.txt")

	// Check if version file exists
	previousVersion := ""
	forceReindex := false

	data, err := os.ReadFile(versionFile)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("failed to read version file: %w", err)
		}
		// File doesn't exist, will create it below
		forceReindex = true
	} else {
		previousVersion = strings.TrimSpace(string(data))
		forceReindex = previousVersion != s.version
	}

	// Update the version file with current version
	if err := os.WriteFile(versionFile, []byte(s.version), 0644); err != nil {
		return forceReindex, fmt.Errorf("failed to write version file: %w", err)
	}

	return forceReindex, nil
}

// indexAll builds or updates all registered indexes
// If forceReindex is true, it will clear the existing index before rebuilding
func (s *Server) indexAll(ctx context.Context, forceReindex bool) error {
	startTime := time.Now()

	// Send notification that indexing has started
	if err := s.notify(ctx, "shopware/indexingStarted", map[string]interface{}{
		"message": "Indexing started",
	}); err != nil {
		return err
	}

	if forceReindex {
		if err := s.fileScanner.ClearHashes(); err != nil {
			return err
		}
	}

	if err := s.fileScanner.IndexAll(ctx); err != nil {
		return err
	}

	elapsedTime := time.Since(startTime)

	// Send notification that indexing has completed
	if err := s.notify(ctx, "shopware/indexingCompleted", map[string]interface{}{
		"message":       "Indexing completed",
		"timeInSeconds": elapsedTime.Seconds(),
	}); err != nil {
		return err
	}

	return nil
}

// capturePusher stores the connection's pusher the first time a request
// carries one, so background goroutines (indexing, diagnostics) can notify
// the client outside any request context.
func (s *Server) capturePusher(ctx context.Context) {
	p, ok := jsonrpc.PusherFromContext(ctx)
	if !ok {
		return
	}
	s.pusherMu.Lock()
	if s.pusher == nil {
		s.pusher = p
	}
	s.pusherMu.Unlock()
}

// notify sends a server-initiated notification to the client. It is a no-op
// before the first request has been received (no pusher captured yet),
// mirroring the previous nil-connection check.
func (s *Server) notify(ctx context.Context, method string, params interface{}) error {
	s.pusherMu.RLock()
	p := s.pusher
	s.pusherMu.RUnlock()
	if p == nil {
		return nil
	}
	return p.Notify(ctx, method, params)
}

// backgroundContext returns a context that lives for the whole connection.
// Handlers must hand it (never their request context, which is canceled when
// the handler returns) to goroutines they spawn.
func (s *Server) backgroundContext() context.Context {
	if s.serveCtx != nil {
		return s.serveCtx
	}
	return context.Background()
}

// CloseAll closes all registered indexers and resources
func (s *Server) CloseAll() error {
	// Close document manager first
	if s.documentManager != nil {
		s.documentManager.Close()
	}

	// Then close all indexers
	s.indexerMu.RLock()
	defer s.indexerMu.RUnlock()

	for _, indexer := range s.indexers {
		if err := indexer.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) Start(in io.Reader, out io.Writer) error {
	// Register commands
	for _, provider := range s.commandProviders {
		for command, fn := range provider.GetCommands(context.Background()) {
			s.commandMap[command] = fn
		}
	}

	// A pusher captured on a previous Start belongs to a dead connection.
	s.pusherMu.Lock()
	s.pusher = nil
	s.pusherMu.Unlock()

	rpc := jsonrpc.New()
	rpc.SetDefaultTimeOut(defaultRequestTimeout)

	// Capture the connection's pusher from the first request so background
	// goroutines can send notifications outside any request context.
	rpc.RegisterGlobalInterceptorCall(func(ctx context.Context, _ string, _ json.RawMessage, _ interface{}) (context.Context, int, error) {
		s.capturePusher(ctx)
		return ctx, jsonrpc.OK, nil
	})

	s.registerMethods(rpc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.serveCtx = ctx
	s.serveCancel = cancel

	// Serve blocks until the client closes stdin (clean EOF, returns nil),
	// the "exit" notification cancels the context, or the stream fails.
	// The server advertises full-document sync, so didOpen/didChange frames
	// carry entire file contents; the previous codec had no size limit, so
	// the transport default (8 MiB, fatal on breach) must be raised.
	err := jsonrpcstdio.Serve(ctx, rpc, jsonrpcstdio.FramingContentLength, in, out,
		jsonrpcstdio.WithMaxMessageSize(maxInboundFrameSize))
	if errors.Is(err, context.Canceled) {
		// Canceled by the "exit" notification: an orderly shutdown.
		return nil
	}
	return err
}

// registerMethods installs all handlers into the dispatcher. Registration
// order preserves the pre-migration dispatch precedence (exit, then
// commands, then built-in LSP methods): a duplicate name keeps the earlier
// registration and is logged.
func (s *Server) registerMethods(rpc *jsonrpc.JSONRPC) {
	register := func(name string, method jsonrpc.RPCMethod) {
		if err := rpc.RegisterMethod(name, method); err != nil {
			log.Printf("Skipping registration of method %q: %v", name, err)
		}
	}

	register("exit", jsonrpc.Typed(s.handleExit))

	for command, fn := range s.commandMap {
		register(command, commandMethod(fn))
	}

	register("initialize", jsonrpc.Typed(s.handleInitialize))
	register("initialized", jsonrpc.Typed(s.handleInitialized))
	register("textDocument/didOpen", jsonrpc.Typed(s.handleDidOpen))
	register("textDocument/didChange", jsonrpc.Typed(s.handleDidChange))
	register("textDocument/didClose", jsonrpc.Typed(s.handleDidClose))
	register("textDocument/completion", jsonrpc.Typed(s.handleCompletion))
	register("textDocument/definition", jsonrpc.Typed(s.handleDefinition))
	register("textDocument/references", jsonrpc.Typed(s.handleReferences))
	register("textDocument/codeLens", jsonrpc.Typed(s.handleCodeLens))
	register("textDocument/hover", jsonrpc.Typed(s.handleHover))
	register("textDocument/diagnostic", jsonrpc.Typed(s.handleDiagnostic))
	register("codeLens/resolve", jsonrpc.Typed(s.handleResolveCodeLens))
	register("textDocument/codeAction", jsonrpc.Typed(s.handleCodeAction))
	register("shopware/forceReindex", jsonrpc.Typed(s.handleForceReindex))
	register("shutdown", jsonrpc.Typed(s.handleShutdown))
	register("workspace/didCreateFiles", jsonrpc.Typed(s.handleDidCreateFiles))
	register("workspace/didRenameFiles", jsonrpc.Typed(s.handleDidRenameFiles))
	register("workspace/didDeleteFiles", jsonrpc.Typed(s.handleDidDeleteFiles))
	register("workspace/didChangeWatchedFiles", jsonrpc.Typed(s.handleDidChangeWatchedFiles))
}

// commandMethod adapts a CommandFunc to the dispatcher's method signature,
// preserving the raw-params contract commands had before the migration.
func commandMethod(fn CommandFunc) jsonrpc.RPCMethod {
	return func(ctx context.Context, data json.RawMessage) (json.RawMessage, int, error) {
		var args *json.RawMessage
		if len(data) > 0 {
			args = &data
		}
		result, err := fn(ctx, args)
		if err != nil {
			// Command errors were client-visible before the migration; keep
			// the detail on the wire via the error's data field (the
			// dispatcher never serializes plain error text).
			return nil, jsonrpc.InternalErrorCode, jsonrpc.NewRPCError(jsonrpc.InternalErrorCode, err).WithData(err.Error())
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, jsonrpc.InternalErrorCode, fmt.Errorf("failed to marshal command result: %w", err)
		}
		return raw, jsonrpc.OK, nil
	}
}

// emptyParams is the parameter type for methods that take no parameters
// (or whose parameters are ignored).
type emptyParams struct{}

// didOpenParams mirrors the subset of LSP DidOpenTextDocumentParams we use.
type didOpenParams struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Text    string `json:"text"`
		Version int    `json:"version"`
	} `json:"textDocument"`
}

// didChangeParams mirrors the subset of LSP DidChangeTextDocumentParams we use.
type didChangeParams struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Version int    `json:"version"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

// didCloseParams mirrors the subset of LSP DidCloseTextDocumentParams we use.
type didCloseParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

// handleExit handles the exit notification: it stops the server loop.
func (s *Server) handleExit(context.Context, emptyParams) (interface{}, error) {
	log.Println("Received exit notification, exiting")
	if s.serveCancel != nil {
		s.serveCancel()
	}
	return nil, nil
}

// handleInitialize handles the LSP initialize request.
func (s *Server) handleInitialize(ctx context.Context, params protocol.InitializeParams) (interface{}, error) {
	return s.initialize(ctx, &params), nil
}

// handleInitialized builds the index when the client is initialized.
func (s *Server) handleInitialized(context.Context, emptyParams) (interface{}, error) {
	ctx := s.backgroundContext()
	go func() {
		// Check if we need to force reindex due to version change
		forceReindex, err := s.shouldForceReindex()
		if err != nil {
			log.Printf("Warning: Failed to check version for reindex: %v", err)
		}

		if forceReindex {
			log.Printf("Version changed to %s, forcing reindex", s.version)
		}

		// Index all registered indexers
		if err := s.indexAll(ctx, forceReindex); err != nil {
			log.Printf("Error indexing: %v", err)
		} else if forceReindex {
			log.Println("Force reindex completed successfully")
		}

		// Start the file watcher after the initial index build to avoid
		// paying for two recursive traversals during startup.
		if err := s.fileScanner.StartWatcher(); err != nil {
			log.Printf("Error starting file watcher: %v", err)
		} else {
			log.Println("File watcher started successfully")
		}
	}()
	return nil, nil
}

func (s *Server) handleDidOpen(_ context.Context, params didOpenParams) (interface{}, error) {
	s.documentManager.OpenDocument(params.TextDocument.URI, params.TextDocument.Text, params.TextDocument.Version)

	// Run diagnostics on the opened document
	go s.publishDiagnostics(s.backgroundContext(), params.TextDocument.URI, params.TextDocument.Version)
	return nil, nil
}

func (s *Server) handleDidChange(_ context.Context, params didChangeParams) (interface{}, error) {
	if len(params.ContentChanges) > 0 {
		s.documentManager.UpdateDocument(params.TextDocument.URI, params.ContentChanges[0].Text, params.TextDocument.Version)

		// Run diagnostics on the updated document
		go s.publishDiagnostics(s.backgroundContext(), params.TextDocument.URI, params.TextDocument.Version)
	}
	return nil, nil
}

func (s *Server) handleDidClose(_ context.Context, params didCloseParams) (interface{}, error) {
	s.documentManager.CloseDocument(params.TextDocument.URI)
	return nil, nil
}

func (s *Server) handleCompletion(ctx context.Context, params protocol.CompletionParams) (*protocol.CompletionList, error) {
	return s.completion(ctx, &params), nil
}

func (s *Server) handleDefinition(ctx context.Context, params protocol.DefinitionParams) ([]protocol.Location, error) {
	return s.definition(ctx, &params), nil
}

func (s *Server) handleReferences(ctx context.Context, params protocol.ReferenceParams) ([]protocol.Location, error) {
	return s.references(ctx, &params), nil
}

func (s *Server) handleCodeLens(ctx context.Context, params protocol.CodeLensParams) ([]protocol.CodeLens, error) {
	return s.codeLens(ctx, &params), nil
}

func (s *Server) handleHover(ctx context.Context, params protocol.HoverParams) (*protocol.Hover, error) {
	h, err := s.hover(ctx, &params)
	if err != nil {
		return nil, jsonrpc.NewRPCError(jsonrpc.InternalErrorCode, err).WithData(err.Error())
	}
	return h, nil
}

func (s *Server) handleDiagnostic(ctx context.Context, params protocol.DiagnosticParams) (interface{}, error) {
	return s.diagnostic(ctx, &params), nil
}

func (s *Server) handleResolveCodeLens(ctx context.Context, codeLens protocol.CodeLens) (*protocol.CodeLens, error) {
	cl, err := s.resolveCodeLens(ctx, &codeLens)
	if err != nil {
		return nil, jsonrpc.NewRPCError(jsonrpc.InternalErrorCode, err).WithData(err.Error())
	}
	return cl, nil
}

func (s *Server) handleCodeAction(ctx context.Context, params protocol.CodeActionParams) ([]protocol.CodeAction, error) {
	return s.codeAction(ctx, &params), nil
}

// handleForceReindex forces a reindex of all indexers in the background.
func (s *Server) handleForceReindex(context.Context, emptyParams) (map[string]interface{}, error) {
	ctx := s.backgroundContext()
	go func() {
		if err := s.indexAll(ctx, true); err != nil {
			log.Printf("Error force reindexing: %v", err)
		}
	}()
	return map[string]interface{}{
		"message": "Force reindexing started",
	}, nil
}

func (s *Server) handleShutdown(context.Context, emptyParams) (interface{}, error) {
	// Clean up resources
	if err := s.CloseAll(); err != nil {
		log.Printf("Error closing indexers: %v", err)
	}

	log.Println("Received shutdown request, waiting for exit notification")
	return nil, nil
}

func (s *Server) handleDidCreateFiles(ctx context.Context, params protocol.CreateFilesParams) (interface{}, error) {
	files := make([]string, len(params.Files))
	for i, file := range params.Files {
		files[i] = strings.TrimPrefix(file.URI, "file://")
	}
	if err := s.fileScanner.IndexFiles(ctx, files); err != nil {
		log.Printf("Error indexing new files: %v", err)
	}

	log.Printf("Watcher Client: Created files: %v", files)

	return nil, nil
}

func (s *Server) handleDidRenameFiles(ctx context.Context, params protocol.RenameFilesParams) (interface{}, error) {
	oldFiles := make([]string, len(params.Files))
	newFiles := make([]string, len(params.Files))
	for i, file := range params.Files {
		oldFiles[i] = strings.TrimPrefix(file.OldURI, "file://")
		newFiles[i] = strings.TrimPrefix(file.NewURI, "file://")
	}

	if err := s.fileScanner.IndexFiles(ctx, newFiles); err != nil {
		log.Printf("Error indexing new files: %v", err)
	}
	if err := s.fileScanner.RemoveFiles(ctx, oldFiles); err != nil {
		log.Printf("Error removing old files: %v", err)
	}

	log.Printf("Watcher Client: Renamed files: %v", oldFiles)

	return nil, nil
}

func (s *Server) handleDidDeleteFiles(ctx context.Context, params protocol.DeleteFilesParams) (interface{}, error) {
	files := make([]string, len(params.Files))
	for i, file := range params.Files {
		files[i] = strings.TrimPrefix(file.URI, "file://")
	}

	log.Printf("Watcher Client: Deleting files: %v", files)

	if err := s.fileScanner.RemoveFiles(ctx, files); err != nil {
		log.Printf("Error removing old files: %v", err)
	}
	return nil, nil
}

func (s *Server) handleDidChangeWatchedFiles(ctx context.Context, params protocol.DidChangeWatchedFilesParams) (interface{}, error) {
	createFiles := []string{}
	deleteFiles := []string{}

	// Handle file change events
	for _, change := range params.Changes {
		switch change.Type {
		case int(protocol.FileCreated):
			createFiles = append(createFiles, strings.TrimPrefix(change.URI, "file://"))
		case int(protocol.FileChanged):
			createFiles = append(createFiles, strings.TrimPrefix(change.URI, "file://"))
		case int(protocol.FileDeleted):
			deleteFiles = append(deleteFiles, strings.TrimPrefix(change.URI, "file://"))
		}
	}

	if len(createFiles) > 0 {
		log.Printf("Watcher Client: Creating files: %v", createFiles)

		if err := s.fileScanner.IndexFiles(ctx, createFiles); err != nil {
			log.Printf("Error indexing new files: %v", err)
		}
	}

	if len(deleteFiles) > 0 {
		log.Printf("Watcher Client: Deleting files: %v", deleteFiles)

		if err := s.fileScanner.RemoveFiles(ctx, deleteFiles); err != nil {
			log.Printf("Error removing old files: %v", err)
		}
	}

	return nil, nil
}

// initialize handles the LSP initialize request
func (s *Server) initialize(ctx context.Context, params *protocol.InitializeParams) interface{} {
	// Extract root path from params
	s.extractRootPath(params)

	// Collect all trigger characters from providers
	triggerChars := s.collectTriggerCharacters()

	// Collect all code action kinds from providers
	codeActionKinds := s.collectCodeActionKinds()

	// Define server capabilities
	return map[string]interface{}{
		"capabilities": map[string]interface{}{
			"textDocumentSync": map[string]interface{}{
				"openClose": true,
				"change":    1, // Full sync
			},
			"diagnosticProvider": map[string]interface{}{
				"interFileDependencies": true,
				"workspaceDiagnostics":  false,
			},
			"completionProvider": map[string]interface{}{
				"triggerCharacters": triggerChars,
			},
			"definitionProvider": true,
			"referencesProvider": true,
			"hoverProvider":      true,
			"codeLensProvider": map[string]interface{}{
				"resolveProvider": true,
			},
			"codeActionProvider": map[string]interface{}{
				"codeActionKinds": codeActionKinds,
			},
			"workspace": map[string]interface{}{
				"fileOperations": map[string]interface{}{
					"didCreate": map[string]interface{}{
						"filters": []map[string]interface{}{
							{"pattern": map[string]interface{}{"glob": "**/*.xml"}},
							{"pattern": map[string]interface{}{"glob": "**/*.php"}},
						},
					},
					"didRename": map[string]interface{}{
						"filters": []map[string]interface{}{
							{"pattern": map[string]interface{}{"glob": "**/*.xml"}},
							{"pattern": map[string]interface{}{"glob": "**/*.php"}},
						},
					},
					"didDelete": map[string]interface{}{
						"filters": []map[string]interface{}{
							{"pattern": map[string]interface{}{"glob": "**/*.xml"}},
							{"pattern": map[string]interface{}{"glob": "**/*.php"}},
						},
					},
				},
			},
		},
	}
}

// extractRootPath extracts the root path from the initialize params
func (s *Server) extractRootPath(params *protocol.InitializeParams) {
	// Try to get from RootPath
	if params.RootPath != "" {
		s.rootPath = params.RootPath
		return
	}

	// Try to get from RootURI
	if params.RootURI != "" {
		rootURI := params.RootURI
		s.rootPath = strings.TrimPrefix(rootURI, "file://")
		return
	}

	// Try to get from WorkspaceFolders
	if len(params.WorkspaceFolders) > 0 {
		folder := params.WorkspaceFolders[0]
		s.rootPath = strings.TrimPrefix(folder.URI, "file://")
		return
	}

	// Fall back to current directory
	s.rootPath, _ = os.Getwd()
}

// collectTriggerCharacters collects all trigger characters from registered providers
func (s *Server) collectTriggerCharacters() []string {
	// Use a map to deduplicate trigger characters
	triggerCharsMap := make(map[string]bool)

	for _, provider := range s.completionProviders {
		for _, char := range provider.GetTriggerCharacters() {
			triggerCharsMap[char] = true
		}
	}

	// Convert map keys to slice
	triggerChars := make([]string, 0, len(triggerCharsMap))
	for char := range triggerCharsMap {
		triggerChars = append(triggerChars, char)
	}

	return triggerChars
}

// collectCodeActionKinds collects all code action kinds from registered providers
func (s *Server) collectCodeActionKinds() []protocol.CodeActionKind {
	// Use a map to deduplicate code action kinds
	kindsMap := make(map[protocol.CodeActionKind]bool)

	for _, provider := range s.codeActionProviders {
		for _, kind := range provider.GetCodeActionKinds() {
			kindsMap[kind] = true
		}
	}

	// Convert map keys to slice
	kinds := make([]protocol.CodeActionKind, 0, len(kindsMap))
	for kind := range kindsMap {
		kinds = append(kinds, kind)
	}

	return kinds
}

func (s *Server) DocumentManager() *DocumentManager {
	return s.documentManager
}

func (s *Server) FileScanner() *indexer.FileScanner {
	return s.fileScanner
}

// RegisterDiagnosticsProvider registers a diagnostics provider with the server
func (s *Server) RegisterDiagnosticsProvider(provider DiagnosticsProvider) {
	s.diagnosticsProviders = append(s.diagnosticsProviders, provider)
}

type docAnalyse struct {
	uri     string
	version int
}

func (s *Server) PublishDiagnostics(ctx context.Context, files []string) {
	var docs []docAnalyse

	if files == nil {
		for _, doc := range s.DocumentManager().documents {
			docs = append(docs, docAnalyse{
				uri:     doc.URI,
				version: doc.Version,
			})
		}
	} else {
		for _, uri := range files {
			version := 0

			if doc, ok := s.DocumentManager().GetDocument(uri); ok {
				version = doc.Version
			}

			docs = append(docs, docAnalyse{
				uri:     uri,
				version: version,
			})
		}
	}

	for _, doc := range docs {
		go s.publishDiagnostics(ctx, doc.uri, doc.version)
	}
}

// publishDiagnostics collects and publishes diagnostics for a document
func (s *Server) publishDiagnostics(ctx context.Context, uri string, version int) {
	s.pusherMu.RLock()
	hasPusher := s.pusher != nil
	s.pusherMu.RUnlock()
	if !hasPusher {
		return
	}

	// Get document content
	content, ok := s.documentManager.GetDocumentText(uri)
	if !ok {
		return
	}

	// Collect diagnostics from all providers
	allDiagnostics := []protocol.Diagnostic{}

	node := s.documentManager.GetRootNode(uri)

	if node == nil {
		return
	}

	for _, provider := range s.diagnosticsProviders {
		diagnostics, err := provider.GetDiagnostics(ctx, uri, node, content)
		if err != nil {
			log.Printf("Error getting diagnostics from provider %s: %v", provider, err)
			continue
		}

		allDiagnostics = append(allDiagnostics, diagnostics...)
	}

	// Publish diagnostics
	params := protocol.PublishDiagnosticsParams{
		URI:         uri,
		Version:     version,
		Diagnostics: allDiagnostics,
	}

	if err := s.notify(ctx, "textDocument/publishDiagnostics", params); err != nil {
		log.Printf("Error publishing diagnostics: %v", err)
	}
}

// diagnostic handles textDocument/diagnostic requests
func (s *Server) diagnostic(ctx context.Context, params *protocol.DiagnosticParams) interface{} {
	uri := params.TextDocument.URI

	// Get document content
	content, ok := s.documentManager.GetDocumentText(uri)
	if !ok {
		return protocol.DiagnosticResult{
			Items: []protocol.Diagnostic{},
		}
	}

	// Collect diagnostics from all providers
	allDiagnostics := []protocol.Diagnostic{}

	node := s.documentManager.GetRootNode(uri)

	if node == nil {
		return protocol.DiagnosticResult{
			Items: []protocol.Diagnostic{},
		}
	}

	for _, provider := range s.diagnosticsProviders {
		diagnostics, err := provider.GetDiagnostics(ctx, uri, node, content)
		if err != nil {
			log.Printf("Error getting diagnostics from provider %s: %v", provider, err)
			continue
		}

		allDiagnostics = append(allDiagnostics, diagnostics...)
	}

	return protocol.DiagnosticResult{
		Items: allDiagnostics,
	}
}

// codeAction handles textDocument/codeAction requests
func (s *Server) codeAction(ctx context.Context, params *protocol.CodeActionParams) []protocol.CodeAction {
	node, docText, ok := s.documentManager.GetNodeAtPosition(params.TextDocument.URI, params.Range.Start.Line, params.Range.Start.Character)
	if ok {
		params.Node = node
		params.DocumentContent = docText.Text
	}

	// Collect code actions from all providers
	var allCodeActions []protocol.CodeAction
	for _, provider := range s.codeActionProviders {
		codeActions := provider.GetCodeActions(ctx, params)
		allCodeActions = append(allCodeActions, codeActions...)
	}

	return allCodeActions
}
