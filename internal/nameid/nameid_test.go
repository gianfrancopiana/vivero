package nameid

import "testing"

func TestDockerSanitizesNames(t *testing.T) {
	cases := map[string]string{
		"Web App":        "web-app",
		"../bad name!!":  "bad-name",
		"---":            "vivero",
		"Already_ok.123": "already_ok.123",
	}
	for input, want := range cases {
		if got := Docker(input); got != want {
			t.Fatalf("Docker(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestShortStableIsDeterministic(t *testing.T) {
	first := ShortStable("demo")
	second := ShortStable("demo")
	if first != second || len(first) != 8 {
		t.Fatalf("ShortStable not stable/short: first=%q second=%q", first, second)
	}
	if first == ShortStable("other") {
		t.Fatal("ShortStable should differ for different inputs")
	}
}
