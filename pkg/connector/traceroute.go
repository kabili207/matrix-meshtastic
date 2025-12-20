package connector

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/kabili207/matrix-meshtastic/pkg/mesh"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

const (
	// TracerouteTimeout is the firmware-enforced minimum time between traceroute requests
	TracerouteTimeout = 30 * time.Second
	// TracerouteResponseTimeout is how long to wait for a traceroute response before giving up
	TracerouteResponseTimeout = 60 * time.Second
)

// TracerouteRequest tracks a pending traceroute request
type TracerouteRequest struct {
	PacketID   uint32
	FromNode   meshid.NodeID
	TargetNode meshid.NodeID
	RoomID     id.RoomID
	Timestamp  time.Time
	Channel    meshid.ChannelDef
}

// TracerouteTracker manages pending traceroute requests and rate limiting
type TracerouteTracker struct {
	mu               sync.Mutex
	pendingRequests  map[uint32]*TracerouteRequest           // keyed by packet ID
	lastTraceroute   map[tracerouteKey]time.Time             // rate limiting per from-to pair
}

// tracerouteKey is used to track rate limits per source-destination pair
type tracerouteKey struct {
	From meshid.NodeID
	To   meshid.NodeID
}

// NewTracerouteTracker creates a new TracerouteTracker
func NewTracerouteTracker() *TracerouteTracker {
	return &TracerouteTracker{
		pendingRequests: make(map[uint32]*TracerouteRequest),
		lastTraceroute:  make(map[tracerouteKey]time.Time),
	}
}

// CanSendTraceroute checks if enough time has passed since the last traceroute to this destination
func (t *TracerouteTracker) CanSendTraceroute(from, to meshid.NodeID) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	key := tracerouteKey{From: from, To: to}
	if lastTime, ok := t.lastTraceroute[key]; ok {
		elapsed := time.Since(lastTime)
		if elapsed < TracerouteTimeout {
			return false, TracerouteTimeout - elapsed
		}
	}
	return true, 0
}

// AddRequest adds a new pending traceroute request
func (t *TracerouteTracker) AddRequest(req *TracerouteRequest) {
	t.mu.Lock()
	defer t.mu.Unlock()

	key := tracerouteKey{From: req.FromNode, To: req.TargetNode}
	t.lastTraceroute[key] = req.Timestamp
	t.pendingRequests[req.PacketID] = req
}

// GetAndRemoveRequest retrieves and removes a pending request by packet ID
func (t *TracerouteTracker) GetAndRemoveRequest(packetID uint32) *TracerouteRequest {
	t.mu.Lock()
	defer t.mu.Unlock()

	req, ok := t.pendingRequests[packetID]
	if ok {
		delete(t.pendingRequests, packetID)
	}
	return req
}

// CleanupExpired removes expired pending requests
func (t *TracerouteTracker) CleanupExpired() {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	for packetID, req := range t.pendingRequests {
		if now.Sub(req.Timestamp) > TracerouteResponseTimeout {
			delete(t.pendingRequests, packetID)
		}
	}

	// Also clean up old rate limit entries
	for key, lastTime := range t.lastTraceroute {
		if now.Sub(lastTime) > TracerouteTimeout*2 {
			delete(t.lastTraceroute, key)
		}
	}
}

// handleMeshTraceroute handles traceroute response events
func (c *MeshtasticConnector) handleMeshTraceroute(evt *mesh.MeshTracerouteEvent) {
	log := c.log.With().
		Str("action", "handle_traceroute_response").
		Stringer("from_node_id", evt.From).
		Stringer("to_node_id", evt.To).
		Uint32("request_id", evt.RequestId).
		Logger()

	// Look up the pending request using the RequestId
	req := c.tracerouteTracker.GetAndRemoveRequest(evt.RequestId)
	if req == nil {
		log.Debug().Msg("Received traceroute response for unknown request, ignoring")
		return
	}

	log.Info().
		Stringer("target_node", req.TargetNode).
		Str("room_id", req.RoomID.String()).
		Msg("Received traceroute response")

	// Format the route for display
	message := c.formatTracerouteResponse(evt, req)

	// Send the response to the Matrix room
	ctx := context.Background()
	c.sendNoticeToRoom(ctx, req.RoomID, message)
}

