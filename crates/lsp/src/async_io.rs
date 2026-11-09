//! Streams of the async protocol servers (`tower-lsp-server`, `rmcp`).
//!
//! A server reads an async reader and writes whole frames to an output
//! queue. Callers hand either blocking streams (stdio, test buffers), which
//! are served by one thread per direction, or Tokio streams (sockets,
//! in-memory pipes), which are used directly.
//!
//! Output goes through a queue of complete frames ([`FrameSender`]) that any
//! thread can feed: a large message costs one copy and one write instead of
//! many pipe-sized fragments, and frames from several senders never
//! interleave. The queue is flushed whenever it runs empty, so a peer sees
//! every message as soon as it is complete.

use std::io::{self, Read, Write};
use std::pin::Pin;
use std::task::{Context, Poll};
use std::thread;

use tokio::io::{AsyncRead, AsyncWrite, AsyncWriteExt};
use tokio::sync::mpsc::{UnboundedReceiver, UnboundedSender, unbounded_channel};
use tokio_util::io::SyncIoBridge;

/// Buffer of the pipe behind a blocking input, and the size of each read.
const PIPE_CAPACITY: usize = 64 * 1024;

/// A reader the async servers can use.
pub type AsyncInput = Box<dyn AsyncRead + Unpin + Send>;

/// Where a server reads its messages.
pub enum Input {
    /// Read by a dedicated thread.
    Blocking(Box<dyn Read + Send>),
    Async(AsyncInput),
}

/// Where a server writes its messages.
pub enum Output {
    /// Written by a dedicated thread.
    Blocking(Box<dyn Write + Send>),
    Async(Box<dyn AsyncWrite + Unpin + Send>),
}

/// Connects `input` and `output` for a server: its reader, the sender of its
/// output frames, and the completion of that output. Must run inside a
/// Tokio runtime. The output ends once every [`FrameSender`] (and
/// [`FrameWriter`]) is dropped.
pub fn connect(input: Input, output: Output) -> io::Result<(AsyncInput, FrameSender, OutputDone)> {
    let reader = reader(input)?;
    let (frames, done) = spawn_output(output)?;
    Ok((reader, frames, done))
}

/// An async reader of `input`. A blocking input is copied by a thread that
/// ends when the input closes or the reader is dropped.
pub fn reader(input: Input) -> io::Result<AsyncInput> {
    let input = match input {
        Input::Async(reader) => return Ok(reader),
        Input::Blocking(input) => input,
    };
    let handle = tokio::runtime::Handle::current();
    let (reader, input_end) = tokio::io::duplex(PIPE_CAPACITY);
    thread::Builder::new()
        .name("io-input".into())
        .spawn(move || {
            let mut input = input;
            let mut sink = SyncIoBridge::new_with_handle(input_end, handle);
            let mut buffer = vec![0; PIPE_CAPACITY];
            loop {
                let read = match input.read(&mut buffer) {
                    Ok(0) => break,
                    Ok(read) => read,
                    Err(error) if error.kind() == io::ErrorKind::Interrupted => continue,
                    Err(_) => break,
                };
                if sink.write_all(&buffer[..read]).is_err() {
                    break;
                }
            }
            let _ = sink.shutdown();
        })?;
    Ok(Box::new(reader))
}

/// Starts writing queued frames to `output`: on a thread for a blocking
/// output, on a task otherwise.
pub fn spawn_output(output: Output) -> io::Result<(FrameSender, OutputDone)> {
    let (sender, frames) = unbounded_channel();
    let done = match output {
        Output::Blocking(output) => {
            let (finished, done) = tokio::sync::oneshot::channel();
            thread::Builder::new()
                .name("io-output".into())
                .spawn(move || {
                    write_blocking(output, frames);
                    let _ = finished.send(());
                })?;
            OutputDone(Completion::Thread(done))
        }
        Output::Async(output) => {
            OutputDone(Completion::Task(tokio::spawn(write_async(output, frames))))
        }
    };
    Ok((FrameSender(sender), done))
}

fn write_blocking(mut output: Box<dyn Write + Send>, mut frames: UnboundedReceiver<Vec<u8>>) {
    while let Some(frame) = frames.blocking_recv() {
        if output.write_all(&frame).is_err() {
            return;
        }
        while let Ok(frame) = frames.try_recv() {
            if output.write_all(&frame).is_err() {
                return;
            }
        }
        if output.flush().is_err() {
            return;
        }
    }
}

async fn write_async(
    mut output: Box<dyn AsyncWrite + Unpin + Send>,
    mut frames: UnboundedReceiver<Vec<u8>>,
) {
    while let Some(frame) = frames.recv().await {
        if output.write_all(&frame).await.is_err() {
            return;
        }
        while let Ok(frame) = frames.try_recv() {
            if output.write_all(&frame).await.is_err() {
                return;
            }
        }
        if output.flush().await.is_err() {
            return;
        }
    }
    let _ = output.shutdown().await;
}

/// Completion of an output started by [`spawn_output`].
pub struct OutputDone(Completion);

enum Completion {
    Thread(tokio::sync::oneshot::Receiver<()>),
    Task(tokio::task::JoinHandle<()>),
}

impl OutputDone {
    /// Waits until every queued frame was written. Completes only after all
    /// senders are dropped.
    pub async fn wait(self) {
        match self.0 {
            Completion::Thread(done) => {
                let _ = done.await;
            }
            Completion::Task(task) => {
                let _ = task.await;
            }
        }
    }
}

/// Queues complete frames for an output, from any thread.
#[derive(Clone)]
pub struct FrameSender(UnboundedSender<Vec<u8>>);

impl FrameSender {
    /// Queues `frame`; false once the output has ended.
    pub fn send(&self, frame: Vec<u8>) -> bool {
        self.0.send(frame).is_ok()
    }
}

/// An `AsyncWrite` over a frame queue, for servers that write their own
/// messages: every write is queued whole. Shutting it down releases its
/// sender.
pub struct FrameWriter(Option<FrameSender>);

impl FrameWriter {
    pub fn new(frames: FrameSender) -> Self {
        Self(Some(frames))
    }
}

impl AsyncWrite for FrameWriter {
    fn poll_write(
        self: Pin<&mut Self>,
        _cx: &mut Context<'_>,
        buffer: &[u8],
    ) -> Poll<io::Result<usize>> {
        match &self.0 {
            Some(frames) if frames.send(buffer.to_vec()) => Poll::Ready(Ok(buffer.len())),
            _ => Poll::Ready(Err(io::ErrorKind::BrokenPipe.into())),
        }
    }

    fn poll_flush(self: Pin<&mut Self>, _cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        Poll::Ready(Ok(()))
    }

    fn poll_shutdown(mut self: Pin<&mut Self>, _cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        self.0 = None;
        Poll::Ready(Ok(()))
    }
}
