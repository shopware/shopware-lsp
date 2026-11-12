//! Type-aware Shopware checks over one shared linked semantic document and one
//! CST traversal.

use std::collections::{HashMap, HashSet, VecDeque};
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::*;
use swlsp_php::PhpIndex;
use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{
    Attribute, Document, Flags, NameKind, Reference, Snapshot, Symbol, SymbolId, SymbolKind,
};
use swlsp_php_types::{Kind as TypeKind, Type};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};
use swlsp_textutil::{eq_ignore_case, lowercase};

use super::util::{equal_php_name, range_contains, ranges_touch, trim_php_name, warning};
use crate::diagnostics::common::{
    call_target_name, local_call_argument, php_type_is_subtype, unwrap_php_parentheses,
};
use crate::diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};

pub const SHOPWARE_PHP_REPOSITORY_IN_LOOP_CODE: &str = "shopware.php.repository.in_loop";
pub const SHOPWARE_PHP_INTERNAL_CLASS_EXTENSION_CODE: &str =
    "shopware.php.internal.class_extension";
pub const SHOPWARE_PHP_INTERNAL_FUNCTION_CALL_CODE: &str = "shopware.php.internal.function_call";
pub const SHOPWARE_PHP_INTERNAL_METHOD_CALL_CODE: &str = "shopware.php.internal.method_call";
pub const SHOPWARE_PHP_SESSION_CONSTRUCTOR_CODE: &str = "shopware.php.session.constructor";
pub const SHOPWARE_PHP_SESSION_PAYMENT_HANDLER_CODE: &str = "shopware.php.session.payment_handler";
pub const SHOPWARE_PHP_SESSION_STORE_API_CODE: &str = "shopware.php.session.store_api";
pub const SHOPWARE_PHP_SCHEDULED_TASK_INTERVAL_CODE: &str = "shopware.php.scheduled_task.interval";
pub const SHOPWARE_PHP_USER_STORE_TOKEN_CODE: &str = "shopware.php.user.store_token";
pub const SHOPWARE_PHP_CONCRETE_DECORATOR_EXTENSION_CODE: &str =
    "shopware.php.decorator.concrete_extension";
pub const SHOPWARE_PHP_DEFAULT_CONTEXT_CODE: &str = "shopware.php.context.default";
pub const SHOPWARE_PHP_SYSTEM_CONFIG_SALES_CHANNEL_CODE: &str =
    "shopware.php.system_config.sales_channel";
pub const SHOPWARE_PHP_ROUTE_LISTENER_CODE: &str = "shopware.php.listener.route_condition";
pub const SHOPWARE_PHP_SYMFONY_COOKIE_CODE: &str = "shopware.php.cookie.symfony_insecure";
pub const SHOPWARE_PHP_EMPTY_RESPONSE_CODE: &str = "shopware.php.response.empty";
pub const SHOPWARE_PHP_LOCAL_WRITE_CODE: &str = "shopware.php.filesystem.local_write";
pub const SHOPWARE_PHP_UNBOUNDED_SEARCH_CODE: &str = "shopware.php.repository.unbounded_search";
pub const SHOPWARE_PHP_SYSTEM_CONFIG_CONSTRUCTOR_CODE: &str =
    "shopware.php.system_config.constructor";
pub const SHOPWARE_PHP_STATE_NAME_CODE: &str = "shopware.php.state.translated_name";
pub const SHOPWARE_PHP_MESSAGE_PAYLOAD_CODE: &str = "shopware.php.message.entity_payload";
pub const SHOPWARE_PHP_GET_MUTATION_CODE: &str = "shopware.php.route.get_mutation";
pub const SHOPWARE_PHP_ROUTE_ACL_CODE: &str = "shopware.php.route.acl_missing";
pub const SHOPWARE_PHP_GETCWD_CODE: &str = "shopware.php.filesystem.getcwd";
pub const SHOPWARE_PHP_DEBUG_OUTPUT_CODE: &str = "shopware.php.function.debug_output";
pub const SHOPWARE_PHP_EMPTY_CATCH_CODE: &str = "shopware.php.catch.empty";

