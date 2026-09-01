package lsp

import (
	"os"
	"strings"
)

func providerPerformanceTraceEnabled() bool {
	value := strings.TrimSpace(strings.ToLower(
		os.Getenv("SHOPWARE_LSP_TRACE_PROVIDERS"),
	))
	return value != "" && value != "0" && value != "false" && value != "off"
}
