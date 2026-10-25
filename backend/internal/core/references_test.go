package core

import "testing"

func TestParseBlobLink(t *testing.T) {
	l, ok := ParseBlobLink("https://github.com/benoitc/gunicorn/blob/master/gunicorn/http/parser.py#L12-L20")
	if !ok || l.Owner != "benoitc" || l.Name != "gunicorn" || l.Rev != "master" || l.Path != "gunicorn/http/parser.py" || l.Start != 12 || l.End != 20 {
		t.Fatalf("parsed %+v %v", l, ok)
	}
	if l.IsSHA() {
		t.Fatal("master is not a sha")
	}
	want := "https://github.com/benoitc/gunicorn/blob/afc7d2fd5dd9f1de455b1be6c10044030c0adf8e/gunicorn/http/parser.py#L12-L20"
	if got := l.Pinned("afc7d2fd5dd9f1de455b1be6c10044030c0adf8e"); got != want {
		t.Fatalf("pinned %s", got)
	}
	one, ok := ParseBlobLink("https://github.com/o/n/blob/abc/src/lib.rs?plain=1#L7")
	if !ok || one.Start != 7 || one.End != 7 || one.Path != "src/lib.rs" {
		t.Fatalf("single line %+v %v", one, ok)
	}
	for _, raw := range []string{
		"https://github.com/o/n/blob/main/src/lib.rs",  // no lines
		"https://github.com/o/n/tree/main/src",         // a directory
		"https://github.com/o/n/blob/main/../x#L1",     // traversal
		"https://solana.com/developers/courses/x#L1",   // not github
		"https://github.com/o/n/blob/main/a.rs#L9-L3",  // backwards
		"https://github.com/o/n/blob/main/a.rs#readme", // not a line anchor
	} {
		if _, ok := ParseBlobLink(raw); ok {
			t.Errorf("%s must not parse", raw)
		}
	}
}
