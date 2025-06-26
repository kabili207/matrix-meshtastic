package meshdb

import (
	"context"

	"github.com/kabili207/matrix-meshtastic/pkg/connector/meshdb/upgrades"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
)

type Database struct {
	*dbutil.Database
	MeshNodeInfo *MeshNodeInfoQuery
}

func New(db *dbutil.Database, log zerolog.Logger) *Database {
	db = db.Child("meshtastic_version", upgrades.Table, dbutil.ZeroLogger(log))
	return &Database{
		Database: db,
		MeshNodeInfo: &MeshNodeInfoQuery{
			QueryHelper: dbutil.MakeQueryHelper(db, newMeshNodeInfo),
		},
	}
}

func (c *Database) Upgrade(ctx context.Context) error {
	return c.Database.Upgrade(ctx)
}
