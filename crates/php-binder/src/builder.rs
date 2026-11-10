//! Document construction: top-level statements and declarations.

use std::collections::HashMap;

use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::fast::Fast;
use rustc_hash::{FxHashMap, FxHashSet};
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_resolver::{ImportKind, NameContext, parse_use_declaration};
use swlsp_php_semantic::{
    Confidence, ConstantArrayItem, ConstantReturn, Document, Flags, ImportTable, LiteralReturn,
    NodeId, Parameter, Scope, ScopeId, ScopeKind, Str, Symbol, SymbolId, SymbolKind, TypeFact,
    TypeSource, attribute_named, new_symbol_id, node_identity,
};
use swlsp_php_types::{Kind as TypeKind, Type};
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};

use crate::capacity::{estimated_document_capacities, estimated_non_literal_type_fact_capacity};
use crate::modifiers::{
    apply_attribute_type_semantics, attribute_refined_type, bind_attribute_value, bind_attributes,
    declaration_flags, declaration_visibility, declaration_write_visibility, has_token, text,
};
use crate::phpdoc::{
    append_unique, apply_attribute_flags, apply_document_flags, bind_assertions, bind_templates,
    leading_doc_comment, resolve_doc_types, template_names,
};
use crate::trait_alias::bind_trait_aliases;

pub use crate::phpdoc::effective_type;

/// Indexes into `import_maps` of the class, function and constant import
/// maps a scope uses (`None` before the namespace has one).
type ImportMapIds = [Option<u32>; 3];

pub(crate) fn bind(path: &str, version: i64, root: Option<&SyntaxNode>, source: &str) -> Document {
    let mut document = Document {
        path: Str::from(path),
        version,
        ..Document::default()
    };
    let Some(root) = root else {
        return document;
    };
    let capacities = estimated_document_capacities(root);
    document.symbols.reserve_exact(capacities.symbols);
    document.scopes.reserve_exact(capacities.scopes);
    document.references.reserve_exact(capacities.references);
    document.reserve_type_facts(estimated_non_literal_type_fact_capacity(
        u32::from(root.text_range().len()),
        capacities.literals,
    ));
    let mut builder = Builder {
        source,
        path: document.path.clone(),
        root_end: root.text_range().end(),
        doc: document,
        declarations: FxHashSet::default(),
        symbol_indexes: FxHashMap::default(),
        import_maps: Vec::new(),
        current_imports: [None; 3],
        scope_imports: Vec::with_capacity(capacities.scopes),
        order_pool: Vec::new(),
    };
    builder.declarations.reserve(capacities.symbols);
    builder.symbol_indexes.reserve(capacities.symbols);
    let context = NameContext::new("");
    let file_scope = builder.new_scope(
        ScopeKind::File,
        0,
        root.text_range(),
        SymbolId::default(),
        &context,
    );
    builder.bind_top_level(root, file_scope, &context);
    builder.finish()
}

pub(crate) struct Builder<'s> {
    pub(crate) source: &'s str,
    pub(crate) path: Str,
    root_end: swlsp_syntax::TextSize,
    pub(crate) doc: Document,
    declarations: FxHashSet<NodeId>,
    /// First document index of every symbol ID.
    symbol_indexes: FxHashMap<SymbolId, u32>,
    /// Import maps shared by every scope of a namespace, materialized into
    /// scopes by `finish`.
    import_maps: Vec<HashMap<Str, Str>>,
    current_imports: ImportMapIds,
    scope_imports: Vec<ImportMapIds>,
    /// Reusable preorder buffers for body binding.
    pub(crate) order_pool: Vec<Vec<SyntaxNode>>,
}

impl<'s> Builder<'s> {
    fn finish(mut self) -> Document {
        let empty = HashMap::new();
        let map = |id: Option<u32>| id.map_or(&empty, |id| &self.import_maps[id as usize]);
        for (scope, ids) in self.doc.scopes.iter_mut().zip(&self.scope_imports) {
            if ids.iter().all(Option::is_none) {
                continue;
            }
            scope.imports = ImportTable {
                classes: map(ids[0]).clone(),
                functions: map(ids[1]).clone(),
                constants: map(ids[2]).clone(),
            };
        }
        self.doc
    }

