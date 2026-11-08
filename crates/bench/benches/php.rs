use criterion::{Criterion, criterion_group, criterion_main};
use swlsp_bench::PHP_DOCUMENT;
use swlsp_php_binder::Binder;
use swlsp_php_inference::Analyzer;
use swlsp_php_semantic::Snapshot;

const PATH: &str = "testdata/01.php";

fn php(c: &mut Criterion) {
    c.bench_function("PureParsing", |b| {
        b.iter(|| swlsp_parser_php::parse(std::hint::black_box(PHP_DOCUMENT)))
    });
    let root = swlsp_parser_php::parse(PHP_DOCUMENT).root();
    c.bench_function("SemanticBinding", |b| {
        b.iter(|| Binder::new().bind_node(PATH, 1, Some(&root), PHP_DOCUMENT))
    });
    let document = Binder::new().bind_node(PATH, 1, Some(&root), PHP_DOCUMENT);
    let snapshot = Snapshot::new(1, std::slice::from_ref(&document));
    let analyzer = Analyzer::new(Some(snapshot), Vec::new());
    c.bench_function("SemanticAnalysis", |b| {
        b.iter(|| analyzer.analyze_owned_with_source(document.clone(), &root, PHP_DOCUMENT))
    });
}

criterion_group!(benches, php);
criterion_main!(benches);
