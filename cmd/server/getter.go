package main

import (
	"context"
	"errors"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/databases"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// connGetter adapts databases.PgRepo to dbconn.ConnectionGetter.
// The repository method takes a DB handle; the manager asks by id only.
type connGetter struct {
	db   platformdb.DBTX
	repo *databases.PgRepo
}

func (g connGetter) GetByID(ctx context.Context, id int64) (dbconn.Connection, error) {
	c, err := g.repo.GetByID(ctx, g.db, id)
	if err != nil {
		if errors.Is(err, databases.ErrNotFound) {
			return dbconn.Connection{}, dbconn.ErrNotFound
		}
		return dbconn.Connection{}, err
	}
	return c, nil
}