    pub(crate) fn text(&self, node: &SyntaxNode) -> &'s str {
        text(self.source, node)
    }

    fn bind_top_level(
        &mut self,
        parent: &SyntaxNode,
        initial_scope: ScopeId,
        initial_context: &NameContext,
    ) {
        let initial_imports = self.current_imports;
        let mut scope = initial_scope;
        let mut context = initial_context.clone();
        let mut open_namespace_scope: Option<ScopeId> = None;

        for node in parent.children() {
            if node.kind() == PHP_NAMESPACE {
                if let Some(open) = open_namespace_scope {
                    let current = self.doc.scopes[open as usize].range;
                    self.doc.scopes[open as usize].range =
                        TextRange::new(current.start(), node.text_range().start());
                }
                let name = node
                    .child_node_of_kind(PHP_NAME)
                    .map(|name| crate::fast::name_value(&name, self.source))
                    .unwrap_or_default();
                let namespace_context = NameContext::new(&name);
                if let Some(block) = node.child_node_of_kind(PHP_BLOCK) {
                    self.current_imports = [None; 3];
                    let namespace_scope = self.new_scope(
                        ScopeKind::Namespace,
                        initial_scope,
                        block.text_range(),
                        SymbolId::default(),
                        &namespace_context,
                    );
                    self.bind_top_level(&block, namespace_scope, &namespace_context);
                    scope = initial_scope;
                    context = initial_context.clone();
                    self.current_imports = initial_imports;
                    open_namespace_scope = None;
                    continue;
                }
                let namespace_range = TextRange::new(node.text_range().end(), self.root_end);
                self.current_imports = [None; 3];
                let namespace_scope = self.new_scope(
                    ScopeKind::Namespace,
                    initial_scope,
                    namespace_range,
                    SymbolId::default(),
                    &namespace_context,
                );
                scope = namespace_scope;
                context = namespace_context;
                open_namespace_scope = Some(namespace_scope);
                if self.doc.namespace.is_empty() {
                    self.doc.namespace = Str::from(name);
                }
                continue;
            }

            match node.kind() {
                PHP_USE_DECLARATION => self.bind_use_declaration(&node, scope, &mut context),
                PHP_CLASS_DECLARATION
                | PHP_INTERFACE_DECLARATION
                | PHP_TRAIT_DECLARATION
                | PHP_ENUM_DECLARATION => self.bind_class(&node, scope, &context),
                PHP_FUNCTION_DECLARATION => {
                    self.bind_function(&node, scope, &context, &SymbolId::default());
                }
                PHP_CONST_DECLARATION => {
                    self.bind_constants(&node, scope, &context, &SymbolId::default(), false);
                }
                _ => self.bind_body_node(&node, scope, &context, &SymbolId::default()),
            }
        }
    }

    fn bind_use_declaration(
        &mut self,
        node: &SyntaxNode,
        scope: ScopeId,
        context: &mut NameContext,
    ) {
        let mut created = false;
        for import in parse_use_declaration(self.text(node)) {
            if import.target.is_empty() {
                continue;
            }
            let slot = match import.kind {
                ImportKind::Class => 0,
                ImportKind::Function => 1,
                ImportKind::Constant => 2,
            };
            let map = match self.current_imports[slot] {
                Some(id) => id,
                None => {
                    let id = self.import_maps.len() as u32;
                    self.import_maps.push(HashMap::new());
                    self.current_imports[slot] = Some(id);
                    created = true;
                    id
                }
            };
            let alias = if import.alias.is_empty() {
                import
                    .target
                    .rsplit_once('\\')
                    .map_or(import.target.as_str(), |(_, last)| last)
            } else {
                import.alias.as_str()
            };
            let key = if import.kind == ImportKind::Constant {
                Str::from(alias)
            } else {
                Str::from(lowercase(alias))
            };
            self.import_maps[map as usize].insert(key, Str::from(import.target.as_str()));
            context.add_import(&import);
        }
        if created {
            self.publish_scope_imports(scope);
        }
    }

    fn publish_scope_imports(&mut self, root: ScopeId) {
        for index in 0..self.doc.scopes.len() {
            if self.scope_shares_import_context(index as ScopeId, root) {
                self.scope_imports[index] = self.current_imports;
            }
        }
    }

    fn scope_shares_import_context(&self, mut scope: ScopeId, root: ScopeId) -> bool {
        while scope != root {
            let Some(current) = self.doc.scopes.get(scope as usize) else {
                return false;
            };
            if current.kind == ScopeKind::Namespace || current.parent == scope {
                return false;
            }
            scope = current.parent;
        }
        (root as usize) < self.doc.scopes.len()
    }

    pub(crate) fn bind_class(
        &mut self,
        node: &SyntaxNode,
        parent_scope: ScopeId,
        context: &NameContext,
    ) {
        let name_node = node.child_node_of_kind(PHP_NAME);
        let name = crate::fast::name_of(name_node.as_ref(), self.source);
        if name.is_empty() {
            return;
        }
        let fully_qualified = qualify(&context.namespace, &name);
        let documentation = swlsp_php_phpdoc::parse(leading_doc_comment(node, self.source));
        let class_templates = template_names(&documentation.templates);
        let context = self.with_phpdoc_aliases(
            context.clone(),
            &fully_qualified,
            &documentation,
            &class_templates,
        );
        let kind = match node.kind() {
            PHP_INTERFACE_DECLARATION => SymbolKind::Interface,
            PHP_TRAIT_DECLARATION => SymbolKind::Trait,
            PHP_ENUM_DECLARATION => SymbolKind::Enum,
            _ => SymbolKind::Class,
        };
        let mut symbol = Symbol {
            id: new_symbol_id(
                kind,
                &fully_qualified,
                &self.path,
                node.text_range().start().into(),
            ),
            kind,
            name: Str::from(name.as_str()),
            fully_qualified: Str::from(fully_qualified.as_str()),
            path: self.path.clone(),
            range: node.trimmed(),
            selection_range: selection_range(name_node.as_ref(), node),
            flags: declaration_flags(node),
            ..Symbol::default()
        };
        symbol.set_metadata(
            bind_attributes(node, &context, self.source),
            Vec::new(),
            documentation.summary.as_str(),
        );
        apply_document_flags(&mut symbol, &documentation);
        apply_attribute_type_semantics(&mut symbol, &context);
        apply_attribute_flags(&mut symbol);
        let templates = bind_templates(&documentation.templates, &context, &[]);
        let mut extends = resolve_names(&context, query::class_extends(node));
        let mut implements = resolve_names(&context, query::class_implements(node));
        let extends_types = resolve_doc_types(&documentation.extends, &context, &class_templates);
        let implements_types =
            resolve_doc_types(&documentation.implements, &context, &class_templates);
        let trait_types = resolve_doc_types(&documentation.uses, &context, &class_templates);
        for extended in &extends_types {
            append_unique(&mut extends, extended.name());
        }
        for implemented in &implements_types {
            append_unique(&mut implements, implemented.name());
        }
        let mut traits = Vec::new();
        for used in &trait_types {
            append_unique(&mut traits, used.name());
        }
        let body = node.child_node_of_kind(PHP_CLASS_BODY);
        if let Some(body) = &body {
            symbol.body_range = body.trimmed();
        }
        if kind == SymbolKind::Interface {
            implements.append(&mut extends);
        }
        let mut enum_backing = Type::unknown();
        if kind == SymbolKind::Enum {
            append_unique(&mut implements, "UnitEnum");
            enum_backing = self.enum_backing_type(node, &context);
            if !enum_backing.is_unknown() {
                append_unique(&mut implements, "BackedEnum");
            }
        }
        symbol.set_templates(templates);
        symbol.set_hierarchy(
            extends,
            implements,
            traits,
            extends_types,
            implements_types,
            trait_types,
            Vec::new(),
        );
        let symbol_id = symbol.id.clone();
        let symbol_index = self.add_symbol(parent_scope, symbol);
        self.mark_declaration(name_node.as_ref());
        self.bind_class_clauses(node, parent_scope, &context);
        self.bind_attribute_references(node, parent_scope, &context);

        let Some(body) = body else {
            return;
        };
        let class_scope = self.new_scope(
            ScopeKind::Class,
            parent_scope,
            body.text_range(),
            symbol_id.clone(),
            &context,
        );
        self.bind_type_aliases(
            &documentation,
            class_scope,
            &context,
            &symbol_id,
            &class_templates,
        );
        if kind == SymbolKind::Enum {
            self.bind_enum_runtime_members(class_scope, symbol_index, &enum_backing);
        }
        self.bind_class_members(&body, class_scope, &context, &symbol_id, symbol_index, true);
        self.bind_synthetic_members(&documentation, class_scope, &context, &symbol_id);
    }

    /// Binds the declarations of a class body. Anonymous classes have no
    /// enum cases.
    pub(crate) fn bind_class_members(
        &mut self,
        body: &SyntaxNode,
        class_scope: ScopeId,
        context: &NameContext,
        class_id: &SymbolId,
        class_index: u32,
        enum_cases: bool,
    ) {
        for member in body.children() {
            match member.kind() {
                PHP_METHOD_DECLARATION => {
                    self.bind_function(&member, class_scope, context, class_id)
                }
                PHP_PROPERTY_DECLARATION => {
                    self.bind_properties(&member, class_scope, context, class_id)
                }
                PHP_CLASS_CONST_DECLARATION => {
                    self.bind_constants(&member, class_scope, context, class_id, true);
                }
                PHP_ENUM_CASE_DECLARATION if enum_cases => {
                    self.bind_enum_case(&member, class_scope, context, class_id);
                }
                PHP_TRAIT_USE_DECLARATION => {
                    self.bind_trait_use(&member, class_scope, context, class_index);
                }
                _ => {}
            }
        }
    }

    fn bind_trait_use(
        &mut self,
        member: &SyntaxNode,
        class_scope: ScopeId,
        context: &NameContext,
        class_index: u32,
    ) {
        let mut used_traits = Vec::new();
        let stored = &self.doc.symbols[class_index as usize];
        let mut traits = stored.traits().to_vec();
        for trait_node in member.children().filter(|child| child.kind() == PHP_NAME) {
            let resolved =
                context.resolve_class(&crate::fast::name_value(&trait_node, self.source));
            used_traits.push(resolved.clone());
            traits.push(Str::from(resolved.as_str()));
            self.add_single_reference(
                &trait_node,
                swlsp_php_semantic::NameKind::Class,
                class_scope,
                &resolved,
            );
        }
        let stored = &mut self.doc.symbols[class_index as usize];
        let mut aliases = stored.trait_aliases().to_vec();
        aliases.extend(bind_trait_aliases(member, context, &used_traits));
        let extends = stored.extends().to_vec();
        let implements = stored.implements().to_vec();
        let extends_types = stored.extends_types().to_vec();
        let implements_types = stored.implements_types().to_vec();
        let trait_types = stored.trait_types().to_vec();
        stored.set_hierarchy(
            extends,
            implements,
            traits,
            extends_types,
            implements_types,
            trait_types,
            aliases,
        );
    }

    pub(crate) fn bind_function(
        &mut self,
        node: &SyntaxNode,
        parent_scope: ScopeId,
        context: &NameContext,
        container: &SymbolId,
    ) {
        let name_node = node.child_node_of_kind(PHP_NAME);
        let name = crate::fast::name_of(name_node.as_ref(), self.source);
        if name.is_empty() {
            return;
        }
        let mut kind = SymbolKind::Function;
        let mut fully_qualified = qualify(&context.namespace, &name);
        if !container.is_empty() {
            kind = SymbolKind::Method;
            if let Some(class) = self.symbol(container) {
                fully_qualified = format!("{}::{name}", class.fully_qualified);
            }
        }
        let mut native_return =
            self.bind_native_type(&crate::fast::direct_type_text(node, self.source), context);
        if eq_ignore_case(&name, "__construct") && native_return.is_unknown() {
            native_return = Type::void();
        }
        let documentation = swlsp_php_phpdoc::parse(leading_doc_comment(node, self.source));
        let inherited_templates = self.container_template_names(container);
        let mut doc_templates = inherited_templates.clone();
        doc_templates.extend(template_names(&documentation.templates));
        let doc_return = context.resolve_phpdoc_type(&documentation.return_type, &doc_templates);
        let parameters: Vec<SyntaxNode> = query::parameters(node).collect();
        let planned_parameters = self.bind_planned_parameters(node, context);
        let mut symbol = Symbol {
            id: new_symbol_id(
                kind,
                &fully_qualified,
                &self.path,
                node.text_range().start().into(),
            ),
            kind,
            name: Str::from(name.as_str()),
            fully_qualified: Str::from(fully_qualified),
            container: container.clone(),
            path: self.path.clone(),
            range: node.trimmed(),
            selection_range: selection_range(name_node.as_ref(), node),
            visibility: declaration_visibility(node),
            flags: declaration_flags(node),
            return_type: effective_type(&native_return, &doc_return),
            native_type: native_return,
            doc_type: doc_return,
            ..Symbol::default()
        };
        symbol.set_metadata(
            bind_attributes(node, context, self.source),
            Vec::new(),
            documentation.summary.as_str(),
        );
        symbol.parameters = Vec::with_capacity(parameters.len() + planned_parameters.len());
        let templates = bind_templates(&documentation.templates, context, &inherited_templates);
        apply_document_flags(&mut symbol, &documentation);
        apply_attribute_type_semantics(&mut symbol, context);
        apply_attribute_flags(&mut symbol);
        let body = node.child_node_of_kind(PHP_BLOCK);
        let (literal_returns, constant_returns) = match &body {
            Some(body) => {
                symbol.body_range = body.trimmed();
                return_metadata(body, context, self.source)
            }
            None => (Vec::new(), Vec::new()),
        };
        symbol.set_signature_extras(
            templates,
            resolve_doc_types(&documentation.throws, context, &doc_templates),
            bind_assertions(&documentation.assertions, context, &doc_templates),
            literal_returns,
            constant_returns,
        );
        let symbol_id = symbol.id.clone();
        let symbol_index = self.add_symbol(parent_scope, symbol);
        self.mark_declaration(name_node.as_ref());

        let scope_range = body
            .as_ref()
            .map_or(node.text_range(), SyntaxNode::text_range);
        let function_scope = self.new_scope(
            ScopeKind::Function,
            parent_scope,
            scope_range,
            symbol_id.clone(),
            context,
        );
        self.bind_direct_type_references(node, function_scope, context, container);
        self.bind_attribute_references(node, function_scope, context);
        let constructor = !container.is_empty() && eq_ignore_case(&name, "__construct");
        let changing_defaults =
            crate::planned_parameters::changing_defaults(&self.doc.symbols[symbol_index as usize]);
        for parameter_node in &parameters {
            let mut parameter = self.bind_parameter(
                parameter_node,
                function_scope,
                context,
                &symbol_id,
                &documentation,
                &doc_templates,
            );
            // The current default is what callers must pass to keep it.
            if parameter.default_value.is_none()
                && changing_defaults
                    .iter()
                    .any(|name| eq_ignore_case(name, parameter.name.trim_start_matches('$')))
                && let Some(default) = query::parameter_default(parameter_node)
            {
                parameter.default_value = Some(Box::new(bind_attribute_value(
                    &default,
                    context,
                    0,
                    self.source,
                )));
            }
            let promoted = constructor && parameter.flags.has(Flags::PROMOTED);
            if promoted {
                self.bind_promoted_property(
                    parameter_node,
                    &parameter,
                    parent_scope,
                    container,
                    context,
                );
            }
            self.doc.symbols[symbol_index as usize]
                .parameters
                .push(parameter);
        }
        self.doc.symbols[symbol_index as usize]
            .parameters
            .extend(planned_parameters);
        if let Some(body) = &body {
            self.bind_body(body, function_scope, context, &symbol_id);
        }
    }

    fn bind_promoted_property(
        &mut self,
        node: &SyntaxNode,
        parameter: &Parameter,
        class_scope: ScopeId,
        container: &SymbolId,
        context: &NameContext,
    ) {
        let name = parameter.name.strip_prefix('$').unwrap_or(&parameter.name);
        if self.member_exists(container, SymbolKind::Property, name) {
            return;
        }
        let variable = node.child_node_of_kind(PHP_VARIABLE);
        let fully_qualified = format!("{container}::${name}");
        let (write_visibility, has_write_visibility) = declaration_write_visibility(node);
        let mut symbol = Symbol {
            id: new_symbol_id(
                SymbolKind::Property,
                &fully_qualified,
                &self.path,
                node.text_range().start().into(),
            ),
            kind: SymbolKind::Property,
            name: Str::from(name),
            fully_qualified: Str::from(fully_qualified),
            container: container.clone(),
            path: self.path.clone(),
            range: node.trimmed(),
            selection_range: selection_range(variable.as_ref(), node),
            visibility: declaration_visibility(node),
            write_visibility,
            has_write_visibility,
            flags: parameter.flags | Flags::PROMOTED,
            ty: parameter.ty.clone(),
            native_type: parameter.native_type.clone(),
            doc_type: parameter.doc_type.clone(),
            ..Symbol::default()
        };
        symbol.set_attributes(bind_attributes(node, context, self.source));
        apply_attribute_type_semantics(&mut symbol, context);
        apply_attribute_flags(&mut symbol);
        self.add_symbol(class_scope, symbol);
    }

    pub(crate) fn bind_parameter(
        &mut self,
        node: &SyntaxNode,
        scope: ScopeId,
        context: &NameContext,
        container: &SymbolId,
        documentation: &swlsp_php_phpdoc::Document,
        templates: &[String],
    ) -> Parameter {
        let name = crate::fast::parameter_name(node);
        let variable = node.child_node_of_kind(PHP_VARIABLE);
        let native_type =
            self.bind_native_type(&crate::fast::direct_type_text(node, self.source), context);
        let mut doc_type = documentation
            .params
            .get(&name)
            .map_or_else(Type::unknown, |value| {
                context.resolve_phpdoc_type(value, templates)
            });
        if doc_type.is_unknown() {
            // A promoted property may carry its property PHPDoc directly
            // before the constructor parameter instead of on the
            // constructor itself. Treat an inline @var as the parameter's
            // documented type so both keep that contract.
            let inline = swlsp_php_phpdoc::parse(leading_doc_comment(node, self.source));
            let key = format!("${}", name.strip_prefix('$').unwrap_or(&name));
            let inline_type = inline
                .vars
                .get(&key)
                .filter(|value| !value.is_unknown())
                .or_else(|| inline.vars.get(""))
                .cloned()
                .unwrap_or_default();
            doc_type = context.resolve_phpdoc_type(&inline_type, templates);
        }
        let mut effective = effective_type(&native_type, &doc_type);
        if effective.is_unknown() {
            effective = Type::mixed();
        }
        let mut flags = declaration_flags(node);
        if has_token(node, TK_ELLIPSIS, "") {
            flags |= Flags::VARIADIC;
        }
        let fully_qualified = format!("{container}:{name}");
        let parameter_id = new_symbol_id(
            SymbolKind::Parameter,
            &fully_qualified,
            &self.path,
            node.text_range().start().into(),
        );
        let attributes = bind_attributes(node, context, self.source);
        effective = attribute_refined_type(effective, &attributes, context);
        let mut default_range = TextRange::default();
        let mut default_value = None;
        if let Some(default_node) = query::parameter_default(node) {
            default_range = default_node.trimmed();
            if attribute_named(&attributes, "ReturnTypeContract").is_some() {
                default_value = Some(Box::new(bind_attribute_value(
                    &default_node,
                    context,
                    0,
                    self.source,
                )));
            }
        }
        let range = node.trimmed();
        let selection = selection_range(variable.as_ref(), node);
        let mut symbol = Symbol {
            id: parameter_id.clone(),
            kind: SymbolKind::Parameter,
            name: Str::from(name.as_str()),
            fully_qualified: Str::from(fully_qualified),
            container: container.clone(),
            path: self.path.clone(),
            range,
            selection_range: selection,
            flags,
            ty: effective.clone(),
            native_type: native_type.clone(),
            doc_type: doc_type.clone(),
            ..Symbol::default()
        };
        symbol.set_attributes(attributes.clone());
        apply_attribute_flags(&mut symbol);
        let flags = symbol.flags;
        self.add_symbol(scope, symbol);
        self.mark_declaration(variable.as_ref());
        self.bind_direct_type_references(node, scope, context, container);
        self.bind_attribute_references(node, scope, context);
        self.bind_value_references(node, scope, context, container);
        let identity = variable.as_ref().map(node_identity).unwrap_or_default();
        self.doc.set_type_fact(
            identity,
            TypeFact {
                ty: effective.clone(),
                confidence: Confidence::Declared,
                source: TypeSource::Native,
                origin: range,
                ..TypeFact::default()
            },
        );
        Parameter {
            id: parameter_id,
            name: Str::from(name.as_str()),
            ty: effective,
            native_type,
            doc_type,
            assistant_tags: documentation
                .param_tags
                .get(&name)
                .map(|tags| tags.iter().map(|tag| Str::from(tag.as_str())).collect())
                .unwrap_or_default(),
            attributes,
            default_value,
            range,
            selection_range: selection,
            default_range,
            flags,
            optional: crate::fast::parameter_optional(node),
        }
    }

    fn bind_properties(
        &mut self,
        node: &SyntaxNode,
        scope: ScopeId,
        context: &NameContext,
        container: &SymbolId,
    ) {
        let native_type =
            self.bind_native_type(&crate::fast::direct_type_text(node, self.source), context);
        self.bind_direct_type_references(node, scope, context, container);
        self.bind_attribute_references(node, scope, context);
        self.bind_value_references(node, scope, context, container);
        let documentation = swlsp_php_phpdoc::parse(leading_doc_comment(node, self.source));
        let templates = self.container_template_names(container);
        for variable in node.children().filter(|child| child.kind() == PHP_VARIABLE) {
            let name = crate::fast::variable_name(&variable);
            let doc_type = documentation
                .vars
                .get(&format!("${name}"))
                .filter(|value| !value.is_unknown())
                .or_else(|| documentation.vars.get(""))
                .cloned()
                .unwrap_or_default();
            let doc_type = context.resolve_phpdoc_type(&doc_type, &templates);
            let effective = effective_type(&native_type, &doc_type);
            let fully_qualified = format!("{container}::${name}");
            let (write_visibility, has_write_visibility) = declaration_write_visibility(node);
            let range = node.trimmed();
            let mut symbol = Symbol {
                id: new_symbol_id(
                    SymbolKind::Property,
                    &fully_qualified,
                    &self.path,
                    variable.text_range().start().into(),
                ),
                kind: SymbolKind::Property,
                name: Str::from(name.as_str()),
                fully_qualified: Str::from(fully_qualified),
                container: container.clone(),
                path: self.path.clone(),
                range,
                selection_range: variable.trimmed(),
                visibility: declaration_visibility(node),
                write_visibility,
                has_write_visibility,
                flags: declaration_flags(node),
                ty: effective.clone(),
                native_type: native_type.clone(),
                doc_type,
                ..Symbol::default()
            };
            symbol.set_attributes(bind_attributes(node, context, self.source));
            apply_document_flags(&mut symbol, &documentation);
            apply_attribute_type_semantics(&mut symbol, context);
            apply_attribute_flags(&mut symbol);
            self.add_symbol(scope, symbol);
            self.mark_declaration(Some(&variable));
            self.doc.set_type_fact(
                node_identity(&variable),
                TypeFact {
                    ty: effective,
                    confidence: Confidence::Declared,
                    source: TypeSource::Native,
                    origin: range,
                    ..TypeFact::default()
                },
            );
        }
    }

    pub(crate) fn bind_constants(
        &mut self,
        node: &SyntaxNode,
        scope: ScopeId,
        context: &NameContext,
        container: &SymbolId,
        class_constant: bool,
    ) {
        let native_type = match node.child_node_of_kind(PHP_TYPE) {
            Some(type_node) => self.bind_native_type(self.text(&type_node), context),
            None => Type::unknown(),
        };
        self.bind_direct_type_references(node, scope, context, container);
        self.bind_attribute_references(node, scope, context);
        self.bind_value_references(node, scope, context, container);
        let documentation = swlsp_php_phpdoc::parse(leading_doc_comment(node, self.source));
        let attributes = bind_attributes(node, context, self.source);
        let mut expect_name = false;
        let mut expect_value = false;
        let mut current_symbol: Option<usize> = None;
        for element in node.children_with_tokens() {
            match element {
                NodeOrToken::Token(token) => {
                    if eq_ignore_case(token.text(), "const") || token.kind() == TK_COMMA {
                        expect_name = true;
                        expect_value = false;
                        current_symbol = None;
                    } else if token.kind() == TK_EQUALS {
                        expect_name = false;
                        expect_value = true;
                    }
                }
                NodeOrToken::Node(value) => {
                    if expect_value && let Some(current) = current_symbol {
                        let items = constant_array_items(&value, self.source);
                        let inferred = if native_type.is_unknown() {
                            constant_literal_type(Some(&value), self.source)
                        } else {
                            Type::unknown()
                        };
                        let symbol = &mut self.doc.symbols[current];
                        symbol.set_constant_array(items);
                        if !inferred.is_unknown() {
                            symbol.ty = inferred;
                        }
                        expect_value = false;
                        continue;
                    }
                    if !expect_name || value.kind() != PHP_NAME {
                        continue;
                    }
                    let name = crate::fast::name_value(&value, self.source);
                    let (kind, fully_qualified) = if class_constant {
                        (SymbolKind::ClassConstant, format!("{container}::{name}"))
                    } else {
                        (
                            SymbolKind::GlobalConstant,
                            qualify(&context.namespace, &name),
                        )
                    };
                    let mut symbol = Symbol {
                        id: new_symbol_id(
                            kind,
                            &fully_qualified,
                            &self.path,
                            value.text_range().start().into(),
                        ),
                        kind,
                        name: Str::from(name.as_str()),
                        fully_qualified: Str::from(fully_qualified),
                        container: container.clone(),
                        path: self.path.clone(),
                        range: node.trimmed(),
                        selection_range: value.trimmed(),
                        visibility: declaration_visibility(node),
                        flags: declaration_flags(node),
                        ty: native_type.clone(),
                        native_type: native_type.clone(),
                        ..Symbol::default()
                    };
                    symbol.set_metadata(
                        attributes.clone(),
                        Vec::new(),
                        documentation.summary.as_str(),
                    );
                    apply_document_flags(&mut symbol, &documentation);
                    apply_attribute_type_semantics(&mut symbol, context);
                    apply_attribute_flags(&mut symbol);
                    current_symbol = Some(self.doc.symbols.len());
                    self.add_symbol(scope, symbol);
                    self.mark_declaration(Some(&value));
                    expect_name = false;
                }
            }
        }
    }

    fn bind_enum_case(
        &mut self,
        node: &SyntaxNode,
        scope: ScopeId,
        context: &NameContext,
        container: &SymbolId,
    ) {
        let name_node = node.child_node_of_kind(PHP_NAME);
        let name = crate::fast::name_of(name_node.as_ref(), self.source);
        if name.is_empty() {
            return;
        }
        let fully_qualified = format!("{container}::{name}");
        let mut symbol = Symbol {
            id: new_symbol_id(
                SymbolKind::EnumCase,
                &fully_qualified,
                &self.path,
                node.text_range().start().into(),
            ),
            kind: SymbolKind::EnumCase,
            name: Str::from(name.as_str()),
            fully_qualified: Str::from(fully_qualified),
            container: container.clone(),
            path: self.path.clone(),
            range: node.trimmed(),
            selection_range: selection_range(name_node.as_ref(), node),
            ..Symbol::default()
        };
        symbol.set_attributes(bind_attributes(node, context, self.source));
        apply_document_flags(
            &mut symbol,
            &swlsp_php_phpdoc::parse(leading_doc_comment(node, self.source)),
        );
        apply_attribute_type_semantics(&mut symbol, context);
        apply_attribute_flags(&mut symbol);
        self.add_symbol(scope, symbol);
        self.mark_declaration(name_node.as_ref());
        self.bind_attribute_references(node, scope, context);
        self.bind_value_references(node, scope, context, container);
    }

    pub(crate) fn bind_native_type(&self, source: &str, context: &NameContext) -> Type {
        let source = source.trim();
        if source.is_empty() {
            return Type::unknown();
        }
        match swlsp_php_types::parse_native(source) {
            Ok(value) => resolve_native_type(&value, source, context),
            Err(_) => Type::error(),
        }
    }

    /// Appends `symbol`, records it in `scope`, and returns its index.
    pub(crate) fn add_symbol(&mut self, scope: ScopeId, symbol: Symbol) -> u32 {
        let index = self.doc.symbols.len() as u32;
        self.symbol_indexes
            .entry(symbol.id.clone())
            .or_insert(index);
        self.doc.symbols.push(symbol);
        self.doc.scopes[scope as usize].add_symbol(index);
        index
    }

    /// The first symbol with `id`.
    pub(crate) fn symbol(&self, id: &SymbolId) -> Option<&Symbol> {
        self.symbol_index(id)
            .map(|index| &self.doc.symbols[index as usize])
    }

    pub(crate) fn symbol_index(&self, id: &SymbolId) -> Option<u32> {
        if id.is_empty() {
            // The binder never creates symbols with empty IDs.
            return None;
        }
        self.symbol_indexes.get(id.as_str()).copied()
    }

    pub(crate) fn new_scope(
        &mut self,
        kind: ScopeKind,
        parent: ScopeId,
        range: TextRange,
        owner: SymbolId,
        context: &NameContext,
    ) -> ScopeId {
        let id = self.doc.scopes.len() as ScopeId;
        self.doc.scopes.push(Scope {
            id,
            kind,
            parent,
            range,
            owner,
            namespace: context.namespace.clone(),
            imports: ImportTable::default(),
            symbols: Vec::new(),
        });
        self.scope_imports.push(self.current_imports);
        id
    }

    pub(crate) fn mark_declaration(&mut self, node: Option<&SyntaxNode>) {
        if let Some(node) = node {
            self.declarations.insert(node_identity(node));
        }
    }

    pub(crate) fn is_declaration(&self, node: &SyntaxNode) -> bool {
        self.declarations.contains(&node_identity(node))
    }
}

