package suggestion

import (
	"math"
	"sort"
	"strings"
)

type scoredCandidate struct {
	value string
	score int
}

// Similar returns at most five candidates using the Symfony plugin's fuzzy
// subsequence scoring: one point per matched character and two bonus points
// for consecutive matches. Results are deterministic when scores tie.
func Similar(input string, candidates []string) []string {
	input = strings.ToLower(input)
	seen := make(map[string]struct{}, len(candidates))
	var scored []scoredCandidate
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		key := strings.ToLower(candidate)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		score := fuzzyDistance(strings.ToLower(candidate), input)
		if score > 0 {
			scored = append(scored, scoredCandidate{
				value: candidate,
				score: score,
			})
		}
	}
	if len(scored) == 0 {
		return nil
	}

	var mean float64
	for _, candidate := range scored {
		mean += float64(candidate.score)
	}
	mean /= float64(len(scored))
	var variance float64
	for _, candidate := range scored {
		delta := float64(candidate.score) - mean
		variance += delta * delta
	}
	deviation := math.Sqrt(variance / float64(len(scored)))

	selected := scored[:0]
	for _, candidate := range scored {
		if float64(candidate.score) > deviation {
			selected = append(selected, candidate)
		}
	}
	sort.Slice(selected, func(left, right int) bool {
		if selected[left].score != selected[right].score {
			return selected[left].score > selected[right].score
		}
		return strings.ToLower(selected[left].value) <
			strings.ToLower(selected[right].value)
	})
	if len(selected) > 5 {
		selected = selected[:5]
	}
	result := make([]string, 0, len(selected))
	for _, candidate := range selected {
		result = append(result, candidate.value)
	}
	return result
}

func SimilarTemplates(input string, candidates []string) []string {
	strippedInput := stripTemplateExtensions(input)
	originals := make(map[string]string, len(candidates))
	var stripped []string
	for _, candidate := range candidates {
		key := stripTemplateExtensions(candidate)
		if key == "" {
			continue
		}
		current, exists := originals[key]
		if !exists || candidate < current {
			originals[key] = candidate
		}
		stripped = append(stripped, key)
	}
	matches := Similar(strippedInput, stripped)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		if original := originals[strings.ToLower(match)]; original != "" {
			result = append(result, original)
		}
	}
	return result
}

func fuzzyDistance(term, query string) int {
	termRunes := []rune(term)
	score := 0
	termIndex := 0
	lastMatch := -2
	for _, queryRune := range query {
		found := false
		for termIndex < len(termRunes) && !found {
			if queryRune == termRunes[termIndex] {
				score++
				if lastMatch+1 == termIndex {
					score += 2
				}
				lastMatch = termIndex
				found = true
			}
			termIndex++
		}
	}
	return score
}

func stripTemplateExtensions(value string) string {
	value = strings.ToLower(value)
	for range 2 {
		index := strings.LastIndexByte(value, '.')
		slash := strings.LastIndexAny(value, `/\`)
		if index <= slash {
			break
		}
		value = value[:index]
	}
	return value
}
