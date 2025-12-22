package connector

import (
	"context"
	"math/rand/v2"
	"sort"
	"time"

	"github.com/kabili207/matrix-meshtastic/pkg/connector/meshdb"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

const (
	// Rates are the standard defaults with a slight offset
	// https://github.com/meshtastic/firmware/blob/master/src/mesh/Default.h
	rateTelemetry       time.Duration = (6 * time.Hour) + (5 * time.Second)
	rateHostInfo        time.Duration = (12 * time.Hour) + (7 * time.Second) // Only sent by bridge, which is a router
	rateNodeInfo        time.Duration = (3 * time.Hour) + (13 * time.Second)
	ratePosition        time.Duration = (3 * time.Hour) + (26 * time.Second)
	rateNeighborInfo    time.Duration = (12 * time.Hour) + (31 * time.Second)
	rateInactiveCleanup time.Duration = 24 * time.Hour
)

func init() {
}

func (mc *MeshtasticConnector) RunNodeInfoTask(ctx context.Context) error {
	go func() {
		mc.sendPeriodicNodeInfo(ctx)
		tickerTele := time.NewTicker(rateTelemetry)
		tickerHost := time.NewTicker(rateHostInfo)
		tickerNodeInfo := time.NewTicker(rateNodeInfo)
		tickerNeighbors := time.NewTicker(rateNeighborInfo)
		for {
			select {
			case <-ctx.Done():
				mc.log.Err(ctx.Err()).Msg("Stopping node info task")
				return
			case <-tickerTele.C:
				mc.sendPeriodicTelemetry(ctx)
			case <-tickerHost.C:
				mc.sendPeriodicHostInfo(ctx)
			case <-tickerNodeInfo.C:
				mc.sendPeriodicNodeInfo(ctx)
			case <-tickerNeighbors.C:
				mc.sendPeriodicNeighborInfo(ctx)
			}
		}
	}()
	return nil
}

func (c *MeshtasticConnector) throttleDuration(d time.Duration) time.Duration {
	// TODO: consult the firmware to apply throttling measures if needed
	// https://github.com/meshtastic/firmware/blob/master/src/mesh/Default.h
	return d
}

func (c *MeshtasticConnector) sendPeriodicHostInfo(_ context.Context) {
	err := c.meshClient.SendHostMetrics(c.GetBaseNodeID(), meshid.BROADCAST_ID)
	if err != nil {
		c.log.Err(err).Msg("Unable to send host metrics")
		return
	}
}

func (c *MeshtasticConnector) sendPeriodicTelemetry(ctx context.Context) {
	c.meshClient.SendTelemetry(c.GetBaseNodeID(), meshid.BROADCAST_ID)

	c.doForAllManagedGhosts(ctx, func(meta *meshdb.MeshNodeInfo) {
		c.log.Info().
			Str("long_name", meta.LongName).
			Str("node_id", meta.UserID).
			Msg("Broadcasting periodic telemetry")
		c.meshClient.SendTelemetry(meta.NodeID, meshid.BROADCAST_ID)
	})
	// TODO: Offset things a tad so we don't overload the mesh
	c.meshClient.SendHostMetrics(c.GetBaseNodeID(), meshid.BROADCAST_ID)
}

func (c *MeshtasticConnector) sendPeriodicNeighborInfo(ctx context.Context) {

	directNeighbors, err := c.meshDB.MeshNodeInfo.GetDirectNeighbors(ctx)
	if err != nil {
		c.log.Err(err).Msg("Unable to fetch direct neighbors")
		return
	}

	sort.Slice(directNeighbors, func(i, j int) bool {
		return directNeighbors[i].LastSeen.After(*directNeighbors[j].LastSeen)
	})

	nodeIDs := []meshid.NodeID{c.GetBaseNodeID()}
	maxAge := time.Now().UTC().Add(-2 * rateNeighborInfo)

	for _, g := range directNeighbors {
		if g.LastSeen.After(maxAge) {
			nodeIDs = append(nodeIDs, g.NodeID)
		} else {
			break
		}
	}

	if err := c.meshClient.SendNeighborInfo(c.GetBaseNodeID(), nodeIDs, uint32(rateNeighborInfo.Seconds())); err != nil {
		c.log.Err(err).Msg("Failed to send periodic neighbor info")
	}
}

func (c *MeshtasticConnector) sendPeriodicNodeInfo(ctx context.Context) {
	pubKey, _ := c.getGhostPublicKey(ctx, c.GetBaseNodeID())
	c.meshClient.SendNodeInfo(c.GetBaseNodeID(), meshid.BROADCAST_ID, c.Config.LongName, c.Config.ShortName, false, pubKey)

	c.doForAllManagedGhosts(ctx, func(meta *meshdb.MeshNodeInfo) {
		log := c.log.Info().
			Str("long_name", meta.LongName).
			Str("node_id", meta.UserID)

		log.Msg("Broadcasting periodic node info")
		if err := c.meshClient.SendNodeInfo(meta.NodeID, meshid.BROADCAST_ID, meta.LongName, meta.ShortName, false, meta.PublicKey); err != nil {
			log.Err(err).Msg("Broadcasting node info failed")
		}
	})
}

func (c *MeshtasticConnector) doForAllManagedGhosts(ctx context.Context, callback func(meta *meshdb.MeshNodeInfo)) {
	ghosts, err := c.meshDB.MeshNodeInfo.GetManagedNodes(ctx)
	if err != nil {
		c.log.Err(err).Msg("Unable to fetch managed nodes")
		return
	}

	maxAge := time.Now().UTC().Add(-7 * 24 * time.Hour)

	for _, g := range ghosts {
		if g.NodeID != c.GetBaseNodeID() && g.LastSeen != nil && g.LastSeen.After(maxAge) {
			waitTime := rand.N(300 * time.Second)
			go func(delay time.Duration, m *meshdb.MeshNodeInfo) {
				select {
				case <-ctx.Done():
					break
				case <-time.After(delay):
					callback(g)
				}

			}(waitTime, g)
		}
	}
}

// RunInactiveCleanupTask starts the background task for cleaning up inactive nodes from channel portals
func (c *MeshtasticConnector) RunInactiveCleanupTask(ctx context.Context) {
	threshold := c.Config.InactivityThreshold
	if threshold <= 0 {
		c.log.Info().Msg("Inactive node cleanup is disabled (inactivity_threshold_days <= 0)")
		return
	}

	c.log.Info().
		Int("threshold_days", threshold).
		Msg("Starting inactive node cleanup task")

	go func() {
		// Run immediately on startup
		c.cleanupInactiveNodes(ctx)

		ticker := time.NewTicker(rateInactiveCleanup)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				c.log.Info().Msg("Stopping inactive node cleanup task")
				return
			case <-ticker.C:
				c.cleanupInactiveNodes(ctx)
			}
		}
	}()
}

