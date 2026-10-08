package foodimport

import (
	"os"
	"testing"

	"schautrack/internal/dbtest"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}
