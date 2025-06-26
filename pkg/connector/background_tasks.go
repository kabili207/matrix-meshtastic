package connector

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/kabili207/matrix-meshtastic/pkg/connector/meshdb"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
)

const (
	// Rates are the standard defaults with a slight offset
	// https://github.com/meshtastic/firmware/blob/master/src/mesh/Default.h
	rateTelemetry    time.Duration = (6 * time.Hour) + (5 * time.Second)
	rateHostInfo     time.Duration = (12 * time.Hour) + (7 * time.Second) // Only sent by bridge, which is a router
	rateNodeInfo     time.Duration = (3 * time.Hour) + (13 * time.Second)
	ratePosition     time.Duration = (3 * time.Hour) + (26 * time.Second)
	rateNeighborInfo time.Duration = (12 * time.Hour) + (31 * time.Second)
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
	// Offset things a tad so we don't overload the mesh
	c.meshClient.SendHostMetrics(c.GetBaseNodeID(), meshid.BROADCAST_ID)
}

func (c *MeshtasticConnector) sendPeriodicNeighborInfo(ctx context.Context) {

	directNeighbors, err := c.meshDB.MeshNodeInfo.GetDirectNeighbors(ctx)
	if err != nil {
		c.log.Err(err).Msg("Unable to fetch direct neighbors")
		return
	}

	nodeIDs := []meshid.NodeID{c.GetBaseNodeID()}
	maxAge := time.Now().UTC().Add(-2 * rateNeighborInfo)

	for _, g := range directNeighbors {
		if g.LastSeen != nil && g.LastSeen.After(maxAge) {
			nodeIDs = append(nodeIDs, g.NodeID)
		}
	}
	c.meshClient.SendNeighborInfo(c.GetBaseNodeID(), nodeIDs, uint32(rateNeighborInfo.Seconds()))
}

func (c *MeshtasticConnector) sendPeriodicNodeInfo(ctx context.Context) {
	c.meshClient.SendNodeInfo(c.GetBaseNodeID(), meshid.BROADCAST_ID, c.Config.LongName, c.Config.ShortName, false, nil)

	c.doForAllManagedGhosts(ctx, func(meta *meshdb.MeshNodeInfo) {
		c.log.Info().
			Str("long_name", meta.LongName).
			Str("node_id", meta.UserID).
			Msg("Broadcasting periodic node info")
		c.meshClient.SendNodeInfo(meta.NodeID, meshid.BROADCAST_ID, meta.LongName, meta.ShortName, false, meta.PublicKey)
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
