package chain

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestProgramDataAddress(t *testing.T) {
	f := newFakeRPC()
	srv := httptest.NewServer(f)
	defer srv.Close()
	programID := testKey("program")
	programData := testKey("programdata")
	frozen := testKey("frozen")
	f.account(programID, BPFLoaderUpgradeable, true, programAccountData(programData))
	f.account(frozen, BPFLoader2, true, []byte("\x7fELF"))

	c := New([]string{srv.URL})
	got, err := c.ProgramDataAddress(context.Background(), programID)
	if err != nil || got != programData {
		t.Fatalf("got %q %v, want %q", got, err, programData)
	}
	if _, err := c.ProgramDataAddress(context.Background(), frozen); err == nil {
		t.Error("a non-upgradeable program has no program data")
	}
	if _, err := c.ProgramDataAddress(context.Background(), testKey("missing")); err == nil {
		t.Error("a missing account has no program data")
	}
	if _, err := programDataAddress([]byte{3, 0, 0, 0}); err == nil {
		t.Error("wrong enum tag must be refused")
	}
}
