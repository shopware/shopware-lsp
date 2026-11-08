//! Reports live heap bytes retained by the decoded catalog and by
//! materialized stub documents (compare with the Go `runtime.MemStats`
//! measurement of `internal/php/stubs`).

use std::alloc::{GlobalAlloc, Layout, System};
use std::sync::atomic::{AtomicIsize, Ordering};
use std::time::Instant;

use swlsp_php_project::Version;

struct Counting;

static LIVE: AtomicIsize = AtomicIsize::new(0);

// SAFETY: forwards to the system allocator and only counts sizes.
unsafe impl GlobalAlloc for Counting {
    unsafe fn alloc(&self, layout: Layout) -> *mut u8 {
        LIVE.fetch_add(layout.size() as isize, Ordering::Relaxed);
        unsafe { System.alloc(layout) }
    }

    unsafe fn dealloc(&self, pointer: *mut u8, layout: Layout) {
        LIVE.fetch_sub(layout.size() as isize, Ordering::Relaxed);
        unsafe { System.dealloc(pointer, layout) }
    }

    unsafe fn realloc(&self, pointer: *mut u8, layout: Layout, size: usize) -> *mut u8 {
        LIVE.fetch_add(size as isize - layout.size() as isize, Ordering::Relaxed);
        unsafe { System.realloc(pointer, layout, size) }
    }
}

#[global_allocator]
static ALLOCATOR: Counting = Counting;

fn live() -> isize {
    LIVE.load(Ordering::Relaxed)
}

fn megabytes(bytes: isize) -> f64 {
    bytes as f64 / 1e6
}

fn main() {
    let version = Version::new(8, 3, 0);
    let base = live();
    let _ = swlsp_php_stubs::extension_for_symbol("strlen");
    let header = live();
    println!(
        "catalog header + extension map: {:.2} MB",
        megabytes(header - base)
    );
    let start = Instant::now();
    let full = swlsp_php_stubs::document(version);
    let elapsed = start.elapsed();
    let after_full = live();
    println!(
        "document(8.3) all extensions: {} symbols, {:.2} MB, {elapsed:?}",
        full.symbols.len(),
        megabytes(after_full - header)
    );
    let none: [&str; 0] = [];
    let start = Instant::now();
    let core = swlsp_php_stubs::document_for_extensions(version, &none, &[]);
    let elapsed = start.elapsed();
    println!(
        "document_for_extensions(8.3, core): {} symbols, {:.2} MB, {elapsed:?}",
        core.symbols.len(),
        megabytes(live() - after_full)
    );
    let start = Instant::now();
    let snapshot = swlsp_php_semantic::Snapshot::new(1, std::slice::from_ref(&full));
    println!(
        "snapshot of document(8.3): {} classes named DateTime, {:?}",
        snapshot.classes("DateTime").len(),
        start.elapsed()
    );
}
