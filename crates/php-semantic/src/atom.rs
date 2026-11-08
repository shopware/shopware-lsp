//! Generational string interner for the retained workspace graph.
//!
//! Published declarations and references store [`Atom`]s (4 bytes) instead
//! of string headers, and materialized names point straight into the atom
//! arena (see [`Str`]).
//!
//! # Generations
//!
//! Atoms live in a [`Generation`]: 16 shards, each a hash table of offsets
//! plus an append-only arena of 256 KiB chunks holding `varint length +
//! bytes` records. An atom encodes its generation slot (up to [`SLOTS`]
//! generations are alive at once), shard and offset, so it resolves without
//! context. Every packed document holds the generation its atoms belong to,
//! so a generation lives exactly as long as some document (and therefore
//! some snapshot) can still reach one of its atoms.
//!
//! Interning targets the generation pinned on the current thread
//! ([`with_generation`]) or the current one. Packing and decoding pin a
//! generation for a whole document, so all atoms of one document share one
//! generation; the store re-homes documents from older generations when it
//! publishes, so every published snapshot is single-generation and atom
//! identity equals string identity within it.
//!
//! # Collection
//!
//! The store collects when the current generation has grown to twice its
//! size after the last collection (and past [`COLLECT_MIN_BYTES`]): it
//! re-interns the strings of every retained document into a fresh
//! generation and makes it current. The old generation is freed once the
//! last snapshot using it is dropped. Interned [`Str`]s keep only their
//! chunk alive (a per-chunk reference count in the chunk header), so a
//! materialized name stays valid after its generation is gone.
//!
//! Transient analysis state (document overlays, binder output, lazily loaded
//! details) never interns, so editing does not grow the table.

use std::alloc::{Layout, alloc, dealloc, handle_alloc_error};
use std::cell::RefCell;
use std::hash::{BuildHasher, Hasher};
use std::num::NonZeroU32;
use std::ptr::NonNull;
use std::sync::atomic::{AtomicBool, AtomicPtr, AtomicUsize, Ordering, fence};
use std::sync::{Arc, Mutex, OnceLock, RwLock, Weak};

use hashbrown::HashTable;

use crate::str::Str;

const SHARD_BITS: u32 = 4;
const SHARDS: usize = 1 << SHARD_BITS;
const SLOT_BITS: u32 = 2;
/// Generations that can be alive at once.
pub(crate) const SLOTS: usize = 1 << SLOT_BITS;
const OFFSET_BITS: u32 = 32 - SHARD_BITS - SLOT_BITS;
const CHUNK_BITS: u32 = 18;
const CHUNK_SIZE: usize = 1 << CHUNK_BITS;
/// Offset space of one shard (64 MiB, so 1 GiB per generation).
const SPACE: usize = 1 << OFFSET_BITS;
const CHUNKS: usize = SPACE / CHUNK_SIZE;
/// Bytes reserved for the [`ChunkHeader`] at the start of every block.
const HEADER: usize = 16;

/// A generation smaller than this is never collected.
pub(crate) const COLLECT_MIN_BYTES: usize = 64 << 20;

/// An interned, immutable string. Equality is identity within one
/// generation.
#[derive(Clone, Copy, Default, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub(crate) struct Atom(Option<NonZeroU32>);

impl Atom {
    /// The empty string; never stored in a table.
    pub const EMPTY: Atom = Atom(None);

    /// Interns `value` in the pinned (or current) generation.
    pub fn intern(value: &str) -> Atom {
        if value.is_empty() {
            return Atom::EMPTY;
        }
        with_target(|generation| generation.intern(value))
    }

    /// Interns `value` in the generation of `self`, which must be non-empty
    /// and reachable (its document keeps the generation alive). Used for
    /// values a packed declaration interns lazily.
    pub fn intern_like(self, value: &str) -> Atom {
        if value.is_empty() {
            return Atom::EMPTY;
        }
        match self.generation() {
            Some(generation) => generation.intern(value),
            None => Atom::intern(value),
        }
    }

    /// This atom in the pinned (or current) generation, re-interning it
    /// when it belongs to another one.
    pub fn in_target(self) -> Atom {
        if self.is_empty() {
            return self;
        }
        with_target(|generation| {
            if generation.slot == self.slot() {
                self
            } else {
                generation.intern(self.as_str())
            }
        })
    }

