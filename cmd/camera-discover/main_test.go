package main

import "testing"

func TestVendorFilter(t *testing.T) {
	tests := map[string]int{"all": 2, "dahua": 1, "unv": 1, "UNIVIEW": 1}
	for input, count := range tests {
		got, err := vendorFilter(input)
		if err != nil || len(got) != count {
			t.Fatalf("vendorFilter(%q) = %v, %v", input, got, err)
		}
	}
	if _, err := vendorFilter("other"); err == nil {
		t.Fatal("unsupported vendor accepted")
	}
}