const ENTITY_REPOSITORY_CLASS: &str =
    "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityRepository";
const PAYMENT_HANDLER_CLASS: &str =
    "Shopware\\Core\\Checkout\\Payment\\Cart\\PaymentHandler\\AbstractPaymentHandler";
const SCHEDULED_TASK_CLASS: &str =
    "Shopware\\Core\\Framework\\MessageQueue\\ScheduledTask\\ScheduledTask";
const USER_ENTITY_CLASS: &str = "Shopware\\Core\\System\\User\\UserEntity";
const SESSION_INTERFACE_CLASS: &str =
    "Symfony\\Component\\HttpFoundation\\Session\\SessionInterface";
const ROUTE_ANNOTATION_CLASS: &str = "Symfony\\Component\\Routing\\Annotation\\Route";
const ROUTE_ATTRIBUTE_CLASS: &str = "Symfony\\Component\\Routing\\Attribute\\Route";

#[derive(Clone)]
pub struct ShopwarePhpSemanticAnalyzer {
    php_index: Option<PhpIndex>,
}

impl ShopwarePhpSemanticAnalyzer {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl DeclaredAnalyzer for ShopwarePhpSemanticAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "shopware.php.semantic",
        description: "Store review rules needing type information: internal API use, repositories queried in loops, session access in Store API routes and payment handlers, scheduled task intervals, default contexts, system config reads ignoring the sales channel, kernel listeners checking the route, insecure Symfony cookies, empty responses, writes to the local disk, unrestricted searches, config reads in constructors, translated state names, entities in queue messages, GET routes that change data, Admin API write routes without privileges, getcwd(), debug output and empty catch blocks.",
        domain: "shopware",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Php],
        source: "shopware-lsp",
        codes: &[
            SHOPWARE_PHP_REPOSITORY_IN_LOOP_CODE,
            SHOPWARE_PHP_INTERNAL_CLASS_EXTENSION_CODE,
            SHOPWARE_PHP_INTERNAL_FUNCTION_CALL_CODE,
            SHOPWARE_PHP_INTERNAL_METHOD_CALL_CODE,
            SHOPWARE_PHP_SESSION_CONSTRUCTOR_CODE,
            SHOPWARE_PHP_SESSION_PAYMENT_HANDLER_CODE,
            SHOPWARE_PHP_SESSION_STORE_API_CODE,
            SHOPWARE_PHP_SCHEDULED_TASK_INTERVAL_CODE,
            SHOPWARE_PHP_USER_STORE_TOKEN_CODE,
            SHOPWARE_PHP_CONCRETE_DECORATOR_EXTENSION_CODE,
            SHOPWARE_PHP_DEFAULT_CONTEXT_CODE,
            SHOPWARE_PHP_SYSTEM_CONFIG_SALES_CHANNEL_CODE,
            SHOPWARE_PHP_ROUTE_LISTENER_CODE,
            SHOPWARE_PHP_SYMFONY_COOKIE_CODE,
            SHOPWARE_PHP_EMPTY_RESPONSE_CODE,
            SHOPWARE_PHP_LOCAL_WRITE_CODE,
            SHOPWARE_PHP_UNBOUNDED_SEARCH_CODE,
            SHOPWARE_PHP_SYSTEM_CONFIG_CONSTRUCTOR_CODE,
            SHOPWARE_PHP_STATE_NAME_CODE,
            SHOPWARE_PHP_MESSAGE_PAYLOAD_CODE,
            SHOPWARE_PHP_GET_MUTATION_CODE,
            SHOPWARE_PHP_ROUTE_ACL_CODE,
            SHOPWARE_PHP_GETCWD_CODE,
            SHOPWARE_PHP_DEBUG_OUTPUT_CODE,
            SHOPWARE_PHP_EMPTY_CATCH_CODE,
        ],
    };
}

