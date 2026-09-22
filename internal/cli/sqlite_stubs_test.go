//go:build !sqlite

package cli

import (
	"strings"
	"testing"
)

func TestSQLiteStoresReturnsErrorWithoutTag(t *testing.T) {
	dep, pol, err := sqliteStores("/tmp/test.db")
	if err == nil {
		t.Fatal("expected sqliteStores to return an error without sqlite build tag")
	}
	if dep != nil {
		t.Errorf("expected nil deployment store, got %v", dep)
	}
	if pol != nil {
		t.Errorf("expected nil policy store, got %v", pol)
	}
	if !strings.Contains(err.Error(), "sqlite") {
		t.Errorf("expected error to mention sqlite, got: %v", err)
	}
	if !strings.Contains(err.Error(), "-tags sqlite") {
		t.Errorf("expected error to mention build tag, got: %v", err)
	}
}
