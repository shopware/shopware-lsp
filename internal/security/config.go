package security

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
)

type ConfigKind uint8

const (
	ConfigProvider ConfigKind = iota
	ConfigFirewall
)

type ConfigRole uint8

const (
	ConfigDeclaration ConfigRole = iota
	ConfigReference
)

type ConfigOrigin uint8

const (
	ConfigProviderDeclaration ConfigOrigin = iota
	ConfigFirewallDeclaration
	ConfigFirewallProvider
	ConfigSwitchUserProvider
	ConfigChainProvider
)

type ConfigOccurrence struct {
	Name   string
	Kind   ConfigKind
	Role   ConfigRole
	Origin ConfigOrigin
	File   string
	Range  cst.TextRange
}

type ConfigRecord struct {
	File        string
	Occurrences []ConfigOccurrence
}

type ConfigSymbol struct {
	Name        string
	Kind        ConfigKind
	Occurrences []ConfigOccurrence
}

func (symbol ConfigSymbol) Declarations() []ConfigOccurrence {
	var result []ConfigOccurrence
	for _, occurrence := range symbol.Occurrences {
		if occurrence.Role == ConfigDeclaration {
			result = append(result, occurrence)
		}
	}
	return result
}

func (symbol ConfigSymbol) References() []ConfigOccurrence {
	var result []ConfigOccurrence
	for _, occurrence := range symbol.Occurrences {
		if occurrence.Role == ConfigReference {
			result = append(result, occurrence)
		}
	}
	return result
}

type ConfigOption struct {
	Name   string
	Detail string
}

// The option tree mirrors SecurityBundle 7.4's MainConfiguration and built-in
// authenticator/user-provider factories. Keep mechanism-specific options
// separate: similarly named authenticators do not accept the same keys.
func configOccurrencesInFile(
	file *indexer.ParsedFile,
) []ConfigOccurrence {
	if file == nil {
		return nil
	}
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		return nil
	}
	return parseConfigOccurrences(file.Path, tree.Root)
}

var rootConfigOptions = []ConfigOption{
	{Name: "security", Detail: "Symfony SecurityBundle configuration"},
}

var securityConfigOptions = []ConfigOption{
	{Name: "access_denied_url", Detail: "Default URL used after access is denied"},
	{Name: "session_fixation_strategy", Detail: "Session strategy after authentication"},
	{Name: "password_hashers", Detail: "Password hashing algorithms by user class"},
	{Name: "providers", Detail: "User providers"},
	{Name: "firewalls", Detail: "Application firewalls"},
	{Name: "access_control", Detail: "Ordered access-control rules"},
	{Name: "role_hierarchy", Detail: "Inherited security roles"},
	{Name: "access_decision_manager", Detail: "Authorization voting strategy"},
	{Name: "erase_credentials", Detail: "Erase credentials after authentication"},
	{Name: "hide_user_not_found", Detail: "Deprecated control for exposing user-not-found errors"},
	{Name: "expose_security_errors", Detail: "Authentication error exposure level"},
}

var providerConfigOptions = []ConfigOption{
	{Name: "id", Detail: "Service-backed user provider"},
	{Name: "chain", Detail: "Chain of user providers"},
	{Name: "entity", Detail: "Doctrine entity user provider"},
	{Name: "ldap", Detail: "LDAP user provider"},
	{Name: "memory", Detail: "In-memory users"},
}

var providerChainOptions = []ConfigOption{
	{Name: "providers", Detail: "Provider names in lookup order"},
}

var providerEntityOptions = []ConfigOption{
	{Name: "class", Detail: "User entity class"},
	{Name: "property", Detail: "Property used to load a user"},
	{Name: "manager_name", Detail: "Doctrine object manager name"},
}

var providerLDAPOptions = []ConfigOption{
	{Name: "service", Detail: "LDAP client service"},
	{Name: "base_dn", Detail: "Base distinguished name"},
	{Name: "search_dn", Detail: "Search distinguished name"},
	{Name: "search_password", Detail: "Search password"},
	{Name: "default_roles", Detail: "Roles assigned to loaded users"},
	{Name: "role_fetcher", Detail: "Service used to load roles"},
	{Name: "uid_key", Detail: "User identifier attribute"},
	{Name: "filter", Detail: "LDAP user filter"},
	{Name: "password_attribute", Detail: "Password attribute"},
	{Name: "extra_fields", Detail: "Additional LDAP attributes"},
}