impl ProblemAnalyzer for ShopwarePhpSemanticAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::diagnostics::inputs(&self.php_index))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(php_index) = &self.php_index else {
            return Ok(Vec::new());
        };
        if document.language != Some(LanguageId::Php) {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let Some(state) = swlsp_lsp_phpanalysis::for_document(php_index, document)? else {
            return Ok(Vec::new());
        };
        let mut references: Vec<&Reference> = state.document.references.iter().collect();
        references.sort_by_key(|reference| reference.range.start());
        let mut run = Run {
            semantic: &state.document,
            snapshot: &state.snapshot,
            references,
            problems: Vec::new(),
            store_api_classes: HashMap::new(),
        };
        phpquery::visit(
            &root,
            &[
                PHP_CLASS_DECLARATION,
                PHP_MEMBER_CALL,
                PHP_FUNCTION_CALL,
                PHP_SCOPED_CALL,
                PHP_OBJECT_CREATION,
                PHP_CATCH_CLAUSE,
            ],
            |node| {
                if ctx.is_cancelled() {
                    return false;
                }
                match node.kind() {
                    PHP_CLASS_DECLARATION => {
                        run.check_class(node);
                        run.check_route_listeners(node);
                        run.check_preflight_class(node);
                    }
                    PHP_MEMBER_CALL => {
                        run.check_member_call(node);
                        run.check_system_config_call(node);
                        run.check_cookie_secure_call(node);
                        run.check_filesystem_method(node);
                        run.check_unbounded_search(node);
                        run.check_system_config_in_constructor(node);
                        run.check_state_name_comparison(node);
                    }
                    PHP_FUNCTION_CALL => {
                        run.check_function_call(node);
                        run.check_filesystem_function(node);
                        run.check_preflight_function(node);
                    }
                    PHP_SCOPED_CALL => {
                        run.check_default_context(node);
                        run.check_cookie_creation(node);
                    }
                    PHP_CATCH_CLAUSE => run.check_empty_catch(node),
                    PHP_OBJECT_CREATION => {
                        run.check_cookie_creation(node);
                        run.check_empty_response(node);
                    }
                    _ => {}
                }
                true
            },
        );
        ctx.check()?;
        let mut problems = run.problems;
        problems.sort_by(|left, right| {
            left.range
                .start()
                .cmp(&right.range.start())
                .then_with(|| left.id.cmp(&right.id))
        });
        Ok(problems)
    }
}

pub(super) struct Run<'a> {
    pub(super) semantic: &'a Document,
    pub(super) snapshot: &'a Snapshot,
    /// The references of the document, by start.
    references: Vec<&'a Reference>,
    problems: Vec<Problem>,
    store_api_classes: HashMap<SymbolId, bool>,
}

