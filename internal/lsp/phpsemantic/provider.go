// Package phpsemantic exposes the PHP semantic graph through LSP features.
package phpsemantic

import (
	"github.com/shopware/shopware-lsp/internal/php"
)

type Provider struct {
	index *php.PHPIndex
}

func New(index *php.PHPIndex) *Provider {
	return &Provider{index: index}
}

func (p *Provider) GetTriggerCharacters() []string {
	return []string{">", ":", "$", "\\"}
}
