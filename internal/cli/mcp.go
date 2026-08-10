package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/projectconfig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	defaultMCPDiagnosticLimit = 200
	maximumMCPDiagnosticLimit = 1000
	defaultMCPSymbolLimit     = 50
	maximumMCPSymbolLimit     = 200
	mcpServerInstructions     = "Use these Shopware MCP tools instead of manually recreating supported behavior. When a user asks to create, generate, scaffold, or initialize a Shopware plugin or another supported Shopware/Symfony artifact, always use shopware_scaffold. For a plugin call it with family=\"shopware\" and kind=\"plugin\"; in a standard Shopware project directory is usually \"custom/plugins\". Set write=true when the user asked to create files; otherwise preview first. Use shopware_scaffold_catalog only when the kind or options are unclear. Analyze the configured Shopware workspace. Paths may be workspace-relative or absolute, and all input positions use one-based lines and columns. List a code action before applying it."
)

type mcpRuntime struct {
	runner              *Runner
	root                string
	editorConfiguration *projectconfig.Partial
	configuration       projectconfig.Effective

	operationMu sync.Mutex
	session     *cliSession
}

type mcpNopWriteCloser struct {
	io.Writer
}

func (mcpNopWriteCloser) Close() error { return nil }

func (r *Runner) runMCP(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return usageError("mcp takes no arguments; use -root before the command")
	}
	root, err := r.workspaceRoot()
	if err != nil {
		return err
	}
	if err := r.requireSupportedProject(root); err != nil {
		return err
	}
	editorConfiguration, err := mcpEditorConfiguration()
	if err != nil {
		return err
	}
	configuration, err := mcpConfiguration(root, editorConfiguration)
	if err != nil {
		return err
	}
	runtime := &mcpRuntime{
		runner: r, root: root, editorConfiguration: editorConfiguration,
		configuration: configuration,
	}
	defer func() {
		if closeErr := runtime.Close(); closeErr != nil && r.verbose {
			_ = writeFormatted(r.errOut, "Close MCP workspace: %v\n", closeErr)
		}
	}()
	server := newMCPServer(runtime)
	transport := &mcp.IOTransport{
		Reader: io.NopCloser(r.in),
		Writer: mcpNopWriteCloser{Writer: r.out},
	}
	if err := server.Run(ctx, transport); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("run stdio MCP server: %w", err)
	}
	return nil
}

func mcpConfiguration(
	root string,
	editor *projectconfig.Partial,
) (projectconfig.Effective, error) {
	project, _, err := projectconfig.Load(root)
	if err != nil {
		return projectconfig.Effective{}, err
	}
	editorValue := projectconfig.Partial{}
	if editor != nil {
		editorValue = *editor
	}
	return projectconfig.Resolve(project, editorValue), nil
}

func mcpEditorConfiguration() (*projectconfig.Partial, error) {
	source := strings.TrimSpace(os.Getenv("SHOPWARE_LSP_EDITOR_CONFIGURATION"))
	if source == "" {
		return nil, nil
	}
	var result projectconfig.Partial
	decoder := json.NewDecoder(strings.NewReader(source))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("decode MCP editor configuration: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return nil, fmt.Errorf("decode MCP editor configuration: %w", err)
	}
	if err := projectconfig.Validate(result); err != nil {
		return nil, fmt.Errorf("validate MCP editor configuration: %w", err)
	}
	return &result, nil
}

