// Package yamlparser provides the lossless, error-tolerant YAML parser used by
// the language server.
package yamlparser

import "github.com/shopware/shopware-lsp/internal/parser/yaml/parser"

func Parse(source string) parser.Result {
	return parser.Parse(source)
}
