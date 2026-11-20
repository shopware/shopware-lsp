//! Times the formatter on every PHP file below the directories given as
//! arguments: a development check, read-only. Each file is formatted as it
//! is and once more as formatted, the case of format on save.

use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

use swlsp_php_format::{Options, try_format};

fn files(directory: &Path, found: &mut Vec<PathBuf>) {
    let Ok(entries) = std::fs::read_dir(directory) else {
        return;
    };
    for entry in entries.flatten() {
        let path = entry.path();
        if path.is_dir() {
            if !path.is_symlink() {
                files(&path, found);
            }
        } else if path.extension().is_some_and(|extension| extension == "php") {
            found.push(path);
        }
    }
}

fn percentile(sorted: &[Duration], percent: usize) -> Duration {
    sorted[(sorted.len() * percent / 100).min(sorted.len() - 1)]
}

fn report(name: &str, mut times: Vec<Duration>, bytes: usize) {
    let total: Duration = times.iter().sum();
    times.sort();
    println!(
        "{name}: total {:.2?}, {:.1} MB/s, median {:.2?}, p90 {:.2?}, p99 {:.2?}, max {:.2?}",
        total,
        bytes as f64 / 1e6 / total.as_secs_f64(),
        percentile(&times, 50),
        percentile(&times, 90),
        percentile(&times, 99),
        times[times.len() - 1],
    );
}

fn main() {
    let options = Options::default();
    let mut found = Vec::new();
    for argument in std::env::args().skip(1) {
        files(Path::new(&argument), &mut found);
    }
    found.sort();
    let (mut parse, mut first, mut second) = (Vec::new(), Vec::new(), Vec::new());
    let mut slowest = Vec::new();
    let (mut bytes, mut formatted_bytes) = (0, 0);
    for path in &found {
        let Ok(source) = std::fs::read_to_string(path) else {
            continue;
        };
        let start = Instant::now();
        let tree = swlsp_parser_php::parse(&source);
        let parsed = start.elapsed();
        drop(tree);
        let start = Instant::now();
        let Ok(formatted) = try_format(&source, options) else {
            continue;
        };
        let took = start.elapsed();
        let start = Instant::now();
        let again = try_format(&formatted, options);
        let steady = start.elapsed();
        drop(again);
        bytes += source.len();
        formatted_bytes += formatted.len();
        parse.push(parsed);
        first.push(took);
        second.push(steady);
        slowest.push((took.max(steady), took, steady, source.len(), path.clone()));
    }
    println!("{} files, {:.1} MB", first.len(), bytes as f64 / 1e6);
    report("parse only      ", parse, bytes);
    report("format source   ", first, bytes);
    report("format formatted", second, formatted_bytes);
    slowest.sort_by_key(|entry| std::cmp::Reverse(entry.0));
    for (_, took, steady, size, path) in slowest.iter().take(10) {
        println!(
            "{took:>10.2?} {steady:>10.2?} {:>7.1} kB  {}",
            *size as f64 / 1e3,
            path.display()
        );
    }
}
