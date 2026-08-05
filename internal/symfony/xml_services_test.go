package symfony

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseXMLServices(t *testing.T) {
	// Test cases
	testCases := []struct {
		name               string
		xmlContent         string
		expectedServices   int
		expectedAliases    int
		expectedParameters int
		expectedTags       map[string][]string // map[serviceID][]tagNames
		expectError        bool
	}{
		{
			name: "Basic service",
			xmlContent: `<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <service id="app.service1" class="App\Service\Service1" />
</container>`,
			expectedServices:   1,
			expectedAliases:    0,
			expectedParameters: 0,
			expectedTags:       map[string][]string{},
			expectError:        false,
		},
		{
			name: "Service with tags",
			xmlContent: `<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <service id="app.service1" class="App\Service\Service1">
        <tag name="app.tag" />
    </service>
</container>`,
			expectedServices:   1,
			expectedAliases:    0,
			expectedParameters: 0,
			expectedTags:       map[string][]string{"app.service1": {"app.tag"}},
			expectError:        false,
		},
		{
			name: "Service with multiple tags",
			xmlContent: `<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <service id="app.service1" class="App\Service\Service1">
        <tag name="app.tag1" />
        <tag name="app.tag2" />
    </service>
</container>`,
			expectedServices:   1,
			expectedAliases:    0,
			expectedParameters: 0,
			expectedTags:       map[string][]string{"app.service1": {"app.tag1", "app.tag2"}},
			expectError:        false,
		},
		{
			name: "Multiple services",
			xmlContent: `<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <service id="app.service1" class="App\Service\Service1" />
    <service id="app.service2" class="App\Service\Service2" />
</container>`,
			expectedServices:   2,
			expectedAliases:    0,
			expectedParameters: 0,
			expectedTags:       map[string][]string{},
			expectError:        false,
		},
		{
			name: "Services with aliases",
			xmlContent: `<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <service id="app.service1" class="App\Service\Service1" />
    <alias id="app.alias1" service="app.service1" />
</container>`,
			expectedServices:   1,
			expectedAliases:    1,
			expectedParameters: 0,
			expectedTags:       map[string][]string{},
			expectError:        false,
		},
		{
			name: "Complex XML with services, tags, and aliases",
			xmlContent: `<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <service id="app.service1" class="App\Service\Service1">
        <tag name="app.tag1" />
        <tag name="app.tag2" />
    </service>
    <service id="app.service2" class="App\Service\Service2">
        <tag name="app.tag3" />
    </service>
    <alias id="app.alias1" service="app.service1" />
    <alias id="app.alias2" service="app.service2" />
</container>`,
			expectedServices:   2,
			expectedAliases:    2,
			expectedParameters: 0,
			expectedTags: map[string][]string{
				"app.service1": {"app.tag1", "app.tag2"},
				"app.service2": {"app.tag3"},
			},
			expectError: false,
		},
		{
			name: "Symfony namespaced XML with nested services",
			xmlContent: `<?xml version="1.0" encoding="UTF-8" ?>
<container xmlns="http://symfony.com/schema/dic/services"
    xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
    xsi:schemaLocation="http://symfony.com/schema/dic/services
        https://symfony.com/schema/dic/services/services-1.0.xsd">

    <services>
        <!-- Default configuration for services in *this* file -->
        <defaults autowire="true" autoconfigure="true"/>

        <!-- makes classes in src/ available to be used as services -->
        <!-- this creates a service per class whose id is the fully-qualified class name -->
        <prototype namespace="App\" resource="../src/" exclude="../src/{DependencyInjection,Entity,Kernel.php}"/>

        <service id="App\Service\SiteUpdateManager">
            <argument key="$adminEmail">manager@example.com</argument>
        </service>

        <service id="bla">
            <argument type="service" id=""/>
        </service>
    </services>
</container>`,
			expectedServices:   2,
			expectedAliases:    0,
			expectedParameters: 0,
			expectedTags:       map[string][]string{},
			expectError:        false,
		},
		{
			name: "Container with parameters",
			xmlContent: `<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <parameters>
        <parameter key="database_host">localhost</parameter>
        <parameter key="database_port">3306</parameter>
        <parameter key="database_name">app</parameter>
    </parameters>
    <service id="app.service1" class="App\Service\Service1" />
</container>`,
			expectedServices:   1,
			expectedAliases:    0,
			expectedParameters: 3,
			expectedTags:       map[string][]string{},
			expectError:        false,
		},
		{
			name: "Container with parameter and value attribute",
			xmlContent: `<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <parameters>
        <parameter key="app.debug" value="true" />
    </parameters>
</container>`,
			expectedServices:   0,
			expectedAliases:    0,
			expectedParameters: 1,
			expectedTags:       map[string][]string{},
			expectError:        false,
		},
		{
			name: "Container with service reference parameter",
			xmlContent: `<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <parameters>
        <parameter key="app.manager" type="service" id="app.service.manager" />
    </parameters>
    <service id="app.service.manager" class="App\Service\Manager" />
</container>`,
			expectedServices:   1,
			expectedAliases:    0,
			expectedParameters: 1,
			expectedTags:       map[string][]string{},
			expectError:        false,
		},
		// Add test cases for invalid/non-service XML
		{
			name: "Non-service XML - HTML document",
			xmlContent: `<!DOCTYPE html>
<html>
<head>
    <title>Test HTML</title>
</head>
<body>
    <h1>This is not a service file</h1>
    <p>Just a regular HTML document</p>
</body>
</html>`,
			expectedServices:   0,
			expectedAliases:    0,
			expectedParameters: 0,
			expectedTags:       map[string][]string{},
			expectError:        false,
		},
		{
			name: "XML without container tag",
			xmlContent: `<?xml version="1.0" encoding="UTF-8" ?>
<config>
    <parameters>
        <parameter name="test">value</parameter>
    </parameters>
</config>`,
			expectedServices:   0,
			expectedAliases:    0,
			expectedParameters: 0,
			expectedTags:       map[string][]string{},
			expectError:        false,
		},
		{
			name: "Empty XML",
			xmlContent: `<?xml version="1.0" encoding="UTF-8" ?>
<container>
</container>`,
			expectedServices:   0,
			expectedAliases:    0,
			expectedParameters: 0,
			expectedTags:       map[string][]string{},
			expectError:        false,
		},
		{
			name: "Services with missing attributes",
			xmlContent: `<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <service />
    <service id="" />
    <service class="App\Service\MissingId" />
    <alias />
    <alias id="missing.service.reference" />
</container>`,
			expectedServices:   0,
			expectedAliases:    0,
			expectedParameters: 0,
			expectedTags:       map[string][]string{},
			expectError:        false,
		},
		{
			name:               "Malformed XML",
			xmlContent:         `<?xml version="1.0" encoding="UTF-8" ?><container><service></container>`,
			expectedServices:   0,
			expectedAliases:    0,
			expectedParameters: 0,
			expectedTags:       map[string][]string{},
			expectError:        false, // The tolerant parser still returns partial results.
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			services, parameters, err := ParseXMLServices("test.xml", []byte(tc.xmlContent))

			if tc.expectError {
				require.Error(t, err, "Expected ParseXMLServices to fail")
				return
			}

			serviceAmount := 0
			aliasAmount := 0

			for _, service := range services {
				if service.AliasTarget != "" {
					aliasAmount++
				} else {
					serviceAmount++
				}
			}

			require.NoError(t, err, "ParseXMLServices failed")

			// Check service count
			assert.Equal(t, tc.expectedServices, serviceAmount, "Expected %d services, got %d", tc.expectedServices, serviceAmount)

			// Check alias count
			assert.Equal(t, tc.expectedAliases, aliasAmount, "Expected %d aliases, got %d", tc.expectedAliases, aliasAmount)

			// Check parameter count
			assert.Len(t, parameters, tc.expectedParameters, "Expected %d parameters, got %d", tc.expectedParameters, len(parameters))

			// Check tags
			for serviceID, expectedTags := range tc.expectedTags {
				var service *Service
				for i := range services {
					if services[i].ID == serviceID {
						service = &services[i]
						break
					}
				}

				assert.NotNil(t, service, "Service %s not found", serviceID)
				if service == nil {
					continue
				}

				// Check that all expected tags are present
				for _, expectedTag := range expectedTags {
					_, found := service.Tags[expectedTag]
					assert.True(t, found, "Expected tag %s for service %s not found", expectedTag, serviceID)
				}

				// Check that there are no unexpected tags
				assert.Len(t, service.Tags, len(expectedTags), "Expected %d tags for service %s, got %d", len(expectedTags), serviceID, len(service.Tags))
			}
		})
	}
}

