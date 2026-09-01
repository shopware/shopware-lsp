package inference

import (
	"slices"
	"strconv"
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/phpdoc"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/suppression"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

type Analyzer struct {
	Snapshot   *semantic.Snapshot
	Extensions []Extension

	observeFunction func(semantic.SymbolID)
}

func New(snapshot *semantic.Snapshot, extensions ...Extension) *Analyzer {
	if snapshot == nil {
		snapshot = semantic.NewSnapshot(0, nil)
	}
	return &Analyzer{Snapshot: snapshot, Extensions: extensions}
}

func (a *Analyzer) Analyze(
	document *semantic.Document,
	root *phpsyntax.Node,
) *semantic.Document {
	if document == nil {
		return nil
	}
	return a.AnalyzeOwned(document.Clone(), root)
}

// AnalyzeOwned enriches a document exclusively owned by the caller. Use this
// in the indexing pipeline, where binder output has not been published and
// does not require a defensive copy.
func (a *Analyzer) AnalyzeOwned(
	document *semantic.Document,
	root *phpsyntax.Node,
) *semantic.Document {
	if document == nil {
		return nil
	}
	result := document
	if root == nil {
		return result
	}
	localAnalyzer := *a
	source := root.Text()
	state := analyzerState{
		analyzer:              &localAnalyzer,
		document:              result,
		relations:             localAnalyzer.Snapshot.Relations(),
		issues:                make(map[string]struct{}),
		suppressions:          suppression.Parse(source),
		readonlyPropertyTypes: make(map[semantic.SymbolID]types.Type),
	}
	functions, functionIndices := indexedFunctions(result, root)
	state.functionIndices = functionIndices

	const maximumInferencePasses = 8
	analyses := make([]functionAnalysis, len(functions))
	scheduled := make([]int, len(functions))
	for index := range functions {
		scheduled[index] = index
	}
	for pass := 0; pass < maximumInferencePasses && len(scheduled) > 0; pass++ {
		changed := make(map[semantic.SymbolID]struct{})
		for _, functionIndex := range scheduled {
			function := functions[functionIndex]
			before := result.Symbols[function.symbolIndex].ReturnType
			analyses[functionIndex] = state.analyzeFunction(function)
			after := result.Symbols[function.symbolIndex].ReturnType
			if !before.Equal(after) {
				changed[function.id] = struct{}{}
			}
		}
		if len(changed) == 0 {
			break
		}
		state.analyzer.Snapshot = state.analyzer.Snapshot.
			WithUpdatedFunctionReturns(result)
		state.relations = state.analyzer.Snapshot.Relations()
		if pass+1 == maximumInferencePasses {
			break
		}
		scheduled = dependentFunctions(analyses, changed)
	}
	state.analyzeTopLevel(root)
	state.validateDeclarations()
	state.relinkVariableReferences()
	return result
}

type indexedFunction struct {
	node        *phpsyntax.Node
	id          semantic.SymbolID
	symbolIndex int
}

type functionAnalysis struct {
	dependencies map[semantic.SymbolID]struct{}
}

func indexedFunctions(
	document *semantic.Document,
	root *phpsyntax.Node,
) ([]indexedFunction, map[semantic.SymbolID]int) {
	functionCount := 0
	for index := range document.Symbols {
		symbol := document.Symbols[index]
		if symbol.Kind == semantic.MethodSymbol ||
			symbol.Kind == semantic.FunctionSymbol {
			functionCount++
		}
	}
	symbolAt := make(map[uint32]int, functionCount)
	for index := range document.Symbols {
		symbol := document.Symbols[index]
		if symbol.Kind == semantic.MethodSymbol ||
			symbol.Kind == semantic.FunctionSymbol {
			symbolAt[symbol.Range.Start] = index
		}
	}
	result := make([]indexedFunction, 0, functionCount)
	indices := make(map[semantic.SymbolID]int, functionCount)
	phpquery.Visit(
		root,
		func(node *phpsyntax.Node) bool {
			symbolIndex, found := symbolAt[node.RangeTrimmedTrivia().Start]
			if !found {
				return true
			}
			symbol := document.Symbols[symbolIndex]
			result = append(result, indexedFunction{
				node:        node,
				id:          symbol.ID,
				symbolIndex: symbolIndex,
			})
			return true
		},
		phpsyntax.PhpMethodDeclaration,
		phpsyntax.PhpFunctionDeclaration,
	)
	// Constructor assignments can refine private readonly properties for all
	// later method analyses. PHP allows constructors to appear anywhere in the
	// class body, so analyze them before getters regardless of source order.
	slices.SortStableFunc(result, func(left, right indexedFunction) int {
		leftConstructor := strings.EqualFold(
			document.Symbols[left.symbolIndex].Name,
			"__construct",
		)
		rightConstructor := strings.EqualFold(
			document.Symbols[right.symbolIndex].Name,
			"__construct",
		)
		switch {
		case leftConstructor && !rightConstructor:
			return -1
		case !leftConstructor && rightConstructor:
			return 1
		default:
			return 0
		}
	})
	for index, function := range result {
		indices[function.id] = index
	}
	return result, indices
}

func dependentFunctions(
	analyses []functionAnalysis,
	changed map[semantic.SymbolID]struct{},
) []int {
	var result []int
	for index, analysis := range analyses {
		for dependency := range analysis.dependencies {
			if _, found := changed[dependency]; found {
				result = append(result, index)
				break
			}
		}
	}
	return result
}

func (s *analyzerState) analyzeTopLevel(root *phpsyntax.Node) {
	if root == nil {
		return
	}
	s.analyzeTopLevelNodes(directNodes(root), s.newEnvironment(0))
}

func (s *analyzerState) analyzeTopLevelNodes(
	nodes directNodeList,
	env environment,
) environment {
	state := functionState{
		analyzerState: s,
		currentClass:  types.Unknown(),
	}
	current := env
	for cursor := nodes.Cursor(); cursor.Next(); {
		node := cursor.Node()
		switch node.Kind() {
		case phpsyntax.PhpNamespace:
			block := phpquery.DirectChild(node, phpsyntax.PhpBlock)
			if block != nil {
				s.analyzeTopLevelNodes(
					directNodes(block),
					s.newEnvironment(0),
				)
			}
		case phpsyntax.PhpClassDeclaration,
			phpsyntax.PhpInterfaceDeclaration,
			phpsyntax.PhpTraitDeclaration,
			phpsyntax.PhpEnumDeclaration,
			phpsyntax.PhpFunctionDeclaration,
			phpsyntax.PhpUseDeclaration,
			phpsyntax.PhpConstDeclaration:
			continue
		default:
			result := state.analyzeStatement(node, current)
			current = result.env
			if result.terminated {
				return current
			}
		}
	}
	return current
}

type analyzerState struct {
	analyzer              *Analyzer
	document              *semantic.Document
	relations             types.Relations
	issues                map[string]struct{}
	suppressions          suppression.Set
	functionIndices       map[semantic.SymbolID]int
	environments          environmentArena
	arguments             callArgumentArena
	readonlyPropertyTypes map[semantic.SymbolID]types.Type
	cachedClassTypeID     semantic.SymbolID
	cachedClassType       types.Type
	cachedParentClassID   semantic.SymbolID
	cachedParentReceivers []types.Type
	cachedParentsReady    bool
}

// environment is a small copy-on-write variable frame. Most PHP functions only
// track a handful of values, so those bindings stay in a compact linear slice;
// larger frames graduate to a map. Forks share immutable storage and copy it
// only when one side writes. Ordinary value copies share a handle, preserving
// the reference semantics used while evaluating an expression.
type environment struct {
	handle *environmentHandle
}

type environmentBinding struct {
	name  string
	value types.Type
}

type environmentHandle struct {
	bindings []environmentBinding
	table    map[string]types.Type
	arena    *environmentArena
	shared   bool
}

// environmentArena amortizes the stable handle identity required by
// copy-on-write forks. Handles are never reused during one analysis: old
// environments may remain reachable through a branch while later functions
// are analyzed.
type environmentArena struct {
	inline        [inlineEnvironmentHandles]environmentHandle
	inlineUsed    uint8
	available     []environmentHandle
	nextBlockSize uint16
}

const (
	smallEnvironmentLimit    = 8
	inlineEnvironmentHandles = 2
	maxEnvironmentBlockSize  = 128
)

// callArgumentArena keeps recursive call arguments stable for the duration of
// one analysis while amortizing the usual one-slice allocation per call.
type callArgumentArena struct {
	inline     [4]CallArgument
	inlineUsed uint8
	available  []CallArgument
}

const callArgumentBlockSize = 8

func (a *callArgumentArena) allocate(count int) []CallArgument {
	if count <= 0 {
		return nil
	}
	inlineRemaining := len(a.inline) - int(a.inlineUsed)
	if count <= inlineRemaining {
		start := int(a.inlineUsed)
		a.inlineUsed += uint8(count)
		return a.inline[start : start+count : start+count]
	}
	if count > callArgumentBlockSize {
		return make([]CallArgument, count)
	}
	if len(a.available) < count {
		a.available = make([]CallArgument, callArgumentBlockSize)
	}
	result := a.available[:count:count]
	a.available = a.available[count:]
	return result
}

func newEnvironment(capacity int) environment {
	return newEnvironmentIn(nil, capacity)
}

func newEnvironmentIn(
	arena *environmentArena,
	capacity int,
) environment {
	handle := newEnvironmentHandle(arena)
	if capacity > smallEnvironmentLimit {
		handle.table = make(map[string]types.Type, capacity)
	} else if capacity > 0 {
		handle.bindings = make([]environmentBinding, 0, capacity)
	}
	return environment{handle: handle}
}

func newEnvironmentHandle(arena *environmentArena) *environmentHandle {
	if arena == nil {
		return &environmentHandle{}
	}
	handle := arena.allocate()
	handle.arena = arena
	return handle
}

func (a *environmentArena) allocate() *environmentHandle {
	if int(a.inlineUsed) < len(a.inline) {
		handle := &a.inline[a.inlineUsed]
		a.inlineUsed++
		return handle
	}
	if len(a.available) == 0 {
		size := int(a.nextBlockSize)
		if size == 0 {
			size = len(a.inline)
		}
		a.available = make([]environmentHandle, size)
		if size < maxEnvironmentBlockSize {
			size = min(size*2, maxEnvironmentBlockSize)
		}
		a.nextBlockSize = uint16(size)
	}
	handle := &a.available[0]
	a.available = a.available[1:]
	return handle
}

func (s *analyzerState) newEnvironment(capacity int) environment {
	return newEnvironmentIn(&s.environments, capacity)
}

func (e environment) get(name string) (types.Type, bool) {
	if e.handle == nil {
		return types.Type{}, false
	}
	if e.handle.table != nil {
		value, ok := e.handle.table[name]
		return value, ok
	}
	for index := range e.handle.bindings {
		if e.handle.bindings[index].name == name {
			return e.handle.bindings[index].value, true
		}
	}
	return types.Type{}, false
}

func (e environment) set(name string, value types.Type) {
	if e.handle == nil {
		panic("set value on an uninitialized inference environment")
	}
	if e.handle.shared {
		e.handle.detach()
	}
	if e.handle.table != nil {
		e.handle.table[name] = value
		return
	}
	for index := range e.handle.bindings {
		if e.handle.bindings[index].name == name {
			e.handle.bindings[index].value = value
			return
		}
	}
	if len(e.handle.bindings) < smallEnvironmentLimit {
		e.handle.bindings = append(e.handle.bindings, environmentBinding{
			name:  name,
			value: value,
		})
		return
	}
	e.handle.table = make(
		map[string]types.Type,
		len(e.handle.bindings)+1,
	)
	for _, binding := range e.handle.bindings {
		e.handle.table[binding.name] = binding.value
	}
	e.handle.bindings = nil
	e.handle.table[name] = value
}

func (e environment) visit(visitor func(string, types.Type)) {
	if e.handle == nil || visitor == nil {
		return
	}
	if e.handle.table != nil {
		for name, value := range e.handle.table {
			visitor(name, value)
		}
		return
	}
	for _, binding := range e.handle.bindings {
		visitor(binding.name, binding.value)
	}
}

func (e environment) deletePrefix(prefix string) {
	if e.handle == nil || prefix == "" {
		return
	}
	if e.handle.shared {
		e.handle.detach()
	}
	if e.handle.table != nil {
		for name := range e.handle.table {
			if strings.HasPrefix(name, prefix) {
				delete(e.handle.table, name)
			}
		}
		return
	}
	kept := e.handle.bindings[:0]
	for _, binding := range e.handle.bindings {
		if !strings.HasPrefix(binding.name, prefix) {
			kept = append(kept, binding)
		}
	}
	e.handle.bindings = kept
}

func (handle *environmentHandle) detach() {
	if handle == nil || !handle.shared {
		return
	}
	if handle.table != nil {
		source := handle.table
		handle.table = make(map[string]types.Type, len(source)+1)
		for name, value := range source {
			handle.table[name] = value
		}
	} else if handle.bindings != nil {
		capacity := cap(handle.bindings)
		if capacity < len(handle.bindings)+1 {
			capacity = len(handle.bindings) + 1
		}
		bindings := make([]environmentBinding, len(handle.bindings), capacity)
		copy(bindings, handle.bindings)
		handle.bindings = bindings
	}
	handle.shared = false
}

func (e environment) len() int {
	if e.handle == nil {
		return 0
	}
	if e.handle.table != nil {
		return len(e.handle.table)
	}
	return len(e.handle.bindings)
}

type functionState struct {
	*analyzerState
	symbol       semantic.Symbol
	scope        semantic.ScopeID
	currentClass types.Type
	namedTypes   namedTypeCache
	generator    bool
	returns      []types.Type
	dependencies map[semantic.SymbolID]struct{}
}

type flow struct {
	env        environment
	terminated bool
	continues  []environment
}

func (s *analyzerState) analyzeFunction(
	function indexedFunction,
) functionAnalysis {
	symbol := s.document.Symbols[function.symbolIndex]
	if s.analyzer.observeFunction != nil {
		s.analyzer.observeFunction(symbol.ID)
	}
	scope := s.scopeOwnedBy(symbol.ID)
	state := functionState{
		analyzerState: s,
		symbol:        symbol,
		scope:         scope,
		currentClass:  s.classType(symbol.Container),
	}
	env := s.newEnvironment(len(symbol.Parameters) + 1)
	inheritedParameters := s.inheritedMethodParameterTypes(symbol)
	for index, parameter := range symbol.Parameters {
		value := parameter.Type
		if index < len(inheritedParameters) &&
			!inheritedParameters[index].IsUnknown() {
			value = inheritedParameters[index]
		}
		if parameter.Flags.Has(semantic.VariadicFlag) {
			value = types.List(value)
		}
		env.set(parameter.Name, value)
	}
	if !state.currentClass.IsUnknown() {
		env.set("$this", state.currentClass)
	}
	body := phpquery.DirectChild(function.node, phpsyntax.PhpBlock)
	if body == nil {
		return functionAnalysis{}
	}
	state.generator = containsDirectYield(body)
	state.analyzeBlockOwned(body, env)
	if symbol.ReturnType.IsUnknown() && len(state.returns) > 0 {
		returnTypes := types.NewJoiner(
			state.relations,
			state.returns[0],
		)
		for _, returned := range state.returns[1:] {
			returnTypes.Add(returned)
		}
		s.document.Symbols[function.symbolIndex].ReturnType =
			returnTypes.Value()
	}
	return functionAnalysis{dependencies: state.dependencies}
}

func (s *analyzerState) inheritedMethodParameterTypes(
	method semantic.Symbol,
) []types.Type {
	if method.Kind != semantic.MethodSymbol || method.Container == "" {
		return nil
	}
	class, found := s.analyzer.Snapshot.Symbol(method.Container)
	if !found || !class.IsClassLike() {
		return nil
	}
	parents := s.inheritedReceiverTypes(class)
	if len(parents) == 0 {
		return nil
	}
	result := make([]types.Type, len(method.Parameters))
	members := resolver.MemberResolver{Snapshot: s.analyzer.Snapshot}
	for _, parent := range parents {
		for _, inherited := range members.Methods(parent, method.Name) {
			parameterCount := min(
				len(method.Parameters),
				len(inherited.Symbol.Parameters),
			)
			for index := 0; index < parameterCount; index++ {
				own := method.Parameters[index]
				candidate := inherited.Symbol.Parameters[index]
				if !own.DocType.IsUnknown() || candidate.DocType.IsUnknown() ||
					types.ContainsUncertain(candidate.Type) ||
					!s.relations.IsAssignableTo(
						candidate.Type,
						own.NativeType,
					) {
					continue
				}
				if result[index].IsUnknown() {
					result[index] = candidate.Type
				} else {
					result[index] = s.relations.Join(
						result[index],
						candidate.Type,
					)
				}
			}
		}
	}
	return result
}

func (s *analyzerState) inheritedReceiverTypes(
	class semantic.Symbol,
) []types.Type {
	if class.ID != "" && s.cachedParentsReady &&
		s.cachedParentClassID == class.ID {
		return s.cachedParentReceivers
	}
	names := append(append([]string(nil), class.Extends...), class.Implements...)
	typed := append(
		append([]types.Type(nil), class.ExtendsTypes...),
		class.ImplementsTypes...,
	)
	result := make([]types.Type, 0, len(names))
	for _, name := range names {
		receiver := types.Unknown()
		for _, candidate := range typed {
			if candidate.Kind() == types.ObjectKind &&
				strings.EqualFold(candidate.Name(), name) {
				receiver = candidate
				break
			}
		}
		if receiver.IsUnknown() {
			receiver = types.Named(name)
		}
		result = append(result, receiver)
	}
	if class.ID != "" {
		s.cachedParentClassID = class.ID
		s.cachedParentReceivers = result
		s.cachedParentsReady = true
	}
	return result
}

func (s *functionState) analyzeBlock(node *phpsyntax.Node, env environment) flow {
	return s.analyzeBlockOwned(node, cloneEnvironment(env))
}

func (s *functionState) analyzeBlockOwned(
	node *phpsyntax.Node,
	env environment,
) flow {
	current := env
	var continues []environment
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*phpsyntax.Node)
		if !ok {
			continue
		}
		result := s.analyzeStatement(child, current)
		continues = append(continues, result.continues...)
		current = result.env
		if result.terminated {
			result.continues = continues
			return result
		}
	}
	return flow{env: current, continues: continues}
}

