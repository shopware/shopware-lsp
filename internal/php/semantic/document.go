package semantic

import (
	"iter"
	"maps"
	"slices"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php/literal"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

type ScopeID uint32

type ScopeKind uint8

const (
	FileScope ScopeKind = iota
	NamespaceScope
	ClassScope
	FunctionScope
	ClosureScope
	BlockScope
)

type NameKind uint8

const (
	ClassName NameKind = iota
	FunctionName
	ConstantName
	MemberName
	VariableName
)

// Scope contains declaration IDs keyed by the PHP-normalized lookup name.
type Scope struct {
	ID        ScopeID
	Kind      ScopeKind
	Parent    ScopeID
	Range     cst.TextRange
	Owner     SymbolID
	Namespace string
	Imports   ImportTable

	symbols []scopeSymbol
}

type scopeSymbol struct {
	name string
	id   SymbolID
}

// AddSymbol records a declaration in this scope. The compact entry list
// preserves insertion order and represents duplicate normalized names without
// a second map and per-name alternatives slice.
func (s *Scope) AddSymbol(name string, id SymbolID) {
	if s == nil {
		return
	}
	s.symbols = append(s.symbols, scopeSymbol{name: name, id: id})
}

// SymbolIDs iterates declarations matching one normalized name in insertion
// order without materializing a one-element slice for the common case.
func (s Scope) SymbolIDs(name string) iter.Seq[SymbolID] {
	return func(yield func(SymbolID) bool) {
		for _, symbol := range s.symbols {
			if symbol.name == name && !yield(symbol.id) {
				return
			}
		}
	}
}

// AllSymbolIDs iterates every declaration in insertion order.
func (s Scope) AllSymbolIDs() iter.Seq[SymbolID] {
	return func(yield func(SymbolID) bool) {
		for _, symbol := range s.symbols {
			if !yield(symbol.id) {
				return
			}
		}
	}
}

func (s Scope) HasSymbol(name string) bool {
	for _, symbol := range s.symbols {
		if symbol.name == name {
			return true
		}
	}
	return false
}

// AppendSymbolIDs appends declarations matching name in insertion order.
func (s Scope) AppendSymbolIDs(
	target []SymbolID,
	name string,
) []SymbolID {
	for _, symbol := range s.symbols {
		if symbol.name == name {
			target = append(target, symbol.id)
		}
	}
	return target
}

type ImportTable struct {
	Classes   map[string]string
	Functions map[string]string
	Constants map[string]string
}

// Reference is a syntactic name use, resolved or unresolved.
type Reference struct {
	Name       string
	Receiver   types.Type
	Resolved   SymbolID
	targets    *referenceTargets
	Range      cst.TextRange
	Scope      ScopeID
	Kind       NameKind
	TargetKind SymbolKind
	Static     bool
	Write      bool
}

// referenceTargets keeps collections off the common variable-reference
// record. Most references resolve through Scope and Resolved alone; class,
// function, and ambiguous references allocate this side record on demand.
type referenceTargets struct {
	singleQualified    [1]string
	extras             *referenceTargetExtras
	hasSingleQualified bool
}

// referenceTargetExtras keeps uncommon collections out of the single-name
// record. Class references overwhelmingly have one qualified target, while
// namespaced function fallbacks and ambiguous resolution need extra values.
type referenceTargetExtras struct {
	qualified  []string
	candidates *referenceCandidates
}

// referenceCandidates keeps the rare ambiguous-target slice out of every
// qualified-name side record. The binder creates hundreds of thousands of
// qualified records, while only a tiny minority are promoted past Resolved.
type referenceCandidates struct {
	values []SymbolID
}

// QualifiedNames returns ordered, resolver-normalized lookup candidates.
func (r Reference) QualifiedNames() []string {
	if r.targets == nil {
		return nil
	}
	if r.targets.hasSingleQualified {
		return r.targets.singleQualified[:]
	}
	if r.targets.extras == nil {
		return nil
	}
	return r.targets.extras.qualified
}

// QualifiedNameCount reports the number of resolver-normalized lookup
// candidates without materializing a slice.
func (r Reference) QualifiedNameCount() int {
	if r.targets == nil {
		return 0
	}
	if r.targets.hasSingleQualified {
		return 1
	}
	if r.targets.extras == nil {
		return 0
	}
	return len(r.targets.extras.qualified)
}

// QualifiedNameAt returns one resolver-normalized lookup candidate. Callers
// must pass an index smaller than QualifiedNameCount.
func (r Reference) QualifiedNameAt(index int) string {
	if r.targets.hasSingleQualified {
		return r.targets.singleQualified[index]
	}
	return r.targets.extras.qualified[index]
}

// SetQualifiedName replaces the qualified-name candidates with one value
// without allocating a one-element slice.
func (r *Reference) SetQualifiedName(name string) {
	if r == nil {
		return
	}
	targets := r.ensureTargets()
	targets.singleQualified[0] = name
	targets.hasSingleQualified = true
	if targets.extras != nil {
		targets.extras.qualified = nil
		targets.clearEmptyExtras()
	}
}

// SetQualifiedNames replaces the ordered qualified-name candidates. The
// caller transfers ownership of names, matching the former slice-field
// semantics.
func (r *Reference) SetQualifiedNames(names []string) {
	if r == nil {
		return
	}
	if len(names) == 0 {
		if r.targets == nil {
			return
		}
		r.targets.singleQualified[0] = ""
		r.targets.hasSingleQualified = false
		if r.targets.extras != nil {
			r.targets.extras.qualified = nil
			r.targets.clearEmptyExtras()
		}
		r.clearEmptyTargets()
		return
	}
	if len(names) == 1 {
		r.SetQualifiedName(names[0])
		return
	}
	targets := r.ensureTargets()
	targets.singleQualified[0] = ""
	targets.hasSingleQualified = false
	targets.ensureExtras().qualified = names
}

// CandidateIDs returns every ambiguous target in resolution order.
func (r Reference) CandidateIDs() []SymbolID {
	if r.targets == nil ||
		r.targets.extras == nil ||
		r.targets.extras.candidates == nil {
		return nil
	}
	return r.targets.extras.candidates.values
}

// SetCandidateIDs replaces the ambiguous resolution targets. The caller
// transfers ownership of candidates.
func (r *Reference) SetCandidateIDs(candidates []SymbolID) {
	if r == nil {
		return
	}
	if len(candidates) == 0 {
		r.ClearCandidateIDs()
		return
	}
	targets := r.ensureTargets()
	extras := targets.ensureExtras()
	if extras.candidates == nil {
		extras.candidates = &referenceCandidates{}
	}
	extras.candidates.values = candidates
}

// ClearCandidateIDs releases ambiguous targets and the side record when it no
// longer contains qualified names.
func (r *Reference) ClearCandidateIDs() {
	if r == nil || r.targets == nil || r.targets.extras == nil {
		return
	}
	r.targets.extras.candidates = nil
	r.targets.clearEmptyExtras()
	r.clearEmptyTargets()
}

func (r *Reference) ensureTargets() *referenceTargets {
	if r.targets == nil {
		r.targets = &referenceTargets{}
	}
	return r.targets
}

func (targets *referenceTargets) ensureExtras() *referenceTargetExtras {
	if targets.extras == nil {
		targets.extras = &referenceTargetExtras{}
	}
	return targets.extras
}

func (targets *referenceTargets) clearEmptyExtras() {
	if targets.extras != nil &&
		len(targets.extras.qualified) == 0 &&
		targets.extras.candidates == nil {
		targets.extras = nil
	}
}

func (r *Reference) clearEmptyTargets() {
	if r.targets != nil &&
		!r.targets.hasSingleQualified &&
		r.targets.extras == nil {
		r.targets = nil
	}
}

// AddCandidate stores the common unique target without allocating a candidate
// slice. A second target promotes both IDs to Candidates and clears Resolved.
func (r *Reference) AddCandidate(id SymbolID) {
	if r == nil || id == "" {
		return
	}
	candidates := r.CandidateIDs()
	if r.Resolved == "" && len(candidates) == 0 {
		r.Resolved = id
		return
	}
	if r.Resolved != "" {
		if len(candidates) == 0 {
			candidates = make([]SymbolID, 1, 2)
			candidates[0] = r.Resolved
		} else {
			candidates = append(candidates, r.Resolved)
		}
		r.Resolved = ""
	}
	r.SetCandidateIDs(append(candidates, id))
}

type NodeID struct {
	Kind  cst.Kind
	Start uint32
	End   uint32
}

func NodeIdentity(node *cst.Node) NodeID {
	if node == nil {
		return NodeID{}
	}
	rng := node.Range()
	return NodeID{Kind: node.Kind(), Start: rng.Start, End: rng.End}
}

type Confidence uint8

const (
	UnknownConfidence Confidence = iota
	InferredConfidence
	DocumentedConfidence
	DeclaredConfidence
)

type TypeSource uint8

const (
	UnknownSource TypeSource = iota
	LiteralSource
	AssignmentSource
	NativeSource
	PHPDocSource
	SignatureSource
	FlowSource
	FrameworkSource
)

type TypeFact struct {
	Type       types.Type
	Confidence Confidence
	Source     TypeSource
	Origin     cst.TextRange
	Reason     string
}

type compactTypeFact struct {
	Type       types.Type
	Confidence Confidence
	Source     TypeSource
	Reason     compactTypeFactReason
}

// compactTypeFactTable stores the overwhelmingly common short syntax ranges
// under a lossless 64-bit key: start(32), length(16), kind(16). Go map buckets
// otherwise spend twelve bytes on every NodeID key. Ordinary assignment
// inference uses a type-only map because its confidence, source, and empty
// reason are implicit. Annotated facts keep that metadata and a compact reason
// code in packed. Facts spanning more than 65,535 bytes are rare and retain
// the full identity in overflow. Scalar literal types are derived from syntax
// and need no entry.
type compactTypeFactTable struct {
	inferred map[uint64]types.Type
	packed   map[uint64]compactTypeFact
	overflow map[NodeID]compactTypeFact
}

func packCompactTypeFactIdentity(identity NodeID) (uint64, bool) {
	if identity.End < identity.Start {
		return 0, false
	}
	length := identity.End - identity.Start
	if length > ^uint32(0)>>16 {
		return 0, false
	}
	return uint64(identity.Start)<<32 |
		uint64(length)<<16 |
		uint64(identity.Kind), true
}

func (facts *compactTypeFactTable) initialized() bool {
	return facts != nil &&
		(facts.inferred != nil ||
			facts.packed != nil ||
			facts.overflow != nil)
}

func (facts *compactTypeFactTable) reserve(capacity int) {
	if facts == nil || facts.initialized() {
		return
	}
	if capacity < 0 {
		capacity = 0
	}
	// Real-world Shopware projects are consistently close to one-third plain
	// assignment facts and two-thirds annotated facts after built-in inference
	// reasons are encoded below.
	inferredCapacity := capacity / 3
	annotatedCapacity := capacity - inferredCapacity
	if inferredCapacity > 0 {
		facts.inferred = make(map[uint64]types.Type, inferredCapacity)
	}
	if annotatedCapacity > 0 {
		facts.packed = make(map[uint64]compactTypeFact, annotatedCapacity)
	}
}

func (facts *compactTypeFactTable) set(
	identity NodeID,
	fact compactTypeFact,
) {
	if facts == nil {
		return
	}
	if key, ok := packCompactTypeFactIdentity(identity); ok {
		switch {
		case fact.Confidence == InferredConfidence &&
			fact.Source == AssignmentSource &&
			fact.Reason == compactTypeFactNoReason:
			if facts.inferred == nil {
				facts.inferred = make(map[uint64]types.Type)
			}
			if _, exists := facts.inferred[key]; exists {
				facts.inferred[key] = fact.Type
				return
			}
			delete(facts.packed, key)
			facts.inferred[key] = fact.Type
		default:
			if facts.packed == nil {
				facts.packed = make(map[uint64]compactTypeFact)
			}
			if _, exists := facts.packed[key]; exists {
				facts.packed[key] = fact
				return
			}
			delete(facts.inferred, key)
			facts.packed[key] = fact
		}
		return
	}
	if facts.overflow == nil {
		facts.overflow = make(map[NodeID]compactTypeFact)
	}
	facts.overflow[identity] = fact
}

func (facts *compactTypeFactTable) get(
	identity NodeID,
) (compactTypeFact, bool) {
	if facts == nil {
		return compactTypeFact{}, false
	}
	if key, ok := packCompactTypeFactIdentity(identity); ok {
		if fact, found := facts.inferred[key]; found {
			return compactTypeFact{
				Type:       fact,
				Confidence: InferredConfidence,
				Source:     AssignmentSource,
			}, true
		}
		fact, found := facts.packed[key]
		return fact, found
	}
	fact, found := facts.overflow[identity]
	return fact, found
}

func (facts *compactTypeFactTable) delete(identity NodeID) {
	if facts == nil {
		return
	}
	if key, ok := packCompactTypeFactIdentity(identity); ok {
		delete(facts.inferred, key)
		delete(facts.packed, key)
		return
	}
	delete(facts.overflow, identity)
}

func (facts *compactTypeFactTable) count() int {
	if facts == nil {
		return 0
	}
	return len(facts.inferred) +
		len(facts.packed) +
		len(facts.overflow)
}

func (facts compactTypeFactTable) clone() compactTypeFactTable {
	return compactTypeFactTable{
		inferred: maps.Clone(facts.inferred),
		packed:   maps.Clone(facts.packed),
		overflow: maps.Clone(facts.overflow),
	}
}

type compactTypeFactReason uint8

const (
	compactTypeFactNoReason compactTypeFactReason = iota
	compactTypeFactAssignment
	compactTypeFactForeachValue
	compactTypeFactInstanceof
	compactTypeFactNullComparison
	compactTypeFactFlowExpression
	compactTypeFactConditionalPredicate
	compactTypeFactByReferenceArgument
	compactTypeFactLogicalCondition
	compactTypeFactTruthiness
	compactTypeFactPredicateIsString
	compactTypeFactPredicateIsInt
	compactTypeFactPredicateIsInteger
	compactTypeFactPredicateIsLong
	compactTypeFactPredicateIsFloat
	compactTypeFactPredicateIsDouble
	compactTypeFactPredicateIsReal
	compactTypeFactPredicateIsBool
	compactTypeFactPredicateIsArray
	compactTypeFactPredicateIsCallable
	compactTypeFactPredicateIsIterable
	compactTypeFactPredicateIsObject
	compactTypeFactPredicateIsNull
)

func compactTypeFactReasonFor(reason string) (compactTypeFactReason, bool) {
	switch reason {
	case "":
		return compactTypeFactNoReason, true
	case "assignment":
		return compactTypeFactAssignment, true
	case "foreach value":
		return compactTypeFactForeachValue, true
	case "instanceof":
		return compactTypeFactInstanceof, true
	case "null comparison":
		return compactTypeFactNullComparison, true
	case "flow expression":
		return compactTypeFactFlowExpression, true
	case "conditional predicate":
		return compactTypeFactConditionalPredicate, true
	case "by-reference argument":
		return compactTypeFactByReferenceArgument, true
	case "logical condition":
		return compactTypeFactLogicalCondition, true
	case "truthiness":
		return compactTypeFactTruthiness, true
	case "is_string":
		return compactTypeFactPredicateIsString, true
	case "is_int":
		return compactTypeFactPredicateIsInt, true
	case "is_integer":
		return compactTypeFactPredicateIsInteger, true
	case "is_long":
		return compactTypeFactPredicateIsLong, true
	case "is_float":
		return compactTypeFactPredicateIsFloat, true
	case "is_double":
		return compactTypeFactPredicateIsDouble, true
	case "is_real":
		return compactTypeFactPredicateIsReal, true
	case "is_bool":
		return compactTypeFactPredicateIsBool, true
	case "is_array":
		return compactTypeFactPredicateIsArray, true
	case "is_callable":
		return compactTypeFactPredicateIsCallable, true
	case "is_iterable":
		return compactTypeFactPredicateIsIterable, true
	case "is_object":
		return compactTypeFactPredicateIsObject, true
	case "is_null":
		return compactTypeFactPredicateIsNull, true
	default:
		return compactTypeFactNoReason, false
	}
}

func (r compactTypeFactReason) String() string {
	switch r {
	case compactTypeFactAssignment:
		return "assignment"
	case compactTypeFactForeachValue:
		return "foreach value"
	case compactTypeFactInstanceof:
		return "instanceof"
	case compactTypeFactNullComparison:
		return "null comparison"
	case compactTypeFactFlowExpression:
		return "flow expression"
	case compactTypeFactConditionalPredicate:
		return "conditional predicate"
	case compactTypeFactByReferenceArgument:
		return "by-reference argument"
	case compactTypeFactLogicalCondition:
		return "logical condition"
	case compactTypeFactTruthiness:
		return "truthiness"
	case compactTypeFactPredicateIsString:
		return "is_string"
	case compactTypeFactPredicateIsInt:
		return "is_int"
	case compactTypeFactPredicateIsInteger:
		return "is_integer"
	case compactTypeFactPredicateIsLong:
		return "is_long"
	case compactTypeFactPredicateIsFloat:
		return "is_float"
	case compactTypeFactPredicateIsDouble:
		return "is_double"
	case compactTypeFactPredicateIsReal:
		return "is_real"
	case compactTypeFactPredicateIsBool:
		return "is_bool"
	case compactTypeFactPredicateIsArray:
		return "is_array"
	case compactTypeFactPredicateIsCallable:
		return "is_callable"
	case compactTypeFactPredicateIsIterable:
		return "is_iterable"
	case compactTypeFactPredicateIsObject:
		return "is_object"
	case compactTypeFactPredicateIsNull:
		return "is_null"
	default:
		return ""
	}
}

type Issue struct {
	Range   cst.TextRange
	Code    string
	Message string
}

// Document is an immutable semantic snapshot for one source version.
type Document struct {
	Path              string
	Version           int
	WorkspaceRevision uint64
	Namespace         string
	Symbols           []Symbol
	Scopes            []Scope
	References        []Reference
	CallContracts     []CallContract
	TypeFacts         map[NodeID]TypeFact
	compactTypeFacts  compactTypeFactTable
	TypeAliases       map[string]types.Type
	Issues            []Issue
}

// WorkspaceGraph returns the compact declaration/reference view needed by a
// workspace Snapshot. File-local scopes, locals, parameters, closures, type
// facts, aliases, diagnostics, and variable references stay persisted and are
// loaded only when a caller requests that document. Nested data in retained
// symbols and references is shared and must remain immutable.
func (d *Document) WorkspaceGraph() *Document {
	if d == nil {
		return nil
	}
	interned := make(map[string]string)
	intern := func(value string) string {
		if value == "" {
			return ""
		}
		if existing, ok := interned[value]; ok {
			return existing
		}
		detached := strings.Clone(value)
		interned[detached] = detached
		return detached
	}
	typeDetacher := types.NewDetacher(intern)
	symbolCount := 0
	for _, symbol := range d.Symbols {
		if isWorkspaceSymbol(symbol.Kind) {
			symbolCount++
		}
	}
	symbols := make([]Symbol, 0, symbolCount)
	for _, symbol := range d.Symbols {
		if isWorkspaceSymbol(symbol.Kind) {
			symbols = append(
				symbols,
				detachWorkspaceSymbol(symbol, intern, typeDetacher),
			)
		}
	}
	referenceCount := 0
	for _, reference := range d.References {
		if reference.Kind != VariableName {
			referenceCount++
		}
	}
	references := make([]Reference, 0, referenceCount)
	for _, reference := range d.References {
		if reference.Kind != VariableName {
			references = append(
				references,
				detachWorkspaceReference(reference, intern, typeDetacher),
			)
		}
	}
	return &Document{
		Path:              intern(d.Path),
		Version:           d.Version,
		WorkspaceRevision: d.WorkspaceRevision,
		Namespace:         intern(d.Namespace),
		Symbols:           symbols,
		References:        references,
		CallContracts:     cloneCallContracts(d.CallContracts),
	}
}

func detachWorkspaceSymbol(
	symbol Symbol,
	intern func(string) string,
	typeDetacher *types.Detacher,
) Symbol {
	symbol.ID = SymbolID(intern(string(symbol.ID)))
	symbol.Name = intern(symbol.Name)
	symbol.FullyQualified = intern(symbol.FullyQualified)
	symbol.Container = SymbolID(intern(string(symbol.Container)))
	symbol.Path = intern(symbol.Path)
	symbol.Type = typeDetacher.Type(symbol.Type)
	symbol.NativeType = typeDetacher.Type(symbol.NativeType)
	symbol.DocType = typeDetacher.Type(symbol.DocType)
	symbol.ReturnType = typeDetacher.Type(symbol.ReturnType)
	for index := range symbol.Parameters {
		if index == 0 {
			symbol.Parameters = slices.Clone(symbol.Parameters)
		}
		parameter := &symbol.Parameters[index]
		parameter.ID = SymbolID(intern(string(parameter.ID)))
		parameter.Name = intern(parameter.Name)
		parameter.Type = typeDetacher.Type(parameter.Type)
		parameter.NativeType = typeDetacher.Type(parameter.NativeType)
		parameter.DocType = typeDetacher.Type(parameter.DocType)
		parameter.AssistantTags = internStrings(parameter.AssistantTags, intern)
		parameter.Attributes = detachAttributes(parameter.Attributes, intern)
		if parameter.DefaultValue != nil {
			value := cloneAttributeValue(*parameter.DefaultValue)
			detachAttributeValue(&value, intern)
			parameter.DefaultValue = &value
		}
	}
	symbol.Templates = slices.Clone(symbol.Templates)
	for index := range symbol.Templates {
		template := &symbol.Templates[index]
		template.Name = intern(template.Name)
		template.Bound = typeDetacher.Type(template.Bound)
		template.Default = typeDetacher.Type(template.Default)
	}
	symbol.Extends = internStrings(symbol.Extends, intern)
	symbol.Implements = internStrings(symbol.Implements, intern)
	symbol.Traits = internStrings(symbol.Traits, intern)
	symbol.ExtendsTypes = detachTypes(symbol.ExtendsTypes, typeDetacher)
	symbol.ImplementsTypes = detachTypes(symbol.ImplementsTypes, typeDetacher)
	symbol.TraitTypes = detachTypes(symbol.TraitTypes, typeDetacher)
	symbol.TraitAliases = slices.Clone(symbol.TraitAliases)
	for index := range symbol.TraitAliases {
		alias := &symbol.TraitAliases[index]
		alias.Trait = intern(alias.Trait)
		alias.Method = intern(alias.Method)
		alias.Alias = intern(alias.Alias)
	}
	symbol.Throws = detachTypes(symbol.Throws, typeDetacher)
	symbol.Assertions = slices.Clone(symbol.Assertions)
	for index := range symbol.Assertions {
		assertion := &symbol.Assertions[index]
		assertion.Target = intern(assertion.Target)
		assertion.Type = typeDetacher.Type(assertion.Type)
	}
	symbol.Attributes = detachAttributes(symbol.Attributes, intern)
	symbol.ConstantArray = slices.Clone(symbol.ConstantArray)
	for index := range symbol.ConstantArray {
		item := &symbol.ConstantArray[index]
		item.Key = intern(item.Key)
		item.Value = intern(item.Value)
		item.Type = typeDetacher.Type(item.Type)
	}
	symbol.LiteralReturns = slices.Clone(symbol.LiteralReturns)
	for index := range symbol.LiteralReturns {
		symbol.LiteralReturns[index].Value = intern(
			symbol.LiteralReturns[index].Value,
		)
		symbol.LiteralReturns[index].Type = typeDetacher.Type(
			symbol.LiteralReturns[index].Type,
		)
	}
	symbol.ConstantReturns = slices.Clone(symbol.ConstantReturns)
	for index := range symbol.ConstantReturns {
		item := &symbol.ConstantReturns[index]
		item.Receiver = intern(item.Receiver)
		item.Name = intern(item.Name)
	}
	symbol.DocSummary = intern(symbol.DocSummary)
	return symbol
}

func detachWorkspaceReference(
	reference Reference,
	intern func(string) string,
	typeDetacher *types.Detacher,
) Reference {
	// Scope IDs address file-local scope tables, which are deliberately absent
	// from the retained workspace graph.
	reference.Scope = 0
	reference.Name = intern(reference.Name)
	reference.Receiver = typeDetacher.Type(reference.Receiver)
	reference.Resolved = SymbolID(intern(string(reference.Resolved)))
	qualified := make([]string, reference.QualifiedNameCount())
	for index := range qualified {
		qualified[index] = intern(reference.QualifiedNameAt(index))
	}
	candidates := slices.Clone(reference.CandidateIDs())
	for index := range candidates {
		candidates[index] = SymbolID(
			intern(string(candidates[index])),
		)
	}
	reference.targets = nil
	reference.SetQualifiedNames(qualified)
	reference.SetCandidateIDs(candidates)
	return reference
}

func detachTypes(values []types.Type, detacher *types.Detacher) []types.Type {
	result := slices.Clone(values)
	for index := range result {
		result[index] = detacher.Type(result[index])
	}
	return result
}

func internStringsOwned(values []string, intern func(string) string) {
	for index := range values {
		values[index] = intern(values[index])
	}
}

func internTypesOwned(
	values []types.Type,
	intern func(types.Type) types.Type,
) {
	for index := range values {
		values[index] = intern(values[index])
	}
}

func internStrings(
	values []string,
	intern func(string) string,
) []string {
	result := slices.Clone(values)
	for index := range result {
		result[index] = intern(result[index])
	}
	return result
}

func isWorkspaceSymbol(kind SymbolKind) bool {
	switch kind {
	case ClassSymbol,
		InterfaceSymbol,
		TraitSymbol,
		EnumSymbol,
		MethodSymbol,
		FunctionSymbol,
		PropertySymbol,
		ClassConstantSymbol,
		GlobalConstantSymbol,
		EnumCaseSymbol,
		TypeAliasSymbol:
		return true
	default:
		return false
	}
}

func (d *Document) TypeOf(node *cst.Node) TypeFact {
	if d == nil || node == nil {
		return TypeFact{Type: types.Unknown()}
	}
	if fact, ok := d.TypeFact(NodeIdentity(node)); ok {
		if fact.Origin == (cst.TextRange{}) {
			fact.Origin = node.RangeTrimmedTrivia()
		}
		return fact
	}
	if value, ok := literal.TypeOf(node); ok {
		return TypeFact{
			Type:       value,
			Confidence: InferredConfidence,
			Source:     LiteralSource,
			Origin:     node.RangeTrimmedTrivia(),
		}
	}
	return TypeFact{Type: types.Unknown()}
}

// TypeFact returns the stored fact for a syntax identity. Common facts without
// explanatory text use a compact side map and are expanded on demand.
func (d *Document) TypeFact(identity NodeID) (TypeFact, bool) {
	if d == nil {
		return TypeFact{}, false
	}
	if fact, ok := d.TypeFacts[identity]; ok {
		return fact, true
	}
	fact, ok := d.compactTypeFacts.get(identity)
	if !ok {
		return TypeFact{}, false
	}
	return TypeFact{
		Type:       fact.Type,
		Confidence: fact.Confidence,
		Source:     fact.Source,
		Reason:     fact.Reason.String(),
	}, true
}

// ReserveTypeFacts prepares compact storage for the estimated non-literal fact
// count. It is intended for parsers that know the syntax shape before
// inference.
func (d *Document) ReserveTypeFacts(capacity int) {
	if d == nil {
		return
	}
	d.compactTypeFacts.reserve(capacity)
}

// SetTypeFact stores common facts in the compact map and keeps uncommon
// explanatory text in the detailed side map.
func (d *Document) SetTypeFact(identity NodeID, fact TypeFact) {
	if d == nil {
		return
	}
	if reason, compact := compactTypeFactReasonFor(fact.Reason); compact {
		d.compactTypeFacts.set(identity, compactTypeFact{
			Type:       fact.Type,
			Confidence: fact.Confidence,
			Source:     fact.Source,
			Reason:     reason,
		})
		delete(d.TypeFacts, identity)
		return
	}
	if d.TypeFacts == nil {
		d.TypeFacts = make(map[NodeID]TypeFact)
	}
	d.TypeFacts[identity] = fact
	d.compactTypeFacts.delete(identity)
}

// DeleteTypeFact removes either representation of a syntax fact.
func (d *Document) DeleteTypeFact(identity NodeID) {
	if d == nil {
		return
	}
	delete(d.TypeFacts, identity)
	d.compactTypeFacts.delete(identity)
}

// TypeFactCount reports detailed and compact syntax facts.
func (d *Document) TypeFactCount() int {
	if d == nil {
		return 0
	}
	return len(d.TypeFacts) + d.compactTypeFacts.count()
}

func (d *Document) ScopeAt(offset uint32) (Scope, bool) {
	if d == nil {
		return Scope{}, false
	}
	best := -1
	var result Scope
	for _, scope := range d.Scopes {
		if offset < scope.Range.Start || offset > scope.Range.End {
			continue
		}
		width := int(scope.Range.End - scope.Range.Start)
		if best < 0 || width < best {
			best = width
			result = scope
		}
	}
	return result, best >= 0
}

func (d *Document) Clone() *Document {
	if d == nil {
		return nil
	}
	result := *d
	result.Symbols = cloneSymbols(d.Symbols)
	result.Scopes = make([]Scope, len(d.Scopes))
	for index, scope := range d.Scopes {
		result.Scopes[index] = scope
		result.Scopes[index].symbols = slices.Clone(scope.symbols)
		result.Scopes[index].Imports = cloneImports(scope.Imports)
	}
	result.References = cloneReferences(d.References)
	result.CallContracts = cloneCallContracts(d.CallContracts)
	result.TypeFacts = maps.Clone(d.TypeFacts)
	result.compactTypeFacts = d.compactTypeFacts.clone()
	result.TypeAliases = maps.Clone(d.TypeAliases)
	result.Issues = slices.Clone(d.Issues)
	return &result
}

func cloneSymbols(symbols []Symbol) []Symbol {
	result := slices.Clone(symbols)
	for index := range result {
		result[index] = cloneSymbol(symbols[index])
	}
	return result
}

func cloneSymbol(symbol Symbol) Symbol {
	result := symbol
	result.Parameters = slices.Clone(symbol.Parameters)
	for parameterIndex := range result.Parameters {
		result.Parameters[parameterIndex].AssistantTags = slices.Clone(
			symbol.Parameters[parameterIndex].AssistantTags,
		)
		result.Parameters[parameterIndex].Attributes = cloneAttributes(
			symbol.Parameters[parameterIndex].Attributes,
		)
		if symbol.Parameters[parameterIndex].DefaultValue != nil {
			value := cloneAttributeValue(
				*symbol.Parameters[parameterIndex].DefaultValue,
			)
			result.Parameters[parameterIndex].DefaultValue = &value
		}
	}
	result.Templates = slices.Clone(symbol.Templates)
	result.Extends = slices.Clone(symbol.Extends)
	result.Implements = slices.Clone(symbol.Implements)
	result.Traits = slices.Clone(symbol.Traits)
	result.ExtendsTypes = slices.Clone(symbol.ExtendsTypes)
	result.ImplementsTypes = slices.Clone(symbol.ImplementsTypes)
	result.TraitTypes = slices.Clone(symbol.TraitTypes)
	result.TraitAliases = slices.Clone(symbol.TraitAliases)
	result.Throws = slices.Clone(symbol.Throws)
	result.Assertions = slices.Clone(symbol.Assertions)
	result.Attributes = cloneAttributes(symbol.Attributes)
	result.ConstantArray = slices.Clone(symbol.ConstantArray)
	result.LiteralReturns = slices.Clone(symbol.LiteralReturns)
	result.ConstantReturns = slices.Clone(symbol.ConstantReturns)
	return result
}

func cloneAttributes(source []Attribute) []Attribute {
	if source == nil {
		return nil
	}
	result := slices.Clone(source)
	for attributeIndex := range result {
		arguments := slices.Clone(source[attributeIndex].Arguments)
		result[attributeIndex].Arguments = arguments
		for argumentIndex := range arguments {
			arguments[argumentIndex].Value = cloneAttributeValue(
				source[attributeIndex].Arguments[argumentIndex].Value,
			)
		}
	}
	return result
}

func cloneAttributeValue(source AttributeValue) AttributeValue {
	result := source
	result.Items = slices.Clone(source.Items)
	for index := range result.Items {
		result.Items[index].Key = cloneAttributeValue(source.Items[index].Key)
		result.Items[index].Value = cloneAttributeValue(source.Items[index].Value)
	}
	return result
}

func detachAttributes(
	source []Attribute,
	intern func(string) string,
) []Attribute {
	result := cloneAttributes(source)
	for attributeIndex := range result {
		attribute := &result[attributeIndex]
		attribute.Name = intern(attribute.Name)
		for argumentIndex := range attribute.Arguments {
			argument := &attribute.Arguments[argumentIndex]
			argument.Name = intern(argument.Name)
			detachAttributeValue(&argument.Value, intern)
		}
	}
	return result
}

func detachAttributeValue(
	value *AttributeValue,
	intern func(string) string,
) {
	if value == nil {
		return
	}
	value.Value = intern(value.Value)
	value.Expression = intern(value.Expression)
	for index := range value.Items {
		detachAttributeValue(&value.Items[index].Key, intern)
		detachAttributeValue(&value.Items[index].Value, intern)
	}
}

func cloneImports(source ImportTable) ImportTable {
	return ImportTable{
		Classes:   maps.Clone(source.Classes),
		Functions: maps.Clone(source.Functions),
		Constants: maps.Clone(source.Constants),
	}
}
