//! A port of the parts of `github.com/pmezard/go-difflib` the CLI uses:
//! Python-style `SequenceMatcher` (autojunk on, no junk predicate) and
//! `GetUnifiedDiffString`, so previews are byte-identical to Go.

use std::collections::HashMap;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct Match {
    a: usize,
    b: usize,
    size: usize,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct OpCode {
    tag: u8,
    i1: usize,
    i2: usize,
    j1: usize,
    j2: usize,
}

struct SequenceMatcher<'a> {
    a: &'a [String],
    b: &'a [String],
    b2j: HashMap<&'a str, Vec<usize>>,
}

impl<'a> SequenceMatcher<'a> {
    fn new(a: &'a [String], b: &'a [String]) -> Self {
        let mut b2j: HashMap<&'a str, Vec<usize>> = HashMap::new();
        for (index, line) in b.iter().enumerate() {
            b2j.entry(line.as_str()).or_default().push(index);
        }
        let n = b.len();
        if n >= 200 {
            let ntest = n / 100 + 1;
            b2j.retain(|_, indices| indices.len() <= ntest);
        }
        Self { a, b, b2j }
    }

    fn find_longest_match(&self, alo: usize, ahi: usize, blo: usize, bhi: usize) -> Match {
        let (mut best_i, mut best_j, mut best_size) = (alo, blo, 0usize);
        let mut j2len: HashMap<usize, usize> = HashMap::new();
        for i in alo..ahi {
            let mut new_j2len: HashMap<usize, usize> = HashMap::new();
            if let Some(indices) = self.b2j.get(self.a[i].as_str()) {
                for &j in indices {
                    if j < blo {
                        continue;
                    }
                    if j >= bhi {
                        break;
                    }
                    let k = j
                        .checked_sub(1)
                        .and_then(|p| j2len.get(&p))
                        .copied()
                        .unwrap_or(0)
                        + 1;
                    new_j2len.insert(j, k);
                    if k > best_size {
                        best_i = i + 1 - k;
                        best_j = j + 1 - k;
                        best_size = k;
                    }
                }
            }
            j2len = new_j2len;
        }
        while best_i > alo && best_j > blo && self.a[best_i - 1] == self.b[best_j - 1] {
            best_i -= 1;
            best_j -= 1;
            best_size += 1;
        }
        while best_i + best_size < ahi
            && best_j + best_size < bhi
            && self.a[best_i + best_size] == self.b[best_j + best_size]
        {
            best_size += 1;
        }
        Match {
            a: best_i,
            b: best_j,
            size: best_size,
        }
    }

    fn matching_blocks(&self) -> Vec<Match> {
        let mut matched = Vec::new();
        let mut stack = vec![(0, self.a.len(), 0, self.b.len())];
        // Go recurses left, appends, then recurses right; emulate that order
        // with an explicit work list of pending ranges and emitted matches.
        enum Work {
            Range(usize, usize, usize, usize),
            Emit(Match),
        }
        let mut work = vec![Work::Range(0, self.a.len(), 0, self.b.len())];
        stack.clear();
        while let Some(item) = work.pop() {
            match item {
                Work::Emit(found) => matched.push(found),
                Work::Range(alo, ahi, blo, bhi) => {
                    let found = self.find_longest_match(alo, ahi, blo, bhi);
                    let (i, j, k) = (found.a, found.b, found.size);
                    if k > 0 {
                        if i + k < ahi && j + k < bhi {
                            work.push(Work::Range(i + k, ahi, j + k, bhi));
                        }
                        work.push(Work::Emit(found));
                        if alo < i && blo < j {
                            work.push(Work::Range(alo, i, blo, j));
                        }
                    }
                }
            }
        }
        let mut non_adjacent = Vec::new();
        let (mut i1, mut j1, mut k1) = (0, 0, 0);
        for block in matched {
            if i1 + k1 == block.a && j1 + k1 == block.b {
                k1 += block.size;
            } else {
                if k1 > 0 {
                    non_adjacent.push(Match {
                        a: i1,
                        b: j1,
                        size: k1,
                    });
                }
                (i1, j1, k1) = (block.a, block.b, block.size);
            }
        }
        if k1 > 0 {
            non_adjacent.push(Match {
                a: i1,
                b: j1,
                size: k1,
            });
        }
        non_adjacent.push(Match {
            a: self.a.len(),
            b: self.b.len(),
            size: 0,
        });
        non_adjacent
    }

    fn op_codes(&self) -> Vec<OpCode> {
        let (mut i, mut j) = (0, 0);
        let mut codes = Vec::new();
        for block in self.matching_blocks() {
            let (ai, bj, size) = (block.a, block.b, block.size);
            let tag = if i < ai && j < bj {
                b'r'
            } else if i < ai {
                b'd'
            } else if j < bj {
                b'i'
            } else {
                0
            };
            if tag > 0 {
                codes.push(OpCode {
                    tag,
                    i1: i,
                    i2: ai,
                    j1: j,
                    j2: bj,
                });
            }
            i = ai + size;
            j = bj + size;
            if size > 0 {
                codes.push(OpCode {
                    tag: b'e',
                    i1: ai,
                    i2: i,
                    j1: bj,
                    j2: j,
                });
            }
        }
        codes
    }

