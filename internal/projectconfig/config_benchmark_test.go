package projectconfig

import (
	"fmt"
	"testing"
)

func BenchmarkApplyDiagnostics(b *testing.B) {
	disabled := false
	configuration := &DiagnosticsConfig{}
	for index := range 24 {
		configuration.Overrides = append(configuration.Overrides, DiagnosticOverride{
			Files: []string{fmt.Sprintf("custom/plugins/Plugin%d/src/**", index)},
			Rules: map[string]Severity{
				"php.arguments": SeverityOff,
			},
		})
	}
	configuration.Overrides = append(configuration.Overrides, DiagnosticOverride{
		Files:   []string{"custom/plugins/FroshTools/src/Generated/**"},
		Enabled: &disabled,
	})
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		policy := DefaultDiagnosticPolicy()
		ApplyDiagnostics(
			&policy,
			configuration,
			"custom/plugins/FroshTools/src/Generated/Example.php",
		)
	}
}
