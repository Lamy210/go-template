// Package natssubject contains the NATS subject grammar shared by process
// configuration and the messaging adapter.
package natssubject

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	errInvalidPattern = errors.New("invalid NATS subject pattern")
	errInvalidLiteral = errors.New("invalid literal NATS subject")
)

// ValidatePattern validates a NATS subscription/stream subject pattern.
// Wildcards must occupy a complete token and > is allowed only as the last token.
func ValidatePattern(subject string) error {
	return validate(subject, true)
}

// ValidateLiteral validates a concrete publish subject. Wildcards are rejected.
func ValidateLiteral(subject string) error {
	return validate(subject, false)
}

// HasDuplicatePatterns reports whether the list contains an exact duplicate.
// NATS subjects are case-sensitive, so case-distinct patterns remain distinct.
func HasDuplicatePatterns(patterns []string) bool {
	seen := make(map[string]struct{}, len(patterns))
	for _, pattern := range patterns {
		if _, exists := seen[pattern]; exists {
			return true
		}
		seen[pattern] = struct{}{}
	}
	return false
}

// PatternContainsPattern reports whether every subject matched by candidate is
// also matched by container. Both arguments must already satisfy ValidatePattern.
func PatternContainsPattern(container, candidate string) bool {
	containerTokens := strings.Split(container, ".")
	candidateTokens := strings.Split(candidate, ".")

	containerTailWildcard := containerTokens[len(containerTokens)-1] == ">"
	candidateTailWildcard := candidateTokens[len(candidateTokens)-1] == ">"

	if !containerTailWildcard {
		if candidateTailWildcard || len(containerTokens) != len(candidateTokens) {
			return false
		}
		for i := range containerTokens {
			if !patternTokenContains(containerTokens[i], candidateTokens[i]) {
				return false
			}
		}
		return true
	}

	// A terminal > consumes one or more tokens, so the candidate's minimum
	// matched length must be at least the container's minimum matched length.
	if len(candidateTokens) < len(containerTokens) {
		return false
	}

	containerPrefix := containerTokens[:len(containerTokens)-1]
	for i, token := range containerPrefix {
		if !patternTokenContains(token, candidateTokens[i]) {
			return false
		}
	}
	return true
}

func patternTokenContains(container, candidate string) bool {
	return container == "*" || container == candidate
}

// PatternMatchesLiteral reports whether a previously validated NATS pattern
// includes a previously validated literal subject.
func PatternMatchesLiteral(pattern, literal string) bool {
	patternTokens := strings.Split(pattern, ".")
	literalTokens := strings.Split(literal, ".")

	for i, token := range patternTokens {
		if token == ">" {
			// NATS > consumes one or more trailing tokens.
			return i == len(patternTokens)-1 && i < len(literalTokens)
		}
		if i >= len(literalTokens) {
			return false
		}
		if token != "*" && token != literalTokens[i] {
			return false
		}
	}
	return len(patternTokens) == len(literalTokens)
}

func validate(subject string, wildcards bool) error {
	if subject == "" || !utf8.ValidString(subject) {
		return validationError(wildcards)
	}

	tokens := strings.Split(subject, ".")
	for i, token := range tokens {
		if token == "" {
			return validationError(wildcards)
		}
		for _, r := range token {
			if unicode.IsSpace(r) || unicode.IsControl(r) {
				return validationError(wildcards)
			}
		}

		switch token {
		case "*":
			if !wildcards {
				return validationError(wildcards)
			}
		case ">":
			if !wildcards || i != len(tokens)-1 {
				return validationError(wildcards)
			}
		default:
			if strings.ContainsAny(token, "*>") {
				return validationError(wildcards)
			}
		}
	}
	return nil
}

func validationError(wildcards bool) error {
	if wildcards {
		return errInvalidPattern
	}
	return errInvalidLiteral
}
