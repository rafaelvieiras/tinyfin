package guid

import "testing"

func TestNormalize(t *testing.T) {
	const want = "0123456789abcdef0123456789abcdef"
	for _, in := range []string{
		"0123456789abcdef0123456789abcdef",
		"0123456789ABCDEF0123456789ABCDEF",
		"01234567-89ab-cdef-0123-456789abcdef",
		"01234567-89AB-CDEF-0123-456789ABCDEF",
		"  01234567-89ab-cdef-0123-456789abcdef ",
	} {
		got, ok := Normalize(in)
		if !ok || got != want {
			t.Errorf("Normalize(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "not-a-guid", "0123456789abcdef0123456789abcde", "0123456789abcdef0123456789abcdeg",
		"0123456789abcdef-0123-4567-89ab-cdef01234567", "{01234567-89ab-cdef-0123-456789abcdef}"} {
		if got, ok := Normalize(in); ok {
			t.Errorf("Normalize(%q) = %q, want failure", in, got)
		}
	}
}

func TestNewAndDerive(t *testing.T) {
	a, b := New(), New()
	if a == b || len(a) != 32 {
		t.Fatalf("New: %q %q", a, b)
	}
	if _, ok := Normalize(a); !ok {
		t.Fatalf("New not canonical: %q", a)
	}
	if Derive("x") != Derive("x") || Derive("x") == Derive("y") || len(Derive("x")) != 32 {
		t.Fatal("Derive not stable/distinct")
	}
	if Dashed(Zero) != "00000000-0000-0000-0000-000000000000" {
		t.Fatal("Dashed")
	}
}
