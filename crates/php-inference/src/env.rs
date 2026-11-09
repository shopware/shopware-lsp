//! Copy-on-write variable frames used by flow analysis.
//!
//! An [`Env`] is a handle. Cloning the handle (`Env::clone`) shares the frame,
//! so writes are visible through every clone while one expression is
//! evaluated;
//! [`Env::fork`] creates an independent handle that shares the immutable
//! storage until either side writes. Small frames are linear vectors and
//! larger ones graduate to a hash map.

use std::cell::RefCell;
use std::rc::Rc;

use rustc_hash::FxHashMap;
use swlsp_php_types::{Relations, Type};

/// Frames above this size switch from a vector to a map.
pub(crate) const SMALL_ENVIRONMENT_LIMIT: usize = 8;

pub(crate) const BOOLEAN_ALIAS_BINDING_PREFIX: &str = "\0boolean-alias:";
pub(crate) const IMPOSSIBLE_ENVIRONMENT_BINDING: &str = "\0impossible-flow";

#[derive(Clone, Debug)]
enum Frame {
    Small(Vec<(Rc<str>, Type)>),
    Map(FxHashMap<Rc<str>, Type>),
}

impl Default for Frame {
    fn default() -> Self {
        Frame::Small(Vec::new())
    }
}

impl Frame {
    fn get(&self, name: &str) -> Option<&Type> {
        match self {
            Frame::Small(bindings) => bindings
                .iter()
                .find(|(key, _)| &**key == name)
                .map(|(_, value)| value),
            Frame::Map(table) => table.get(name),
        }
    }

    fn len(&self) -> usize {
        match self {
            Frame::Small(bindings) => bindings.len(),
            Frame::Map(table) => table.len(),
        }
    }
}

/// A shared handle to a copy-on-write variable frame.
#[derive(Clone, Debug, Default)]
pub(crate) struct Env(Rc<RefCell<Rc<Frame>>>);

impl Env {
    pub(crate) fn new(capacity: usize) -> Env {
        let frame = if capacity > SMALL_ENVIRONMENT_LIMIT {
            Frame::Map(FxHashMap::with_capacity_and_hasher(
                capacity,
                Default::default(),
            ))
        } else {
            Frame::Small(Vec::with_capacity(capacity))
        };
        Env(Rc::new(RefCell::new(Rc::new(frame))))
    }

    /// An independent handle sharing the current storage until a write.
    pub(crate) fn fork(&self) -> Env {
        Env(Rc::new(RefCell::new(self.0.borrow().clone())))
    }

    pub(crate) fn get(&self, name: &str) -> Option<Type> {
        self.0.borrow().get(name).cloned()
    }

    pub(crate) fn contains(&self, name: &str) -> bool {
        self.0.borrow().get(name).is_some()
    }

    pub(crate) fn set(&self, name: &str, value: Type) {
        let mut handle = self.0.borrow_mut();
        if handle.get(name).is_some_and(|current| *current == value) {
            return;
        }
        let frame = Rc::make_mut(&mut *handle);
        match frame {
            Frame::Map(table) => {
                if let Some(slot) = table.get_mut(name) {
                    *slot = value;
                } else {
                    table.insert(Rc::from(name), value);
                }
            }
            Frame::Small(bindings) => {
                if let Some((_, slot)) = bindings.iter_mut().find(|(key, _)| &**key == name) {
                    *slot = value;
                    return;
                }
                if bindings.len() < SMALL_ENVIRONMENT_LIMIT {
                    bindings.push((Rc::from(name), value));
                    return;
                }
                let mut table: FxHashMap<Rc<str>, Type> =
                    FxHashMap::with_capacity_and_hasher(bindings.len() + 1, Default::default());
                for (key, value) in bindings.drain(..) {
                    table.insert(key, value);
                }
                table.insert(Rc::from(name), value);
                *frame = Frame::Map(table);
            }
        }
    }

    /// Visits every binding. The order is unspecified for large frames;
    /// callers only use it for order-independent updates.
    pub(crate) fn visit(&self, mut visitor: impl FnMut(&str, &Type)) {
        let frame = self.0.borrow().clone();
        match &*frame {
            Frame::Small(bindings) => {
                for (key, value) in bindings {
                    visitor(key, value);
                }
            }
            Frame::Map(table) => {
                for (key, value) in table {
                    visitor(key, value);
                }
            }
        }
    }

    pub(crate) fn delete_prefix(&self, prefix: &str) {
        if prefix.is_empty() || !self.has_prefix(prefix) {
            return;
        }
        let mut handle = self.0.borrow_mut();
        match Rc::make_mut(&mut *handle) {
            Frame::Small(bindings) => bindings.retain(|(key, _)| !key.starts_with(prefix)),
            Frame::Map(table) => table.retain(|key, _| !key.starts_with(prefix)),
        }
    }

    pub(crate) fn has_prefix(&self, prefix: &str) -> bool {
        match &**self.0.borrow() {
            Frame::Small(bindings) => bindings.iter().any(|(key, _)| key.starts_with(prefix)),
            Frame::Map(table) => table.keys().any(|key| key.starts_with(prefix)),
        }
    }

    pub(crate) fn len(&self) -> usize {
        self.0.borrow().len()
    }

    /// Whether both handles address the same frame.
    #[cfg(test)]
    pub(crate) fn same_handle(&self, other: &Env) -> bool {
        Rc::ptr_eq(&self.0, &other.0)
    }

    /// Whether the storage is still shared with another handle.
    #[cfg(test)]
    pub(crate) fn storage_shared(&self) -> bool {
        Rc::strong_count(&self.0.borrow()) > 1
    }

    #[cfg(test)]
    pub(crate) fn is_map(&self) -> bool {
        matches!(&**self.0.borrow(), Frame::Map(_))
    }

    pub(crate) fn is_impossible(&self) -> bool {
        self.contains(IMPOSSIBLE_ENVIRONMENT_BINDING)
    }
}

/// Joins two branch environments. A variable known on only one side keeps
/// its value; boolean-alias refinements survive only when both sides know
/// them.
pub(crate) fn join_environments(relations: Relations<'_>, left: &Env, right: &Env) -> Env {
    if left.is_impossible() {
        return right.fork();
    }
    if right.is_impossible() {
        return left.fork();
    }
    let result = Env::new(left.len().max(right.len()));
    left.visit(|name, value| {
        let other = right.get(name);
        if name.starts_with(BOOLEAN_ALIAS_BINDING_PREFIX) && other.is_none() {
            return;
        }
        match other {
            Some(other) => result.set(name, relations.join(value, &other)),
            None => result.set(name, value.clone()),
        }
    });
    right.visit(|name, value| {
        if name.starts_with(BOOLEAN_ALIAS_BINDING_PREFIX) {
            return;
        }
        if !result.contains(name) {
            result.set(name, value.clone());
        }
    });
    result
}
