//! Port of Go `shopware_php_local.go`: cheap, syntax-local Shopware checks
//! in a single CST walk. No PHP semantic document is constructed.

use std::sync::{Arc, LazyLock};

use regex::Regex;
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::*;
use swlsp_php::NameResolver;
use swlsp_php_types::equal_fold;
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

use super::util::{go_parse_int, node_text, warning};
use crate::ProblemAnalyzer;
use crate::common::{local_call_argument, unwrap_php_parentheses};

pub const SHOPWARE_PHP_SUPERGLOBAL_CODE: &str = "shopware.php.superglobal";
pub const SHOPWARE_PHP_DISALLOWED_FUNCTION_CODE: &str = "shopware.php.function.disallowed";
pub const SHOPWARE_PHP_SESSION_FUNCTION_CODE: &str = "shopware.php.session.function";
pub const SHOPWARE_PHP_GLOB_BRACE_CODE: &str = "shopware.php.glob_brace";
pub const SHOPWARE_PHP_TLS_VERIFICATION_CODE: &str = "shopware.php.tls.verification_disabled";
pub const SHOPWARE_PHP_PREDICTABLE_SALT_CODE: &str = "shopware.php.crypto.predictable_salt";
pub const SHOPWARE_PHP_WEAK_KEY_CODE: &str = "shopware.php.crypto.weak_key";
pub const SHOPWARE_PHP_INSECURE_COOKIE_CODE: &str = "shopware.php.cookie.insecure";
pub const SHOPWARE_PHP_FOREIGN_KEY_CHECKS_CODE: &str = "shopware.php.foreign_key_checks.disabled";

/// Go `(?i)\bFOREIGN_KEY_CHECKS\s*=\s*0\b` with RE2's ASCII `\b` and `\s`.
static DISABLED_FOREIGN_KEY_CHECKS: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"(?i)(?-u:\b)FOREIGN_KEY_CHECKS[\t\n\x0C\r ]*=[\t\n\x0C\r ]*0(?-u:\b)")
        .expect("foreign key pattern")
});

const FORBIDDEN_SUPERGLOBALS: [&str; 4] = ["_GET", "_POST", "_FILES", "_REQUEST"];
const DISALLOWED_FUNCTIONS: [&str; 5] = ["var_dump", "exit", "die", "dd", "dump"];
const DISALLOWED_SESSION_FUNCTIONS: [&str; 3] =
    ["session_write_close", "session_start", "session_destroy"];

/// Go `ShopwarePHPLocalAnalyzer`.
#[derive(Debug, Default, Clone, Copy)]
pub struct ShopwarePhpLocalAnalyzer;

impl ShopwarePhpLocalAnalyzer {
    pub fn new() -> Self {
        Self
    }
}

impl ProblemAnalyzer for ShopwarePhpLocalAnalyzer {
    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        if document.language != Some(LanguageId::Php) {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let mut result = Vec::new();
        let mut name_resolver: Option<NameResolver> = None;
        phpquery::visit(
            &root,
            &[PHP_VARIABLE, PHP_FUNCTION_CALL, PHP_NAME, PHP_STRING],
            |node| {
                if ctx.is_cancelled() {
                    return false;
                }
                match node.kind() {
                    PHP_VARIABLE => {
                        if FORBIDDEN_SUPERGLOBALS.contains(&phpquery::variable_name(node).as_str())
                        {
                            let name = phpquery::variable_key(node);
                            result.push(local_problem(
                                SHOPWARE_PHP_SUPERGLOBAL_CODE,
                                node.range_trimmed_trivia(),
                                format!(
                                    "Do not use superglobal {name}; use the request object instead"
                                ),
                            ));
                        }
                    }
                    PHP_FUNCTION_CALL => result.extend(function_problems(node)),
                    PHP_NAME => {
                        if let Some(problem) = name_problem(node) {
                            result.push(problem);
                        }
                    }
                    PHP_STRING => {
                        let value = phpquery::string_value(node);
                        if contains_ascii_fold(&value, "FOREIGN_KEY_CHECKS")
                            && DISABLED_FOREIGN_KEY_CHECKS.is_match(&value)
                        {
                            let resolver =
                                name_resolver.get_or_insert_with(|| NameResolver::new(&root));
                            if let Some(problem) = foreign_key_problem(node, resolver) {
                                result.push(problem);
                            }
                        }
                    }
                    _ => {}
                }
                true
            },
        );
        ctx.check()?;
        let suppressions = swlsp_php_suppression::parse(document.source());
        result.retain(|problem| {
            !suppressions.suppresses(problem.range.start().into(), problem.id.as_str())
        });
        result.sort_by_key(|problem| problem.range.start());
        Ok(result)
    }
}

fn local_problem(code: &str, range: TextRange, message: String) -> Problem {
    warning(code, range, message)
}