    pub fn is_empty(self) -> bool {
        self.0.is_none()
    }

    /// The interned text. The returned reference is valid while the
    /// atom's generation is alive, which every holder of an atom (a packed
    /// document, or a scope pinned to the generation) guarantees; callers
    /// must not keep it beyond that. Use [`Atom::name`] for an owned handle.
    pub fn as_str(self) -> &'static str {
        match self.generation() {
            None => "",
            Some(generation) => {
                let raw = self.raw();
                generation.shards[(raw as usize) & (SHARDS - 1)]
                    .read((raw >> (SHARD_BITS + SLOT_BITS)) as usize)
            }
        }
    }

    /// The text as a [`Str`] that keeps its chunk alive.
    pub fn name(self) -> Str {
        let value = self.as_str();
        if value.is_empty() {
            return Str::EMPTY;
        }
        // SAFETY: `value` points into a live arena chunk.
        unsafe { Str::from_interned(NonNull::from(value.as_bytes()).cast(), value.len()) }
    }

    /// The atom with a [`Atom::raw`] identifier.
    pub(crate) fn from_raw(raw: u32) -> Atom {
        Atom(NonZeroU32::new(raw))
    }

    /// Raw identifier, stable while the generation lives.
    pub fn raw(self) -> u32 {
        self.0.map_or(0, NonZeroU32::get)
    }

    fn from_parts(offset: usize, slot: usize, shard: usize) -> Atom {
        debug_assert!((HEADER..SPACE).contains(&offset));
        let raw = ((offset as u32) << (SHARD_BITS + SLOT_BITS))
            | ((slot as u32) << SHARD_BITS)
            | shard as u32;
        Atom(NonZeroU32::new(raw))
    }

    fn slot(self) -> usize {
        ((self.raw() >> SHARD_BITS) as usize) & (SLOTS - 1)
    }

    fn generation(self) -> Option<&'static Generation> {
        if self.is_empty() {
            return None;
        }
        let pointer = SLOT_POINTERS[self.slot()].load(Ordering::Acquire);
        assert!(!pointer.is_null(), "atom of a freed generation");
        // SAFETY: the generation is alive while any of its atoms is
        // reachable (see the module documentation).
        Some(unsafe { &*pointer })
    }
}

impl std::fmt::Debug for Atom {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{:?}", self.as_str())
    }
}

/// The canonical interned [`Str`] for `value` in the pinned (or current)
/// generation.
pub(crate) fn intern_str(value: &str) -> Str {
    Atom::intern(value).name()
}

/// The empty name.
pub fn empty_name() -> Str {
    Str::EMPTY
}

pub(crate) fn hash_str(value: &str) -> u64 {
    let mut hasher = rustc_hash::FxBuildHasher.build_hasher();
    hasher.write(value.as_bytes());
    hasher.finish()
}

// ---------------------------------------------------------------------------
// Chunks

/// Lives in the first [`HEADER`] bytes of every block. `refs` counts the
/// owning generation plus every interned [`Str`] pointing into the block.
#[repr(C)]
struct ChunkHeader {
    refs: AtomicUsize,
    size: usize,
}

const _: () = assert!(std::mem::size_of::<ChunkHeader>() <= HEADER);

/// The header of the block holding the interned string at `pointer`.
/// Records start in the first chunk of their block, so the header is at the
/// chunk-aligned address below it.
fn chunk_header(pointer: *const u8) -> *const ChunkHeader {
    ((pointer as usize) & !(CHUNK_SIZE - 1)) as *const ChunkHeader
}

/// Adds a reference to the block holding `pointer`.
///
/// # Safety
///
/// `pointer` must point into a block that is alive (referenced by its
/// generation or another interned [`Str`]).
pub(crate) unsafe fn chunk_retain(pointer: *const u8) {
    // SAFETY: the caller guarantees the block is alive.
    unsafe {
        (*chunk_header(pointer))
            .refs
            .fetch_add(1, Ordering::Relaxed)
    };
}

