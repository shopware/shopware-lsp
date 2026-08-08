package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/rewrite"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const codeActionEnvelopeSchema = 2

type codeActionEnvelope struct {
	Schema     int                 `json:"schema"`
	Inspection string              `json:"inspection"`
	Fix        FixID               `json:"fix"`
	Binding    int                 `json:"binding"`
	Diagnostic protocol.Diagnostic `json:"diagnostic"`
}

func (s *Server) codeAction(ctx context.Context, params *protocol.CodeActionParams) []protocol.CodeAction {
	if params == nil {
		return nil
	}
	syntax, _ := s.documentManager.SyntaxContext(
		params.TextDocument.URI,
		params.Range.Start.Line,
		params.Range.Start.Character,
	)
	request := &CodeActionRequest{CodeActionParams: params, SyntaxContext: syntax}

	actions := s.inspectionCodeActions(ctx, request)
	for _, provider := range s.actionProviders {
		if ctx.Err() != nil {
			break
		}
		if !providerMatchesOnly(provider, params.Context.Only) {
			continue
		}
		actions = append(actions, provider.GetCodeActions(ctx, request)...)
	}
	return actions
}

func (s *Server) inspectionCodeActions(
	ctx context.Context,
	request *CodeActionRequest,
) []protocol.CodeAction {
	if request == nil || request.Document == nil {
		return nil
	}
	var result []protocol.CodeAction
	for _, diagnostic := range request.Context.Diagnostics {
		if ctx.Err() != nil {
			return result
		}
		envelope, err := decodeDiagnosticEnvelope(diagnostic.Data)
		if err != nil || envelope.URI != request.Document.URI ||
			envelope.DocumentVersion != request.Document.Version ||
			fmt.Sprint(diagnostic.Code) != string(envelope.Code) {
			continue
		}
		registered, found := s.inspections.inspection(envelope.Inspection)
		if !found || registered != s.inspections.byCode[envelope.Code] ||
			!supportsLanguage(registered.definition, request.Document.SyntaxLanguage) {
			continue
		}
		if _, err := envelope.Anchor.Resolve(
			request.Document.URI,
			request.Document.Version,
			request.Document.SyntaxLanguage,
			request.Document.SyntaxTree,
		); err != nil {
			continue
		}
		for binding, bound := range envelope.Fixes {
			fix, found := registered.fixes[bound.ID]
			if !found {
				continue
			}
			fixContext := FixContext{
				Document:       request.Document,
				Diagnostic:     diagnostic,
				Anchor:         envelope.Anchor,
				ProblemPayload: envelope.Payload,
				FixPayload:     bound.Payload,
				Documents:      serverDocumentResolver{server: s},
			}
			presentation, available, err := fix.Present(ctx, fixContext)
			if err != nil || !available || !kindMatchesOnly(presentation.Kind, request.Context.Only) {
				continue
			}
			action := protocol.CodeAction{
				Title:       presentation.Title,
				Kind:        presentation.Kind,
				Diagnostics: []protocol.Diagnostic{diagnostic},
				IsPreferred: presentation.Preferred,
				Data: codeActionEnvelope{
					Schema:     codeActionEnvelopeSchema,
					Inspection: envelope.Inspection,
					Fix:        bound.ID,
					Binding:    binding,
					Diagnostic: diagnostic,
				},
			}
			if presentation.Resolution == FixEager || !s.codeActionResolveSupport {
				s.populateInspectionEdit(ctx, &action, fix, fixContext)
			}
			result = append(result, action)
		}
	}
	return result
}

