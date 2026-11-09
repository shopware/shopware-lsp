use std::sync::Arc;
use std::sync::atomic::{AtomicU64, AtomicUsize, Ordering};

use swlsp_lsp::{BoxError, CancellationToken, Context, Problem, TextDocument};

use crate::test_support::document;
use crate::{CodeGroup, ProblemAnalyzer, SharedAnalysis};

/// Reports `a` on the first token and `b` on the root, counting runs.
#[derive(Default)]
struct Counting {
    runs: AtomicUsize,
    revision: AtomicU64,
}

impl ProblemAnalyzer for Counting {
    fn analyze(&self, _: &Context, document: &Arc<TextDocument>) -> Result<Vec<Problem>, BoxError> {
        self.runs.fetch_add(1, Ordering::SeqCst);
        let root = document.root().unwrap();
        let token = root.first_token().unwrap();
        Ok(vec![
            Problem {
                id: "a".into(),
                range: token.text_range(),
                element: Some(token.into()),
                ..Problem::default()
            },
            Problem {
                id: "b".into(),
                range: root.text_range(),
                ..Problem::default()
            },
        ])
    }

    fn input_revision(&self, _: &TextDocument) -> Option<u64> {
        Some(self.revision.load(Ordering::SeqCst))
    }
}

#[test]
fn groups_share_one_run_per_document_and_revision() {
    let shared = SharedAnalysis::new(Counting::default());
    let a = CodeGroup::new(shared.clone(), &["a"]);
    let b = CodeGroup::new(shared.clone(), &["b"]);
    let ctx = Context::background();
    let runs = || shared.analyzer().runs.load(Ordering::SeqCst);
    let first = document("file:///a.php", "<?php echo 1;");

    let problems = a.analyze(&ctx, &first).unwrap();
    assert_eq!(problems.len(), 1);
    assert_eq!(problems[0].id.as_str(), "a");
    // The element survives the memo: the same token, not the smallest
    // element covering its range.
    let element = problems[0].element.as_ref().unwrap();
    assert!(element.as_token().is_some());
    assert_eq!(b.analyze(&ctx, &first).unwrap()[0].id.as_str(), "b");
    assert_eq!(runs(), 1);

    // A new snapshot of the document runs again.
    let second = document("file:///a.php", "<?php echo 2;");
    a.analyze(&ctx, &second).unwrap();
    assert_eq!(runs(), 2);
    // So does a changed input revision.
    shared.analyzer().revision.store(1, Ordering::SeqCst);
    b.analyze(&ctx, &second).unwrap();
    assert_eq!(runs(), 3);

    // A cancelled run is not memoized.
    let token = CancellationToken::new();
    token.cancel();
    let third = document("file:///a.php", "<?php echo 3;");
    a.analyze(&Context::with_token(token), &third).unwrap();
    a.analyze(&ctx, &third).unwrap();
    assert_eq!(runs(), 5);
}