func newMCPServer(runtime *mcpRuntime) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    "shopware-lsp",
			Version: runtime.runner.options.Version,
		},
		&mcp.ServerOptions{
			Instructions: mcpServerInstructions,
			Capabilities: &mcp.ServerCapabilities{},
		},
	)
	readOnly := &mcp.ToolAnnotations{
		ReadOnlyHint: true, IdempotentHint: true,
		OpenWorldHint: boolPointer(false),
	}
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_diagnostics", Title: "Shopware diagnostics",
		Description: "Run the same configured Shopware LSP diagnostics used by the editor for one workspace file or directory.",
		Annotations: readOnly,
	}, runtime.diagnostics)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_code_actions", Title: "Shopware code actions",
		Description: "List available quick fixes and refactorings at a one-based file position without changing files.",
		Annotations: readOnly,
	}, runtime.codeActions)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_apply_code_action", Title: "Apply Shopware code action",
		Description: "Resolve and apply one exact code-action title at a one-based file position. This writes workspace files and returns the resulting diff.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: false, IdempotentHint: false,
			DestructiveHint: boolPointer(true), OpenWorldHint: boolPointer(false),
		},
	}, runtime.applyCodeAction)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_hover", Title: "Shopware hover",
		Description: "Return Shopware LSP hover information at a one-based file position.",
		Annotations: readOnly,
	}, runtime.hover)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_definition", Title: "Shopware definitions",
		Description: "Find definitions for the symbol at a one-based file position.",
		Annotations: readOnly,
	}, runtime.definition)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_references", Title: "Shopware references",
		Description: "Find references, including the declaration, for the symbol at a one-based file position.",
		Annotations: readOnly,
	}, runtime.references)
	addMCPTool(server, runtime, &mcp.Tool{
		Name: "shopware_workspace_symbols", Title: "Shopware workspace symbols",
		Description: "Search indexed Shopware workspace symbols such as PHP classes and members.",
		Annotations: readOnly,
	}, runtime.workspaceSymbols)
	registerMCPScaffoldTools(server, runtime, readOnly)
	return server
}

func addMCPTool[Input, Output any](
	server *mcp.Server,
	runtime *mcpRuntime,
	tool *mcp.Tool,
	handler func(
		context.Context,
		*mcp.CallToolRequest,
		Input,
	) (*mcp.CallToolResult, Output, error),
) {
	if runtime == nil || tool == nil || !runtime.toolEnabled(tool.Name) {
		return
	}
	mcp.AddTool(server, tool, handler)
}

func (runtime *mcpRuntime) toolEnabled(name string) bool {
	if runtime == nil {
		return false
	}
	return runtime.configuration.MCPToolEnabled(name)
}

func boolPointer(value bool) *bool { return &value }

func withMCPSession[T any](
	ctx context.Context,
	runtime *mcpRuntime,
	operation func(*cliSession) (T, error),
) (T, error) {
	var zero T
	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
	if runtime.session == nil {
		configuration := []projectconfig.Partial(nil)
		if runtime.editorConfiguration != nil {
			configuration = append(configuration, *runtime.editorConfiguration)
		}
		session, err := newCLISession(
			ctx, runtime.root, runtime.runner.options.Version,
			runtime.runner.errOut, true,
			runtime.runner.allowUnsupportedProject, configuration...,
		)
		if err != nil {
			return zero, err
		}
		result, err := session.waitForIndex(ctx)
		if err != nil {
			_ = session.Close()
			return zero, err
		}
		session.initialIndex = result
		runtime.session = session
	}
	return operation(runtime.session)
}

func (runtime *mcpRuntime) Close() error {
	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
	if runtime.session == nil {
		return nil
	}
	err := runtime.session.Close()
	runtime.session = nil
	return err
}

type diagnosticsInput struct {
	Path       string `json:"path" jsonschema:"workspace-relative or absolute path to a file or directory"`
	Severity   string `json:"severity,omitempty" jsonschema:"minimum reported severity: error, warning, info, or hint; defaults to project configuration"`
	MaxResults int    `json:"maxResults,omitempty" jsonschema:"maximum diagnostics returned; defaults to 200 and cannot exceed 1000"`
}

type positionInput struct {
	Path   string `json:"path" jsonschema:"workspace-relative or absolute file path"`
	Line   int    `json:"line" jsonschema:"one-based line number"`
	Column int    `json:"column,omitempty" jsonschema:"one-based UTF-16 column number; defaults to 1"`
}

type codeActionsInput struct {
	Path   string `json:"path" jsonschema:"workspace-relative or absolute file path"`
	Line   int    `json:"line" jsonschema:"one-based line number"`
	Column int    `json:"column,omitempty" jsonschema:"one-based UTF-16 column number; defaults to 1"`
	Kind   string `json:"kind,omitempty" jsonschema:"optional hierarchical code-action kind such as quickfix or source.fixAll"`
}