func (s *functionState) analyzeStatement(node *phpsyntax.Node, env environment) flow {
	if node == nil {
		return flow{env: env}
	}
	if environmentIsImpossible(env) {
		return flow{env: env, terminated: true}
	}
	annotations := s.statementVarAnnotations(node)
	s.applyVarAnnotations(annotations, env, node.Range().Start)
	switch node.Kind() {
	case phpsyntax.PhpBlock:
		return s.analyzeBlock(node, env)
	case phpsyntax.PhpExpressionStatement:
		if expression := firstDirectNode(node); expression != nil {
			if isCallNamed(expression, "unset") {
				s.applyUnset(expression, env)
				return flow{env: env}
			}
			value := s.infer(expression, env)
			s.applyVarAnnotations(
				annotations,
				env,
				node.RangeTrimmedTrivia().Start,
			)
			if value.Kind() == types.NeverKind {
				return flow{env: env, terminated: true}
			}
			if isCallNamed(expression, "assert") {
				if condition := phpquery.ArgumentExpression(expression, 0); condition != nil {
					narrowed, _ := s.conditionEnvironments(condition, env)
					return flow{env: narrowed}
				}
			}
			s.applyUnconditionalCallAssertions(expression, env)
		}
		return flow{env: env}
	case phpsyntax.PhpReturnStatement:
		value := types.Null()
		expression := firstDirectNode(node)
		if expression != nil {
			value = s.infer(expression, env)
		}
		s.validateReturn(node, value, expression != nil)
		s.returns = append(s.returns, value)
		return flow{env: env, terminated: true}
	case phpsyntax.PhpThrowStatement:
		if expression := firstDirectNode(node); expression != nil {
			s.infer(expression, env)
		}
		return flow{env: env, terminated: true}
	case phpsyntax.PhpBreakStatement:
		return flow{env: env, terminated: true}
	case phpsyntax.PhpContinueStatement:
		return flow{
			env:        env,
			terminated: true,
			continues:  []environment{env},
		}
	case phpsyntax.PhpIfStatement:
		return s.analyzeIf(node, env)
	case phpsyntax.PhpForeachStatement:
		return s.analyzeForeach(node, env, annotations)
	case phpsyntax.PhpWhileStatement, phpsyntax.PhpDoWhileStatement,
		phpsyntax.PhpForStatement:
		return s.analyzeLoop(node, env)
	case phpsyntax.PhpTryStatement:
		return s.analyzeTry(node, env)
	case phpsyntax.PhpSwitchStatement:
		return s.analyzeSwitch(node, env)
	default:
		for index := 0; index < node.ChildCount(); index++ {
			child, ok := node.Child(index).(*phpsyntax.Node)
			if ok && isExpressionKind(child.Kind()) {
				s.infer(child, env)
			}
		}
		return flow{env: env}
	}
}

