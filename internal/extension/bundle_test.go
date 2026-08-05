package extension

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidBundleIndexPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path  string
		valid bool
	}{
		{path: "/project/src/Acme/AcmeBundle.php", valid: true},
		{path: "/project/tests/FixtureBundle.php", valid: false},
		{path: "/project/FiXtUrEs/FixtureBundle.php", valid: false},
		{path: `C:\project\_Fixtures\FixtureBundle.php`, valid: false},
		{path: "/project/src/.HiddenBundle.php", valid: false},
		{path: "/project/src/ProductTest.php", valid: false},
		{path: "/project/src/TestBundle.php", valid: true},
		{path: "/project/src/TestPlugin.php", valid: true},
	}
	for _, test := range tests {
		require.Equal(t, test.valid, isValidForIndex(test.path), test.path)
	}
}

func TestValidBundleIndexPathDoesNotAllocateForCleanPath(t *testing.T) {
	var valid bool
	allocations := testing.AllocsPerRun(100, func() {
		valid = isValidForIndex("/project/src/Acme/AcmeBundle.php")
	})
	require.Zero(t, allocations)
	require.True(t, valid)
}