type applyCodeActionInput struct {
	Path   string `json:"path" jsonschema:"workspace-relative or absolute file path"`
	Line   int    `json:"line" jsonschema:"one-based line number"`
	Column int    `json:"column,omitempty" jsonschema:"one-based UTF-16 column number; defaults to 1"`
	Title  string `json:"title" jsonschema:"exact title returned by shopware_code_actions"`
	Kind   string `json:"kind,omitempty" jsonschema:"optional hierarchical code-action kind used to disambiguate the title"`
}

type workspaceSymbolsInput struct {
	Query string `json:"query" jsonschema:"symbol name query"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum symbols returned; defaults to 50 and cannot exceed 200"`
}

type mcpPosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type mcpRange struct {
	Start mcpPosition `json:"start"`
	End   mcpPosition `json:"end"`
}

type mcpLocation struct {
	Path  string   `json:"path"`
	Range mcpRange `json:"range"`
}

type mcpRelatedDiagnostic struct {
	Location mcpLocation `json:"location"`
	Message  string      `json:"message"`
}

type mcpDiagnostic struct {
	Path     string                 `json:"path"`
	Range    mcpRange               `json:"range"`
	Severity string                 `json:"severity"`
	Code     string                 `json:"code,omitempty"`
	Source   string                 `json:"source,omitempty"`
	Message  string                 `json:"message"`
	Tags     []string               `json:"tags,omitempty"`
	Related  []mcpRelatedDiagnostic `json:"related,omitempty"`
}

type mcpCodeAction struct {
	Title          string `json:"title"`
	Kind           string `json:"kind,omitempty"`
	Preferred      bool   `json:"preferred,omitempty"`
	DisabledReason string `json:"disabledReason,omitempty"`
	HasEdit        bool   `json:"hasEdit"`
	Resolvable     bool   `json:"resolvable"`
	Command        string `json:"command,omitempty"`
}

type diagnosticsOutput struct {
	Root         string          `json:"root"`
	Path         string          `json:"path"`
	FilesChecked int             `json:"filesChecked"`
	Severity     string          `json:"severity"`
	Total        int             `json:"total"`
	Diagnostics  []mcpDiagnostic `json:"diagnostics"`
	Truncated    bool            `json:"truncated"`
}

type codeActionsOutput struct {
	Path     string          `json:"path"`
	Position mcpPosition     `json:"position"`
	Actions  []mcpCodeAction `json:"actions"`
}

type applyCodeActionOutput struct {
	Applied bool     `json:"applied"`
	Title   string   `json:"title"`
	Files   []string `json:"files"`
	Diff    string   `json:"diff"`
}

type mcpHover struct {
	Kind  string    `json:"kind"`
	Value string    `json:"value"`
	Range *mcpRange `json:"range,omitempty"`
}

type hoverOutput struct {
	Path  string    `json:"path"`
	Hover *mcpHover `json:"hover"`
}

type locationsOutput struct {
	Path      string        `json:"path"`
	Position  mcpPosition   `json:"position"`
	Locations []mcpLocation `json:"locations"`
}

type mcpWorkspaceSymbol struct {
	Name      string      `json:"name"`
	Kind      string      `json:"kind"`
	Container string      `json:"container"`
	Location  mcpLocation `json:"location"`
}

type workspaceSymbolsOutput struct {
	Query     string               `json:"query"`
	Total     int                  `json:"total"`
	Symbols   []mcpWorkspaceSymbol `json:"symbols"`
	Truncated bool                 `json:"truncated"`
}