func (s *functionState) statementVarAnnotations(
	node *phpsyntax.Node,
) map[string]types.Type {
	comment := inferenceLeadingDocComment(node)
	if !strings.Contains(comment, "@var") {
		return nil
	}
	documentation := phpdoc.Parse(comment)
	if len(documentation.Vars) == 0 {
		return nil
	}
	var templates []string
	collectTypeTemplateNames(s.currentClass, &templates)
	context := s.nameContextAt(node.Range().Start)
	result := make(map[string]types.Type, len(documentation.Vars))
	for name, value := range documentation.Vars {
		resolved := context.ResolvePHPDocType(value, templates)
		if resolved.IsUnknown() || resolved.Kind() == types.ErrorKind {
			continue
		}
		if name == "" {
			name = statementAnnotationTarget(node)
		}
		if name != "" {
			result[name] = resolved
		}
	}
	return result
}

func (s *functionState) applyVarAnnotations(
	annotations map[string]types.Type,
	env environment,
	offset uint32,
) {
	for name, value := range annotations {
		env.set(name, value)
		s.updateLocalType(name, offset, value)
	}
}

func inferenceLeadingDocComment(node *phpsyntax.Node) string {
	if node == nil {
		return ""
	}
	rng := node.Range()
	trimmed := node.RangeTrimmedTrivia()
	if trimmed.Start <= rng.Start {
		return ""
	}
	text := node.Text()
	prefixLength := int(trimmed.Start - rng.Start)
	if prefixLength > len(text) {
		prefixLength = len(text)
	}
	prefix := text[:prefixLength]
	start := strings.LastIndex(prefix, "/**")
	if start < 0 {
		return ""
	}
	end := strings.Index(prefix[start:], "*/")
	if end < 0 {
		return ""
	}
	end += start + 2
	if !onlyCommentTrivia(prefix[end:]) {
		return ""
	}
	return prefix[start:end]
}

