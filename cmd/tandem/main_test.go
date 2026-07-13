package main

import "testing"

func TestContainsSubmitKey(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want bool
	}{
		{name: "plain text", in: []byte("hello"), want: false},
		{name: "carriage return", in: []byte{'\r'}, want: true},
		{name: "line feed", in: []byte{'\n'}, want: true},
		{name: "mixed chunk", in: []byte("prompt\rnext"), want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := containsSubmitKey(tc.in); got != tc.want {
				t.Fatalf("containsSubmitKey(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
