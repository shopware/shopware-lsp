package lsp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"slices"
	"sort"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/projectdetect"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// WorkspaceRuntime owns the resources created for one initialized workspace.
// Shopware LSP currently supports one workspace root per server process.
type WorkspaceRuntime interface {
	Root() string
	Scanner() *indexer.FileScanner
	InitialForceReindex() bool
	Close() error
}

type WorkspaceFactory func(context.Context, string, *Server) (WorkspaceRuntime, error)

func (s *Server) SetWorkspaceFactory(factory WorkspaceFactory) {
	s.workspaceFactory = factory
}

func (s *Server) IndexFiles(ctx context.Context, files []string) error {
	if s.fileScanner == nil {
		return fmt.Errorf("workspace is not initialized")
	}
	return s.fileScanner.IndexFiles(ctx, files)
}

func (s *Server) notifyIndexingFailed(ctx context.Context, cause error) {
	if cause == nil {
		return
	}
	if conn := s.connection(); conn != nil {
		if err := conn.Notify(ctx, "shopware/indexingFailed", map[string]string{
			"message": cause.Error(),
		}); err != nil && ctx.Err() == nil {
			log.Printf("Error publishing indexing failure: %v", err)
		}
	}
}

func (s *Server) initialize(ctx context.Context, params *protocol.InitializeParams) (interface{}, error) {
	s.initializeMu.Lock()
	defer s.initializeMu.Unlock()
	if s.initialized {
		return nil, fmt.Errorf("server is already initialized")
	}

	rootPath, err := workspaceRoot(params)
	if err != nil {
		return nil, err
	}
	s.rootPath = rootPath
	s.initializationOptions = params.InitializationOptions
	if s.projectDetectionRequired &&
		!s.allowUnsupportedProject &&
		!params.InitializationOptions.AllowUnsupportedProject {
		detection, detectErr := projectdetect.Detect(rootPath)
		if detectErr != nil {
			return nil, fmt.Errorf("detect project type: %w", detectErr)
		}
		if !detection.Supported {
			return nil, fmt.Errorf(
				"unsupported project root %s: no Shopware or Symfony project markers found; add .config/shopware-lsp/config.json or allow unsupported projects explicitly",
				rootPath,
			)
		}
	}
	scopeLoad := make(chan configurationScopeLoad, 1)
	go func() { scopeLoad <- loadConfigurationScopes(rootPath) }()
	if err := s.initializeConfiguration(rootPath, params.InitializationOptions); err != nil {
		return nil, fmt.Errorf("load configuration: %w", err)
	}
	s.codeActionResolveSupport = params.Capabilities.TextDocument.CodeAction != nil &&
		params.Capabilities.TextDocument.CodeAction.DataSupport &&
		slices.Contains(
			params.Capabilities.TextDocument.CodeAction.ResolveSupport.Properties,
			"edit",
		)

	if s.workspaceFactory != nil && s.workspace == nil {
		workspace, err := s.workspaceFactory(ctx, rootPath, s)
		if err != nil {
			return nil, fmt.Errorf("initialize workspace: %w", err)
		}
		if workspace == nil || workspace.Scanner() == nil {
			if workspace != nil {
				_ = workspace.Close()
			}
			return nil, fmt.Errorf("initialize workspace: factory returned no scanner")
		}
		s.workspace = workspace
		s.fileScanner = workspace.Scanner()
		s.fileScanner.SetOnUpdate(func() {
			log.Printf("Publishing diagnostics to all open files")
			s.PublishDiagnostics(s.lifecycleCtx, nil)
		})
	}
	if err := s.installInitialConfigurationScopes(<-scopeLoad); err != nil {
		if s.workspace != nil {
			_ = s.workspace.Close()
			s.workspace = nil
			s.fileScanner = nil
		}
		return nil, fmt.Errorf("load scoped configuration: %w", err)
	}
	if err := s.validateConfiguredDiagnosticIDs(); err != nil {
		if s.initializationOptions.CLIMode {
			if s.workspace != nil {
				_ = s.workspace.Close()
				s.workspace = nil
				s.fileScanner = nil
			}
			return nil, fmt.Errorf("validate configuration: %w", err)
		}
		s.configurationMu.Lock()
		s.configurationErr = errors.Join(s.configurationErr, err)
		s.configurationMu.Unlock()
		log.Printf("Invalid Shopware LSP configuration: %v", err)
	}
	s.initialized = true

	return map[string]interface{}{
		"serverInfo": map[string]interface{}{
			"name":    "shopware-lsp",
			"version": s.version,
		},
		"capabilities": map[string]interface{}{
			"textDocumentSync": map[string]interface{}{
				"openClose": true,
				"change":    1,
			},
			"diagnosticProvider": map[string]interface{}{
				"interFileDependencies": true,
				"workspaceDiagnostics":  false,
			},
			"completionProvider": map[string]interface{}{
				"triggerCharacters": s.collectTriggerCharacters(),
			},
			"definitionProvider":         true,
			"implementationProvider":     true,
			"typeHierarchyProvider":      true,
			"callHierarchyProvider":      true,
			"referencesProvider":         true,
			"renameProvider":             true,
			"workspaceSymbolProvider":    true,
			"documentSymbolProvider":     true,
			"documentHighlightProvider":  true,
			"linkedEditingRangeProvider": true,
			"foldingRangeProvider":       true,
			"selectionRangeProvider":     true,
			"colorProvider":              true,
			"workspace": map[string]interface{}{
				"fileOperations": protocol.FileOperationOptions{
					WillRename: &protocol.FileOperationRegistrationOptions{
						Filters: []protocol.FileOperationFilter{
							{
								Scheme: "file",
								Pattern: protocol.FileOperationPattern{
									Glob:    "**/*.twig",
									Matches: "file",
								},
							},
						},
					},
				},
			},
			"hoverProvider":     true,
			"inlayHintProvider": true,
			"documentLinkProvider": map[string]interface{}{
				"resolveProvider": false,
			},
			"semanticTokensProvider": map[string]interface{}{
				"legend": protocol.SemanticTokensLegend{
					TokenTypes: append(
						[]string(nil),
						protocol.SemanticTokenTypes...,
					),
					TokenModifiers: append(
						[]string(nil),
						protocol.SemanticTokenModifiers...,
					),
				},
				"full":  true,
				"range": false,
			},
			"signatureHelpProvider": map[string]interface{}{
				"triggerCharacters":   []string{"(", ","},
				"retriggerCharacters": []string{","},
			},
			"codeLensProvider": map[string]interface{}{
				"resolveProvider": true,
			},
			"codeActionProvider": map[string]interface{}{
				"codeActionKinds": s.collectCodeActionKinds(),
				"resolveProvider": s.codeActionResolveSupport,
			},
		},
	}, nil
}