var providerMemoryOptions = []ConfigOption{
	{Name: "users", Detail: "In-memory users by identifier"},
}

var providerMemoryUserOptions = []ConfigOption{
	{Name: "password", Detail: "Encoded user password"},
	{Name: "roles", Detail: "Roles assigned to the user"},
}

var firewallConfigOptions = []ConfigOption{
	{Name: "pattern", Detail: "Request path regular expression"},
	{Name: "host", Detail: "Request host regular expression"},
	{Name: "methods", Detail: "Accepted HTTP methods"},
	{Name: "request_matcher", Detail: "RequestMatcher service"},
	{Name: "security", Detail: "Enable security for this firewall"},
	{Name: "stateless", Detail: "Do not persist the security token"},
	{Name: "lazy", Detail: "Initialize security only when used"},
	{Name: "provider", Detail: "User provider name"},
	{Name: "context", Detail: "Shared firewall context"},
	{Name: "entry_point", Detail: "Authentication entry-point service"},
	{Name: "user_checker", Detail: "User checker service"},
	{Name: "access_denied_handler", Detail: "Access-denied handler service"},
	{Name: "access_denied_url", Detail: "URL used after access is denied"},
	{Name: "custom_authenticators", Detail: "Authenticator services"},
	{Name: "form_login", Detail: "Form-login authenticator"},
	{Name: "form_login_ldap", Detail: "LDAP form-login authenticator"},
	{Name: "json_login", Detail: "JSON-login authenticator"},
	{Name: "json_login_ldap", Detail: "LDAP JSON-login authenticator"},
	{Name: "http_basic", Detail: "HTTP Basic authenticator"},
	{Name: "http_basic_ldap", Detail: "LDAP HTTP Basic authenticator"},
	{Name: "login_link", Detail: "Login-link authenticator"},
	{Name: "login_throttling", Detail: "Login attempt rate limiting"},
	{Name: "remember_me", Detail: "Remember-me authentication"},
	{Name: "remote_user", Detail: "Remote-user authentication"},
	{Name: "x509", Detail: "X.509 authentication"},
	{Name: "access_token", Detail: "Access-token authenticator"},
	{Name: "logout", Detail: "Logout handling"},
	{Name: "switch_user", Detail: "User impersonation"},
	{Name: "required_badges", Detail: "Passport badges required for authentication"},
}

var authenticatorConfigOptions = []ConfigOption{
	{Name: "provider", Detail: "User provider name"},
	{Name: "remember_me", Detail: "Enable remember-me support"},
	{Name: "success_handler", Detail: "Authentication success-handler service"},
	{Name: "failure_handler", Detail: "Authentication failure-handler service"},
}

var loginPathConfigOptions = []ConfigOption{
	{Name: "check_path", Detail: "Authentication submission path"},
	{Name: "use_forward", Detail: "Forward internally to the login path"},
	{Name: "login_path", Detail: "Authentication login path"},
}

var successHandlerConfigOptions = []ConfigOption{
	{Name: "always_use_default_target_path", Detail: "Always use the default target"},
	{Name: "default_target_path", Detail: "Default post-login target"},
	{Name: "target_path_parameter", Detail: "Request parameter containing the target path"},
	{Name: "use_referer", Detail: "Use the Referer header as target"},
}

var failureHandlerConfigOptions = []ConfigOption{
	{Name: "failure_path", Detail: "Path used after failed authentication"},
	{Name: "failure_forward", Detail: "Forward internally after authentication failure"},
	{Name: "failure_path_parameter", Detail: "Request parameter containing the failure path"},
}

var formLoginSpecificConfigOptions = []ConfigOption{
	{Name: "username_parameter", Detail: "Username request parameter"},
	{Name: "password_parameter", Detail: "Password request parameter"},
	{Name: "csrf_parameter", Detail: "CSRF request parameter"},
	{Name: "csrf_token_id", Detail: "CSRF token identifier"},
	{Name: "enable_csrf", Detail: "Enable CSRF protection"},
	{Name: "post_only", Detail: "Accept only POST login submissions"},
	{Name: "form_only", Detail: "Accept only form-encoded submissions"},
}

var formLoginConfigOptions = combineConfigOptions(
	authenticatorConfigOptions,
	loginPathConfigOptions,
	successHandlerConfigOptions,
	failureHandlerConfigOptions,
	formLoginSpecificConfigOptions,
)

