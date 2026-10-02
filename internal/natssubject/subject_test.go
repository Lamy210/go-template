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
		string([]byte{0xff}),
		"events." + string([]byte{0xc3, 0x28}),
	}
	for _, subject := range invalid {
		if err := ValidatePattern(subject); err == nil {
			t.Fatalf("ValidatePattern(%q) error = nil, want error", subject)
		}
	}
}

func TestValidateLiteralRejectsWildcards(t *testing.T) {
	t.Parallel()

	for _, subject := range []string{
		"events.*",
		"events.>",
		">",
		string([]byte{0xff}),
		"events." + string([]byte{0xc3, 0x28}),
	} {
		if err := ValidateLiteral(subject); err == nil {
			t.Fatalf("ValidateLiteral(%q) error = nil, want wildcard error", subject)
		}
	}
	if err := ValidateLiteral("events.quarantine"); err != nil {
		t.Fatalf("ValidateLiteral() error = %v", err)
	}
}

func TestPatternContainsPattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		container string
		candidate string
		want      bool
	}{
		{name: "global tail contains literal", container: ">", candidate: "events.work", want: true},
		{name: "global tail contains single wildcard", container: ">", candidate: "*", want: true},
		{name: "tail contains narrower wildcard", container: "events.>", candidate: "events.*", want: true},
		{name: "tail contains deeper tail", container: "events.>", candidate: "events.work.>", want: true},
		{name: "single wildcard contains literal", container: "events.*", candidate: "events.work", want: true},
		{name: "matching wildcards", container: "events.*", candidate: "events.*", want: true},
		{name: "tail requires at least one token", container: "events.>", candidate: "events", want: false},
		{name: "fixed pattern cannot contain tail", container: "events.*", candidate: "events.>", want: false},
		{name: "literal cannot contain wildcard", container: "events.work", candidate: "events.*", want: false},
		{name: "narrow tail cannot contain wider prefix", container: "events.work.>", candidate: "events.*.>", want: false},
		{name: "longer minimum cannot contain shorter pattern", container: "events.*.>", candidate: "events.*", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := PatternContainsPattern(tt.container, tt.candidate); got != tt.want {
				t.Fatalf(
					"PatternContainsPattern(%q, %q) = %t, want %t",
					tt.container,
					tt.candidate,
					got,
					tt.want,
				)
			}
		})
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