func TestParseXMLDeprecatedServiceMetadata(t *testing.T) {
	services, _, err := ParseXMLServices(
		"services.xml",
		[]byte(`<container><services>
<service id="legacy" class="App\Legacy" deprecated="Use App\Modern instead"/>
<service id="flagged" class="App\Flagged" deprecated="true"/>
<service id="active" class="App\Active" deprecated="false"/>
<service id="compiled.legacy" class="App\CompiledLegacy">
  <deprecated package="app/package" version="2.0">The "%service_id%" service is deprecated.</deprecated>
</service>
<service id="compiled.alias" alias="modern.service">
  <deprecated package="app/package" version="2.0">Replace %alias_id% with modern.service.</deprecated>
</service>
<alias id="legacy.alias" service="modern.service" deprecated="Use modern.service"/>
</services></container>`),
	)
	require.NoError(t, err)
	require.Len(t, services, 6)
	assert.True(t, services[0].Deprecated)
	assert.Equal(t, "Use App\\Modern instead", services[0].Deprecation)
	assert.NotZero(t, services[0].DeprecatedRange.Len())
	assert.True(t, services[1].Deprecated)
	assert.Empty(t, services[1].Deprecation)
	assert.False(t, services[2].Deprecated)
	assert.True(t, services[3].Deprecated)
	assert.Equal(
		t,
		`The "%service_id%" service is deprecated.`,
		services[3].Deprecation,
	)
	assert.NotZero(t, services[3].DeprecatedRange.Len())
	assert.True(t, services[4].Deprecated)
	assert.Equal(t, "modern.service", services[4].AliasTarget)
	assert.Empty(t, services[4].Class)
	assert.True(t, services[5].Deprecated)
	assert.Equal(t, "Use modern.service", services[5].Deprecation)
}

func BenchmarkParseXMLServices(b *testing.B) {
	for _, serviceCount := range []int{100, 1000, 5000} {
		b.Run(fmt.Sprintf("services_%d", serviceCount), func(b *testing.B) {
			var source strings.Builder
			source.Grow(serviceCount*80 + 50)
			source.WriteString("<container><services>\n")
			for i := 0; i < serviceCount; i++ {
				fmt.Fprintf(
					&source,
					"<service id=\"app.service.%d\" class=\"App\\Service\\Service%d\"/>\n",
					i,
					i,
				)
			}
			source.WriteString("</services></container>")
			content := []byte(source.String())

			b.ReportAllocs()
			b.SetBytes(int64(len(content)))
			for b.Loop() {
				_, _, _ = ParseXMLServices("services.xml", content)
			}
		})
	}
}
