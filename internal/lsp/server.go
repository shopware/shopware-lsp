package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/projectconfig"
	"github.com/sourcegraph/jsonrpc2"
)

// Server represents the LSP server
type Server struct {
	rootPath                        string
	version                         string
	initializationOptions           protocol.InitializationOptions
	initializeMu                    sync.Mutex
	initialized                     bool
	connMu                          sync.RWMutex
	conn                            *jsonrpc2.Conn
	completionProviders             []CompletionProvider
	definitionProviders             []GotoDefinitionProvider
	implementationProviders         []ImplementationProvider
	typeHierarchyProviders          []TypeHierarchyProvider
	callHierarchyProviders          []CallHierarchyProvider
	referencesProviders             []ReferencesProvider
	codeLensProviders               []CodeLensProvider
	actionProviders                 []ActionProvider
	inspections                     *inspectionRegistry
	codeActionResolveSupport        bool
	hoverProviders                  []HoverProvider
	signatureProviders              []SignatureHelpProvider
	renameProviders                 []RenameProvider
	inlayHintProviders              []InlayHintProvider
	documentLinkProviders           []DocumentLinkProvider
	documentSymbolProviders         []DocumentSymbolProvider
	documentHighlightProviders      []DocumentHighlightProvider
	linkedEditingProviders          []LinkedEditingRangeProvider
	foldingRangeProviders           []FoldingRangeProvider
	selectionRangeProviders         []SelectionRangeProvider
	documentColorProviders          []DocumentColorProvider
	semanticTokensProviders         []SemanticTokensProvider
	fileRenameProviders             []FileRenameProvider
	workspaceSymbolProviders        []WorkspaceSymbolProvider
	commandProviders                []CommandProvider
	commandMap                      map[string]CommandFunc
	contextEnrichers                map[language.ID]ContextEnricher
	documentManager                 *DocumentManager
	fileScanner                     *indexer.FileScanner
	workspaceFactory                WorkspaceFactory
	workspace                       WorkspaceRuntime
	lifecycleCtx                    context.Context
	lifecycleCancel                 context.CancelFunc
	lifecycleWG                     sync.WaitGroup
	backgroundMu                    sync.Mutex
	closing                         bool
	closeOnce                       sync.Once
	closeErr                        error
	diagnosticsMu                   sync.Mutex
	diagnosticsPublishMu            sync.Mutex
	diagnosticsJobs                 map[string]*diagnosticsJob
	diagnosticsGenerations          map[string]uint64
	diagnosticsCache                map[string]diagnosticsCacheEntry
	configurationMu                 sync.RWMutex
	projectConfiguration            projectconfig.Partial
	scopedConfigurations            []projectconfig.Scope
	editorConfiguration             projectconfig.Partial
	effectiveConfiguration          projectconfig.Effective
	configurationErr                error
	configurationIssues             []ConfigurationIssue
	pendingConfigurationFingerprint string
}

func (s *Server) InitializationOptions() protocol.InitializationOptions {
	configuration := s.EffectiveConfiguration()
	return protocol.InitializationOptions{
		PHPExtensions:         append([]string(nil), configuration.PHP.Extensions...),
		DisabledPHPExtensions: append([]string(nil), configuration.PHP.DisabledExtensions...),
		ShopwareTargetVersion: configuration.Shopware.TargetVersion,
		CLIMode:               s.initializationOptions.CLIMode,
	}
}

func (s *Server) connection() *jsonrpc2.Conn {
	s.connMu.RLock()
	defer s.connMu.RUnlock()
	return s.conn
}

func (s *Server) setConnection(conn *jsonrpc2.Conn) {
	s.connMu.Lock()
	s.conn = conn
	s.connMu.Unlock()
}

type diagnosticsJob struct {
	cancel context.CancelFunc
	done   <-chan struct{}
}

const diagnosticsDebounce = 150 * time.Millisecond

