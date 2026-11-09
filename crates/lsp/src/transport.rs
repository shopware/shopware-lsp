//! JSON-RPC transport and the ordered request dispatcher.
//!
//! Input framing, the read loop and the routing of client responses come
//! from `tower-lsp-server` (its [`Server`] driving a raw [`tower::Service`]
//! over JSON-RPC requests); dispatch and output are our own:
//!
//! * the transport calls [`LspService::call`] once per message, in arrival
//!   order; `call` enqueues the message for **one worker thread**
//!   synchronously, so requests and notifications run strictly in arrival
//!   order and a `didChange` can never race a feature request;
//! * `$/cancelRequest` is handled in `call`, before the queue, so it cancels
//!   queued and running requests without waiting for them; a request
//!   cancelled before or during execution answers `-32800`;
//! * server-to-client requests and notifications are written by
//!   [`Connection`] the same way as responses, so the client sees them in
//!   the order the worker sent them; client responses come back through
//!   [`ChannelLoopback`] to the waiting [`Connection::call`];
//! * the worker writes each response itself, as one complete frame handed to
//!   the output thread: a large result (completion lists reach tens of
//!   megabytes) is serialized once, written without a detour through the
//!   runtime, and freed on the worker while the output writes it;
//! * the end of input cancels every queued and running request.
//!
//! The typed `LanguageServer` trait of `tower-lsp-server` is not used: it
//! would re-type every request and result and drop fields its types do not
//! model.

use std::collections::HashMap;
use std::convert::Infallible;
use std::io::{Read, Write};
use std::pin::Pin;
use std::sync::atomic::{AtomicBool, AtomicI64, Ordering};
use std::sync::{Arc, Mutex};
use std::task::{Context as TaskContext, Poll};
use std::thread;
use std::time::{Duration, Instant};

use crossbeam_channel::{Receiver, Sender, bounded, select, unbounded};
use futures::Sink;
use futures::future::{Ready, ready};
use serde::Serialize;
use serde_json::Value;
use tower_lsp_server::Loopback;
use tower_lsp_server::jsonrpc::{Error as JsonRpcError, ErrorCode, Id, Request, Response};

use crate::async_io::{FrameSender, FrameWriter, Input, Output};
use crate::context::{CancellationToken, RpcError, lock};

/// Error of an outgoing call.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum CallError {
    #[error("connection closed")]
    Closed,
    #[error("cancelled")]
    Cancelled,
    #[error("timed out")]
    Timeout,
    #[error("{0}")]
    Remote(RpcError),
    #[error("{0}")]
    Io(String),
}

/// The server-to-client half of a connection plus outgoing-call
/// bookkeeping. Usable from any thread.
pub(crate) struct Connection {
    /// The output, shared with response writing so everything the worker
    /// sends reaches the client in the order it was sent.
    frames: Mutex<Option<FrameSender>>,
    closed: AtomicBool,
    next_id: AtomicI64,
    pending: Mutex<HashMap<Id, Sender<Result<Value, CallError>>>>,
}

impl Connection {
    fn new() -> Self {
        Self {
            frames: Mutex::new(None),
            closed: AtomicBool::new(false),
            next_id: AtomicI64::new(1),
            pending: Mutex::new(HashMap::new()),
        }
    }

    fn send(&self, request: Request) -> Result<(), CallError> {
        if self.closed.load(Ordering::Acquire) {
            return Err(CallError::Closed);
        }
        let frame = encode_frame(&request).map_err(|error| CallError::Io(error.to_string()))?;
        let frames = lock(&self.frames).clone();
        match frames {
            Some(frames) if frames.send(frame) => Ok(()),
            _ => Err(CallError::Closed),
        }
    }

    fn message(method: &str, params: impl Serialize, id: Option<Id>) -> Result<Request, CallError> {
        let params =
            serde_json::to_value(params).map_err(|error| CallError::Io(error.to_string()))?;
        let mut builder = Request::build(method.to_owned());
        if !params.is_null() {
            builder = builder.params(params);
        }
        if let Some(id) = id {
            builder = builder.id(id);
        }
        Ok(builder.finish())
    }

    pub(crate) fn notify(&self, method: &str, params: impl Serialize) -> Result<(), CallError> {
        self.send(Self::message(method, params, None)?)
    }

