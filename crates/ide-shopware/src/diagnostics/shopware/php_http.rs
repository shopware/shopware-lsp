//! Symfony HTTP objects: cookies restricted to HTTPS and responses with a
//! body.

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::*;
use swlsp_php_semantic::{AttributeValueKind, Parameter, Symbol, SymbolKind};
use swlsp_php_types::Kind as TypeKind;
use swlsp_php_types::Type;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::eq_ignore_case;

use super::php_local::{php_boolean_literal, php_integer_literal};
use super::php_semantic::{
    Run, SHOPWARE_PHP_EMPTY_RESPONSE_CODE, SHOPWARE_PHP_SYMFONY_COOKIE_CODE,
};
use super::util::trim_php_name;
use crate::diagnostics::common::{
    call_target_name, local_argument_expression, local_call_argument, unwrap_php_parentheses,
};

const COOKIE_CLASS: &str = "Symfony\\Component\\HttpFoundation\\Cookie";
const RESPONSE_CLASS: &str = "Symfony\\Component\\HttpFoundation\\Response";

/// The position of `$secure` in `Cookie::__construct()` and `create()`.
const SECURE_PARAMETER: usize = 5;

/// Status codes whose response has no body.
const EMPTY_STATUS_CODES: [i64; 7] = [204, 301, 302, 303, 304, 307, 308];
/// Constructor parameters holding the body of a response.
const BODY_PARAMETERS: [&str; 5] = ["content", "data", "body", "message", "html"];
/// Constructor parameters holding the status code of a response.
const STATUS_PARAMETERS: [&str; 3] = ["status", "statusCode", "code"];

impl Run<'_> {
    /// `new Cookie(...)` and `Cookie::create(...)` that do not set the
    /// secure flag.
    pub(super) fn check_cookie_creation(&mut self, creation: &SyntaxNode) {
        if !self.creates_cookie(creation) || is_secure_creation(creation) {
            return;
        }
        let range = if creation.kind() == PHP_SCOPED_CALL {
            call_target_name(creation)
        } else {
            phpquery::direct_child(creation, PHP_NAME)
        }
        .map_or_else(
            || creation.range_trimmed_trivia(),
            |name| name.range_trimmed_trivia(),
        );
        self.add(
            SHOPWARE_PHP_SYMFONY_COOKIE_CODE,
            range,
            "Symfony cookie without the secure flag; set $secure to true to restrict it to HTTPS"
                .to_owned(),
        );
    }

    /// `$cookie->withSecure(false)`.
    pub(super) fn check_cookie_secure_call(&mut self, call: &SyntaxNode) {
        if !eq_ignore_case(&phpquery::call_method_name(call), "withSecure") {
            return;
        }
        // Without an argument the flag is set.
        let Some(secure) = phpquery::argument_expression(call, 0) else {
            return;
        };
        if php_boolean_literal(Some(&secure)) == Some(true) {
            return;
        }
        let Some(receiver) = phpquery::call_receiver(call) else {
            return;
        };
        if !self.is_instance(&receiver, COOKIE_CLASS) {
            return;
        }
        // An insecure creation at the start of the chain is reported itself.
        let mut origin = receiver;
        while origin.kind() == PHP_MEMBER_CALL {
            let Some(next) = phpquery::call_receiver(&origin) else {
                break;
            };
            origin = next;
        }
        if self.creates_cookie(&origin) && !is_secure_creation(&origin) {
            return;
        }
        self.add(
            SHOPWARE_PHP_SYMFONY_COOKIE_CODE,
            secure.range_trimmed_trivia(),
            "Symfony cookie with the secure flag disabled; keep it restricted to HTTPS".to_owned(),
        );
    }

    fn creates_cookie(&self, node: &SyntaxNode) -> bool {
        match node.kind() {
            PHP_OBJECT_CREATION => {}
            PHP_SCOPED_CALL if eq_ignore_case(&phpquery::call_method_name(node), "create") => {}
            _ => return false,
        }
        self.names_subtype(
            phpquery::direct_child(node, PHP_NAME).as_ref(),
            COOKIE_CLASS,
        )
    }

    /// A response created with an empty body and a status that expects one.
    pub(super) fn check_empty_response(&mut self, creation: &SyntaxNode) {
        let Some(name) = phpquery::direct_child(creation, PHP_NAME) else {
            return;
        };
        let Some(constructor) = self
            .resolved_symbols(Some(&name), SymbolKind::Class)
            .into_iter()
            .find(|class| {
                self.snapshot
                    .is_subtype_of(trim_php_name(&class.fully_qualified), RESPONSE_CLASS)
            })
            .and_then(|class| self.constructor(&class))
        else {
            return;
        };
        let Some((position, body)) = parameter_among(&constructor, &BODY_PARAMETERS) else {
            return;
        };
        let empty = match argument_for(creation, position, body) {
            Passed::Unknown => return,
            Passed::Omitted => defaults_to_empty_string(body),
            Passed::Value(value) => self.is_empty_body(&value),
        };
        if !empty {
            return;
        }
        if let Some((position, status)) = parameter_among(&constructor, &STATUS_PARAMETERS)
            && let Passed::Value(value) = argument_for(creation, position, status)
            && self
                .integer(&value)
                .is_some_and(|status| EMPTY_STATUS_CODES.contains(&status))
        {
            return;
        }
        self.add(
            SHOPWARE_PHP_EMPTY_RESPONSE_CODE,
            name.range_trimmed_trivia(),
            "Response with an empty body; return content, or use a status like 204 for an \
             intentionally empty response"
                .to_owned(),
        );
    }

    /// The constructor `class` declares or inherits.
    fn constructor(&self, class: &Symbol) -> Option<Symbol> {
        let mut current = class.clone();
        // Bounded against inheritance cycles in broken code.
        for _ in 0..32 {
            if let Some(constructor) = self
                .snapshot
                .members(current.id.as_str(), "__construct")
                .into_iter()
                .find(|member| member.kind == SymbolKind::Method)
            {
                return Some(constructor);
            }
            let parent = current.extends().first()?.to_string();
            current = self
                .snapshot
                .classes(trim_php_name(&parent))
                .into_iter()
                .next()?;
        }
        None
    }

    fn is_empty_body(&self, value: &SyntaxNode) -> bool {
        let value = unwrap_php_parentheses(value);
        match value.kind() {
            PHP_NULL => true,
            PHP_STRING => {
                let text = value.text().to_string();
                matches!(text.trim(), "''" | "\"\"")
            }
            _ => {
                let ty = self.semantic.type_of(&value).ty;
                ty.kind() == TypeKind::Null
                    || ty.kind() == TypeKind::LiteralString && ty.name().is_empty()
            }
        }
    }

    fn integer(&self, value: &SyntaxNode) -> Option<i64> {
        let value = unwrap_php_parentheses(value);
        if let Some(literal) = php_integer_literal(Some(&value)) {
            return Some(literal);
        }
        let ty = self.semantic.type_of(&value).ty;
        (ty.kind() == TypeKind::LiteralInt)
            .then(|| swlsp_textutil::parse_int_literal(ty.name()))
            .flatten()
    }
}

