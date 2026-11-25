package main

import "testing"

func TestLangOf(t *testing.T) {
	cases := map[string]string{
		"¡Hola! Quiero mi sticker STK-ABCDE": "es",
		"Hi! I want my sticker STK-ABCDE":    "en",
		"STK-ABCDE":                          "es",
		"hello STK-ABCDE":                    "en",
	}
	for in, want := range cases {
		if got := langOf(in); got != want {
			t.Errorf("langOf(%q) = %s, quería %s", in, got, want)
		}
	}
}
