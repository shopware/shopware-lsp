package lsp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/projectconfig"
	"github.com/shopware/shopware-lsp/internal/rewrite"
)

// RegisterInspection registers one inspection together with the quick fixes
// it can bind to reported problems. Invalid registrations fail fast during
// workspace composition.
func (s *Server) RegisterInspection(inspection Inspection) {
	if err := s.inspections.register(inspection); err != nil {
		panic(fmt.Sprintf("register inspection: %v", err))
	}
}

type documentAnalysis struct {
	uri     string
	version int
}

type diagnosticsCacheEntry struct {
	document    *TextDocument
	generation  uint64
	diagnostics []protocol.Diagnostic
}

func (s *Server) PublishDiagnostics(ctx context.Context, files []string) {
	var documents []documentAnalysis
	if files == nil {
		for _, document := range s.documentManager.Documents() {
			documents = append(documents, documentAnalysis{
				uri:     document.URI,
				version: document.Version,
			})
		}
	} else {
		for _, uri := range files {
			version := 0
			if document, ok := s.documentManager.GetDocument(uri); ok {
				version = document.Version
			}
			documents = append(documents, documentAnalysis{uri: uri, version: version})
		}
	}

	for _, document := range documents {
		s.scheduleDiagnostics(document.uri, document.version, 0)
	}
}

// RefreshOpenDocumentDiagnostics schedules a debounced re-analysis for every
// open document accepted by match. Workspace overlays use this when an open
// dependency changes: the changed file gets its normal diagnostics pass while
// consumers are refreshed without publishing one job per keystroke eagerly.
func (s *Server) RefreshOpenDocumentDiagnostics(
	match func(*TextDocument) bool,
) {
	if s == nil || s.documentManager == nil {
		return
	}
	if s.initializationOptions.CLIMode {
		return
	}
	for _, document := range s.documentManager.Documents() {
		if document == nil || match != nil && !match(document) {
			continue
		}
		s.scheduleDiagnostics(
			document.URI, document.Version, diagnosticsDebounce,
		)
	}
}

func (s *Server) scheduleDiagnostics(uri string, version int, delay time.Duration) {
	parent := s.lifecycleCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	job := &diagnosticsJob{cancel: cancel, done: ctx.Done()}

	s.diagnosticsMu.Lock()
	if s.diagnosticsGenerations == nil {
		s.diagnosticsGenerations = make(map[string]uint64)
	}
	if s.diagnosticsCache == nil {
		s.diagnosticsCache = make(map[string]diagnosticsCacheEntry)
	}
	s.diagnosticsGenerations[uri]++
	delete(s.diagnosticsCache, uri)
	if previous := s.diagnosticsJobs[uri]; previous != nil {
		previous.cancel()
	}
	s.diagnosticsJobs[uri] = job
	s.diagnosticsMu.Unlock()

	if !s.startBackground(func(context.Context) {
		timer := time.NewTimer(delay)
		defer timer.Stop()

		select {
		case <-timer.C:
			s.publishDiagnostics(ctx, uri, version)
		case <-ctx.Done():
		}

		s.diagnosticsMu.Lock()
		if s.diagnosticsJobs[uri] == job {
			delete(s.diagnosticsJobs, uri)
		}
		s.diagnosticsMu.Unlock()
	}) {
		cancel()
		s.diagnosticsMu.Lock()
		if s.diagnosticsJobs[uri] == job {
			delete(s.diagnosticsJobs, uri)
		}
		s.diagnosticsMu.Unlock()
	}
}

func (s *Server) cancelDiagnostics(uri string) {
	s.diagnosticsMu.Lock()
	defer s.diagnosticsMu.Unlock()
	if job := s.diagnosticsJobs[uri]; job != nil {
		job.cancel()
		delete(s.diagnosticsJobs, uri)
	}
	delete(s.diagnosticsCache, uri)
	delete(s.diagnosticsGenerations, uri)
}