impl Run<'_> {
    pub(super) fn add(&mut self, code: &str, range: TextRange, message: String) {
        self.problems.push(warning(code, range, message));
    }

    fn check_class(&mut self, class_node: &SyntaxNode) {
        let Some(class) = self.class_symbol(Some(class_node)) else {
            return;
        };
        if class.kind != SymbolKind::Class {
            return;
        }
        for parent_name in class.extends() {
            let Some(parent) = preferred_semantic_class(Some(self.snapshot), parent_name) else {
                continue;
            };
            let range = self.parent_range(class_node, &parent);
            // Generated stubs carry the flag for every PHP built-in; only a
            // declared `@internal` counts.
            if parent.flags.has(Flags::INTERNAL) && !parent.flags.has(Flags::GENERATED_STUB) {
                self.add(
                    SHOPWARE_PHP_INTERNAL_CLASS_EXTENSION_CODE,
                    range,
                    format!(
                        "Class {} extends internal class {}; internal APIs are not extension points",
                        &*class.name,
                        trim_php_name(&parent.fully_qualified),
                    ),
                );
            }
            if let Some(abstraction) = self.decorator_abstraction(&parent) {
                self.add(
                    SHOPWARE_PHP_CONCRETE_DECORATOR_EXTENSION_CODE,
                    range,
                    format!(
                        "Class {} should extend decoration abstraction {} instead of concrete decorator {}",
                        &*class.name,
                        trim_php_name(&abstraction.fully_qualified),
                        trim_php_name(&parent.fully_qualified),
                    ),
                );
            }
        }
        self.check_scheduled_task(&class);
    }

    fn check_scheduled_task(&mut self, class: &Symbol) {
        if equal_php_name(&class.fully_qualified, SCHEDULED_TASK_CLASS)
            || !self
                .snapshot
                .is_subtype_of(&class.fully_qualified, SCHEDULED_TASK_CLASS)
        {
            return;
        }
        let semantic = self.semantic;
        for method in &semantic.symbols {
            if method.kind != SymbolKind::Method
                || method.container != class.id
                || !eq_ignore_case(&method.name, "getDefaultInterval")
            {
                continue;
            }
            for returned in method.literal_returns() {
                if returned.ty.kind() != TypeKind::LiteralInt {
                    continue;
                }
                let Some(interval) =
                    swlsp_textutil::parse_int_literal(&returned.value.replace('_', ""))
                else {
                    continue;
                };
                if interval >= 300 {
                    continue;
                }
                self.add(
                    SHOPWARE_PHP_SCHEDULED_TASK_INTERVAL_CODE,
                    returned.range,
                    format!(
                        "Scheduled task interval is {interval} seconds; use at least 300 seconds"
                    ),
                );
                return;
            }
        }
    }

    fn check_member_call(&mut self, call: &SyntaxNode) {
        let Some(receiver) = phpquery::call_receiver(call) else {
            return;
        };
        let receiver_type = self.semantic.type_of(&receiver).ty;
        let method_name = phpquery::call_method_name(call);
        let target = call_target_name(call);
        let range = target.as_ref().map_or_else(
            || call.range_trimmed_trivia(),
            SyntaxNodeExt::range_trimmed_trivia,
        );
        let snapshot = Some(self.snapshot);
        if php_type_is_subtype(&receiver_type, snapshot, ENTITY_REPOSITORY_CLASS)
            && inside_php_loop(call)
        {
            self.add(
                SHOPWARE_PHP_REPOSITORY_IN_LOOP_CODE,
                range,
                "EntityRepository method calls inside loops cause N+1 database queries".to_owned(),
            );
        }
        if eq_ignore_case(&method_name, "getStoreToken")
            && php_type_is_subtype(&receiver_type, snapshot, USER_ENTITY_CLASS)
        {
            self.add(
                SHOPWARE_PHP_USER_STORE_TOKEN_CODE,
                range,
                "Do not read UserEntity::getStoreToken(); the Shopware store token is internal"
                    .to_owned(),
            );
        }
        if php_type_is_subtype(&receiver_type, snapshot, SESSION_INTERFACE_CLASS) {
            self.check_session_call(call, range);
        }
        let Some(target) = target else {
            return;
        };
        let source_namespace = self.namespace_at(range.start().into());
        if source_namespace == "Shopware" || source_namespace.starts_with("Shopware\\") {
            return;
        }
        for method in self.resolved_symbols(Some(&target), SymbolKind::Method) {
            if !internal_shopware_use(&method, &source_namespace, Some(self.snapshot)) {
                continue;
            }
            self.add(
                SHOPWARE_PHP_INTERNAL_METHOD_CALL_CODE,
                range,
                format!(
                    "Call to internal Shopware method {} is not supported",
                    trim_php_name(&method.fully_qualified)
                ),
            );
            break;
        }
    }

    fn check_session_call(&mut self, call: &SyntaxNode, range: TextRange) {
        if let Some(method_node) = phpquery::method_at(call)
            && eq_ignore_case(&phpquery::method_name(&method_node), "__construct")
        {
            self.add(
                SHOPWARE_PHP_SESSION_CONSTRUCTOR_CODE,
                range,
                "Do not access the Symfony session in a constructor; defer it to the method that needs it"
                    .to_owned(),
            );
        }
        let class_node = phpquery::class_at(call);
        let Some(class) = self.class_symbol(class_node.as_ref()) else {
            return;
        };
        if !equal_php_name(&class.fully_qualified, PAYMENT_HANDLER_CLASS)
            && self
                .snapshot
                .is_subtype_of(&class.fully_qualified, PAYMENT_HANDLER_CLASS)
        {
            self.add(
                SHOPWARE_PHP_SESSION_PAYMENT_HANDLER_CODE,
                range,
                "Session usage is not allowed in payment handlers".to_owned(),
            );
        }
        if let Some(class_node) = &class_node
            && self.is_store_api_class(class_node, &class)
        {
            self.add(
                SHOPWARE_PHP_SESSION_STORE_API_CODE,
                range,
                "Session usage is not allowed in Store API controllers".to_owned(),
            );
        }
    }

    fn check_function_call(&mut self, call: &SyntaxNode) {
        let Some(target) = call_target_name(call) else {
            return;
        };
        let namespace = self.namespace_at(target.text_range().start().into());
        for function in self.resolved_symbols(Some(&target), SymbolKind::Function) {
            if !internal_shopware_use(&function, &namespace, Some(self.snapshot)) {
                continue;
            }
            self.add(
                SHOPWARE_PHP_INTERNAL_FUNCTION_CALL_CODE,
                target.range_trimmed_trivia(),
                format!(
                    "Call to internal Shopware function {} is not supported",
                    trim_php_name(&function.fully_qualified)
                ),
            );
            break;
        }
    }

    pub(super) fn class_symbol(&self, node: Option<&SyntaxNode>) -> Option<Symbol> {
        let name = phpquery::direct_child(node?, PHP_NAME)?;
        let name_range = name.range_trimmed_trivia();
        self.semantic
            .symbols
            .iter()
            .find(|symbol| {
                symbol.is_class_like() && ranges_touch(name_range, symbol.selection_range)
            })
            .cloned()
    }

    fn parent_range(&self, class_node: &SyntaxNode, parent: &Symbol) -> TextRange {
        if let Some(clause) = phpquery::direct_child(class_node, PHP_EXTENDS_CLAUSE) {
            for name in phpquery::nodes(&clause, &[PHP_NAME]) {
                for candidate in self.resolved_symbols(Some(&name), SymbolKind::Class) {
                    if equal_php_name(&candidate.fully_qualified, &parent.fully_qualified) {
                        return name.range_trimmed_trivia();
                    }
                }
            }
        }
        class_node.range_trimmed_trivia()
    }

    pub(super) fn resolved_symbols(
        &self,
        node: Option<&SyntaxNode>,
        kind: SymbolKind,
    ) -> Vec<Symbol> {
        let Some(node) = node else {
            return Vec::new();
        };
        let range = node.range_trimmed_trivia();
        let mut seen: HashSet<SymbolId> = HashSet::new();
        let mut result = Vec::new();
        // A reference lies inside the name it resolves, so the ones touching
        // `range` start in it.
        let first = self
            .references
            .partition_point(|reference| reference.range.start() < range.start());
        for reference in &self.references[first..] {
            if reference.range.start() >= range.end() && reference.range != range {
                break;
            }
            if !reference_can_resolve_symbol_kind(reference, kind)
                || !ranges_touch(range, reference.range)
            {
                continue;
            }
            for id in std::iter::once(&reference.resolved).chain(reference.candidate_ids()) {
                if id.is_empty() || seen.contains(id) {
                    continue;
                }
                let Some(symbol) = self.snapshot.symbol(id.as_str()) else {
                    continue;
                };
                if symbol.kind != kind {
                    continue;
                }
                seen.insert(id.clone());
                result.push(symbol);
            }
        }
        result
    }

    fn namespace_at(&self, offset: u32) -> String {
        let mut result: &str = &self.semantic.namespace;
        let mut best_width = u32::MAX;
        for scope in &self.semantic.scopes {
            let width = u32::from(scope.range.len());
            if scope.namespace.is_empty()
                || !range_contains(scope.range, offset)
                || width > best_width
            {
                continue;
            }
            result = &scope.namespace;
            best_width = width;
        }
        trim_php_name(result).to_owned()
    }

    fn decorator_abstraction(&self, concrete: &Symbol) -> Option<Symbol> {
        if concrete.flags.has(Flags::ABSTRACT) {
            return None;
        }
        let members = (MemberResolver {
            snapshot: Some(self.snapshot),
        })
        .methods(
            &Type::named(trim_php_name(&concrete.fully_qualified), Vec::new()),
            "getDecorated",
        );
        if members.is_empty() {
            return None;
        }
        let mut queue: VecDeque<String> =
            concrete.extends().iter().map(ToString::to_string).collect();
        let mut seen = HashSet::new();
        while let Some(name) = queue.pop_front() {
            let key = lowercase(trim_php_name(&name));
            if !seen.insert(key) {
                continue;
            }
            let Some(parent) = preferred_semantic_class(Some(self.snapshot), &name) else {
                continue;
            };
            if parent.flags.has(Flags::ABSTRACT) {
                return Some(parent);
            }
            queue.extend(parent.extends().iter().map(ToString::to_string));
        }
        None
    }

    fn is_store_api_class(&mut self, class_node: &SyntaxNode, class: &Symbol) -> bool {
        if let Some(cached) = self.store_api_classes.get(&class.id) {
            return *cached;
        }
        let mut result = false;
        'attributes: for attribute_node in phpquery::attributes(class_node) {
            if !self.is_route_attribute(&attribute_node, class.attributes()) {
                continue;
            }
            let defaults = local_call_argument(&attribute_node, "defaults", 4);
            let Some(route_scope) = self.array_string_entry(defaults.as_ref(), "_routeScope")
            else {
                continue;
            };
            let route_scope = route_scope.map(|node| unwrap_php_parentheses(&node));
            if self.expression_string_equals(route_scope.as_ref(), "store-api") {
                result = true;
                break;
            }
            let Some(route_scope) = route_scope else {
                continue;
            };
            if route_scope.kind() != PHP_ARRAY {
                continue;
            }
            for item in phpquery::array_items(&route_scope) {
                if self.expression_string_equals(
                    phpquery::array_item_value(&item).as_ref(),
                    "store-api",
                ) {
                    result = true;
                    break 'attributes;
                }
            }
        }
        self.store_api_classes.insert(class.id.clone(), result);
        result
    }

    fn is_route_attribute(&self, node: &SyntaxNode, attributes: &[Attribute]) -> bool {
        let range = node.range_trimmed_trivia();
        for attribute in attributes {
            if !ranges_touch(range, attribute.range) {
                continue;
            }
            let name = trim_php_name(&attribute.name);
            return equal_php_name(name, ROUTE_ANNOTATION_CLASS)
                || equal_php_name(name, ROUTE_ATTRIBUTE_CLASS);
        }
        false
    }

    /// `Some(value)` when a key matches.
    #[allow(clippy::option_option)]
    fn array_string_entry(
        &self,
        array: Option<&SyntaxNode>,
        name: &str,
    ) -> Option<Option<SyntaxNode>> {
        let array = unwrap_php_parentheses(array?);
        if array.kind() != PHP_ARRAY {
            return None;
        }
        for item in phpquery::array_items(&array) {
            let Some(key) = phpquery::array_item_key(&item) else {
                continue;
            };
            if !self.expression_string_equals(Some(&key), name) {
                continue;
            }
            return Some(phpquery::array_item_value(&item));
        }
        None
    }

    fn expression_string_equals(&self, node: Option<&SyntaxNode>, expected: &str) -> bool {
        self.expression_strings(node)
            .iter()
            .any(|value| eq_ignore_case(value, expected))
    }

    /// The strings `node` stands for: a literal, or the class constants it
    /// may resolve to.
    pub(super) fn expression_strings(&self, node: Option<&SyntaxNode>) -> Vec<String> {
        let Some(node) = node.map(unwrap_php_parentheses) else {
            return Vec::new();
        };
        if node.kind() == PHP_STRING {
            return vec![phpquery::string_value(&node)];
        }
        self.resolved_symbols(Some(&node), SymbolKind::ClassConstant)
            .iter()
            .filter(|constant| constant.ty.kind() == TypeKind::LiteralString)
            .map(|constant| constant.ty.name().to_owned())
            .collect()
    }

    /// Whether the class named by `name` is `target` or a subclass of it.
    pub(super) fn names_subtype(&self, name: Option<&SyntaxNode>, target: &str) -> bool {
        self.resolved_symbols(name, SymbolKind::Class)
            .iter()
            .any(|class| {
                self.snapshot
                    .is_subtype_of(trim_php_name(&class.fully_qualified), target)
            })
    }

    /// Whether `expression` is an object of `target` or of a subclass.
    pub(super) fn is_instance(&self, expression: &SyntaxNode, target: &str) -> bool {
        php_type_is_subtype(
            &self.semantic.type_of(expression).ty,
            Some(self.snapshot),
            target,
        )
    }
}

