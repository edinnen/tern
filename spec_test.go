package tern

import "testing"

// The worked example in SPEC.md.
func TestSpecExample(t *testing.T) {
	got, err := callSymbols("N0CALL")
	if err != nil {
		t.Fatal(err)
	}
	want := []uint8{6, 23, 0, 12, 10, 21, 21}
	if string(got) != string(want) {
		t.Fatalf("call field %v, the document says %v", got, want)
	}
	if gfExp[1] != 2 {
		t.Fatal("alpha is not 2")
	}
}

// The numbers SPEC.md quotes: the start of the whitening sequence, the tones of a
// call sign field, and the test vector.
func TestSpecVector(t *testing.T) {
	if got, want := white[:7], []uint8{40, 11, 12, 22, 61, 12, 17}; string(got) != string(want) {
		t.Fatalf("whitening starts %v, the document says %v", got, want)
	}
	if got, want := pack([]byte("hello!")), []uint8{26, 6, 21, 44, 27, 6, 60, 33}; string(got) != string(want) {
		t.Fatalf("information symbols %v, the document says %v", got, want)
	}
	got, err := Symbols("N0CALL", []byte("hello!"), false)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint8{
		9, 54, 23, 40,
		46, 28, 12, 26, 55, 25, 4,
		20, 14, 19, 30, 0, 28, 12, 11,
		63, 13, 45, 37, 11, 58, 6, 43, 42, 57, 51, 11, 6, 43, 14, 5,
	}
	if string(got) != string(want) {
		t.Fatalf("transmission is\n%v\nthe document says\n%v", got, want)
	}
}
