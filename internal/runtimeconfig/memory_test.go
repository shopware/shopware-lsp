package runtimeconfig

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyMemoryPolicyUsesBalancedDefault(t *testing.T) {
	appliedPercent := 0
	policy := applyMemoryPolicy(
		func(string) (string, bool) {
			return "", false
		},
		func(percent int) int {
			appliedPercent = percent
			return 100
		},
	)

	require.Equal(t, DefaultGCPercent, appliedPercent)
	require.Equal(t, MemoryPolicy{
		GCPercent: DefaultGCPercent,
		Applied:   true,
	}, policy)
}

func TestApplyMemoryPolicyPreservesRuntimeEnvironment(t *testing.T) {
	for _, variable := range []string{"GOGC", "GOMEMLIMIT"} {
		t.Run(variable, func(t *testing.T) {
			setCalls := 0
			policy := applyMemoryPolicy(
				func(name string) (string, bool) {
					if name == variable {
						return "", true
					}
					return "", false
				},
				func(int) int {
					setCalls++
					return 100
				},
			)

			require.Zero(t, setCalls)
			require.Equal(t, MemoryPolicy{}, policy)
		})
	}
}