fn unwrap_opt(node: Option<SyntaxNode>) -> Option<SyntaxNode> {
    node.map(|node| unwrap_php_parentheses(&node))
}

fn function_problems(call: &SyntaxNode) -> Option<Problem> {
    let name = normalized_local_function_name(call);
    if name.is_empty() {
        return None;
    }
    let name_range = php_call_name_range(call);
    if DISALLOWED_FUNCTIONS.contains(&name.as_str()) {
        return Some(local_problem(
            SHOPWARE_PHP_DISALLOWED_FUNCTION_CODE,
            name_range,
            format!("Do not use {name}() in application code"),
        ));
    }
    if DISALLOWED_SESSION_FUNCTIONS.contains(&name.as_str()) {
        return Some(local_problem(
            SHOPWARE_PHP_SESSION_FUNCTION_CODE,
            name_range,
            format!("Do not use {name}(); use Symfony's session from the request instead"),
        ));
    }
    match name.as_str() {
        "curl_setopt" => disabled_curl_option_problem(call),
        "curl_setopt_array" => disabled_curl_option_array_problem(call),
        "stream_context_create" => disabled_stream_context_problem(call),
        "crypt" => {
            let salt = unwrap_opt(local_call_argument(call, "salt", 1))?;
            (salt.kind() == PHP_STRING).then(|| {
                local_problem(
                    SHOPWARE_PHP_PREDICTABLE_SALT_CODE,
                    salt.range_trimmed_trivia(),
                    "Hard-coded crypt() salts are predictable; generate a random salt instead"
                        .to_owned(),
                )
            })
        }
        "password_hash" => password_hash_salt_problem(call),
        "openssl_pkey_new" => weak_openssl_key_problem(call),
        "setcookie" | "setrawcookie" => insecure_native_cookie_problem(call, &name),
        _ => None,
    }
}

fn name_problem(node: &SyntaxNode) -> Option<Problem> {
    let value = phpquery::name_value(node);
    let trimmed = value.strip_prefix('\\').unwrap_or(&value);
    let name = swlsp_php_types::go_to_lower(trimmed);
    if (name == "exit" || name == "die") && is_standalone_php_name(node) {
        return Some(local_problem(
            SHOPWARE_PHP_DISALLOWED_FUNCTION_CODE,
            node.range_trimmed_trivia(),
            format!("Do not use {name} in application code"),
        ));
    }
    if !equal_fold(trimmed, "GLOB_BRACE") || !is_bare_php_constant_name(node) {
        return None;
    }
    Some(local_problem(
        SHOPWARE_PHP_GLOB_BRACE_CODE,
        node.range_trimmed_trivia(),
        "GLOB_BRACE is not portable across all supported platforms".to_owned(),
    ))
}

fn is_standalone_php_name(node: &SyntaxNode) -> bool {
    node.parent()
        .is_some_and(|parent| parent.kind() == PHP_EXPRESSION_STATEMENT)
}

fn is_bare_php_constant_name(node: &SyntaxNode) -> bool {
    const EXCLUDED: &[Kind] = &[
        PHP_FUNCTION_CALL,
        PHP_MEMBER_CALL,
        PHP_SCOPED_CALL,
        PHP_MEMBER_ACCESS,
        PHP_SCOPED_ACCESS,
        PHP_OBJECT_CREATION,
        PHP_ATTRIBUTE,
        PHP_USE_DECLARATION,
        PHP_CONST_DECLARATION,
        PHP_CLASS_CONST_DECLARATION,
        PHP_CLASS_DECLARATION,
        PHP_INTERFACE_DECLARATION,
        PHP_TRAIT_DECLARATION,
        PHP_ENUM_DECLARATION,
        PHP_FUNCTION_DECLARATION,
        PHP_METHOD_DECLARATION,
        PHP_TYPE,
    ];
    node.parent()
        .is_some_and(|parent| !EXCLUDED.contains(&parent.kind()))
}

fn disabled_curl_option_problem(call: &SyntaxNode) -> Option<Problem> {
    let option = local_call_argument(call, "option", 1)?;
    let value = local_call_argument(call, "value", 2)?;
    let option_name = normalized_php_constant_name(Some(&option));
    disabled_tls_option_value(&option_name, &value)
        .then(|| disabled_tls_problem(&option_name, &value))
}

fn disabled_curl_option_array_problem(call: &SyntaxNode) -> Option<Problem> {
    let options = unwrap_opt(local_call_argument(call, "options", 1))?;
    if options.kind() != PHP_ARRAY {
        return None;
    }
    for item in phpquery::array_items(&options) {
        let key = phpquery::array_item_key(&item);
        let value = phpquery::array_item_value(&item);
        let option_name = normalized_php_constant_name(key.as_ref());
        if let Some(value) = value
            && disabled_tls_option_value(&option_name, &value)
        {
            return Some(disabled_tls_problem(&option_name, &value));
        }
    }
    None
}