// cleanupInactiveNodes removes inactive ghosts from all channel portals
func (c *MeshtasticConnector) cleanupInactiveNodes(ctx context.Context) {
	threshold := time.Now().Add(-time.Duration(c.Config.InactivityThreshold) * 24 * time.Hour)

	c.log.Info().
		Time("threshold", threshold).
		Msg("Running inactive node cleanup")

	portals, err := c.bridge.GetAllPortalsWithMXID(ctx)
	if err != nil {
		c.log.Err(err).Msg("Failed to get portals for inactive cleanup")
		return
	}

	totalRemoved := 0
	for _, portal := range portals {
		// Only process channel portals, not DMs
		if portal.RoomType != database.RoomTypeDefault {
			continue
		}

		removed, err := c.cleanupPortalInactiveMembers(ctx, portal, threshold)
		if err != nil {
			c.log.Err(err).
				Str("portal_id", string(portal.ID)).
				Msg("Failed to cleanup inactive members in portal")
			continue
		}
		totalRemoved += removed
	}

	if totalRemoved > 0 {
		c.log.Info().
			Int("total_removed", totalRemoved).
			Msg("Inactive node cleanup completed")
	} else {
		c.log.Debug().Msg("Inactive node cleanup completed, no nodes removed")
	}
}

// cleanupPortalInactiveMembers removes inactive ghosts from a single portal
func (c *MeshtasticConnector) cleanupPortalInactiveMembers(ctx context.Context, portal *bridgev2.Portal, threshold time.Time) (int, error) {
	if portal.MXID == "" {
		return 0, nil
	}

	// Get the matrix connector to access room members
	matrixConn, ok := c.bridge.Matrix.(*matrix.Connector)
	if !ok {
		return 0, nil
	}

	// Get room members
	members, err := matrixConn.Bot.JoinedMembers(ctx, portal.MXID)
	if err != nil {
		return 0, err
	}

	removed := 0
	thresholdNanos := threshold.UnixNano()

	for memberMXID := range members.Joined {
		// Check if this is a ghost user
		ghostID, ok := c.bridge.Matrix.ParseGhostMXID(memberMXID)
		if !ok {
			continue
		}

		// Parse the node ID from the ghost ID
		nodeID, err := meshid.ParseUserID(ghostID)
		if err != nil {
			continue
		}

		// Never remove managed nodes
		if c.IsManagedNode(nodeID) {
			continue
		}

		// Get last activity in this portal
		lastActivity := c.getLastActivityInPortal(ctx, ghostID, portal.PortalKey)
		if lastActivity != nil && lastActivity.UnixNano() >= thresholdNanos {
			// Node is active, skip
			continue
		}

		// Remove the ghost from the portal
		if err := c.removeGhostFromPortal(ctx, matrixConn, memberMXID, portal.MXID); err != nil {
			c.log.Err(err).
				Str("ghost_id", string(ghostID)).
				Str("portal_id", string(portal.ID)).
				Msg("Failed to remove inactive ghost from portal")
			continue
		}

		c.log.Info().
			Str("ghost_id", string(ghostID)).
			Str("portal_id", string(portal.ID)).
			Stringer("node_id", nodeID).
			Msg("Removed inactive ghost from portal")
		removed++
	}

	return removed, nil
}

