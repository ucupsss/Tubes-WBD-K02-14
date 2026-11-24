package migration

import (
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
)

func TestIter2_PlaceholderInsideDollarOrLongerName(t *testing.T) {
	in := Input{
		Title:       "Batch",
		ExecMode:    ModeBatch,
		BatchTable:  "big_events",
		BatchColumn: "id",
		BatchSize:   1000,
		UpSQL:       "UPDATE big_events SET payload = payload + 1 WHERE id > :fromage AND note = $$ :from :to $$",
		DownSQL:     "UPDATE big_events SET payload = payload - 1 WHERE id > :from AND id <= :to",
	}
	if _, ok := apperr.FieldErrors(in.Validate()); !ok {
		t.Fatal("up SQL tidak punya placeholder sungguhan")
	}
	in.UpSQL = "UPDATE big_events SET payload = payload + 1 WHERE id > :from AND id <= :to"
	if err := in.Validate(); err != nil {
		t.Fatal(err)
	}
}
