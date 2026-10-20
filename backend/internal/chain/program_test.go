package chain

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRPC is a minimal Solana JSON-RPC server for getAccountInfo,
// getBlockTime and getSlot.
type fakeRPC struct {
	mu         sync.Mutex
	accounts   map[string]map[string]any // nil value means the account does not exist
	blockTimes map[uint64]int64
	slot       uint64
	calls      atomic.Int32
	methods    []string
	status     int // when non-zero, every response uses this HTTP status
	rpcError   string
}

func newFakeRPC() *fakeRPC {
	return &fakeRPC{accounts: map[string]map[string]any{}, blockTimes: map[uint64]int64{}, slot: 300_000_000}
}

func (f *fakeRPC) account(pubkey, owner string, executable bool, data []byte) {
	f.accounts[pubkey] = map[string]any{
		"owner": owner, "lamports": 1_000_000, "executable": executable, "rentEpoch": 0,
		"data": []string{base64.StdEncoding.EncodeToString(data), "base64"},
	}
}

func (f *fakeRPC) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	var req struct {
		Method string `json:"method"`
		Params []any  `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	f.mu.Lock()
	f.methods = append(f.methods, req.Method)
	f.mu.Unlock()
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	if f.rpcError != "" {
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": -32000, "message": f.rpcError}})
		return
	}
	var result any
	switch req.Method {
	case "getAccountInfo":
		pk, _ := req.Params[0].(string)
		f.mu.Lock()
		v, ok := f.accounts[pk]
		f.mu.Unlock()
		var value any
		if ok && v != nil {
			value = v
		}
		result = map[string]any{"context": map[string]any{"slot": f.slot}, "value": value}
	case "getBlockTime":
		slot := uint64(req.Params[0].(float64))
		if ts, ok := f.blockTimes[slot]; ok {
			result = ts
		} else {
			result = nil
		}
	case "getSlot":
		result = f.slot
	default:
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": -32601, "message": "method not found"}})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
}

func (f *fakeRPC) seen(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.methods {
		if m == method {
			n++
		}
	}
	return n
}

// programAccountData is the BPFLoaderUpgradeable "Program" account: enum 2
// followed by the programdata address.
func programAccountData(programData string) []byte {
	pd, _ := Decode(programData)
	buf := make([]byte, 4, 36)
	binary.LittleEndian.PutUint32(buf, 2)
	return append(buf, pd...)
}

// programDataAccount is the "ProgramData" account: enum 3, u64 slot,
// Option<Pubkey> upgrade authority, then the ELF with zero padding.
func programDataAccount(slot uint64, authority string, code []byte, padding int) []byte {
	buf := make([]byte, 12)
	binary.LittleEndian.PutUint32(buf[:4], 3)
	binary.LittleEndian.PutUint64(buf[4:12], slot)
	if authority == "" {
		buf = append(buf, 0)
		buf = append(buf, make([]byte, 32)...)
	} else {
		a, _ := Decode(authority)
		buf = append(buf, 1)
		buf = append(buf, a...)
	}
	buf = append(buf, code...)
	return append(buf, make([]byte, padding)...)
}

// idlAccountData is an Anchor IDL account: 8 byte discriminator, 32 byte
// authority, u32 length, zlib JSON.
func idlAccountData(t *testing.T, idlJSON string) []byte {
	t.Helper()
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	w.Write([]byte(idlJSON))
	w.Close()
	buf := make([]byte, 44)
	binary.LittleEndian.PutUint32(buf[40:44], uint32(z.Len()))
	return append(buf, z.Bytes()...)
}

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestHashBytecodeTrimsTrailingZeros(t *testing.T) {
	code := []byte{0x7f, 'E', 'L', 'F', 0, 1, 2}
	if hashBytecode(append(code, make([]byte, 1000)...)) != sha256hex(code) {
		t.Fatal("trailing zero padding must not change the hash")
	}
	if hashBytecode(code) != sha256hex(code) {
		t.Fatal("unpadded input must hash as-is")
	}
	if hashBytecode(nil) != sha256hex(nil) {
		t.Fatal("empty input")
	}
	if hashBytecode([]byte{0, 0, 1, 0}) != sha256hex([]byte{0, 0, 1}) {
		t.Fatal("only trailing zeros are trimmed")
	}
}

func TestProgramUpgradeable(t *testing.T) {
	f := newFakeRPC()
	srv := httptest.NewServer(f)
	defer srv.Close()

	programID := testKey("program")
	programData := testKey("programdata")
	authority := testKey("authority")
	code := []byte("\x7fELF fake bytecode with some bytes\x00\x01\x02")
	f.account(programID, BPFLoaderUpgradeable, true, programAccountData(programData))
	f.account(programData, BPFLoaderUpgradeable, false, programDataAccount(123_456_789, authority, code, 4096))
	f.blockTimes[123_456_789] = 1_700_000_000
	idlAddr, _ := AnchorIDLAddress(programID)
	f.account(idlAddr, programID, false, idlAccountData(t, `{"version":"0.1.0","name":"thing","metadata":{"version":"0.2.5","spec":"0.1.0"}}`))

	c := New([]string{srv.URL})
	facts, err := c.Program(context.Background(), programID)
	if err != nil {
		t.Fatal(err)
	}
	want := ProgramFacts{
		ProgramID: programID, Exists: true, Executable: true, Loader: "upgradeable", LoaderProgram: BPFLoaderUpgradeable,
		UpgradeAuthority: authority, LastDeploySlot: 123_456_789, DeployedAt: time.Unix(1_700_000_000, 0).UTC(),
		BytecodeSHA256: sha256hex(code), IDLPresent: true, IDLVersion: "0.2.5", Slot: f.slot,
	}
	if len(facts.Bytecode) == 0 {
		t.Fatal("bytecode bytes must be kept for profiling")
	}
	facts.Bytecode = nil
	if !reflect.DeepEqual(facts, want) {
		t.Fatalf("facts\n got %+v\nwant %+v", facts, want)
	}
	if facts.DeployedAt.Location() != time.UTC {
		t.Fatal("deploy time must be UTC")
	}
}

func TestProgramImmutableAndIDLFallbacks(t *testing.T) {
	f := newFakeRPC()
	srv := httptest.NewServer(f)
	defer srv.Close()

	programID := testKey("program-immutable")
	programData := testKey("programdata-immutable")
	code := []byte("immutable code")
	f.account(programID, BPFLoaderUpgradeable, true, programAccountData(programData))
	f.account(programData, BPFLoaderUpgradeable, false, programDataAccount(42, "", code, 0))
	// No block time for slot 42: DeployedAt stays zero.
	// IDL without metadata.version falls back to the top-level version.
	idlAddr, _ := AnchorIDLAddress(programID)
	f.account(idlAddr, programID, false, idlAccountData(t, `{"version":"1.4.2","name":"thing"}`))

	facts, err := New([]string{srv.URL}).Program(context.Background(), programID)
	if err != nil {
		t.Fatal(err)
	}
	if facts.UpgradeAuthority != "" {
		t.Fatalf("immutable program must have no authority, got %q", facts.UpgradeAuthority)
	}
	if facts.LastDeploySlot != 42 || !facts.DeployedAt.IsZero() {
		t.Fatalf("slot=%d deployedAt=%v", facts.LastDeploySlot, facts.DeployedAt)
	}
	if facts.BytecodeSHA256 != sha256hex(code) {
		t.Fatal("bytecode hash")
	}
	if !facts.IDLPresent || facts.IDLVersion != "1.4.2" {
		t.Fatalf("idl present=%v version=%q", facts.IDLPresent, facts.IDLVersion)
	}
}

func TestProgramIDLOwnedByOtherIsIgnored(t *testing.T) {
	f := newFakeRPC()
	srv := httptest.NewServer(f)
	defer srv.Close()
	programID := testKey("p-idl-foreign")
	programData := testKey("pd-idl-foreign")
	f.account(programID, BPFLoaderUpgradeable, true, programAccountData(programData))
	f.account(programData, BPFLoaderUpgradeable, false, programDataAccount(1, testKey("a"), []byte("x"), 0))
	idlAddr, _ := AnchorIDLAddress(programID)
	f.account(idlAddr, testKey("somebody-else"), false, idlAccountData(t, `{"version":"9.9.9"}`))
	facts, err := New([]string{srv.URL}).Program(context.Background(), programID)
	if err != nil {
		t.Fatal(err)
	}
	if facts.IDLPresent || facts.IDLVersion != "" {
		t.Fatal("an IDL account not owned by the program must not count")
	}
}

func TestProgramNonUpgradeableLoaders(t *testing.T) {
	code := []byte("legacy bytecode")
	cases := []struct {
		name       string
		owner      string
		data       []byte
		wantLoader string
		wantSlot   uint64
		wantAuth   string
	}{
		{"bpf loader 2", BPFLoader2, append(code, make([]byte, 10)...), "non-upgradeable", 0, ""},
		{"bpf loader 1", BPFLoader1, code, "non-upgradeable", 0, ""},
		{"loader v4", LoaderV4, func() []byte {
			b := make([]byte, 48)
			binary.LittleEndian.PutUint64(b[:8], 555)
			a, _ := Decode(testKey("v4-authority"))
			copy(b[8:40], a)
			return append(b, code...)
		}(), "loader_v4", 555, testKey("v4-authority")},
		{"loader v4 short data", LoaderV4, code, "loader_v4", 0, ""},
		{"unknown owner but executable", testKey("custom-loader"), code, "non-upgradeable", 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeRPC()
			srv := httptest.NewServer(f)
			defer srv.Close()
			programID := testKey("prog-" + c.name)
			f.account(programID, c.owner, true, c.data)
			facts, err := New([]string{srv.URL}).Program(context.Background(), programID)
			if err != nil {
				t.Fatal(err)
			}
			if facts.Loader != c.wantLoader || facts.LoaderProgram != c.owner || !facts.Exists || !facts.Executable {
				t.Fatalf("facts %+v", facts)
			}
			if facts.LastDeploySlot != c.wantSlot || facts.UpgradeAuthority != c.wantAuth {
				t.Fatalf("slot=%d auth=%q", facts.LastDeploySlot, facts.UpgradeAuthority)
			}
			if facts.BytecodeSHA256 != sha256hex(code) {
				t.Fatalf("hash %s want %s", facts.BytecodeSHA256, sha256hex(code))
			}
			if facts.IDLPresent {
				t.Fatal("no idl account was served")
			}
		})
	}
}

func TestProgramNotExecutableUnknownOwner(t *testing.T) {
	f := newFakeRPC()
	srv := httptest.NewServer(f)
	defer srv.Close()
	id := testKey("wallet")
	f.account(id, "11111111111111111111111111111111", false, nil)
	facts, err := New([]string{srv.URL}).Program(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Exists || facts.Executable || facts.Loader != "unknown" {
		t.Fatalf("a plain wallet: %+v", facts)
	}
}

func TestProgramMissing(t *testing.T) {
	f := newFakeRPC()
	srv := httptest.NewServer(f)
	defer srv.Close()
	facts, err := New([]string{srv.URL}).Program(context.Background(), testKey("nothing-here"))
	if err != nil {
		t.Fatal(err)
	}
	if facts.Exists || facts.Loader != "unknown" || facts.Slot != f.slot || facts.BytecodeSHA256 != "" {
		t.Fatalf("missing program: %+v", facts)
	}
	if f.seen("getAccountInfo") != 1 {
		t.Fatalf("a missing program needs one rpc call, got %d", f.seen("getAccountInfo"))
	}
}

func TestProgramDataWrongEnumIsSkipped(t *testing.T) {
	f := newFakeRPC()
	srv := httptest.NewServer(f)
	defer srv.Close()
	programID := testKey("p-bad-pd")
	programData := testKey("pd-bad-pd")
	f.account(programID, BPFLoaderUpgradeable, true, programAccountData(programData))
	bad := programDataAccount(7, testKey("a"), []byte("code"), 0)
	binary.LittleEndian.PutUint32(bad[:4], 1) // "Buffer", not "ProgramData"
	f.account(programData, BPFLoaderUpgradeable, false, bad)
	facts, err := New([]string{srv.URL}).Program(context.Background(), programID)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Loader != "upgradeable" || facts.LastDeploySlot != 0 || facts.UpgradeAuthority != "" || facts.BytecodeSHA256 != "" {
		t.Fatalf("buffer enum must not be parsed as programdata: %+v", facts)
	}
}

func idlVersion(data []byte) string { v, _ := idlMeta(data); return v }

func TestIDLRepository(t *testing.T) {
	if _, r := idlMeta(idlAccountData(t, `{"metadata":{"version":"2.0.0","repository":"https://github.com/o/r"}}`)); r != "https://github.com/o/r" {
		t.Fatalf("metadata.repository, got %q", r)
	}
	if _, r := idlMeta(idlAccountData(t, `{"version":"1.0.0"}`)); r != "" {
		t.Fatalf("no repository must be empty, got %q", r)
	}
}

func TestIDLVersion(t *testing.T) {
	if v := idlVersion(idlAccountData(t, `{"metadata":{"version":"2.0.0"},"version":"1.0.0"}`)); v != "2.0.0" {
		t.Fatalf("metadata.version wins, got %q", v)
	}
	if v := idlVersion(idlAccountData(t, `{"version":"1.0.0"}`)); v != "1.0.0" {
		t.Fatalf("top-level version, got %q", v)
	}
	if v := idlVersion(idlAccountData(t, `not json`)); v != "" {
		t.Fatalf("bad json must be empty, got %q", v)
	}
	if v := idlVersion(make([]byte, 43)); v != "" {
		t.Fatal("short account")
	}
	// Length prefix claims more than is present.
	d := idlAccountData(t, `{"version":"1.0.0"}`)
	binary.LittleEndian.PutUint32(d[40:44], uint32(len(d)))
	if v := idlVersion(d); v != "" {
		t.Fatal("overlong length must be rejected")
	}
	// Not zlib.
	raw := make([]byte, 44)
	binary.LittleEndian.PutUint32(raw[40:44], 5)
	if v := idlVersion(append(raw, []byte("hello")...)); v != "" {
		t.Fatal("non-zlib payload")
	}
}
