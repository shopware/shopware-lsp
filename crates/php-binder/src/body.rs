//! Function bodies: locals, closures, nested declarations, and references.

use crate::fast::Fast;
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_resolver::NameContext;
use swlsp_php_semantic::{
    Confidence, Flags, NameKind, Reference, ScopeId, ScopeKind, Str, Symbol, SymbolId, SymbolKind,
    TypeFact, TypeSource, anonymous_class_name, new_symbol_id, node_identity,
};
use swlsp_php_types::Type;
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt, TextRange};
use swlsp_textutil::eq_ignore_case;

use crate::builder::{Builder, effective_type, resolve_names};
use crate::declaration_references::is_type_node;
use crate::modifiers::{declaration_flags, has_token};
use crate::phpdoc::{bind_assertions, bind_templates, leading_doc_comment, template_names};

impl Builder<'_> {
    /// Declarations are bound in one preorder walk that records the
    /// visited nodes; references then replay that order instead of walking
    /// (and re-materializing) the red tree a second time.
    pub(crate) fn bind_body(
        &mut self,
        body: &SyntaxNode,
        function_scope: ScopeId,
        context: &NameContext,
        owner: &SymbolId,
    ) {
        let mut order = self.order_pool.pop().unwrap_or_default();
        self.bind_body_declarations(body, function_scope, context, owner, &mut order);
        for node in &order {
            self.bind_body_reference(node, function_scope, context, owner);
        }
        order.clear();
        self.order_pool.push(order);
    }

    pub(crate) fn bind_body_node(
        &mut self,
        node: &SyntaxNode,
        function_scope: ScopeId,
        context: &NameContext,
        owner: &SymbolId,
    ) {
        let mut order = self.order_pool.pop().unwrap_or_default();
        self.bind_body_declaration(node, function_scope, context, owner);
        if !nested_function(node.kind()) {
            self.bind_body_declarations(node, function_scope, context, owner, &mut order);
        }
        self.bind_body_reference(node, function_scope, context, owner);
        for child in &order {
            self.bind_body_reference(child, function_scope, context, owner);
        }
        order.clear();
        self.order_pool.push(order);
    }

    fn bind_body_declarations(
        &mut self,
        parent: &SyntaxNode,
        function_scope: ScopeId,
        context: &NameContext,
        owner: &SymbolId,
        order: &mut Vec<SyntaxNode>,
    ) {
        for node in parent.children() {
            self.bind_body_declaration(&node, function_scope, context, owner);
            let nested = nested_function(node.kind());
            order.push(node.clone());
            if !nested {
                self.bind_body_declarations(&node, function_scope, context, owner, order);
            }
        }
    }

    fn bind_body_declaration(
        &mut self,
        node: &SyntaxNode,
        function_scope: ScopeId,
        context: &NameContext,
        owner: &SymbolId,
    ) {
        match node.kind() {
            PHP_CLASS_DECLARATION
            | PHP_INTERFACE_DECLARATION
            | PHP_TRAIT_DECLARATION
            | PHP_ENUM_DECLARATION => {
                self.bind_class(node, function_scope, context);
            }
            PHP_ANONYMOUS_CLASS => self.bind_anonymous_class(node, function_scope, context),
            PHP_ASSIGNMENT_EXPRESSION => {
                let Some(left) = node.children().next() else {
                    return;
                };
                match left.kind() {
                    PHP_VARIABLE => self.bind_local(&left, function_scope, owner, Type::unknown()),
                    PHP_ARRAY_ACCESS => {
                        if let Some(base) = left.children().next()
                            && base.kind() == PHP_VARIABLE
                        {
                            self.bind_local(&base, function_scope, owner, Type::unknown());
                        }
                    }
                    PHP_ARRAY => {
                        let variables = left
                            .descendants()
                            .filter(|variable| variable.kind() == PHP_VARIABLE)
                            .collect::<Vec<_>>();
                        for variable in &variables {
                            self.bind_local(variable, function_scope, owner, Type::unknown());
                        }
                    }
                    _ => {}
                }
            }
            PHP_FOREACH_STATEMENT => self.bind_foreach_locals(node, function_scope, owner),
            PHP_CATCH_CLAUSE => self.bind_catch_local(node, function_scope, context, owner),
            PHP_GLOBAL_STATEMENT | PHP_STATIC_STATEMENT => {
                for variable in node.children().filter(|child| child.kind() == PHP_VARIABLE) {
                    self.bind_local(&variable, function_scope, owner, Type::unknown());
                }
            }
            PHP_CLOSURE | PHP_ARROW_FUNCTION => {
                self.bind_closure(node, function_scope, context, owner)
            }
            PHP_FUNCTION_DECLARATION => {
                self.bind_function(node, function_scope, context, &SymbolId::default());
            }
            PHP_FUNCTION_CALL => self.bind_class_alias(node, function_scope, context),
            _ => {}
        }
    }

    fn bind_class_alias(&mut self, call: &SyntaxNode, scope: ScopeId, context: &NameContext) {
        let call_name = query::call_name(call);
        if !eq_ignore_case(
            call_name.strip_prefix('\\').unwrap_or(&call_name),
            "class_alias",
        ) {
            return;
        }
        let target_node = query::argument_expression(call, 0);
        let alias_node = query::argument_expression(call, 1);
        let target = static_class_alias_name(target_node.as_ref(), context, self.source);
        let alias = static_class_alias_name(alias_node.as_ref(), context, self.source);
        let (Some((target, target_range, target_literal)), Some((alias, alias_range, _))) =
            (target, alias)
        else {
            return;
        };
        if eq_ignore_case(&target, &alias) {
            return;
        }
        let alias_name = alias
            .rsplit_once('\\')
            .map_or(alias.as_str(), |(_, last)| last);
        let mut symbol = Symbol {
            id: new_symbol_id(
                SymbolKind::Class,
                &alias,
                &self.path,
                call.text_range().start().into(),
            ),
            kind: SymbolKind::Class,
            name: Str::from(alias_name),
            fully_qualified: Str::from(alias.as_str()),
            path: self.path.clone(),
            range: call.trimmed(),
            selection_range: alias_range,
            flags: Flags::SYNTHETIC | Flags::CLASS_ALIAS,
            ..Symbol::default()
        };
        symbol.set_extends(vec![Str::from(target.as_str())]);
        self.add_symbol(scope, symbol);
        if target_literal {
            let mut reference = Reference {
                name: Str::from(target.as_str()),
                kind: NameKind::Class,
                range: target_range,
                scope,
                ..Reference::default()
            };
            reference.set_qualified_name(target);
            self.doc.references.push(reference);
        }
    }

    fn bind_body_reference(
        &mut self,
        node: &SyntaxNode,
        function_scope: ScopeId,
        context: &NameContext,
        owner: &SymbolId,
    ) {
        match node.kind() {
            PHP_VARIABLE => {
                if self.is_declaration(node) || static_property_name(node) {
                    return;
                }
                self.push_variable_reference(
                    crate::fast::variable_key(node),
                    node.trimmed(),
                    function_scope,
                );
            }
            PHP_OBJECT_CREATION => {
                if self.is_declaration(node) {
                    return;
                }
                if let Some(name_node) = node.child_node_of_kind(PHP_NAME) {
                    let name = crate::fast::name_value(&name_node, self.source);
                    if name.starts_with('$') {
                        self.push_variable_reference(name, name_node.trimmed(), function_scope);
                    } else {
                        self.add_resolved_class_reference(
                            &name_node,
                            function_scope,
                            context,
                            owner,
                        );
                    }
                }
            }
            PHP_FUNCTION_CALL => {
                if self.is_declaration(node) {
                    return;
                }
                if let Some(name_node) = node.child_node_of_kind(PHP_NAME) {
                    let qualified =
                        context.resolve_function(&crate::fast::name_value(&name_node, self.source));
                    self.add_reference(&name_node, NameKind::Function, function_scope, qualified);
                }
            }
            PHP_SCOPED_CALL | PHP_SCOPED_ACCESS => {
                if self.is_declaration(node) {
                    return;
                }
                if let Some(receiver) = node.children().next()
                    && receiver.kind() == PHP_NAME
                {
                    self.add_resolved_class_reference(&receiver, function_scope, context, owner);
                }
            }
            PHP_MEMBER_ACCESS => {
                if self.is_declaration(node) {
                    return;
                }
                if has_token(node, TK_SCOPE_RESOLUTION, "")
                    && let Some(receiver) = node.children().next()
                    && receiver.kind() == PHP_NAME
                {
                    self.add_resolved_class_reference(&receiver, function_scope, context, owner);
                }
            }
            PHP_BINARY_EXPRESSION => {
                if self.is_declaration(node) {
                    return;
                }
                self.bind_instanceof_reference(node, function_scope, context, owner);
            }
            _ => {}
        }
    }

    fn push_variable_reference(&mut self, name: String, range: TextRange, scope: ScopeId) {
        self.doc.references.push(Reference {
            name: Str::from(name),
            kind: NameKind::Variable,
            range,
            scope,
            ..Reference::default()
        });
    }

    fn bind_anonymous_class(
        &mut self,
        node: &SyntaxNode,
        parent_scope: ScopeId,
        context: &NameContext,
    ) {
        let fully_qualified = anonymous_class_name(&self.path, node.trimmed().start().into());
        let mut symbol = Symbol {
            id: new_symbol_id(
                SymbolKind::Class,
                &fully_qualified,
                &self.path,
                node.text_range().start().into(),
            ),
            kind: SymbolKind::Class,
            name: Str::from("{anonymous}"),
            fully_qualified: Str::from(fully_qualified),
            path: self.path.clone(),
            range: node.trimmed(),
            selection_range: node.trimmed(),
            flags: declaration_flags(node) | Flags::SYNTHETIC,
            ..Symbol::default()
        };
        symbol.set_hierarchy(
            resolve_names(context, query::class_extends(node)),
            resolve_names(context, query::class_implements(node)),
            Vec::new(),
            Vec::new(),
            Vec::new(),
            Vec::new(),
            Vec::new(),
        );
        let body = node.child_node_of_kind(PHP_CLASS_BODY);
        if let Some(body) = &body {
            symbol.body_range = body.trimmed();
        }
        let symbol_id = symbol.id.clone();
        let symbol_index = self.add_symbol(parent_scope, symbol);
        self.bind_class_clauses(node, parent_scope, context);
        self.bind_attribute_references(node, parent_scope, context);

        // Constructor arguments are evaluated in the enclosing scope, not the
        // anonymous class scope.
        if let Some(arguments) = node.child_node_of_kind(PHP_ARGUMENT_LIST) {
            let owner = self.doc.scopes[parent_scope as usize].owner.clone();
            self.bind_body(&arguments, parent_scope, context, &owner);
        }
        let Some(body) = body else {
            return;
        };
        let class_scope = self.new_scope(
            ScopeKind::Class,
            parent_scope,
            body.text_range(),
            symbol_id.clone(),
            context,
        );
        self.bind_class_members(&body, class_scope, context, &symbol_id, symbol_index, false);
    }

    fn bind_local(&mut self, node: &SyntaxNode, scope: ScopeId, owner: &SymbolId, value: Type) {
        if node.kind() != PHP_VARIABLE {
            return;
        }
        let name = crate::fast::variable_key(node);
        if name.is_empty() || self.scope_has(scope, &name) {
            return;
        }
        let range = node.trimmed();
        let symbol = Symbol {
            id: new_symbol_id(
                SymbolKind::Local,
                "",
                &self.path,
                node.text_range().start().into(),
            ),
            kind: SymbolKind::Local,
            fully_qualified: Str::from(format!("{owner}:{name}")),
            name: Str::from(name),
            container: owner.clone(),
            path: self.path.clone(),
            range,
            selection_range: range,
            ty: value.clone(),
            ..Symbol::default()
        };
        self.add_symbol(scope, symbol);
        self.mark_declaration(Some(node));
        if !value.is_unknown() {
            self.doc.set_type_fact(
                node_identity(node),
                TypeFact {
                    ty: value,
                    confidence: Confidence::Inferred,
                    source: TypeSource::Assignment,
                    origin: range,
                    ..TypeFact::default()
                },
            );
        }
    }

    fn bind_foreach_locals(&mut self, node: &SyntaxNode, scope: ScopeId, owner: &SymbolId) {
        let mut in_targets = false;
        for child in node.children_with_tokens() {
            match child {
                NodeOrToken::Token(token) => {
                    if eq_ignore_case(token.text(), "as") {
                        in_targets = true;
                    } else if in_targets && token.kind() == TK_CLOSE_PAREN {
                        return;
                    }
                }
                NodeOrToken::Node(child) => {
                    if in_targets {
                        self.bind_foreach_target_locals(&child, scope, owner);
                    }
                }
            }
        }
    }

    fn bind_foreach_target_locals(&mut self, node: &SyntaxNode, scope: ScopeId, owner: &SymbolId) {
        if node.kind() == PHP_VARIABLE {
            self.bind_local(node, scope, owner, Type::unknown());
            return;
        }
        for child in node.children() {
            self.bind_foreach_target_locals(&child, scope, owner);
        }
    }

    fn bind_catch_local(
        &mut self,
        node: &SyntaxNode,
        scope: ScopeId,
        context: &NameContext,
        owner: &SymbolId,
    ) {
        self.bind_direct_type_references(node, scope, context, owner);
        let caught = match node.children().find(|child| is_type_node(child.kind())) {
            Some(type_node) => self.bind_native_type(self.text(&type_node), context),
            None => Type::unknown(),
        };
        if let Some(variable) = node.children().find(|child| child.kind() == PHP_VARIABLE) {
            self.bind_local(&variable, scope, owner, caught);
        }
    }

    fn bind_closure(
        &mut self,
        node: &SyntaxNode,
        parent_scope: ScopeId,
        context: &NameContext,
        owner: &SymbolId,
    ) {
        let documentation = swlsp_php_phpdoc::parse(leading_doc_comment(node, self.source));
        let native_return =
            self.bind_native_type(&crate::fast::direct_type_text(node, self.source), context);
        let inherited_templates = self.container_template_names(owner);
        let mut doc_templates = inherited_templates.clone();
        doc_templates.extend(template_names(&documentation.templates));
        let doc_return = context.resolve_phpdoc_type(&documentation.return_type, &doc_templates);
        let parameters: Vec<SyntaxNode> = query::parameters(node).collect();
        let start: u32 = node.text_range().start().into();
        let id = new_symbol_id(SymbolKind::Closure, "", &self.path, start);
        let trimmed = node.trimmed();
        let mut symbol = Symbol {
            id: id.clone(),
            kind: SymbolKind::Closure,
            name: Str::from("{closure}"),
            fully_qualified: Str::from(format!("{owner}:{{closure}}@{start}")),
            container: owner.clone(),
            path: self.path.clone(),
            range: trimmed,
            selection_range: TextRange::new(trimmed.start(), trimmed.start()),
            flags: declaration_flags(node),
            return_type: effective_type(&native_return, &doc_return),
            native_type: native_return,
            doc_type: doc_return,
            ..Symbol::default()
        };
        symbol.set_doc_summary(documentation.summary.as_str());
        symbol.parameters = Vec::with_capacity(parameters.len());
        symbol.set_signature_extras(
            bind_templates(&documentation.templates, context, &inherited_templates),
            Vec::new(),
            bind_assertions(&documentation.assertions, context, &doc_templates),
            Vec::new(),
            Vec::new(),
        );
        let symbol_index = self.add_symbol(parent_scope, symbol);

        let scope_parent = if node.kind() == PHP_CLOSURE {
            self.non_function_parent(parent_scope)
        } else {
            parent_scope
        };
        let closure_scope = self.new_scope(
            ScopeKind::Closure,
            scope_parent,
            node.text_range(),
            id.clone(),
            context,
        );
        for parameter_node in &parameters {
            let parameter = self.bind_parameter(
                parameter_node,
                closure_scope,
                context,
                &id,
                &documentation,
                &doc_templates,
            );
            self.doc.symbols[symbol_index as usize]
                .parameters
                .push(parameter);
        }
        self.bind_direct_type_references(node, closure_scope, context, owner);

        if node.kind() == PHP_CLOSURE {
            for child in node.children().filter(|child| child.kind() == PHP_VARIABLE) {
                let name = crate::fast::variable_key(&child);
                self.doc.references.push(Reference {
                    name: Str::from(name.as_str()),
                    kind: NameKind::Variable,
                    range: child.trimmed(),
                    scope: parent_scope,
                    ..Reference::default()
                });
                let value = self.local_type(parent_scope, &name);
                self.bind_closure_capture(&child, closure_scope, &id, value);
            }
        }

        if let Some(block) = node.child_node_of_kind(PHP_BLOCK) {
            self.bind_body(&block, closure_scope, context, &id);
            return;
        }
        let last = node
            .children()
            .filter(|child| child.kind() != PHP_PARAMETER_LIST && !is_type_node(child.kind()))
            .last();
        if let Some(child) = last {
            self.bind_body(&child, closure_scope, context, &id);
        }
    }

    fn bind_closure_capture(
        &mut self,
        node: &SyntaxNode,
        scope: ScopeId,
        owner: &SymbolId,
        value: Type,
    ) {
        let name = crate::fast::variable_key(node);
        let range = node.trimmed();
        let symbol = Symbol {
            id: new_symbol_id(
                SymbolKind::Local,
                "",
                &self.path,
                node.text_range().start().into(),
            ),
            kind: SymbolKind::Local,
            fully_qualified: Str::from(format!("{owner}:{name}")),
            name: Str::from(name),
            container: owner.clone(),
            path: self.path.clone(),
            range,
            selection_range: range,
            ty: value,
            ..Symbol::default()
        };
        self.add_symbol(scope, symbol);
        self.mark_declaration(Some(node));
    }

    fn bind_instanceof_reference(
        &mut self,
        node: &SyntaxNode,
        scope: ScopeId,
        context: &NameContext,
        owner: &SymbolId,
    ) {
        if !has_direct_text(node, "instanceof") {
            return;
        }
        if let Some(name_node) = node.children().last()
            && name_node.kind() == PHP_NAME
        {
            self.add_resolved_class_reference(&name_node, scope, context, owner);
        }
    }

    fn non_function_parent(&self, mut scope: ScopeId) -> ScopeId {
        while let Some(current) = self.doc.scopes.get(scope as usize) {
            if current.kind != ScopeKind::Function && current.kind != ScopeKind::Closure {
                return scope;
            }
            if current.id == current.parent {
                return scope;
            }
            scope = current.parent;
        }
        0
    }

    fn local_type(&self, mut scope: ScopeId, name: &str) -> Type {
        while let Some(current) = self.doc.scopes.get(scope as usize) {
            // Every scope entry has a symbol, so the first match decides.
            if let Some(id) = current.symbol_ids(&self.doc.symbols, name).next()
                && let Some(symbol) = self.symbol(id)
            {
                return symbol.ty.clone();
            }
            if current.id == current.parent {
                break;
            }
            scope = current.parent;
        }
        Type::unknown()
    }

    fn scope_has(&self, mut scope: ScopeId, name: &str) -> bool {
        while let Some(current) = self.doc.scopes.get(scope as usize) {
            if current.has_symbol(&self.doc.symbols, name) {
                return true;
            }
            if current.id == current.parent {
                return false;
            }
            scope = current.parent;
        }
        false
    }
}