func (s *Server) cancelAllDiagnostics() {
	s.diagnosticsMu.Lock()
	defer s.diagnosticsMu.Unlock()
	for uri, job := range s.diagnosticsJobs {
		job.cancel()
		delete(s.diagnosticsJobs, uri)
	}
	s.diagnosticsCache = make(map[string]diagnosticsCacheEntry)
	s.diagnosticsGenerations = make(map[string]uint64)
}

func (s *Server) publishDiagnostics(ctx context.Context, uri string, version int) {
	conn := s.connection()
	if conn == nil {
		return
	}
	document, ok := s.documentManager.GetDocument(uri)
	if !ok || document.Version != version {
		return
	}

	diagnostics := s.diagnosticsForDocument(ctx, document)
	s.diagnosticsPublishMu.Lock()
	defer s.diagnosticsPublishMu.Unlock()
	current, ok := s.documentManager.GetDocument(uri)
	if ctx.Err() != nil || !ok || current != document || current.Version != version {
		return
	}

	params := protocol.PublishDiagnosticsParams{
		URI:         uri,
		Version:     version,
		Diagnostics: diagnostics,
	}
	if err := conn.Notify(ctx, "textDocument/publishDiagnostics", params); err != nil {
		log.Printf("Error publishing diagnostics: %v", err)
	}
}

// clearPublishedDiagnostics removes diagnostics owned by this server when an
// editor closes a document. Publishing and clearing share a lock so a canceled
// background analysis cannot write a stale result after the empty result.
func (s *Server) clearPublishedDiagnostics(ctx context.Context, uri string) {
	conn := s.connection()
	if conn == nil {
		return
	}
	s.diagnosticsPublishMu.Lock()
	defer s.diagnosticsPublishMu.Unlock()
	params := protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: []protocol.Diagnostic{},
	}
	if err := conn.Notify(ctx, "textDocument/publishDiagnostics", params); err != nil {
		log.Printf("Error clearing diagnostics: %v", err)
	}
}

func (s *Server) diagnostic(ctx context.Context, params *protocol.DiagnosticParams) interface{} {
	document, ok := s.documentManager.GetDocument(params.TextDocument.URI)
	if !ok {
		return protocol.DiagnosticResult{Items: []protocol.Diagnostic{}}
	}
	return protocol.DiagnosticResult{Items: s.diagnosticsForDocument(ctx, document)}
}

func (s *Server) diagnosticsForDocument(
	ctx context.Context,
	document *TextDocument,
) []protocol.Diagnostic {
	if document == nil || !s.diagnosticPolicy(document.URI).Enabled {
		return []protocol.Diagnostic{}
	}
	s.diagnosticsMu.Lock()
	generation := s.diagnosticsGenerations[document.URI]
	cached, found := s.diagnosticsCache[document.URI]
	s.diagnosticsMu.Unlock()
	if found && cached.document == document &&
		cached.generation == generation {
		return append([]protocol.Diagnostic(nil), cached.diagnostics...)
	}

	diagnostics := s.collectDiagnostics(ctx, document)
	if ctx.Err() != nil {
		return diagnostics
	}
	s.diagnosticsMu.Lock()
	if s.diagnosticsGenerations == nil {
		s.diagnosticsGenerations = make(map[string]uint64)
	}
	if s.diagnosticsCache == nil {
		s.diagnosticsCache = make(map[string]diagnosticsCacheEntry)
	}
	if s.diagnosticsGenerations[document.URI] == generation {
		s.diagnosticsCache[document.URI] = diagnosticsCacheEntry{
			document: document, generation: generation,
			diagnostics: append([]protocol.Diagnostic(nil), diagnostics...),
		}
	}
	s.diagnosticsMu.Unlock()
	return diagnostics
}