    fn grouped_op_codes(&self, n: usize) -> Vec<Vec<OpCode>> {
        let mut codes = self.op_codes();
        if codes.is_empty() {
            codes = vec![OpCode {
                tag: b'e',
                i1: 0,
                i2: 1,
                j1: 0,
                j2: 1,
            }];
        }
        if codes[0].tag == b'e' {
            let c = codes[0];
            codes[0] = OpCode {
                tag: c.tag,
                i1: c.i1.max(c.i2.saturating_sub(n)),
                i2: c.i2,
                j1: c.j1.max(c.j2.saturating_sub(n)),
                j2: c.j2,
            };
        }
        let last = codes.len() - 1;
        if codes[last].tag == b'e' {
            let c = codes[last];
            codes[last] = OpCode {
                tag: c.tag,
                i1: c.i1,
                i2: c.i2.min(c.i1 + n),
                j1: c.j1,
                j2: c.j2.min(c.j1 + n),
            };
        }
        let nn = n + n;
        let mut groups = Vec::new();
        let mut group = Vec::new();
        for c in codes {
            let (mut i1, i2, mut j1, j2) = (c.i1, c.i2, c.j1, c.j2);
            if c.tag == b'e' && i2 - i1 > nn {
                group.push(OpCode {
                    tag: c.tag,
                    i1,
                    i2: i2.min(i1 + n),
                    j1,
                    j2: j2.min(j1 + n),
                });
                groups.push(std::mem::take(&mut group));
                i1 = i1.max(i2.saturating_sub(n));
                j1 = j1.max(j2.saturating_sub(n));
            }
            group.push(OpCode {
                tag: c.tag,
                i1,
                i2,
                j1,
                j2,
            });
        }
        if !group.is_empty() && !(group.len() == 1 && group[0].tag == b'e') {
            groups.push(group);
        }
        groups
    }
}

fn format_range_unified(start: usize, stop: usize) -> String {
    let mut beginning = start + 1;
    let length = stop - start;
    if length == 1 {
        return beginning.to_string();
    }
    if length == 0 {
        beginning -= 1;
    }
    format!("{beginning},{length}")
}

/// Go `difflib.SplitLines`.
pub fn split_lines(text: &str) -> Vec<String> {
    let mut lines: Vec<String> = text.split_inclusive('\n').map(str::to_owned).collect();
    if text.is_empty() || text.ends_with('\n') {
        lines.push(String::new());
    }
    if let Some(last) = lines.last_mut() {
        last.push('\n');
    }
    lines
}

/// Go `difflib.GetUnifiedDiffString` with `Context` lines of context.
pub fn unified_diff(
    a: &[String],
    b: &[String],
    from_file: &str,
    to_file: &str,
    context: usize,
) -> String {
    let matcher = SequenceMatcher::new(a, b);
    let mut output = String::new();
    let mut started = false;
    for group in matcher.grouped_op_codes(context) {
        if !started {
            started = true;
            if !from_file.is_empty() || !to_file.is_empty() {
                output.push_str(&format!("--- {from_file}\n+++ {to_file}\n"));
            }
        }
        let (first, last) = (group[0], group[group.len() - 1]);
        output.push_str(&format!(
            "@@ -{} +{} @@\n",
            format_range_unified(first.i1, last.i2),
            format_range_unified(first.j1, last.j2)
        ));
        for code in &group {
            if code.tag == b'e' {
                for line in &a[code.i1..code.i2] {
                    output.push(' ');
                    output.push_str(line);
                }
                continue;
            }
            if code.tag == b'r' || code.tag == b'd' {
                for line in &a[code.i1..code.i2] {
                    output.push('-');
                    output.push_str(line);
                }
            }
            if code.tag == b'r' || code.tag == b'i' {
                for line in &b[code.j1..code.j2] {
                    output.push('+');
                    output.push_str(line);
                }
            }
        }
    }
    output
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn splits_like_go() {
        assert_eq!(split_lines(""), vec!["\n"]);
        assert_eq!(split_lines("a\nb"), vec!["a\n", "b\n"]);
        assert_eq!(split_lines("a\n"), vec!["a\n", "\n"]);
    }

    #[test]
    fn renders_unified_diff() {
        let a = split_lines("one\ntwo\nthree\n");
        let b = split_lines("one\n2\nthree\n");
        assert_eq!(
            unified_diff(&a, &b, "/x", "/x", 3),
            "--- /x\n+++ /x\n@@ -1,4 +1,4 @@\n one\n-two\n+2\n three\n \n"
        );
        assert_eq!(unified_diff(&a, &a, "/x", "/x", 3), "");
    }
}