fn static_class_alias_name(
    node: Option<&SyntaxNode>,
    context: &NameContext,
    source: &str,
) -> Option<(String, TextRange, bool)> {
    let node = node?;
    let name = query::class_constant_name(node);
    if !name.is_empty() {
        let range = node
            .child_node_of_kind(PHP_NAME)
            .map_or_else(|| node.trimmed(), |receiver| receiver.trimmed());
        return Some((
            context.resolve_class(&name).trim_matches('\\').to_owned(),
            range,
            false,
        ));
    }
    if node.kind() != PHP_STRING || crate::modifiers::text(source, node).contains('$') {
        return None;
    }
    let value = query::string_value(node);
    let value = replace_class_alias_escapes(&value);
    let value = value.trim().trim_matches('\\');
    if value.is_empty() {
        return None;
    }
    Some((value.to_owned(), query::string_content_range(node), true))
}

/// Unescapes `\\`, `\'` and `\"` in a single left-to-right pass; other
/// backslashes stay.
fn replace_class_alias_escapes(value: &str) -> String {
    let mut result = String::with_capacity(value.len());
    let mut rest = value;
    while let Some(index) = rest.find('\\') {
        result.push_str(&rest[..index]);
        let after = &rest[index + 1..];
        match after.as_bytes().first() {
            Some(b'\\') | Some(b'\'') | Some(b'"') => {
                result.push_str(&after[..1]);
                rest = &after[1..];
            }
            _ => {
                result.push('\\');
                rest = after;
            }
        }
    }
    result.push_str(rest);
    result
}

