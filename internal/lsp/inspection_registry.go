package lsp

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/rewrite"
)

const diagnosticEnvelopeKey = "_shopwareLSP"
const diagnosticEnvelopeSchema = 1

type boundFixEnvelope struct {
	ID      FixID           `json:"id"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type diagnosticEnvelope struct {
	Schema          int                   `json:"schema"`
	Inspection      string                `json:"inspection"`
	Code            DiagnosticID          `json:"code"`
	URI             string                `json:"uri"`
	DocumentVersion int                   `json:"documentVersion"`
	Anchor          rewrite.ElementHandle `json:"anchor"`
	Payload         json.RawMessage       `json:"payload,omitempty"`
	Fixes           []boundFixEnvelope    `json:"fixes,omitempty"`
}

type registeredInspection struct {
	inspection Inspection
	definition InspectionDefinition
	problems   map[DiagnosticID]ProblemDefinition
	fixes      map[FixID]QuickFix
}

type inspectionRegistry struct {
	byID       map[string]*registeredInspection
	byCode     map[DiagnosticID]*registeredInspection
	byLanguage map[language.ID][]*registeredInspection
}

func newInspectionRegistry() *inspectionRegistry {
	return &inspectionRegistry{
		byID:       make(map[string]*registeredInspection),
		byCode:     make(map[DiagnosticID]*registeredInspection),
		byLanguage: make(map[language.ID][]*registeredInspection),
	}
}

func (r *inspectionRegistry) register(inspection Inspection) error {
	if inspection == nil {
		return fmt.Errorf("inspection must not be nil")
	}
	definition := inspection.Definition()
	if definition.ID == "" {
		return fmt.Errorf("inspection ID must not be empty")
	}
	if _, exists := r.byID[definition.ID]; exists {
		return fmt.Errorf("inspection %q is registered more than once", definition.ID)
	}
	if len(definition.Languages) == 0 {
		return fmt.Errorf("inspection %q has no languages", definition.ID)
	}
	registered := &registeredInspection{
		inspection: inspection,
		definition: definition,
		problems:   make(map[DiagnosticID]ProblemDefinition),
		fixes:      make(map[FixID]QuickFix),
	}
	for _, problem := range definition.Problems {
		if problem.ID == "" {
			return fmt.Errorf("inspection %q has an empty diagnostic ID", definition.ID)
		}
		if problem.Source == "" {
			return fmt.Errorf("diagnostic %q has no source", problem.ID)
		}
		if _, exists := registered.problems[problem.ID]; exists {
			return fmt.Errorf("inspection %q declares diagnostic %q twice", definition.ID, problem.ID)
		}
		if owner, exists := r.byCode[problem.ID]; exists {
			return fmt.Errorf("diagnostic %q belongs to both %q and %q", problem.ID, owner.definition.ID, definition.ID)
		}
		registered.problems[problem.ID] = problem
	}
	if len(registered.problems) == 0 {
		return fmt.Errorf("inspection %q has no diagnostics", definition.ID)
	}
	for _, fix := range inspection.QuickFixes() {
		if fix == nil || fix.ID() == "" {
			return fmt.Errorf("inspection %q has an invalid quick fix", definition.ID)
		}
		if _, exists := registered.fixes[fix.ID()]; exists {
			return fmt.Errorf("inspection %q declares quick fix %q twice", definition.ID, fix.ID())
		}
		registered.fixes[fix.ID()] = fix
	}

	seenLanguages := make(map[language.ID]struct{}, len(definition.Languages))
	languages := make([]language.ID, 0, len(definition.Languages))
	for _, languageID := range definition.Languages {
		if languageID == "" {
			return fmt.Errorf("inspection %q has an empty language", definition.ID)
		}
		if _, exists := seenLanguages[languageID]; exists {
			continue
		}
		seenLanguages[languageID] = struct{}{}
		languages = append(languages, languageID)
	}

	// Publish only after the complete definition has been validated so a
	// failed registration cannot leave a partially populated registry.
	r.byID[definition.ID] = registered
	for code := range registered.problems {
		r.byCode[code] = registered
	}
	for _, languageID := range languages {
		r.byLanguage[languageID] = append(r.byLanguage[languageID], registered)
	}
	return nil
}

func (r *inspectionRegistry) inspection(id string) (*registeredInspection, bool) {
	value, found := r.byID[id]
	return value, found
}

func (r *inspectionRegistry) inspections(languageID language.ID) []*registeredInspection {
	return r.byLanguage[languageID]
}

func encodeDiagnosticData(envelope diagnosticEnvelope) (map[string]any, error) {
	metadata, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	var metadataValue map[string]any
	if err := json.Unmarshal(metadata, &metadataValue); err != nil {
		return nil, err
	}
	return map[string]any{diagnosticEnvelopeKey: metadataValue}, nil
}

func decodeDiagnosticEnvelope(value any) (diagnosticEnvelope, error) {
	data, ok := value.(map[string]any)
	if !ok {
		encoded, err := json.Marshal(value)
		if err != nil {
			return diagnosticEnvelope{}, err
		}
		if err := json.Unmarshal(encoded, &data); err != nil {
			return diagnosticEnvelope{}, err
		}
	}
	metadata, exists := data[diagnosticEnvelopeKey]
	if !exists {
		return diagnosticEnvelope{}, fmt.Errorf("diagnostic has no Shopware LSP envelope")
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return diagnosticEnvelope{}, err
	}
	var envelope diagnosticEnvelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return diagnosticEnvelope{}, err
	}
	if envelope.Schema != diagnosticEnvelopeSchema {
		return diagnosticEnvelope{}, fmt.Errorf("unsupported diagnostic envelope schema %d", envelope.Schema)
	}
	return envelope, nil
}

func encodePayload(value any) (json.RawMessage, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	return json.RawMessage(encoded), err
}

func diagnosticElement(root *cst.Node, rng cst.TextRange) cst.Element {
	if root == nil {
		return nil
	}
	return root.DescendantForRange(rng)
}

func supportsLanguage(definition InspectionDefinition, languageID language.ID) bool {
	return slices.Contains(definition.Languages, languageID)
}