func (s *Server) collectDiagnostics(ctx context.Context, document *TextDocument) []protocol.Diagnostic {
	allDiagnostics := []protocol.Diagnostic{}
	policy := s.diagnosticPolicy(document.URI)
	if !policy.Enabled {
		return allDiagnostics
	}
	tracePerformance := diagnosticPerformanceTraceEnabled()
	var requestStarted time.Time
	if tracePerformance {
		requestStarted = time.Now()
	}
	for _, inspection := range s.inspections.inspections(document.SyntaxLanguage) {
		if ctx.Err() != nil {
			break
		}
		if !s.inspectionPresentedToClient(inspection.definition.ID) {
			continue
		}
		domain := inspectionDomain(inspection.definition.ID)
		if domain != "" && !s.domainEnabled(domain) ||
			!diagnosticInspectionEnabled(policy, inspection.definition.ID) ||
			!inspectionHasEnabledRule(inspection, policy) {
			continue
		}
		var inspectionStarted time.Time
		if tracePerformance {
			inspectionStarted = time.Now()
		}
		reporter := &inspectionProblemReporter{
			server:     s,
			document:   document,
			inspection: inspection,
			policy:     policy,
		}
		if err := inspection.inspection.Inspect(ctx, document, reporter); err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				break
			}
			log.Printf("Error getting diagnostics from inspection %s: %v", inspection.definition.ID, err)
			continue
		}
		allDiagnostics = append(allDiagnostics, reporter.diagnostics...)
		if tracePerformance {
			log.Printf(
				"Diagnostic inspection %s took %s (%d findings)",
				inspection.definition.ID,
				time.Since(inspectionStarted).Round(time.Microsecond),
				len(reporter.diagnostics),
			)
		}
	}
	var normalizeStarted time.Time
	if tracePerformance {
		normalizeStarted = time.Now()
	}
	result := normalizeDiagnostics(allDiagnostics)
	if tracePerformance {
		log.Printf(
			"Diagnostic request %s took %s (%d findings, normalization %s)",
			document.URI,
			time.Since(requestStarted).Round(time.Microsecond),
			len(result),
			time.Since(normalizeStarted).Round(time.Microsecond),
		)
	}
	return result
}

func diagnosticPerformanceTraceEnabled() bool {
	value := strings.TrimSpace(strings.ToLower(
		os.Getenv("SHOPWARE_LSP_TRACE_DIAGNOSTICS"),
	))
	return value != "" && value != "0" && value != "false" && value != "off"
}

type inspectionProblemReporter struct {
	server      *Server
	document    *TextDocument
	inspection  *registeredInspection
	policy      projectconfig.DiagnosticPolicy
	diagnostics []protocol.Diagnostic
}

func (r *inspectionProblemReporter) Report(problem Problem) error {
	definition, declared := r.inspection.problems[problem.ID]
	if !declared {
		return fmt.Errorf("inspection %q reported undeclared diagnostic %q", r.inspection.definition.ID, problem.ID)
	}
	if r.server != nil {
		if configured, found := diagnosticRuleSeverity(r.policy, problem.ID); found {
			if configured == projectconfig.SeverityOff {
				return nil
			}
		} else if definition.DisabledByDefault {
			return nil
		}
	}
	if problem.Range.Start > problem.Range.End ||
		problem.Range.End > uint32(len(r.document.Source)) {
		return fmt.Errorf("diagnostic %q has invalid range %s", problem.ID, problem.Range)
	}
	element := problem.Element
	if element == nil && r.document.SyntaxTree != nil {
		element = diagnosticElement(r.document.SyntaxTree.Root, problem.Range)
	}
	anchor, err := rewrite.NewElementHandle(
		r.document.URI,
		r.document.Version,
		r.document.SyntaxLanguage,
		element,
	)
	if err != nil {
		return fmt.Errorf("diagnostic %q anchor: %w", problem.ID, err)
	}
	payload, err := encodePayload(problem.Payload)
	if err != nil {
		return fmt.Errorf("diagnostic %q payload: %w", problem.ID, err)
	}
	fixes := make([]boundFixEnvelope, 0, len(problem.Fixes))
	for _, bound := range problem.Fixes {
		if _, found := r.inspection.fixes[bound.ID]; !found {
			return fmt.Errorf("diagnostic %q bound unregistered fix %q", problem.ID, bound.ID)
		}
		fixPayload, err := encodePayload(bound.Payload)
		if err != nil {
			return fmt.Errorf("diagnostic %q fix %q payload: %w", problem.ID, bound.ID, err)
		}
		fixes = append(fixes, boundFixEnvelope{ID: bound.ID, Payload: fixPayload})
	}
	envelope := diagnosticEnvelope{
		Schema:          diagnosticEnvelopeSchema,
		Inspection:      r.inspection.definition.ID,
		Code:            problem.ID,
		URI:             r.document.URI,
		DocumentVersion: r.document.Version,
		Anchor:          anchor,
		Payload:         payload,
		Fixes:           fixes,
	}
	data, err := encodeDiagnosticData(envelope)
	if err != nil {
		return fmt.Errorf("diagnostic %q data: %w", problem.ID, err)
	}
	severity := problem.Severity
	if severity == 0 {
		severity = definition.DefaultSeverity
	}
	if r.server != nil {
		if configured, found := diagnosticRuleSeverity(r.policy, problem.ID); found {
			severity = protocolDiagnosticSeverity(configured)
		}
	}
	source := problem.Source
	if source == "" {
		source = definition.Source
	}
	r.diagnostics = append(r.diagnostics, protocol.Diagnostic{
		Range:              protocolRangeFromText(r.document.LineIndex, problem.Range),
		Severity:           severity,
		Code:               string(problem.ID),
		Source:             source,
		Message:            problem.Message,
		Tags:               append([]protocol.DiagnosticTag(nil), problem.Tags...),
		RelatedInformation: append([]protocol.DiagnosticRelatedInformation(nil), problem.RelatedInformation...),
		Data:               data,
	})
	return nil
}