func (s *Server) resolveCodeAction(
	ctx context.Context,
	action protocol.CodeAction,
) protocol.CodeAction {
	var data codeActionEnvelope
	if err := decodeJSONValue(action.Data, &data); err != nil ||
		data.Schema != codeActionEnvelopeSchema {
		return action
	}
	registered, found := s.inspections.inspection(data.Inspection)
	if !found {
		return disabledCodeAction(action, "The inspection that provided this fix is no longer available")
	}
	fix, found := registered.fixes[data.Fix]
	if !found {
		return disabledCodeAction(action, "The quick fix is no longer available")
	}
	envelope, err := decodeDiagnosticEnvelope(data.Diagnostic.Data)
	if err != nil || envelope.Inspection != data.Inspection {
		return disabledCodeAction(action, "The diagnostic data is no longer valid")
	}
	document, found := s.documentManager.GetDocument(envelope.URI)
	if !found || document.Version != envelope.DocumentVersion {
		return disabledCodeAction(action, "The document changed; request code actions again")
	}
	bound, found := boundFixAt(envelope.Fixes, data.Fix, data.Binding)
	if !found {
		return disabledCodeAction(action, "The diagnostic no longer offers this fix")
	}
	if _, err := envelope.Anchor.Resolve(
		document.URI,
		document.Version,
		document.SyntaxLanguage,
		document.SyntaxTree,
	); err != nil {
		return disabledCodeAction(action, "The diagnostic target changed; request code actions again")
	}
	fixContext := FixContext{
		Document:       document,
		Diagnostic:     data.Diagnostic,
		Anchor:         envelope.Anchor,
		ProblemPayload: envelope.Payload,
		FixPayload:     bound.Payload,
		Documents:      serverDocumentResolver{server: s},
	}
	presentation, available, err := fix.Present(ctx, fixContext)
	if err != nil || !available {
		return disabledCodeAction(action, "The quick fix is no longer applicable")
	}
	action.Title = presentation.Title
	action.Kind = presentation.Kind
	action.IsPreferred = presentation.Preferred
	s.populateInspectionEdit(ctx, &action, fix, fixContext)
	return action
}

func (s *Server) populateInspectionEdit(
	ctx context.Context,
	action *protocol.CodeAction,
	fix QuickFix,
	fixContext FixContext,
) {
	if commandFix, ok := fix.(CommandQuickFix); ok {
		command, err := commandFix.BuildCommand(ctx, fixContext)
		if err != nil || command == nil {
			action.Disabled = &protocol.CodeActionDisabled{Reason: "The quick fix could not be prepared"}
			action.Command = nil
			return
		}
		action.Disabled = nil
		action.Command = command
		return
	}
	rewriteFix, ok := fix.(RewriteQuickFix)
	if !ok {
		action.Disabled = &protocol.CodeActionDisabled{Reason: "The quick fix has no implementation"}
		return
	}
	plan, err := rewriteFix.Build(ctx, fixContext)
	if err != nil {
		disabled := "The quick fix could not be prepared"
		if errors.Is(err, rewrite.ErrStaleHandle) || errors.Is(err, rewrite.ErrMissingHandle) {
			disabled = "The document changed; request code actions again"
		}
		action.Disabled = &protocol.CodeActionDisabled{Reason: disabled}
		action.Edit = nil
		return
	}
	if err := s.validateWorkspacePlan(ctx, plan); err != nil {
		action.Disabled = &protocol.CodeActionDisabled{Reason: "The generated edit is no longer valid"}
		action.Edit = nil
		return
	}
	edit, err := plan.WorkspaceEdit()
	if err != nil {
		action.Disabled = &protocol.CodeActionDisabled{Reason: "The generated edit is invalid"}
		action.Edit = nil
		return
	}
	action.Disabled = nil
	action.Edit = edit
}