fn reference_can_resolve_symbol_kind(reference: &Reference, kind: SymbolKind) -> bool {
    match kind {
        SymbolKind::Class => reference.kind == NameKind::Class,
        SymbolKind::Function => reference.kind == NameKind::Function,
        SymbolKind::Method => {
            reference.kind == NameKind::Member && reference.target_kind == SymbolKind::Method
        }
        _ => reference.target_kind == kind,
    }
}

fn preferred_semantic_class(snapshot: Option<&Snapshot>, name: &str) -> Option<Symbol> {
    let classes = snapshot?.classes(trim_php_name(name));
    if let Some(class) = classes
        .iter()
        .find(|class| !class.flags.has(Flags::GENERATED_STUB))
    {
        return Some(class.clone());
    }
    classes.into_iter().next()
}

fn internal_shopware_use(
    target: &Symbol,
    source_namespace: &str,
    snapshot: Option<&Snapshot>,
) -> bool {
    if !target.flags.has(Flags::INTERNAL) {
        return false;
    }
    let target_namespace = symbol_namespace(target, snapshot);
    if target_namespace != "Shopware" && !target_namespace.starts_with("Shopware\\") {
        return false;
    }
    !same_php_namespace_package(source_namespace, &target_namespace)
}

fn symbol_namespace(symbol: &Symbol, snapshot: Option<&Snapshot>) -> String {
    if !symbol.container.is_empty()
        && let Some(snapshot) = snapshot
        && let Some(container) = snapshot.symbol(symbol.container.as_str())
    {
        return class_namespace(&container);
    }
    php_namespace(&symbol.fully_qualified)
}

