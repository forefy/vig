// Package testdb starts an embedded Postgres for tests so the suite runs
// without Docker. The server binaries are downloaded once into a shared
// cache; when that download is impossible (offline) the test is skipped.
package testdb

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"github.com/forefy/vig/backend/internal/db"
	"github.com/forefy/vig/backend/internal/store"
)

var (
	mu       sync.Mutex
	shared   *embeddedpostgres.EmbeddedPostgres
	baseURL  string
	startErr error
	dbSeq    int
)

// CachePath is where the Postgres binaries are kept between runs.
func CachePath() string {
	if p := os.Getenv("VIG_PG_CACHE"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "vig-pg-cache")
	}
	return filepath.Join(home, ".cache", "vig", "embedded-postgres")
}

// Config returns an embedded Postgres configuration for the given port and
// data directory. The binaries cache is shared with the test helper.
func Config(port uint32, dataDir string) embeddedpostgres.Config {
	return embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Port(port).
		Username("vig").
		Password("vig").
		Database("vig").
		CachePath(CachePath()).
		RuntimePath(filepath.Join(dataDir, "runtime")).
		DataPath(filepath.Join(dataDir, "data")).
		Logger(nil)
}

func freePort() (uint32, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return uint32(l.Addr().(*net.TCPAddr).Port), nil
}

// start boots one server per test binary, lazily.
func start() (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if shared != nil || startErr != nil {
		return baseURL, startErr
	}
	port, err := freePort()
	if err != nil {
		startErr = err
		return "", err
	}
	runtime, err := os.MkdirTemp("", "vig-testdb-")
	if err != nil {
		startErr = err
		return "", err
	}
	pg := embeddedpostgres.NewDatabase(Config(port, runtime))
	if err := pg.Start(); err != nil {
		_ = os.RemoveAll(runtime)
		startErr = fmt.Errorf("embedded postgres: %w", err)
		return "", startErr
	}
	shared = pg
	baseURL = fmt.Sprintf("postgres://vig:vig@127.0.0.1:%d/", port)
	return baseURL, nil
}

// Stop shuts the shared server down. Call it from TestMain.
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if shared != nil {
		_ = shared.Stop()
		shared = nil
	}
}

// New returns a migrated store on a fresh database. It skips the test when
// the embedded server cannot start (for example, offline with an empty
// binaries cache).
func New(t testing.TB) *store.Store {
	t.Helper()
	base, err := start()
	if err != nil {
		if os.Getenv("VIG_TESTDB_REQUIRED") != "" {
			t.Fatal(err)
		}
		t.Skipf("embedded postgres unavailable: %v", err)
	}
	mu.Lock()
	dbSeq++
	name := "vig_test_" + strconv.Itoa(dbSeq)
	mu.Unlock()
	ctx := context.Background()
	admin, err := store.New(ctx, base+"vig")
	if err != nil {
		t.Fatal(err)
	}
	_, err = admin.Pool.Exec(ctx, "create database "+name)
	admin.Close()
	if err != nil {
		t.Fatal(err)
	}
	url := base + name
	if err := db.Migrate(ctx, url, "up"); err != nil {
		t.Fatal(err)
	}
	s, err := store.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}
