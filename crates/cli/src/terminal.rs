//! Terminal presentation: colors and the live status line on stderr.

use std::io::Write;
use std::sync::{Arc, Condvar, Mutex, MutexGuard};
use std::thread::JoinHandle;
use std::time::{Duration, Instant};

use crate::{CliError, Output};

pub(crate) const RED: &str = "31";
pub(crate) const GREEN: &str = "32";
pub(crate) const YELLOW: &str = "33";
pub(crate) const BLUE: &str = "34";
pub(crate) const CYAN: &str = "36";
pub(crate) const BOLD: &str = "1";
pub(crate) const DIM: &str = "2";
pub(crate) const HEADER: &str = "1;4";

/// `--color`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum ColorChoice {
    Auto,
    Always,
    Never,
}

impl ColorChoice {
    pub(crate) fn parse(value: Option<&str>) -> Result<Self, CliError> {
        match value.map(str::trim) {
            None | Some("" | "auto") => Ok(Self::Auto),
            Some("always") => Ok(Self::Always),
            Some("never") => Ok(Self::Never),
            Some(other) => Err(CliError::usage(format!(
                "unknown --color value {other:?}: expected auto, always or never"
            ))),
        }
    }

    /// The style of text written to `output`. `auto` colors terminals only;
    /// `NO_COLOR` turns colors off and `FORCE_COLOR` / `CLICOLOR_FORCE`
    /// turn them on wherever the output goes.
    pub(crate) fn style(self, output: &Output) -> Style {
        let set = |name: &str| std::env::var_os(name).is_some_and(|value| !value.is_empty());
        let forced = |name: &str| {
            std::env::var_os(name).is_some_and(|value| value != "0" && !value.is_empty())
        };
        let color = match self {
            Self::Always => true,
            Self::Never => false,
            Self::Auto if set("NO_COLOR") => false,
            Self::Auto if forced("FORCE_COLOR") || forced("CLICOLOR_FORCE") => true,
            Self::Auto => output.is_terminal(),
        };
        Style { color }
    }
}

/// Whether text is colored.
#[derive(Debug, Clone, Copy, Default)]
pub(crate) struct Style {
    color: bool,
}

impl Style {
    #[cfg(test)]
    pub(crate) fn colored() -> Self {
        Self { color: true }
    }

    /// `text` in the SGR attributes `code`, or as is without colors.
    pub(crate) fn paint(self, code: &str, text: &str) -> String {
        if self.color && !text.is_empty() {
            format!("\x1b[{code}m{text}\x1b[0m")
        } else {
            text.to_owned()
        }
    }
}

/// A short duration: `420ms`, `3.2s`, `1m 12s`.
pub(crate) fn short_duration(duration: Duration) -> String {
    let millis = duration.as_millis();
    if millis < 1000 {
        format!("{millis}ms")
    } else if millis < 60_000 {
        format!("{:.1}s", duration.as_secs_f64())
    } else {
        let seconds = duration.as_secs();
        format!("{}m {}s", seconds / 60, seconds % 60)
    }
}

const FRAMES: [&str; 10] = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];
const TICK: Duration = Duration::from_millis(80);
const ERASE: &str = "\r\x1b[2K";

struct State {
    message: String,
    started: Instant,
    drawn: bool,
    done: bool,
}

struct Shared {
    out: Output,
    style: Style,
    state: Mutex<State>,
    wake: Condvar,
}

impl Shared {
    fn lock(&self) -> MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(|p| p.into_inner())
    }

    fn erase(&self, state: &mut State) {
        if state.drawn {
            let _ = self.out.clone().write_all(ERASE.as_bytes());
            state.drawn = false;
        }
    }

    fn draw(&self, state: &mut State) {
        let elapsed = state.started.elapsed();
        let frame = FRAMES[(elapsed.as_millis() / TICK.as_millis()) as usize % FRAMES.len()];
        let line = format!(
            "{ERASE}{} {} {}",
            self.style.paint(CYAN, frame),
            state.message,
            self.style.paint(DIM, &short_duration(elapsed)),
        );
        let _ = self.out.clone().write_all(line.as_bytes());
        state.drawn = true;
    }
}