func (runtime *mcpRuntime) diagnostics(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input diagnosticsInput,
) (*mcp.CallToolResult, diagnosticsOutput, error) {
	target, err := runtime.resolvePath(input.Path)
	if err != nil {
		return nil, diagnosticsOutput{}, err
	}
	limit, err := boundedLimit(
		input.MaxResults, defaultMCPDiagnosticLimit, maximumMCPDiagnosticLimit,
		"maxResults",
	)
	if err != nil {
		return nil, diagnosticsOutput{}, err
	}
	output, err := withMCPSession(ctx, runtime, func(session *cliSession) (diagnosticsOutput, error) {
		severity := strings.TrimSpace(input.Severity)
		if severity == "" {
			severity = string(session.configuration.Effective.Check.Severity)
		}
		cutoff, err := diagnosticSeverity(severity)
		if err != nil {
			return diagnosticsOutput{}, err
		}
		paths, err := resolveCheckFiles(ctx, []string{target})
		if err != nil {
			return diagnosticsOutput{}, err
		}
		findings := make([]mcpDiagnostic, 0)
		total := 0
		for _, path := range paths {
			diagnostics, err := session.checkDocument(ctx, path, cutoff)
			if err != nil {
				return diagnosticsOutput{}, err
			}
			for _, finding := range diagnostics {
				total++
				if len(findings) < limit {
					findings = append(findings, runtime.convertDiagnostic(finding))
				}
			}
		}
		return diagnosticsOutput{
			Root: runtime.root, Path: runtime.outputPath(target),
			FilesChecked: len(paths), Severity: strings.ToLower(severity),
			Total: total, Diagnostics: findings, Truncated: total > len(findings),
		}, nil
	})
	return nil, output, err
}

func (runtime *mcpRuntime) codeActions(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input codeActionsInput,
) (*mcp.CallToolResult, codeActionsOutput, error) {
	path, position, err := runtime.position(input.Path, input.Line, input.Column)
	if err != nil {
		return nil, codeActionsOutput{}, err
	}
	output, err := withMCPSession(ctx, runtime, func(session *cliSession) (codeActionsOutput, error) {
		return withMCPCodeActions(ctx, session, path, position, input.Kind, func(
			_ *cliDocument,
			actions []protocol.CodeAction,
		) (codeActionsOutput, error) {
			result := make([]mcpCodeAction, 0, len(actions))
			for _, action := range actions {
				entry := mcpCodeAction{
					Title: action.Title, Kind: string(action.Kind),
					Preferred: action.IsPreferred, HasEdit: action.Edit != nil,
					Resolvable: action.Edit == nil && action.Data != nil,
				}
				if action.Disabled != nil {
					entry.DisabledReason = action.Disabled.Reason
				}
				if action.Command != nil {
					entry.Command = action.Command.Command
				}
				result = append(result, entry)
			}
			return codeActionsOutput{
				Path:     runtime.outputPath(path),
				Position: toMCPPosition(position), Actions: result,
			}, nil
		})
	})
	return nil, output, err
}

func (runtime *mcpRuntime) applyCodeAction(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input applyCodeActionInput,
) (*mcp.CallToolResult, applyCodeActionOutput, error) {
	if strings.TrimSpace(input.Title) == "" {
		return nil, applyCodeActionOutput{}, errors.New("title is required")
	}
	path, position, err := runtime.position(input.Path, input.Line, input.Column)
	if err != nil {
		return nil, applyCodeActionOutput{}, err
	}
	output, err := withMCPSession(ctx, runtime, func(session *cliSession) (applyCodeActionOutput, error) {
		return withMCPCodeActions(ctx, session, path, position, input.Kind, func(
			_ *cliDocument,
			actions []protocol.CodeAction,
		) (applyCodeActionOutput, error) {
			matches := make([]protocol.CodeAction, 0, 1)
			for _, action := range actions {
				if action.Title == input.Title {
					matches = append(matches, action)
				}
			}
			if len(matches) == 0 {
				return applyCodeActionOutput{}, fmt.Errorf("no code action with exact title %q", input.Title)
			}
			if len(matches) > 1 {
				return applyCodeActionOutput{}, fmt.Errorf("code action title %q is ambiguous; also provide kind", input.Title)
			}
			action := matches[0]
			if action.Disabled != nil {
				return applyCodeActionOutput{}, fmt.Errorf("code action %q is disabled: %s", action.Title, action.Disabled.Reason)
			}
			if action.Edit == nil && action.Data != nil {
				var resolved protocol.CodeAction
				if err := session.call(ctx, "codeAction/resolve", action, &resolved); err != nil {
					return applyCodeActionOutput{}, err
				}
				action = resolved
				if action.Disabled != nil {
					return applyCodeActionOutput{}, fmt.Errorf("code action %q is disabled: %s", action.Title, action.Disabled.Reason)
				}
			}
			if action.Edit == nil {
				if action.Command != nil {
					return applyCodeActionOutput{}, fmt.Errorf("code action %q requires unsupported editor command %q", action.Title, action.Command.Command)
				}
				return applyCodeActionOutput{}, fmt.Errorf("code action %q returned no edits", action.Title)
			}
			if err := runtime.validateWorkspaceEdit(action.Edit); err != nil {
				return applyCodeActionOutput{}, err
			}
			var diff bytes.Buffer
			if err := applyWorkspaceEdit(&diff, action.Edit, editMode{Write: true, Diff: true}); err != nil {
				return applyCodeActionOutput{}, err
			}
			return applyCodeActionOutput{
				Applied: true, Title: action.Title,
				Files: runtime.workspaceEditPaths(action.Edit), Diff: diff.String(),
			}, nil
		})
	})
	return nil, output, err
}

