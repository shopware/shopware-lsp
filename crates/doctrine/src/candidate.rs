//! Cheap byte-level screening of files that may register DBAL types (port of
//! Go `candidate.go`): an ASCII case-folding Aho-Corasick automaton.

use std::sync::LazyLock;

use swlsp_indexer::ParsedFile;

pub(crate) type Flags = u16;

pub(crate) const TYPE_CANDIDATE: Flags = 1;
const EXTENSION_MARKER: Flags = 1 << 1;
const NAME_MARKER: Flags = 1 << 2;
const DBAL_MARKER: Flags = 1 << 3;
const TYPES_MARKER: Flags = 1 << 4;
const TYPE_TAG_MARKER: Flags = 1 << 5;

#[derive(Clone)]
struct State {
    next: [u16; 128],
    flags: Flags,
}

impl Default for State {
    fn default() -> Self {
        State {
            next: [0; 128],
            flags: 0,
        }
    }
}

struct Matcher {
    states: Vec<State>,
}

impl Matcher {
    fn new(patterns: &[(&str, Flags)]) -> Self {
        let mut states = vec![State::default()];
        for (text, flags) in patterns {
            let mut state = 0usize;
            for &byte in text.as_bytes() {
                let value = byte.to_ascii_lowercase();
                assert!(value < 128, "Doctrine candidate patterns must be ASCII");
                let mut next = states[state].next[value as usize];
                if next == 0 {
                    states.push(State::default());
                    next = u16::try_from(states.len() - 1).expect("too many states");
                    states[state].next[value as usize] = next;
                }
                state = next as usize;
            }
            states[state].flags |= flags;
        }
        let mut failures = vec![0u16; states.len()];
        let mut queue: Vec<u16> = (0..128)
            .map(|value| states[0].next[value])
            .filter(|&state| state != 0)
            .collect();
        let mut head = 0;
        while head < queue.len() {
            let state = queue[head] as usize;
            head += 1;
            let failure = failures[state] as usize;
            for value in 0..128 {
                let next = states[state].next[value];
                if next == 0 {
                    states[state].next[value] = states[failure].next[value];
                    continue;
                }
                queue.push(next);
                failures[next as usize] = states[failure].next[value];
                let inherited = states[failures[next as usize] as usize].flags;
                states[next as usize].flags |= inherited;
            }
        }
        Matcher { states }
    }

    fn matches(&self, content: &[u8]) -> Flags {
        let mut result = 0;
        let mut state = 0usize;
        for &byte in content {
            let value = byte.to_ascii_lowercase();
            if value >= 128 {
                state = 0;
                continue;
            }
            state = self.states[state].next[value as usize] as usize;
            result |= self.states[state].flags;
        }
        result
    }
}

static PHP: LazyLock<Matcher> = LazyLock::new(|| {
    Matcher::new(&[
        ("addtype", TYPE_CANDIDATE),
        ("overridetype", TYPE_CANDIDATE),
        ("gettyperegistry", TYPE_CANDIDATE),
        ("extension", EXTENSION_MARKER),
        ("doctrine", NAME_MARKER),
        ("dbal", DBAL_MARKER),
        ("types", TYPES_MARKER),
    ])
});

static YAML: LazyLock<Matcher> = LazyLock::new(|| {
    Matcher::new(&[
        ("doctrine:", NAME_MARKER),
        ("dbal:", DBAL_MARKER),
        ("types:", TYPES_MARKER),
    ])
});

static XML: LazyLock<Matcher> = LazyLock::new(|| {
    Matcher::new(&[
        ("dbal", DBAL_MARKER),
        ("<doctrine:type", TYPE_TAG_MARKER),
        ("<type", TYPE_TAG_MARKER),
    ])
});

fn all(markers: Flags, required: Flags) -> bool {
    markers & required == required
}

/// `TYPE_CANDIDATE` when the file may contain a DBAL type registration.
pub(crate) fn doctrine_candidates(file: &ParsedFile) -> Flags {
    let content = file.content();
    let mut markers = match file.extension() {
        ".php" => {
            let mut markers = PHP.matches(content);
            if markers & TYPE_CANDIDATE == 0
                && all(
                    markers,
                    EXTENSION_MARKER | NAME_MARKER | DBAL_MARKER | TYPES_MARKER,
                )
            {
                markers |= TYPE_CANDIDATE;
            }
            markers
        }
        ".yaml" | ".yml" => {
            let mut markers = YAML.matches(content);
            if all(markers, NAME_MARKER | DBAL_MARKER | TYPES_MARKER) {
                markers |= TYPE_CANDIDATE;
            }
            markers
        }
        ".xml" => {
            let mut markers = XML.matches(content);
            if all(markers, DBAL_MARKER | TYPE_TAG_MARKER) {
                markers |= TYPE_CANDIDATE;
            }
            markers
        }
        _ => 0,
    };
    markers &= TYPE_CANDIDATE;
    markers
}