// formatTracerouteResponse creates a human-readable traceroute result
func (c *MeshtasticConnector) formatTracerouteResponse(evt *mesh.MeshTracerouteEvent, req *TracerouteRequest) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("**Traceroute to %s**\n", req.TargetNode))

	// Format forward route
	sb.WriteString("**Route:**\n")
	sb.WriteString(fmt.Sprintf("  %s (origin)\n", req.FromNode))

	for i, nodeID := range evt.Route {
		node := meshid.NodeID(nodeID)
		snrStr := "?"
		if i < len(evt.SnrTowards) && evt.SnrTowards[i] != math.MinInt8 {
			snrStr = fmt.Sprintf("%.2f", float32(evt.SnrTowards[i])/4)
		}
		nodeName := c.getNodeDisplayName(node)
		sb.WriteString(fmt.Sprintf("  → %s (%s dB)\n", nodeName, snrStr))
	}

	// Final hop SNR (received at destination)
	destSnrStr := "?"
	if len(evt.SnrTowards) > 0 {
		lastSnr := evt.SnrTowards[len(evt.SnrTowards)-1]
		if lastSnr != math.MinInt8 {
			destSnrStr = fmt.Sprintf("%.2f", float32(lastSnr)/4)
		}
	}
	sb.WriteString(fmt.Sprintf("  → %s (destination, %s dB)\n", c.getNodeDisplayName(req.TargetNode), destSnrStr))

	// Format return route if available
	if len(evt.RouteBack) > 0 {
		sb.WriteString("\n**Return Route:**\n")
		sb.WriteString(fmt.Sprintf("  %s (origin)\n", c.getNodeDisplayName(req.TargetNode)))

		for i, nodeID := range evt.RouteBack {
			node := meshid.NodeID(nodeID)
			snrStr := "?"
			if i < len(evt.SnrBack) && evt.SnrBack[i] != math.MinInt8 {
				snrStr = fmt.Sprintf("%.2f", float32(evt.SnrBack[i])/4)
			}
			nodeName := c.getNodeDisplayName(node)
			sb.WriteString(fmt.Sprintf("  → %s (%s dB)\n", nodeName, snrStr))
		}

		// Final hop SNR for return
		returnSnrStr := "?"
		if len(evt.SnrBack) > 0 {
			lastSnr := evt.SnrBack[len(evt.SnrBack)-1]
			if lastSnr != math.MinInt8 {
				returnSnrStr = fmt.Sprintf("%.2f", float32(lastSnr)/4)
			}
		}
		sb.WriteString(fmt.Sprintf("  → %s (destination, %s dB)\n", req.FromNode, returnSnrStr))
	}

	return sb.String()
}

// getNodeDisplayName returns a display name for a node, using long name if available
func (c *MeshtasticConnector) getNodeDisplayName(nodeID meshid.NodeID) string {
	ctx := context.Background()
	nodeInfo, err := c.meshDB.MeshNodeInfo.GetByNodeID(ctx, nodeID)
	if err == nil && nodeInfo != nil && nodeInfo.LongName != "" {
		return fmt.Sprintf("%s (%s)", nodeInfo.LongName, nodeID)
	}
	return nodeID.String()
}

// sendNoticeToRoom sends a notice message to a Matrix room
func (c *MeshtasticConnector) sendNoticeToRoom(ctx context.Context, roomID id.RoomID, message string) {
	content := &event.Content{
		Parsed: &event.MessageEventContent{
			MsgType: event.MsgNotice,
			Body:    message,
		},
	}
	_, err := c.bridge.Bot.SendMessage(ctx, roomID, event.EventMessage, content, nil)
	if err != nil {
		c.log.Err(err).Str("room_id", roomID.String()).Msg("Failed to send traceroute response to room")
	}
}
