//! Symfony's `security.access_control`: which routes require a user. Used
//! to type `app.user` as not null in the templates those routes render.

use regex::Regex;
use serde::{Deserialize, Serialize};
use swlsp_parser_yaml::query as yamlquery;
use swlsp_syntax::SyntaxNode;

/// Security attributes only a user is granted. Any other attribute is
/// decided by a voter, which may grant access without a user.
const USER_ATTRIBUTES: &[&str] = &[
    "IS_AUTHENTICATED",
    "IS_AUTHENTICATED_FULLY",
    "IS_AUTHENTICATED_REMEMBERED",
    "IS_IMPERSONATOR",
];

/// Whether a security attribute is only granted to a user.
pub(crate) fn is_user_attribute(attribute: &str) -> bool {
    attribute.starts_with("ROLE_") || USER_ATTRIBUTES.contains(&attribute)
}

/// One entry of `security.access_control`.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct AccessRule {
    /// The path pattern, a regular expression; empty when the rule has none.
    pub path: String,
    /// The route name the rule is limited to; empty when it has none.
    pub route: String,
    pub roles: Vec<String>,
    /// Whether the rule has a condition the analysis cannot evaluate: a
    /// host, port, method, IP, request attribute or expression.
    pub conditional: bool,
}

/// The `security.access_control` entries of a configuration file, in order.
/// Only the top-level `security` key counts: an environment-specific
/// override cannot be applied without knowing the environment.
pub fn access_rules_in_yaml(root: &SyntaxNode) -> Vec<AccessRule> {
    let Some(rules) = yamlquery::root_value(root)
        .filter(yamlquery::is_mapping)
        .and_then(|config| yamlquery::property(&config, "security"))
        .filter(yamlquery::is_mapping)
        .and_then(|security| yamlquery::property(&security, "access_control"))
        .filter(yamlquery::is_sequence)
    else {
        return Vec::new();
    };
    yamlquery::items(&rules)
        .filter_map(|item| yamlquery::item_value(&item))
        .filter(yamlquery::is_mapping)
        .map(|entry| {
            let mut rule = AccessRule::default();
            for pair in yamlquery::pairs(&entry) {
                let key = yamlquery::pair_key(&pair)
                    .map(|key| yamlquery::scalar_value(&key))
                    .unwrap_or_default();
                let value = yamlquery::pair_value(&pair);
                let text = || {
                    value
                        .as_ref()
                        .map(yamlquery::scalar_value)
                        .unwrap_or_default()
                };
                match key.as_str() {
                    "path" => rule.path = text(),
                    "route" => rule.route = text(),
                    "roles" | "role" => {
                        rule.roles = match &value {
                            Some(list) if yamlquery::is_sequence(list) => yamlquery::items(list)
                                .filter_map(|item| yamlquery::item_value(&item))
                                .map(|role| yamlquery::scalar_value(&role))
                                .collect(),
                            // `roles: ROLE_A, ROLE_B`
                            _ => text()
                                .split(',')
                                .map(|role| role.trim().to_owned())
                                .filter(|role| !role.is_empty())
                                .collect(),
                        };
                    }
                    "requires_channel" => {}
                    _ => rule.conditional = true,
                }
            }
            rule
        })
        .collect()
}

/// Whether the first rule that applies to the route `name` at `path`
/// requires a user. `false` when none applies, when the rule depends on a
/// condition that cannot be evaluated, or when a pattern cannot be read.
pub(crate) fn route_requires_user(rules: &[AccessRule], name: &str, path: &str) -> bool {
    let path = filled_path(path);
    for rule in rules {
        if !rule.route.is_empty() && rule.route != name {
            continue;
        }
        if !rule.path.is_empty() {
            match Regex::new(&rule.path) {
                Ok(pattern) if pattern.is_match(&path) => {}
                Ok(_) => continue,
                Err(_) => return false,
            }
        }
        if rule.conditional {
            return false;
        }
        return !rule.roles.is_empty() && rule.roles.iter().all(|role| is_user_attribute(role));
    }
    false
}

/// A route path with its placeholders filled in: `/orgs/{org}` becomes
/// `/orgs/1`.
fn filled_path(path: &str) -> String {
    let mut result = String::with_capacity(path.len());
    let mut rest = path;
    while let Some(start) = rest.find('{') {
        result.push_str(&rest[..start]);
        match rest[start..].find('}') {
            Some(end) => {
                result.push('1');
                rest = &rest[start + end + 1..];
            }
            None => {
                rest = &rest[start..];
                break;
            }
        }
    }
    result.push_str(rest);
    result
}

#[cfg(test)]
mod tests {
    use super::*;

    fn rules(source: &str) -> Vec<AccessRule> {
        access_rules_in_yaml(&swlsp_parser_yaml::parse(source).root())
    }

    #[test]
    fn access_control_is_read_and_matched_in_order() {
        let rules = rules(
            "security:
    access_control:
        - { path: ^/login, roles: PUBLIC_ACCESS }
        - { path: '^/admin/orgs/\\d+', roles: [ROLE_ADMIN, IS_AUTHENTICATED_FULLY] }
        - { path: ^/internal, roles: ROLE_USER, ips: [127.0.0.1] }
        - { route: account_export, roles: ROLE_USER }
        - { path: ^/voter, roles: POST_EDIT }
        - path: ^/account
          roles: ROLE_USER, ROLE_STAFF
when@test:
    security:
        access_control:
            - { path: ^/, roles: ROLE_USER }
",
        );
        assert_eq!(rules.len(), 6);
        assert_eq!(rules[5].roles, ["ROLE_USER", "ROLE_STAFF"]);
        let requires = |name: &str, path: &str| route_requires_user(&rules, name, path);
        assert!(!requires("login", "/login"));
        assert!(requires("org", "/admin/orgs/{org}/members"));
        // A rule limited to an IP may not apply.
        assert!(!requires("internal", "/internal/health"));
        assert!(requires("account_export", "/export"));
        assert!(!requires("other", "/export"));
        assert!(!requires("voter", "/voter/1"));
        assert!(requires("account", "/account/{id}"));
        // No rule matches.
        assert!(!requires("home", "/"));
    }
}
