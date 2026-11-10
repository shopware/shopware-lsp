//! Analyzer entry point, fixed-point scheduling, and statement flow.

use std::borrow::Cow;
use std::collections::{BTreeMap, HashMap};
use std::sync::Arc;

use rustc_hash::{FxHashMap, FxHashSet};
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_resolver::{MemberResolver, NameContext};
use swlsp_php_semantic::{
    Confidence, Document, Flags, Issue, NodeId, Snapshot, Str, Symbol, SymbolId, SymbolKind,
    TypeFact, TypeSource, node_identity,
};
use swlsp_php_types::{Joiner, Kind, Relations, Type, contains_uncertain, parse_native};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};
use swlsp_textutil::eq_ignore_case;

use crate::env::{Env, join_environments};
use crate::extensions::Extension;
use crate::syntax::{
    Source, compact, contains_direct_yield, direct_foreach_variables, direct_nodes, first_node,
    is_call_named, is_expression_kind, is_true_condition, is_type_kind, leading_doc_comment, start,
    trimmed_start, variable_key,
};
use crate::types_util::{
    NamedTypeCache, collect_type_template_names, is_array_container_type, iterable_types,
    resolve_special_type,
};

/// Test hook invoked before each function analysis.
pub(crate) type FunctionObserver = Arc<dyn Fn(&SymbolId) + Send + Sync>;

/// Local expression and control-flow analysis over one bound document.
#[derive(Clone)]
pub struct Analyzer {
    pub snapshot: Arc<Snapshot>,
    pub extensions: Vec<Arc<dyn Extension>>,
    pub(crate) observe_function: Option<FunctionObserver>,
}

impl Analyzer {
    pub fn new(snapshot: Option<Arc<Snapshot>>, extensions: Vec<Arc<dyn Extension>>) -> Self {
        Analyzer {
            snapshot: snapshot.unwrap_or_else(|| Snapshot::new(0, &[])),
            extensions,
            observe_function: None,
        }
    }

    /// Analyzes a copy of `document`.
    pub fn analyze(&self, document: &Document, root: &SyntaxNode) -> Document {
        let source = root.text_string();
        self.run(document.clone(), root, &source, false, true)
    }

    /// Enriches a document exclusively owned by the caller. Use this in the
    /// indexing pipeline, where binder output has not been published.
    pub fn analyze_owned(&self, document: Document, root: &SyntaxNode) -> Document {
        let source = root.text_string();
        self.analyze_owned_with_source(document, root, &source)
    }

    /// Like [`Analyzer::analyze_owned`] when the caller already holds the
    /// tree's source text (`root` must start at offset zero).
    ///
    /// Return types inferred for one function are visible to the functions
    /// analyzed after it in the same pass.
    pub fn analyze_owned_with_source(
        &self,
        document: Document,
        root: &SyntaxNode,
        source: &str,
    ) -> Document {
        self.run(document, root, source, true, true)
    }

    /// Infers only the return types of the document's function-like
    /// declarations (the fixed point of [`Analyzer::analyze_owned_with_source`]),
    /// skipping top-level code, declaration validation, and variable
    /// references. Expression facts in the result are incomplete.
    pub fn infer_returns_owned(
        &self,
        document: Document,
        root: &SyntaxNode,
        source: &str,
    ) -> Document {
        self.run(document, root, source, true, false)
    }

    fn run(
        &self,
        document: Document,
        root: &SyntaxNode,
        source: &str,
        live: bool,
        complete: bool,
    ) -> Document {
        const MAXIMUM_INFERENCE_PASSES: usize = 8;
        let mut state = AnalyzerState::new(self, document, source);
        let (functions, function_indices) = indexed_functions(&state.document, root);
        state.function_indices = function_indices;

        let mut analyses: Vec<FxHashSet<SymbolId>> = vec![FxHashSet::default(); functions.len()];
        let mut returns = ReturnPublisher::new(&state.document);
        let mut scheduled: Vec<usize> = (0..functions.len()).collect();
        let mut pass = 0;
        while pass < MAXIMUM_INFERENCE_PASSES && !scheduled.is_empty() {
            let mut changed: FxHashSet<SymbolId> = FxHashSet::default();
            for &function_index in &scheduled {
                let function = &functions[function_index];
                let before = state.document.symbols[function.symbol_index]
                    .return_type
                    .clone();
                let snapshot = state.snapshot.clone();
                analyses[function_index] = state.analyze_function(&snapshot, function);
                drop(snapshot);
                if before != state.document.symbols[function.symbol_index].return_type {
                    changed.insert(function.id.clone());
                    if live {
                        returns.publish(&mut state, std::slice::from_ref(&function.id));
                    }
                }
            }
            if changed.is_empty() {
                break;
            }
            if !live {
                let changed: Vec<SymbolId> = changed.iter().cloned().collect();
                returns.publish(&mut state, &changed);
            }
            if pass + 1 == MAXIMUM_INFERENCE_PASSES {
                break;
            }
            scheduled = analyses
                .iter()
                .enumerate()
                .filter(|(_, dependencies)| {
                    dependencies
                        .iter()
                        .any(|dependency| changed.contains(dependency))
                })
                .map(|(index, _)| index)
                .collect();
            pass += 1;
        }
        if !complete {
            return state.document;
        }
        let snapshot = state.snapshot.clone();
        state.analyze_top_level(&snapshot, root);
        state.validate_declarations(&snapshot);
        state.relink_variable_references();
        state.document
    }
}