/// Drops a reference to the block holding `pointer`, freeing it with the
/// last one.
///
/// # Safety
///
/// The caller must own one reference taken by [`chunk_retain`] (or the
/// generation's own).
pub(crate) unsafe fn chunk_release(pointer: *const u8) {
    let header = chunk_header(pointer);
    // SAFETY: the caller owns a reference, so the block is alive.
    unsafe {
        if (*header).refs.fetch_sub(1, Ordering::Release) != 1 {
            return;
        }
        fence(Ordering::Acquire);
        let size = (*header).size;
        dealloc(
            header as *mut u8,
            Layout::from_size_align_unchecked(size, CHUNK_SIZE),
        );
    }
}

// ---------------------------------------------------------------------------
// Generations

struct ShardState {
    table: HashTable<u32>,
    /// Offset of the next record; a multiple of [`CHUNK_SIZE`] means the
    /// next record starts a new block.
    used: usize,
    bytes: usize,
    blocks: Vec<NonNull<u8>>,
}

// SAFETY: the block pointers are owned by the shard and only freed through
// the atomic chunk reference count.
unsafe impl Send for ShardState {}

struct Shard {
    state: Mutex<ShardState>,
    chunks: Box<[AtomicPtr<u8>]>,
}

/// One interner generation; see the module documentation.
pub(crate) struct Generation {
    slot: usize,
    shards: [Shard; SHARDS],
    /// String bytes right after this generation became current.
    baseline: AtomicUsize,
}

fn varint_len(value: usize) -> usize {
    let mut length = 1;
    let mut value = value >> 7;
    while value != 0 {
        length += 1;
        value >>= 7;
    }
    length
}

impl Shard {
    fn new() -> Shard {
        Shard {
            state: Mutex::new(ShardState {
                table: HashTable::new(),
                used: 0,
                bytes: 0,
                blocks: Vec::new(),
            }),
            chunks: (0..CHUNKS)
                .map(|_| AtomicPtr::new(std::ptr::null_mut()))
                .collect(),
        }
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, ShardState> {
        self.state
            .lock()
            .unwrap_or_else(|poison| poison.into_inner())
    }

    /// Reads the record at `offset`.
    fn read(&self, offset: usize) -> &'static str {
        let chunk = self.chunks[offset >> CHUNK_BITS].load(Ordering::Acquire);
        debug_assert!(!chunk.is_null());
        // SAFETY: the record was fully written before its offset was
        // published (under the shard lock, followed by the release store of
        // the chunk pointer or by the synchronization that handed the atom
        // to this thread). A record lies within its block. The block lives
        // as long as its generation, which the caller keeps alive.
        unsafe {
            let mut cursor = chunk.add(offset & (CHUNK_SIZE - 1));
            let mut length = 0usize;
            let mut shift = 0;
            loop {
                let byte = *cursor;
                cursor = cursor.add(1);
                length |= usize::from(byte & 0x7F) << shift;
                if byte & 0x80 == 0 {
                    break;
                }
                shift += 7;
            }
            let bytes = std::slice::from_raw_parts(cursor, length);
            std::str::from_utf8_unchecked(bytes)
        }
    }

