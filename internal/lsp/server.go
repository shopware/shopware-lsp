package lsp

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
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
	clientProtocolVersion           int
	presentationProfile             string
	filterClientCommands            bool
	supportedClientCommands         map[string]struct{}
	workDoneProgress                bool
	progressSequence                atomic.Uint64
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
	documentFormattingProviders     []DocumentFormattingProvider
	selectionRangeProviders         []SelectionRangeProvider
	documentColorProviders          []DocumentColorProvider
	semanticTokensProviders         []SemanticTokensProvider
	fileRenameProviders             []FileRenameProvider
	workspaceSymbolProviders        []WorkspaceSymbolProvider
	commandProviders                []CommandProvider
	commandMap                      map[string]CommandFunc
	methodHandlers                  map[string]rpcMethodHandler
	contextEnrichers                map[language.ID]ContextEnricher
	documentManager                 *DocumentManager
	fileScanner                     *indexer.FileScanner
	workspaceFactory                WorkspaceFactory
	workspace                       WorkspaceRuntime
	projectDetectionRequired        bool
	allowUnsupportedProject         bool
	inactiveProject                 bool
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
	traceProviders                  bool
}

func (s *Server) InitializationOptions() protocol.InitializationOptions {
	configuration := s.EffectiveConfiguration()
	var shopwareClient *protocol.ShopwareClientOptions
	if s.initializationOptions.ShopwareClient != nil {
		current := *s.initializationOptions.ShopwareClient
		current.SupportedCommands = append(
			[]string(nil), current.SupportedCommands...,
		)
		shopwareClient = &current
	}
	return protocol.InitializationOptions{
		PHPExtensions:         append([]string(nil), configuration.PHP.Extensions...),
		DisabledPHPExtensions: append([]string(nil), configuration.PHP.DisabledExtensions...),
		ShopwareTargetVersion: configuration.Shopware.TargetVersion,
		AllowUnsupportedProject: s.initializationOptions.AllowUnsupportedProject ||
			s.allowUnsupportedProject,
		CLIMode:        s.initializationOptions.CLIMode,
		ShopwareClient: shopwareClient,
	}
}

func (s *Server) ConfigureProjectDetection(required, allowUnsupported bool) {
	s.projectDetectionRequired = required
	s.allowUnsupportedProject = allowUnsupported
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
		completionProviders:         make([]CompletionProvider, 0),
		definitionProviders:         make([]GotoDefinitionProvider, 0),
		implementationProviders:     make([]ImplementationProvider, 0),
		typeHierarchyProviders:      make([]TypeHierarchyProvider, 0),
		callHierarchyProviders:      make([]CallHierarchyProvider, 0),
		referencesProviders:         make([]ReferencesProvider, 0),
		codeLensProviders:           make([]CodeLensProvider, 0),
		actionProviders:             make([]ActionProvider, 0),
		inspections:                 newInspectionRegistry(),
		hoverProviders:              make([]HoverProvider, 0),
		signatureProviders:          make([]SignatureHelpProvider, 0),
		renameProviders:             make([]RenameProvider, 0),
		inlayHintProviders:          make([]InlayHintProvider, 0),
		documentLinkProviders:       make([]DocumentLinkProvider, 0),
		documentSymbolProviders:     make([]DocumentSymbolProvider, 0),
		documentHighlightProviders:  make([]DocumentHighlightProvider, 0),
		linkedEditingProviders:      make([]LinkedEditingRangeProvider, 0),
		foldingRangeProviders:       make([]FoldingRangeProvider, 0),
		documentFormattingProviders: make([]DocumentFormattingProvider, 0),
		selectionRangeProviders:     make([]SelectionRangeProvider, 0),
		documentColorProviders:      make([]DocumentColorProvider, 0),
		semanticTokensProviders:     make([]SemanticTokensProvider, 0),
		fileRenameProviders:         make([]FileRenameProvider, 0),
		workspaceSymbolProviders:    make([]WorkspaceSymbolProvider, 0),
		commandProviders:            make([]CommandProvider, 0),
		commandMap:                  make(map[string]CommandFunc),
		supportedClientCommands:     make(map[string]struct{}),
		contextEnrichers:            make(map[language.ID]ContextEnricher),
		documentManager:             NewDocumentManager(),
		fileScanner:                 filescanner,
		rootPath:                    rootPath,
		version:                     version,
		lifecycleCtx:                lifecycleCtx,
		lifecycleCancel:             lifecycleCancel,
		diagnosticsJobs:             make(map[string]*diagnosticsJob),
		diagnosticsGenerations:      make(map[string]uint64),
		diagnosticsCache:            make(map[string]diagnosticsCacheEntry),
		effectiveConfiguration:      projectconfig.Default(),
		traceProviders:              providerPerformanceTraceEnabled(),
	}
	s.methodHandlers = s.protocolMethodHandlers()

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

func (s *Server) RegisterDocumentFormattingProvider(
	provider DocumentFormattingProvider,
) {
	if provider != nil {
		s.documentFormattingProviders = append(
			s.documentFormattingProviders,
			provider,
		)
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
func (s *Server) indexAll(
	ctx context.Context,
	forceReindex bool,
) (returnErr error) {
	if s.fileScanner == nil {
		return nil
	}
	startTime := time.Now()
	finishProgress := s.beginIndexingProgress(ctx)
	defer func() {
		finishProgress(time.Since(startTime), returnErr)
	}()

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

func (s *Server) beginIndexingProgress(
	ctx context.Context,
) func(time.Duration, error) {
	if !s.workDoneProgress {
		return func(time.Duration, error) {}
	}
	conn := s.connection()
	if conn == nil {
		return func(time.Duration, error) {}
	}
	token := fmt.Sprintf(
		"shopware-index-%d", s.progressSequence.Add(1),
	)
	progressContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := conn.Call(
		progressContext,
		"window/workDoneProgress/create",
		map[string]interface{}{"token": token},
		nil,
	); err != nil {
		if ctx.Err() == nil {
			log.Printf("Create indexing progress: %v", err)
		}
		return func(time.Duration, error) {}
	}
	if err := conn.Notify(ctx, "$/progress", map[string]interface{}{
		"token": token,
		"value": map[string]interface{}{
			"kind": "begin", "title": "Shopware indexing",
			"message": "Indexing workspace", "cancellable": false,
		},
	}); err != nil {
		if ctx.Err() == nil {
			log.Printf("Publish indexing progress: %v", err)
		}
		return func(time.Duration, error) {}
	}
	return func(elapsed time.Duration, cause error) {
		message := fmt.Sprintf(
			"Indexing completed in %.2fs", elapsed.Seconds(),
		)
		if cause != nil {
			message = "Indexing failed: " + cause.Error()
		}
		if err := conn.Notify(ctx, "$/progress", map[string]interface{}{
			"token": token,
			"value": map[string]interface{}{
				"kind": "end", "message": message,
			},
		}); err != nil && ctx.Err() == nil {
			log.Printf("Complete indexing progress: %v", err)
		}
	}
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
