package parser_test

import (
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	javascriptlexer "github.com/shopware/shopware-lsp/internal/parser/javascript/lexer"
	jsonparser "github.com/shopware/shopware-lsp/internal/parser/json"
	jsonlexer "github.com/shopware/shopware-lsp/internal/parser/json/lexer"
	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phplexer "github.com/shopware/shopware-lsp/internal/parser/php/lexer"
	scssparser "github.com/shopware/shopware-lsp/internal/parser/scss"
	scsslexer "github.com/shopware/shopware-lsp/internal/parser/scss/lexer"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twiglexer "github.com/shopware/shopware-lsp/internal/parser/twig/lexer"
	vueparser "github.com/shopware/shopware-lsp/internal/parser/vue"
	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	xmllexer "github.com/shopware/shopware-lsp/internal/parser/xml/lexer"
	xpathparser "github.com/shopware/shopware-lsp/internal/parser/xpath"
	xpathlexer "github.com/shopware/shopware-lsp/internal/parser/xpath/lexer"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	yamllexer "github.com/shopware/shopware-lsp/internal/parser/yaml/lexer"
)

var performanceRoot *cst.Node
var performanceTokens []parsekit.Token

type nativeBenchmark struct {
	name   string
	source string
	parse  func(string) *cst.Node
	lex    func(string) []parsekit.Token
}

func nativeBenchmarks() []nativeBenchmark {
	return []nativeBenchmark{
		{"JavaScript", strings.Repeat("const service = Shopware.Service('repositoryFactory').create('product');\n", 700), func(s string) *cst.Node { return javascriptparser.Parse(s).Tree.Root }, javascriptlexer.Lex},
		{"JSON", "[" + strings.Repeat(`{"name":"product","enabled":true,"values":[1,2,3,4]},`, 900) + "null]", func(s string) *cst.Node { return jsonparser.Parse(s).Tree.Root }, jsonlexer.Lex},
		{"PHP", "<?php\nclass ParserBenchmark {\n" + strings.Repeat("public function load(): string { return $this->translator->trans('product.detail'); }\n", 600) + "}\n", func(s string) *cst.Node { return phpparser.Parse(s).Tree.Root }, phplexer.Lex},
		{"SCSS", strings.Repeat(".product-card { color: $sw-color-brand-primary; &:hover { display: block; } }\n", 700), func(s string) *cst.Node { return scssparser.Parse(s).Tree.Root }, scsslexer.Lex},
		{"Twig", strings.Repeat(`<div class="product">{{ product.translated.name|default('Fallback') }}</div>`+"\n", 700), func(s string) *cst.Node { return twigparser.Parse(s).Tree.Root }, twiglexer.Lex},
		{"XML", "<container><services>" + strings.Repeat(`<service id="App\Service" class="App\Service"><argument type="service" id="logger"/></service>`, 550) + "</services></container>", func(s string) *cst.Node { return xmlparser.Parse(s).Tree.Root }, xmllexer.Lex},
		{"XPath", strings.Repeat("//product[@active = true()] | ", 1200) + "//product/name", func(s string) *cst.Node { return xpathparser.Parse(s).Tree.Root }, xpathlexer.Lex},
		{"YAML", "services:\n" + strings.Repeat("  App\\Service\\ParserBenchmark:\n    class: App\\Service\\ParserBenchmark\n    arguments: ['@logger', '%kernel.environment%']\n", 450), func(s string) *cst.Node { return yamlparser.Parse(s).Tree.Root }, yamllexer.Lex},
		{"Vue", "<template>\n" + strings.Repeat(`<div class="product">{{ product.translated.name }}</div>`+"\n", 300) + "</template>\n<script setup>\n" + strings.Repeat("const product = repository.get(productId);\n", 300) + "</script>\n<style lang=\"scss\">\n" + strings.Repeat(".product { color: $sw-color-brand-primary; }\n", 300) + "</style>\n", func(s string) *cst.Node { return vueparser.Parse(s).Tree.Root }, nil},
	}
}

func BenchmarkNativeParsers(b *testing.B) {
	for _, test := range nativeBenchmarks() {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(test.source)))
			for b.Loop() {
				performanceRoot = test.parse(test.source)
			}
		})
	}
}

func BenchmarkNativeLexers(b *testing.B) {
	for _, test := range nativeBenchmarks() {
		if test.lex == nil {
			continue
		}
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(test.source)))
			for b.Loop() {
				performanceTokens = test.lex(test.source)
			}
		})
	}
}
