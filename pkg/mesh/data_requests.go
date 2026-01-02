package mesh

import (
	"sync"
	"time"

	"github.com/kabili207/matrix-meshtastic/pkg/mesh/connectors"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
	"go.mau.fi/util/ptr"
)

// requestThrottle tracks the last time we responded to a specific request type
// to prevent flooding the mesh with responses
type requestThrottle struct {
	mu             sync.Mutex
	lastTelemetry  map[meshid.NodeID]time.Time
	lastNeighbor   map[meshid.NodeID]time.Time
	throttlePeriod time.Duration
}

func newRequestThrottle(period time.Duration) *requestThrottle {
	return &requestThrottle{
		lastTelemetry:  make(map[meshid.NodeID]time.Time),
		lastNeighbor:   make(map[meshid.NodeID]time.Time),
		throttlePeriod: period,
	}
}

func (t *requestThrottle) canRespondTelemetry(nodeID meshid.NodeID) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if lastTime, ok := t.lastTelemetry[nodeID]; ok {
		if time.Since(lastTime) < t.throttlePeriod {
			return false
		}
	}
	t.lastTelemetry[nodeID] = time.Now()
	return true
}

func (t *requestThrottle) canRespondNeighbor(nodeID meshid.NodeID) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if lastTime, ok := t.lastNeighbor[nodeID]; ok {
		if time.Since(lastTime) < t.throttlePeriod {
			return false
		}
	}
	t.lastNeighbor[nodeID] = time.Now()
	return true
}

// NeighborProvider is a function type that returns the list of neighbor node IDs
// for a given managed node. This allows the mesh client to request neighbor info
// from the higher-level application that manages node relationships.
type NeighborProvider func(nodeID meshid.NodeID) []meshid.NodeID

// handleTelemetryRequest handles an incoming telemetry request destined for a managed node.
// It responds with the appropriate telemetry data based on the request type.
func (c *MeshtasticClient) handleTelemetryRequest(packet connectors.NetworkMeshPacket, telemetry *pb.Telemetry) {
	toNode := meshid.NodeID(packet.To)
	fromNode := meshid.NodeID(packet.From)

	if !c.managedNodeFunc(toNode) {
		return
	}

	// Check throttle - firmware uses 3-minute cooldown
	if !c.requestThrottle.canRespondTelemetry(toNode) {
		c.log.Debug().
			Stringer("to", toNode).
			Stringer("from", fromNode).
			Msg("Skipping telemetry response due to throttle")
		return
	}

	c.log.Info().
		Stringer("to", toNode).
		Stringer("from", fromNode).
		Msg("Telemetry request received")

	encType := PSKEncryption
	if packet.ChannelName == "PKI" {
		encType = PKIEncryption
	}
	chanKey, _ := meshid.NewChannelDef(packet.ChannelName, packet.ChannelKey)

	// Determine what type of telemetry was requested and send response
	switch telemetry.Variant.(type) {
	case *pb.Telemetry_DeviceMetrics:
		c.SendTelemetryResponse(toNode, fromNode, chanKey, encType, packet.Id)
	case *pb.Telemetry_HostMetrics:
		if err := c.SendHostMetricsResponse(toNode, fromNode, chanKey, encType, packet.Id); err != nil {
			c.log.Err(err).Msg("Failed to send host metrics response")
		}
	default:
		// For empty requests, respond with device metrics (most common case)
		if telemetry.Variant == nil {
			c.SendTelemetryResponse(toNode, fromNode, chanKey, encType, packet.Id)
		}
	}
}

// handleNeighborInfoRequest handles an incoming neighbor info request destined for a managed node.
// It responds with the node's neighbor information.
func (c *MeshtasticClient) handleNeighborInfoRequest(packet connectors.NetworkMeshPacket, neighborInfo *pb.NeighborInfo) {
	toNode := meshid.NodeID(packet.To)
	fromNode := meshid.NodeID(packet.From)

	if !c.managedNodeFunc(toNode) {
		return
	}

	// Skip dummy/interceptable packets (single neighbor with nodeId=0, snr=0)
	if len(neighborInfo.Neighbors) == 1 &&
		neighborInfo.Neighbors[0].NodeId == 0 &&
		neighborInfo.Neighbors[0].Snr == 0 {
		c.log.Debug().
			Stringer("to", toNode).
			Stringer("from", fromNode).
			Msg("Ignoring dummy neighbor info request")
		return
	}

	// Check throttle - firmware uses 3-minute cooldown
	if !c.requestThrottle.canRespondNeighbor(toNode) {
		c.log.Debug().
			Stringer("to", toNode).
			Stringer("from", fromNode).
			Msg("Skipping neighbor info response due to throttle")
		return
	}

	c.log.Info().
		Stringer("to", toNode).
		Stringer("from", fromNode).
		Msg("Neighbor info request received")

	// Get neighbors from the provider if set
	var neighborIDs []meshid.NodeID
	if c.neighborProvider != nil {
		neighborIDs = c.neighborProvider(toNode)
	}

	encType := PSKEncryption
	if packet.ChannelName == "PKI" {
		encType = PKIEncryption
	}
	chanKey, _ := meshid.NewChannelDef(packet.ChannelName, packet.ChannelKey)

	broadcastInterval := ptr.Val(c.neighborBroadcastInterval)
	if err := c.SendNeighborInfoResponse(toNode, fromNode, neighborIDs, broadcastInterval, chanKey, encType, packet.Id); err != nil {
		c.log.Err(err).Msg("Failed to send neighbor info response")
	}
}
