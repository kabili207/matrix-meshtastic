package meshdb

import (
	"context"
	"time"

	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"go.mau.fi/util/dbutil"
	"go.mau.fi/util/ptr"
)

const (
	getWaypointSelect            = "SELECT id, name, icon, description, latitude, longitude, locked_to, expires, updated_by, updated_date FROM mesh_waypoints "
	getWaypointByWaypointIDQuery = getWaypointSelect + "WHERE id=$1"
	getWaypointByNodeIDQuery     = getWaypointSelect + "WHERE locked_to=$1 or updated_by=$1"

	deleteWaypointByWaypointIDQuery = "DELETE FROM mesh_waypoints WHERE id=$1"

	setWaypointQuery = `
		INSERT INTO mesh_waypoints (id, name, icon, description, latitude, longitude, locked_to, expires, updated_by, updated_date)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			name=excluded.name,
			icon=excluded.icon,
			description=excluded.description,
			latitude=excluded.latitude,
			longitude=excluded.longitude,
			locked_to=excluded.locked_to,
			expires=excluded.expires,
			updated_by=excluded.updated_by,
			updated_date=excluded.updated_date
	`
)

type WaypointQuery struct {
	*dbutil.QueryHelper[*Waypoint]
}

type Waypoint struct {
	qh *dbutil.QueryHelper[*Waypoint]

	WaypointID  uint32
	Name        string
	Icon        string
	Description string
	Latitude    float32
	Longitude   float32
	LockedTo    *meshid.NodeID
	Expires     *time.Time
	UpdatedBy   meshid.NodeID
	UpdatedDate *time.Time
}

var _ dbutil.DataStruct[*Waypoint] = (*Waypoint)(nil)

func newWaypoint(qh *dbutil.QueryHelper[*Waypoint]) *Waypoint {
	return &Waypoint{qh: qh}
}

func (q *WaypointQuery) GetByWaypointID(ctx context.Context, waypointID uint32) (*Waypoint, error) {
	return q.QueryOne(ctx, getWaypointByWaypointIDQuery, waypointID)
}

func (q *WaypointQuery) GetByNodeID(ctx context.Context, nodeID meshid.NodeID) ([]*Waypoint, error) {
	return q.QueryMany(ctx, getWaypointByNodeIDQuery, nodeID)
}
func (q *WaypointQuery) DeleteByID(ctx context.Context, waypointID uint32) error {
	return q.Exec(ctx, deleteWaypointByWaypointIDQuery, waypointID)
}

func (w *Waypoint) sqlVariables() []any {
	var expires, updated *int64
	if w.Expires != nil {
		expires = ptr.Ptr(w.Expires.UTC().Unix())
	}
	if w.UpdatedDate != nil {
		updated = ptr.Ptr(w.UpdatedDate.UTC().Unix())
	}
	return []any{w.WaypointID, w.Name, w.Icon, w.Description, w.Latitude, w.Longitude, w.LockedTo, expires, w.UpdatedBy, updated}
}

func (w *Waypoint) SetAll(ctx context.Context) error {
	return w.qh.Exec(ctx, setWaypointQuery, w.sqlVariables()...)
}

func (w *Waypoint) Scan(row dbutil.Scannable) (*Waypoint, error) {
	var expires, updated *int64
	err := row.Scan(&w.WaypointID, &w.Name, &w.Icon, &w.Description, &w.Latitude, &w.Longitude, &w.LockedTo, &expires, &w.UpdatedBy, &updated)
	if err != nil {
		if expires != nil {
			w.Expires = ptr.Ptr(time.Unix(*expires, 0))
		}
		if updated != nil {
			w.UpdatedDate = ptr.Ptr(time.Unix(*updated, 0))
		}
	}
	return w, err
}
