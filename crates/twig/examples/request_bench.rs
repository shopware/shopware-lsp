//! Release-mode benchmark for request-time Twig scans, probing definitions
//! the way the real-world tests do (up to 250 word-start positions per file,
//! every scan called once per probe).
//!
//! `cargo run --release -p swlsp-twig --example request_bench -- <file-list>`

use std::collections::BTreeMap;
use std::time::{Duration, Instant};

fn is_word(b: u8) -> bool {
    b == b'_' || b == b'$' || b == b'@' || b.is_ascii_alphanumeric() || b >= 0x80
}

fn positions(source: &str) -> Vec<u32> {
    let bytes = source.as_bytes();
    let mut all = Vec::new();
    for offset in 1..bytes.len() {
        let previous = bytes[offset - 1];
        let keep = previous == b'\''
            || previous == b'"'
            || (offset >= 2 && is_word(previous) && !is_word(bytes[offset - 2]));
        if keep && source.is_char_boundary(offset) {
            all.push(offset as u32);
        }
    }
    const LIMIT: usize = 250;
    if all.len() <= LIMIT {
        return all;
    }
    (0..LIMIT).map(|i| all[i * all.len() / LIMIT]).collect()
}

fn main() {
    let list = std::env::args().nth(1).expect("file list");
    let listing = std::fs::read_to_string(list).expect("read list");
    let mut timing: BTreeMap<&str, Duration> = BTreeMap::new();
    let mut hits: BTreeMap<&str, usize> = BTreeMap::new();
    let mut time = |name: &'static str, hit: bool, started: Instant| {
        *timing.entry(name).or_default() += started.elapsed();
        *hits.entry(name).or_default() += usize::from(hit);
    };
    let mut probes = 0;
    for path in listing.lines() {
        let Ok(source) = std::fs::read_to_string(path) else {
            continue;
        };
        if path.ends_with(".twig") {
            let root = swlsp_parser_twig::parse(&source).tree.root();
            for offset in positions(&source) {
                probes += 1;
                let t = Instant::now();
                let hit = swlsp_twig::see_reference_at(&root, offset).is_some();
                time("see_reference_at", hit, t);
                let t = Instant::now();
                let hit = swlsp_twig::guard_reference_at(source.as_bytes(), offset).is_some();
                time("guard_reference_at", hit, t);
                let t = Instant::now();
                let hit =
                    swlsp_twig::types_tag_class_reference_at(source.as_bytes(), offset).is_some();
                time("types_tag_class_reference_at", hit, t);
                let t = Instant::now();
                let hit = swlsp_twig::test_expression_at(&root, offset).is_some();
                time("test_expression_at", hit, t);
                let t = Instant::now();
                let hit = swlsp_twig::twig_tag_usages(source.as_bytes())
                    .iter()
                    .any(|usage| usage.range.contains_inclusive(offset.into()));
                time("twig_tag_usages", hit, t);
            }
        } else if path.ends_with(".php") {
            let root = swlsp_parser_php::parse(&source).tree.root();
            for offset in positions(&source) {
                probes += 1;
                let t = Instant::now();
                let references = swlsp_twig::php_template_references("", &root);
                let hit = swlsp_twig::template_reference_at(&references, offset).is_some();
                time("php_template_references", hit, t);
            }
        }
    }
    eprintln!("probes {probes}");
    for (name, duration) in timing {
        eprintln!(
            "{name:32} {:8.3}s hits {}",
            duration.as_secs_f64(),
            hits[name]
        );
    }
}