/// Publishes inferred return types to the analysis snapshot.
///
/// The first publication overlays every function-like declaration of the
/// document ([`Snapshot::with_updated_function_returns`]). Afterwards only
/// return types change, so each later publication overrides just the
/// changed declarations in place. The result equals refreshing the whole
/// document after every change: an ID resolves to its last declaration in
/// document order, as in the overlay's index.
struct ReturnPublisher {
    refreshed: bool,
    last_declaration: FxHashMap<SymbolId, usize>,
}

impl ReturnPublisher {
    fn new(document: &Document) -> Self {
        let last_declaration = document
            .symbols
            .iter()
            .enumerate()
            .filter(|(_, symbol)| symbol.kind.is_function_like())
            .map(|(index, symbol)| (symbol.id.clone(), index))
            .collect();
        ReturnPublisher {
            refreshed: false,
            last_declaration,
        }
    }

    fn publish(&mut self, state: &mut AnalyzerState<'_>, changed: &[SymbolId]) {
        if !self.refreshed {
            self.refreshed = true;
            state.snapshot = state
                .snapshot
                .with_updated_function_returns(&state.document);
            return;
        }
        for id in changed {
            if let Some(&index) = self.last_declaration.get(id) {
                Snapshot::override_symbol(&mut state.snapshot, &state.document.symbols[index]);
            }
        }
    }
}

pub(crate) struct IndexedFunction {
    node: SyntaxNode,
    id: SymbolId,
    symbol_index: usize,
}

fn indexed_functions(
    document: &Document,
    root: &SyntaxNode,
) -> (Vec<IndexedFunction>, FxHashMap<SymbolId, usize>) {
    let mut symbol_at: FxHashMap<u32, usize> = FxHashMap::default();
    for (index, symbol) in document.symbols.iter().enumerate() {
        if matches!(symbol.kind, SymbolKind::Method | SymbolKind::Function) {
            symbol_at.insert(symbol.range.start().into(), index);
        }
    }
    let mut result = Vec::with_capacity(symbol_at.len());
    query::visit(
        root,
        &[PHP_METHOD_DECLARATION, PHP_FUNCTION_DECLARATION],
        |node| {
            if let Some(&symbol_index) = symbol_at.get(&trimmed_start(node)) {
                result.push(IndexedFunction {
                    node: node.clone(),
                    id: document.symbols[symbol_index].id.clone(),
                    symbol_index,
                });
            }
            true
        },
    );
    // Constructor assignments can refine private readonly properties for all
    // later method analyses, so analyze constructors first.
    result.sort_by_key(|function| {
        !eq_ignore_case(&document.symbols[function.symbol_index].name, "__construct")
    });
    let indices = result
        .iter()
        .enumerate()
        .map(|(index, function)| (function.id.clone(), index))
        .collect();
    (result, indices)
}

/// Per-document analysis state shared by every function analysis.
pub(crate) struct AnalyzerState<'e> {
    extensions: &'e [Arc<dyn Extension>],
    observe_function: Option<&'e (dyn Fn(&SymbolId) + Send + Sync)>,
    pub(crate) snapshot: Arc<Snapshot>,
    pub(crate) document: Document,
    pub(crate) source: Source<'e>,
    issues: FxHashSet<String>,
    suppressions: swlsp_php_suppression::Set,
    pub(crate) function_indices: FxHashMap<SymbolId, usize>,
    pub(crate) readonly_property_types: FxHashMap<SymbolId, Type>,
    cached_class_type: Option<(SymbolId, Type)>,
    cached_parent_receivers: Option<(SymbolId, Vec<Type>)>,
    name_contexts: Vec<Option<NameContext>>,
    document_name_context: Option<NameContext>,
}

impl<'e> AnalyzerState<'e> {
    fn new(analyzer: &'e Analyzer, document: Document, source: &'e str) -> Self {
        AnalyzerState {
            extensions: &analyzer.extensions,
            observe_function: analyzer.observe_function.as_deref(),
            snapshot: analyzer.snapshot.clone(),
            suppressions: swlsp_php_suppression::parse(source),
            document,
            source: Source(source),
            issues: FxHashSet::default(),
            function_indices: FxHashMap::default(),
            readonly_property_types: FxHashMap::default(),
            cached_class_type: None,
            cached_parent_receivers: None,
            name_contexts: Vec::new(),
            document_name_context: None,
        }
    }

