//! CartBehavior recalculation, filterable price definition and
//! ReflectionHelper migrations.

use swlsp_lsp::{Context, Problem};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_ARRAY_ACCESS, PHP_MEMBER_ACCESS, PHP_MEMBER_CALL, PHP_NAMED_ARGUMENT, PHP_SCOPED_ACCESS,
    PHP_SCOPED_CALL, PHP_VARIABLE, TK_ELLIPSIS,
};
use swlsp_php_semantic::{Document, Snapshot};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::eq_ignore_case;

use super::api::valid_source_range;
use super::{
    ShopwareMigrationAnalyzer, ShopwareMigrationPayload, call_name_range, class_name_range,
    migration_problem, node_text, php_argument_expression, php_class_is_subtype,
    php_own_method_for_migration, scan,
};
use crate::diagnostics::common::php_type_is_subtype;

const CART_BEHAVIOR_PERMISSION_CODE: &str = "shopware.migration.cart_behavior.permission";
const FILTERABLE_PRICE_DEFINITION_CODE: &str = "shopware.migration.price_definition.filterable";
const REFLECTION_HELPER_CODE: &str = "shopware.migration.reflection_helper.native";

const CART_BEHAVIOR_CLASS: &str = "Shopware\\Core\\Checkout\\Cart\\CartBehavior";
const PRICE_DEFINITION_INTERFACE: &str =
    "Shopware\\Core\\Checkout\\Cart\\Price\\Struct\\PriceDefinitionInterface";
const FILTERABLE_INTERFACE: &str =
    "Shopware\\Core\\Checkout\\Cart\\Price\\Struct\\FilterableInterface";
const RULE_CLASS: &str = "Shopware\\Core\\Framework\\Rule\\Rule";
const REFLECTION_HELPER_CLASS: &str =
    "Shopware\\Core\\Framework\\Test\\TestCaseHelper\\ReflectionHelper";

impl ShopwareMigrationAnalyzer {
    /// `$behavior->isRecalculation()` is replaced by a permission check.
    pub(super) fn cart_behavior_problems(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        document: &Document,
        snapshot: Option<&Snapshot>,
    ) -> Vec<Problem> {
        let mut result = Vec::new();
        for call in scan::member_calls(root) {
            if ctx.is_cancelled() {
                return result;
            }
            if !eq_ignore_case(&phpquery::call_method_name(&call), "isRecalculation")
                || phpquery::arguments(&call).next().is_some()
            {
                continue;
            }
            let Some(receiver) = phpquery::call_receiver(&call) else {
                continue;
            };
            if !php_type_is_subtype(
                &document.type_of(&receiver).ty,
                snapshot,
                CART_BEHAVIOR_CLASS,
            ) {
                continue;
            }
            result.push(migration_problem(
                CART_BEHAVIOR_PERMISSION_CODE,
                call_name_range(&call),
                &call,
                "Shopware 6.8: check CheckoutPermissions::SKIP_CART_PERSISTENCE instead of \
                 CartBehavior::isRecalculation()"
                    .to_owned(),
                &ShopwareMigrationPayload {
                    rule: "cart-behavior-permission".into(),
                    kind: "method".into(),
                    safe: true,
                    ..ShopwareMigrationPayload::default()
                },
            ));
        }
        result
    }

    /// A price definition with a `getFilter(): ?Rule` method declares
    /// `FilterableInterface`.
    pub(super) fn filterable_price_definition_problems(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        snapshot: Option<&Snapshot>,
    ) -> Vec<Problem> {
        let resolver = scan::resolver(root);
        let mut result = Vec::new();
        for class in scan::classes(root) {
            if ctx.is_cancelled() {
                return result;
            }
            let Some(method) = php_own_method_for_migration(&class, "getFilter") else {
                continue;
            };
            let return_type = phpquery::method_return_type(&method);
            let return_type = return_type.trim().trim_start_matches('?');
            if return_type.is_empty()
                || return_type.contains(['|', '&'])
                || phpquery::declaration_visibility(&method) != "public"
                || phpquery::declaration_is_static(&method)
                || phpquery::parameters(&method).next().is_some()
                || !eq_ignore_case(resolver.resolve(return_type).trim_matches('\\'), RULE_CLASS)
                || !php_class_is_subtype(&class, root, snapshot, PRICE_DEFINITION_INTERFACE)
                || php_class_is_subtype(&class, root, snapshot, FILTERABLE_INTERFACE)
            {
                continue;
            }
            result.push(migration_problem(
                FILTERABLE_PRICE_DEFINITION_CODE,
                class_name_range(&class),
                &class,
                "Shopware 6.7: a price definition with getFilter() must implement \
                 FilterableInterface"
                    .to_owned(),
                &ShopwareMigrationPayload {
                    rule: "filterable-price-definition".into(),
                    kind: "class".into(),
                    safe: true,
                    replacement: format!("\\{FILTERABLE_INTERFACE}"),
                    ..ShopwareMigrationPayload::default()
                },
            ));
        }
        result
    }