func inspectionHasEnabledRule(
	inspection *registeredInspection,
	policy projectconfig.DiagnosticPolicy,
) bool {
	if inspection == nil {
		return false
	}
	for id, definition := range inspection.problems {
		severity, configured := diagnosticRuleSeverity(policy, id)
		if configured && severity != projectconfig.SeverityOff ||
			!configured && !definition.DisabledByDefault {
			return true
		}
	}
	return false
}

func protocolRangeFromText(lineIndex *cst.LineIndex, rng cst.TextRange) protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Range{
		Start: protocol.Position{Line: int(startLine), Character: int(startCharacter)},
		End:   protocol.Position{Line: int(endLine), Character: int(endCharacter)},
	}
}

func textRangeFromProtocol(lineIndex *cst.LineIndex, rng protocol.Range) cst.TextRange {
	return cst.TextRange{
		Start: lineIndex.OffsetUTF16(uint32(rng.Start.Line), uint32(rng.Start.Character)),
		End:   lineIndex.OffsetUTF16(uint32(rng.End.Line), uint32(rng.End.Character)),
	}
}

func normalizeDiagnostics(values []protocol.Diagnostic) []protocol.Diagnostic {
	sort.SliceStable(values, func(i, j int) bool {
		left, right := values[i], values[j]
		if left.Range.Start.Line != right.Range.Start.Line {
			return left.Range.Start.Line < right.Range.Start.Line
		}
		if left.Range.Start.Character != right.Range.Start.Character {
			return left.Range.Start.Character < right.Range.Start.Character
		}
		if left.Range.End.Line != right.Range.End.Line {
			return left.Range.End.Line < right.Range.End.Line
		}
		if left.Range.End.Character != right.Range.End.Character {
			return left.Range.End.Character < right.Range.End.Character
		}
		leftCode, rightCode := fmt.Sprint(left.Code), fmt.Sprint(right.Code)
		if leftCode != rightCode {
			return leftCode < rightCode
		}
		return left.Message < right.Message
	})
	result := values[:0]
	for _, diagnostic := range values {
		if len(result) != 0 && sameDiagnostic(result[len(result)-1], diagnostic) {
			continue
		}
		result = append(result, diagnostic)
	}
	return result
}

func sameDiagnostic(left, right protocol.Diagnostic) bool {
	return left.Range == right.Range && fmt.Sprint(left.Code) == fmt.Sprint(right.Code) &&
		left.Source == right.Source && left.Message == right.Message
}
