//! Regenerates the embedded PHP runtime catalog from a phpstorm-stubs
//! checkout (port of `cmd/generate_phpstorm_stubs`).

use std::path::PathBuf;
use std::process::ExitCode;

use swlsp_php_stubs_generate::{EmptyListPolicy, build, encode, load_lock, verify_source};

const USAGE: &str = "usage: generate-phpstorm-stubs [-source DIR] [-lock FILE] [-output FILE] \
[-skip-revision-check] [-go-head]

  -source DIR            path to a phpstorm-stubs checkout (default ../phpstorm-stubs)
  -lock FILE             pinned source manifest (default internal/php/stubs/phpstorm-stubs.lock.json)
  -output FILE           generated catalog path (default crates/php-stubs/data/phpstorm-stubs.msgpack)
  -skip-revision-check   allow a source tree without the locked Git revision
  -go-head               write empty lists like the current Go generator instead of
                         the committed artifact";

struct Options {
    source: PathBuf,
    lock: PathBuf,
    output: PathBuf,
    skip_revision_check: bool,
    policy: EmptyListPolicy,
}

fn parse_options() -> Result<Options, String> {
    let mut options = Options {
        source: PathBuf::from("../phpstorm-stubs"),
        lock: PathBuf::from("internal/php/stubs/phpstorm-stubs.lock.json"),
        output: PathBuf::from("crates/php-stubs/data/phpstorm-stubs.msgpack"),
        skip_revision_check: false,
        policy: EmptyListPolicy::Committed,
    };
    let mut arguments = std::env::args().skip(1);
    while let Some(argument) = arguments.next() {
        // Accept Go flag syntax: -name, --name, -name=value.
        let trimmed = argument.trim_start_matches('-');
        let (name, inline) = match trimmed.split_once('=') {
            Some((name, value)) => (name, Some(value.to_owned())),
            None => (trimmed, None),
        };
        let mut value = |name: &str| {
            inline
                .clone()
                .or_else(|| arguments.next())
                .ok_or_else(|| format!("flag needs an argument: -{name}"))
        };
        match name {
            "source" => options.source = value(name)?.into(),
            "lock" => options.lock = value(name)?.into(),
            "output" => options.output = value(name)?.into(),
            "skip-revision-check" => options.skip_revision_check = true,
            "go-head" => options.policy = EmptyListPolicy::GoHead,
            "h" | "help" => return Err(USAGE.to_owned()),
            _ => {
                return Err(format!(
                    "flag provided but not defined: {argument}\n{USAGE}"
                ));
            }
        }
    }
    Ok(options)
}

fn run() -> Result<(), String> {
    let options = parse_options()?;
    let lock = load_lock(&options.lock).map_err(|err| err.to_string())?;
    if !options.skip_revision_check {
        verify_source(&options.source, &lock).map_err(|err| err.to_string())?;
    }
    let (generated, stats) =
        build(&options.source, &lock, options.policy).map_err(|err| err.to_string())?;
    let data = encode(&generated, options.policy);
    if let Some(parent) = options.output.parent()
        && !parent.as_os_str().is_empty()
    {
        std::fs::create_dir_all(parent)
            .map_err(|err| format!("create generated PHP stub directory: {err}"))?;
    }
    std::fs::write(&options.output, &data)
        .map_err(|err| format!("write generated PHP stub catalog: {err}"))?;
    println!(
        "generated {} records from {} symbols in {} files ({} source bytes, {} catalog bytes) at {}",
        stats.records,
        stats.parsed_symbols,
        stats.files,
        stats.source_bytes,
        data.len(),
        options.output.display()
    );
    Ok(())
}

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(message) => {
            eprintln!("{message}");
            ExitCode::FAILURE
        }
    }
}
