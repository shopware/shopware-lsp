package security

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
)

func TestConfigReferenceAtProviderDeclarationAndReferences(t *testing.T) {
	source := `security:
  providers:
    app_users:
      memory: null
    fallback:
      chain:
        providers: [app_users]
  firewalls:
    main:
      provider: fallback
      form_login:
        provider: app_users
      switch_user:
        provider: app_users
`
	file := indexer.NewParsedFile("/project/config/security.yaml", []byte(source))
	tree := file.SyntaxTree()
	require.NotNil(t, tree)

	tests := []struct {
		needle string
		name   string
		role   ConfigRole
		origin ConfigOrigin
	}{
		{
			needle: "app_users:",
			name:   "app_users",
			role:   ConfigDeclaration,
			origin: ConfigProviderDeclaration,
		},
		{
			needle: "providers: [app_users]",
			name:   "app_users",
			role:   ConfigReference,
			origin: ConfigChainProvider,
		},
		{
			needle: "provider: fallback",
			name:   "fallback",
			role:   ConfigReference,
			origin: ConfigFirewallProvider,
		},
		{
			needle: "provider: app_users\n      switch_user",
			name:   "app_users",
			role:   ConfigReference,
			origin: ConfigFirewallProvider,
		},
		{
			needle: "switch_user:\n        provider: app_users",
			name:   "app_users",
			role:   ConfigReference,
			origin: ConfigSwitchUserProvider,
		},
	}
	for _, test := range tests {
		offset := strings.Index(source, test.needle)
		require.NotEqual(t, -1, offset, test.needle)
		offset += strings.LastIndex(test.needle, test.name)
		node := tree.Root.NodeAtOffset(uint32(offset))
		reference, found := ConfigReferenceAt(node)
		require.True(t, found, test.needle)
		require.Equal(t, test.name, reference.Name, test.needle)
		require.Equal(t, test.role, reference.Role, test.needle)
		require.Equal(t, test.origin, reference.Origin, test.needle)
	}
}

func TestConfigOptionsAtSecurityAndFirewallKeys(t *testing.T) {
	source := `security:
  providers: {}
  firewalls:
    main:
      stateless: true
      form_login:
        check_path: app_login
`
	file := indexer.NewParsedFile("/project/config/security.yaml", []byte(source))
	tree := file.SyntaxTree()
	require.NotNil(t, tree)

	for needle, expected := range map[string]string{
		"providers":  "firewalls",
		"stateless":  "custom_authenticators",
		"check_path": "username_parameter",
	} {
		offset := strings.Index(source, needle)
		require.NotEqual(t, -1, offset)
		options := ConfigOptionsAt(tree.Root.NodeAtOffset(uint32(offset)))
		var names []string
		for _, option := range options {
			names = append(names, option.Name)
		}
		require.Contains(t, names, expected, needle)
	}
}

