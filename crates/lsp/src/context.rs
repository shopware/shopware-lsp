//! Request context: cooperative cancellation plus typed request-local values.
//!
//! A [`CancellationToken`] is cancelled
//! explicitly (`$/cancelRequest`, a superseded diagnostics job, server
//! shutdown) and propagates to every child token. [`Context`] pairs a token
//! with immutable typed values that context enrichers attach before provider
//! fan-out.

use std::any::{Any, TypeId};
use std::collections::HashMap;
use std::fmt;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Condvar, Mutex, Weak};
use std::time::{Duration, Instant};

/// Boxed error returned by providers, inspections and commands. A provider
/// may return an [`RpcError`] to choose the JSON-RPC error code.
pub type BoxError = Box<dyn std::error::Error + Send + Sync + 'static>;

/// JSON-RPC error codes used by the server.
pub mod codes {
    pub const PARSE_ERROR: i32 = -32700;
    pub const INVALID_PARAMS: i32 = -32602;
    pub const METHOD_NOT_FOUND: i32 = -32601;
    pub const INTERNAL_ERROR: i32 = -32603;
    /// LSP `RequestCancelled`.
    pub const REQUEST_CANCELLED: i32 = -32800;
}

/// A JSON-RPC error response. Errors without a code of their own answer
/// `InternalError`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RpcError {
    pub code: i32,
    pub message: String,
}

impl RpcError {
    pub fn new(code: i32, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
        }
    }

    pub fn cancelled() -> Self {
        Self::new(codes::REQUEST_CANCELLED, "request cancelled")
    }

    /// `InternalError` with `message`.
    pub fn internal(message: impl Into<String>) -> Self {
        Self::new(codes::INTERNAL_ERROR, message)
    }

    /// `InvalidParams` with `message`.
    pub fn invalid_params(message: impl Into<String>) -> Self {
        Self::new(codes::INVALID_PARAMS, message)
    }

    /// Converts any handler error; an embedded [`RpcError`] keeps its code.
    pub fn from_error(error: BoxError) -> Self {
        match error.downcast::<RpcError>() {
            Ok(rpc) => *rpc,
            Err(other) => Self::internal(other.to_string()),
        }
    }
}

impl fmt::Display for RpcError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{} (code {})", self.message, self.code)
    }
}

impl std::error::Error for RpcError {}

/// The error a cancelled operation returns.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Cancelled;

impl fmt::Display for Cancelled {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("cancelled")
    }
}

impl std::error::Error for Cancelled {}

/// Reports whether `error` is a cancellation from this crate or the indexer.
pub fn is_cancellation(error: &(dyn std::error::Error + 'static)) -> bool {
    if error.is::<Cancelled>() {
        return true;
    }
    if let Some(indexer) = error.downcast_ref::<swlsp_indexer::Error>() {
        return indexer.is_cancelled();
    }
    error
        .downcast_ref::<RpcError>()
        .is_some_and(|rpc| rpc.code == codes::REQUEST_CANCELLED)
}

type Callback = Box<dyn FnOnce() + Send>;

#[derive(Default)]
struct TokenState {
    children: Vec<Weak<TokenInner>>,
    callbacks: Vec<Callback>,
}

#[derive(Default)]
struct TokenInner {
    cancelled: AtomicBool,
    state: Mutex<TokenState>,
    condvar: Condvar,
}

/// A cloneable cancellation flag. Cancelling a token cancels its children
/// and wakes every waiter.
#[derive(Clone, Default)]
pub struct CancellationToken(Arc<TokenInner>);

impl fmt::Debug for CancellationToken {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_tuple("CancellationToken")
            .field(&self.is_cancelled())
            .finish()
    }
}

impl CancellationToken {
    pub fn new() -> Self {
        Self::default()
    }

    #[inline]
    pub fn is_cancelled(&self) -> bool {
        self.0.cancelled.load(Ordering::Acquire)
    }

    /// `Err(Cancelled)` once cancelled.
    #[inline]
    pub fn check(&self) -> Result<(), Cancelled> {
        if self.is_cancelled() {
            Err(Cancelled)
        } else {
            Ok(())
        }
    }

    pub fn cancel(&self) {
        let (children, callbacks) = {
            let mut state = lock(&self.0.state);
            if self.0.cancelled.swap(true, Ordering::AcqRel) {
                return;
            }
            (
                std::mem::take(&mut state.children),
                std::mem::take(&mut state.callbacks),
            )
        };
        self.0.condvar.notify_all();
        for child in children {
            if let Some(child) = child.upgrade() {
                CancellationToken(child).cancel();
            }
        }
        for callback in callbacks {
            callback();
        }
    }

    /// A new token cancelled together with this one.
    pub fn child(&self) -> CancellationToken {
        let child = CancellationToken::new();
        self.link(&child);
        child
    }

    /// Cancels `other` whenever this token is cancelled.
    pub fn link(&self, other: &CancellationToken) {
        {
            let mut state = lock(&self.0.state);
            if !self.is_cancelled() {
                state.children.retain(|child| child.strong_count() > 0);
                state.children.push(Arc::downgrade(&other.0));
                return;
            }
        }
        other.cancel();
    }