    /// Sends a request and blocks until its response, cancellation or the
    /// optional timeout. Must not be called from an async task.
    pub(crate) fn call(
        &self,
        method: &str,
        params: impl Serialize,
        cancel: &CancellationToken,
        timeout: Option<Duration>,
    ) -> Result<Value, CallError> {
        if cancel.is_cancelled() {
            return Err(CallError::Cancelled);
        }
        let id = Id::Number(self.next_id.fetch_add(1, Ordering::Relaxed));
        let (sender, receiver) = bounded(1);
        lock(&self.pending).insert(id.clone(), sender);
        if let Err(error) =
            Self::message(method, params, Some(id.clone())).and_then(|request| self.send(request))
        {
            lock(&self.pending).remove(&id);
            return Err(error);
        }
        // Poll the token instead of registering a cancel callback, so
        // long-lived tokens (the server lifecycle) do not accumulate one
        // callback per call.
        const POLL: Duration = Duration::from_millis(25);
        let deadline = timeout.map(|timeout| Instant::now() + timeout);
        let result = loop {
            if cancel.is_cancelled() {
                break Err(CallError::Cancelled);
            }
            let mut wait = POLL;
            if let Some(deadline) = deadline {
                let now = Instant::now();
                if now >= deadline {
                    break Err(CallError::Timeout);
                }
                wait = wait.min(deadline - now);
            }
            match receiver.recv_timeout(wait) {
                Ok(result) => break result,
                Err(crossbeam_channel::RecvTimeoutError::Timeout) => {}
                Err(crossbeam_channel::RecvTimeoutError::Disconnected) => {
                    break Err(CallError::Closed);
                }
            }
        };
        lock(&self.pending).remove(&id);
        result
    }

    fn route_response(&self, response: Response) {
        let (id, body) = response.into_parts();
        let Some(sender) = lock(&self.pending).remove(&id) else {
            return;
        };
        let result = body.map_err(|error| {
            CallError::Remote(RpcError::new(
                error.code.code() as i32,
                error.message.into_owned(),
            ))
        });
        let _ = sender.try_send(result);
    }

    /// Marks the connection closed and fails pending calls.
    pub(crate) fn close(&self) {
        if self.closed.swap(true, Ordering::AcqRel) {
            return;
        }
        lock(&self.pending).clear();
        lock(&self.frames).take();
    }
}

/// One queued request or notification.
pub(crate) struct Work {
    pub id: Option<Id>,
    pub method: String,
    pub params: Value,
    pub cancel: CancellationToken,
}

/// Executes one unit of work; implemented by the server.
pub(crate) trait Handler: Send + Sync + 'static {
    /// Handles a request or notification. Notifications ignore the result.
    fn handle(&self, conn: &Arc<Connection>, work: &Work) -> Result<Value, RpcError>;

    /// Lets the server move selected work off the ordered worker (CLI pull
    /// diagnostics). Returns the work back when it must run in order.
    fn offload(&self, dispatcher: &Arc<Dispatcher>, work: Work) -> Option<Work>;
}

/// Tracks queued and running requests so they can be cancelled, and
/// answers them.
pub(crate) struct Dispatcher {
    root: CancellationToken,
    requests: Mutex<HashMap<Id, CancellationToken>>,
    /// Where responses go; `None` before serving starts and after it ended.
    frames: Mutex<Option<FrameSender>>,
    conn: Arc<Connection>,
    handler: Arc<dyn Handler>,
}

impl Dispatcher {
    fn cancel_request(&self, params: Option<&Value>) {
        let Some(id) = params.and_then(|params| params.get("id")) else {
            return;
        };
        let Ok(id) = serde_json::from_value::<Id>(id.clone()) else {
            return;
        };
        if let Some(cancel) = lock(&self.requests).get(&id) {
            cancel.cancel();
        }
    }

    /// Runs one unit of work and answers requests.
    pub(crate) fn execute(&self, work: Work) {
        let result = if work.cancel.is_cancelled() && work.id.is_some() {
            Err(RpcError::cancelled())
        } else {
            self.handler.handle(&self.conn, &work)
        };
        if let Some(id) = &work.id {
            lock(&self.requests).remove(id);
            let response = match result {
                Ok(value) => Response::from_ok(id.clone(), value),
                Err(error) => Response::from_error(id.clone(), json_rpc_error(error)),
            };
            self.respond(&response);
        } else if let Err(error) = result {
            log::warn!("notification {:?} failed: {}", work.method, error.message);
        }
        work.cancel.cancel();
    }
}