var jsonLoginConfigOptions = combineConfigOptions(
	authenticatorConfigOptions,
	loginPathConfigOptions,
	[]ConfigOption{
		{Name: "username_path", Detail: "Property path containing the username"},
		{Name: "password_path", Detail: "Property path containing the password"},
	},
)

var ldapAuthenticatorConfigOptions = []ConfigOption{
	{Name: "service", Detail: "LDAP client service"},
	{Name: "dn_string", Detail: "LDAP distinguished-name template"},
	{Name: "query_string", Detail: "LDAP query used to find the user"},
	{Name: "search_dn", Detail: "LDAP search distinguished name"},
	{Name: "search_password", Detail: "LDAP search password"},
}

var formLoginLDAPConfigOptions = combineConfigOptions(
	formLoginConfigOptions,
	ldapAuthenticatorConfigOptions,
)

var jsonLoginLDAPConfigOptions = combineConfigOptions(
	jsonLoginConfigOptions,
	ldapAuthenticatorConfigOptions,
)

var httpBasicConfigOptions = []ConfigOption{
	{Name: "provider", Detail: "User provider name"},
	{Name: "realm", Detail: "HTTP Basic authentication realm"},
}

var httpBasicLDAPConfigOptions = combineConfigOptions(
	httpBasicConfigOptions,
	ldapAuthenticatorConfigOptions,
)

var remoteUserConfigOptions = []ConfigOption{
	{Name: "provider", Detail: "User provider name"},
	{Name: "user", Detail: "Server variable containing the user identifier"},
}

var x509ConfigOptions = []ConfigOption{
	{Name: "provider", Detail: "User provider name"},
	{Name: "user", Detail: "Server variable containing the certificate user"},
	{Name: "credentials", Detail: "Server variable containing certificate credentials"},
	{Name: "user_identifier", Detail: "Certificate field used as user identifier"},
}

var loginLinkConfigOptions = combineConfigOptions(
	[]ConfigOption{
		{Name: "check_route", Detail: "Route that validates login links"},
		{Name: "check_post_only", Detail: "Validate login links only on POST requests"},
		{Name: "signature_properties", Detail: "User properties included in the signature"},
		{Name: "lifetime", Detail: "Login-link lifetime in seconds"},
		{Name: "max_uses", Detail: "Maximum number of link uses"},
		{Name: "used_link_cache", Detail: "Cache service for expired links"},
		{Name: "success_handler", Detail: "Authentication success-handler service"},
		{Name: "failure_handler", Detail: "Authentication failure-handler service"},
		{Name: "provider", Detail: "User provider name"},
		{Name: "secret", Detail: "Secret used to sign login links"},
	},
	successHandlerConfigOptions,
	failureHandlerConfigOptions,
)

var loginThrottlingConfigOptions = []ConfigOption{
	{Name: "limiter", Detail: "Custom request rate-limiter service"},
	{Name: "max_attempts", Detail: "Maximum login attempts per interval"},
	{Name: "interval", Detail: "Login-attempt rate-limit interval"},
	{Name: "lock_factory", Detail: "Lock factory used by the rate limiter"},
	{Name: "cache_pool", Detail: "Cache pool storing limiter state"},
	{Name: "storage_service", Detail: "Custom limiter storage service"},
}

var rememberMeConfigOptions = []ConfigOption{
	{Name: "secret", Detail: "Secret used to sign remember-me cookies"},
	{Name: "service", Detail: "Custom remember-me handler service"},
	{Name: "user_providers", Detail: "User providers queried for remembered users"},
	{Name: "catch_exceptions", Detail: "Ignore user-provider exceptions"},
	{Name: "signature_properties", Detail: "User properties included in the cookie signature"},
	{Name: "token_provider", Detail: "Persistent remember-me token provider"},
	{Name: "token_verifier", Detail: "Custom remember-me token verifier"},
	{Name: "name", Detail: "Remember-me cookie name"},
	{Name: "lifetime", Detail: "Remember-me cookie lifetime"},
	{Name: "path", Detail: "Remember-me cookie path"},
	{Name: "domain", Detail: "Remember-me cookie domain"},
	{Name: "secure", Detail: "Remember-me cookie secure policy"},
	{Name: "httponly", Detail: "Restrict the cookie to HTTP requests"},
	{Name: "samesite", Detail: "Remember-me cookie SameSite policy"},
	{Name: "always_remember_me", Detail: "Always create a remember-me cookie"},
	{Name: "remember_me_parameter", Detail: "Request parameter enabling remember-me"},
}

