package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	defaultMCPDiagnosticLimit = 200
	maximumMCPDiagnosticLimit = 1000
	defaultMCPSymbolLimit     = 50
	maximumMCPSymbolLimit     = 200
)

type mcpRuntime struct {
	runner *Runner
	root   string

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
	runtime := &mcpRuntime{runner: r, root: root}
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

func newMCPServer(runtime *mcpRuntime) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    "shopware-lsp",
			Version: runtime.runner.options.Version,
		},
		&mcp.ServerOptions{
			Instructions: "Analyze the configured Shopware workspace. Paths may be workspace-relative or absolute, and all input positions use one-based lines and columns. List a code action before applying it.",
			Capabilities: &mcp.ServerCapabilities{},
		},
	)
	readOnly := &mcp.ToolAnnotations{
		ReadOnlyHint: true, IdempotentHint: true,
		OpenWorldHint: boolPointer(false),
	}
	mcp.AddTool[diagnosticsInput, any](server, &mcp.Tool{
		Name: "shopware_diagnostics", Title: "Shopware diagnostics",
		Description: "Run the same configured Shopware LSP diagnostics used by the editor for one workspace file or directory.",
		Annotations: readOnly,
	}, runtime.diagnostics)
	mcp.AddTool[codeActionsInput, any](server, &mcp.Tool{
		Name: "shopware_code_actions", Title: "Shopware code actions",
		Description: "List available quick fixes and refactorings at a one-based file position without changing files.",
		Annotations: readOnly,
	}, runtime.codeActions)
	mcp.AddTool[applyCodeActionInput, any](server, &mcp.Tool{
		Name: "shopware_apply_code_action", Title: "Apply Shopware code action",
		Description: "Resolve and apply one exact code-action title at a one-based file position. This writes workspace files and returns the resulting diff.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: false, IdempotentHint: false,
			DestructiveHint: boolPointer(true), OpenWorldHint: boolPointer(false),
		},
	}, runtime.applyCodeAction)
	mcp.AddTool[positionInput, any](server, &mcp.Tool{
		Name: "shopware_hover", Title: "Shopware hover",
		Description: "Return Shopware LSP hover information at a one-based file position.",
		Annotations: readOnly,
	}, runtime.hover)
	mcp.AddTool[positionInput, any](server, &mcp.Tool{
		Name: "shopware_definition", Title: "Shopware definitions",
		Description: "Find definitions for the symbol at a one-based file position.",
		Annotations: readOnly,
	}, runtime.definition)
	mcp.AddTool[positionInput, any](server, &mcp.Tool{
		Name: "shopware_references", Title: "Shopware references",
		Description: "Find references, including the declaration, for the symbol at a one-based file position.",
		Annotations: readOnly,
	}, runtime.references)
	mcp.AddTool[workspaceSymbolsInput, any](server, &mcp.Tool{
		Name: "shopware_workspace_symbols", Title: "Shopware workspace symbols",
		Description: "Search indexed Shopware workspace symbols such as PHP classes and members.",
		Annotations: readOnly,
	}, runtime.workspaceSymbols)
	return server
}

func boolPointer(value bool) *bool { return &value }