fn disabled_stream_context_problem(call: &SyntaxNode) -> Option<Problem> {
    let options = unwrap_opt(local_call_argument(call, "options", 0));
    let (ssl, _) = local_array_string_entry(options.as_ref(), "ssl")?;
    let ssl = unwrap_opt(ssl)?;
    if ssl.kind() != PHP_ARRAY {
        return None;
    }
    for option_name in ["verify_peer", "verify_peer_name"] {
        if let Some((value, _)) = local_array_string_entry(Some(&ssl), option_name)
            && is_disabled_php_boolean(value.as_ref())
        {
            return value.map(|value| disabled_tls_problem(option_name, &value));
        }
    }
    None
}

fn disabled_tls_option_value(option_name: &str, value: &SyntaxNode) -> bool {
    match option_name {
        "CURLOPT_SSL_VERIFYPEER" => is_disabled_php_boolean(Some(value)),
        "CURLOPT_SSL_VERIFYHOST" => {
            let value = unwrap_php_parentheses(value);
            if is_disabled_php_boolean(Some(&value)) {
                return true;
            }
            php_integer_literal(Some(&value)).is_some_and(|integer| integer < 2)
        }
        _ => false,
    }
}

fn disabled_tls_problem(option_name: &str, value: &SyntaxNode) -> Problem {
    local_problem(
        SHOPWARE_PHP_TLS_VERIFICATION_CODE,
        unwrap_php_parentheses(value).range_trimmed_trivia(),
        format!(
            "TLS certificate verification is disabled by {option_name}; this permits man-in-the-middle attacks"
        ),
    )
}

fn password_hash_salt_problem(call: &SyntaxNode) -> Option<Problem> {
    let options = unwrap_opt(local_call_argument(call, "options", 2));
    let (_, item) = local_array_string_entry(options.as_ref(), "salt")?;
    let range = match phpquery::array_item_key(&item) {
        Some(key) => phpquery::string_content_range(&key),
        None => item.range_trimmed_trivia(),
    };
    Some(local_problem(
        SHOPWARE_PHP_PREDICTABLE_SALT_CODE,
        range,
        "Do not provide a custom password_hash() salt; PHP generates a secure random salt"
            .to_owned(),
    ))
}

fn weak_openssl_key_problem(call: &SyntaxNode) -> Option<Problem> {
    let options = unwrap_opt(local_call_argument(call, "options", 0));
    let (value, _) = local_array_string_entry(options.as_ref(), "private_key_bits")?;
    let value = unwrap_opt(value)?;
    let bits = php_integer_literal(Some(&value))?;
    if bits >= 2048 {
        return None;
    }
    Some(local_problem(
        SHOPWARE_PHP_WEAK_KEY_CODE,
        value.range_trimmed_trivia(),
        format!("Weak cryptographic key size ({bits} bits); use at least 2048 bits for RSA keys"),
    ))
}

fn insecure_native_cookie_problem(call: &SyntaxNode, function_name: &str) -> Option<Problem> {
    if let Some(secure) = local_call_argument(call, "secure", 5) {
        if let Some(false) = php_boolean_literal(Some(&secure)) {
            return Some(insecure_cookie_problem(
                function_name,
                &secure,
                "explicitly disables the secure flag",
            ));
        }
        return None;
    }
    let options = unwrap_opt(local_call_argument(call, "expires_or_options", 2));
    if let Some(options) = &options
        && options.kind() == PHP_ARRAY
    {
        let Some((value, _)) = local_array_string_entry(Some(options), "secure") else {
            return Some(insecure_cookie_problem(
                function_name,
                options,
                "omits the secure option",
            ));
        };
        if let Some(false) = php_boolean_literal(value.as_ref())
            && let Some(value) = value
        {
            return Some(insecure_cookie_problem(
                function_name,
                &value,
                "explicitly disables the secure option",
            ));
        }
        return None;
    }
    if options.is_some() {
        // A dynamic third argument may be an options array containing secure=true.
        return None;
    }
    Some(insecure_cookie_problem(
        function_name,
        call,
        "omits the secure flag",
    ))
}

fn insecure_cookie_problem(function_name: &str, node: &SyntaxNode, reason: &str) -> Problem {
    let range = if node.kind() == PHP_FUNCTION_CALL {
        php_call_name_range(node)
    } else {
        node.range_trimmed_trivia()
    };
    local_problem(
        SHOPWARE_PHP_INSECURE_COOKIE_CODE,
        range,
        format!("{function_name}() {reason}; cookies should be restricted to HTTPS"),
    )
}

