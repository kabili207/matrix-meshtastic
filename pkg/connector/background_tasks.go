package connector

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"maunium.net/go/mautrix/id"
)

const (
	// Rates are the standard defaults with a slight offset
	// https://pole1.co.uk/broadcastcalc/
	rateTelemetry    time.Duration = (1 * time.Hour) + (5 * time.Second)
	rateHostInfo     time.Duration = (1 * time.Hour) + (1 * time.Minute) + (7 * time.Second)
	rateNodeInfo     time.Duration = (3 * time.Hour) + (13 * time.Second)
	ratePosition     time.Duration = (3 * time.Hour) + (26 * time.Second)
	rateNeighborInfo time.Duration = (6 * time.Hour) + (31 * time.Second)
)

func init() {
}

func (mc *MeshtasticConnector) RunNodeInfoTask(ctx context.Context) error {
	go func() {
		mc.sendPeriodicNodeInfo(ctx)
		mc.sendPeriodicTelemetry(ctx)
		mc.sendPeriodicHostInfo(ctx)
		tickerTele := time.Tick(rateTelemetry)
		tickerHost := time.Tick(rateHostInfo)
		tickerNodeInfo := time.Tick(rateNodeInfo)
		tickerNeighbors := time.Tick(rateNeighborInfo)
		for {
			select {
			case <-ctx.Done():
				mc.log.Err(ctx.Err()).Msg("Stopping node info task")
				return
			case <-tickerTele:
				mc.sendPeriodicTelemetry(ctx)
			case <-tickerHost:
				mc.sendPeriodicHostInfo(ctx)
			case <-tickerNodeInfo:
				mc.sendPeriodicNodeInfo(ctx)
			case <-tickerNeighbors:
				mc.sendPeriodicNeighborInfo(ctx)
			}
		}
	}()
	return nil
}

func (c *MeshtasticConnector) sendPeriodicHostInfo(ctx context.Context) {
	err := c.meshClient.SendHostMetrics(c.GetBaseNodeID(), meshid.BROADCAST_ID)
	if err != nil {
		c.log.Err(err).Msg("Unable to send host metrics")
		return
	}
}

func (c *MeshtasticConnector) sendPeriodicTelemetry(ctx context.Context) {
	c.meshClient.SendTelemetry(c.GetBaseNodeID(), meshid.BROADCAST_ID)

	c.doForAllManagedGhosts(ctx, func(nodeID meshid.NodeID, meta *meshid.GhostMetadata) {
		c.log.Info().
			Str("long_name", meta.LongName).
			Stringer("node_id", nodeID).
			Msg("Broadcasting periodic telemetry")
		c.meshClient.SendTelemetry(nodeID, meshid.BROADCAST_ID)
	})
	// Offset things a tad so we don't overload the mesh
	c.meshClient.SendHostMetrics(c.GetBaseNodeID(), meshid.BROADCAST_ID)
}

func (c *MeshtasticConnector) sendPeriodicNeighborInfo(ctx context.Context) {

	ghosts, err := c.bridge.DB.Ghost.GetByMetadata(ctx, "is_managed", true)
	if err != nil {
		c.log.Err(err).Msg("Unable to fetch ghosts")
		return
	}

	directNeighbors, err := c.bridge.DB.Ghost.GetByMetadata(ctx, "direct_neighbor", true)
	if err != nil {
		c.log.Err(err).Msg("Unable to fetch ghosts")
		return
	}

	nodeIDs := []meshid.NodeID{c.GetBaseNodeID()}
	for _, g := range ghosts {
		if meta, ok := g.Metadata.(*meshid.GhostMetadata); ok {

			nodeIDs = append(nodeIDs, meshid.MXIDToNodeID(id.UserID(meta.UserMXID)))
		}
	}

	maxAge := time.Now().UTC().Add(-2 * rateNeighborInfo)

	for _, g := range directNeighbors {
		if meta, ok := g.Metadata.(*meshid.GhostMetadata); ok && meta.LastSeen != nil && meta.LastSeen.After(maxAge) {
			if id, err := meshid.ParseNodeID(string(g.ID)); err == nil {
				nodeIDs = append(nodeIDs, id)
			}
		}
	}
	c.meshClient.SendNeighborInfo(c.GetBaseNodeID(), nodeIDs)
	//c.doForAllManagedGhosts(ctx, func(nodeID meshid.NodeID, meta *meshid.GhostMetadata) {
	//	c.log.Info().
	//		Str("long_name", meta.LongName).
	//		Stringer("node_id", nodeID).
	//		Msg("Broadcasting periodic neighbor info")
	//	c.meshClient.SendNeighborInfo(nodeID, nodeIDs)
	//})
}

func (c *MeshtasticConnector) sendPeriodicNodeInfo(ctx context.Context) {
	c.meshClient.SendNodeInfo(c.GetBaseNodeID(), meshid.BROADCAST_ID, c.Config.LongName, c.Config.ShortName, false, nil)

	c.doForAllManagedGhosts(ctx, func(nodeID meshid.NodeID, meta *meshid.GhostMetadata) {
		c.log.Info().
			Str("long_name", meta.LongName).
			Stringer("node_id", nodeID).
			Msg("Broadcasting periodic node info")
		c.meshClient.SendNodeInfo(nodeID, meshid.BROADCAST_ID, meta.LongName, meta.ShortName, false, meta.PublicKey)
	})
}

func (c *MeshtasticConnector) doForAllManagedGhosts(ctx context.Context, callback func(nodeID meshid.NodeID, meta *meshid.GhostMetadata)) {
	ghosts, err := c.bridge.DB.Ghost.GetByMetadata(ctx, "is_managed", true)
	if err != nil {
		c.log.Err(err).Msg("Unable to fetch ghosts")
		return
	}

	for _, g := range ghosts {
		if meta, ok := g.Metadata.(*meshid.GhostMetadata); ok && meta.UserMXID != "" {
			waitTime := rand.N(300 * time.Second)
			go func(delay time.Duration, m *meshid.GhostMetadata) {
				select {
				case <-ctx.Done():
					break
				case <-time.After(delay):
					nodeID := meshid.MXIDToNodeID(id.UserID(m.UserMXID))
					callback(nodeID, meta)
				}

			}(waitTime, meta)
		}
	}
}