    /// Appends a record, returning its offset. Caller holds the lock.
    fn append(&self, state: &mut ShardState, value: &str) -> usize {
        let needed = varint_len(value.len()) + value.len();
        let mut offset = state.used;
        let within = offset & (CHUNK_SIZE - 1);
        let mut end = offset + needed;
        if within == 0 || needed > CHUNK_SIZE - within {
            // Start a new block at the next chunk boundary. A record larger
            // than one chunk gets a block of its own.
            let start = if within == 0 {
                offset
            } else {
                (offset | (CHUNK_SIZE - 1)) + 1
            };
            let chunks = (HEADER + needed).div_ceil(CHUNK_SIZE);
            assert!(
                start + chunks * CHUNK_SIZE <= SPACE,
                "atom generation exhausted"
            );
            let size = chunks * CHUNK_SIZE;
            // SAFETY: non-zero size, power-of-two alignment.
            let layout = unsafe { Layout::from_size_align_unchecked(size, CHUNK_SIZE) };
            // SAFETY: valid layout.
            let base = unsafe { alloc(layout) };
            let Some(base) = NonNull::new(base) else {
                handle_alloc_error(layout);
            };
            // SAFETY: the block starts with room for the header.
            unsafe {
                base.cast::<ChunkHeader>().write(ChunkHeader {
                    refs: AtomicUsize::new(1),
                    size,
                });
            }
            state.blocks.push(base);
            let first = start >> CHUNK_BITS;
            for index in 0..chunks {
                // SAFETY: within the block.
                let pointer = unsafe { base.as_ptr().add(index * CHUNK_SIZE) };
                self.chunks[first + index].store(pointer, Ordering::Release);
            }
            offset = start + HEADER;
            end = if chunks > 1 {
                start + size
            } else {
                offset + needed
            };
        }
        let chunk = self.chunks[offset >> CHUNK_BITS].load(Ordering::Relaxed);
        // SAFETY: the destination range lies inside a block owned by this
        // shard and not yet published to readers.
        unsafe {
            let mut cursor = chunk.add(offset & (CHUNK_SIZE - 1));
            let mut length = value.len();
            loop {
                let mut byte = (length & 0x7F) as u8;
                length >>= 7;
                if length != 0 {
                    byte |= 0x80;
                }
                cursor.write(byte);
                cursor = cursor.add(1);
                if length == 0 {
                    break;
                }
            }
            std::ptr::copy_nonoverlapping(value.as_ptr(), cursor, value.len());
        }
        state.used = end;
        state.bytes += value.len();
        offset
    }
}

impl Generation {
    fn new(slot: usize) -> Generation {
        Generation {
            slot,
            shards: std::array::from_fn(|_| Shard::new()),
            baseline: AtomicUsize::new(0),
        }
    }

    fn intern(&self, value: &str) -> Atom {
        let hash = hash_str(value);
        let shard_index = ((hash >> 32) as usize) & (SHARDS - 1);
        let shard = &self.shards[shard_index];
        let mut state = shard.lock();
        if let Some(&offset) = state
            .table
            .find(hash, |&offset| shard.read(offset as usize) == value)
        {
            return Atom::from_parts(offset as usize, self.slot, shard_index);
        }
        let offset = shard.append(&mut state, value);
        state.table.insert_unique(hash, offset as u32, |&offset| {
            hash_str(shard.read(offset as usize))
        });
        Atom::from_parts(offset, self.slot, shard_index)
    }

    /// The existing atom for `value`, without interning it.
    #[cfg(test)]
    fn lookup(&self, value: &str) -> Option<Atom> {
        let hash = hash_str(value);
        let shard_index = ((hash >> 32) as usize) & (SHARDS - 1);
        let shard = &self.shards[shard_index];
        let state = shard.lock();
        state
            .table
            .find(hash, |&offset| shard.read(offset as usize) == value)
            .map(|&offset| Atom::from_parts(offset as usize, self.slot, shard_index))
    }

    /// `(strings, string bytes, approximate retained bytes)`.
    fn stats(&self) -> (usize, usize, usize) {
        let mut count = 0;
        let mut bytes = 0;
        let mut retained = 0;
        for shard in &self.shards {
            let state = shard.lock();
            count += state.table.len();
            bytes += state.bytes;
            retained += state.table.capacity() * (std::mem::size_of::<u32>() + 1);
            retained += state.used.div_ceil(CHUNK_SIZE) * CHUNK_SIZE;
        }
        (count, bytes, retained)
    }

    fn bytes(&self) -> usize {
        self.shards.iter().map(|shard| shard.lock().bytes).sum()
    }

    /// Whether `self` and `other` are the same generation.
    pub(crate) fn same(&self, other: &Generation) -> bool {
        std::ptr::eq(self, other)
    }
}

impl Drop for Generation {
    fn drop(&mut self) {
        // A new generation may already reuse the slot; only clear our own
        // registration.
        let _ = SLOT_POINTERS[self.slot].compare_exchange(
            self as *mut Generation,
            std::ptr::null_mut(),
            Ordering::AcqRel,
            Ordering::Relaxed,
        );
        for shard in &self.shards {
            let state = shard.lock();
            for block in &state.blocks {
                // SAFETY: the generation owns one reference to each block.
                unsafe { chunk_release(block.as_ptr()) };
            }
        }
    }
}

// ---------------------------------------------------------------------------
// Registry

static SLOT_POINTERS: [AtomicPtr<Generation>; SLOTS] =
    [const { AtomicPtr::new(std::ptr::null_mut()) }; SLOTS];

