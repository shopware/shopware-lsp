package lsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceRootUsesInitializeURI(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace with spaces")
	params := &protocol.InitializeParams{RootURI: uriutil.FileURI(root)}
	resolved, err := workspaceRoot(params)
	require.NoError(t, err)
	require.Equal(t, root, resolved)
}

func TestWorkspaceRootRejectsMissingAndMultipleRoots(t *testing.T) {
	_, err := workspaceRoot(&protocol.InitializeParams{})
	require.ErrorContains(t, err, "workspace root is required")

	_, err = workspaceRoot(&protocol.InitializeParams{
		WorkspaceFolders: []protocol.WorkspaceFolder{
			{URI: "file:///first"},
			{URI: "file:///second"},
		},
	})
	require.ErrorContains(t, err, "multiple workspace folders")
}

func TestInitializeRejectsUnsupportedProjectWhenDetectionIsRequired(t *testing.T) {
	root := t.TempDir()
	server := NewServer(nil, "", "test")
	server.ConfigureProjectDetection(true, false)

	_, err := server.initialize(context.Background(), &protocol.InitializeParams{
		RootURI: uriutil.FileURI(root),
	})
	require.ErrorContains(t, err, "unsupported project root")
	require.False(t, server.initialized)
}

func TestInitializeAcceptsConfiguredOrExplicitlyAllowedProject(t *testing.T) {
	configured := t.TempDir()
	require.NoError(t, os.MkdirAll(
		filepath.Join(configured, ".config", "shopware"), 0o755,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(configured, ".config", "shopware", "lsp.json"),
		[]byte(`{"version":1}`), 0o644,
	))
	server := NewServer(nil, "", "test")
	server.ConfigureProjectDetection(true, false)
	_, err := server.initialize(context.Background(), &protocol.InitializeParams{
		RootURI: uriutil.FileURI(configured),
	})
	require.NoError(t, err)
	require.NoError(t, server.CloseAll())

	unsupported := t.TempDir()
	server = NewServer(nil, "", "test")
	server.ConfigureProjectDetection(true, false)
	_, err = server.initialize(context.Background(), &protocol.InitializeParams{
		RootURI: uriutil.FileURI(unsupported),
		InitializationOptions: protocol.InitializationOptions{
			AllowUnsupportedProject: true,
		},
	})
	require.NoError(t, err)
	require.NoError(t, server.CloseAll())
}

func TestInitializePropagatesWorkspaceConstructionFailure(t *testing.T) {
	expected := errors.New("open repository")
	server := NewServer(nil, "", "test")
	server.SetWorkspaceFactory(func(context.Context, string, *Server) (WorkspaceRuntime, error) {
		return nil, expected
	})

	_, err := server.initialize(context.Background(), &protocol.InitializeParams{RootURI: "file:///workspace"})
	require.ErrorIs(t, err, expected)
	require.NoError(t, server.CloseAll())
}

func TestInitializeRejectsSecondInitialization(t *testing.T) {
	server := NewServer(nil, "", "test")
	_, err := server.initialize(
		context.Background(),
		&protocol.InitializeParams{RootURI: "file:///workspace"},
	)
	require.NoError(t, err)
	_, err = server.initialize(
		context.Background(),
		&protocol.InitializeParams{RootURI: "file:///other"},
	)
	require.ErrorContains(t, err, "already initialized")
	require.NoError(t, server.CloseAll())
}

func TestInitializePublishesPHPStubExtensionOptionsToWorkspace(t *testing.T) {
	server := NewServer(nil, "", "test")
	var received protocol.InitializationOptions
	server.SetWorkspaceFactory(func(
		_ context.Context,
		root string,
		current *Server,
	) (WorkspaceRuntime, error) {
		received = current.InitializationOptions()
		return nil, errors.New("stop after options")
	})
	_, err := server.initialize(context.Background(), &protocol.InitializeParams{
		RootURI: "file:///workspace",
		InitializationOptions: protocol.InitializationOptions{
			PHPExtensions:         []string{"redis"},
			DisabledPHPExtensions: []string{"imagick"},
			ShopwareTargetVersion: "6.8.0",
		},
	})
	require.ErrorContains(t, err, "stop after options")
	require.Equal(t, []string{"redis"}, received.PHPExtensions)
	require.Equal(t, []string{"imagick"}, received.DisabledPHPExtensions)
	require.Equal(t, "6.8.0", received.ShopwareTargetVersion)
}