impl Dispatcher {
    /// Writes `response` as one frame. The caller drops it afterwards, so a
    /// large result is freed while the output thread writes it.
    fn respond(&self, response: &Response) {
        let frame = match encode_frame(response) {
            Ok(frame) => frame,
            Err(error) => {
                log::warn!("encode a response: {error}");
                return;
            }
        };
        let frames = lock(&self.frames).clone();
        if let Some(frames) = frames {
            frames.send(frame);
        }
    }
}

/// One message with its `Content-Length` header, as the codec of
/// `tower-lsp-server` frames it.
fn encode_frame(message: &impl Serialize) -> serde_json::Result<Vec<u8>> {
    let body = serde_json::to_vec(message)?;
    let header = format!("Content-Length: {}\r\n\r\n", body.len());
    let mut frame = Vec::with_capacity(header.len() + body.len());
    frame.extend_from_slice(header.as_bytes());
    frame.extend_from_slice(&body);
    Ok(frame)
}

fn json_rpc_error(error: RpcError) -> JsonRpcError {
    JsonRpcError {
        code: ErrorCode::from(i64::from(error.code)),
        message: error.message.into(),
        data: None,
    }
}

/// The `tower::Service` the transport calls once per incoming message, in
/// arrival order.
struct LspService {
    dispatcher: Arc<Dispatcher>,
    queue: Sender<Work>,
    /// Set once `exit` was queued: the transport stops reading there, and
    /// the worker still runs everything before it in order.
    exited: Arc<AtomicBool>,
}

impl tower::Service<Request> for LspService {
    type Response = Option<Response>;
    type Error = Infallible;
    type Future = Ready<Result<Option<Response>, Infallible>>;

    fn poll_ready(&mut self, _cx: &mut TaskContext<'_>) -> Poll<Result<(), Infallible>> {
        Poll::Ready(Ok(()))
    }

    fn call(&mut self, request: Request) -> Self::Future {
        let (method, id, params) = request.into_parts();
        if method == "$/cancelRequest" {
            self.dispatcher.cancel_request(params.as_ref());
            return ready(Ok(None));
        }
        if method == "exit" {
            self.exited.store(true, Ordering::Release);
        }
        let cancel = self.dispatcher.root.child();
        if let Some(id) = &id {
            lock(&self.dispatcher.requests).insert(id.clone(), cancel.clone());
        }
        let work = Work {
            id: id.clone(),
            method: method.into_owned(),
            params: params.unwrap_or(Value::Null),
            cancel,
        };
        // Enqueued synchronously: the worker sees messages in arrival order
        // and answers requests itself (`Dispatcher::respond`).
        if self.queue.send(work).is_err() {
            return ready(Ok(id.map(|id| {
                lock(&self.dispatcher.requests).remove(&id);
                Response::from_error(id, json_rpc_error(RpcError::cancelled()))
            })));
        }
        ready(Ok(None))
    }
}

/// Client responses routed back to [`Connection::call`]. Server-to-client
/// messages do not use its request stream: [`Connection`] writes them as
/// frames, in order with the responses.
struct ChannelLoopback {
    conn: Arc<Connection>,
}

struct ResponseRouter(Arc<Connection>);

impl Sink<Response> for ResponseRouter {
    type Error = Infallible;

    fn poll_ready(self: Pin<&mut Self>, _cx: &mut TaskContext<'_>) -> Poll<Result<(), Infallible>> {
        Poll::Ready(Ok(()))
    }

    fn start_send(self: Pin<&mut Self>, response: Response) -> Result<(), Infallible> {
        self.0.route_response(response);
        Ok(())
    }

    fn poll_flush(self: Pin<&mut Self>, _cx: &mut TaskContext<'_>) -> Poll<Result<(), Infallible>> {
        Poll::Ready(Ok(()))
    }

    fn poll_close(self: Pin<&mut Self>, _cx: &mut TaskContext<'_>) -> Poll<Result<(), Infallible>> {
        Poll::Ready(Ok(()))
    }
}

impl Loopback for ChannelLoopback {
    type RequestStream = futures::stream::Pending<Request>;
    type ResponseSink = ResponseRouter;

    fn split(self) -> (Self::RequestStream, Self::ResponseSink) {
        (futures::stream::pending(), ResponseRouter(self.conn))
    }
}