    pub(crate) fn extensions(&self) -> &'e [Arc<dyn Extension>] {
        self.extensions
    }

    fn analyze_function(
        &mut self,
        snapshot: &Snapshot,
        function: &IndexedFunction,
    ) -> FxHashSet<SymbolId> {
        let symbol = self.document.symbols[function.symbol_index].clone();
        if let Some(observe) = self.observe_function {
            observe(&symbol.id);
        }
        let scope = self.scope_owned_by(&symbol.id);
        let current_class = self.class_type(&symbol.container);
        let parameters = symbol.parameters.clone();
        let mut state = Fs::new(self, snapshot, symbol, current_class);
        state.scope = scope;
        let env = Env::new(parameters.len() + 1);
        let inherited = state.inherited_method_parameter_types();
        for (index, parameter) in parameters.iter().enumerate() {
            let mut value = parameter.ty.clone();
            if let Some(inherited) = inherited.get(index)
                && !inherited.is_unknown()
            {
                value = inherited.clone();
            }
            if parameter.flags.has(Flags::VARIADIC) {
                value = Type::list(value);
            }
            env.set(&parameter.name, value);
        }
        if !state.current_class.is_unknown() {
            env.set("$this", state.current_class.clone());
        }
        let Some(body) = query::direct_child(&function.node, PHP_BLOCK) else {
            return FxHashSet::default();
        };
        state.generator = contains_direct_yield(&body);
        state.analyze_block_owned(&body, env);
        let returns = std::mem::take(&mut state.returns);
        let dependencies = state.dependencies.take().unwrap_or_default();
        let relations = state.relations;
        if state.symbol.return_type.is_unknown() && !returns.is_empty() {
            let mut joiner = Joiner::new(relations, returns[0].clone());
            for returned in &returns[1..] {
                joiner.add(returned.clone());
            }
            let symbol = &mut self.document.symbols[function.symbol_index];
            symbol.return_type = joiner.value();
            // The body can call into other files, so the type is only as
            // complete as this analysis snapshot.
            symbol.flags.insert(Flags::INFERRED_RETURN);
        }
        dependencies
    }

    fn analyze_top_level(&mut self, snapshot: &Snapshot, root: &SyntaxNode) {
        let env = Env::new(0);
        let mut state = Fs::new(self, snapshot, Symbol::default(), Type::UNKNOWN);
        state.analyze_top_level_nodes(&direct_nodes(root), env);
    }

    fn scope_owned_by(&self, owner: &SymbolId) -> u32 {
        self.document
            .scopes
            .iter()
            .find(|scope| {
                scope.owner == *owner && scope.kind == swlsp_php_semantic::ScopeKind::Function
            })
            .map_or(0, |scope| scope.id)
    }

    pub(crate) fn class_type(&mut self, id: &SymbolId) -> Type {
        if id.is_empty() {
            return Type::UNKNOWN;
        }
        if let Some((cached, value)) = &self.cached_class_type
            && cached == id
        {
            return value.clone();
        }
        let symbol = self.snapshot.symbol(id).or_else(|| {
            self.document
                .symbols
                .iter()
                .find(|candidate| candidate.id == *id)
                .cloned()
        });
        let Some(symbol) = symbol else {
            return Type::UNKNOWN;
        };
        let arguments = symbol
            .templates()
            .iter()
            .map(|template| Type::template(&template.name))
            .collect();
        let result = Type::named(&symbol.fully_qualified, arguments);
        self.cached_class_type = Some((id.clone(), result.clone()));
        result
    }

    fn inherited_receiver_types(&mut self, class: &Symbol) -> Vec<Type> {
        if !class.id.is_empty()
            && let Some((cached, receivers)) = &self.cached_parent_receivers
            && *cached == class.id
        {
            return receivers.clone();
        }
        let typed: Vec<&Type> = class
            .extends_types()
            .iter()
            .chain(class.implements_types())
            .collect();
        let result: Vec<Type> = class
            .extends()
            .iter()
            .chain(class.implements())
            .map(|name| {
                typed
                    .iter()
                    .find(|candidate| {
                        candidate.kind() == Kind::Object && eq_ignore_case(candidate.name(), name)
                    })
                    .map_or_else(|| Type::named(name, Vec::new()), |found| (*found).clone())
            })
            .collect();
        if !class.id.is_empty() {
            self.cached_parent_receivers = Some((class.id.clone(), result.clone()));
        }
        result
    }

    pub(crate) fn report(&mut self, node: &SyntaxNode, code: &str, message: String) {
        self.report_range(node.range_trimmed_trivia(), code, message);
    }

    pub(crate) fn report_range(&mut self, range: TextRange, code: &str, message: String) {
        let start: u32 = range.start().into();
        if self.suppressions.suppresses(start, code) {
            return;
        }
        let key = format!("{code}:{start}:{message}");
        if !self.issues.insert(key) {
            return;
        }
        self.document.issues.push(Issue {
            range,
            code: Str::from(code),
            message: Str::from(message),
        });
    }

    /// The name context at a source offset; contexts are cached per scope.
    pub(crate) fn name_context_at(&mut self, offset: u32) -> NameContext {
        let scope = self
            .document
            .scope_at(offset)
            .map(|scope| scope.id as usize);
        match scope {
            None => {
                if let Some(context) = &self.document_name_context {
                    return context.clone();
                }
                let mut context = NameContext::new(&self.document.namespace);
                context.phpdoc_aliases = self.document_aliases();
                self.document_name_context = Some(context.clone());
                context
            }
            Some(index) => {
                if let Some(Some(context)) = self.name_contexts.get(index) {
                    return context.clone();
                }
                let aliases = self.document_aliases();
                let scope = &self.document.scopes[index];
                let mut context =
                    NameContext::with_imports(scope.namespace.clone(), &scope.imports);
                context.phpdoc_aliases = aliases;
                if self.name_contexts.len() <= index {
                    self.name_contexts.resize(index + 1, None);
                }
                self.name_contexts[index] = Some(context.clone());
                context
            }
        }
    }

    fn document_aliases(&mut self) -> Arc<HashMap<Str, Type>> {
        if let Some(context) = &self.document_name_context {
            return context.phpdoc_aliases.clone();
        }
        if let Some(context) = self.name_contexts.iter().flatten().next() {
            return context.phpdoc_aliases.clone();
        }
        Arc::new(self.document.type_aliases.clone())
    }

    pub(crate) fn record_fact(
        &mut self,
        node: &SyntaxNode,
        value: &Type,
        confidence: Confidence,
        source: TypeSource,
        reason: Cow<'static, str>,
    ) {
        let identity = node_identity(node);
        // Absence already means unknown; delete so a later fixed-point pass
        // cannot leave a stale known result behind.
        if value.is_unknown() {
            self.document.delete_type_fact(identity);
            return;
        }
        self.document.set_type_fact(
            identity,
            TypeFact {
                ty: value.clone(),
                confidence,
                source,
                origin: node.range_trimmed_trivia(),
                reason,
            },
        );
    }

    pub(crate) fn type_fact(&self, identity: NodeId) -> Option<TypeFact> {
        self.document.type_fact(identity)
    }
}

