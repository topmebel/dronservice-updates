package main

import "testing"

func TestRequestHostname(t *testing.T) {
	tests := map[string]string{
		"192.168.1.10:80": "192.168.1.10",
		"raspberry.local": "raspberry.local",
		"[fe80::1]:8080":  "fe80::1",
	}
	for input, want := range tests {
		got, err := requestHostname(input)
		if err != nil || got != want {
			t.Fatalf("requestHostname(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := requestHostname("evil/host"); err == nil {
		t.Fatal("requestHostname() accepted invalid host")
	}
}
