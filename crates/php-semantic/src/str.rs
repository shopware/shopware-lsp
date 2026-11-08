//! A 16-byte immutable string: a `'static` literal, a shared `Arc<str>`,
//! or an interned string in the atom arena.
//!
//! Strings materialized from the retained workspace graph point into the
//! atom arena, so materializing declarations never allocates; they keep
//! their arena chunk alive through the chunk's reference count, so they
//! stay valid after their interner generation is collected. Strings
//! created by analysis (binder output, open-document overlays) are `Arc`s.

use std::borrow::Borrow;
use std::cmp::Ordering;
use std::fmt;
use std::hash::{Hash, Hasher};
use std::ops::Deref;
use std::ptr::NonNull;
use std::sync::Arc;

const SHARED: usize = 1 << (usize::BITS - 1);
const INTERNED: usize = 1 << (usize::BITS - 2);
const TAGS: usize = SHARED | INTERNED;

pub struct Str {
    ptr: NonNull<u8>,
    /// Byte length; the top bit marks an `Arc<str>` allocation, the next
    /// one an interned string.
    len: usize,
}

// SAFETY: the shared representation is an `Arc<str>` (Send + Sync), the
// interned one an immutable arena record with an atomic chunk count, and the
// static one a `&'static str`.
unsafe impl Send for Str {}
unsafe impl Sync for Str {}

impl Str {
    pub const EMPTY: Str = Str::from_static("");

    /// A `'static` literal.
    pub const fn from_static(value: &'static str) -> Str {
        // SAFETY: string slices are never null.
        let ptr = unsafe { NonNull::new_unchecked(value.as_ptr() as *mut u8) };
        Str {
            ptr,
            len: value.len(),
        }
    }

    fn shared(value: Arc<str>) -> Str {
        if value.is_empty() {
            return Str::EMPTY;
        }
        let len = value.len();
        assert!(len & TAGS == 0, "string too long");
        let raw = Arc::into_raw(value) as *const u8 as *mut u8;
        // SAFETY: `Arc::into_raw` never returns null.
        Str {
            ptr: unsafe { NonNull::new_unchecked(raw) },
            len: len | SHARED,
        }
    }

    /// An interned string at `ptr`, taking a reference to its arena chunk.
    ///
    /// # Safety
    ///
    /// `ptr..ptr + len` must be an interned record in a live arena chunk.
    pub(crate) unsafe fn from_interned(ptr: NonNull<u8>, len: usize) -> Str {
        assert!(len & TAGS == 0, "string too long");
        // SAFETY: the caller guarantees the chunk is alive.
        unsafe { crate::atom::chunk_retain(ptr.as_ptr()) };
        Str {
            ptr,
            len: len | INTERNED,
        }
    }

    fn is_shared(&self) -> bool {
        self.len & SHARED != 0
    }

    /// Whether this string points into the atom arena.
    pub fn is_interned(&self) -> bool {
        self.len & INTERNED != 0
    }

    fn byte_len(&self) -> usize {
        self.len & !TAGS
    }

    fn raw_str(&self) -> *const str {
        std::ptr::slice_from_raw_parts(self.ptr.as_ptr() as *const u8, self.byte_len())
            as *const str
    }

    pub fn as_str(&self) -> &str {
        // SAFETY: `ptr`/`len` describe a live UTF-8 string: static data, an
        // `Arc<str>` or an arena chunk kept alive by this handle.
        unsafe { &*self.raw_str() }
    }

    /// Whether this string is a `'static` literal (or empty).
    pub fn is_static(&self) -> bool {
        self.len & TAGS == 0
    }
}

impl Drop for Str {
    fn drop(&mut self) {
        if self.is_shared() {
            // SAFETY: the pointer came from `Arc::into_raw` of an `Arc<str>`
            // with this length.
            unsafe { Arc::decrement_strong_count(self.raw_str()) };
        } else if self.is_interned() {
            // SAFETY: this handle owns one chunk reference.
            unsafe { crate::atom::chunk_release(self.ptr.as_ptr()) };
        }
    }
}