/// The result of analyzing a statement.
pub(crate) struct Flow {
    pub(crate) env: Env,
    pub(crate) terminated: bool,
    pub(crate) continues: Vec<Env>,
}

impl Flow {
    pub(crate) fn of(env: Env) -> Flow {
        Flow {
            env,
            terminated: false,
            continues: Vec::new(),
        }
    }

    fn terminated(env: Env) -> Flow {
        Flow {
            env,
            terminated: true,
            continues: Vec::new(),
        }
    }
}

/// Analysis state of one function body (or the top level).
pub(crate) struct Fs<'a, 'e, 's> {
    pub(crate) st: &'a mut AnalyzerState<'e>,
    pub(crate) snapshot: &'s Snapshot,
    pub(crate) relations: Relations<'s>,
    pub(crate) symbol: Symbol,
    pub(crate) scope: u32,
    pub(crate) current_class: Type,
    pub(crate) named_types: NamedTypeCache,
    pub(crate) generator: bool,
    pub(crate) returns: Vec<Type>,
    pub(crate) dependencies: Option<FxHashSet<SymbolId>>,
}

impl<'a, 'e, 's> Fs<'a, 'e, 's> {
    pub(crate) fn new(
        st: &'a mut AnalyzerState<'e>,
        snapshot: &'s Snapshot,
        symbol: Symbol,
        current_class: Type,
    ) -> Self {
        Fs {
            st,
            snapshot,
            relations: snapshot.relations(),
            symbol,
            scope: 0,
            current_class,
            named_types: NamedTypeCache::default(),
            generator: false,
            returns: Vec::new(),
            dependencies: None,
        }
    }

