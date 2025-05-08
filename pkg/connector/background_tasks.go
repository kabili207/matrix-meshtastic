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
		tickerTele := time.Tick(rateTelemetry)
		tickerNodeInfo := time.Tick(rateNodeInfo)
		tickerNeighbors := time.Tick(rateNeighborInfo)
		for {
			select {
			case <-ctx.Done():
				mc.log.Err(ctx.Err()).Msg("Stopping node info task")
				return
			case <-tickerTele:
				mc.sendPeriodicTelemetry(ctx)
			case <-tickerNodeInfo:
				mc.sendPeriodicNodeInfo(ctx)
			case <-tickerNeighbors:
				mc.sendPeriodicNeighborInfo(ctx)
			}
		}
	}()
	return nil
}

func (c *MeshtasticConnector) sendPeriodicTelemetry(ctx context.Context) {
	c.meshClient.SendTelemetry(c.GetBaseNodeID(), meshid.BROADCAST_ID)
	c.doForAllManagedGhosts(ctx, func(nodeID meshid.NodeID, meta *GhostMetadata) {
		c.log.Debug().Msgf("Broadcasting telemetry for %s (%s)", nodeID, meta.LongName)
		c.meshClient.SendTelemetry(nodeID, meshid.BROADCAST_ID)
	})
}

func (c *MeshtasticConnector) sendPeriodicNeighborInfo(ctx context.Context) {

	ghosts, err := c.bridge.DB.Ghost.GetByMetadata(ctx, "is_managed", true)
	if err != nil {
		c.log.Err(err).Msg("Unable to fetch ghosts")
		return
	}
	nodeIDs := []meshid.NodeID{c.GetBaseNodeID()}
	for _, g := range ghosts {
		if meta, ok := g.Metadata.(*GhostMetadata); ok {
			nodeIDs = append(nodeIDs, meshid.MXIDToNodeID(id.UserID(meta.UserMXID)))
		}
	}
	c.meshClient.SendNeighborInfo(c.GetBaseNodeID(), nodeIDs)
	c.doForAllManagedGhosts(ctx, func(nodeID meshid.NodeID, meta *GhostMetadata) {
		c.log.Debug().Msgf("Sending neighbor info for %s (%s)", nodeID, meta.LongName)
		c.meshClient.SendNeighborInfo(nodeID, nodeIDs)
	})
}

func (c *MeshtasticConnector) sendPeriodicNodeInfo(ctx context.Context) {
	c.meshClient.SendNodeInfo(c.GetBaseNodeID(), meshid.BROADCAST_ID, c.Config.LongName, c.Config.ShortName, false, nil)

	c.doForAllManagedGhosts(ctx, func(nodeID meshid.NodeID, meta *GhostMetadata) {
		c.log.Debug().Msgf("Broadcasting node info for %s (%s)", nodeID, meta.LongName)
		c.meshClient.SendNodeInfo(nodeID, meshid.BROADCAST_ID, meta.LongName, meta.ShortName, false, meta.PublicKey)
	})
}

func (c *MeshtasticConnector) doForAllManagedGhosts(ctx context.Context, callback func(nodeID meshid.NodeID, meta *GhostMetadata)) {
	ghosts, err := c.bridge.DB.Ghost.GetByMetadata(ctx, "is_managed", true)
	if err != nil {
		c.log.Err(err).Msg("Unable to fetch ghosts")
		return
	}

	for _, g := range ghosts {
		if meta, ok := g.Metadata.(*GhostMetadata); ok {
			waitTime := rand.N(300 * time.Second)
			go func(delay time.Duration, m *GhostMetadata) {
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
