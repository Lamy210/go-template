package messageid

import "testing"

func TestValidate(t *testing.T) {
	t.Parallel()

	valid := []string{
		"event-1",
		"order:123",
		"日本語-id",
		"internal\ttab",
	}
	for _, id := range valid {
		if err := Validate(id); err != nil {
			t.Fatalf("Validate(%q) error = %v", id, err)
		}
	}

	invalid := []string{
		"",
		" event-1",
		"event-1 ",
		"\tevent-1",
		"event-1\t",
		"event\n1",
		"event\r1",
		"event\x001",
		string([]byte{0xff}),
		"event-" + string([]byte{0xc3, 0x28}),
	}
	for _, id := range invalid {
		if err := Validate(id); err == nil {
			t.Fatalf("Validate(%q) error = nil, want error", id)
		}
	}
}
