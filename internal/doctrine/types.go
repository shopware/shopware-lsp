package doctrine

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

var builtInDoctrineTypes = []string{
	"array",
	"ascii_string",
	"bigint",
	"binary",
	"blob",
	"boolean",
	"collection",
	"date",
	"date_immutable",
	"dateinterval",
	"datetime",
	"datetime_immutable",
	"datetimetz",
	"datetimetz_immutable",
	"decimal",
	"enum",
	"file",
	"float",
	"guid",
	"hash",
	"id",
	"increment",
	"integer",
	"json",
	"json_array",
	"json_object",
	"jsonb",
	"jsonb_object",
	"number",
	"object",
	"raw",
	"simple_array",
	"smallfloat",
	"smallint",
	"string",
	"text",
	"time",
	"time_immutable",
	"timestamp",
}

type TypeDeclaration struct {
	Name   string
	Class  string
	File   string
	Range  cst.TextRange
	Family TypeFamily
}

type TypeFamily uint8

const (
	DBALTypeFamily TypeFamily = iota
	MongoDBTypeFamily
	CouchDBTypeFamily
)

func BuiltInTypes() []string {
	return append([]string(nil), builtInDoctrineTypes...)
}

// TypeDeclarations discovers conventional custom DBAL/ODM Type subclasses.
// Doctrine allows an arbitrary runtime registration name, but the conventional
// FooBarType => foo_bar spelling gives useful completion and navigation without
// executing project code.
func TypeDeclarations(index *php.PHPIndex) []TypeDeclaration {
	if index == nil {
		return nil
	}
	snapshot := index.SemanticSnapshot()
	var result []TypeDeclaration
	seen := make(map[string]struct{})
	for _, symbol := range index.ClassSymbols() {
		family := DBALTypeFamily
		matched := false
		for _, candidate := range []struct {
			base   string
			family TypeFamily
		}{
			{"Doctrine\\DBAL\\Types\\Type", DBALTypeFamily},
			{
				"Doctrine\\ODM\\MongoDB\\Types\\Type",
				MongoDBTypeFamily,
			},
			{
				"Doctrine\\ODM\\CouchDB\\Types\\Type",
				CouchDBTypeFamily,
			},
		} {
			if snapshot.IsSubtypeOf(symbol.FullyQualified, candidate.base) {
				family = candidate.family
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		name := doctrineTypeDeclaredName(index, symbol.FullyQualified)
		if name == "" {
			short := strings.TrimSuffix(symbol.Name, "Type")
			name = snakeCaseDoctrineType(short)
		}
		if name == "" {
			continue
		}
		key := strings.ToLower(name + "|" + symbol.FullyQualified)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, TypeDeclaration{
			Name:   name,
			Class:  symbol.FullyQualified,
			File:   symbol.Path,
			Range:  symbol.SelectionRange,
			Family: family,
		})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Name != result[right].Name {
			return result[left].Name < result[right].Name
		}
		return result[left].Class < result[right].Class
	})
	return result
}

func doctrineTypeDeclaredName(
	index *php.PHPIndex,
	class string,
) string {
	for _, method := range index.Methods(class) {
		if !strings.EqualFold(method.Name, "getName") {
			continue
		}
		for _, literal := range method.LiteralReturns() {
			if literal.Type.Kind() != types.LiteralStringKind {
				continue
			}
			if name := strings.TrimSpace(literal.Value); name != "" {
				return name
			}
		}
		for _, returned := range method.ConstantReturns() {
			receiver := doctrineConstantReturnReceiver(
				index,
				method,
				returned,
			)
			if receiver == "" {
				continue
			}
			for _, constant := range index.FindConstants(
				receiver,
				returned.Name,
			) {
				if constant.Type.Kind() != types.LiteralStringKind {
					continue
				}
				if name := strings.TrimSpace(constant.Type.Name()); name != "" {
					return name
				}
			}
		}
		return ""
	}
	return ""
}

func doctrineConstantReturnReceiver(
	index *php.PHPIndex,
	method semantic.Symbol,
	returned semantic.ConstantReturn,
) string {
	receiver := strings.TrimSpace(returned.Receiver)
	switch strings.ToLower(receiver) {
	case "self", "static", "parent":
		snapshot := index.SemanticSnapshot()
		owner, found := snapshot.Symbol(method.Container)
		if !found {
			return ""
		}
		if !strings.EqualFold(receiver, "parent") {
			return owner.FullyQualified
		}
		if len(owner.Extends()) != 0 {
			return owner.Extends()[0]
		}
		return ""
	default:
		return strings.TrimPrefix(receiver, "\\")
	}
}

// TypeDeclarationsForMapping applies the same filename manager convention as
// Doctrine's mapping drivers: *.orm.* sees DBAL types, *.mongodb.* and
// *.couchdb.* see their respective ODM types, and generic ODM/document files
// see both document stores. Files without a manager marker retain the safe
// all-types fallback.
func TypeDeclarationsForMapping(
	path string,
	declarations []TypeDeclaration,
) []TypeDeclaration {
	name := strings.ToLower(filepath.Base(path))
	var accepted map[TypeFamily]struct{}
	switch {
	case strings.Contains(name, ".orm."):
		accepted = map[TypeFamily]struct{}{DBALTypeFamily: {}}
	case strings.Contains(name, ".mongodb."):
		accepted = map[TypeFamily]struct{}{MongoDBTypeFamily: {}}
	case strings.Contains(name, ".couchdb."):
		accepted = map[TypeFamily]struct{}{CouchDBTypeFamily: {}}
	case strings.Contains(name, ".odm."),
		strings.Contains(name, ".document."):
		accepted = map[TypeFamily]struct{}{
			MongoDBTypeFamily: {},
			CouchDBTypeFamily: {},
		}
	default:
		return append([]TypeDeclaration(nil), declarations...)
	}
	result := make([]TypeDeclaration, 0, len(declarations))
	for _, declaration := range declarations {
		if _, exists := accepted[declaration.Family]; exists {
			result = append(result, declaration)
		}
	}
	return result
}

func IsKnownType(value string, custom []TypeDeclaration) bool {
	value = strings.TrimSpace(value)
	for _, builtIn := range builtInDoctrineTypes {
		if strings.EqualFold(builtIn, value) {
			return true
		}
	}
	for _, declaration := range custom {
		if strings.EqualFold(declaration.Name, value) {
			return true
		}
	}
	return false
}

func snakeCaseDoctrineType(value string) string {
	var result strings.Builder
	var previousLower bool
	for _, character := range value {
		if unicode.IsUpper(character) {
			if previousLower && result.Len() != 0 {
				result.WriteByte('_')
			}
			result.WriteRune(unicode.ToLower(character))
			previousLower = false
			continue
		}
		result.WriteRune(unicode.ToLower(character))
		previousLower = unicode.IsLetter(character) ||
			unicode.IsDigit(character)
	}
	return strings.Trim(result.String(), "_")
}
