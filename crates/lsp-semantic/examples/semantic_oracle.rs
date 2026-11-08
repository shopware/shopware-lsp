//! Differential driver: indexes PHP and Administration files, then prints
//! every provider's raw tokens per file in the format of the Go oracle.
//!
//! Usage: `semantic_oracle <cache> <php-index-list> <php-query-list>
//! <admin-index-list> <admin-query-list> <twig-list>`

use std::io::{BufWriter, Write};
use std::sync::Arc;

use swlsp_admin::AdminComponentIndexer;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, SemanticTokensProvider, SemanticTokensRequest, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_lsp_semantic::{
    AdminMarkupProvider, EmbeddedCssProvider, EmbeddedJsonProvider, EmbeddedLanguageProvider,
    EmbeddedXPathProvider, TwigUxToolkitProvider,
};
use swlsp_php::PhpIndex;

fn list(path: &str) -> Vec<String> {
    std::fs::read_to_string(path)
        .expect("read list")
        .lines()
        .filter(|line| !line.is_empty())
        .map(str::to_owned)
        .collect()
}

fn read(path: &str) -> Option<String> {
    String::from_utf8(std::fs::read(path).expect("read file")).ok()
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let cache = &args[1];
    let php = Arc::new(PhpIndex::open(format!("{cache}/php"), None).expect("php index"));
    for path in list(&args[2]) {
        if let Some(source) = read(&path) {
            let _ = php.index(&ParsedFile::new(path, source.into_bytes()));
        }
    }
    let admin =
        Arc::new(AdminComponentIndexer::new(&format!("{cache}/admin"), None).expect("admin index"));
    for path in list(&args[4]) {
        if let Some(source) = read(&path) {
            let _ = admin.index(&ParsedFile::new(path, source.into_bytes()));
        }
    }
    let stdout = std::io::stdout();
    let mut out = BufWriter::with_capacity(1 << 20, stdout.lock());
    let ctx = Context::background();
    let mut run = |list_path: &str, providers: &[(&str, &dyn SemanticTokensProvider)]| {
        for path in list(list_path) {
            let Some(source) = read(&path) else {
                continue;
            };
            let uri = swlsp_uriutil::file_uri(&path);
            let request = SemanticTokensRequest {
                params: protocol::SemanticTokensParams {
                    text_document: protocol::TextDocumentIdentifier { uri: uri.clone() },
                    ..Default::default()
                },
                document: Arc::new(TextDocument::new(&uri, &source, 1)),
            };
            for (name, provider) in providers {
                match provider.get_semantic_tokens(&ctx, &request) {
                    Err(_) => writeln!(out, "{name} {path} ERROR").unwrap(),
                    Ok(tokens) if tokens.is_empty() => {}
                    Ok(tokens) => {
                        write!(out, "{name} {path}").unwrap();
                        for token in tokens {
                            write!(
                                out,
                                " {}:{}:{}",
                                u32::from(token.range.start()),
                                u32::from(token.range.end()),
                                token.token_type
                            )
                            .unwrap();
                        }
                        writeln!(out).unwrap();
                    }
                }
            }
        }
    };
    run(
        &args[3],
        &[
            (
                "language",
                &EmbeddedLanguageProvider::new(Some((*php).clone())),
            ),
            ("json", &EmbeddedJsonProvider::new(Some((*php).clone()))),
            ("css", &EmbeddedCssProvider::new(Some((*php).clone()))),
            ("xpath", &EmbeddedXPathProvider::new(Some((*php).clone()))),
        ],
    );
    run(
        &args[5],
        &[("admin", &AdminMarkupProvider::new(admin.clone()))],
    );
    run(&args[6], &[("twigux", &TwigUxToolkitProvider::new())]);
    drop(out);
    php.close().expect("close php");
    admin.close().expect("close admin");
}