func TestConfigOptionsAtUsesMechanismSpecificNestedSchemas(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		needle   string
		contains []string
		excludes []string
	}{
		{
			name: "security root",
			source: `security:
  providers: {}
`,
			needle:   "providers",
			contains: []string{"expose_security_errors", "session_fixation_strategy"},
			excludes: []string{"enable_authenticator_manager"},
		},
		{
			name: "form login",
			source: `security:
  firewalls:
    main:
      form_login:
        check_path: app_login
`,
			needle:   "check_path",
			contains: []string{"username_parameter", "target_path_parameter", "form_only"},
			excludes: []string{"username_path"},
		},
		{
			name: "json login",
			source: `security:
  firewalls:
    main:
      json_login:
        check_path: app_login
`,
			needle:   "check_path",
			contains: []string{"username_path", "password_path", "remember_me"},
			excludes: []string{"username_parameter", "target_path_parameter"},
		},
		{
			name: "ldap login",
			source: `security:
  firewalls:
    main:
      form_login_ldap:
        service: ldap
`,
			needle:   "service",
			contains: []string{"dn_string", "search_password", "username_parameter"},
		},
		{
			name: "http basic",
			source: `security:
  firewalls:
    main:
      http_basic:
        realm: Private
`,
			needle:   "realm",
			contains: []string{"provider"},
			excludes: []string{"success_handler"},
		},
		{
			name: "login link",
			source: `security:
  firewalls:
    main:
      login_link:
        check_route: app_login_link
`,
			needle:   "check_route",
			contains: []string{"check_post_only", "used_link_cache", "max_uses"},
		},
		{
			name: "login throttling",
			source: `security:
  firewalls:
    main:
      login_throttling:
        max_attempts: 5
`,
			needle:   "max_attempts",
			contains: []string{"interval", "cache_pool", "storage_service"},
		},
		{
			name: "remember me",
			source: `security:
  firewalls:
    main:
      remember_me:
        lifetime: 3600
`,
			needle:   "lifetime",
			contains: []string{"signature_properties", "token_provider", "samesite"},
		},
		{
			name: "remember me token provider",
			source: `security:
  firewalls:
    main:
      remember_me:
        token_provider:
          service: app.token_provider
`,
			needle:   "service",
			contains: []string{"doctrine"},
		},
		{
			name: "remember me doctrine provider",
			source: `security:
  firewalls:
    main:
      remember_me:
        token_provider:
          doctrine:
            enabled: true
`,
			needle:   "enabled",
			contains: []string{"connection"},
		},
		{
			name: "logout cookie",
			source: `security:
  firewalls:
    main:
      logout:
        delete_cookies:
          SESSION:
            path: /
`,
			needle:   "path",
			contains: []string{"domain", "samesite", "partitioned"},
		},
		{
			name: "x509",
			source: `security:
  firewalls:
    main:
      x509:
        user: SSL_CLIENT_S_DN_Email
`,
			needle:   "user",
			contains: []string{"credentials", "user_identifier"},
		},
		{
			name: "access token",
			source: `security:
  firewalls:
    api:
      access_token:
        realm: API
`,
			needle:   "realm",
			contains: []string{"token_extractors", "token_handler"},
		},
		{
			name: "access token handler",
			source: `security:
  firewalls:
    api:
      access_token:
        token_handler:
          id: app.token_handler
`,
			needle:   "id",
			contains: []string{"oidc", "oidc_user_info", "cas", "oauth2"},
		},
		{
			name: "oidc handler",
			source: `security:
  firewalls:
    api:
      access_token:
        token_handler:
          oidc:
            audience: shop
`,
			needle:   "audience",
			contains: []string{"discovery", "algorithms", "encryption"},
		},
		{
			name: "oidc discovery cache",
			source: `security:
  firewalls:
    api:
      access_token:
        token_handler:
          oidc:
            discovery:
              cache:
                id: cache.app
`,
			needle:   "id",
			contains: []string{"id"},
			excludes: []string{"base_uri"},
		},
		{
			name: "memory user",
			source: `security:
  providers:
    local:
      memory:
        users:
          admin:
            password: hash
`,
			needle:   "password",
			contains: []string{"roles"},
		},
		{
			name: "password hasher",
			source: `security:
  password_hashers:
    App\User:
      algorithm: auto
`,
			needle:   "algorithm",
			contains: []string{"hash_algorithm", "iterations", "id"},
		},
		{
			name: "conditional environment",
			source: `when@test:
  security:
    firewalls:
      main:
        json_login:
          username_path: credentials.email
`,
			needle:   "username_path",
			contains: []string{"password_path", "check_path"},
			excludes: []string{"username_parameter"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			names := configOptionNamesAt(t, test.source, test.needle)
			for _, expected := range test.contains {
				require.Contains(t, names, expected)
			}
			for _, unexpected := range test.excludes {
				require.NotContains(t, names, unexpected)
			}
		})
	}
}

func configOptionNamesAt(
	t *testing.T,
	source string,
	needle string,
) []string {
	t.Helper()
	file := indexer.NewParsedFile(
		"/project/config/packages/security.yaml",
		[]byte(source),
	)
	tree := file.SyntaxTree()
	require.NotNil(t, tree)
	offset := strings.Index(source, needle)
	require.NotEqual(t, -1, offset)
	options := ConfigOptionsAt(tree.Root.NodeAtOffset(uint32(offset)))
	names := make([]string, 0, len(options))
	for _, option := range options {
		names = append(names, option.Name)
	}
	return names
}