struct Registry {
    current: Arc<Generation>,
    slots: [Weak<Generation>; SLOTS],
}

static COLLECTING: AtomicBool = AtomicBool::new(false);
static COLLECTIONS: AtomicUsize = AtomicUsize::new(0);

fn registry() -> &'static RwLock<Registry> {
    static REGISTRY: OnceLock<RwLock<Registry>> = OnceLock::new();
    REGISTRY.get_or_init(|| {
        let current = register(0);
        let mut slots: [Weak<Generation>; SLOTS] = std::array::from_fn(|_| Weak::new());
        slots[0] = Arc::downgrade(&current);
        RwLock::new(Registry { current, slots })
    })
}

fn register(slot: usize) -> Arc<Generation> {
    let generation = Arc::new(Generation::new(slot));
    SLOT_POINTERS[slot].store(
        Arc::as_ptr(&generation) as *mut Generation,
        Ordering::Release,
    );
    generation
}

/// The generation new documents are interned into.
pub(crate) fn current_generation() -> Arc<Generation> {
    registry()
        .read()
        .unwrap_or_else(|poison| poison.into_inner())
        .current
        .clone()
}

thread_local! {
    static PINNED: RefCell<Option<Arc<Generation>>> = const { RefCell::new(None) };
}

/// The pinned generation of this thread, or the current one.
pub(crate) fn target_generation() -> Arc<Generation> {
    PINNED
        .with(|pinned| pinned.borrow().clone())
        .unwrap_or_else(current_generation)
}

fn with_target<R>(use_generation: impl FnOnce(&Generation) -> R) -> R {
    let mut use_generation = Some(use_generation);
    let pinned = PINNED.with(|pinned| {
        let pinned = pinned.borrow();
        pinned
            .as_ref()
            .map(|generation| (use_generation.take().unwrap())(generation))
    });
    if let Some(result) = pinned {
        return result;
    }
    let registry = registry()
        .read()
        .unwrap_or_else(|poison| poison.into_inner());
    (use_generation.take().unwrap())(&registry.current)
}

/// Runs `body` with interning pinned to `generation` on this thread.
pub(crate) fn with_generation<R>(generation: &Arc<Generation>, body: impl FnOnce() -> R) -> R {
    struct Restore(Option<Arc<Generation>>);
    impl Drop for Restore {
        fn drop(&mut self) {
            let previous = self.0.take();
            PINNED.with(|pinned| *pinned.borrow_mut() = previous);
        }
    }
    let previous = PINNED.with(|pinned| pinned.borrow_mut().replace(generation.clone()));
    let _restore = Restore(previous);
    body()
}

/// Whether the current generation has accumulated enough garbage to be
/// worth collecting: at least [`COLLECT_MIN_BYTES`] and twice its size
/// right after it became current.
pub(crate) fn should_collect() -> bool {
    let current = current_generation();
    let bytes = current.bytes();
    bytes >= COLLECT_MIN_BYTES && bytes >= 2 * current.baseline.load(Ordering::Relaxed)
}

/// A collection in progress: a fresh generation that becomes current on
/// [`Collection::finish`]. Dropping it unfinished abandons it.
pub(crate) struct Collection {
    generation: Arc<Generation>,
}

impl Collection {
    /// Starts a collection, or `None` while another collection runs or
    /// every generation slot is still in use.
    pub(crate) fn begin() -> Option<Collection> {
        if COLLECTING
            .compare_exchange(false, true, Ordering::AcqRel, Ordering::Relaxed)
            .is_err()
        {
            return None;
        }
        let mut registry = registry()
            .write()
            .unwrap_or_else(|poison| poison.into_inner());
        let free = (0..SLOTS).find(|&slot| {
            registry.slots[slot].strong_count() == 0
                && SLOT_POINTERS[slot].load(Ordering::Acquire).is_null()
        });
        let Some(slot) = free else {
            COLLECTING.store(false, Ordering::Release);
            return None;
        };
        let generation = register(slot);
        registry.slots[slot] = Arc::downgrade(&generation);
        Some(Collection { generation })
    }

    pub(crate) fn generation(&self) -> &Arc<Generation> {
        &self.generation
    }