func (runtime *mcpRuntime) hover(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input positionInput,
) (*mcp.CallToolResult, hoverOutput, error) {
	path, position, err := runtime.position(input.Path, input.Line, input.Column)
	if err != nil {
		return nil, hoverOutput{}, err
	}
	output, err := withMCPSession(ctx, runtime, func(session *cliSession) (hoverOutput, error) {
		return withMCPDocument(ctx, session, path, func(document *cliDocument) (hoverOutput, error) {
			var hover *protocol.Hover
			if err := session.call(ctx, "textDocument/hover", positionParams(document.URI, position), &hover); err != nil {
				return hoverOutput{}, err
			}
			if hover == nil {
				return hoverOutput{Path: runtime.outputPath(path)}, nil
			}
			result := &mcpHover{
				Kind: string(hover.Contents.Kind), Value: hover.Contents.Value,
			}
			if hover.Range != nil {
				converted := toMCPRange(*hover.Range)
				result.Range = &converted
			}
			return hoverOutput{Path: runtime.outputPath(path), Hover: result}, nil
		})
	})
	return nil, output, err
}

func (runtime *mcpRuntime) definition(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input positionInput,
) (*mcp.CallToolResult, locationsOutput, error) {
	return runtime.locationsAtPosition(ctx, input, "textDocument/definition", false)
}

func (runtime *mcpRuntime) references(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input positionInput,
) (*mcp.CallToolResult, locationsOutput, error) {
	return runtime.locationsAtPosition(ctx, input, "textDocument/references", true)
}

func (runtime *mcpRuntime) locationsAtPosition(
	ctx context.Context,
	input positionInput,
	method string,
	includeDeclaration bool,
) (*mcp.CallToolResult, locationsOutput, error) {
	path, position, err := runtime.position(input.Path, input.Line, input.Column)
	if err != nil {
		return nil, locationsOutput{}, err
	}
	output, err := withMCPSession(ctx, runtime, func(session *cliSession) (locationsOutput, error) {
		return withMCPDocument(ctx, session, path, func(document *cliDocument) (locationsOutput, error) {
			params := positionParams(document.URI, position)
			if includeDeclaration {
				params["context"] = map[string]bool{"includeDeclaration": true}
			}
			var locations []protocol.Location
			if err := session.call(ctx, method, params, &locations); err != nil {
				return locationsOutput{}, err
			}
			result := make([]mcpLocation, 0, len(locations))
			for _, location := range locations {
				result = append(result, runtime.convertLocation(location))
			}
			return locationsOutput{
				Path:     runtime.outputPath(path),
				Position: toMCPPosition(position), Locations: result,
			}, nil
		})
	})
	return nil, output, err
}

