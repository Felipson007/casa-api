package migrations

import (
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
)

func TestEmbeddedMigrationsLoad(t *testing.T) {
	if _, err := iofs.New(FS, "."); err != nil {
		t.Fatalf("embedded migrations did not load: %v", err)
	}
}
