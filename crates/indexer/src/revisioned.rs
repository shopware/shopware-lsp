//! Revisions of published index data.

use std::sync::Arc;

/// Published data with a revision that advances whenever the data changes.
/// Consumers memoize derived results under it, and inspections report the
/// revisions of the indexes they read as their input revision.
///
/// Revisions only grow, so the sum of several is a valid combined revision:
/// it changes whenever one of them does.
pub trait Revisioned {
    fn revision(&self) -> u64;
}

impl<T: Revisioned + ?Sized> Revisioned for &T {
    fn revision(&self) -> u64 {
        (**self).revision()
    }
}

impl<T: Revisioned + ?Sized> Revisioned for Arc<T> {
    fn revision(&self) -> u64 {
        (**self).revision()
    }
}

/// An absent index never changes.
impl<T: Revisioned> Revisioned for Option<T> {
    fn revision(&self) -> u64 {
        self.as_ref().map_or(0, Revisioned::revision)
    }
}