func TestInitializeAdvertisesTwigFileRenameEdits(t *testing.T) {
	server := NewServer(nil, "", "test")
	result, err := server.initialize(
		context.Background(),
		&protocol.InitializeParams{RootURI: "file:///workspace"},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })

	payload, ok := result.(map[string]interface{})
	require.True(t, ok)
	capabilities, ok := payload["capabilities"].(map[string]interface{})
	require.True(t, ok)
	workspace, ok := capabilities["workspace"].(map[string]interface{})
	require.True(t, ok)
	operations, ok := workspace["fileOperations"].(protocol.FileOperationOptions)
	require.True(t, ok)
	require.NotNil(t, operations.WillRename)
	require.Equal(t, "**/*.twig", operations.WillRename.Filters[0].Pattern.Glob)
	require.Equal(t, "file", operations.WillRename.Filters[0].Pattern.Matches)
	require.Equal(t, true, capabilities["workspaceSymbolProvider"])
	require.Equal(t, true, capabilities["documentSymbolProvider"])
	require.Equal(t, true, capabilities["documentHighlightProvider"])
	require.Equal(t, true, capabilities["linkedEditingRangeProvider"])
	require.Equal(t, true, capabilities["foldingRangeProvider"])
	require.Equal(t, true, capabilities["selectionRangeProvider"])
	require.Equal(t, true, capabilities["colorProvider"])
	require.Equal(t, true, capabilities["inlayHintProvider"])
	require.Equal(t, true, capabilities["implementationProvider"])
	require.Equal(t, true, capabilities["typeHierarchyProvider"])
	require.Equal(t, true, capabilities["callHierarchyProvider"])
	documentLinks, ok := capabilities["documentLinkProvider"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, false, documentLinks["resolveProvider"])
	semanticTokens, ok := capabilities["semanticTokensProvider"].(map[string]interface{})
	require.True(t, ok)
	legend, ok := semanticTokens["legend"].(protocol.SemanticTokensLegend)
	require.True(t, ok)
	require.Equal(t, protocol.SemanticTokenTypes, legend.TokenTypes)
	require.Empty(t, legend.TokenModifiers)
	require.Equal(t, true, semanticTokens["full"])
	require.Equal(t, false, semanticTokens["range"])
}

func TestInitializeNegotiatesCodeActionResolution(t *testing.T) {
	server := NewServer(nil, "", "test")
	server.RegisterInspection(testInspection{})
	params := &protocol.InitializeParams{RootURI: "file:///workspace"}
	params.Capabilities.TextDocument.CodeAction = &protocol.CodeActionClientCapabilities{
		DataSupport: true,
		ResolveSupport: protocol.CodeActionResolveSupport{
			Properties: []string{"edit"},
		},
	}
	result, err := server.initialize(context.Background(), params)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })

	payload := result.(map[string]interface{})
	capabilities := payload["capabilities"].(map[string]interface{})
	codeActions := capabilities["codeActionProvider"].(map[string]interface{})
	require.Equal(t, true, codeActions["resolveProvider"])
	require.Contains(
		t,
		codeActions["codeActionKinds"].([]protocol.CodeActionKind),
		protocol.CodeActionQuickFix,
	)
}

func TestServerClosesWorkspaceExactlyOnce(t *testing.T) {
	root := t.TempDir()
	scanner, err := indexer.NewFileScanner(root, filepath.Join(root, "scanner.db"))
	require.NoError(t, err)
	runtime := &testWorkspaceRuntime{root: root, scanner: scanner}

	server := NewServer(nil, "", "test")
	server.SetWorkspaceFactory(func(context.Context, string, *Server) (WorkspaceRuntime, error) {
		return runtime, nil
	})
	_, err = server.initialize(context.Background(), &protocol.InitializeParams{RootURI: uriutil.FileURI(root)})
	require.NoError(t, err)

	require.NoError(t, server.CloseAll())
	require.NoError(t, server.CloseAll())
	require.EqualValues(t, 1, runtime.closeCalls.Load())
}

func TestServerCloseCancelsAndWaitsForBackgroundWork(t *testing.T) {
	server := NewServer(nil, "", "test")
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	require.True(t, server.startBackground(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
	}))
	<-started

	closed := make(chan error, 1)
	go func() { closed <- server.CloseAll() }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("background context was not canceled")
	}
	select {
	case <-closed:
		t.Fatal("CloseAll returned before background work stopped")
	default:
	}
	close(release)
	require.NoError(t, <-closed)
	require.False(t, server.startBackground(func(context.Context) {}))
}

type testWorkspaceRuntime struct {
	root       string
	scanner    *indexer.FileScanner
	closeCalls atomic.Int64
}

func (r *testWorkspaceRuntime) Root() string                  { return r.root }
func (r *testWorkspaceRuntime) Scanner() *indexer.FileScanner { return r.scanner }
func (r *testWorkspaceRuntime) InitialForceReindex() bool     { return false }
func (r *testWorkspaceRuntime) Close() error {
	r.closeCalls.Add(1)
	return r.scanner.Close()
}