pub(crate) fn constant_literal_type(node: Option<&SyntaxNode>, source: &str) -> Type {
    let Some(node) = node else {
        return Type::unknown();
    };
    if let Some(value) = swlsp_php_literal::type_of(node) {
        return value;
    }
    if node.kind() == PHP_UNARY_EXPRESSION {
        let text = text(source, node).trim().replace('_', "");
        let bytes = text.as_bytes();
        if bytes.len() > 1 && (bytes[0] == b'+' || bytes[0] == b'-') {
            if numeric_literal_is_float(&text[1..]) {
                return Type::literal_float(&text);
            }
            return Type::literal_int(&text);
        }
    }
    Type::unknown()
}

fn constant_array_items(node: &SyntaxNode, source: &str) -> Vec<ConstantArrayItem> {
    if node.kind() != PHP_ARRAY {
        return Vec::new();
    }
    let mut result = Vec::new();
    for item in node
        .children()
        .filter(|child| child.kind() == PHP_ARRAY_ITEM)
    {
        let key = query::array_item_key(&item);
        let value = query::array_item_value(&item);
        let (Some(key), Some(value)) = (key, value) else {
            continue;
        };
        if key.kind() != PHP_STRING {
            continue;
        }
        let name = query::string_value(&key);
        if name.is_empty() {
            continue;
        }
        result.push(ConstantArrayItem {
            key: Str::from(name),
            key_range: key.trimmed(),
            value: Str::from(text(source, &value).trim()),
            value_range: value.trimmed(),
            ty: constant_array_value_type(&value, source),
        });
    }
    result
}