func (runtime *mcpRuntime) workspaceSymbols(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input workspaceSymbolsInput,
) (*mcp.CallToolResult, workspaceSymbolsOutput, error) {
	if strings.TrimSpace(input.Query) == "" {
		return nil, workspaceSymbolsOutput{}, errors.New("query is required")
	}
	limit, err := boundedLimit(
		input.Limit, defaultMCPSymbolLimit, maximumMCPSymbolLimit, "limit",
	)
	if err != nil {
		return nil, workspaceSymbolsOutput{}, err
	}
	output, err := withMCPSession(ctx, runtime, func(session *cliSession) (workspaceSymbolsOutput, error) {
		var symbols []protocol.SymbolInformation
		if err := session.call(
			ctx, "workspace/symbol", map[string]string{"query": input.Query}, &symbols,
		); err != nil {
			return workspaceSymbolsOutput{}, err
		}
		total := len(symbols)
		if len(symbols) > limit {
			symbols = symbols[:limit]
		}
		result := make([]mcpWorkspaceSymbol, 0, len(symbols))
		for _, symbol := range symbols {
			result = append(result, mcpWorkspaceSymbol{
				Name: symbol.Name, Kind: symbolKindLabel(symbol.Kind),
				Container: symbol.ContainerName,
				Location:  runtime.convertLocation(symbol.Location),
			})
		}
		return workspaceSymbolsOutput{
			Query: input.Query, Total: total, Symbols: result,
			Truncated: total > len(result),
		}, nil
	})
	return nil, output, err
}

func boundedLimit(value, defaultValue, maximum int, name string) (int, error) {
	if value == 0 {
		return defaultValue, nil
	}
	if value < 1 || value > maximum {
		return 0, fmt.Errorf("%s must be between 1 and %d", name, maximum)
	}
	return value, nil
}

func (runtime *mcpRuntime) position(
	path string,
	line,
	column int,
) (string, protocol.Position, error) {
	resolved, err := runtime.resolvePath(path)
	if err != nil {
		return "", protocol.Position{}, err
	}
	if line < 1 {
		return "", protocol.Position{}, errors.New("line must be at least 1")
	}
	if column == 0 {
		column = 1
	}
	if column < 1 {
		return "", protocol.Position{}, errors.New("column must be at least 1")
	}
	return resolved, protocol.Position{Line: line - 1, Character: column - 1}, nil
}

func (runtime *mcpRuntime) resolvePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("path is required")
	}
	if strings.HasPrefix(value, "file://") {
		path, err := uriutil.Path(value)
		if err != nil {
			return "", fmt.Errorf("resolve path URI: %w", err)
		}
		value = path
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(runtime.root, value)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if !pathWithinRoot(runtime.root, absolute) {
		return "", fmt.Errorf("path is outside workspace root: %s", absolute)
	}
	return absolute, nil
}

func pathWithinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (runtime *mcpRuntime) outputPath(path string) string {
	path = filepath.Clean(path)
	if pathWithinRoot(runtime.root, path) {
		relative, err := filepath.Rel(runtime.root, path)
		if err == nil {
			if relative == "." {
				return "."
			}
			return filepath.ToSlash(relative)
		}
	}
	return path
}

func withMCPDocument[T any](
	ctx context.Context,
	session *cliSession,
	path string,
	operation func(*cliDocument) (T, error),
) (T, error) {
	var zero T
	document, err := session.openDocument(ctx, path)
	if err != nil {
		return zero, err
	}
	result, operationErr := operation(document)
	closeErr := session.closeDocument(ctx, document)
	return result, errors.Join(operationErr, closeErr)
}

func withMCPCodeActions[T any](
	ctx context.Context,
	session *cliSession,
	path string,
	position protocol.Position,
	kind string,
	operation func(*cliDocument, []protocol.CodeAction) (T, error),
) (T, error) {
	return withMCPDocument(ctx, session, path, func(document *cliDocument) (T, error) {
		var zero T
		var diagnostics protocol.DiagnosticResult
		if err := session.call(
			ctx, "textDocument/diagnostic", textDocumentParams(document.URI), &diagnostics,
		); err != nil {
			return zero, err
		}
		params := positionParams(document.URI, position)
		params["range"] = protocol.Range{Start: position, End: position}
		contextParams := map[string]any{"diagnostics": diagnostics.Items}
		if kind != "" {
			contextParams["only"] = []string{kind}
		}
		params["context"] = contextParams
		var actions []protocol.CodeAction
		if err := session.call(ctx, "textDocument/codeAction", params, &actions); err != nil {
			return zero, err
		}
		if kind != "" {
			filtered := actions[:0]
			for _, action := range actions {
				if matchesCodeActionKind(string(action.Kind), kind) {
					filtered = append(filtered, action)
				}
			}
			actions = filtered
		}
		return operation(document, actions)
	})
}

