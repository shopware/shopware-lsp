//go:build integration

package semantic

// TypeFactProfileStats exposes compact-table cardinality to integration
// profiling tests without adding production instrumentation to the hot path.
type TypeFactProfileStats struct {
	Total              int
	Detailed           int
	Common             int
	Declared           int
	InferredAssignment int
	InferredLiteral    int
	InferredSignature  int
	InferredFlow       int
	Other              int
}

// ProfileTypeFacts classifies the current detailed and compact fact tables.
// It is available only to integration builds.
func ProfileTypeFacts(document *Document) TypeFactProfileStats {
	var result TypeFactProfileStats
	if document == nil {
		return result
	}
	for _, fact := range document.TypeFacts {
		result.add(fact.Confidence, fact.Source, fact.Reason)
		result.Detailed++
	}
	for range document.compactTypeFacts.inferred {
		result.add(InferredConfidence, AssignmentSource, "")
	}
	for _, fact := range document.compactTypeFacts.packed {
		result.add(fact.Confidence, fact.Source, fact.Reason.String())
	}
	for _, fact := range document.compactTypeFacts.overflow {
		result.add(fact.Confidence, fact.Source, fact.Reason.String())
	}
	return result
}

func (stats *TypeFactProfileStats) add(
	confidence Confidence,
	source TypeSource,
	reason string,
) {
	stats.Total++
	if confidence == InferredConfidence &&
		source == AssignmentSource &&
		reason == "" {
		stats.Common++
	}
	switch {
	case confidence == DeclaredConfidence:
		stats.Declared++
	case confidence == InferredConfidence && source == AssignmentSource:
		stats.InferredAssignment++
	case confidence == InferredConfidence && source == LiteralSource:
		stats.InferredLiteral++
	case confidence == InferredConfidence && source == SignatureSource:
		stats.InferredSignature++
	case confidence == InferredConfidence && source == FlowSource:
		stats.InferredFlow++
	default:
		stats.Other++
	}
}