func (runtime *mcpRuntime) withSession(
	ctx context.Context,
	operation func(*cliSession) (any, error),
) (any, error) {
	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
	if runtime.session == nil {
		session, err := newCLISession(
			ctx,
			runtime.root,
			runtime.runner.options.Version,
			runtime.runner.errOut,
			true,
		)
		if err != nil {
			return nil, err
		}
		result, err := session.waitForIndex(ctx)
		if err != nil {
			_ = session.Close()
			return nil, err
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
	Code     any                    `json:"code,omitempty"`
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

func (runtime *mcpRuntime) diagnostics(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input diagnosticsInput,
) (*mcp.CallToolResult, any, error) {
	target, err := runtime.resolvePath(input.Path)
	if err != nil {
		return nil, nil, err
	}
	limit, err := boundedLimit(
		input.MaxResults, defaultMCPDiagnosticLimit, maximumMCPDiagnosticLimit,
		"maxResults",
	)
	if err != nil {
		return nil, nil, err
	}
	return runtimeToolResult(runtime.withSession(ctx, func(session *cliSession) (any, error) {
		severity := strings.TrimSpace(input.Severity)
		if severity == "" {
			severity = string(session.configuration.Effective.Check.Severity)
		}
		cutoff, err := diagnosticSeverity(severity)
		if err != nil {
			return nil, err
		}
		paths, err := resolveCheckFiles(ctx, []string{target})
		if err != nil {
			return nil, err
		}
		findings := make([]mcpDiagnostic, 0)
		total := 0
		for _, path := range paths {
			diagnostics, err := session.checkDocument(ctx, path, cutoff)
			if err != nil {
				return nil, err
			}
			for _, finding := range diagnostics {
				total++
				if len(findings) < limit {
					findings = append(findings, runtime.convertDiagnostic(finding))
				}
			}
		}
		return map[string]any{
			"root":         runtime.root,
			"path":         runtime.outputPath(target),
			"filesChecked": len(paths),
			"severity":     strings.ToLower(severity),
			"total":        total,
			"diagnostics":  findings,
			"truncated":    total > len(findings),
		}, nil
	}))
}

func (runtime *mcpRuntime) codeActions(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input codeActionsInput,
) (*mcp.CallToolResult, any, error) {
	path, position, err := runtime.position(input.Path, input.Line, input.Column)
	if err != nil {
		return nil, nil, err
	}
	return runtimeToolResult(runtime.withSession(ctx, func(session *cliSession) (any, error) {
		return runtime.withCodeActions(ctx, session, path, position, input.Kind, func(
			_ *cliDocument,
			actions []protocol.CodeAction,
		) (any, error) {
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
			return map[string]any{
				"path":     runtime.outputPath(path),
				"position": toMCPPosition(position),
				"actions":  result,
			}, nil
		})
	}))
}

func (runtime *mcpRuntime) applyCodeAction(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input applyCodeActionInput,
) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Title) == "" {
		return nil, nil, errors.New("title is required")
	}
	path, position, err := runtime.position(input.Path, input.Line, input.Column)
	if err != nil {
		return nil, nil, err
	}
	return runtimeToolResult(runtime.withSession(ctx, func(session *cliSession) (any, error) {
		return runtime.withCodeActions(ctx, session, path, position, input.Kind, func(
			_ *cliDocument,
			actions []protocol.CodeAction,
		) (any, error) {
			matches := make([]protocol.CodeAction, 0, 1)
			for _, action := range actions {
				if action.Title == input.Title {
					matches = append(matches, action)
				}
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("no code action with exact title %q", input.Title)
			}
			if len(matches) > 1 {
				return nil, fmt.Errorf("code action title %q is ambiguous; also provide kind", input.Title)
			}
			action := matches[0]
			if action.Disabled != nil {
				return nil, fmt.Errorf("code action %q is disabled: %s", action.Title, action.Disabled.Reason)
			}
			if action.Edit == nil && action.Data != nil {
				var resolved protocol.CodeAction
				if err := session.call(ctx, "codeAction/resolve", action, &resolved); err != nil {
					return nil, err
				}
				action = resolved
				if action.Disabled != nil {
					return nil, fmt.Errorf("code action %q is disabled: %s", action.Title, action.Disabled.Reason)
				}
			}
			if action.Edit == nil {
				if action.Command != nil {
					return nil, fmt.Errorf("code action %q requires unsupported editor command %q", action.Title, action.Command.Command)
				}
				return nil, fmt.Errorf("code action %q returned no edits", action.Title)
			}
			if err := runtime.validateWorkspaceEdit(action.Edit); err != nil {
				return nil, err
			}
			var diff bytes.Buffer
			if err := applyWorkspaceEdit(&diff, action.Edit, editMode{Write: true, Diff: true}); err != nil {
				return nil, err
			}
			return map[string]any{
				"applied": true,
				"title":   action.Title,
				"files":   runtime.workspaceEditPaths(action.Edit),
				"diff":    diff.String(),
			}, nil
		})
	}))
}

func (runtime *mcpRuntime) hover(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input positionInput,
) (*mcp.CallToolResult, any, error) {
	path, position, err := runtime.position(input.Path, input.Line, input.Column)
	if err != nil {
		return nil, nil, err
	}
	return runtimeToolResult(runtime.withSession(ctx, func(session *cliSession) (any, error) {
		return runtime.withDocument(ctx, session, path, func(document *cliDocument) (any, error) {
			var hover *protocol.Hover
			if err := session.call(ctx, "textDocument/hover", positionParams(document.URI, position), &hover); err != nil {
				return nil, err
			}
			if hover == nil {
				return map[string]any{"path": runtime.outputPath(path), "hover": nil}, nil
			}
			result := map[string]any{
				"kind": string(hover.Contents.Kind), "value": hover.Contents.Value,
			}
			if hover.Range != nil {
				result["range"] = toMCPRange(*hover.Range)
			}
			return map[string]any{"path": runtime.outputPath(path), "hover": result}, nil
		})
	}))
}

func (runtime *mcpRuntime) definition(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input positionInput,
) (*mcp.CallToolResult, any, error) {
	return runtime.locationsAtPosition(ctx, input, "textDocument/definition", false)
}

func (runtime *mcpRuntime) references(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input positionInput,
) (*mcp.CallToolResult, any, error) {
	return runtime.locationsAtPosition(ctx, input, "textDocument/references", true)
}

func (runtime *mcpRuntime) locationsAtPosition(
	ctx context.Context,
	input positionInput,
	method string,
	includeDeclaration bool,
) (*mcp.CallToolResult, any, error) {
	path, position, err := runtime.position(input.Path, input.Line, input.Column)
	if err != nil {
		return nil, nil, err
	}
	return runtimeToolResult(runtime.withSession(ctx, func(session *cliSession) (any, error) {
		return runtime.withDocument(ctx, session, path, func(document *cliDocument) (any, error) {
			params := positionParams(document.URI, position)
			if includeDeclaration {
				params["context"] = map[string]bool{"includeDeclaration": true}
			}
			var locations []protocol.Location
			if err := session.call(ctx, method, params, &locations); err != nil {
				return nil, err
			}
			result := make([]mcpLocation, 0, len(locations))
			for _, location := range locations {
				result = append(result, runtime.convertLocation(location))
			}
			return map[string]any{
				"path":      runtime.outputPath(path),
				"position":  toMCPPosition(position),
				"locations": result,
			}, nil
		})
	}))
}

func (runtime *mcpRuntime) workspaceSymbols(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input workspaceSymbolsInput,
) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Query) == "" {
		return nil, nil, errors.New("query is required")
	}
	limit, err := boundedLimit(
		input.Limit, defaultMCPSymbolLimit, maximumMCPSymbolLimit, "limit",
	)
	if err != nil {
		return nil, nil, err
	}
	return runtimeToolResult(runtime.withSession(ctx, func(session *cliSession) (any, error) {
		var symbols []protocol.SymbolInformation
		if err := session.call(
			ctx, "workspace/symbol", map[string]string{"query": input.Query}, &symbols,
		); err != nil {
			return nil, err
		}
		total := len(symbols)
		if len(symbols) > limit {
			symbols = symbols[:limit]
		}
		result := make([]map[string]any, 0, len(symbols))
		for _, symbol := range symbols {
			result = append(result, map[string]any{
				"name":      symbol.Name,
				"kind":      symbolKindLabel(symbol.Kind),
				"container": symbol.ContainerName,
				"location":  runtime.convertLocation(symbol.Location),
			})
		}
		return map[string]any{
			"query": input.Query, "total": total, "symbols": result,
			"truncated": total > len(result),
		}, nil
	}))
}

func runtimeToolResult(value any, err error) (*mcp.CallToolResult, any, error) {
	return nil, value, err
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

func (runtime *mcpRuntime) withDocument(
	ctx context.Context,
	session *cliSession,
	path string,
	operation func(*cliDocument) (any, error),
) (any, error) {
	document, err := session.openDocument(ctx, path)
	if err != nil {
		return nil, err
	}
	result, operationErr := operation(document)
	closeErr := session.closeDocument(ctx, document)
	return result, errors.Join(operationErr, closeErr)
}

func (runtime *mcpRuntime) withCodeActions(
	ctx context.Context,
	session *cliSession,
	path string,
	position protocol.Position,
	kind string,
	operation func(*cliDocument, []protocol.CodeAction) (any, error),
) (any, error) {
	return runtime.withDocument(ctx, session, path, func(document *cliDocument) (any, error) {
		var diagnostics protocol.DiagnosticResult
		if err := session.call(
			ctx, "textDocument/diagnostic", textDocumentParams(document.URI), &diagnostics,
		); err != nil {
			return nil, err
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
			return nil, err
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
		Code:     diagnostic.Code, Source: diagnostic.Source, Message: diagnostic.Message,
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