var rememberMeTokenProviderOptions = []ConfigOption{
	{Name: "service", Detail: "Custom token-provider service"},
	{Name: "doctrine", Detail: "Doctrine-backed token provider"},
}

var rememberMeDoctrineOptions = []ConfigOption{
	{Name: "enabled", Detail: "Enable the Doctrine token provider"},
	{Name: "connection", Detail: "Doctrine DBAL connection name"},
}

var logoutConfigOptions = []ConfigOption{
	{Name: "path", Detail: "Logout route or path"},
	{Name: "target", Detail: "Post-logout target"},
	{Name: "invalidate_session", Detail: "Invalidate the session"},
	{Name: "clear_site_data", Detail: "Clear-Site-Data header values"},
	{Name: "delete_cookies", Detail: "Cookies removed on logout"},
	{Name: "enable_csrf", Detail: "Enable logout CSRF protection"},
	{Name: "csrf_parameter", Detail: "CSRF request parameter"},
	{Name: "csrf_token_id", Detail: "CSRF token identifier"},
	{Name: "csrf_token_manager", Detail: "CSRF token-manager service"},
}

var logoutCookieConfigOptions = []ConfigOption{
	{Name: "path", Detail: "Cookie path"},
	{Name: "domain", Detail: "Cookie domain"},
	{Name: "secure", Detail: "Delete only secure cookies"},
	{Name: "samesite", Detail: "Cookie SameSite policy"},
	{Name: "partitioned", Detail: "Delete a partitioned cookie"},
}

var switchUserConfigOptions = []ConfigOption{
	{Name: "provider", Detail: "User provider name"},
	{Name: "parameter", Detail: "Impersonation request parameter"},
	{Name: "role", Detail: "Required impersonation role"},
	{Name: "target_route", Detail: "Route after switching user"},
}

var accessControlOptions = []ConfigOption{
	{Name: "request_matcher", Detail: "RequestMatcher service"},
	{Name: "path", Detail: "Request path regular expression"},
	{Name: "host", Detail: "Request host regular expression"},
	{Name: "methods", Detail: "Accepted HTTP methods"},
	{Name: "ips", Detail: "Allowed client IP addresses"},
	{Name: "port", Detail: "Required request port"},
	{Name: "attributes", Detail: "Request attributes to match"},
	{Name: "route", Detail: "Route name to match"},
	{Name: "roles", Detail: "Required security attributes"},
	{Name: "allow_if", Detail: "Security expression"},
	{Name: "requires_channel", Detail: "Required http or https channel"},
}

var passwordHasherOptions = []ConfigOption{
	{Name: "algorithm", Detail: "Password hashing algorithm"},
	{Name: "migrate_from", Detail: "Legacy hashers to migrate from"},
	{Name: "hash_algorithm", Detail: "PBKDF2 hashing algorithm"},
	{Name: "key_length", Detail: "PBKDF2 derived-key length"},
	{Name: "ignore_case", Detail: "Compare plaintext passwords case-insensitively"},
	{Name: "encode_as_base64", Detail: "Encode the derived password as Base64"},
	{Name: "iterations", Detail: "PBKDF2 iteration count"},
	{Name: "cost", Detail: "CPU cost"},
	{Name: "time_cost", Detail: "Argon time cost"},
	{Name: "memory_cost", Detail: "Argon memory cost"},
	{Name: "id", Detail: "Custom password-hasher service"},
}

var accessDecisionOptions = []ConfigOption{
	{Name: "strategy", Detail: "Voting strategy"},
	{Name: "strategy_service", Detail: "Custom voting-strategy service"},
	{Name: "allow_if_all_abstain", Detail: "Decision when all voters abstain"},
	{Name: "allow_if_equal_granted_denied", Detail: "Decision when votes tie"},
	{Name: "service", Detail: "Custom decision strategy service"},
}

var accessTokenConfigOptions = combineConfigOptions(
	authenticatorConfigOptions,
	[]ConfigOption{
		{Name: "realm", Detail: "Authentication realm"},
		{Name: "token_extractors", Detail: "Services used to extract access tokens"},
		{Name: "token_handler", Detail: "Access-token validation handler"},
	},
)