func onlyCommentTrivia(text string) bool {
	for {
		text = strings.TrimSpace(text)
		if text == "" {
			return true
		}
		switch {
		case strings.HasPrefix(text, "//"), strings.HasPrefix(text, "#"):
			if newline := strings.IndexByte(text, '\n'); newline >= 0 {
				text = text[newline+1:]
				continue
			}
			return true
		case strings.HasPrefix(text, "/*"):
			end := strings.Index(text[2:], "*/")
			if end < 0 {
				return false
			}
			text = text[end+4:]
		default:
			return false
		}
	}
}

func statementAnnotationTarget(node *phpsyntax.Node) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case phpsyntax.PhpExpressionStatement:
		expression := firstDirectNode(node)
		if expression != nil &&
			expression.Kind() == phpsyntax.PhpAssignmentExpression {
			left := firstDirectNode(expression)
			if left != nil && left.Kind() == phpsyntax.PhpVariable {
				return phpquery.VariableKey(left)
			}
		}
	case phpsyntax.PhpForeachStatement:
		_, value := directForeachVariables(node)
		if value != nil {
			return phpquery.VariableKey(value)
		}
	}
	return ""
}

func collectTypeTemplateNames(value types.Type, result *[]string) {
	if value.Kind() == types.TemplateKind {
		*result = append(*result, value.Name())
		return
	}
	for index := 0; index < value.ArgumentCount(); index++ {
		collectTypeTemplateNames(value.Argument(index), result)
	}
}

