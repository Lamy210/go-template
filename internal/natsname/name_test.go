package natsname

import "testing"

func TestValidate(t *testing.T) {
	t.Parallel()

	valid := []string{
		"APP_EVENTS",
		"app-worker",
		"consumer_01",
	}
	for _, name := range valid {
		if err := Validate(name); err != nil {
			t.Fatalf("Validate(%q) error = %v", name, err)
		}
	}

	invalid := []string{
		"",
		"APP.EVENTS",
		"app*worker",
		"app>worker",
		"app worker",
		"app/worker",
		"app\\worker",
		"app\tworker",
		"app\rworker",
		"app\nworker",
	}
	for _, name := range invalid {
		if err := Validate(name); err == nil {
			t.Fatalf("Validate(%q) error = nil, want error", name)
		}
	}
}
