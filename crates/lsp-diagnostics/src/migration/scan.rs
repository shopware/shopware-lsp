//! Per-analysis memo of whole-tree queries shared by the migration rules.
//!
//! Every rule used to rebuild the name resolver and re-walk the tree for
//! classes, member calls and the namespace. `ScanScope` keeps those results
//! for the root being analyzed on this thread; the helpers fall back to a
//! fresh computation for any other root, so results are always identical to
//! the direct queries.

use std::cell::RefCell;
use std::rc::Rc;

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_MEMBER_CALL;
use swlsp_php::NameResolver;
use swlsp_syntax::SyntaxNode;

struct Scan {
    root: SyntaxNode,
    resolver: Option<Rc<NameResolver>>,
    classes: Option<Vec<SyntaxNode>>,
    member_calls: Option<Vec<SyntaxNode>>,
    namespace: Option<String>,
}

thread_local! {
    static SCAN: RefCell<Option<Scan>> = const { RefCell::new(None) };
}

/// Enables the memo for `root` until dropped.
pub(crate) struct ScanScope {
    previous: Option<Scan>,
}

impl ScanScope {
    pub(crate) fn enter(root: &SyntaxNode) -> Self {
        let scan = Scan {
            root: root.clone(),
            resolver: None,
            classes: None,
            member_calls: None,
            namespace: None,
        };
        Self {
            previous: SCAN.with(|current| current.borrow_mut().replace(scan)),
        }
    }
}

impl Drop for ScanScope {
    fn drop(&mut self) {
        let previous = self.previous.take();
        SCAN.with(|current| *current.borrow_mut() = previous);
    }
}

fn cached<T: Clone>(
    root: &SyntaxNode,
    field: impl Fn(&mut Scan) -> &mut Option<T>,
    compute: impl FnOnce() -> T,
) -> T {
    let hit = SCAN.with(|current| {
        let mut current = current.borrow_mut();
        let scan = current.as_mut().filter(|scan| scan.root == *root)?;
        field(scan).clone()
    });
    if let Some(value) = hit {
        return value;
    }
    let value = compute();
    SCAN.with(|current| {
        if let Some(scan) = current
            .borrow_mut()
            .as_mut()
            .filter(|scan| scan.root == *root)
        {
            *field(scan) = Some(value.clone());
        }
    });
    value
}

/// `NameResolver::new(root)`.
pub(crate) fn resolver(root: &SyntaxNode) -> Rc<NameResolver> {
    cached(
        root,
        |scan| &mut scan.resolver,
        || Rc::new(NameResolver::new(root)),
    )
}

/// `phpquery::classes(root)`.
pub(crate) fn classes(root: &SyntaxNode) -> Vec<SyntaxNode> {
    cached(root, |scan| &mut scan.classes, || phpquery::classes(root))
}

/// `phpquery::nodes(root, &[PHP_MEMBER_CALL])`.
pub(crate) fn member_calls(root: &SyntaxNode) -> Vec<SyntaxNode> {
    cached(
        root,
        |scan| &mut scan.member_calls,
        || phpquery::nodes(root, &[PHP_MEMBER_CALL]),
    )
}

/// `phpquery::namespace(root)`.
pub(crate) fn namespace(root: &SyntaxNode) -> String {
    cached(
        root,
        |scan| &mut scan.namespace,
        || phpquery::namespace(root),
    )
}