func (runtime *mcpRuntime) convertDiagnostic(finding diagnosticOutput) mcpDiagnostic {
	diagnostic := finding.Diagnostic
	severity := diagnostic.Severity
	if severity == 0 {
		severity = protocol.DiagnosticSeverityWarning
	}
	result := mcpDiagnostic{
		Path: runtime.outputURI(finding.URI), Range: toMCPRange(diagnostic.Range),
		Severity: strings.ToLower(severityLabel(severity)),
		Source:   diagnostic.Source, Message: diagnostic.Message,
	}
	if diagnostic.Code != nil {
		result.Code = fmt.Sprint(diagnostic.Code)
	}
	for _, tag := range diagnostic.Tags {
		switch tag {
		case protocol.DiagnosticTagUnnecessary:
			result.Tags = append(result.Tags, "unnecessary")
		case protocol.DiagnosticTagDeprecated:
			result.Tags = append(result.Tags, "deprecated")
		default:
			result.Tags = append(result.Tags, fmt.Sprintf("tag-%d", tag))
		}
	}
	for _, related := range finding.Related {
		result.Related = append(result.Related, mcpRelatedDiagnostic{
			Location: runtime.convertLocation(related.Location), Message: related.Message,
		})
	}
	return result
}

func (runtime *mcpRuntime) convertLocation(location protocol.Location) mcpLocation {
	return mcpLocation{
		Path: runtime.outputURI(location.URI), Range: toMCPRange(location.Range),
	}
}

func (runtime *mcpRuntime) outputURI(uri string) string {
	path, err := uriutil.Path(uri)
	if err != nil {
		return uri
	}
	return runtime.outputPath(path)
}

func toMCPRange(value protocol.Range) mcpRange {
	return mcpRange{Start: toMCPPosition(value.Start), End: toMCPPosition(value.End)}
}

func toMCPPosition(value protocol.Position) mcpPosition {
	return mcpPosition{Line: value.Line + 1, Column: value.Character + 1}
}

func (runtime *mcpRuntime) validateWorkspaceEdit(edit *protocol.WorkspaceEdit) error {
	for uri := range edit.Changes {
		if _, err := runtime.resolvePath(uri); err != nil {
			return fmt.Errorf("reject workspace edit target %q: %w", uri, err)
		}
	}
	for _, change := range edit.DocumentChanges {
		if change.Kind != "" && change.Kind != protocol.CreateFileOperation {
			return fmt.Errorf("reject unsupported workspace resource operation %q", change.Kind)
		}
		uri := change.URI
		if change.TextDocument != nil {
			uri = change.TextDocument.URI
		}
		if uri == "" {
			continue
		}
		if _, err := runtime.resolvePath(uri); err != nil {
			return fmt.Errorf("reject workspace edit target %q: %w", uri, err)
		}
	}
	return nil
}

func (runtime *mcpRuntime) workspaceEditPaths(edit *protocol.WorkspaceEdit) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	add := func(uri string) {
		path := runtime.outputURI(uri)
		if _, exists := seen[path]; exists {
			return
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	for uri := range edit.Changes {
		add(uri)
	}
	for _, change := range edit.DocumentChanges {
		if change.TextDocument != nil {
			add(change.TextDocument.URI)
		} else if change.URI != "" {
			add(change.URI)
		}
	}
	slices.Sort(result)
	return result
}

func symbolKindLabel(kind protocol.SymbolKind) string {
	labels := [...]string{
		"", "file", "module", "namespace", "package", "class", "method",
		"property", "field", "constructor", "enum", "interface", "function",
		"variable", "constant", "string", "number", "boolean", "array", "object",
		"key", "null", "enumMember", "struct", "event", "operator", "typeParameter",
	}
	if int(kind) > 0 && int(kind) < len(labels) {
		return labels[kind]
	}
	return fmt.Sprintf("kind-%d", kind)
}