    /// Makes the new generation current.
    pub(crate) fn finish(self) {
        self.generation
            .baseline
            .store(self.generation.bytes(), Ordering::Relaxed);
        registry()
            .write()
            .unwrap_or_else(|poison| poison.into_inner())
            .current = self.generation.clone();
        COLLECTIONS.fetch_add(1, Ordering::Relaxed);
    }
}

impl Drop for Collection {
    fn drop(&mut self) {
        COLLECTING.store(false, Ordering::Release);
    }
}

/// Whether `value` is in the pinned (or current) generation.
#[cfg(test)]
pub(crate) fn is_interned(value: &str) -> bool {
    with_target(|generation| generation.lookup(value).is_some())
}

/// `(strings, string bytes, approximate retained bytes)` of the current
/// atom generation.
pub fn atom_table_stats() -> (usize, usize, usize) {
    current_generation().stats()
}

/// Interner generation counters.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct AtomGenerationStats {
    /// Generations still alive (the current one included).
    pub live: usize,
    /// Completed collections since process start.
    pub collections: usize,
}

/// Live generations and completed collections.
pub fn atom_generation_stats() -> AtomGenerationStats {
    let registry = registry()
        .read()
        .unwrap_or_else(|poison| poison.into_inner());
    AtomGenerationStats {
        live: registry
            .slots
            .iter()
            .filter(|slot| slot.strong_count() > 0)
            .count(),
        collections: COLLECTIONS.load(Ordering::Relaxed),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn interns_canonical_atoms() {
        let first = Atom::intern("App\\Service");
        let second = Atom::intern(&String::from("App\\Service"));
        assert_eq!(first, second);
        assert_eq!(first.as_str(), "App\\Service");
        assert!(std::ptr::eq(first.as_str(), second.as_str()));
        assert_eq!(Atom::intern(""), Atom::EMPTY);
        assert_eq!(Atom::EMPTY.as_str(), "");
        assert_ne!(Atom::intern("a"), Atom::intern("b"));
        assert!(first.name().is_interned());
    }

    #[test]
    fn private_generation_frees_blocks_after_last_name() {
        // A generation outside the registry: only its own slot pointer is
        // touched, so pick a slot no other test can be using (tests never
        // collect the global registry).
        let slot = SLOTS - 1;
        let generation = register(slot);
        let atom = generation.intern("private-generation-value");
        let large = "y".repeat(CHUNK_SIZE + 5);
        let large_atom = generation.intern(&large);
        let name = atom.name();
        let large_name = large_atom.name();
        assert_eq!(atom.as_str(), "private-generation-value");
        assert_eq!(large_atom.as_str(), large);
        drop(generation);
        assert!(SLOT_POINTERS[slot].load(Ordering::Acquire).is_null());
        // The names keep their blocks alive after the generation is gone.
        assert_eq!(&*name, "private-generation-value");
        assert_eq!(&*large_name, large);
        let clone = name.clone();
        drop(name);
        assert_eq!(&*clone, "private-generation-value");
    }

    #[test]
    fn interns_large_and_unicode_values() {
        let large = "x".repeat(CHUNK_SIZE * 2 + 17);
        let atom = Atom::intern(&large);
        assert_eq!(atom.as_str(), large);
        let after = Atom::intern("after-large-ä");
        assert_eq!(after.as_str(), "after-large-ä");
        assert_eq!(Atom::intern(&large), atom);
        let medium = "y".repeat(200);
        assert_eq!(Atom::intern(&medium).as_str(), medium);
        assert_eq!(&*atom.name(), large);
    }

    #[test]
    fn interns_many_values_concurrently() {
        let threads: Vec<_> = (0..8)
            .map(|thread| {
                std::thread::spawn(move || {
                    (0..5000)
                        .map(|index| {
                            Atom::intern(&format!(
                                "value-{}-{}",
                                (index * 7 + thread) % 7000,
                                "z".repeat(index % 50)
                            ))
                        })
                        .collect::<Vec<_>>()
                })
            })
            .collect();
        for handle in threads {
            for atom in handle.join().unwrap() {
                assert!(atom.as_str().starts_with("value-"));
                assert_eq!(Atom::intern(atom.as_str()), atom);
            }
        }
    }
}
