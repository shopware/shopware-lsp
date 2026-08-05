package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shopware/shopware-lsp/internal/app"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/sourcegraph/jsonrpc2"
)

type indexingResult struct {
	TimeInSeconds float64 `json:"timeInSeconds"`
}

type cliSession struct {
	application  *app.Application
	client       *jsonrpc2.Conn
	clientSide   net.Conn
	serverSide   net.Conn
	serverDone   chan struct{}
	serverErrMu  sync.Mutex
	serverErr    error
	indexStarted chan struct{}
	indexDone    chan indexingResult
	indexFailed  chan error
	root         string
	capabilities interface{}
	initialIndex indexingResult
	closeOnce    sync.Once
	closeErr     error
}

type cliDocument struct {
	Path    string
	URI     string
	Source  string
	Version int
}

func (r *Runner) workspaceRoot() (string, error) {
	root := r.root
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("current directory: %w", err)
		}
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("workspace root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace root is not a directory: %s", absolute)
	}
	return filepath.Clean(absolute), nil
}

func (r *Runner) connect(ctx context.Context) (*cliSession, error) {
	root, err := r.workspaceRoot()
	if err != nil {
		return nil, err
	}
	if r.verbose {
		if err := writeFormatted(r.errOut, "Initializing workspace %s...\n", root); err != nil {
			return nil, err
		}
	}
	session, err := newCLISession(ctx, root, r.options.Version, r.errOut)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	result, err := session.waitForIndex(ctx)
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	session.initialIndex = result
	if r.verbose {
		duration := time.Duration(result.TimeInSeconds * float64(time.Second))
		if duration <= 0 {
			duration = time.Since(started)
		}
		if err := writeFormatted(r.errOut, "Workspace ready in %s\n", duration.Round(time.Millisecond)); err != nil {
			_ = session.Close()
			return nil, err
		}
	}
	return session, nil
}

func newCLISession(
	ctx context.Context,
	root,
	version string,
	errOut io.Writer,
) (*cliSession, error) {
	serverSide, clientSide := net.Pipe()
	session := &cliSession{
		application:  app.New(version),
		clientSide:   clientSide,
		serverSide:   serverSide,
		serverDone:   make(chan struct{}),
		indexStarted: make(chan struct{}, 4),
		indexDone:    make(chan indexingResult, 4),
		indexFailed:  make(chan error, 4),
		root:         root,
	}
	go func() {
		err := session.application.Run(serverSide, serverSide)
		session.serverErrMu.Lock()
		session.serverErr = err
		session.serverErrMu.Unlock()
		close(session.serverDone)
	}()
	session.client = jsonrpc2.NewConn(
		ctx,
		jsonrpc2.NewBufferedStream(clientSide, jsonrpc2.VSCodeObjectCodec{}),
		jsonrpc2.HandlerWithError(func(
			_ context.Context,
			_ *jsonrpc2.Conn,
			request *jsonrpc2.Request,
		) (interface{}, error) {
			switch request.Method {
			case "shopware/indexingStarted":
				select {
				case session.indexStarted <- struct{}{}:
				default:
				}
			case "shopware/indexingCompleted":
				var result indexingResult
				if request.Params != nil {
					if err := json.Unmarshal(*request.Params, &result); err != nil {
						return nil, err
					}
				}
				session.indexDone <- result
			case "shopware/indexingFailed":
				var result struct {
					Message string `json:"message"`
				}
				if request.Params != nil {
					if err := json.Unmarshal(*request.Params, &result); err != nil {
						return nil, err
					}
				}
				session.indexFailed <- errors.New(result.Message)
			case "window/logMessage", "window/showMessage":
				if errOut != nil && request.Params != nil {
					var message struct {
						Message string `json:"message"`
					}
					if json.Unmarshal(*request.Params, &message) == nil && message.Message != "" {
						if err := writeFormatted(errOut, "%s\n", message.Message); err != nil {
							return nil, err
						}
					}
				}
			case "workspace/applyEdit":
				return map[string]interface{}{
					"applied":       false,
					"failureReason": "CLI commands apply returned edits explicitly",
				}, nil
			}
			return nil, nil
		}).SuppressErrClosed(),
	)
	initialize := map[string]interface{}{
		"processId": os.Getpid(),
		"rootUri":   uriutil.FileURI(root),
		"initializationOptions": map[string]bool{
			"cliMode": true,
		},
		"workspaceFolders": []map[string]string{{
			"uri": uriutil.FileURI(root), "name": filepath.Base(root),
		}},
		"capabilities": map[string]interface{}{
			"textDocument": map[string]interface{}{
				"codeAction": map[string]interface{}{
					"dataSupport": true,
					"resolveSupport": map[string]interface{}{
						"properties": []string{"edit"},
					},
				},
			},
		},
	}
	if err := session.client.Call(
		ctx, "initialize", initialize, &session.capabilities,
	); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("initialize LSP session: %w", err)
	}
	if err := session.client.Notify(ctx, "initialized", struct{}{}); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("notify initialized: %w", err)
	}
	return session, nil
}