fn foreign_key_problem(literal: &SyntaxNode, resolver: &NameResolver) -> Option<Problem> {
    let method = phpquery::method_at(literal)?;
    let class = phpquery::class_at(literal)?;
    if !equal_fold(&phpquery::method_name(&method), "update") {
        return None;
    }
    let allowed = phpquery::class_extends(&class).iter().any(|parent| {
        let resolved = resolver.resolve(parent);
        let resolved = resolved.strip_prefix('\\').unwrap_or(&resolved);
        resolved == "Shopware\\Core\\Framework\\Migration\\MigrationStep"
            || resolved == "Shopware\\Core\\Framework\\Plugin"
    });
    if !allowed {
        return None;
    }
    let value = phpquery::string_value(literal);
    let found = DISABLED_FOREIGN_KEY_CHECKS.find(&value)?;
    let content = phpquery::string_content_range(literal);
    let start = content.start() + TextSize::from(found.start() as u32);
    let range = TextRange::new(
        start,
        start + TextSize::from((found.end() - found.start()) as u32),
    );
    Some(local_problem(
        SHOPWARE_PHP_FOREIGN_KEY_CHECKS_CODE,
        range,
        "Do not disable foreign-key checks in migrations; delete data in dependency order"
            .to_owned(),
    ))
}

fn normalized_local_function_name(call: &SyntaxNode) -> String {
    let name = phpquery::direct_child(call, PHP_NAME)
        .map(|node| phpquery::name_value(&node))
        .unwrap_or_default();
    let name = name.trim();
    let absolute = name.starts_with('\\');
    let name = name.strip_prefix('\\').unwrap_or(name);
    if name.is_empty() || name.contains('\\') && !absolute {
        return String::new();
    }
    swlsp_php_types::go_to_lower(name)
}

fn normalized_php_constant_name(node: Option<&SyntaxNode>) -> String {
    let Some(node) = node.map(unwrap_php_parentheses) else {
        return String::new();
    };
    if node.kind() != PHP_NAME {
        return String::new();
    }
    let value = phpquery::name_value(&node);
    go_to_upper(value.strip_prefix('\\').unwrap_or(&value))
}

/// Go `strings.ToUpper`.
fn go_to_upper(value: &str) -> String {
    value
        .chars()
        .map(|character| {
            let mut upper = character.to_uppercase();
            match (upper.next(), upper.next()) {
                (Some(single), None) => single,
                _ => character,
            }
        })
        .collect()
}

fn php_call_name_range(call: &SyntaxNode) -> TextRange {
    match phpquery::direct_child(call, PHP_NAME) {
        Some(name) => name.range_trimmed_trivia(),
        None => call.range_trimmed_trivia(),
    }
}

/// Go `localArrayStringEntry`: `(value, item)` of the first string-keyed
/// entry named `name`.
fn local_array_string_entry(
    array: Option<&SyntaxNode>,
    name: &str,
) -> Option<(Option<SyntaxNode>, SyntaxNode)> {
    let array = unwrap_php_parentheses(array?);
    if array.kind() != PHP_ARRAY {
        return None;
    }
    for candidate in phpquery::array_items(&array) {
        let Some(key) = phpquery::array_item_key(&candidate) else {
            continue;
        };
        if !equal_fold(&phpquery::string_value(&key), name) {
            continue;
        }
        return Some((phpquery::array_item_value(&candidate), candidate));
    }
    None
}

/// Go `phpBooleanLiteral`: `Some(value)` for literal booleans, `null` and
/// integer literals.
fn php_boolean_literal(node: Option<&SyntaxNode>) -> Option<bool> {
    let node = unwrap_php_parentheses(node?);
    match node.kind() {
        PHP_BOOLEAN => {
            let value = swlsp_php_types::go_to_lower(node_text(&node).trim());
            match value.as_str() {
                "true" => Some(true),
                "false" => Some(false),
                _ => None,
            }
        }
        PHP_NULL => Some(false),
        PHP_NUMBER => php_integer_literal(Some(&node)).map(|value| value != 0),
        _ => None,
    }
}

fn is_disabled_php_boolean(node: Option<&SyntaxNode>) -> bool {
    php_boolean_literal(node) == Some(false)
}

fn php_integer_literal(node: Option<&SyntaxNode>) -> Option<i64> {
    let node = node?;
    if node.kind() != PHP_NUMBER {
        return None;
    }
    go_parse_int(&node_text(node).trim().replace('_', ""))
}

/// Go `containsASCIIFoldLocal`: byte windows compared case-insensitively.
/// For an ASCII needle, Go's `strings.EqualFold` on a same-length byte
/// window only matches ASCII text, so ASCII folding is exact.
fn contains_ascii_fold(source: &str, needle: &str) -> bool {
    let (source, needle) = (source.as_bytes(), needle.as_bytes());
    if needle.is_empty() {
        return true;
    }
    source
        .windows(needle.len())
        .any(|window| window.eq_ignore_ascii_case(needle))
}
