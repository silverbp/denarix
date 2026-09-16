// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

// Integration-test harness: every *_integration_test.go in this package
// drives the real gRPC handlers against a real Postgres with the real
// schema applied, so a refactor that touches every handler (resource
// table, batch child loading, shared line pipeline) is caught here rather
// than in production.
//
// Postgres comes from DENARIX_TEST_DSN when set (an existing database the tests
// may freely write to - each test creates its own business, so rows
// accumulate but never collide), otherwise from an embedded Postgres
// started once per `go test` run (github.com/fergusstrange/embedded-postgres;
// binaries are cached in ~/.embedded-postgres-go after the first download,
// no Docker needed). `go test -short` skips everything that needs a
// database.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sync/atomic"
	"testing"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/silverbp/denarix/internal/auth"
	"github.com/silverbp/denarix/internal/db"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/migrate"
)

var testStore *db.Store

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}

	dsn := os.Getenv("DENARIX_TEST_DSN")
	var stop func()
	if dsn == "" {
		var err error
		dsn, stop, err = startEmbeddedPostgres()
		if err != nil {
			fmt.Fprintf(os.Stderr, "starting embedded postgres: %v\n(set DENARIX_TEST_DSN to use an existing database, or run with -short to skip integration tests)\n", err)
			os.Exit(1)
		}
	}
	if err := migrate.Up(dsn); err != nil {
		fmt.Fprintf(os.Stderr, "applying migrations: %v\n", err)
		if stop != nil {
			stop()
		}
		os.Exit(1)
	}
	store, err := db.NewStore(context.Background(), dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connecting: %v\n", err)
		if stop != nil {
			stop()
		}
		os.Exit(1)
	}
	testStore = store

	code := m.Run()
	store.Close()
	if stop != nil {
		stop()
	}
	os.Exit(code)
}

func startEmbeddedPostgres() (dsn string, stop func(), err error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	port := uint32(l.Addr().(*net.TCPAddr).Port)
	_ = l.Close()

	dataDir, err := os.MkdirTemp("", "denarix-test-pg-")
	if err != nil {
		return "", nil, err
	}
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(port).
		Username("denarix").Password("denarix").Database("denarix").
		DataPath(dataDir).
		Logger(io.Discard))
	if err := pg.Start(); err != nil {
		_ = os.RemoveAll(dataDir)
		return "", nil, err
	}
	stop = func() {
		_ = pg.Stop()
		_ = os.RemoveAll(dataDir)
	}
	return fmt.Sprintf("postgres://denarix:denarix@127.0.0.1:%d/denarix?sslmode=disable", port), stop, nil
}

// testTenant is one freshly created business with an OWNER user, plus a
// second user who is a member of nothing - the "wrong tenant" caller every
// authorization check is exercised against.
type testTenant struct {
	t          *testing.T
	store      *db.Store
	q          *sqlcgen.Queries
	businessID int64
	user       *auth.User
	ctx        context.Context // as the owner
	outsider   context.Context // as a user with no membership anywhere
}

var tenantSeq atomic.Int64

// newTenant skips the test under -short (no database) and otherwise
// provisions a business the test owns outright.
func newTenant(t *testing.T) *testTenant {
	t.Helper()
	if testStore == nil {
		t.Skip("integration test: needs a database (run without -short)")
	}
	ctx := context.Background()
	q := testStore.Queries
	n := tenantSeq.Add(1)

	owner, err := q.CreateAppUser(ctx, sqlcgen.CreateAppUserParams{Email: fmt.Sprintf("owner-%d-%d@test.local", os.Getpid(), n)})
	if err != nil {
		t.Fatalf("creating owner: %v", err)
	}
	stranger, err := q.CreateAppUser(ctx, sqlcgen.CreateAppUserParams{Email: fmt.Sprintf("stranger-%d-%d@test.local", os.Getpid(), n)})
	if err != nil {
		t.Fatalf("creating outsider: %v", err)
	}
	biz, err := q.CreateBusiness(ctx, sqlcgen.CreateBusinessParams{Name: fmt.Sprintf("Test Business %d", n), CreatedByUserID: &owner.ID})
	if err != nil {
		t.Fatalf("creating business: %v", err)
	}
	if _, err := q.CreateBusinessUser(ctx, sqlcgen.CreateBusinessUserParams{BusinessID: biz.ID, UserID: owner.ID, Role: "OWNER"}); err != nil {
		t.Fatalf("creating membership: %v", err)
	}

	u := &auth.User{ID: owner.ID, Email: owner.Email}
	return &testTenant{
		t:          t,
		store:      testStore,
		q:          q,
		businessID: biz.ID,
		user:       u,
		ctx:        auth.ContextWithUser(ctx, u),
		outsider:   auth.ContextWithUser(ctx, &auth.User{ID: stranger.ID, Email: stranger.Email}),
	}
}

// wantCode fails the test unless err carries the given gRPC code.
func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if status.Code(err) != want {
		t.Fatalf("got %v (%v), want %v", status.Code(err), err, want)
	}
}

// must unwraps a (value, error) call, so a handler result can be used
// inline: inv := must(invoices.CreateInvoice(...)).GetInvoice(). Go can't
// pass a multi-value call alongside a *testing.T, so an error here panics
// with a mustFailure, and every integration test defers failOnPanic to
// turn that into a normal t.Fatal.
func must[T any](v T, err error) T {
	if err != nil {
		panic(mustFailure{err})
	}
	return v
}

type mustFailure struct{ err error }

// failOnPanic converts a must failure into t.Fatal; anything else keeps
// panicking. Defer it first thing in each integration test (and subtest).
func failOnPanic(t *testing.T) {
	t.Helper()
	if r := recover(); r != nil {
		if f, ok := r.(mustFailure); ok {
			t.Fatalf("unexpected error: %v", f.err)
		}
		panic(r)
	}
}