func (s *cliSession) waitForIndex(ctx context.Context) (indexingResult, error) {
	select {
	case result := <-s.indexDone:
		return result, nil
	case err := <-s.indexFailed:
		return indexingResult{}, fmt.Errorf("index workspace: %w", err)
	case <-s.serverDone:
		err := s.languageServerError()
		if err == nil {
			err = errors.New("language server stopped during indexing")
		}
		return indexingResult{}, err
	case <-ctx.Done():
		return indexingResult{}, ctx.Err()
	}
}

func (s *cliSession) openDocument(
	ctx context.Context,
	path string,
) (*cliDocument, error) {
	if strings.HasPrefix(path, "file://") {
		resolved, err := uriutil.Path(path)
		if err != nil {
			return nil, err
		}
		path = resolved
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve document path: %w", err)
	}
	content, err := os.ReadFile(absolute)
	if err != nil {
		return nil, fmt.Errorf("read document: %w", err)
	}
	languageID := strings.TrimPrefix(strings.ToLower(filepath.Ext(absolute)), ".")
	if definition, ok := language.DefaultRegistry().ForPath(absolute); ok {
		languageID = string(definition.ID)
	}
	document := &cliDocument{
		Path: absolute, URI: uriutil.FileURI(absolute),
		Source: string(content), Version: 1,
	}
	if err := s.client.Notify(ctx, "textDocument/didOpen", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": document.URI, "version": document.Version,
			"languageId": languageID, "text": document.Source,
		},
	}); err != nil {
		return nil, fmt.Errorf("open document: %w", err)
	}
	return document, nil
}

func (s *cliSession) closeDocument(
	ctx context.Context,
	document *cliDocument,
) error {
	if document == nil {
		return nil
	}
	if err := s.client.Notify(ctx, "textDocument/didClose", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": document.URI},
	}); err != nil {
		return fmt.Errorf("close document: %w", err)
	}
	return nil
}

func (s *cliSession) checkDocument(
	ctx context.Context,
	path string,
	cutoff protocol.DiagnosticSeverity,
) ([]diagnosticOutput, error) {
	document, err := s.openDocument(ctx, path)
	if err != nil {
		return nil, err
	}
	var result protocol.DiagnosticResult
	callErr := s.call(
		ctx,
		"textDocument/diagnostic",
		textDocumentParams(document.URI),
		&result,
	)
	closeErr := s.closeDocument(ctx, document)
	if err := errors.Join(callErr, closeErr); err != nil {
		return nil, err
	}
	findings := make([]diagnosticOutput, 0, len(result.Items))
	for _, diagnostic := range result.Items {
		severity := diagnostic.Severity
		if severity == 0 {
			severity = protocol.DiagnosticSeverityWarning
		}
		if severity > cutoff {
			continue
		}
		findings = append(findings, diagnosticOutput{
			URI: document.URI, Diagnostic: diagnostic,
			Related: diagnostic.RelatedInformation,
		})
	}
	return findings, nil
}

func (s *cliSession) call(
	ctx context.Context,
	method string,
	params,
	result interface{},
) error {
	if err := s.client.Call(ctx, method, params, result); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	return nil
}

func (s *cliSession) Close() error {
	s.closeOnce.Do(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if s.client != nil {
			var result interface{}
			if err := s.client.Call(
				shutdownCtx, "shutdown", struct{}{}, &result,
			); err != nil && !errors.Is(err, jsonrpc2.ErrClosed) {
				s.closeErr = errors.Join(s.closeErr, err)
			}
			_ = s.client.Notify(shutdownCtx, "exit", struct{}{})
		}
		select {
		case <-s.serverDone:
			s.closeErr = errors.Join(s.closeErr, s.languageServerError())
		case <-shutdownCtx.Done():
			s.closeErr = errors.Join(s.closeErr, errors.New("language server did not stop"))
		}
		if s.client != nil {
			_ = s.client.Close()
		}
		if s.clientSide != nil {
			_ = s.clientSide.Close()
		}
		if s.serverSide != nil {
			_ = s.serverSide.Close()
		}
	})
	return s.closeErr
}

func (s *cliSession) languageServerError() error {
	s.serverErrMu.Lock()
	defer s.serverErrMu.Unlock()
	return s.serverErr
}

func textDocumentParams(uri string) map[string]interface{} {
	return map[string]interface{}{
		"textDocument": map[string]string{"uri": uri},
	}
}

func positionParams(uri string, position protocol.Position) map[string]interface{} {
	return map[string]interface{}{
		"textDocument": map[string]string{"uri": uri},
		"position":     position,
	}
}