func (s *functionState) validateReturn(
	node *phpsyntax.Node,
	value types.Type,
	hasExpression bool,
) {
	if s.generator {
		return
	}
	expected := resolveSpecialType(
		s.symbol.ReturnType,
		s.currentClass,
		s.currentClass,
	)
	if expected.IsUnknown() || expected.Kind() == types.MixedKind ||
		types.ContainsUncertain(expected) {
		return
	}
	if expected.Kind() == types.VoidKind {
		if hasExpression {
			s.report(
				node,
				"php.returnType",
				"Void function must not return a value",
			)
		}
		return
	}
	if !hasExpression {
		s.report(
			node,
			"php.returnType",
			"Expected return type "+expected.String()+", got no value",
		)
		return
	}
	if !types.ContainsUncertain(value) &&
		!s.relations.IsAssignableTo(value, expected) {
		s.report(
			node,
			"php.returnType",
			"Expected return type "+expected.String()+", got "+value.String(),
		)
	}
}

func containsDirectYield(node *phpsyntax.Node) bool {
	if node == nil {
		return false
	}
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*phpsyntax.Node)
		if !ok {
			continue
		}
		if child.Kind() == phpsyntax.PhpYieldExpression {
			return true
		}
		switch child.Kind() {
		case phpsyntax.PhpMethodDeclaration,
			phpsyntax.PhpFunctionDeclaration,
			phpsyntax.PhpClosure,
			phpsyntax.PhpArrowFunction:
			continue
		}
		if containsDirectYield(child) {
			return true
		}
	}
	return false
}

