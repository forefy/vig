package api

import (
	"strings"
	"testing"
)

func TestShellOfEmptiesThePrerenderedRoot(t *testing.T) {
	index := []byte(`<!doctype html><html><head><title>x</title></head><body>
    <div id="root"><div class="a"><header>h</header><main>m</main></div><script>window.x = 1;</script></div>
  </body></html>`)
	got := string(shellOf(index))
	if !strings.Contains(got, `<div id="root"></div>`) || strings.Contains(got, "<main>") || strings.Contains(got, "<script>") {
		t.Fatalf("shell: %s", got)
	}
	if !strings.HasPrefix(got, "<!doctype html>") || !strings.HasSuffix(got, "</body></html>") {
		t.Fatalf("shell lost its ends: %s", got)
	}
	// A document without a root is left alone.
	if string(shellOf([]byte("<html></html>"))) != "<html></html>" {
		t.Fatal("a document without a root changed")
	}
}