func TestXMLConfigOccurrencesAndReferences(t *testing.T) {
	source := `<?xml version="1.0"?>
<srv:container xmlns="http://symfony.com/schema/dic/security"
    xmlns:srv="http://symfony.com/schema/dic/services">
  <config>
    <provider name="app_users">
      <memory/>
    </provider>
    <provider name="all_users">
      <chain>
        <provider>app_users</provider>
      </chain>
    </provider>
    <firewall name="main" provider="all_users">
      <form-login provider="app_users"/>
      <switch-user provider="app_users"/>
    </firewall>
  </config>
</srv:container>
`
	file := indexer.NewParsedFile(
		"/project/config/packages/security.xml",
		[]byte(source),
	)
	tree := file.SyntaxTree()
	require.NotNil(t, tree)

	occurrences := ConfigOccurrencesInDocument(
		file.Path,
		tree.Root,
	)
	require.Len(t, occurrences, 7)
	requireConfigOccurrence(
		t,
		occurrences,
		"app_users",
		ConfigProvider,
		ConfigDeclaration,
		ConfigProviderDeclaration,
	)
	requireConfigOccurrence(
		t,
		occurrences,
		"all_users",
		ConfigProvider,
		ConfigReference,
		ConfigFirewallProvider,
	)
	requireConfigOccurrence(
		t,
		occurrences,
		"app_users",
		ConfigProvider,
		ConfigReference,
		ConfigSwitchUserProvider,
	)
	requireConfigOccurrence(
		t,
		occurrences,
		"main",
		ConfigFirewall,
		ConfigDeclaration,
		ConfigFirewallDeclaration,
	)

	for _, needle := range []string{
		"<provider>app_users</provider>",
		`provider="all_users"`,
		`provider="app_users"/>`,
	} {
		offset := strings.Index(source, needle)
		require.NotEqual(t, -1, offset)
		offset += strings.Index(needle, "app_users")
		if strings.Contains(needle, "all_users") {
			offset = strings.Index(source, needle) +
				strings.Index(needle, "all_users")
		}
		reference, found := ConfigReferenceAt(
			tree.Root.NodeAtOffset(uint32(offset)),
		)
		require.True(t, found, needle)
		require.Equal(t, ConfigReference, reference.Role, needle)
	}
}

func TestPHPConfigOccurrencesRequireTypedSecurityConfig(t *testing.T) {
	source := `<?php
use Symfony\Config\SecurityConfig;

return static function (SecurityConfig $security, object $other): void {
    $security->provider('app_users')->entity();
    $security->provider('all_users')->chain()
        ->providers(['app_users', 'legacy_users']);

    $main = $security->firewall('main');
    $main->provider('all_users')
        ->switchUser()
            ->provider('app_users');

    $security->provider($dynamic);
    $other->provider('not_security');
};
`
	file := indexer.NewParsedFile(
		"/project/config/packages/security.php",
		[]byte(source),
	)
	tree := file.SyntaxTree()
	require.NotNil(t, tree)

	occurrences := ConfigOccurrencesInDocument(file.Path, tree.Root)
	require.Len(t, occurrences, 7)
	requireConfigOccurrence(
		t,
		occurrences,
		"app_users",
		ConfigProvider,
		ConfigDeclaration,
		ConfigProviderDeclaration,
	)
	requireConfigOccurrence(
		t,
		occurrences,
		"legacy_users",
		ConfigProvider,
		ConfigReference,
		ConfigChainProvider,
	)
	requireConfigOccurrence(
		t,
		occurrences,
		"main",
		ConfigFirewall,
		ConfigDeclaration,
		ConfigFirewallDeclaration,
	)
	requireConfigOccurrence(
		t,
		occurrences,
		"all_users",
		ConfigProvider,
		ConfigReference,
		ConfigFirewallProvider,
	)
	requireConfigOccurrence(
		t,
		occurrences,
		"app_users",
		ConfigProvider,
		ConfigReference,
		ConfigSwitchUserProvider,
	)
	for _, excluded := range []string{"not_security", "$dynamic"} {
		for _, occurrence := range occurrences {
			require.NotEqual(t, excluded, occurrence.Name)
		}
	}

	offset := strings.LastIndex(source, "'app_users'") + 2
	reference, found := ConfigReferenceAt(
		tree.Root.NodeAtOffset(uint32(offset)),
	)
	require.True(t, found)
	require.Equal(t, "app_users", reference.Name)
	require.Equal(t, ConfigSwitchUserProvider, reference.Origin)
}

func requireConfigOccurrence(
	t *testing.T,
	occurrences []ConfigOccurrence,
	name string,
	kind ConfigKind,
	role ConfigRole,
	origin ConfigOrigin,
) {
	t.Helper()
	for _, occurrence := range occurrences {
		if occurrence.Name == name &&
			occurrence.Kind == kind &&
			occurrence.Role == role &&
			occurrence.Origin == origin {
			return
		}
	}
	require.Failf(
		t,
		"config occurrence not found",
		"%s kind=%d role=%d origin=%d in %#v",
		name,
		kind,
		role,
		origin,
		occurrences,
	)
}