func (s *Server) validateWorkspacePlan(ctx context.Context, plan rewrite.WorkspacePlan) error {
	for _, created := range plan.Creates {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := uriutil.Path(created.URI)
		if err != nil || !pathWithinRoot(s.rootPath, path) {
			return fmt.Errorf("created document %q is outside the workspace", created.URI)
		}
		if _, err := os.Stat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("created document %q already exists or cannot be checked", created.URI)
		}
		if _, result, parsed := s.documentManager.languages.ParsePath(created.URI, created.Content); parsed && len(result.Errors) != 0 {
			return fmt.Errorf("created document has %d parser errors", len(result.Errors))
		}
	}
	for _, planned := range plan.Documents {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := (serverDocumentResolver{server: s}).ResolveDocument(ctx, planned.URI)
		if err != nil {
			return err
		}
		if current.Document.Source != planned.Source ||
			!sameOptionalVersion(current.Version, planned.Version) {
			return rewrite.ErrStaleHandle
		}
		updated, err := planned.Apply()
		if err != nil {
			return err
		}
		_, result, parsed := s.documentManager.languages.ParsePath(planned.URI, updated)
		if parsed && len(result.Errors) > len(current.Document.ParseErrors) {
			return fmt.Errorf("rewrite introduces %d parser errors", len(result.Errors)-len(current.Document.ParseErrors))
		}
	}
	for _, deleted := range plan.Deletes {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := uriutil.Path(deleted.URI)
		if err != nil || !pathWithinRoot(s.rootPath, path) {
			return fmt.Errorf("deleted document %q is outside the workspace", deleted.URI)
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("deleted document %q does not exist or is not a file", deleted.URI)
		}
		current, err := (serverDocumentResolver{server: s}).ResolveDocument(ctx, deleted.URI)
		if err != nil {
			return err
		}
		if current.Document.Source != deleted.Source ||
			!sameOptionalVersion(current.Version, deleted.Version) {
			return rewrite.ErrStaleHandle
		}
	}
	return nil
}

type serverDocumentResolver struct {
	server *Server
}

func (r serverDocumentResolver) ResolveDocument(
	ctx context.Context,
	uri string,
) (DocumentSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return DocumentSnapshot{}, err
	}
	if r.server == nil {
		return DocumentSnapshot{}, errors.New("document resolver has no server")
	}
	if document, found := r.server.documentManager.GetDocument(uri); found {
		version := document.Version
		return DocumentSnapshot{Document: document, Version: &version}, nil
	}
	path, err := uriutil.Path(uri)
	if err != nil {
		return DocumentSnapshot{}, fmt.Errorf("resolve document URI: %w", err)
	}
	if !pathWithinRoot(r.server.rootPath, path) {
		return DocumentSnapshot{}, fmt.Errorf("document %q is outside the workspace", uri)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return DocumentSnapshot{}, fmt.Errorf("read document %q: %w", uri, err)
	}
	document := NewTextDocumentWithRegistry(
		r.server.documentManager.languages,
		uri,
		string(content),
		0,
	)
	return DocumentSnapshot{Document: document}, nil
}

func pathWithinRoot(root, path string) bool {
	if root == "" {
		return false
	}
	root, rootErr := filepath.Abs(root)
	path, pathErr := filepath.Abs(path)
	if rootErr != nil || pathErr != nil {
		return false
	}
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func providerMatchesOnly(provider ActionProvider, only []string) bool {
	if len(only) == 0 {
		return true
	}
	for _, kind := range provider.GetCodeActionKinds() {
		if kindMatchesOnly(kind, only) {
			return true
		}
	}
	return false
}

func kindMatchesOnly(kind protocol.CodeActionKind, only []string) bool {
	if len(only) == 0 {
		return true
	}
	value := string(kind)
	for _, requested := range only {
		if value == requested || strings.HasPrefix(value, requested+".") {
			return true
		}
	}
	return false
}

func boundFixAt(values []boundFixEnvelope, id FixID, binding int) (boundFixEnvelope, bool) {
	if binding < 0 || binding >= len(values) || values[binding].ID != id {
		return boundFixEnvelope{}, false
	}
	return values[binding], true
}

func decodeJSONValue(value any, target any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

func disabledCodeAction(action protocol.CodeAction, reason string) protocol.CodeAction {
	action.Disabled = &protocol.CodeActionDisabled{Reason: reason}
	action.Edit = nil
	return action
}

func sameOptionalVersion(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