func (s *Server) collectTriggerCharacters() []string {
	unique := make(map[string]struct{})
	for _, provider := range s.completionProviders {
		for _, character := range provider.GetTriggerCharacters() {
			unique[character] = struct{}{}
		}
	}
	characters := make([]string, 0, len(unique))
	for character := range unique {
		characters = append(characters, character)
	}
	return characters
}

func (s *Server) collectCodeActionKinds() []protocol.CodeActionKind {
	unique := make(map[protocol.CodeActionKind]struct{})
	for _, provider := range s.actionProviders {
		for _, kind := range provider.GetCodeActionKinds() {
			unique[kind] = struct{}{}
		}
	}
	if len(s.inspections.byID) != 0 {
		unique[protocol.CodeActionQuickFix] = struct{}{}
	}
	kinds := make([]protocol.CodeActionKind, 0, len(unique))
	for kind := range unique {
		kinds = append(kinds, kind)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return kinds
}

func workspaceRoot(params *protocol.InitializeParams) (string, error) {
	if len(params.WorkspaceFolders) > 1 {
		return "", fmt.Errorf("multiple workspace folders are not supported; start one Shopware LSP process per workspace")
	}

	var value string
	switch {
	case len(params.WorkspaceFolders) == 1:
		value = params.WorkspaceFolders[0].URI
	case params.RootURI != "":
		value = params.RootURI
	case params.RootPath != "":
		value = params.RootPath
	default:
		return "", fmt.Errorf("a workspace root is required")
	}

	path, err := uriutil.Path(value)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("make workspace root absolute: %w", err)
	}
	return filepath.Clean(absolute), nil
}
