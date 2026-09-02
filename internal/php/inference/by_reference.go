package inference

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func (s *functionState) applyByReferenceArguments(
	call *phpsyntax.Node,
	symbol semantic.Symbol,
	arguments []resolver.Argument,
	env environment,
) {
	hasByReference := false
	for _, parameter := range symbol.Parameters {
		if parameter.Flags.Has(semantic.ByReferenceFlag) {
			hasByReference = true
			break
		}
	}
	if !hasByReference {
		return
	}

	nodes := phpquery.Arguments(call)
	if len(nodes) == 0 {
		return
	}
	provided := make([]bool, len(symbol.Parameters))
	nextPositional := 0
	for argumentIndex, argument := range arguments {
		parameterIndex := -1
		if argument.Name != "" {
			name := strings.TrimPrefix(argument.Name, "$")
			for index, parameter := range symbol.Parameters {
				if strings.TrimPrefix(parameter.Name, "$") == name {
					parameterIndex = index
					break
				}
			}
			if parameterIndex < 0 && len(symbol.Parameters) > 0 &&
				symbol.Parameters[len(symbol.Parameters)-1].Flags.Has(
					semantic.VariadicFlag,
				) {
				parameterIndex = len(symbol.Parameters) - 1
			}
		} else {
			for nextPositional < len(symbol.Parameters) &&
				provided[nextPositional] &&
				!symbol.Parameters[nextPositional].Flags.Has(
					semantic.VariadicFlag,
				) {
				nextPositional++
			}
			if nextPositional < len(symbol.Parameters) {
				parameterIndex = nextPositional
			} else if len(symbol.Parameters) > 0 &&
				symbol.Parameters[len(symbol.Parameters)-1].Flags.Has(
					semantic.VariadicFlag,
				) {
				parameterIndex = len(symbol.Parameters) - 1
			}
		}
		if parameterIndex < 0 {
			continue
		}
		parameter := symbol.Parameters[parameterIndex]
		provided[parameterIndex] = true
		if !parameter.Flags.Has(semantic.VariadicFlag) {
			nextPositional = parameterIndex + 1
		}
		if !parameter.Flags.Has(semantic.ByReferenceFlag) ||
			argumentIndex >= len(nodes) {
			continue
		}
		value := lastDirectNode(nodes[argumentIndex])
		if value == nil || value.Kind() != phpsyntax.PhpVariable {
			continue
		}
		s.declareByReferenceOutput(value, parameter.Type, env)
	}
}

func (s *functionState) declareByReferenceOutput(
	node *phpsyntax.Node,
	value types.Type,
	env environment,
) {
	name := phpquery.VariableKey(node)
	if name == "" {
		return
	}
	if value.IsUnknown() {
		value = types.Mixed()
	}
	if previous, exists := env.get(name); exists {
		value = s.relations.Join(previous, value)
	}
	env.set(name, value)
	s.record(node, value, semantic.SignatureSource, "by-reference argument")

	scope, exists := s.document.ScopeAt(node.Range().Start)
	if !exists {
		return
	}
	for current := scope; ; current = s.document.Scopes[current.Parent] {
		if current.HasSymbol(s.document.Symbols, name) {
			s.updateLocalType(name, node.Range().Start, value)
			return
		}
		if current.ID == current.Parent ||
			int(current.Parent) >= len(s.document.Scopes) {
			break
		}
	}

	owner := scope.Owner
	fullyQualified := string(owner) + ":" + name
	symbol := semantic.Symbol{
		ID: semantic.NewSymbolID(
			semantic.LocalSymbol,
			fullyQualified,
			s.document.Path,
			node.Range().Start,
		),
		Kind:           semantic.LocalSymbol,
		Name:           name,
		FullyQualified: fullyQualified,
		Container:      owner,
		Path:           s.document.Path,
		Range:          node.RangeTrimmedTrivia(),
		SelectionRange: node.RangeTrimmedTrivia(),
		Type:           value,
	}
	symbolIndex := uint32(len(s.document.Symbols))
	s.document.Symbols = append(s.document.Symbols, symbol)
	s.document.Scopes[scope.ID].AddSymbol(symbolIndex)
}

func (s *analyzerState) relinkVariableReferences() {
	for index := range s.document.References {
		reference := &s.document.References[index]
		if reference.Kind != semantic.VariableName ||
			reference.Resolved != "" ||
			len(reference.CandidateIDs()) != 0 ||
			int(reference.Scope) >= len(s.document.Scopes) {
			continue
		}
		for scope := reference.Scope; ; scope = s.document.Scopes[scope].Parent {
			current := s.document.Scopes[scope]
			for id := range current.SymbolIDs(
				s.document.Symbols,
				reference.Name,
			) {
				reference.Resolved = id
				break
			}
			if reference.Resolved != "" ||
				current.ID == current.Parent ||
				int(current.Parent) >= len(s.document.Scopes) {
				break
			}
		}
	}
}
