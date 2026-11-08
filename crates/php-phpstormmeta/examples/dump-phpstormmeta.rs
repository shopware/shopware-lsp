//! Reads file paths from stdin and prints the extracted contracts (also for
//! every `step`-byte prefix when `step` > 0, as a digest) in the Go oracle's
//! format.

use std::io::{BufRead, BufWriter, Write};

use swlsp_php_semantic::{CallContract, CallValue, ExpectedArgumentContract};
use swlsp_php_types::go_quote as q;

fn cv(value: &CallValue) -> String {
    format!(
        "{}:{}:{}",
        value.kind as u8,
        q(&value.value),
        q(&value.expression)
    )
}

fn cvs(values: &[CallValue]) -> String {
    format!("[{}]", values.iter().map(cv).collect::<Vec<_>>().join(","))
}

fn expected(values: &[ExpectedArgumentContract]) -> String {
    format!(
        "[{}]",
        values
            .iter()
            .map(|value| format!("{}{}", value.argument, cvs(&value.values)))
            .collect::<Vec<_>>()
            .join(",")
    )
}

fn contract(contract: &CallContract) -> String {
    let map: Vec<String> = contract
        .return_contract
        .map
        .iter()
        .map(|entry| format!("{}=>{}", cv(&entry.key), cv(&entry.result)))
        .collect();
    format!(
        "C {}:{}:{} ret={}:{}:[{}] exp={} rv={} exit={}:{}",
        contract.target.kind as u8,
        q(&contract.target.name),
        q(&contract.target.class),
        contract.return_contract.kind as u8,
        contract.return_contract.argument,
        map.join(","),
        expected(&contract.expected_arguments),
        cvs(&contract.expected_return_values),
        contract.exit_point,
        expected(&contract.exit_arguments)
    )
}

fn main() -> std::io::Result<()> {
    let step: usize = std::env::args()
        .nth(1)
        .and_then(|value| value.parse().ok())
        .unwrap_or(0);
    let mut out = BufWriter::new(std::io::stdout().lock());
    for path in std::io::stdin().lock().lines() {
        let path = path?;
        let source = std::fs::read_to_string(&path)?;
        let mut cuts = vec![source.len()];
        if step > 0 {
            cuts.extend(
                (0..source.len())
                    .step_by(step)
                    .filter(|cut| source.is_char_boundary(*cut)),
            );
        }
        for cut in cuts {
            let contracts = swlsp_php_phpstormmeta::parse(&swlsp_parser_php::root(&source[..cut]));
            let mut text = String::new();
            for value in &contracts {
                text.push_str(&contract(value));
                text.push('\n');
            }
            if cut == source.len() {
                write!(out, "F {} {} {}\n{text}", q(&path), cut, contracts.len())?;
            } else {
                // Prefixes print an FNV-1a digest to keep the output small.
                let hash = text
                    .bytes()
                    .fold(14_695_981_039_346_656_037u64, |hash, byte| {
                        (hash ^ u64::from(byte)).wrapping_mul(1_099_511_628_211)
                    });
                writeln!(out, "F {} {} {} {hash:x}", q(&path), cut, contracts.len())?;
            }
        }
    }
    out.flush()
}
