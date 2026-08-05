package symfony

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseYAMLServices(t *testing.T) {
	yamlContent := `
services:
  _defaults:
    autowire: true
    autoconfigure: true

  App\Service\ExampleService:
    arguments:
      $parameter: 'value'
    tags:
      - name: kernel.event_subscriber

  app.another_service:
    class: App\Service\AnotherService
    tags:
      - { name: doctrine.event_listener, event: postPersist }

  app.aliased: '@app.another_service'

  app.alias_service:
    alias: app.another_service

parameters:
  app.parameter.string: 'parameter value'
  app.parameter.integer: 42
  app.parameter.service: '@app.another_service'
`

	// Parse the services
	services, params, err := ParseYAMLServices("test.yaml", []byte(yamlContent))
	assert.NoError(t, err, "Should be able to parse YAML services")

	// Verify services count
	assert.Len(t, services, 4, "Should have found 4 services")

	// Create a map of services by ID for easier testing
	serviceMap := make(map[string]Service)
	for _, service := range services {
		serviceMap[service.ID] = service
	}

	// Test service with class name as ID
	service, ok := serviceMap["App\\Service\\ExampleService"]
	assert.True(t, ok, "Service 'App\\Service\\ExampleService' should exist")
	if ok {
		assert.Equal(t, "App\\Service\\ExampleService", service.Class, "Class name should match service ID")
		_, hasTag := service.Tags["kernel.event_subscriber"]
		assert.True(t, hasTag, "Service should have 'kernel.event_subscriber' tag")
	}

	// Test service with explicit ID
	service, ok = serviceMap["app.another_service"]
	assert.True(t, ok, "Service 'app.another_service' should exist")
	if ok {
		assert.Equal(t, "App\\Service\\AnotherService", service.Class, "Class should match expected value")
		_, hasTag := service.Tags["doctrine.event_listener"]
		assert.True(t, hasTag, "Service should have normalized tag name")
	}

	// Test service with string alias
	service, ok = serviceMap["app.aliased"]
	assert.True(t, ok, "Service 'app.aliased' should exist")
	if ok {
		assert.Equal(t, "app.another_service", service.AliasTarget, "Alias target should match expected value")
	}

	// Test service with alias configuration
	service, ok = serviceMap["app.alias_service"]
	assert.True(t, ok, "Service 'app.alias_service' should exist")
	if ok {
		assert.Equal(t, "app.another_service", service.AliasTarget, "Alias target should match expected value")
	}

	// Verify parameters count
	assert.Len(t, params, 3, "Should have found 3 parameters")

	// Create a map of parameters by name for easier testing
	paramMap := make(map[string]Parameter)
	for _, param := range params {
		paramMap[param.Name] = param
	}

	// Test string parameter
	param, ok := paramMap["app.parameter.string"]
	assert.True(t, ok, "Parameter 'app.parameter.string' should exist")
	if ok {
		assert.Equal(t, "parameter value", param.Value, "Parameter value should match expected string")
	}

	// Test integer parameter
	param, ok = paramMap["app.parameter.integer"]
	assert.True(t, ok, "Parameter 'app.parameter.integer' should exist")
	if ok {
		assert.Equal(t, "42", param.Value, "Parameter value should match expected numeric string")
	}

	// Test service reference parameter
	param, ok = paramMap["app.parameter.service"]
	assert.True(t, ok, "Parameter 'app.parameter.service' should exist")
	if ok {
		assert.Equal(t, "@app.another_service", param.Value, "Parameter value should match expected reference")
	}
}

func TestParseYAMLDeprecatedServiceMetadata(t *testing.T) {
	services, _, err := ParseYAMLServices(
		"services.yaml",
		[]byte(`services:
  legacy:
    class: App\Legacy
    deprecated: 'Use App\Modern instead'
  flagged:
    class: App\Flagged
    deprecated: true
  active:
    class: App\Active
    deprecated: false
  legacy_null_marker:
    class: App\LegacyNullMarker
    deprecated: ~
`),
	)
	require.NoError(t, err)
	require.Len(t, services, 4)
	assert.True(t, services[0].Deprecated)
	assert.Equal(t, "Use App\\Modern instead", services[0].Deprecation)
	assert.NotZero(t, services[0].DeprecatedRange.Len())
	assert.True(t, services[1].Deprecated)
	assert.Empty(t, services[1].Deprecation)
	assert.False(t, services[2].Deprecated)
	assert.True(t, services[3].Deprecated)
	assert.Empty(t, services[3].Deprecation)
}

func TestParseYAMLServicesFromFile(t *testing.T) {
	yamlContent := `
services:
  App\Service\ExampleService:
    tags:
      - { name: tag1 }
      - { name: tag2 }

  with_alias:
    alias: original_service

parameters:
  param1: 'value1'
  param2: 'value2'
`

	// Parse the services
	services, params, err := ParseYAMLServices("test.yaml", []byte(yamlContent))
	assert.NoError(t, err, "Should be able to parse YAML services")

	// Verify services and parameters
	assert.Len(t, services, 2, "Should have found 2 services")
	assert.Len(t, params, 2, "Should have found 2 parameters")

	// Verify service details
	serviceMap := make(map[string]Service)
	for _, service := range services {
		serviceMap[service.ID] = service
	}

	// Check the example service
	service, ok := serviceMap["App\\Service\\ExampleService"]
	assert.True(t, ok, "Service 'App\\Service\\ExampleService' should exist")
	if ok {
		assert.Equal(t, "App\\Service\\ExampleService", service.Class, "Class name should match service ID")
		// Verify tags
		assert.Contains(t, service.Tags, "tag1", "Should have tag1")
		assert.Contains(t, service.Tags, "tag2", "Should have tag2")
	}

	// Check the alias service
	service, ok = serviceMap["with_alias"]
	assert.True(t, ok, "Service 'with_alias' should exist")
	if ok {
		assert.Equal(t, "original_service", service.AliasTarget, "Alias target should match expected value")
	}

	// Check parameters
	paramMap := make(map[string]Parameter)
	for _, param := range params {
		paramMap[param.Name] = param
	}

	param, ok := paramMap["param1"]
	assert.True(t, ok, "Parameter 'param1' should exist")
	if ok {
		assert.Equal(t, "value1", param.Value, "Parameter value should match expected string")
	}

	param, ok = paramMap["param2"]
	assert.True(t, ok, "Parameter 'param2' should exist")
	if ok {
		assert.Equal(t, "value2", param.Value, "Parameter value should match expected string")
	}
}

func TestParseYAMLServicesHandlesNullDefinitions(t *testing.T) {
	services, parameters, err := ParseYAMLServices("test.yaml", []byte(`
services:
  App\Service\DefaultService: ~
parameters:
  optional: null
`))
	assert.NoError(t, err)
	if assert.Len(t, services, 1) {
		assert.Equal(t, `App\Service\DefaultService`, services[0].Class)
	}
	if assert.Len(t, parameters, 1) {
		assert.Empty(t, parameters[0].Value)
	}
}