fn class_namespace(class: &Symbol) -> String {
    php_namespace(&class.fully_qualified)
}

fn php_namespace(name: &str) -> String {
    let name = trim_php_name(name);
    match name.rfind('\\') {
        Some(separator) => name[..separator].to_owned(),
        None => String::new(),
    }
}

/// Whether two namespaces belong to the same Composer package (prefix relation
/// or same `Vendor\Package` root).
pub(crate) fn same_php_namespace_package(left: &str, right: &str) -> bool {
    let left = trim_php_name(left);
    let right = trim_php_name(right);
    if left.is_empty() || right.is_empty() {
        return left == right;
    }
    if left == right
        || left
            .strip_prefix(right)
            .is_some_and(|rest| rest.starts_with('\\'))
        || right
            .strip_prefix(left)
            .is_some_and(|rest| rest.starts_with('\\'))
    {
        return true;
    }
    php_package_root(left) == php_package_root(right)
}

fn php_package_root(namespace: &str) -> &str {
    let mut parts = namespace.splitn(3, '\\');
    let first = parts.next().unwrap_or_default();
    match parts.next() {
        Some(second) => &namespace[..first.len() + 1 + second.len()],
        None => namespace,
    }
}

fn inside_php_loop(node: &SyntaxNode) -> bool {
    let mut child = node.clone();
    let mut current = node.parent();
    while let Some(parent) = current {
        match parent.kind() {
            PHP_FOREACH_STATEMENT => {
                if !contains_php_node(&phpquery::extraction_loop_parts(&parent).condition, &child) {
                    return true;
                }
            }
            PHP_FOR_STATEMENT => {
                if !contains_php_node(&phpquery::extraction_loop_parts(&parent).init, &child) {
                    return true;
                }
            }
            PHP_WHILE_STATEMENT | PHP_DO_WHILE_STATEMENT => return true,
            _ => {}
        }
        current = parent.parent();
        child = parent;
    }
    false
}

fn contains_php_node(nodes: &[SyntaxNode], node: &SyntaxNode) -> bool {
    nodes.iter().any(|candidate| {
        candidate.text_range() == node.text_range() && candidate.kind() == node.kind()
    })
}
