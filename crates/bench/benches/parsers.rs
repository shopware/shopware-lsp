use criterion::{Criterion, Throughput, criterion_group, criterion_main};
use swlsp_bench::parser_inputs;

fn parse(language: &str, source: &str) -> usize {
    let parse = match language {
        "JavaScript" => swlsp_parser_js::parse(source),
        "JSON" => swlsp_parser_json::parse(source),
        "PHP" => swlsp_parser_php::parse(source),
        "SCSS" => swlsp_parser_scss::parse(source),
        "Twig" => swlsp_parser_twig::parse(source),
        "XML" => swlsp_parser_xml::parse(source),
        "XPath" => swlsp_parser_xpath::parse(source),
        "YAML" => swlsp_parser_yaml::parse(source),
        "Vue" => swlsp_parser_vue::parse(source),
        other => unreachable!("unknown language {other}"),
    };
    usize::from(parse.root().text_range().len())
}

fn native_parsers(c: &mut Criterion) {
    let mut group = c.benchmark_group("NativeParsers");
    for (language, source) in parser_inputs() {
        assert_eq!(
            parse(language, &source),
            source.len(),
            "{language} parse is lossless"
        );
        group.throughput(Throughput::Bytes(source.len() as u64));
        group.bench_function(language, |b| {
            b.iter(|| parse(language, std::hint::black_box(&source)))
        });
    }
    group.finish();
}

criterion_group!(benches, native_parsers);
criterion_main!(benches);
