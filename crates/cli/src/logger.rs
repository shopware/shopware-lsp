//! A process-wide logger matching Go's `log` package with no flags: one
//! message per line, written to a switchable sink (stderr, a log file, or
//! nowhere). Info and above correspond to Go's `log.Printf` calls.

use std::io::Write;
use std::sync::{Mutex, Once};

use crate::Output;

pub enum LogSink {
    Discard,
    Writer(Output),
    File(std::fs::File),
}

static SINK: Mutex<Option<LogSink>> = Mutex::new(None);
static INSTALL: Once = Once::new();

struct CliLogger;

impl log::Log for CliLogger {
    fn enabled(&self, metadata: &log::Metadata<'_>) -> bool {
        metadata.level() <= log::Level::Info
    }

    fn log(&self, record: &log::Record<'_>) {
        if !self.enabled(record.metadata()) {
            return;
        }
        let mut message = record.args().to_string();
        if !message.ends_with('\n') {
            message.push('\n');
        }
        let mut sink = SINK.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        match sink.as_mut() {
            Some(LogSink::Writer(output)) => {
                let _ = output.write_all(message.as_bytes());
            }
            Some(LogSink::File(file)) => {
                let _ = file.write_all(message.as_bytes());
            }
            Some(LogSink::Discard) | None => {}
        }
    }

    fn flush(&self) {}
}

pub fn install_logger() {
    INSTALL.call_once(|| {
        if log::set_boxed_logger(Box::new(CliLogger)).is_ok() {
            log::set_max_level(log::LevelFilter::Info);
        }
    });
}

pub fn set_log_sink(sink: LogSink) {
    *SINK.lock().unwrap_or_else(|poisoned| poisoned.into_inner()) = Some(sink);
}
