//go:build !integration

package docker_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/devdb"
	"github.com/tzone85/nexus-dispatch/internal/devdb/docker"
)

func TestPGHelper_Connect_BadDSN(t *testing.T) {
	_, err := docker.ConnectPG(context.Background(), "postgres://invalid:invalid@127.0.0.1:1/postgres?sslmode=disable")
	if err == nil {
		t.Fatal("expected error from bad DSN")
	}
	if !errors.Is(err, devdb.ErrProviderDown) {
		t.Errorf("err = %v, want wraps ErrProviderDown", err)
	}
}
