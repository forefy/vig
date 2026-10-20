package main

import "testing"

func TestAttachRefAndTypes(t *testing.T) {
	for _, c := range []struct {
		in      string
		ord, st int
		bad     bool
	}{{"3", 3, 0, false}, {"3.2", 3, 2, false}, {"0", 0, 0, true}, {"3.0", 0, 0, true}, {"x", 0, 0, true}} {
		o, s, err := parseRef(c.in)
		if (err != nil) != c.bad || o != c.ord || s != c.st {
			t.Errorf("%q: %d %d %v", c.in, o, s, err)
		}
	}
	for name, want := range map[string]string{"poc.py": "text/x-python", "drain.MP4": "video/mp4", "clip.webm": "video/webm", "shot.png": "image/png", "dump.har": "application/json"} {
		if got := detectType(name, []byte("x")); got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
	// No extension: the bytes decide.
	if got := detectType("capture", []byte("\x89PNG\r\n\x1a\n"+"0000000000000000")); got != "image/png" {
		t.Errorf("png bytes: %s", got)
	}
}