// NewServer creates a new LSP server
func NewServer(filescanner *indexer.FileScanner, rootPath, version string) *Server {
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	s := &Server{
		completionProviders:        make([]CompletionProvider, 0),
		definitionProviders:        make([]GotoDefinitionProvider, 0),
		implementationProviders:    make([]ImplementationProvider, 0),
		typeHierarchyProviders:     make([]TypeHierarchyProvider, 0),
		callHierarchyProviders:     make([]CallHierarchyProvider, 0),
		referencesProviders:        make([]ReferencesProvider, 0),
		codeLensProviders:          make([]CodeLensProvider, 0),
		actionProviders:            make([]ActionProvider, 0),
		inspections:                newInspectionRegistry(),
		hoverProviders:             make([]HoverProvider, 0),
		signatureProviders:         make([]SignatureHelpProvider, 0),
		renameProviders:            make([]RenameProvider, 0),
		inlayHintProviders:         make([]InlayHintProvider, 0),
		documentLinkProviders:      make([]DocumentLinkProvider, 0),
		documentSymbolProviders:    make([]DocumentSymbolProvider, 0),
		documentHighlightProviders: make([]DocumentHighlightProvider, 0),
		linkedEditingProviders:     make([]LinkedEditingRangeProvider, 0),
		foldingRangeProviders:      make([]FoldingRangeProvider, 0),
		selectionRangeProviders:    make([]SelectionRangeProvider, 0),
		documentColorProviders:     make([]DocumentColorProvider, 0),
		semanticTokensProviders:    make([]SemanticTokensProvider, 0),
		fileRenameProviders:        make([]FileRenameProvider, 0),
		workspaceSymbolProviders:   make([]WorkspaceSymbolProvider, 0),
		commandProviders:           make([]CommandProvider, 0),
		commandMap:                 make(map[string]CommandFunc),
		contextEnrichers:           make(map[language.ID]ContextEnricher),
		documentManager:            NewDocumentManager(),
		fileScanner:                filescanner,
		rootPath:                   rootPath,
		version:                    version,
		lifecycleCtx:               lifecycleCtx,
		lifecycleCancel:            lifecycleCancel,
		diagnosticsJobs:            make(map[string]*diagnosticsJob),
		diagnosticsGenerations:     make(map[string]uint64),
		diagnosticsCache:           make(map[string]diagnosticsCacheEntry),
		effectiveConfiguration:     projectconfig.Default(),
	}

	if s.fileScanner != nil {
		s.fileScanner.SetOnUpdate(func() {
			log.Printf("Publishing diagnostics to all open files")
			s.PublishDiagnostics(s.lifecycleCtx, nil)
		})
	}

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

func (s *Server) RegisterImplementationProvider(provider ImplementationProvider) {
	if provider != nil {
		s.implementationProviders = append(s.implementationProviders, provider)
	}
}

func (s *Server) RegisterTypeHierarchyProvider(provider TypeHierarchyProvider) {
	if provider != nil {
		s.typeHierarchyProviders = append(s.typeHierarchyProviders, provider)
	}
}

func (s *Server) RegisterCallHierarchyProvider(provider CallHierarchyProvider) {
	if provider != nil {
		s.callHierarchyProviders = append(s.callHierarchyProviders, provider)
	}
}

func (s *Server) RegisterLinkedEditingRangeProvider(
	provider LinkedEditingRangeProvider,
) {
	if provider != nil {
		s.linkedEditingProviders = append(s.linkedEditingProviders, provider)
	}
}

func (s *Server) RegisterFoldingRangeProvider(provider FoldingRangeProvider) {
	if provider != nil {
		s.foldingRangeProviders = append(s.foldingRangeProviders, provider)
	}
}

func (s *Server) RegisterSelectionRangeProvider(provider SelectionRangeProvider) {
	if provider != nil {
		s.selectionRangeProviders = append(s.selectionRangeProviders, provider)
	}
}

func (s *Server) RegisterDocumentColorProvider(provider DocumentColorProvider) {
	if provider != nil {
		s.documentColorProviders = append(s.documentColorProviders, provider)
	}
}

// RegisterReferencesProvider registers a references provider with the server
func (s *Server) RegisterReferencesProvider(provider ReferencesProvider) {
	s.referencesProviders = append(s.referencesProviders, provider)
}

// RegisterCodeLensProvider registers a code lens provider with the server
func (s *Server) RegisterCodeLensProvider(provider CodeLensProvider) {
	s.codeLensProviders = append(s.codeLensProviders, provider)
}

// RegisterActionProvider registers a code action provider with the server
func (s *Server) RegisterActionProvider(provider ActionProvider) {
	s.actionProviders = append(s.actionProviders, provider)
}

// RegisterHoverProvider registers a hover provider with the server
func (s *Server) RegisterHoverProvider(provider HoverProvider) {
	s.hoverProviders = append(s.hoverProviders, provider)
}

func (s *Server) RegisterSignatureHelpProvider(provider SignatureHelpProvider) {
	s.signatureProviders = append(s.signatureProviders, provider)
}

func (s *Server) RegisterRenameProvider(provider RenameProvider) {
	s.renameProviders = append(s.renameProviders, provider)
}

func (s *Server) RegisterInlayHintProvider(provider InlayHintProvider) {
	if provider != nil {
		s.inlayHintProviders = append(s.inlayHintProviders, provider)
	}
}

func (s *Server) RegisterDocumentLinkProvider(provider DocumentLinkProvider) {
	if provider != nil {
		s.documentLinkProviders = append(s.documentLinkProviders, provider)
	}
}

func (s *Server) RegisterDocumentSymbolProvider(provider DocumentSymbolProvider) {
	if provider != nil {
		s.documentSymbolProviders = append(s.documentSymbolProviders, provider)
	}
}

func (s *Server) RegisterDocumentHighlightProvider(
	provider DocumentHighlightProvider,
) {
	if provider != nil {
		s.documentHighlightProviders = append(
			s.documentHighlightProviders, provider,
		)
	}
}

func (s *Server) RegisterSemanticTokensProvider(
	provider SemanticTokensProvider,
) {
	if provider != nil {
		s.semanticTokensProviders = append(
			s.semanticTokensProviders,
			provider,
		)
	}
}

func (s *Server) RegisterFileRenameProvider(provider FileRenameProvider) {
	if provider != nil {
		s.fileRenameProviders = append(s.fileRenameProviders, provider)
	}
}

// RegisterCommandProvider registers a command provider with the server
func (s *Server) RegisterCommandProvider(provider CommandProvider) {
	s.commandProviders = append(s.commandProviders, provider)
	for command, fn := range provider.GetCommands(s.lifecycleCtx) {
		s.commandMap[command] = fn
	}
}

func (s *Server) registerCommands() {
	for _, provider := range s.commandProviders {
		for command, fn := range provider.GetCommands(s.lifecycleCtx) {
			s.commandMap[command] = fn
		}
	}
}

func (s *Server) startBackground(work func(context.Context)) bool {
	s.backgroundMu.Lock()
	defer s.backgroundMu.Unlock()
	if s.closing {
		return false
	}
	ctx := s.lifecycleCtx
	if ctx == nil {
		ctx = context.Background()
	}
	s.lifecycleWG.Add(1)
	go func() {
		defer s.lifecycleWG.Done()
		work(ctx)
	}()
	return true
}

// RegisterContextEnricher adds optional language-specific semantic context
// without exposing domain indexes through the protocol server.
func (s *Server) RegisterContextEnricher(languageID language.ID, enricher ContextEnricher) {
	if enricher != nil {
		s.contextEnrichers[languageID] = enricher
	}
}

// RegisterDocumentObserver exposes the open-document lifecycle to workspace
// composition without leaking the document manager itself to feature
// providers. Observers receive immutable snapshots and are replayed for any
// documents which are already open.
func (s *Server) RegisterDocumentObserver(observer DocumentObserver) {
	if s.documentManager != nil {
		s.documentManager.RegisterObserver(observer)
	}
}

// indexAll builds or updates all registered indexes
// If forceReindex is true, it will clear the existing index before rebuilding
func (s *Server) indexAll(ctx context.Context, forceReindex bool) error {
	if s.fileScanner == nil {
		return nil
	}
	startTime := time.Now()

	// Send notification that indexing has started
	if conn := s.connection(); conn != nil {
		if err := conn.Notify(ctx, "shopware/indexingStarted", map[string]interface{}{
			"message": "Indexing started",
		}); err != nil {
			return err
		}
	}

	if forceReindex {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.fileScanner.ClearHashes(); err != nil {
			return err
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.fileScanner.IndexAll(ctx); err != nil {
		return err
	}

	elapsedTime := time.Since(startTime)

	// Send notification that indexing has completed
	if conn := s.connection(); conn != nil {
		if err := conn.Notify(ctx, "shopware/indexingCompleted", map[string]interface{}{
			"message":       "Indexing completed",
			"timeInSeconds": elapsedTime.Seconds(),
		}); err != nil {
			return err
		}
	}

	return nil
}

// CloseAll closes all registered indexers and resources
func (s *Server) CloseAll() error {
	s.closeOnce.Do(func() {
		s.backgroundMu.Lock()
		s.closing = true
		if s.lifecycleCancel != nil {
			s.lifecycleCancel()
		}
		s.backgroundMu.Unlock()
		s.cancelAllDiagnostics()
		s.lifecycleWG.Wait()
		if s.documentManager != nil {
			s.documentManager.Close()
		}
		if s.workspace != nil {
			s.closeErr = s.workspace.Close()
		} else if s.fileScanner != nil {
			s.closeErr = s.fileScanner.Close()
		}
	})
	return s.closeErr
}

func (s *Server) Start(in io.Reader, out io.Writer) error {
	s.registerCommands()

	// Create a new JSON-RPC connection
	stream := jsonrpc2.NewBufferedStream(rwc{in, out}, jsonrpc2.VSCodeObjectCodec{})
	ordered := jsonrpc2.HandlerWithError(s.handle)
	conn := jsonrpc2.NewConn(
		context.Background(),
		stream,
		&cliDiagnosticHandler{
			server:  s,
			ordered: ordered,
		},
	)
	s.setConnection(conn)

	// Wait for the connection to close
	<-conn.DisconnectNotify()
	s.setConnection(nil)

	return s.CloseAll()
}

// cliDiagnosticHandler keeps normal LSP request ordering intact while letting
// the CLI issue a bounded set of independent pull-diagnostic requests. Each
// didOpen notification is still handled synchronously before its diagnostic
// request, and the CLI sends didClose only after that request completes.
type cliDiagnosticHandler struct {
	server  *Server
	ordered jsonrpc2.Handler
}

func (handler *cliDiagnosticHandler) Handle(
	ctx context.Context,
	conn *jsonrpc2.Conn,
	request *jsonrpc2.Request,
) {
	if request.Method == "textDocument/diagnostic" &&
		handler.server.initializationOptions.CLIMode {
		if handler.server.startBackground(func(context.Context) {
			handler.ordered.Handle(ctx, conn, request)
		}) {
			return
		}
	}
	handler.ordered.Handle(ctx, conn, request)
}

// rwc combines a reader and writer into a single ReadWriteCloser
type rwc struct {
	io.Reader
	io.Writer
}

// Close implements io.Closer
func (rwc) Close() error {
	return nil
}

// handle processes incoming JSON-RPC requests and notifications
func (s *Server) handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (interface{}, error) {
	s.setConnection(conn)
	// Handle exit notification after shutdown
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

	if cmd, ok := s.commandMap[req.Method]; ok {
		if !s.featureEnabled("commands") && !isConfigurationMethod(req.Method) {
			return nil, &jsonrpc2.Error{
				Code: jsonrpc2.CodeMethodNotFound, Message: "Shopware commands are disabled by configuration",
			}
		}
		return cmd(ctx, req.Params)
	}

	switch req.Method {
	case "initialize":
		var params protocol.InitializeParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeParseError, Message: err.Error()}
		}
		result, err := s.initialize(ctx, &params)
		if err != nil {
			return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeInvalidParams, Message: err.Error()}
		}
		return result, nil

	case "initialized":
		if configurationErr := s.configurationError(); configurationErr != nil {
			if conn := s.connection(); conn != nil {
				_ = conn.Notify(ctx, "window/showMessage", map[string]interface{}{
					"type":    1,
					"message": "Invalid Shopware LSP configuration: " + configurationErr.Error(),
				})
			}
		}
		if s.fileScanner == nil || !s.EffectiveConfiguration().Indexing.Enabled {
			if s.initializationOptions.CLIMode {
				if conn := s.connection(); conn != nil {
					_ = conn.Notify(ctx, "shopware/indexingCompleted", map[string]interface{}{
						"message": "Indexing disabled", "timeInSeconds": 0,
					})
				}
			}
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

			if ctx.Err() != nil {
				return
			}
			if s.initializationOptions.CLIMode {
				return
			}
			if err := s.fileScanner.StartWatcher(); err != nil {
				log.Printf("Error starting file watcher: %v", err)
			} else {
				log.Println("File watcher started successfully")
			}
		})
		return nil, nil

	case "textDocument/didOpen":
		var params struct {
			TextDocument struct {
				URI     string `json:"uri"`
				Text    string `json:"text"`
				Version int    `json:"version"`
			} `json:"textDocument"`
		}
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		s.documentManager.OpenDocument(params.TextDocument.URI, params.TextDocument.Text, params.TextDocument.Version)

		// Run diagnostics on the opened document
		if !s.initializationOptions.CLIMode {
			s.scheduleDiagnostics(params.TextDocument.URI, params.TextDocument.Version, 0)
		}
		return nil, nil

	case "textDocument/didChange":
		var params struct {
			TextDocument struct {
				URI     string `json:"uri"`
				Version int    `json:"version"`
			} `json:"textDocument"`
			ContentChanges []struct {
				Text string `json:"text"`
			} `json:"contentChanges"`
		}
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		if len(params.ContentChanges) > 0 {
			s.documentManager.UpdateDocument(params.TextDocument.URI, params.ContentChanges[0].Text, params.TextDocument.Version)

			if !s.initializationOptions.CLIMode {
				s.scheduleDiagnostics(params.TextDocument.URI, params.TextDocument.Version, diagnosticsDebounce)
			}
		}
		return nil, nil

	case "textDocument/didClose":
		var params struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		s.cancelDiagnostics(params.TextDocument.URI)
		s.documentManager.CloseDocument(params.TextDocument.URI)
		s.clearPublishedDiagnostics(ctx, params.TextDocument.URI)
		return nil, nil

	case "textDocument/completion":
		var params protocol.CompletionParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}

		return s.completion(ctx, &params), nil

	case "textDocument/definition":
		var params protocol.DefinitionParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.definition(ctx, &params), nil

	case "textDocument/implementation":
		var params protocol.ImplementationParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.implementation(ctx, &params), nil

	case "textDocument/prepareTypeHierarchy":
		var params protocol.PrepareTypeHierarchyParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.prepareTypeHierarchy(ctx, &params), nil

	case "typeHierarchy/supertypes":
		var params protocol.TypeHierarchySupertypesParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.typeHierarchySupertypes(ctx, params.Item), nil

	case "typeHierarchy/subtypes":
		var params protocol.TypeHierarchySubtypesParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.typeHierarchySubtypes(ctx, params.Item), nil

	case "textDocument/prepareCallHierarchy":
		var params protocol.CallHierarchyPrepareParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.prepareCallHierarchy(ctx, &params)

	case "callHierarchy/incomingCalls":
		var params protocol.CallHierarchyIncomingCallsParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.callHierarchyIncomingCalls(ctx, params.Item)

	case "callHierarchy/outgoingCalls":
		var params protocol.CallHierarchyOutgoingCallsParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.callHierarchyOutgoingCalls(ctx, params.Item)

	case "textDocument/references":
		var params protocol.ReferenceParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.references(ctx, &params)

	case "textDocument/codeLens":
		var params protocol.CodeLensParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.codeLens(ctx, &params)

	case "textDocument/hover":
		var params protocol.HoverParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.hover(ctx, &params)

	case "textDocument/signatureHelp":
		var params protocol.SignatureHelpParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.signatureHelp(ctx, &params)

	case "textDocument/rename":
		var params protocol.RenameParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.rename(ctx, &params)

	case "textDocument/inlayHint":
		var params protocol.InlayHintParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.inlayHints(ctx, &params)

	case "textDocument/documentLink":
		var params protocol.DocumentLinkParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.documentLinks(ctx, &params)

	case "textDocument/documentSymbol":
		var params protocol.DocumentSymbolParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.documentSymbols(ctx, &params)

	case "textDocument/documentHighlight":
		var params protocol.DocumentHighlightParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.documentHighlights(ctx, &params)

	case "textDocument/linkedEditingRange":
		var params protocol.LinkedEditingRangeParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.linkedEditingRanges(ctx, &params)

	case "textDocument/foldingRange":
		var params protocol.FoldingRangeParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.foldingRanges(ctx, &params)

	case "textDocument/selectionRange":
		var params protocol.SelectionRangeParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.selectionRanges(ctx, &params)

	case "textDocument/documentColor":
		var params protocol.DocumentColorParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.documentColors(ctx, &params)

	case "textDocument/colorPresentation":
		var params protocol.ColorPresentationParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.colorPresentations(ctx, &params)

	case "textDocument/semanticTokens/full":
		var params protocol.SemanticTokensParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.semanticTokens(ctx, &params)

	case "textDocument/diagnostic":
		var params protocol.DiagnosticParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.diagnostic(ctx, &params), nil

	case "shopware/configuration/catalog":
		return s.configurationCatalog(), nil

	case "shopware/configuration/effective":
		return s.EffectiveConfiguration(), nil

	case "shopware/configuration/reload":
		return s.reloadProjectConfiguration(ctx), nil

	case "workspace/didChangeConfiguration":
		var params struct {
			Settings projectconfig.Partial `json:"settings"`
		}
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		result := s.replaceEditorConfiguration(ctx, params.Settings)
		if result.Error != "" {
			log.Printf("Invalid editor configuration: %s", result.Error)
			if conn := s.connection(); conn != nil {
				_ = conn.Notify(ctx, "window/showMessage", map[string]interface{}{
					"type":    1,
					"message": "Invalid Shopware LSP configuration: " + result.Error,
				})
			}
		}
		return nil, nil

	case "codeLens/resolve":
		var codeLens protocol.CodeLens
		if err := json.Unmarshal(*req.Params, &codeLens); err != nil {
			return nil, err
		}
		return s.resolveCodeLens(ctx, &codeLens)

	case "textDocument/codeAction":
		var params protocol.CodeActionParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.codeAction(ctx, &params), nil

	case "codeAction/resolve":
		var action protocol.CodeAction
		if err := json.Unmarshal(*req.Params, &action); err != nil {
			return nil, err
		}
		return s.resolveCodeAction(ctx, action), nil

	case "shopware/forceReindex":
		if !s.EffectiveConfiguration().Indexing.Enabled {
			return nil, fmt.Errorf("indexing is disabled by configuration")
		}
		s.startBackground(func(ctx context.Context) {
			if err := s.indexAll(ctx, true); err != nil {
				log.Printf("Error force reindexing: %v", err)
				s.notifyIndexingFailed(ctx, err)
			}
		})
		return map[string]interface{}{
			"message": "Force reindexing started",
		}, nil

	case "shopware/index/stats":
		if s.fileScanner == nil {
			return nil, fmt.Errorf("workspace is not initialized")
		}
		return s.fileScanner.Stats(ctx)

	case "shopware/commands":
		commands := make([]string, 0, len(s.commandMap))
		for command := range s.commandMap {
			commands = append(commands, command)
		}
		sort.Strings(commands)
		return commands, nil

	case "workspace/willRenameFiles":
		var params protocol.RenameFilesParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.willRenameFiles(ctx, &params)

	case "workspace/symbol":
		var params protocol.WorkspaceSymbolParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
		return s.workspaceSymbols(ctx, &params)

	case "shutdown":
		// Clean up resources
		if err := s.CloseAll(); err != nil {
			log.Printf("Error closing indexers: %v", err)
		}

		log.Println("Received shutdown request, waiting for exit notification")
		return nil, nil

	case "workspace/didCreateFiles",
		"workspace/didRenameFiles",
		"workspace/didDeleteFiles",
		"workspace/didChangeWatchedFiles":
		// fsnotify is the single source of index file events. Accepting client
		// notifications as well would process the same change twice.
		return nil, nil

	default:
		// Check if this is a notification (no ID)
		if req.ID == (jsonrpc2.ID{}) {
			// This is a notification, no response needed
			return nil, nil
		}
		return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeMethodNotFound, Message: "Method not implemented: " + req.Method}
	}
}