fn static_property_name(node: &SyntaxNode) -> bool {
    let Some(mut parent) = node.parent() else {
        return false;
    };
    let mut name_node = node.clone();
    if parent.kind() == PHP_NAME
        && let Some(grandparent) = parent.parent()
    {
        name_node = parent;
        parent = grandparent;
    }
    if parent.kind() != PHP_SCOPED_ACCESS
        && (parent.kind() != PHP_MEMBER_ACCESS || !has_token(&parent, TK_SCOPE_RESOLUTION, ""))
    {
        return false;
    }
    let mut last = None;
    let mut count = 0;
    for child in parent.children() {
        last = Some(child);
        count += 1;
    }
    count >= 2 && last.is_some_and(|last| last == name_node)
}

fn nested_function(kind: Kind) -> bool {
    matches!(
        kind,
        PHP_CLOSURE
            | PHP_ARROW_FUNCTION
            | PHP_FUNCTION_DECLARATION
            | PHP_CLASS_DECLARATION
            | PHP_ANONYMOUS_CLASS
            | PHP_INTERFACE_DECLARATION
            | PHP_TRAIT_DECLARATION
            | PHP_ENUM_DECLARATION
    )
}

fn has_direct_text(node: &SyntaxNode, text: &str) -> bool {
    node.children_with_tokens().any(|child| match child {
        NodeOrToken::Token(token) => eq_ignore_case(token.text(), text),
        NodeOrToken::Node(_) => false,
    })
}
