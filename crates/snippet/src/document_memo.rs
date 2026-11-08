//! Per-tree memoization for request-time document scans.
//!
//! Providers such as definition, hover and references ask "which reference
//! is at this offset?" once per request, and Go answers it by rescanning the
//! whole document every time. A `DocumentMemo` computes the full scan once
//! per immutable tree snapshot and reuses it for later requests on the same
//! snapshot.
//!
//! Entries are keyed by the identity of the node's green tree plus the node's
//! start offset (rowan may share identical green subtrees between positions)
//! and an extra string key (for example the file path stored in the results).
//! Each entry keeps its green node alive, so a key can never be reused by a
//! different tree while it is cached.

use std::sync::{Arc, Mutex};

use swlsp_syntax::SyntaxNode;
use swlsp_syntax::rowan::{self, GreenNode};

/// A small, thread-safe most-recently-used cache of per-tree scan results.
pub(crate) struct DocumentMemo<T> {
    capacity: usize,
    entries: Mutex<Vec<MemoEntry<T>>>,
}

struct MemoEntry<T> {
    green: GreenNode,
    start: u32,
    key: Box<str>,
    value: Arc<T>,
}

impl<T> DocumentMemo<T> {
    pub(crate) const fn new(capacity: usize) -> Self {
        Self {
            capacity,
            entries: Mutex::new(Vec::new()),
        }
    }

    /// The cached value for `node` and `key`, computing it with `compute` on
    /// a miss. `compute` runs without holding the cache lock.
    pub(crate) fn get_or_compute(
        &self,
        node: &SyntaxNode,
        key: &str,
        compute: impl FnOnce() -> T,
    ) -> Arc<T> {
        let green = node.green();
        let pointer: *const rowan::GreenNodeData = &*green;
        let start = u32::from(node.text_range().start());
        if let Ok(mut entries) = self.entries.lock()
            && let Some(index) = entries.iter().position(|entry| {
                std::ptr::eq(&*entry.green as *const rowan::GreenNodeData, pointer)
                    && entry.start == start
                    && &*entry.key == key
            })
        {
            let entry = entries.remove(index);
            let value = entry.value.clone();
            entries.push(entry);
            return value;
        }
        let green = green.into_owned();
        let value = Arc::new(compute());
        if let Ok(mut entries) = self.entries.lock() {
            if entries.len() >= self.capacity.max(1) {
                entries.remove(0);
            }
            entries.push(MemoEntry {
                green,
                start,
                key: key.into(),
                value: value.clone(),
            });
        }
        value
    }
}