    /// A nested state (closure body) sharing the analyzer state.
    pub(crate) fn nested<'b>(&'b mut self, symbol: Symbol) -> Fs<'b, 'e, 's> {
        Fs {
            st: &mut *self.st,
            snapshot: self.snapshot,
            relations: self.relations,
            symbol,
            scope: self.scope,
            current_class: self.current_class.clone(),
            named_types: self.named_types.clone(),
            generator: self.generator,
            returns: Vec::new(),
            dependencies: None,
        }
    }

    pub(crate) fn src(&self) -> Source<'e> {
        self.st.source
    }

    pub(crate) fn members(&self) -> MemberResolver<'s> {
        MemberResolver::new(self.snapshot)
    }

    pub(crate) fn join(&self, left: &Env, right: &Env) -> Env {
        join_environments(self.relations, left, right)
    }

    pub(crate) fn named_type(&mut self, name: &str) -> Type {
        self.named_types.type_for(name)
    }

    pub(crate) fn name_context_at(&mut self, offset: u32) -> NameContext {
        self.st.name_context_at(offset)
    }

    pub(crate) fn parse_native_type(&mut self, source: &str, offset: u32) -> Type {
        match parse_native(source) {
            Ok(value) => self.name_context_at(offset).resolve_type(&value),
            Err(_) => Type::ERROR,
        }
    }

    pub(crate) fn type_from_syntax(&mut self, node: &SyntaxNode) -> Type {
        let text = compact(self.src().text(node)).into_owned();
        self.parse_native_type(&text, start(node))
    }

    pub(crate) fn record(
        &mut self,
        node: &SyntaxNode,
        value: &Type,
        source: TypeSource,
        reason: impl Into<Cow<'static, str>>,
    ) {
        self.st
            .record_fact(node, value, Confidence::Inferred, source, reason.into());
    }

    pub(crate) fn record_opt(
        &mut self,
        node: Option<&SyntaxNode>,
        value: &Type,
        source: TypeSource,
        reason: impl Into<Cow<'static, str>>,
    ) {
        if let Some(node) = node {
            self.record(node, value, source, reason);
        }
    }

    pub(crate) fn report(&mut self, node: &SyntaxNode, code: &str, message: String) {
        self.st.report(node, code, message);
    }

    fn inherited_method_parameter_types(&mut self) -> Vec<Type> {
        let method = &self.symbol;
        if method.kind != SymbolKind::Method || method.container.is_empty() {
            return Vec::new();
        }
        let Some(class) = self.snapshot.symbol(&method.container) else {
            return Vec::new();
        };
        if !class.is_class_like() {
            return Vec::new();
        }
        let parents = self.st.inherited_receiver_types(&class);
        if parents.is_empty() {
            return Vec::new();
        }
        let method = &self.symbol;
        let relations = self.relations;
        let mut result = vec![Type::UNKNOWN; method.parameters.len()];
        let members = MemberResolver::new(self.snapshot);
        #[allow(clippy::needless_range_loop)]
        for parent in &parents {
            members.visit_methods(parent, &method.name, |inherited| {
                let count = method
                    .parameters
                    .len()
                    .min(inherited.symbol.parameters.len());
                for index in 0..count {
                    let own = &method.parameters[index];
                    let candidate = &inherited.symbol.parameters[index];
                    if !own.doc_type.is_unknown()
                        || candidate.doc_type.is_unknown()
                        || contains_uncertain(&candidate.ty)
                        || !relations.is_assignable_to(&candidate.ty, &own.native_type)
                    {
                        continue;
                    }
                    result[index] = if result[index].is_unknown() {
                        candidate.ty.clone()
                    } else {
                        relations.join(&result[index], &candidate.ty)
                    };
                }
                true
            });
        }
        result
    }

    fn analyze_top_level_nodes(&mut self, nodes: &[SyntaxNode], env: Env) -> Env {
        let mut current = env;
        for node in nodes {
            match node.kind() {
                PHP_NAMESPACE => {
                    if let Some(block) = query::direct_child(node, PHP_BLOCK) {
                        let mut nested = Fs::new(
                            &mut *self.st,
                            self.snapshot,
                            Symbol::default(),
                            Type::UNKNOWN,
                        );
                        nested.analyze_top_level_nodes(&direct_nodes(&block), Env::new(0));
                    }
                }
                PHP_CLASS_DECLARATION
                | PHP_INTERFACE_DECLARATION
                | PHP_TRAIT_DECLARATION
                | PHP_ENUM_DECLARATION
                | PHP_FUNCTION_DECLARATION
                | PHP_USE_DECLARATION
                | PHP_CONST_DECLARATION => {}
                _ => {
                    let result = self.analyze_statement(node, current);
                    current = result.env;
                    if result.terminated {
                        return current;
                    }
                }
            }
        }
        current
    }

    pub(crate) fn analyze_block(&mut self, node: &SyntaxNode, env: &Env) -> Flow {
        self.analyze_block_owned(node, env.fork())
    }

    pub(crate) fn analyze_block_owned(&mut self, node: &SyntaxNode, env: Env) -> Flow {
        let mut current = env;
        let mut continues = Vec::new();
        for child in node.children() {
            let result = self.analyze_statement(&child, current);
            continues.extend(result.continues);
            current = result.env;
            if result.terminated {
                return Flow {
                    env: current,
                    terminated: true,
                    continues,
                };
            }
        }
        Flow {
            env: current,
            terminated: false,
            continues,
        }
    }

    pub(crate) fn analyze_statement(&mut self, node: &SyntaxNode, env: Env) -> Flow {
        if env.is_impossible() {
            return Flow::terminated(env);
        }
        let annotations = self.statement_var_annotations(node);
        self.apply_var_annotations(&annotations, &env, start(node));
        match node.kind() {
            PHP_BLOCK => self.analyze_block(node, &env),
            PHP_EXPRESSION_STATEMENT => {
                if let Some(expression) = first_node(node) {
                    if is_call_named(&expression, "unset") {
                        self.apply_unset(&expression, &env);
                        return Flow::of(env);
                    }
                    let value = self.infer(&expression, &env);
                    self.apply_var_annotations(&annotations, &env, trimmed_start(node));
                    if value.kind() == Kind::Never {
                        return Flow::terminated(env);
                    }
                    if is_call_named(&expression, "assert")
                        && let Some(condition) = query::argument_expression(&expression, 0)
                    {
                        let (narrowed, _) = self.condition_environments(Some(&condition), &env);
                        return Flow::of(narrowed);
                    }
                    self.apply_unconditional_call_assertions(&expression, &env);
                }
                Flow::of(env)
            }
            PHP_RETURN_STATEMENT => {
                let expression = first_node(node);
                let value = match &expression {
                    Some(expression) => self.infer(expression, &env),
                    None => Type::NULL,
                };
                self.validate_return(node, &value, expression.is_some());
                self.returns.push(value);
                Flow::terminated(env)
            }
            PHP_THROW_STATEMENT => {
                if let Some(expression) = first_node(node) {
                    self.infer(&expression, &env);
                }
                Flow::terminated(env)
            }
            PHP_BREAK_STATEMENT => Flow::terminated(env),
            PHP_CONTINUE_STATEMENT => Flow {
                env: env.clone(),
                terminated: true,
                continues: vec![env],
            },
            PHP_IF_STATEMENT => self.analyze_if(node, env),
            PHP_FOREACH_STATEMENT => self.analyze_foreach(node, env, &annotations),
            PHP_FOR_STATEMENT => self.analyze_for(node, env),
            PHP_WHILE_STATEMENT | PHP_DO_WHILE_STATEMENT => self.analyze_loop(node, env),
            PHP_TRY_STATEMENT => self.analyze_try(node, env),
            PHP_SWITCH_STATEMENT => self.analyze_switch(node, env),
            _ => {
                for child in node.children() {
                    if is_expression_kind(child.kind()) {
                        self.infer(&child, &env);
                    }
                }
                Flow::of(env)
            }
        }
    }

    fn statement_var_annotations(&mut self, node: &SyntaxNode) -> BTreeMap<String, Type> {
        let comment = leading_doc_comment(self.src(), node);
        if !comment.contains("@var") {
            return BTreeMap::new();
        }
        let documentation = swlsp_php_phpdoc::parse(comment);
        if documentation.vars.is_empty() {
            return BTreeMap::new();
        }
        let mut templates = Vec::new();
        collect_type_template_names(&self.current_class, &mut templates);
        let context = self.name_context_at(start(node));
        let mut result = BTreeMap::new();
        for (name, value) in &documentation.vars {
            let resolved = context.resolve_phpdoc_type(value, &templates);
            if resolved.is_unknown() || resolved.kind() == Kind::Error {
                continue;
            }
            let name = if name.is_empty() {
                statement_annotation_target(node)
            } else {
                name.clone()
            };
            if !name.is_empty() {
                result.insert(name, resolved);
            }
        }
        result
    }

    pub(crate) fn apply_var_annotations(
        &mut self,
        annotations: &BTreeMap<String, Type>,
        env: &Env,
        offset: u32,
    ) {
        for (name, value) in annotations {
            env.set(name, value.clone());
            self.update_local_type(name, offset, value);
        }
    }

    fn validate_return(&mut self, node: &SyntaxNode, value: &Type, has_expression: bool) {
        if self.generator {
            return;
        }
        let expected = resolve_special_type(
            &self.symbol.return_type,
            &self.current_class,
            &self.current_class,
        );
        if expected.is_unknown() || expected.kind() == Kind::Mixed || contains_uncertain(&expected)
        {
            return;
        }
        if expected.kind() == Kind::Void {
            if has_expression {
                self.report(
                    node,
                    "php.returnType",
                    "Void function must not return a value".to_owned(),
                );
            }
            return;
        }
        if !has_expression {
            self.report(
                node,
                "php.returnType",
                format!("Expected return type {expected}, got no value"),
            );
            return;
        }
        if !contains_uncertain(value) && !self.relations.is_assignable_to(value, &expected) {
            self.report(
                node,
                "php.returnType",
                format!("Expected return type {expected}, got {value}"),
            );
        }
    }

    fn analyze_if(&mut self, node: &SyntaxNode, env: Env) -> Flow {
        let children = direct_nodes(node);
        if children.is_empty() {
            return Flow::of(env);
        }
        let (true_env, mut false_env) = self.condition_environments(Some(&children[0]), &env);
        let mut outcomes = Vec::new();
        let mut index = 1;
        if index < children.len()
            && children[index].kind() != PHP_ELSE_IF_CLAUSE
            && children[index].kind() != PHP_ELSE_CLAUSE
        {
            outcomes.push(self.analyze_statement(&children[index], true_env));
            index += 1;
        }
        while index < children.len() && children[index].kind() == PHP_ELSE_IF_CLAUSE {
            let clause = direct_nodes(&children[index]);
            if !clause.is_empty() {
                let (clause_true, clause_false) =
                    self.condition_environments(Some(&clause[0]), &false_env);
                if clause.len() > 1 {
                    outcomes.push(self.analyze_statement(&clause[1], clause_true));
                }
                false_env = clause_false;
            }
            index += 1;
        }
        if index < children.len() && children[index].kind() == PHP_ELSE_CLAUSE {
            if let Some(statement) = first_node(&children[index]) {
                outcomes.push(self.analyze_statement(&statement, false_env));
            }
        } else {
            outcomes.push(Flow::of(false_env));
        }
        self.join_flows(outcomes)
    }

    fn analyze_foreach(
        &mut self,
        node: &SyntaxNode,
        env: Env,
        annotations: &BTreeMap<String, Type>,
    ) -> Flow {
        let nodes = direct_nodes(node);
        if nodes.is_empty() {
            return Flow::of(env);
        }
        let iterable = self.infer(&nodes[0], &env);
        let (key_type, value_type) = iterable_types(&iterable, self.relations);
        let loop_env = env.fork();
        let (key_variable, value_variable) = direct_foreach_variables(node);
        match (&key_variable, &value_variable) {
            (None, Some(value_variable)) => {
                let name = variable_key(value_variable);
                loop_env.set(&name, value_type.clone());
                self.record(
                    value_variable,
                    &value_type,
                    TypeSource::Assignment,
                    "foreach value",
                );
                self.update_local_type(&name, start(value_variable), &value_type);
            }
            (Some(key_variable), Some(value_variable)) => {
                let key_name = variable_key(key_variable);
                let value_name = variable_key(value_variable);
                loop_env.set(&key_name, key_type.clone());
                loop_env.set(&value_name, value_type.clone());
                self.update_local_type(&key_name, start(key_variable), &key_type);
                self.update_local_type(&value_name, start(value_variable), &value_type);
            }
            _ => {}
        }
        self.apply_var_annotations(annotations, &loop_env, trimmed_start(node));
        let body = &nodes[nodes.len() - 1];
        let body_flow = if body.kind() == PHP_BLOCK {
            self.analyze_block_owned(body, loop_env)
        } else {
            self.analyze_statement(body, loop_env)
        };
        let mut result = self.join(&env, &body_flow.env);
        for continued in &body_flow.continues {
            result = self.join(&result, continued);
        }
        if !body_flow.terminated
            && body_flow.continues.is_empty()
            && query::nodes(body, &[PHP_BREAK_STATEMENT]).is_empty()
            && nodes[0].kind() == PHP_VARIABLE
        {
            // When the iterated array itself is transformed without an early
            // exit, every element passes through the body; the pre-loop shape
            // cannot survive the loop.
            let name = variable_key(&nodes[0]);
            if let (Some(before), Some(after)) = (env.get(&name), body_flow.env.get(&name))
                && before != after
                && is_array_container_type(&before)
                && is_array_container_type(&after)
            {
                result.set(&name, after);
            }
        }
        Flow::of(result)
    }

    fn analyze_loop(&mut self, node: &SyntaxNode, env: Env) -> Flow {
        let nodes = direct_nodes(node);
        if node.kind() == PHP_WHILE_STATEMENT && !nodes.is_empty() {
            let (true_env, false_env) = self.condition_environments(Some(&nodes[0]), &env);
            if nodes.len() == 1 {
                return Flow::of(false_env);
            }
            let body = self.analyze_statement(&nodes[nodes.len() - 1], true_env);
            let mut result = self.join(&false_env, &body.env);
            for continued in &body.continues {
                result = self.join(&result, continued);
            }
            return Flow::of(result);
        }
        let loop_env = env.fork();
        for child in &nodes {
            if child.kind() == PHP_BLOCK {
                let body = self.analyze_block_owned(child, loop_env);
                let mut result = self.join(&env, &body.env);
                for continued in &body.continues {
                    result = self.join(&result, continued);
                }
                return Flow::of(result);
            }
            self.infer(child, &loop_env);
        }
        Flow::of(self.join(&env, &loop_env))
    }

    /// `for (init; condition; update) body`: the body runs where the
    /// condition holds (`$parent !== null` narrows `$parent` inside it), and
    /// the loop is left where it does not.
    fn analyze_for(&mut self, node: &SyntaxNode, env: Env) -> Flow {
        let mut sections: [Vec<SyntaxNode>; 3] = Default::default();
        let mut section = 0;
        let mut body = None;
        let mut header_closed = false;
        for element in node.children_with_tokens() {
            match element {
                swlsp_syntax::rowan::NodeOrToken::Token(token) => match token.kind() {
                    TK_SEMICOLON if !header_closed => section = (section + 1).min(2),
                    TK_CLOSE_PAREN if !header_closed => header_closed = true,
                    _ => {}
                },
                swlsp_syntax::rowan::NodeOrToken::Node(child) => {
                    if header_closed {
                        body = Some(child);
                    } else if is_expression_kind(child.kind()) {
                        sections[section].push(child);
                    }
                }
            }
        }
        let [init, conditions, updates] = sections;
        for expression in &init {
            self.infer(expression, &env);
        }
        // Every condition expression is evaluated; the last one decides.
        let (true_env, false_env) = match conditions.split_last() {
            Some((last, earlier)) => {
                for expression in earlier {
                    self.infer(expression, &env);
                }
                self.condition_environments(Some(last), &env)
            }
            None => (env.fork(), env.fork()),
        };
        let body_flow = match &body {
            Some(body) => self.analyze_statement(body, true_env),
            None => Flow::of(true_env),
        };
        let mut iteration = body_flow.env;
        for continued in &body_flow.continues {
            iteration = self.join(&iteration, continued);
        }
        for expression in &updates {
            self.infer(expression, &iteration);
        }
        let result = if conditions.is_empty() {
            self.join(&env, &iteration)
        } else {
            self.join(&false_env, &iteration)
        };
        Flow::of(result)
    }

    fn analyze_try(&mut self, node: &SyntaxNode, env: Env) -> Flow {
        let mut outcomes = Vec::new();
        for child in node.children() {
            match child.kind() {
                PHP_BLOCK => outcomes.push(self.analyze_block(&child, &env)),
                PHP_CATCH_CLAUSE => {
                    let catch_env = env.fork();
                    let mut catch_type = Type::UNKNOWN;
                    for clause_node in child.children() {
                        if is_type_kind(clause_node.kind()) {
                            catch_type = self.type_from_syntax(&clause_node);
                        }
                        if clause_node.kind() == PHP_VARIABLE {
                            catch_env.set(&variable_key(&clause_node), catch_type.clone());
                        }
                        if clause_node.kind() == PHP_BLOCK {
                            outcomes
                                .push(self.analyze_block_owned(&clause_node, catch_env.clone()));
                        }
                    }
                }
                PHP_FINALLY_CLAUSE => {
                    if let Some(last) = child.last_child() {
                        let base = self.join_flows(outcomes);
                        return self.analyze_statement(&last, base.env);
                    }
                }
                _ => {}
            }
        }
        self.join_flows(outcomes)
    }

    fn analyze_switch(&mut self, node: &SyntaxNode, env: Env) -> Flow {
        let children = direct_nodes(node);
        let mut switch_true = false;
        if let Some(selector) = children.first()
            && selector.kind() != PHP_CASE_CLAUSE
        {
            self.infer(selector, &env);
            switch_true = is_true_condition(self.src(), selector);
        }
        let mut outcomes = Vec::new();
        let mut remaining = env.fork();
        let mut fallthrough_env: Option<Env> = None;
        let mut has_default = false;
        for child in &children {
            if child.kind() != PHP_CASE_CLAUSE {
                continue;
            }
            let clause = direct_nodes(child);
            let mut statement_index = 0;
            let mut clause_env = env.fork();
            if clause
                .first()
                .is_some_and(|first| is_expression_kind(first.kind()))
            {
                let condition = &clause[0];
                statement_index = 1;
                if switch_true {
                    let (matched, unmatched) =
                        self.condition_environments(Some(condition), &remaining);
                    clause_env = matched;
                    remaining = unmatched;
                } else {
                    self.infer(condition, &clause_env);
                }
            } else {
                has_default = true;
                clause_env = remaining.clone();
            }
            if let Some(fallthrough) = &fallthrough_env {
                clause_env = self.join(fallthrough, &clause_env);
            }
            let mut outcome = Flow::of(clause_env);
            let mut broke = false;
            while statement_index < clause.len() {
                let statement = &clause[statement_index];
                outcome = self.analyze_statement(statement, outcome.env);
                if statement.kind() == PHP_BREAK_STATEMENT {
                    outcome.terminated = false;
                    broke = true;
                    break;
                }
                if outcome.terminated {
                    break;
                }
                statement_index += 1;
            }
            let falls_through = !broke && !outcome.terminated;
            fallthrough_env = falls_through.then(|| outcome.env.clone());
            outcomes.push(outcome);
        }
        if !has_default {
            outcomes.push(Flow::of(remaining));
        }
        if outcomes.is_empty() {
            return Flow::of(env);
        }
        self.join_flows(outcomes)
    }

    pub(crate) fn join_flows(&mut self, flows: Vec<Flow>) -> Flow {
        let mut result: Option<Env> = None;
        let mut continues = Vec::new();
        let mut all_terminated = !flows.is_empty();
        for candidate in flows {
            continues.extend(candidate.continues);
            if candidate.terminated {
                continue;
            }
            all_terminated = false;
            result = Some(match result {
                None => candidate.env,
                Some(result) => self.join(&result, &candidate.env),
            });
        }
        Flow {
            env: result.unwrap_or_else(|| Env::new(0)),
            terminated: all_terminated,
            continues,
        }
    }

    /// Joins `value` into the local symbol named `name` visible at `offset`.
    pub(crate) fn update_local_type(&mut self, name: &str, offset: u32, value: &Type) {
        if value.is_unknown() {
            return;
        }
        let document = &mut self.st.document;
        let Some(mut scope_index) = document.scope_at(offset).map(|scope| scope.id as usize) else {
            return;
        };
        loop {
            let scope = &document.scopes[scope_index];
            let ids: Vec<SymbolId> = scope.symbol_ids(&document.symbols, name).cloned().collect();
            let parent = scope.parent as usize;
            let id = scope.id as usize;
            for candidate in &ids {
                if let Some(symbol) = document
                    .symbols
                    .iter_mut()
                    .find(|symbol| symbol.id == *candidate && symbol.kind == SymbolKind::Local)
                {
                    symbol.ty = if symbol.ty.is_unknown() {
                        value.clone()
                    } else {
                        self.relations.join(&symbol.ty, value)
                    };
                    return;
                }
            }
            if id == parent || parent >= document.scopes.len() {
                return;
            }
            scope_index = parent;
        }
    }
}

fn statement_annotation_target(node: &SyntaxNode) -> String {
    match node.kind() {
        PHP_EXPRESSION_STATEMENT => {
            if let Some(expression) = first_node(node)
                && expression.kind() == PHP_ASSIGNMENT_EXPRESSION
                && let Some(left) = first_node(&expression)
                && left.kind() == PHP_VARIABLE
            {
                return variable_key(&left);
            }
        }
        PHP_FOREACH_STATEMENT => {
            if let (_, Some(value)) = direct_foreach_variables(node) {
                return variable_key(&value);
            }
        }
        _ => {}
    }
    String::new()
}
