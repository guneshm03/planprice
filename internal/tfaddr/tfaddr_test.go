package tfaddr

import (
	"reflect"
	"testing"
)

func TestSegments(t *testing.T) {
	cases := map[string][]string{
		"aws_instance.web":                           {"aws_instance", "web"},
		"aws_instance.web[0]":                        {"aws_instance", "web"},
		`module.web["a.b"].aws_instance.x["k]"]`:     {"module", "web", "aws_instance", "x"},
		`module.a.module.b[1].aws_s3_bucket.logs`:    {"module", "a", "module", "b", "aws_s3_bucket", "logs"},
		`aws_lambda_function.workers["q\"uote.d"]`:   {"aws_lambda_function", "workers"},
		`aws_lambda_function.workers["back\\slash"]`: {"aws_lambda_function", "workers"},
	}
	for in, want := range cases {
		if got := Segments(in); !reflect.DeepEqual(got, want) {
			t.Errorf("Segments(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestStripKeys(t *testing.T) {
	cases := map[string]string{
		`module.web[0].aws_instance.web["a"]`: "module.web.aws_instance.web",
		"aws_vpc.main":                        "aws_vpc.main",
		`x.y["]["]`:                           "x.y",
	}
	for in, want := range cases {
		if got := StripKeys(in); got != want {
			t.Errorf("StripKeys(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestKeyEnd(t *testing.T) {
	cases := map[string]int{
		"[0].x":         3,
		`["a]b"]`:       7,
		`["a\"]"]rest`:  8,
		"no-key":        -1,
		"[unterminated": -1,
		"":              -1,
	}
	for in, want := range cases {
		if got := KeyEnd(in); got != want {
			t.Errorf("KeyEnd(%q) = %d, want %d", in, got, want)
		}
	}
}

func FuzzStripKeys(f *testing.F) {
	for _, s := range []string{`module.a["x"].aws_instance.b[0]`, `[`, `"]`, `a.b["\`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		stripped := StripKeys(s)
		// Stripping is idempotent on any output that has no brackets left.
		if again := StripKeys(stripped); KeyEnd(stripped) < 0 && again != stripped && !containsBracket(stripped) {
			t.Fatalf("not idempotent: %q -> %q -> %q", s, stripped, again)
		}
		_ = Segments(s)
		if e := KeyEnd(s); e > len(s) {
			t.Fatalf("KeyEnd(%q) = %d beyond length", s, e)
		}
	})
}

func containsBracket(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '[' || s[i] == ']' {
			return true
		}
	}
	return false
}