func (s *analyzerState) report(node *phpsyntax.Node, code, message string) {
	if node == nil {
		return
	}
	s.reportRange(node.RangeTrimmedTrivia(), code, message)
}

func (s *analyzerState) reportRange(
	rng phpsyntax.TextRange,
	code,
	message string,
) {
	if s.suppressions.Suppresses(rng.Start, code) {
		return
	}
	key := code + ":" + strconv.FormatUint(uint64(rng.Start), 10) + ":" + message
	if _, exists := s.issues[key]; exists {
		return
	}
	s.issues[key] = struct{}{}
	s.document.Issues = append(s.document.Issues, semantic.Issue{
		Range:   rng,
		Code:    code,
		Message: message,
	})
}

func (s *functionState) analyzeIf(node *phpsyntax.Node, env environment) flow {
	children := directNodes(node)
	childCount := children.Len()
	if childCount == 0 {
		return flow{env: env}
	}
	trueEnv, falseEnv := s.conditionEnvironments(children.At(0), env)
	var outcomes []flow
	index := 1
	if index < childCount &&
		children.At(index).Kind() != phpsyntax.PhpElseIfClause &&
		children.At(index).Kind() != phpsyntax.PhpElseClause {
		outcomes = append(
			outcomes,
			s.analyzeStatement(children.At(index), trueEnv),
		)
		index++
	}
	for index < childCount &&
		children.At(index).Kind() == phpsyntax.PhpElseIfClause {
		clause := directNodes(children.At(index))
		clauseCount := clause.Len()
		if clauseCount > 0 {
			clauseTrue, clauseFalse := s.conditionEnvironments(
				clause.At(0),
				falseEnv,
			)
			if clauseCount > 1 {
				outcomes = append(
					outcomes,
					s.analyzeStatement(clause.At(1), clauseTrue),
				)
			}
			falseEnv = clauseFalse
		}
		index++
	}
	hasElse := index < childCount &&
		children.At(index).Kind() == phpsyntax.PhpElseClause
	if hasElse {
		clause := directNodes(children.At(index))
		if clause.Len() > 0 {
			outcomes = append(
				outcomes,
				s.analyzeStatement(clause.At(0), falseEnv),
			)
		}
	} else {
		outcomes = append(outcomes, flow{env: falseEnv})
	}
	return s.joinFlows(outcomes)
}

