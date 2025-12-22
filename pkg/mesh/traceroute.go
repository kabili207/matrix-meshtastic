package mesh

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/kabili207/matrix-meshtastic/pkg/mesh/connectors"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
)

// handleTracerouteRequest handles an incoming traceroute request destined for a managed node.
// It processes the forward route, adds the bridge's contribution, and sends back the response.
func (c *MeshtasticClient) handleTracerouteRequest(packet connectors.NetworkMeshPacket, disco *pb.RouteDiscovery) {
	toNode := meshid.NodeID(packet.To)
	fromNode := meshid.NodeID(packet.From)

	if !c.managedNodeFunc(toNode) {
		return
	}

	if packet.WantAck {
		c.SendAck(toNode, fromNode, packet.Id)
	}

	// Insert placeholder entries for nodes that didn't add themselves to the route
	// (older firmware or nodes without the channel key)
	c.insertUnknownHops(packet.MeshPacket, disco, true)

	// Add gateway node to forward route if present and not already included
	c.addGatewayToRoute(packet, disco, true)

	// If the destination is a managed node but not the bridge itself,
	// we're relaying on behalf of that node - add bridge to both routes
	if toNode != c.nodeId {
		c.addNodeToRouteIfMissing(disco, c.nodeId, 0, true)  // Forward route
		c.addNodeToRouteIfMissing(disco, c.nodeId, 0, false) // Return route (for the response)
	}

	// Add final SNR entry for the destination
	disco.SnrTowards = append(disco.SnrTowards, 0)

	c.logRoute(disco, packet.From, packet.To)

	chanKey, _ := meshid.NewChannelDef(packet.ChannelName, packet.ChannelKey)
	c.sendProtoMessage(chanKey, disco, PacketInfo{
		PortNum:   pb.PortNum_TRACEROUTE_APP,
		Encrypted: PSKEncryption,
		From:      toNode,
		To:        fromNode,
		RequestId: packet.Id,
	})
}

// handleTracerouteResponse processes an incoming traceroute response.
// The response is traveling back to us (the originator of the request).
func (c *MeshtasticClient) handleTracerouteResponse(packet connectors.NetworkMeshPacket, disco *pb.RouteDiscovery) {
	toNode := meshid.NodeID(packet.To)

	if !c.managedNodeFunc(toNode) {
		return
	}

	// Insert placeholder entries for nodes that didn't add themselves to the route
	// (older firmware or nodes without the channel key)
	c.insertUnknownHops(packet.MeshPacket, disco, false)

	// Add gateway node to return route if present and not already included
	c.addGatewayToRoute(packet, disco, false)

	// If the destination is a managed node but not the bridge itself,
	// add the bridge to the return route (it's the last real hop before the managed node)
	if toNode != c.nodeId {
		c.addNodeToRouteIfMissing(disco, c.nodeId, 0, false)
	}

	// Add final SNR entry for the return path destination (the managed node)
	// Use 0 since we don't have real SNR data for this virtual hop
	disco.SnrBack = append(disco.SnrBack, 0)
}

// insertUnknownHops adds placeholder entries for hops that didn't add themselves to the route.
// This handles older firmware versions that don't support traceroutes or nodes without the channel key.
func (c *MeshtasticClient) insertUnknownHops(packet *pb.MeshPacket, disco *pb.RouteDiscovery, isForwardRoute bool) {
	var route *[]uint32
	var snrList *[]int32

	if isForwardRoute {
		route = &disco.Route
		snrList = &disco.SnrTowards
	} else {
		route = &disco.RouteBack
		snrList = &disco.SnrBack
	}

	if packet.HopStart == 0 || packet.HopLimit > packet.HopStart {
		return
	}

	hopsTaken := int(packet.HopStart - packet.HopLimit)
	routeCount := len(*route)

	// Add unknown node placeholders for missing hops
	for i := routeCount; i < hopsTaken; i++ {
		*route = append(*route, uint32(meshid.BROADCAST_ID))
	}

	// Ensure SNR list matches route length
	for len(*snrList) < len(*route) {
		*snrList = append(*snrList, math.MinInt8) // MinInt8 == SNR Unknown
	}
}

// addGatewayToRoute adds the gateway node to the appropriate route if it's not already present.
func (c *MeshtasticClient) addGatewayToRoute(packet connectors.NetworkMeshPacket, disco *pb.RouteDiscovery, isForwardRoute bool) {
	if packet.GatewayNode == 0 || packet.GatewayNode == meshid.NodeID(packet.From) {
		return
	}

	c.addNodeToRouteIfMissing(disco, packet.GatewayNode, int32(packet.RxSnr*4), isForwardRoute)
}

// addNodeToRouteIfMissing adds a node to the specified route if it's not already present.
func (c *MeshtasticClient) addNodeToRouteIfMissing(disco *pb.RouteDiscovery, nodeID meshid.NodeID, snr int32, isForwardRoute bool) {
	if isForwardRoute {
		if !slices.Contains(disco.Route, uint32(nodeID)) {
			disco.Route = append(disco.Route, uint32(nodeID))
			disco.SnrTowards = append(disco.SnrTowards, snr)
		}
	} else {
		if !slices.Contains(disco.RouteBack, uint32(nodeID)) {
			disco.RouteBack = append(disco.RouteBack, uint32(nodeID))
			disco.SnrBack = append(disco.SnrBack, snr)
		}
	}
}

func (c *MeshtasticClient) logRoute(r *pb.RouteDiscovery, origin, dest uint32) {
	var route strings.Builder

	route.WriteString(fmt.Sprintf("0x%x --> ", origin))

	for i := 0; i < len(r.Route); i++ {
		if i < len(r.SnrTowards) && r.SnrTowards[i] != math.MinInt8 {
			route.WriteString(fmt.Sprintf("0x%x (%.2fdB) --> ", r.Route[i], float32(r.SnrTowards[i])/4))
		} else {
			route.WriteString(fmt.Sprintf("0x%x (?dB) --> ", r.Route[i]))
		}
	}

	if len(r.SnrTowards) > 0 && r.SnrTowards[len(r.SnrTowards)-1] != math.MinInt8 {
		route.WriteString(fmt.Sprintf("0x%x (%.2fdB)", dest, float32(r.SnrTowards[len(r.SnrTowards)-1])/4))
	} else {
		route.WriteString(fmt.Sprintf("0x%x (?dB)", dest))
	}

	log := c.log.With().
		Stringer("from", meshid.NodeID(origin)).
		Stringer("to", meshid.NodeID(dest)).
		Str("route", route.String()).Logger()
	log.Info().Msg("Traceroute request received")
}
