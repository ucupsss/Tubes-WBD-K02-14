//go:build integration

package platformdb_test

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/ucupsss/Tubes-WBD-K02-14/db"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

func TestMigrate_TwiceIsSuccess(t *testing.T) {
	t.Parallel()
	pool := testutil.NewPlatformDB(t)
	cc := pool.Config().ConnConfig
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cc.User, cc.Password),
		Host:   fmt.Sprintf("%s:%d", cc.Host, cc.Port),
		Path:   "/" + cc.Database,
	}
	q := u.Query()
	q.Set("sslmode", "disable")
	if mode := cc.RuntimeParams["sslmode"]; mode != "" {
		q.Set("sslmode", mode)
	}
	u.RawQuery = q.Encode()

	if err := platformdb.Migrate(u.String(), db.PlatformMigrations); err != nil {
		t.Fatal(err)
	}
}