/// Whether a cookie creation passes `secure: true`, or sets the flag in
/// the calls chained to it.
fn is_secure_creation(creation: &SyntaxNode) -> bool {
    if local_call_argument(creation, "secure", SECURE_PARAMETER)
        .is_some_and(|secure| php_boolean_literal(Some(&secure)) == Some(true))
    {
        return true;
    }
    let mut current = creation.clone();
    while let Some(call) = current.parent().filter(|parent| {
        parent.kind() == PHP_MEMBER_CALL
            && phpquery::call_receiver(parent).as_ref() == Some(&current)
    }) {
        if eq_ignore_case(&phpquery::call_method_name(&call), "withSecure")
            && phpquery::argument_expression(&call, 0)
                .is_none_or(|secure| php_boolean_literal(Some(&secure)) == Some(true))
        {
            return true;
        }
        current = call;
    }
    false
}

/// Whether leaving `body` out passes an empty string. The index keeps few
/// default values, so an optional string parameter without one is taken
/// for `?string $content = ''`, the signature of Symfony's `Response`; an
/// optional `mixed $data = null` (`JsonResponse`) is not.
fn defaults_to_empty_string(body: &Parameter) -> bool {
    if let Some(default) = body.default_value.as_deref() {
        return default.kind == AttributeValueKind::String && default.value.is_empty();
    }
    let alternatives: &[Type] = if body.native_type.kind() == TypeKind::Union {
        body.native_type.arguments()
    } else {
        std::slice::from_ref(&body.native_type)
    };
    body.optional
        && alternatives
            .iter()
            .any(|alternative| alternative.kind() == TypeKind::String)
        && alternatives
            .iter()
            .all(|alternative| matches!(alternative.kind(), TypeKind::String | TypeKind::Null))
}

fn parameter_among<'a>(method: &'a Symbol, names: &[&str]) -> Option<(usize, &'a Parameter)> {
    method
        .parameters
        .iter()
        .enumerate()
        .find(|(_, parameter)| names.contains(&parameter.name.trim_start_matches('$')))
}

/// What a call passes for one parameter.
enum Passed {
    Omitted,
    Value(SyntaxNode),
    /// Behind a spread, where positions are unknown.
    Unknown,
}

fn argument_for(call: &SyntaxNode, position: usize, parameter: &Parameter) -> Passed {
    let name = parameter.name.trim_start_matches('$');
    for (index, argument) in phpquery::arguments(call).enumerate() {
        if argument.child_token_of_kind(TK_ELLIPSIS).is_some() {
            return Passed::Unknown;
        }
        let named = argument.kind() == PHP_NAMED_ARGUMENT;
        if named && phpquery::argument_name(&argument) == name || !named && index == position {
            return local_argument_expression(&argument).map_or(Passed::Unknown, Passed::Value);
        }
    }
    Passed::Omitted
}
