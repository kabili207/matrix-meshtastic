package meshdb

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"go.mau.fi/util/dbutil"
	"go.mau.fi/util/ptr"
)

const (
	getMeshNodeInfoSelect             = "SELECT id, user_id, long_name, short_name, node_role, is_licensed, is_unmessageable, is_managed, is_direct, public_key, private_key, last_seen FROM mesh_node_info "
	getMeshNodeInfoByNodeIDQuery      = getMeshNodeInfoSelect + "WHERE id=$1"
	getMeshNodeInfoByUserIDQuery      = getMeshNodeInfoSelect + "WHERE user_id=$1"
	getMeshNodeInfoByShortUserIDQuery = getMeshNodeInfoSelect + "WHERE user_id LIKE $1"
	getMeshNodeInfoIsManagedQuery     = getMeshNodeInfoSelect + "WHERE is_managed=true"
	getMeshNodeInfoNeighborsQuery     = getMeshNodeInfoSelect + "WHERE is_direct=true"

	setMeshNodeInfoNamesQuery = "UPDATE mesh_node_info SET long_name=$1, short_name=$2 WHERE id = $3"

	setMeshNodeInfoLastSeen = `
		INSERT INTO mesh_node_info (id, user_id, is_direct, last_seen)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET
			is_direct=excluded.is_direct,
			last_seen=excluded.last_seen
	`

	setMeshNodeInfoQuery = `
		INSERT INTO mesh_node_info (id, user_id, long_name, short_name, node_role, is_licensed, is_unmessageable, is_managed, is_direct, public_key, private_key, last_seen)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (id) DO UPDATE SET
			user_id=excluded.user_id,
			long_name=excluded.long_name,
			short_name=excluded.short_name,
			node_role=excluded.node_role,
			is_licensed=excluded.is_licensed,
			is_unmessageable=excluded.is_unmessageable,
			is_managed=excluded.is_managed,
			is_direct=excluded.is_direct,
			public_key=excluded.public_key,
			private_key=excluded.private_key,
			last_seen=excluded.last_seen
	`
)

type MeshNodeInfoQuery struct {
	*dbutil.QueryHelper[*MeshNodeInfo]
}

type MeshNodeInfo struct {
	qh *dbutil.QueryHelper[*MeshNodeInfo]

	NodeID         meshid.NodeID
	UserID         string
	LongName       string
	ShortName      string
	Role           string
	IsLicensed     bool
	IsUnmessagable bool
	IsManaged      bool
	IsDirect       bool
	PublicKey      []byte
	PrivateKey     []byte
	LastSeen       *time.Time
}

var _ dbutil.DataStruct[*MeshNodeInfo] = (*MeshNodeInfo)(nil)

func newMeshNodeInfo(qh *dbutil.QueryHelper[*MeshNodeInfo]) *MeshNodeInfo {
	return &MeshNodeInfo{qh: qh}
}

func (q *MeshNodeInfoQuery) GetByNodeID(ctx context.Context, nodeID meshid.NodeID) (*MeshNodeInfo, error) {
	return q.QueryOne(ctx, getMeshNodeInfoByNodeIDQuery, nodeID)
}

func (q *MeshNodeInfoQuery) GetByUserID(ctx context.Context, userID string) (*MeshNodeInfo, error) {
	return q.QueryOne(ctx, getMeshNodeInfoByUserIDQuery, userID)
}

func (q *MeshNodeInfoQuery) GetByShortUserID(ctx context.Context, userID string) (*MeshNodeInfo, error) {
	if ok, err := regexp.MatchString("^[0-9a-f]{4}$", userID); err != nil {
		return nil, err
	} else if !ok {
		return nil, errors.New("short user ID must be a four-character hex code")
	}
	return q.QueryOne(ctx, getMeshNodeInfoByShortUserIDQuery, fmt.Sprintf("!____%s", userID))
}

func (q *MeshNodeInfoQuery) GetDirectNeighbors(ctx context.Context) ([]*MeshNodeInfo, error) {
	return q.QueryMany(ctx, getMeshNodeInfoNeighborsQuery)
}

func (q *MeshNodeInfoQuery) GetManagedNodes(ctx context.Context) ([]*MeshNodeInfo, error) {
	return q.QueryMany(ctx, getMeshNodeInfoIsManagedQuery)
}

func (m *MeshNodeInfo) sqlVariables() []any {
	var ts *int64
	if m.LastSeen != nil {
		ts = ptr.Ptr(m.LastSeen.UTC().Unix())
	}
	return []any{m.NodeID, m.UserID, m.LongName, m.ShortName, m.Role, m.IsLicensed, m.IsUnmessagable, m.IsManaged, m.IsDirect, m.PublicKey, m.PrivateKey, ts}
}

func (m *MeshNodeInfo) SetAll(ctx context.Context) error {
	return m.qh.Exec(ctx, setMeshNodeInfoQuery, m.sqlVariables()...)
}

func (q *MeshNodeInfoQuery) SetNames(ctx context.Context, nodeID meshid.NodeID, longName, shortName string) error {
	return q.Exec(ctx, setMeshNodeInfoNamesQuery, longName, shortName, nodeID)
}

func (q *MeshNodeInfoQuery) SetLastSeen(ctx context.Context, nodeID meshid.NodeID, isDirect bool) error {
	return q.Exec(ctx, setMeshNodeInfoLastSeen, nodeID, nodeID.String(), isDirect, time.Now().Unix())
}

func (f *MeshNodeInfo) Scan(row dbutil.Scannable) (*MeshNodeInfo, error) {
	var ts *int64
	err := row.Scan(&f.NodeID, &f.UserID, &f.LongName, &f.ShortName, &f.Role, &f.IsLicensed, &f.IsUnmessagable, &f.IsManaged, &f.IsDirect, &f.PublicKey, &f.PrivateKey, &ts)
	if err == nil && ts != nil {
		f.LastSeen = ptr.Ptr(time.Unix(*ts, 0))
	}
	return f, err
}