fn constant_array_value_type(node: &SyntaxNode, source: &str) -> Type {
    match node.kind() {
        PHP_ARRAY => Type::array(Type::mixed(), Type::mixed()),
        PHP_STRING => Type::string(),
        PHP_NUMBER => {
            if numeric_literal_is_float(text(source, node)) {
                Type::float()
            } else {
                Type::int()
            }
        }
        PHP_BOOLEAN => Type::bool(),
        PHP_NULL => Type::null(),
        PHP_UNARY_EXPRESSION if !constant_literal_type(Some(node), source).is_unknown() => {
            if numeric_literal_is_float(text(source, node)) {
                Type::float()
            } else {
                Type::int()
            }
        }
        _ => Type::unknown(),
    }
}

pub(crate) fn numeric_literal_is_float(value: &str) -> bool {
    let value = value.trim().replace('_', "");
    let lower = lowercase(value.trim_start_matches(['+', '-']));
    if lower.starts_with("0x") || lower.starts_with("0b") || lower.starts_with("0o") {
        return false;
    }
    lower.contains(['.', 'e'])
}

fn return_metadata(
    body: &SyntaxNode,
    context: &NameContext,
    source: &str,
) -> (Vec<LiteralReturn>, Vec<ConstantReturn>) {
    let mut literal_result = Vec::new();
    let mut constant_result = Vec::new();
    let mut ranges = Vec::new();
    crate::fast::direct_return_ranges(body, &mut ranges);
    for range in ranges {
        let Some(statement) = crate::fast::node_with_range(body, range, PHP_RETURN_STATEMENT)
        else {
            continue;
        };
        let statement = &statement;
        let value = statement.children().next();
        let literal_type = constant_literal_type(value.as_ref(), source);
        if let Some(value) = &value
            && !literal_type.is_unknown()
        {
            let literal_value = if value.kind() == PHP_STRING {
                query::string_value(value)
            } else {
                text(source, value).trim().to_owned()
            };
            literal_result.push(LiteralReturn {
                value: Str::from(literal_value),
                range: value.trimmed(),
                ty: literal_type,
            });
        }
        let Some(value) = value else {
            continue;
        };
        if value.kind() != PHP_SCOPED_ACCESS && value.kind() != PHP_MEMBER_ACCESS {
            continue;
        }
        let text = text(source, &value).trim();
        let Some(separator) = text.rfind("::") else {
            continue;
        };
        if separator == 0 || separator + 2 >= text.len() {
            continue;
        }
        let receiver = text[..separator].trim();
        let name = text[separator + 2..].trim();
        if receiver.is_empty() || name.is_empty() || eq_ignore_case(name, "class") {
            continue;
        }
        let lower = lowercase(receiver);
        let receiver = match lower.as_str() {
            "self" | "static" | "parent" => lower,
            _ => context.resolve_class(receiver),
        };
        constant_result.push(ConstantReturn {
            receiver: Str::from(receiver),
            name: Str::from(name),
            range: value.trimmed(),
        });
    }
    (literal_result, constant_result)
}

