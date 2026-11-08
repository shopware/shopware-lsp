//! Benchmark inputs shared by `benches/`. The cases mirror the Go oracle's
//! CodSpeed suite (`internal/parser/performance_validation_test.go`,
//! `internal/php/benchmark_test.go`) so both implementations track the same
//! production paths.

/// Synthetic parser inputs, identical to Go `nativeBenchmarks`.
pub fn parser_inputs() -> Vec<(&'static str, String)> {
    vec![
        ("JavaScript", "const service = Shopware.Service('repositoryFactory').create('product');\n".repeat(700)),
        ("JSON", format!("[{}null]", r#"{"name":"product","enabled":true,"values":[1,2,3,4]},"#.repeat(900))),
        ("PHP", format!("<?php\nclass ParserBenchmark {{\n{}}}\n", "public function load(): string { return $this->translator->trans('product.detail'); }\n".repeat(600))),
        ("SCSS", ".product-card { color: $sw-color-brand-primary; &:hover { display: block; } }\n".repeat(700)),
        ("Twig", "<div class=\"product\">{{ product.translated.name|default('Fallback') }}</div>\n".repeat(700)),
        ("XML", format!("<container><services>{}</services></container>", r#"<service id="App\Service" class="App\Service"><argument type="service" id="logger"/></service>"#.repeat(550))),
        ("XPath", format!("{}//product/name", "//product[@active = true()] | ".repeat(1200))),
        ("YAML", format!("services:\n{}", "  App\\Service\\ParserBenchmark:\n    class: App\\Service\\ParserBenchmark\n    arguments: ['@logger', '%kernel.environment%']\n".repeat(450))),
        ("Vue", format!(
            "<template>\n{}</template>\n<script setup>\n{}</script>\n<style lang=\"scss\">\n{}</style>\n",
            "<div class=\"product\">{{ product.translated.name }}</div>\n".repeat(300),
            "const product = repository.get(productId);\n".repeat(300),
            ".product { color: $sw-color-brand-primary; }\n".repeat(300),
        )),
    ]
}

/// The PHP document of Go `BenchmarkSemanticBinding`/`BenchmarkSemanticAnalysis`.
pub const PHP_DOCUMENT: &str = include_str!("../data/01.php");
