//! "Did you mean" candidate ranking (port of `internal/suggestion`).

use std::collections::{HashMap, HashSet};

struct ScoredCandidate<'a> {
    value: &'a str,
    lower: String,
    score: i64,
}

/// Lowercases like Go's `strings.ToLower`: every rune maps to exactly one
/// rune (Rust's multi-character special cases keep their first character).
fn go_to_lower(value: &str) -> String {
    if value.is_ascii() {
        return value.to_ascii_lowercase();
    }
    value
        .chars()
        .map(|character| character.to_lowercase().next().unwrap_or(character))
        .collect()
}

/// Returns at most five candidates using the Symfony plugin's fuzzy
/// subsequence scoring: one point per matched character and two bonus points
/// for consecutive matches. Results are deterministic when scores tie.
pub fn similar<S: AsRef<str>>(input: &str, candidates: &[S]) -> Vec<String> {
    let input = go_to_lower(input);
    let mut seen: HashSet<String> = HashSet::with_capacity(candidates.len());
    let mut scored: Vec<ScoredCandidate<'_>> = Vec::new();
    for candidate in candidates {
        let candidate = candidate.as_ref();
        if candidate.is_empty() {
            continue;
        }
        let lower = go_to_lower(candidate);
        if !seen.insert(lower.clone()) {
            continue;
        }
        let score = fuzzy_distance(&lower, &input);
        if score > 0 {
            scored.push(ScoredCandidate {
                value: candidate,
                lower,
                score,
            });
        }
    }
    if scored.is_empty() {
        return Vec::new();
    }

    let count = scored.len() as f64;
    let mean = scored.iter().map(|value| value.score as f64).sum::<f64>() / count;
    let variance = scored
        .iter()
        .map(|value| {
            let delta = value.score as f64 - mean;
            delta * delta
        })
        .sum::<f64>();
    let deviation = (variance / count).sqrt();

    scored.retain(|candidate| candidate.score as f64 > deviation);
    scored.sort_by(|left, right| {
        right
            .score
            .cmp(&left.score)
            .then_with(|| left.lower.cmp(&right.lower))
    });
    scored.truncate(5);
    scored
        .into_iter()
        .map(|value| value.value.to_owned())
        .collect()
}

/// Like [`similar`] but ignores template format and extension suffixes such
/// as `.html.twig`, returning original candidate names.
pub fn similar_templates<S: AsRef<str>>(input: &str, candidates: &[S]) -> Vec<String> {
    let stripped_input = strip_template_extensions(input);
    let mut originals: HashMap<String, &str> = HashMap::with_capacity(candidates.len());
    let mut stripped: Vec<String> = Vec::new();
    for candidate in candidates {
        let candidate = candidate.as_ref();
        let key = strip_template_extensions(candidate);
        if key.is_empty() {
            continue;
        }
        match originals.get(&key) {
            Some(current) if candidate >= *current => {}
            _ => {
                originals.insert(key.clone(), candidate);
            }
        }
        stripped.push(key);
    }
    similar(&stripped_input, &stripped)
        .into_iter()
        .filter_map(|matched| {
            originals
                .get(&go_to_lower(&matched))
                .filter(|original| !original.is_empty())
                .map(|original| (*original).to_owned())
        })
        .collect()
}

fn fuzzy_distance(term: &str, query: &str) -> i64 {
    if term.is_ascii() && query.is_ascii() {
        return fuzzy_distance_units(term.as_bytes(), query.as_bytes());
    }
    let term: Vec<char> = term.chars().collect();
    let query: Vec<char> = query.chars().collect();
    fuzzy_distance_units(&term, &query)
}

fn fuzzy_distance_units<T: PartialEq>(term: &[T], query: &[T]) -> i64 {
    let mut score = 0;
    let mut term_index = 0usize;
    let mut last_match: isize = -2;
    for query_unit in query {
        let mut found = false;
        while term_index < term.len() && !found {
            if *query_unit == term[term_index] {
                score += 1;
                if last_match + 1 == term_index as isize {
                    score += 2;
                }
                last_match = term_index as isize;
                found = true;
            }
            term_index += 1;
        }
    }
    score
}

fn strip_template_extensions(value: &str) -> String {
    let mut value = go_to_lower(value);
    for _ in 0..2 {
        let Some(index) = value.rfind('.') else {
            break;
        };
        let slash = value.rfind(['/', '\\']);
        if slash.is_some_and(|slash| index <= slash) {
            break;
        }
        value.truncate(index);
    }
    value
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn similar_ranks_symfony_names() {
        let result = similar(
            "product.sho",
            &[
                "admin.dashboard",
                "product.search",
                "product.show",
                "product.list",
            ],
        );
        assert!(!result.is_empty());
        assert_eq!(result[0], "product.show");
        assert!(result.len() <= 5);
    }

    #[test]
    fn similar_templates_ignores_format_and_extension() {
        let result = similar_templates(
            "storefront/page/prodcut.html.twig",
            &[
                "storefront/page/account.html.twig",
                "storefront/page/product.xml.twig",
            ],
        );
        assert!(!result.is_empty());
        assert_eq!(result[0], "storefront/page/product.xml.twig");
    }

    #[test]
    fn similar_handles_unicode_and_empty_input() {
        assert!(similar::<&str>("x", &[]).is_empty());
        assert_eq!(similar("größe", &["Größe", "groesse"])[0], "Größe");
    }
}
