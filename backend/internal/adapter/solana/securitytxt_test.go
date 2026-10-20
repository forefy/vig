package solana

import "testing"

func TestSecurityTxt(t *testing.T) {
	img := append([]byte("junk\x7fELF"), securityTxtBegin...)
	img = append(img, []byte("\x00name\x00Thing\x00source_code\x00https://github.com/o/r/tree/main/programs/x\x00source_release\x00v1.2.0\x00")...)
	img = append(img, securityTxtEnd...)
	img = append(img, []byte("more")...)
	got := SecurityTxt(img)
	if got["name"] != "Thing" || got["source_code"] != "https://github.com/o/r/tree/main/programs/x" || got["source_release"] != "v1.2.0" {
		t.Fatalf("got %v", got)
	}
	if SecurityTxt([]byte("nothing here")) != nil {
		t.Fatal("expected nil without markers")
	}
	if SecurityTxt(append(append([]byte{}, securityTxtBegin...), securityTxtEnd...)) != nil {
		t.Fatal("expected nil for an empty block")
	}
}