func (s *functionState) analyzeForeach(
	node *phpsyntax.Node,
	env environment,
	annotations map[string]types.Type,
) flow {
	nodes := directNodes(node)
	nodeCount := nodes.Len()
	if nodeCount == 0 {
		return flow{env: env}
	}
	iterable := s.infer(nodes.At(0), env)
	keyType, valueType := iterableTypes(iterable, s.relations)
	loopEnv := cloneEnvironment(env)
	keyVariable, valueVariable := directForeachVariables(node)
	if keyVariable == nil && valueVariable != nil {
		name := phpquery.VariableKey(valueVariable)
		loopEnv.set(name, valueType)
		s.record(valueVariable, valueType, semantic.AssignmentSource, "foreach value")
		s.updateLocalType(name, valueVariable.Range().Start, valueType)
	} else if keyVariable != nil && valueVariable != nil {
		keyName := phpquery.VariableKey(keyVariable)
		valueName := phpquery.VariableKey(valueVariable)
		loopEnv.set(keyName, keyType)
		loopEnv.set(valueName, valueType)
		s.updateLocalType(
			keyName,
			keyVariable.Range().Start,
			keyType,
		)
		s.updateLocalType(
			valueName,
			valueVariable.Range().Start,
			valueType,
		)
	}
	s.applyVarAnnotations(annotations, loopEnv, node.RangeTrimmedTrivia().Start)
	body := nodes.At(nodeCount - 1)
	var bodyFlow flow
	if body.Kind() == phpsyntax.PhpBlock {
		bodyFlow = s.analyzeBlockOwned(body, loopEnv)
	} else {
		bodyFlow = s.analyzeStatement(body, loopEnv)
	}
	result := joinEnvironments(s.relations, env, bodyFlow.env)
	for _, continued := range bodyFlow.continues {
		result = joinEnvironments(s.relations, result, continued)
	}
	if !bodyFlow.terminated && len(bodyFlow.continues) == 0 &&
		len(phpquery.Nodes(body, phpsyntax.PhpBreakStatement)) == 0 &&
		nodes.At(0).Kind() == phpsyntax.PhpVariable {
		// When the iterated array itself is transformed without an early exit,
		// every existing element passes through the body. The zero-iteration
		// path means the array was empty, which is already represented by the
		// transformed array type; joining the pre-loop element shape back in
		// would incorrectly claim that stale elements can survive the loop.
		name := phpquery.VariableKey(nodes.At(0))
		before, hadBefore := env.get(name)
		after, hadAfter := bodyFlow.env.get(name)
		if hadBefore && hadAfter && !before.Equal(after) &&
			isArrayContainerType(before) && isArrayContainerType(after) {
			result.set(name, after)
		}
	}
	return flow{env: result}
}