impl Clone for Str {
    fn clone(&self) -> Str {
        if self.is_shared() {
            // SAFETY: as in `drop`; the handle keeps the allocation alive.
            unsafe { Arc::increment_strong_count(self.raw_str()) };
        } else if self.is_interned() {
            // SAFETY: this handle keeps the chunk alive.
            unsafe { crate::atom::chunk_retain(self.ptr.as_ptr()) };
        }
        Str {
            ptr: self.ptr,
            len: self.len,
        }
    }
}

impl Default for Str {
    fn default() -> Str {
        Str::EMPTY
    }
}

impl Deref for Str {
    type Target = str;
    fn deref(&self) -> &str {
        self.as_str()
    }
}

impl AsRef<str> for Str {
    fn as_ref(&self) -> &str {
        self.as_str()
    }
}

impl Borrow<str> for Str {
    fn borrow(&self) -> &str {
        self.as_str()
    }
}

impl From<&str> for Str {
    fn from(value: &str) -> Str {
        if value.is_empty() {
            Str::EMPTY
        } else {
            Str::shared(Arc::from(value))
        }
    }
}

impl From<&String> for Str {
    fn from(value: &String) -> Str {
        Str::from(value.as_str())
    }
}

impl From<String> for Str {
    fn from(value: String) -> Str {
        if value.is_empty() {
            Str::EMPTY
        } else {
            Str::shared(Arc::from(value))
        }
    }
}

impl From<Arc<str>> for Str {
    fn from(value: Arc<str>) -> Str {
        Str::shared(value)
    }
}

impl From<&Str> for Str {
    fn from(value: &Str) -> Str {
        value.clone()
    }
}

impl From<Str> for Arc<str> {
    fn from(value: Str) -> Arc<str> {
        Arc::from(value.as_str())
    }
}

impl PartialEq for Str {
    fn eq(&self, other: &Str) -> bool {
        self.as_str() == other.as_str()
    }
}

impl Eq for Str {}

impl PartialEq<str> for Str {
    fn eq(&self, other: &str) -> bool {
        self.as_str() == other
    }
}

impl PartialEq<&str> for Str {
    fn eq(&self, other: &&str) -> bool {
        self.as_str() == *other
    }
}

impl PartialEq<Str> for str {
    fn eq(&self, other: &Str) -> bool {
        self == other.as_str()
    }
}

impl PartialEq<Str> for &str {
    fn eq(&self, other: &Str) -> bool {
        *self == other.as_str()
    }
}

impl PartialOrd for Str {
    fn partial_cmp(&self, other: &Str) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}

impl Ord for Str {
    fn cmp(&self, other: &Str) -> Ordering {
        self.as_str().cmp(other.as_str())
    }
}

impl Hash for Str {
    fn hash<H: Hasher>(&self, state: &mut H) {
        self.as_str().hash(state);
    }
}

impl fmt::Display for Str {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.as_str())
    }
}

impl fmt::Debug for Str {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        fmt::Debug::fmt(self.as_str(), f)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn shared_and_static_strings_compare_by_value() {
        let shared = Str::from("App\\Service");
        let copy = shared.clone();
        let fixed = Str::from_static("App\\Service");
        assert_eq!(shared, fixed);
        assert_eq!(copy, "App\\Service");
        assert!(!shared.is_static() && fixed.is_static());
        assert_eq!(std::mem::size_of::<Str>(), 16);
        assert_eq!(std::mem::size_of::<Option<Str>>(), 16);
        assert_eq!(Str::from(""), Str::EMPTY);
        assert!(Str::from(String::new()).is_static());
        drop(shared);
        assert_eq!(copy.len(), 11);
        let mut set = std::collections::HashSet::new();
        set.insert(copy);
        assert!(set.contains("App\\Service"));
    }
}
