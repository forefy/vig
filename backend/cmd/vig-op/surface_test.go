package main

import "testing"

func TestSkipAsset(t *testing.T) {
	if !skipAsset("/static/app.js", "") || !skipAsset("/x", "image/png") || skipAsset("/api/items", "application/json") {
		t.Error("asset filter")
	}
}