var accessTokenHandlerOptions = []ConfigOption{
	{Name: "id", Detail: "Custom token-handler service"},
	{Name: "oidc_user_info", Detail: "OIDC UserInfo endpoint token handler"},
	{Name: "oidc", Detail: "OIDC token validation handler"},
	{Name: "cas", Detail: "CAS 2.0 token handler"},
	{Name: "oauth2", Detail: "OAuth 2.0 introspection handler service"},
}

var oidcUserInfoOptions = []ConfigOption{
	{Name: "base_uri", Detail: "OIDC server or UserInfo endpoint URI"},
	{Name: "discovery", Detail: "OIDC discovery configuration"},
	{Name: "claim", Detail: "Claim containing the user identifier"},
	{Name: "client", Detail: "HTTP client service"},
}

var oidcOptions = []ConfigOption{
	{Name: "discovery", Detail: "OIDC discovery configuration"},
	{Name: "claim", Detail: "Claim containing the user identifier"},
	{Name: "audience", Detail: "Expected token audience"},
	{Name: "issuers", Detail: "Allowed token issuers"},
	{Name: "algorithm", Detail: "Deprecated token signature algorithm"},
	{Name: "algorithms", Detail: "Allowed token signature algorithms"},
	{Name: "key", Detail: "Deprecated JSON-encoded JWK"},
	{Name: "keyset", Detail: "JSON-encoded JWK set"},
	{Name: "encryption", Detail: "Encrypted-token configuration"},
}

var oidcDiscoveryOptions = []ConfigOption{
	{Name: "base_uri", Detail: "OIDC server base URIs"},
	{Name: "cache", Detail: "OIDC discovery cache configuration"},
}

var oidcUserInfoDiscoveryOptions = []ConfigOption{
	{Name: "cache", Detail: "OIDC discovery cache configuration"},
}

var oidcDiscoveryCacheOptions = []ConfigOption{
	{Name: "id", Detail: "Cache service for OIDC discovery metadata"},
}

var oidcEncryptionOptions = []ConfigOption{
	{Name: "enabled", Detail: "Enable encrypted-token support"},
	{Name: "enforce", Detail: "Require tokens to be encrypted"},
	{Name: "algorithms", Detail: "Allowed token decryption algorithms"},
	{Name: "keyset", Detail: "JSON-encoded private JWK set"},
}

var casTokenHandlerOptions = []ConfigOption{
	{Name: "validation_url", Detail: "CAS server validation URL"},
	{Name: "prefix", Detail: "CAS protocol prefix"},
	{Name: "http_client", Detail: "HTTP client service"},
}

func combineConfigOptions(groups ...[]ConfigOption) []ConfigOption {
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	result := make([]ConfigOption, 0, total)
	for _, group := range groups {
		result = append(result, group...)
	}
	return result
}

func parseConfigOccurrences(
	path string,
	root *cst.Node,
) []ConfigOccurrence {
	if root == nil {
		return nil
	}
	switch root.Kind() {
	case yamlsyntax.YamlStream, yamlsyntax.YamlDocument:
		return parseYAMLConfigOccurrences(path, root)
	case xmlsyntax.XmlDocument:
		return parseXMLConfigOccurrences(path, root)
	case phpsyntax.PhpProgram:
		return parsePHPConfigOccurrences(path, root)
	default:
		return nil
	}
}

