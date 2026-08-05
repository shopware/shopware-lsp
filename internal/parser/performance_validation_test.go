package parser_test

import (
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsonparser "github.com/shopware/shopware-lsp/internal/parser/json"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	scssparser "github.com/shopware/shopware-lsp/internal/parser/scss"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
)

var performanceRoot *cst.Node

func BenchmarkNativeParsers(b *testing.B) {
	cases := []struct {
		name   string
		source string
		parse  func(string) *cst.Node
	}{
		{"JavaScript", strings.Repeat("const service = Shopware.Service('repositoryFactory').create('product');\n", 700), func(s string) *cst.Node { return javascriptparser.Parse(s).Tree.Root }},
		{"JSON", "[" + strings.Repeat(`{"name":"product","enabled":true,"values":[1,2,3,4]},`, 900) + "null]", func(s string) *cst.Node { return jsonparser.Parse(s).Tree.Root }},
		{"PHP", "<?php\nclass ParserBenchmark {\n" + strings.Repeat("public function load(): string { return $this->translator->trans('product.detail'); }\n", 600) + "}\n", func(s string) *cst.Node { return phpparser.Parse(s).Tree.Root }},
		{"SCSS", strings.Repeat(".product-card { color: $sw-color-brand-primary; &:hover { display: block; } }\n", 700), func(s string) *cst.Node { return scssparser.Parse(s).Tree.Root }},
		{"Twig", strings.Repeat(`<div class="product">{{ product.translated.name|default('Fallback') }}</div>`+"\n", 700), func(s string) *cst.Node { return twigparser.Parse(s).Tree.Root }},
		{"XML", "<container><services>" + strings.Repeat(`<service id="App\Service" class="App\Service"><argument type="service" id="logger"/></service>`, 550) + "</services></container>", func(s string) *cst.Node { return xmlparser.Parse(s).Tree.Root }},
		{"YAML", "services:\n" + strings.Repeat("  App\\Service\\ParserBenchmark:\n    class: App\\Service\\ParserBenchmark\n    arguments: ['@logger', '%kernel.environment%']\n", 450), func(s string) *cst.Node { return yamlparser.Parse(s).Tree.Root }},
	}
	for _, test := range cases {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(test.source)))
			for b.Loop() {
				performanceRoot = test.parse(test.source)
			}
		})
	}
}