/// A live status line: a spinner, a message and the elapsed time, redrawn
/// in place until finished. It is only shown on a terminal; elsewhere every
/// method is a no-op.
pub(crate) struct Status {
    shared: Option<Arc<Shared>>,
    ticker: Option<JoinHandle<()>>,
}

impl Status {
    pub(crate) fn disabled() -> Self {
        Self {
            shared: None,
            ticker: None,
        }
    }

    pub(crate) fn start(out: &Output, style: Style, message: impl Into<String>) -> Self {
        if !out.is_terminal() {
            return Self::disabled();
        }
        let shared = Arc::new(Shared {
            out: out.clone(),
            style,
            state: Mutex::new(State {
                message: message.into(),
                started: Instant::now(),
                drawn: false,
                done: false,
            }),
            wake: Condvar::new(),
        });
        let ticker = {
            let shared = shared.clone();
            std::thread::Builder::new()
                .name("status".into())
                .spawn(move || {
                    let mut state = shared.lock();
                    while !state.done {
                        shared.draw(&mut state);
                        state = shared
                            .wake
                            .wait_timeout(state, TICK)
                            .map(|(state, _)| state)
                            .unwrap_or_else(|p| p.into_inner().0);
                    }
                    shared.erase(&mut state);
                })
                .ok()
        };
        Self {
            shared: Some(shared),
            ticker,
        }
    }

    pub(crate) fn is_live(&self) -> bool {
        self.shared.is_some()
    }

    pub(crate) fn set_message(&self, message: impl Into<String>) {
        if let Some(shared) = &self.shared {
            shared.lock().message = message.into();
        }
    }

    /// Runs `write` with the status line erased, so its output does not
    /// land in the middle of it; the next tick redraws the line.
    pub(crate) fn suspend<T>(&self, write: impl FnOnce() -> T) -> T {
        let Some(shared) = &self.shared else {
            return write();
        };
        let mut state = shared.lock();
        shared.erase(&mut state);
        write()
    }

    /// Erases the line and stops redrawing.
    pub(crate) fn finish(&mut self) {
        if let Some(shared) = self.shared.take() {
            shared.lock().done = true;
            shared.wake.notify_all();
            if let Some(ticker) = self.ticker.take() {
                let _ = ticker.join();
            }
        }
    }
}

impl Drop for Status {
    fn drop(&mut self) {
        self.finish();
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn formats_durations() {
        assert_eq!(short_duration(Duration::from_millis(420)), "420ms");
        assert_eq!(short_duration(Duration::from_millis(3240)), "3.2s");
        assert_eq!(short_duration(Duration::from_secs(72)), "1m 12s");
    }

    #[test]
    fn colors_only_when_enabled() {
        assert_eq!(Style::default().paint(RED, "x"), "x");
        assert_eq!(Style::colored().paint(RED, "x"), "\x1b[31mx\x1b[0m");
        let buffer = crate::Buffer::new();
        assert_eq!(
            ColorChoice::Always.style(&buffer.output()).paint(DIM, "x"),
            "\x1b[2mx\x1b[0m"
        );
        assert_eq!(
            ColorChoice::Never.style(&buffer.output()).paint(DIM, "x"),
            "x"
        );
        assert!(ColorChoice::parse(Some("sometimes")).is_err());
    }

    #[test]
    fn a_status_off_a_terminal_writes_nothing() {
        let buffer = crate::Buffer::new();
        let mut status = Status::start(&buffer.output(), Style::default(), "Indexing");
        assert!(!status.is_live());
        status.set_message("Checking");
        assert_eq!(status.suspend(|| 1), 1);
        status.finish();
        assert_eq!(buffer.contents(), "");
    }
}