func parseYAMLConfigOccurrences(
	path string,
	root *yamlsyntax.Node,
) []ConfigOccurrence {
	if root == nil {
		return nil
	}
	security := yamlquery.Property(yamlquery.RootValue(root), "security")
	if !yamlquery.IsMapping(security) {
		return nil
	}
	var result []ConfigOccurrence
	providers := yamlquery.Property(security, "providers")
	if yamlquery.IsMapping(providers) {
		for _, pair := range yamlquery.Pairs(providers) {
			name := yamlquery.ScalarValue(yamlquery.PairKey(pair))
			if name == "" || strings.HasPrefix(name, "_") {
				continue
			}
			result = append(result, ConfigOccurrence{
				Name:   name,
				Kind:   ConfigProvider,
				Role:   ConfigDeclaration,
				Origin: ConfigProviderDeclaration,
				File:   path,
				Range:  yamlValueRange(yamlquery.PairKey(pair)),
			})
			config := yamlquery.PairValue(pair)
			if !yamlquery.IsMapping(config) {
				continue
			}
			chain := yamlquery.Property(config, "chain")
			if yamlquery.IsMapping(chain) {
				result = appendConfigValues(
					result,
					path,
					yamlquery.Property(chain, "providers"),
					ConfigProvider,
					ConfigChainProvider,
				)
			}
		}
	}
	firewalls := yamlquery.Property(security, "firewalls")
	if yamlquery.IsMapping(firewalls) {
		for _, pair := range yamlquery.Pairs(firewalls) {
			name := yamlquery.ScalarValue(yamlquery.PairKey(pair))
			if name == "" || strings.HasPrefix(name, "_") {
				continue
			}
			result = append(result, ConfigOccurrence{
				Name:   name,
				Kind:   ConfigFirewall,
				Role:   ConfigDeclaration,
				Origin: ConfigFirewallDeclaration,
				File:   path,
				Range:  yamlValueRange(yamlquery.PairKey(pair)),
			})
			config := yamlquery.PairValue(pair)
			if !yamlquery.IsMapping(config) {
				continue
			}
			result = appendConfigValues(
				result,
				path,
				yamlquery.Property(config, "provider"),
				ConfigProvider,
				ConfigFirewallProvider,
			)
			for _, option := range yamlquery.Pairs(config) {
				optionName := yamlquery.ScalarValue(
					yamlquery.PairKey(option),
				)
				optionConfig := yamlquery.PairValue(option)
				if !yamlquery.IsMapping(optionConfig) {
					continue
				}
				origin := ConfigFirewallProvider
				if optionName == "switch_user" {
					origin = ConfigSwitchUserProvider
				}
				result = appendConfigValues(
					result,
					path,
					yamlquery.Property(optionConfig, "provider"),
					ConfigProvider,
					origin,
				)
			}
		}
	}
	return uniqueConfigOccurrences(result)
}

func appendConfigValues(
	result []ConfigOccurrence,
	path string,
	node *yamlsyntax.Node,
	kind ConfigKind,
	origin ConfigOrigin,
) []ConfigOccurrence {
	if node == nil {
		return result
	}
	if node.Kind() == yamlsyntax.YamlScalar {
		name := yamlquery.ScalarValue(node)
		if name != "" {
			result = append(result, ConfigOccurrence{
				Name:   name,
				Kind:   kind,
				Role:   ConfigReference,
				Origin: origin,
				File:   path,
				Range:  yamlValueRange(node),
			})
		}
		return result
	}
	if yamlquery.IsSequence(node) {
		for _, item := range yamlquery.Items(node) {
			result = appendConfigValues(
				result,
				path,
				yamlquery.ItemValue(item),
				kind,
				origin,
			)
		}
	}
	return result
}

func ConfigOccurrencesInDocument(
	path string,
	root *cst.Node,
) []ConfigOccurrence {
	if root == nil {
		return nil
	}
	return parseConfigOccurrences(path, root)
}

func ConfigReferenceAt(node *cst.Node) (ConfigOccurrence, bool) {
	if node == nil {
		return ConfigOccurrence{}, false
	}
	root := node
	for root.Parent() != nil {
		root = root.Parent()
	}
	switch root.Kind() {
	case xmlsyntax.XmlDocument, phpsyntax.PhpProgram:
		nodeRange := node.RangeTrimmedTrivia()
		for _, occurrence := range parseConfigOccurrences("", root) {
			if occurrence.Range.Start <= nodeRange.End &&
				nodeRange.Start <= occurrence.Range.End {
				return occurrence, true
			}
		}
		return ConfigOccurrence{}, false
	}

	scalar := yamlScalarAt(node)
	if scalar == nil {
		return ConfigOccurrence{}, false
	}
	pair := yamlquery.AncestorPair(scalar)
	if pair == nil {
		return ConfigOccurrence{}, false
	}
	path := yamlquery.PairPath(scalar)
	key := yamlquery.PairKey(pair)
	if key == scalar {
		if len(path) == 3 && path[0] == "security" {
			switch path[1] {
			case "providers":
				return configOccurrenceAt(
					scalar,
					ConfigProvider,
					ConfigDeclaration,
					ConfigProviderDeclaration,
				)
			case "firewalls":
				return configOccurrenceAt(
					scalar,
					ConfigFirewall,
					ConfigDeclaration,
					ConfigFirewallDeclaration,
				)
			}
		}
		return ConfigOccurrence{}, false
	}
	if len(path) == 4 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "provider" {
		return configOccurrenceAt(
			scalar,
			ConfigProvider,
			ConfigReference,
			ConfigFirewallProvider,
		)
	}
	if len(path) == 5 && path[0] == "security" &&
		path[1] == "firewalls" && path[4] == "provider" {
		origin := ConfigFirewallProvider
		if path[3] == "switch_user" {
			origin = ConfigSwitchUserProvider
		}
		return configOccurrenceAt(
			scalar,
			ConfigProvider,
			ConfigReference,
			origin,
		)
	}
	if len(path) == 5 && path[0] == "security" &&
		path[1] == "providers" && path[3] == "chain" &&
		path[4] == "providers" {
		return configOccurrenceAt(
			scalar,
			ConfigProvider,
			ConfigReference,
			ConfigChainProvider,
		)
	}
	return ConfigOccurrence{}, false
}

