package console

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

const (
	HelperInterface = "Symfony\\Component\\Console\\Helper\\HelperInterface"
	HelperSetClass  = "Symfony\\Component\\Console\\Helper\\HelperSet"
	CommandClass    = "Symfony\\Component\\Console\\Command\\Command"
)

type Helper struct {
	Name    string
	Class   string
	File    string
	Range   cst.TextRange
	Summary string
}

type HelperReference struct {
	Name  string
	Range cst.TextRange
	Call  *phpsyntax.Node
}

// HelperCatalog caches helper discovery for one immutable PHP workspace
// generation. Completion is requested repeatedly while typing, so the class
// graph should be traversed only after the semantic revision changes.
type HelperCatalog struct {
	index *php.PHPIndex

	mu       sync.Mutex
	valid    bool
	revision uint64
	helpers  []Helper
}

func NewHelperCatalog(index *php.PHPIndex) *HelperCatalog {
	return &HelperCatalog{index: index}
}

func (catalog *HelperCatalog) Helpers() []Helper {
	if catalog == nil || catalog.index == nil {
		return nil
	}
	revision := catalog.index.SemanticSnapshot().Revision
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	if !catalog.valid || catalog.revision != revision {
		catalog.helpers = Helpers(catalog.index)
		catalog.revision = revision
		catalog.valid = true
	}
	return slices.Clone(catalog.helpers)
}

// Helpers resolves concrete Console helpers from the shared PHP semantic
// snapshot. A helper name is intentionally accepted only when getName()
// returns a direct string literal, keeping completion deterministic without
// evaluating arbitrary PHP.
func Helpers(index *php.PHPIndex) []Helper {
	if index == nil {
		return nil
	}
	snapshot := index.SemanticSnapshot()
	var result []Helper
	seen := make(map[string]struct{})
	for _, class := range snapshot.GlobalSymbols() {
		if class.Kind != semantic.ClassSymbol ||
			class.Flags.Has(semantic.AbstractFlag) ||
			!snapshot.IsSubtypeOf(class.FullyQualified, HelperInterface) {
			continue
		}
		for _, method := range index.FindMethods(class.FullyQualified, "getName") {
			// Inherited implementations do not define another helper key. This
			// avoids duplicate navigation targets for subclasses which merely
			// reuse their parent's registered name.
			if method.Container != class.ID {
				continue
			}
			for _, literal := range method.LiteralReturns() {
				if literal.Type.Kind() != types.LiteralStringKind ||
					strings.TrimSpace(literal.Value) == "" {
					continue
				}
				key := strings.ToLower(literal.Value) + "\x00" +
					strings.ToLower(class.FullyQualified)
				if _, duplicate := seen[key]; duplicate {
					continue
				}
				seen[key] = struct{}{}
				rng := class.SelectionRange
				if rng.Len() == 0 {
					rng = class.Range
				}
				summary := class.DocSummary()
				if summary == "" {
					summary = method.DocSummary()
				}
				result = append(result, Helper{
					Name:    literal.Value,
					Class:   class.FullyQualified,
					File:    class.Path,
					Range:   rng,
					Summary: summary,
				})
			}
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Name != result[right].Name {
			return strings.ToLower(result[left].Name) <
				strings.ToLower(result[right].Name)
		}
		if result[left].Class != result[right].Class {
			return strings.ToLower(result[left].Class) <
				strings.ToLower(result[right].Class)
		}
		if result[left].File != result[right].File {
			return result[left].File < result[right].File
		}
		return result[left].Range.Start < result[right].Range.Start
	})
	return result
}

func HelperReferenceAt(node *phpsyntax.Node) (HelperReference, bool) {
	literal := phpquery.StringAt(node)
	if literal == nil {
		return HelperReference{}, false
	}
	call := phpquery.CallAt(literal)
	if call == nil ||
		phpquery.ArgumentIndex(call, literal) != 0 ||
		phpquery.ArgumentExpression(call, 0) != literal {
		return HelperReference{}, false
	}
	switch strings.ToLower(phpquery.CallMethodName(call)) {
	case "get", "has", "gethelper":
	default:
		return HelperReference{}, false
	}
	return HelperReference{
		Name:  phpquery.StringValue(literal),
		Range: phpquery.StringContentRange(literal),
		Call:  call,
	}, true
}

func ValidateHelperReference(
	ctx context.Context,
	index *php.PHPIndex,
	reference HelperReference,
	source []byte,
) bool {
	if index == nil || reference.Call == nil {
		return false
	}
	target := ""
	switch strings.ToLower(phpquery.CallMethodName(reference.Call)) {
	case "get", "has":
		target = HelperSetClass
	case "gethelper":
		target = CommandClass
	default:
		return false
	}
	return index.IsMethodCalledOnClass(
		ctx,
		reference.Call,
		source,
		target,
	)
}