// getLastActivityInPortal returns the most recent message or reaction timestamp for a ghost in a portal
func (c *MeshtasticConnector) getLastActivityInPortal(ctx context.Context, senderID networkid.UserID, portalKey networkid.PortalKey) *time.Time {
	bridgeID := c.bridge.ID

	// Query for latest message timestamp
	var msgTimestamp *int64
	msgQuery := `SELECT MAX(timestamp) FROM message WHERE bridge_id = $1 AND room_id = $2 AND room_receiver = $3 AND sender_id = $4`
	err := c.bridge.DB.Database.QueryRow(ctx, msgQuery, bridgeID, string(portalKey.ID), string(portalKey.Receiver), string(senderID)).Scan(&msgTimestamp)
	if err != nil {
		c.log.Debug().Err(err).Msg("Failed to query message activity")
	}

	// Query for latest reaction timestamp
	var rxnTimestamp *int64
	rxnQuery := `SELECT MAX(timestamp) FROM reaction WHERE bridge_id = $1 AND room_id = $2 AND room_receiver = $3 AND sender_id = $4`
	err = c.bridge.DB.Database.QueryRow(ctx, rxnQuery, bridgeID, string(portalKey.ID), string(portalKey.Receiver), string(senderID)).Scan(&rxnTimestamp)
	if err != nil {
		c.log.Debug().Err(err).Msg("Failed to query reaction activity")
	}

	// Return the more recent of the two
	var latest *int64
	if msgTimestamp != nil && rxnTimestamp != nil {
		if *msgTimestamp > *rxnTimestamp {
			latest = msgTimestamp
		} else {
			latest = rxnTimestamp
		}
	} else if msgTimestamp != nil {
		latest = msgTimestamp
	} else if rxnTimestamp != nil {
		latest = rxnTimestamp
	}

	if latest == nil {
		return nil
	}

	// Convert nanoseconds to time.Time
	t := time.Unix(0, *latest)
	return &t
}

// removeGhostFromPortal removes a ghost from a portal room
func (c *MeshtasticConnector) removeGhostFromPortal(ctx context.Context, matrixConn *matrix.Connector, memberMXID id.UserID, roomID id.RoomID) error {
	_, err := matrixConn.AS.Intent(memberMXID).LeaveRoom(ctx, roomID)
	return err
}
