package lsp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/rewrite"
)

type DiagnosticID string
type FixID string

type ProblemDefinition struct {
	ID                DiagnosticID
	Source            string
	DefaultSeverity   protocol.DiagnosticSeverity
	DisabledByDefault bool
}

type InspectionDefinition struct {
	ID        string
	Languages []language.ID
	Problems  []ProblemDefinition
}

// Problem is the internal byte-oriented representation of a diagnostic. Fixes
// are bound while the problem is reported, mirroring IntelliJ's ProblemsHolder
// rather than rediscovering the owning fix through provider fan-out.
type Problem struct {
	ID                 DiagnosticID
	Range              cst.TextRange
	Element            cst.Element
	Message            string
	Severity           protocol.DiagnosticSeverity
	Source             string
	Tags               []protocol.DiagnosticTag
	RelatedInformation []protocol.DiagnosticRelatedInformation
	Payload            any
	Fixes              []BoundFix
}

type BoundFix struct {
	ID      FixID
	Payload any
}

func BindFix[T any](id FixID, payload T) BoundFix {
	return BoundFix{ID: id, Payload: payload}
}

type ProblemReporter interface {
	Report(Problem) error
}

type Inspection interface {
	Definition() InspectionDefinition
	Inspect(context.Context, *TextDocument, ProblemReporter) error
	QuickFixes() []QuickFix
}

type FixResolution uint8

const (
	FixEager FixResolution = iota
	FixLazy
)

type FixPresentation struct {
	Title      string
	Kind       protocol.CodeActionKind
	Preferred  bool
	Resolution FixResolution
}

type QuickFix interface {
	ID() FixID
	Present(context.Context, FixContext) (FixPresentation, bool, error)
}

// RewriteQuickFix compiles a structural source rewrite. It is kept separate
// from QuickFix so command-backed fixes can participate in the same exact
// diagnostic binding without manufacturing an empty edit plan.
type RewriteQuickFix interface {
	QuickFix
	Build(context.Context, FixContext) (rewrite.WorkspacePlan, error)
}

// CommandQuickFix resolves to an LSP command for workflows that require user
// input or editor snippet semantics instead of a plain text rewrite.
type CommandQuickFix interface {
	QuickFix
	BuildCommand(context.Context, FixContext) (*protocol.CommandAction, error)
}

type DocumentSnapshot struct {
	Document *TextDocument
	Version  *int
}

type DocumentResolver interface {
	ResolveDocument(context.Context, string) (DocumentSnapshot, error)
}

type FixContext struct {
	Document       *TextDocument
	Diagnostic     protocol.Diagnostic
	Anchor         rewrite.ElementHandle
	ProblemPayload json.RawMessage
	FixPayload     json.RawMessage
	Documents      DocumentResolver
}

func DecodeProblemPayload[T any](context FixContext) (T, error) {
	return decodeFixValue[T](context.ProblemPayload, "problem payload")
}

func DecodeBoundFixPayload[T any](context FixContext) (T, error) {
	return decodeFixValue[T](context.FixPayload, "fix payload")
}

func decodeFixValue[T any](value json.RawMessage, label string) (T, error) {
	var result T
	if len(value) == 0 || string(value) == "null" {
		return result, nil
	}
	if err := json.Unmarshal(value, &result); err != nil {
		return result, fmt.Errorf("decode %s: %w", label, err)
	}
	return result, nil
}