    /// Runs `callback` once when the token is cancelled, immediately when it
    /// already is.
    pub fn on_cancel(&self, callback: impl FnOnce() + Send + 'static) {
        {
            let mut state = lock(&self.0.state);
            if !self.is_cancelled() {
                state.callbacks.push(Box::new(callback));
                return;
            }
        }
        callback();
    }

    /// Whether both handles refer to the same token.
    pub fn same(&self, other: &CancellationToken) -> bool {
        Arc::ptr_eq(&self.0, &other.0)
    }

    /// Blocks until the token is cancelled.
    pub fn wait(&self) {
        let mut state = lock(&self.0.state);
        while !self.is_cancelled() {
            state = self
                .0
                .condvar
                .wait(state)
                .unwrap_or_else(|poisoned| poisoned.into_inner());
        }
    }

    /// Blocks until the token is cancelled or `timeout` elapses. Returns
    /// whether the token was cancelled.
    pub fn wait_timeout(&self, timeout: Duration) -> bool {
        let deadline = Instant::now() + timeout;
        let mut state = lock(&self.0.state);
        while !self.is_cancelled() {
            let now = Instant::now();
            if now >= deadline {
                return false;
            }
            state = self
                .0
                .condvar
                .wait_timeout(state, deadline - now)
                .unwrap_or_else(|poisoned| poisoned.into_inner())
                .0;
        }
        true
    }

    /// An indexer cancel token mirroring this token.
    pub fn indexer_token(&self) -> swlsp_indexer::CancelToken {
        let token = swlsp_indexer::CancelToken::new();
        let mirrored = token.clone();
        self.on_cancel(move || mirrored.cancel());
        token
    }
}

pub(crate) fn lock<T>(mutex: &Mutex<T>) -> std::sync::MutexGuard<'_, T> {
    mutex
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

type Values = HashMap<TypeId, Arc<dyn Any + Send + Sync>>;

/// Cancellation plus immutable typed request values.
#[derive(Clone, Default)]
pub struct Context {
    token: CancellationToken,
    values: Option<Arc<Values>>,
}

impl fmt::Debug for Context {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Context")
            .field("cancelled", &self.is_cancelled())
            .finish_non_exhaustive()
    }
}

impl Context {
    /// A context that is never cancelled unless the caller cancels its token.
    pub fn background() -> Self {
        Self::default()
    }

    pub fn with_token(token: CancellationToken) -> Self {
        Self {
            token,
            values: None,
        }
    }

    pub fn token(&self) -> &CancellationToken {
        &self.token
    }

    #[inline]
    pub fn is_cancelled(&self) -> bool {
        self.token.is_cancelled()
    }

    /// `Err(Cancelled)` once cancelled.
    #[inline]
    pub fn check(&self) -> Result<(), Cancelled> {
        self.token.check()
    }

    /// A derived context whose token is a child of this one; values are kept.
    pub fn child(&self) -> Context {
        Self {
            token: self.token.child(),
            values: self.values.clone(),
        }
    }

    /// A derived context carrying `value`, replacing a previous value of the
    /// same type.
    pub fn with_value<T: Any + Send + Sync>(&self, value: T) -> Context {
        let mut values = self.values.as_deref().cloned().unwrap_or_default();
        values.insert(TypeId::of::<T>(), Arc::new(value));
        Self {
            token: self.token.clone(),
            values: Some(Arc::new(values)),
        }
    }

    pub fn value<T: Any + Send + Sync>(&self) -> Option<&T> {
        self.values
            .as_ref()?
            .get(&TypeId::of::<T>())?
            .downcast_ref::<T>()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cancellation_propagates_to_children_and_callbacks() {
        let parent = CancellationToken::new();
        let child = parent.child();
        let grandchild = child.child();
        let fired = Arc::new(AtomicBool::new(false));
        let observed = fired.clone();
        grandchild.on_cancel(move || observed.store(true, Ordering::SeqCst));
        assert!(!grandchild.is_cancelled());
        parent.cancel();
        assert!(child.is_cancelled());
        assert!(grandchild.is_cancelled());
        assert!(fired.load(Ordering::SeqCst));
        assert!(parent.child().is_cancelled());
    }

    #[test]
    fn wait_timeout_wakes_on_cancel() {
        let token = CancellationToken::new();
        assert!(!token.wait_timeout(Duration::from_millis(5)));
        let remote = token.clone();
        let thread = std::thread::spawn(move || remote.wait_timeout(Duration::from_secs(10)));
        token.cancel();
        assert!(thread.join().unwrap());
    }

    #[test]
    fn context_values_are_typed_and_copy_on_write() {
        #[derive(Debug, PartialEq)]
        struct Marker(u32);
        let base = Context::background();
        let enriched = base.with_value(Marker(7));
        assert!(base.value::<Marker>().is_none());
        assert_eq!(enriched.value::<Marker>(), Some(&Marker(7)));
        assert_eq!(enriched.child().value::<Marker>(), Some(&Marker(7)));
    }
}