fn resolve_native_type(value: &Type, source: &str, context: &NameContext) -> Type {
    match value.kind() {
        TypeKind::Object => {
            if native_type_is_fully_qualified(source, value.name()) {
                let name = value.name();
                return Type::named(
                    name.strip_prefix('\\').unwrap_or(name),
                    value.arguments().to_vec(),
                );
            }
            context.resolve_type(value)
        }
        TypeKind::Union => Type::union(
            value
                .arguments()
                .iter()
                .map(|member| resolve_native_type(member, source, context))
                .collect::<Vec<_>>(),
        ),
        TypeKind::Intersection => Type::intersection(
            value
                .arguments()
                .iter()
                .map(|member| resolve_native_type(member, source, context))
                .collect::<Vec<_>>(),
        ),
        _ => context.resolve_type(value),
    }
}

fn native_type_is_fully_qualified(source: &str, name: &str) -> bool {
    if source.is_empty() || name.is_empty() {
        return false;
    }
    let needle = format!("\\{}", name.strip_prefix('\\').unwrap_or(name));
    let bytes = source.as_bytes();
    let mut start = 0;
    while start < source.len() {
        let Some(index) = source[start..].find(&needle) else {
            return false;
        };
        let index = index + start;
        if index == 0 || !is_php_type_name_byte(bytes[index - 1]) {
            return true;
        }
        start = index + needle.len();
    }
    false
}

fn is_php_type_name_byte(value: u8) -> bool {
    value == b'\\' || value == b'_' || value.is_ascii_alphanumeric() || value >= 0x80
}

pub(crate) fn selection_range(node: Option<&SyntaxNode>, fallback: &SyntaxNode) -> TextRange {
    match node {
        Some(node) => node.trimmed(),
        None => fallback.trimmed(),
    }
}

pub(crate) fn qualify(namespace: &str, name: &str) -> String {
    let namespace = namespace.trim_matches('\\');
    let name = name.trim_matches('\\');
    if namespace.is_empty() {
        return name.to_owned();
    }
    format!("{namespace}\\{name}")
}

pub(crate) fn resolve_names(context: &NameContext, names: Vec<String>) -> Vec<Str> {
    names
        .iter()
        .map(|name| Str::from(context.resolve_class(name)))
        .collect()
}
