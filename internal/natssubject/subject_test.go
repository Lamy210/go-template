package natssubject

import "testing"

func TestValidatePattern(t *testing.T) {
	t.Parallel()

	valid := []string{
		"events.work",
		"events.*",
		"events.>",
		">",
		"$JS.API.STREAM.INFO.*",
	}
	for _, subject := range valid {
		if err := ValidatePattern(subject); err != nil {
			t.Fatalf("ValidatePattern(%q) error = %v", subject, err)
		}
	}

	invalid := []string{
		"",
		".events",
		"events.",
		"events..work",
		"events.foo*",
		"events.>.work",
		"events work",
	}
	for _, subject := range invalid {
		if err := ValidatePattern(subject); err == nil {
			t.Fatalf("ValidatePattern(%q) error = nil, want error", subject)
		}
	}
}

func TestValidateLiteralRejectsWildcards(t *testing.T) {
	t.Parallel()

	for _, subject := range []string{"events.*", "events.>", ">"} {
		if err := ValidateLiteral(subject); err == nil {
			t.Fatalf("ValidateLiteral(%q) error = nil, want wildcard error", subject)
		}
	}
	if err := ValidateLiteral("events.quarantine"); err != nil {
		t.Fatalf("ValidateLiteral() error = %v", err)
	}
}

func TestPatternMatchesLiteral(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		literal string
		want    bool
	}{
		{pattern: "events.work", literal: "events.work", want: true},
		{pattern: "events.*", literal: "events.work", want: true},
		{pattern: "events.*", literal: "events.work.retry", want: false},
		{pattern: "events.>", literal: "events.quarantine", want: true},
		{pattern: "events.>", literal: "events.quarantine.retry", want: true},
		{pattern: "events.>", literal: "events", want: false},
		{pattern: ">", literal: "events", want: true},
		{pattern: "other.>", literal: "events.quarantine", want: false},
	}
	for _, tt := range tests {
		if got := PatternMatchesLiteral(tt.pattern, tt.literal); got != tt.want {
			t.Fatalf(
				"PatternMatchesLiteral(%q, %q) = %t, want %t",
				tt.pattern,
				tt.literal,
				got,
				tt.want,
			)
		}
	}
}