func ConfigOptionsAt(node *cst.Node) []ConfigOption {
	scalar := yamlScalarAt(node)
	if scalar == nil {
		return nil
	}
	pair := yamlquery.AncestorPair(scalar)
	if pair == nil || yamlquery.PairKey(pair) != scalar {
		return nil
	}
	path := yamlquery.PairPath(scalar)
	if len(path) == 0 {
		return nil
	}
	return configOptionsForParent(path[:len(path)-1])
}

func ConfigOptionAt(node *cst.Node) (ConfigOption, bool) {
	scalar := yamlScalarAt(node)
	if scalar == nil {
		return ConfigOption{}, false
	}
	pair := yamlquery.AncestorPair(scalar)
	if pair == nil || yamlquery.PairKey(pair) != scalar {
		return ConfigOption{}, false
	}
	path := yamlquery.PairPath(scalar)
	if len(path) == 0 {
		return ConfigOption{}, false
	}
	name := yamlquery.ScalarValue(scalar)
	for _, option := range configOptionsForParent(path[:len(path)-1]) {
		if option.Name == name {
			return option, true
		}
	}
	return ConfigOption{}, false
}

func configOptionsForParent(path []string) []ConfigOption {
	path = unwrapConditionalConfigPath(path)
	switch {
	case len(path) == 0:
		return rootConfigOptions
	case equalConfigPath(path, "security"):
		return securityConfigOptions
	case len(path) == 3 && path[0] == "security" &&
		path[1] == "providers":
		return providerConfigOptions
	case len(path) == 4 && path[0] == "security" &&
		path[1] == "providers" && path[3] == "chain":
		return providerChainOptions
	case len(path) == 4 && path[0] == "security" &&
		path[1] == "providers" && path[3] == "entity":
		return providerEntityOptions
	case len(path) == 4 && path[0] == "security" &&
		path[1] == "providers" && path[3] == "ldap":
		return providerLDAPOptions
	case len(path) == 4 && path[0] == "security" &&
		path[1] == "providers" && path[3] == "memory":
		return providerMemoryOptions
	case len(path) == 6 && path[0] == "security" &&
		path[1] == "providers" && path[3] == "memory" &&
		path[4] == "users":
		return providerMemoryUserOptions
	case len(path) == 3 && path[0] == "security" &&
		path[1] == "firewalls":
		return firewallConfigOptions
	case len(path) == 4 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "form_login":
		return formLoginConfigOptions
	case len(path) == 4 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "form_login_ldap":
		return formLoginLDAPConfigOptions
	case len(path) == 4 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "json_login":
		return jsonLoginConfigOptions
	case len(path) == 4 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "json_login_ldap":
		return jsonLoginLDAPConfigOptions
	case len(path) == 4 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "http_basic":
		return httpBasicConfigOptions
	case len(path) == 4 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "http_basic_ldap":
		return httpBasicLDAPConfigOptions
	case len(path) == 4 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "remote_user":
		return remoteUserConfigOptions
	case len(path) == 4 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "x509":
		return x509ConfigOptions
	case len(path) == 4 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "login_link":
		return loginLinkConfigOptions
	case len(path) == 4 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "login_throttling":
		return loginThrottlingConfigOptions
	case len(path) == 4 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "remember_me":
		return rememberMeConfigOptions
	case len(path) == 5 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "remember_me" &&
		path[4] == "token_provider":
		return rememberMeTokenProviderOptions
	case len(path) == 6 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "remember_me" &&
		path[4] == "token_provider" && path[5] == "doctrine":
		return rememberMeDoctrineOptions
	case len(path) == 4 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "logout":
		return logoutConfigOptions
	case len(path) == 6 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "logout" &&
		path[4] == "delete_cookies":
		return logoutCookieConfigOptions
	case len(path) == 4 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "switch_user":
		return switchUserConfigOptions
	case len(path) == 4 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "access_token":
		return accessTokenConfigOptions
	case len(path) == 5 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "access_token" &&
		path[4] == "token_handler":
		return accessTokenHandlerOptions
	case len(path) == 6 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "access_token" &&
		path[4] == "token_handler" && path[5] == "oidc_user_info":
		return oidcUserInfoOptions
	case len(path) == 6 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "access_token" &&
		path[4] == "token_handler" && path[5] == "oidc":
		return oidcOptions
	case len(path) == 6 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "access_token" &&
		path[4] == "token_handler" && path[5] == "cas":
		return casTokenHandlerOptions
	case len(path) == 7 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "access_token" &&
		path[4] == "token_handler" && path[5] == "oidc" &&
		path[6] == "discovery":
		return oidcDiscoveryOptions
	case len(path) == 7 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "access_token" &&
		path[4] == "token_handler" && path[5] == "oidc_user_info" &&
		path[6] == "discovery":
		return oidcUserInfoDiscoveryOptions
	case len(path) == 8 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "access_token" &&
		path[4] == "token_handler" &&
		(path[5] == "oidc" || path[5] == "oidc_user_info") &&
		path[6] == "discovery" && path[7] == "cache":
		return oidcDiscoveryCacheOptions
	case len(path) == 7 && path[0] == "security" &&
		path[1] == "firewalls" && path[3] == "access_token" &&
		path[4] == "token_handler" && path[5] == "oidc" &&
		path[6] == "encryption":
		return oidcEncryptionOptions
	case equalConfigPath(path, "security", "access_control"):
		return accessControlOptions
	case len(path) == 3 && path[0] == "security" &&
		path[1] == "password_hashers":
		return passwordHasherOptions
	case equalConfigPath(path, "security", "access_decision_manager"):
		return accessDecisionOptions
	default:
		return nil
	}
}