/// Serves one connection on blocking streams, on a runtime of its own, until
/// the client disconnects or sends `exit`. Returns after every queued and
/// running request finished.
pub(crate) fn serve_blocking(
    input: impl Read + Send + 'static,
    output: impl Write + Send + 'static,
    handler: Arc<dyn Handler>,
    on_connect: impl FnOnce(&Arc<Connection>),
) -> Arc<Connection> {
    let executor = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(2)
        .thread_name("lsp-io")
        .enable_all()
        .build()
        .expect("build LSP I/O runtime");
    let conn = executor.block_on(serve(
        Input::Blocking(Box::new(input)),
        Output::Blocking(Box::new(output)),
        handler,
        on_connect,
        std::future::pending(),
    ));
    // The input thread may still block in a read the client never ends.
    executor.shutdown_background();
    conn
}

/// Serves one connection until the client disconnects, sends `exit`, or
/// `stop` completes. Returns after every queued and running request
/// finished and the output was written.
pub(crate) async fn serve(
    input: Input,
    output: Output,
    handler: Arc<dyn Handler>,
    on_connect: impl FnOnce(&Arc<Connection>),
    stop: impl Future<Output = ()>,
) -> Arc<Connection> {
    let conn = Arc::new(Connection::new());
    on_connect(&conn);
    let (reader, frames, output_done) = match crate::async_io::connect(input, output) {
        Ok(connected) => connected,
        Err(error) => {
            log::warn!("connect the streams: {error}");
            conn.close();
            return conn;
        }
    };
    *lock(&conn.frames) = Some(frames.clone());
    let dispatcher = Arc::new(Dispatcher {
        root: CancellationToken::new(),
        requests: Mutex::new(HashMap::new()),
        frames: Mutex::new(Some(frames.clone())),
        conn: conn.clone(),
        handler,
    });
    let (queue_sender, queue) = unbounded::<Work>();
    let (stop_sender, stop_worker) = bounded::<()>(1);
    let worker = {
        let dispatcher = dispatcher.clone();
        thread::Builder::new()
            .name("lsp-worker".into())
            .spawn(move || run_worker(&dispatcher, &queue, &stop_worker))
    };
    let worker = match worker {
        Ok(worker) => worker,
        Err(error) => {
            log::warn!("start the worker: {error}");
            conn.close();
            lock(&dispatcher.frames).take();
            drop(frames);
            output_done.wait().await;
            return conn;
        }
    };
    let exited = Arc::new(AtomicBool::new(false));
    let service = LspService {
        dispatcher: dispatcher.clone(),
        queue: queue_sender,
        exited: exited.clone(),
    };
    let loopback = ChannelLoopback { conn: conn.clone() };
    let server = tower_lsp_server::Server::new(reader, FrameWriter::new(frames), loopback);
    let stopped = tokio::select! {
        () = server.serve(service) => false,
        () = stop => true,
    };
    // Serving ended and the queue is closed. After `exit` the worker runs
    // what came before it, in order, and stops at the closed queue. The end
    // of input or a stop cancels everything still queued or running
    // instead, as nobody waits for those answers.
    if stopped || !exited.load(Ordering::Acquire) {
        conn.close();
        dispatcher.root.cancel();
        let _ = stop_sender.try_send(());
    }
    // The output ends once the worker's frame sender is gone too.
    let joined = {
        let dispatcher = dispatcher.clone();
        let conn = conn.clone();
        tokio::task::spawn_blocking(move || {
            let _ = worker.join();
            conn.close();
            dispatcher.root.cancel();
            lock(&dispatcher.frames).take();
        })
    };
    let _ = joined.await;
    output_done.wait().await;
    conn
}

fn run_worker(dispatcher: &Arc<Dispatcher>, queue: &Receiver<Work>, stop: &Receiver<()>) {
    loop {
        if dispatcher.root.is_cancelled() {
            // Queued requests still get an answer (cancelled); queued
            // notifications are dropped.
            while let Ok(work) = queue.try_recv() {
                if work.id.is_some() {
                    dispatcher.execute(work);
                }
            }
            return;
        }
        let work = select! {
            recv(queue) -> work => match work {
                Ok(work) => work,
                Err(_) => return,
            },
            recv(stop) -> _ => return,
        };
        if let Some(work) = dispatcher.handler.offload(dispatcher, work) {
            dispatcher.execute(work);
        }
    }
}
