//! Shared driver for the per-language `dump-cst-<lang>` differential-test
//! binaries. Reads paths from stdin and prints `=== <path>` followed by the
//! debug tree and parse errors, matching `go run ./cmd/dump_cst`.

use std::io::{BufRead, BufWriter, Write};

use crate::Parse;

pub fn run(parse: fn(&str) -> Parse) {
    let stdout = std::io::stdout();
    let mut out = BufWriter::with_capacity(1 << 20, stdout.lock());
    for path in std::io::stdin().lock().lines() {
        let path = path.expect("read stdin");
        let Ok(bytes) = std::fs::read(&path) else {
            let _ = writeln!(out, "=== {path}\nREAD ERROR");
            continue;
        };
        let source = String::from_utf8_lossy(&bytes);
        let parse = parse(&source);
        let _ = writeln!(out, "=== {path}\n{}", parse.debug_dump());
    }
}