func unwrapConditionalConfigPath(path []string) []string {
	if len(path) != 0 && strings.HasPrefix(path[0], "when@") {
		return path[1:]
	}
	return path
}

func configOccurrenceAt(
	node *yamlsyntax.Node,
	kind ConfigKind,
	role ConfigRole,
	origin ConfigOrigin,
) (ConfigOccurrence, bool) {
	name := yamlquery.ScalarValue(node)
	if role == ConfigDeclaration &&
		(name == "" || strings.HasPrefix(name, "_")) {
		return ConfigOccurrence{}, false
	}
	return ConfigOccurrence{
		Name:   name,
		Kind:   kind,
		Role:   role,
		Origin: origin,
		Range:  yamlValueRange(node),
	}, true
}

func yamlScalarAt(node *cst.Node) *yamlsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == yamlsyntax.YamlScalar {
			return current
		}
	}
	return nil
}

func equalConfigPath(path []string, expected ...string) bool {
	if len(path) != len(expected) {
		return false
	}
	for index := range path {
		if path[index] != expected[index] {
			return false
		}
	}
	return true
}

func uniqueConfigOccurrences(
	values []ConfigOccurrence,
) []ConfigOccurrence {
	seen := make(map[string]struct{}, len(values))
	result := make([]ConfigOccurrence, 0, len(values))
	for _, value := range values {
		key := strings.ToLower(value.Name) + ":" +
			value.Range.String() + ":" +
			string(rune(value.Kind)) + ":" +
			string(rune(value.Role))
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Range.Start != result[right].Range.Start {
			return result[left].Range.Start < result[right].Range.Start
		}
		return result[left].Role < result[right].Role
	})
	return result
}

func configSymbolKey(name string, kind ConfigKind) string {
	return string(rune(kind)) + ":" + strings.ToLower(name)
}

func sortConfigOccurrences(occurrences []ConfigOccurrence) {
	sort.Slice(occurrences, func(left, right int) bool {
		if occurrences[left].File != occurrences[right].File {
			return occurrences[left].File < occurrences[right].File
		}
		if occurrences[left].Range.Start != occurrences[right].Range.Start {
			return occurrences[left].Range.Start <
				occurrences[right].Range.Start
		}
		return occurrences[left].Role < occurrences[right].Role
	})
}
