package extension

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseManifestXml(t *testing.T) {
	// Test cases
	testCases := []struct {
		name          string
		xmlContent    string
		expectedName  string
		expectedLabel string
		expectNil     bool
	}{
		{
			name: "Basic manifest",
			xmlContent: `<?xml version="1.0" encoding="UTF-8"?>
<manifest xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
          xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/shopware/trunk/src/Core/Framework/App/Manifest/Schema/manifest-3.0.xsd">
    <meta>
        <name>TestApp</name>
        <label>Test App Label</label>
        <description>A test description</description>
        <author>Test Company Ltd.</author>
        <copyright>(c) by Test Company Ltd.</copyright>
        <version>1.0.0</version>
        <license>MIT</license>
    </meta>
</manifest>`,
			expectedName:  "TestApp",
			expectedLabel: "Test App Label",
			expectNil:     false,
		},
		{
			name: "Missing name",
			xmlContent: `<?xml version="1.0" encoding="UTF-8"?>
<manifest xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
          xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/shopware/trunk/src/Core/Framework/App/Manifest/Schema/manifest-3.0.xsd">
    <meta>
        <label>Test App Label</label>
        <description>A test description</description>
        <author>Test Company Ltd.</author>
        <copyright>(c) by Test Company Ltd.</copyright>
        <version>1.0.0</version>
        <license>MIT</license>
    </meta>
</manifest>`,
			expectedName:  "",
			expectedLabel: "Test App Label",
			expectNil:     false,
		},
		{
			name: "Empty elements",
			xmlContent: `<?xml version="1.0" encoding="UTF-8"?>
<manifest xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
          xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/shopware/trunk/src/Core/Framework/App/Manifest/Schema/manifest-3.0.xsd">
    <meta>
        <name></name>
        <label></label>
        <description></description>
        <author></author>
        <copyright></copyright>
        <version></version>
        <license></license>
    </meta>
</manifest>`,
			expectedName:  "",
			expectedLabel: "",
			expectNil:     false,
		},
		{
			name: "Missing meta node",
			xmlContent: `<?xml version="1.0" encoding="UTF-8"?>
<manifest xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
          xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/shopware/trunk/src/Core/Framework/App/Manifest/Schema/manifest-3.0.xsd">
</manifest>`,
			expectNil: true,
		},
		{
			name: "Not a manifest file",
			xmlContent: `<?xml version="1.0" encoding="UTF-8"?>
<not-manifest>
    <something>
        <name>NotApp</name>
    </something>
</not-manifest>`,
			expectNil: true,
		},
		{
			name:       "Empty XML",
			xmlContent: `<?xml version="1.0" encoding="UTF-8"?>`,
			expectNil:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Parse the manifest file
			manifest, err := ParseManifestXml("test.xml", []byte(tc.xmlContent))
			require.NoError(t, err)

			if tc.expectNil {
				assert.Nil(t, manifest, "Expected manifest to be nil")
				return
			}

			require.NotNil(t, manifest, "Expected manifest to not be nil")
			assert.Equal(t, tc.expectedName, manifest.Name, "Name should match expected value")
			assert.Equal(t, tc.expectedLabel, manifest.Label, "Label should match expected value")
			assert.Equal(t, "test.xml", manifest.Path, "Path should match expected value")
		})
	}
}

func TestParseManifestPermissions(t *testing.T) {
	manifest, err := ParseManifestXml("/app/manifest.xml", []byte(`
<manifest>
    <meta><name>AcmeApp</name></meta>
    <permissions>
        <read>product</read>
        <create>order</create>
    </permissions>
</manifest>`))
	require.NoError(t, err)
	require.NotNil(t, manifest)
	require.Equal(t, []AppPermission{
		{Operation: "read", Entity: "product", Line: 5},
		{Operation: "create", Entity: "order", Line: 6},
	}, manifest.Permissions)
}
