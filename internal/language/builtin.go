package language

import (
	"sync"

	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsonparser "github.com/shopware/shopware-lsp/internal/parser/json"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	scssparser "github.com/shopware/shopware-lsp/internal/parser/scss"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	vueparser "github.com/shopware/shopware-lsp/internal/parser/vue"
	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
)

var (
	defaultRegistryOnce sync.Once
	defaultRegistry     *Registry
)

func NewBuiltinRegistry() (*Registry, error) {
	return NewRegistry(
		Definition{
			ID:         JavaScript,
			Extensions: []string{".js", ".ts"},
			Parse: func(source string) ParseResult {
				result := javascriptparser.Parse(source)
				return ParseResult{Tree: result.Tree, Errors: result.Errors}
			},
		},
		Definition{
			ID:         JSON,
			Extensions: []string{".json"},
			Parse: func(source string) ParseResult {
				result := jsonparser.Parse(source)
				return ParseResult{Tree: result.Tree, Errors: result.Errors}
			},
		},
		Definition{
			ID:         PHP,
			Extensions: []string{".php"},
			Parse: func(source string) ParseResult {
				result := phpparser.Parse(source)
				return ParseResult{Tree: result.Tree, Errors: result.Errors}
			},
		},
		Definition{
			ID:         SCSS,
			Extensions: []string{".scss"},
			Parse: func(source string) ParseResult {
				result := scssparser.Parse(source)
				return ParseResult{Tree: result.Tree, Errors: result.Errors}
			},
		},
		Definition{
			ID:         Twig,
			Extensions: []string{".twig", ".html"},
			Parse: func(source string) ParseResult {
				result := twigparser.Parse(source)
				return ParseResult{Tree: result.Tree, Errors: result.Errors}
			},
		},
		Definition{
			ID:         Vue,
			Extensions: []string{".vue"},
			Parse: func(source string) ParseResult {
				result := vueparser.Parse(source)
				return ParseResult{Tree: result.Tree, Errors: result.Errors}
			},
		},
		Definition{
			ID:         XML,
			Extensions: []string{".xml", ".xlf", ".xliff"},
			Parse: func(source string) ParseResult {
				result := xmlparser.Parse(source)
				return ParseResult{Tree: result.Tree, Errors: result.Errors}
			},
		},
		Definition{
			ID:         YAML,
			Extensions: []string{".yaml", ".yml"},
			Parse: func(source string) ParseResult {
				result := yamlparser.Parse(source)
				return ParseResult{Tree: result.Tree, Errors: result.Errors}
			},
		},
	)
}

// DefaultRegistry returns the immutable built-in registry used by compatibility
// constructors. Application code should prefer explicitly passing a registry.
func DefaultRegistry() *Registry {
	defaultRegistryOnce.Do(func() {
		var err error
		defaultRegistry, err = NewBuiltinRegistry()
		if err != nil {
			panic(err)
		}
	})
	return defaultRegistry
}