func isArrayContainerType(value types.Type) bool {
	switch value.Kind() {
	case types.ArrayKind, types.NonEmptyArrayKind, types.ListKind,
		types.NonEmptyListKind,
		types.ArrayShapeKind:
		return true
	case types.UnionKind:
		if value.ArgumentCount() == 0 {
			return false
		}
		for index := 0; index < value.ArgumentCount(); index++ {
			if !isArrayContainerType(value.Argument(index)) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func directForeachVariables(
	node *phpsyntax.Node,
) (key, value *phpsyntax.Node) {
	inTargets := false
	for index := 0; index < node.ChildCount(); index++ {
		switch child := node.Child(index).(type) {
		case *phpsyntax.Token:
			if strings.EqualFold(child.Text(), "as") {
				inTargets = true
			} else if inTargets && child.Kind() == phpsyntax.TkCloseParen {
				return key, value
			}
		case *phpsyntax.Node:
			if !inTargets || child.Kind() != phpsyntax.PhpVariable {
				continue
			}
			if value != nil {
				key = value
			}
			value = child
		}
	}
	return key, value
}

func (s *functionState) analyzeLoop(node *phpsyntax.Node, env environment) flow {
	nodes := directNodes(node)
	if node.Kind() == phpsyntax.PhpWhileStatement && nodes.Len() > 0 {
		trueEnv, falseEnv := s.conditionEnvironments(nodes.At(0), env)
		if nodes.Len() == 1 {
			return flow{env: falseEnv}
		}
		body := s.analyzeStatement(nodes.At(nodes.Len()-1), trueEnv)
		result := joinEnvironments(s.relations, falseEnv, body.env)
		for _, continued := range body.continues {
			result = joinEnvironments(s.relations, result, continued)
		}
		return flow{env: result}
	}
	loopEnv := cloneEnvironment(env)
	for cursor := nodes.Cursor(); cursor.Next(); {
		child := cursor.Node()
		if child.Kind() == phpsyntax.PhpBlock {
			body := s.analyzeBlockOwned(child, loopEnv)
			result := joinEnvironments(s.relations, env, body.env)
			for _, continued := range body.continues {
				result = joinEnvironments(s.relations, result, continued)
			}
			return flow{env: result}
		}
		s.infer(child, loopEnv)
	}
	return flow{env: joinEnvironments(s.relations, env, loopEnv)}
}

func (s *functionState) analyzeTry(node *phpsyntax.Node, env environment) flow {
	var outcomes []flow
	for cursor := directNodes(node).Cursor(); cursor.Next(); {
		child := cursor.Node()
		switch child.Kind() {
		case phpsyntax.PhpBlock:
			outcomes = append(outcomes, s.analyzeBlock(child, env))
		case phpsyntax.PhpCatchClause:
			clauseNodes := directNodes(child)
			catchEnv := cloneEnvironment(env)
			catchType := types.Unknown()
			for clauseCursor := clauseNodes.Cursor(); clauseCursor.Next(); {
				clauseNode := clauseCursor.Node()
				if isTypeKind(clauseNode.Kind()) {
					catchType = s.typeFromSyntax(clauseNode)
				}
				if clauseNode.Kind() == phpsyntax.PhpVariable {
					catchEnv.set(
						phpquery.VariableKey(clauseNode),
						catchType,
					)
				}
				if clauseNode.Kind() == phpsyntax.PhpBlock {
					outcomes = append(
						outcomes,
						s.analyzeBlockOwned(clauseNode, catchEnv),
					)
				}
			}
		case phpsyntax.PhpFinallyClause:
			clauseNodes := directNodes(child)
			clauseCount := clauseNodes.Len()
			if clauseCount > 0 {
				base := s.joinFlows(outcomes)
				return s.analyzeStatement(
					clauseNodes.At(clauseCount-1),
					base.env,
				)
			}
		}
	}
	return s.joinFlows(outcomes)
}

func (s *functionState) analyzeSwitch(node *phpsyntax.Node, env environment) flow {
	children := directNodes(node)
	switchTrue := false
	if children.Len() > 0 && children.At(0).Kind() != phpsyntax.PhpCaseClause {
		selector := children.At(0)
		s.infer(selector, env)
		switchTrue = isTrueCondition(selector)
	}

	var outcomes []flow
	remaining := cloneEnvironment(env)
	var fallthroughEnv environment
	hasFallthrough := false
	hasDefault := false
	for cursor := children.Cursor(); cursor.Next(); {
		child := cursor.Node()
		if child.Kind() != phpsyntax.PhpCaseClause {
			continue
		}

		clause := directNodes(child)
		statementIndex := 0
		clauseEnv := cloneEnvironment(env)
		if clause.Len() > 0 && isExpressionKind(clause.At(0).Kind()) {
			condition := clause.At(0)
			statementIndex = 1
			if switchTrue {
				clauseEnv, remaining = s.conditionEnvironments(
					condition,
					remaining,
				)
			} else {
				s.infer(condition, clauseEnv)
			}
		} else {
			hasDefault = true
			clauseEnv = remaining
		}
		if hasFallthrough {
			clauseEnv = joinEnvironments(
				s.relations,
				fallthroughEnv,
				clauseEnv,
			)
		}

		outcome := flow{env: clauseEnv}
		broke := false
		for ; statementIndex < clause.Len(); statementIndex++ {
			statement := clause.At(statementIndex)
			outcome = s.analyzeStatement(statement, outcome.env)
			if statement.Kind() == phpsyntax.PhpBreakStatement {
				outcome.terminated = false
				broke = true
				break
			}
			if outcome.terminated {
				break
			}
		}
		outcomes = append(outcomes, outcome)
		hasFallthrough = !broke && !outcome.terminated
		if hasFallthrough {
			fallthroughEnv = outcome.env
		}
	}
	if !hasDefault {
		outcomes = append(outcomes, flow{env: remaining})
	}
	if len(outcomes) == 0 {
		return flow{env: env}
	}
	return s.joinFlows(outcomes)
}

func isTrueCondition(node *phpsyntax.Node) bool {
	for node != nil && node.Kind() == phpsyntax.PhpParenthesized {
		node = firstDirectNode(node)
	}
	return node != nil && node.Kind() == phpsyntax.PhpBoolean &&
		strings.EqualFold(compact(node.Text()), "true")
}

func (s *functionState) joinFlows(flows []flow) flow {
	var result environment
	var continues []environment
	hasResult := false
	allTerminated := len(flows) > 0
	for _, candidate := range flows {
		continues = append(continues, candidate.continues...)
		if candidate.terminated {
			continue
		}
		allTerminated = false
		if !hasResult {
			result = candidate.env
			hasResult = true
		} else {
			result = joinEnvironments(s.relations, result, candidate.env)
		}
	}
	if !hasResult {
		result = s.newEnvironment(0)
	}
	return flow{
		env:        result,
		terminated: allTerminated,
		continues:  continues,
	}
}

func (s *analyzerState) scopeOwnedBy(owner semantic.SymbolID) semantic.ScopeID {
	for _, scope := range s.document.Scopes {
		if scope.Owner == owner && scope.Kind == semantic.FunctionScope {
			return scope.ID
		}
	}
	return 0
}

func (s *analyzerState) classType(id semantic.SymbolID) types.Type {
	if id == "" {
		return types.Unknown()
	}
	if s.cachedClassTypeID == id {
		return s.cachedClassType
	}
	symbol, ok := s.analyzer.Snapshot.Symbol(id)
	if !ok {
		for _, candidate := range s.document.Symbols {
			if candidate.ID == id {
				symbol, ok = candidate, true
				break
			}
		}
	}
	if !ok {
		return types.Unknown()
	}
	var args []types.Type
	for _, template := range symbol.Templates {
		args = append(args, types.Template(template.Name))
	}
	result := types.Named(symbol.FullyQualified, args...)
	s.cachedClassTypeID = id
	s.cachedClassType = result
	return result
}
