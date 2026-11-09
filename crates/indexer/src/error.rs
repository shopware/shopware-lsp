//! Error type and cooperative cancellation shared by the indexing layer.

use std::fmt;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};

/// Errors produced by stores, repositories, indexers and the scanner.
#[derive(Debug, thiserror::Error)]
pub enum Error {
    /// The operation observed a cancelled [`CancelToken`].
    #[error("operation cancelled")]
    Cancelled,
    #[error(transparent)]
    Sqlite(#[from] rusqlite::Error),
    #[error(transparent)]
    Io(#[from] std::io::Error),
    #[error("failed to marshal item: {0}")]
    Encode(#[from] rmp_serde::encode::Error),
    #[error("failed to unmarshal item: {0}")]
    Decode(#[from] rmp_serde::decode::Error),
    #[error("{0}")]
    Message(String),
    /// An error wrapped with operation context; displays as `context: source`.
    #[error("{context}: {source}")]
    Context {
        context: String,
        #[source]
        source: Box<Error>,
    },
    /// Several independent failures, displayed one per line.
    #[error("{}", join_messages(.0))]
    Multiple(Vec<Error>),
    /// An error from a domain crate.
    #[error(transparent)]
    Other(Box<dyn std::error::Error + Send + Sync>),
}

fn join_messages(errors: &[Error]) -> String {
    errors
        .iter()
        .map(ToString::to_string)
        .collect::<Vec<_>>()
        .join("\n")
}

impl Error {
    pub fn msg(message: impl Into<String>) -> Self {
        Error::Message(message.into())
    }

    pub fn other(error: impl std::error::Error + Send + Sync + 'static) -> Self {
        Error::Other(Box::new(error))
    }

    pub fn context(self, context: impl Into<String>) -> Self {
        Error::Context {
            context: context.into(),
            source: Box::new(self),
        }
    }

    /// Joins errors: `None` for no errors, the error
    /// itself for exactly one, [`Error::Multiple`] otherwise.
    pub fn join(errors: impl IntoIterator<Item = Error>) -> Option<Error> {
        let mut errors: Vec<Error> = errors.into_iter().collect();
        match errors.len() {
            0 => None,
            1 => errors.pop(),
            _ => Some(Error::Multiple(errors)),
        }
    }

    /// Reports whether this error or any wrapped error is a cancellation.
    pub fn is_cancelled(&self) -> bool {
        self.any(&|error| matches!(error, Error::Cancelled))
    }

    /// Visits this error and every wrapped [`Error`].
    pub fn any(&self, predicate: &dyn Fn(&Error) -> bool) -> bool {
        if predicate(self) {
            return true;
        }
        match self {
            Error::Context { source, .. } => source.any(predicate),
            Error::Multiple(errors) => errors.iter().any(|error| error.any(predicate)),
            _ => false,
        }
    }
}

impl From<swlsp_pathmatch::CompileError> for Error {
    fn from(error: swlsp_pathmatch::CompileError) -> Self {
        Error::other(error)
    }
}

impl From<swlsp_phar::PharError> for Error {
    fn from(error: swlsp_phar::PharError) -> Self {
        Error::other(error)
    }
}

pub type Result<T, E = Error> = std::result::Result<T, E>;

/// Adds operation context to fallible results.
pub trait ResultExt<T> {
    fn context(self, context: impl Into<String>) -> Result<T>;
    fn with_context<C: Into<String>>(self, context: impl FnOnce() -> C) -> Result<T>;
}

impl<T, E: Into<Error>> ResultExt<T> for std::result::Result<T, E> {
    fn context(self, context: impl Into<String>) -> Result<T> {
        self.map_err(|error| error.into().context(context))
    }

    fn with_context<C: Into<String>>(self, context: impl FnOnce() -> C) -> Result<T> {
        self.map_err(|error| error.into().context(context()))
    }
}

/// Cooperative cancellation shared by a scanner run and its workers.
#[derive(Clone, Default)]
pub struct CancelToken(Arc<AtomicBool>);

impl CancelToken {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn cancel(&self) {
        self.0.store(true, Ordering::Release);
    }

    pub fn is_cancelled(&self) -> bool {
        self.0.load(Ordering::Acquire)
    }

    /// `Err(Error::Cancelled)` once cancelled.
    pub fn check(&self) -> Result<()> {
        if self.is_cancelled() {
            Err(Error::Cancelled)
        } else {
            Ok(())
        }
    }
}

impl fmt::Debug for CancelToken {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_tuple("CancelToken")
            .field(&self.is_cancelled())
            .finish()
    }
}
