use super::*;
use std::sync::Arc;
use swlsp_lsp::{Context, FoldingRangeProvider, FoldingRangeRequest, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_lsp_protocol::{
    FOLDING_RANGE_KIND_COMMENT as COMMENT, FOLDING_RANGE_KIND_IMPORTS as IMPORTS,
};

fn request(uri: &str, source: &str, version: i32) -> FoldingRangeRequest {
    FoldingRangeRequest {
        params: protocol::FoldingRangeParams {
            text_document: protocol::TextDocumentIdentifier {
                uri: uri.to_owned(),
            },
            ..Default::default()
        },
        document: Arc::new(TextDocument::new(uri, source, version)),
    }
}

fn fold(start_line: u32, end_line: u32, kind: &str) -> protocol::FoldingRange {
    protocol::FoldingRange {
        start_line,
        end_line,
        kind: kind.to_owned(),
        ..Default::default()
    }
}

fn php_folds(source: &str) -> Vec<protocol::FoldingRange> {
    PhpFoldingProvider::new()
        .get_folding_ranges(
            &Context::background(),
            &request("file:///live.php", source, 7),
        )
        .expect("folding ranges")
}

#[track_caller]
fn assert_contains(ranges: &[protocol::FoldingRange], start: u32, end: u32, kind: &str) {
    let expected = fold(start, end, kind);
    assert!(
        ranges.contains(&expected),
        "expected {start}..{end} kind {kind:?} in {ranges:#?}"
    );
}

#[test]
fn php_folding_uses_current_syntax() {
    let source = r"<?php
use First\Thing;
use Second\Thing as Other;
/**
 * Documentation 😀
 */
class Example {
    public function run() {
        $items = [
            1,
            2,
        ];
        if ($items):
            echo 'yes';
        endif;
    }
}
namespace Separate;
use Third\Thing;
use Fourth\Thing as Last;
";
    let result = php_folds(source);
    assert_contains(&result, 1, 2, IMPORTS);
    assert_contains(&result, 3, 5, COMMENT);
    assert_contains(&result, 6, 15, "");
    assert_contains(&result, 7, 14, "");
    assert_contains(&result, 8, 10, "");
    assert_contains(&result, 12, 13, "");
    assert_contains(&result, 18, 19, IMPORTS);
    for fold in &result {
        assert!(fold.start_character.is_none());
        assert!(fold.end_character.is_none());
        assert!(fold.end_line > fold.start_line);
    }
}

#[test]
fn php_folding_incomplete_blocks_comments_and_strings() {
    let source = "<?php\r\n// one\r\n// two\r\n\r\n// separate\r\nclass Live {\r\n function unfinished() {\r\n  echo 1;\r\n  echo 2;";
    let result = php_folds(source);
    assert_contains(&result, 1, 2, COMMENT);
    assert!(!result.contains(&fold(1, 4, COMMENT)));
    assert_contains(&result, 5, 8, "");
    assert_contains(&result, 6, 8, "");
    let strings = php_folds("<?php\n$value = <<<'TEXT'\nfirst\nsecond\nTEXT;\n");
    assert_contains(&strings, 1, 4, "");
    assert!(php_folds("<?php class Tiny {}").is_empty());
}

#[test]
fn php_folding_unsupported_and_cancellation() {
    let provider = PhpFoldingProvider::new();
    let result = provider
        .get_folding_ranges(
            &Context::background(),
            &request("file:///test.js", "{\n x\n}", 1),
        )
        .expect("folding ranges");
    assert!(result.is_empty());

    let ctx = Context::background();
    ctx.token().cancel();
    let error = provider
        .get_folding_ranges(&ctx, &request("file:///live.php", "<?php", 1))
        .expect_err("cancelled");
    assert!(swlsp_lsp::is_cancellation(error.as_ref()));
}

#[test]
fn php_folding_switch_match_and_property_hooks() {
    let source = "<?php
class Example {
 public string $label {
  get {
   return 'label';
  }
 }
 function run($value) {
  switch ($value) {
   case 1:
    return match ($value) {
     1 => 'one',
     default => 'other',
    };
  }
 }
}";
    let result = php_folds(source);
    assert_contains(&result, 2, 5, "");
    assert_contains(&result, 3, 4, "");
    assert_contains(&result, 8, 13, "");
    assert_contains(&result, 10, 12, "");
}

#[test]
fn php_folding_results_are_sorted_and_deduplicated() {
    let result =
        php_folds("<?php\nclass A {\n function a() {\n  if ($x):\n   echo 1;\n  endif;\n }\n}\n");
    let mut sorted = result.clone();
    sorted.sort_by(|left, right| {
        (left.start_line, left.end_line, &left.kind).cmp(&(
            right.start_line,
            right.end_line,
            &right.kind,
        ))
    });
    sorted.dedup();
    assert_eq!(result, sorted);
}