    /// `ReflectionHelper::getProperty(...)` and its siblings are replaced by
    /// native reflection.
    pub(super) fn reflection_helper_problems(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        source: &str,
    ) -> Vec<Problem> {
        let resolver = scan::resolver(root);
        let mut result = Vec::new();
        for call in phpquery::nodes(root, &[PHP_SCOPED_CALL]) {
            if ctx.is_cancelled() {
                return result;
            }
            let method = phpquery::call_method_name(&call);
            let Some(native) = NATIVE_REFLECTION
                .iter()
                .find(|native| eq_ignore_case(native.method, &method))
            else {
                continue;
            };
            let Some(receiver) = phpquery::call_receiver(&call) else {
                continue;
            };
            if !eq_ignore_case(
                resolver
                    .resolve(node_text(&receiver).trim())
                    .trim_matches('\\'),
                REFLECTION_HELPER_CLASS,
            ) {
                continue;
            }
            let range = call.range_trimmed_trivia();
            let replacement = native
                .replacement(&call)
                .filter(|_| valid_source_range(range.start().into(), range.end().into(), source));
            result.push(migration_problem(
                REFLECTION_HELPER_CODE,
                call_name_range(&call),
                &call,
                format!(
                    "Shopware 6.7: use {} instead of ReflectionHelper::{}()",
                    native.class, native.method
                ),
                &ShopwareMigrationPayload {
                    rule: "reflection-helper-native".into(),
                    kind: "call".into(),
                    safe: replacement.is_some(),
                    original: replacement
                        .as_ref()
                        .map(|_| source[range].to_owned())
                        .unwrap_or_default(),
                    replacement: replacement.unwrap_or_default(),
                    start: range.start().into(),
                    end: range.end().into(),
                    ..ShopwareMigrationPayload::default()
                },
            ));
        }
        result
    }
}

/// The native reflection that replaces one `ReflectionHelper` method.
struct NativeReflection {
    method: &'static str,
    class: &'static str,
    arguments: usize,
    /// The method called on the reflection object, if any.
    then: Option<Then>,
}

enum Then {
    GetFileName,
    /// `getValue($object)`, with the object the reflection was created for.
    GetValue,
}

const NATIVE_REFLECTION: &[NativeReflection] = &[
    NativeReflection {
        method: "getMethod",
        class: "ReflectionMethod",
        arguments: 2,
        then: None,
    },
    NativeReflection {
        method: "getProperty",
        class: "ReflectionProperty",
        arguments: 2,
        then: None,
    },
    NativeReflection {
        method: "getPropertyValue",
        class: "ReflectionProperty",
        arguments: 2,
        then: Some(Then::GetValue),
    },
    NativeReflection {
        method: "getFileName",
        class: "ReflectionClass",
        arguments: 1,
        then: Some(Then::GetFileName),
    },
];

impl NativeReflection {
    /// The source replacing `call`, when its arguments are plain positional
    /// ones (named arguments have other names in native reflection).
    fn replacement(&self, call: &SyntaxNode) -> Option<String> {
        let arguments: Vec<SyntaxNode> = phpquery::arguments(call).collect();
        if arguments.len() != self.arguments
            || arguments.iter().any(|argument| {
                argument.kind() == PHP_NAMED_ARGUMENT
                    || argument.child_token_of_kind(TK_ELLIPSIS).is_some()
            })
        {
            return None;
        }
        let expressions: Vec<SyntaxNode> = arguments
            .iter()
            .map(php_argument_expression)
            .collect::<Option<_>>()?;
        let passed: Vec<String> = expressions
            .iter()
            .map(|expression| node_text(expression).trim().to_owned())
            .collect();
        let creation = format!("new \\{}({})", self.class, passed.join(", "));
        Some(match self.then {
            None if is_dereferenced(call) => format!("({creation})"),
            None => creation,
            Some(Then::GetFileName) => format!("({creation})->getFileName()"),
            Some(Then::GetValue) => {
                // The object is read twice: only a variable is safe to repeat.
                if expressions[0].kind() != PHP_VARIABLE {
                    return None;
                }
                format!("({creation})->getValue({})", passed[0])
            }
        })
    }
}

/// Whether a member is read from the result of `call`, which `new` only
/// allows in parentheses.
fn is_dereferenced(call: &SyntaxNode) -> bool {
    call.parent().is_some_and(|parent| {
        matches!(
            parent.kind(),
            PHP_MEMBER_CALL
                | PHP_MEMBER_ACCESS
                | PHP_SCOPED_CALL
                | PHP_SCOPED_ACCESS
                | PHP_ARRAY_ACCESS
        ) && parent.children().next().as_ref() == Some(call)
    })
}
