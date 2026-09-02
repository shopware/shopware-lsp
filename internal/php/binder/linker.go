package binder

import (
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

// Link resolves references against one immutable workspace generation.
func Link(document *semantic.Document, snapshot *semantic.Snapshot) *semantic.Document {
	if document == nil {
		return nil
	}
	return LinkOwned(document.Clone(), snapshot)
}

// LinkOwned resolves references in a document exclusively owned by the
// caller. The indexing pipeline creates a fresh document and can transfer
// ownership through every analysis stage instead of deep-cloning its complete
// symbol, scope, reference, and type-fact graph.
func LinkOwned(
	document *semantic.Document,
	snapshot *semantic.Snapshot,
) *semantic.Document {
	if document == nil {
		return nil
	}
	result := document
	for index := range result.References {
		reference := &result.References[index]
		reference.ClearCandidateIDs()
		reference.Resolved = ""
		switch reference.Kind {
		case semantic.ClassName:
			for qualifiedIndex := 0; qualifiedIndex < reference.QualifiedNameCount(); qualifiedIndex++ {
				name := reference.QualifiedNameAt(qualifiedIndex)
				snapshot.VisitClassViews(name, func(candidate semantic.SymbolView) bool {
					reference.AddCandidate(candidate.ID())
					return true
				})
			}
		case semantic.FunctionName:
			for qualifiedIndex := 0; qualifiedIndex < reference.QualifiedNameCount(); qualifiedIndex++ {
				name := reference.QualifiedNameAt(qualifiedIndex)
				found := false
				snapshot.VisitFunctionViews(name, func(candidate semantic.SymbolView) bool {
					found = true
					reference.AddCandidate(candidate.ID())
					return true
				})
				if found {
					break
				}
			}
		case semantic.ConstantName:
			for qualifiedIndex := 0; qualifiedIndex < reference.QualifiedNameCount(); qualifiedIndex++ {
				name := reference.QualifiedNameAt(qualifiedIndex)
				found := false
				snapshot.VisitConstantViews(name, func(candidate semantic.SymbolView) bool {
					found = true
					reference.AddCandidate(candidate.ID())
					return true
				})
				if found {
					break
				}
			}
		case semantic.VariableName:
			appendLocalCandidateIDs(result, reference)
		case semantic.MemberName:
			memberResolver := resolver.MemberResolver{Snapshot: snapshot}
			visitCandidate := func(id semantic.SymbolID) bool {
				reference.AddCandidate(id)
				return true
			}
			switch reference.TargetKind {
			case semantic.MethodSymbol:
				memberResolver.VisitMethodIDs(
					reference.Receiver,
					reference.Name,
					visitCandidate,
				)
			case semantic.ClassConstantSymbol:
				memberResolver.VisitConstantIDs(
					reference.Receiver,
					reference.Name,
					visitCandidate,
				)
			default:
				memberResolver.VisitPropertyIDs(
					reference.Receiver,
					reference.Name,
					visitCandidate,
				)
			}
		}
		candidates := reference.CandidateIDs()
		if reference.Resolved == "" && len(candidates) == 1 {
			reference.Resolved = candidates[0]
			reference.ClearCandidateIDs()
		}
	}
	return result
}

func appendLocalCandidateIDs(
	document *semantic.Document,
	reference *semantic.Reference,
) {
	if reference == nil {
		return
	}
	if int(reference.Scope) >= len(document.Scopes) {
		return
	}
	name := reference.Name
	scope := reference.Scope
	for {
		current := document.Scopes[scope]
		if current.HasSymbol(document.Symbols, name) {
			for id := range current.SymbolIDs(document.Symbols, name) {
				reference.AddCandidate(id)
			}
			return
		}
		if current.ID == current.Parent {
			return
		}
		scope = current.Parent
	}
}
