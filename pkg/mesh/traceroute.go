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

func (c *MeshtasticClient) handleTraceroute(packet connectors.NetworkMeshPacket, disco *pb.RouteDiscovery) {

	toNode := meshid.NodeID(packet.To)
	fromNode := meshid.NodeID(packet.From)
	if !c.managedNodeFunc(toNode) {
		return
	}

	if packet.WantAck {
		c.SendAck(toNode, fromNode, packet.Id)
	}

	// We never process packets that *aren't* to us, so this is always be true,
	// In the firmware, this would check if message.RequestId == 0
	isTowardsDestination := true

	c.insertUnknownHops(packet.MeshPacket, disco, isTowardsDestination)

	// Gateway node isn't always included in the route list, so ensure we add it
	if packet.GatewayNode != 0 && packet.GatewayNode != fromNode && !slices.Contains(disco.Route, uint32(packet.GatewayNode)) {
		disco.Route = append(disco.RouteBack, uint32(packet.GatewayNode))
		disco.SnrTowards = append(disco.SnrBack, int32(packet.RxSnr*4))
	}

	// Add forward and backward info if dest is not the bridge itself
	if toNode != c.nodeId {
		packet.HopLimit -= 1
		c.appendMyIdAndSnr(disco, isTowardsDestination, false)
		c.appendMyIdAndSnr(disco, !isTowardsDestination, false)
	}

	c.appendMyIdAndSnr(disco, isTowardsDestination, true)

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

func (c *MeshtasticClient) insertUnknownHops(packet *pb.MeshPacket, disco *pb.RouteDiscovery, isTowardsDestination bool) {
	// Insert unknown
	var routeCount = 0
	var snrCount = 0
	var route *[]uint32
	var snrList *[]int32

	if isTowardsDestination {
		routeCount = len(disco.Route)
		snrCount = len(disco.SnrTowards)
		route = &disco.Route
		snrList = &disco.SnrTowards
	} else {
		routeCount = len(disco.RouteBack)
		snrCount = len(disco.SnrBack)
		route = &disco.RouteBack
		snrList = &disco.SnrBack
	}

	if packet.HopStart != 0 && packet.HopLimit <= packet.HopStart {
		hopsTaken := packet.HopStart - packet.HopLimit
		diff := int(hopsTaken) - routeCount

		for i := 0; i < diff; i++ {
			if routeCount < len(*route) {
				r := append(*route, uint32(meshid.BROADCAST_ID))
				route = &r
				routeCount += 1
			}
		}

		diff = routeCount - snrCount
		for i := 0; i < diff; i++ {
			if snrCount < len(*snrList) {
				s := append(*snrList, math.MinInt8) // Min == SNR Unknown
				snrList = &s
				snrCount += 1
			}
		}
	}
}

func (c *MeshtasticClient) appendMyIdAndSnr(disco *pb.RouteDiscovery, isTowardsDestination bool, snrOnly bool) {
	// Insert unknown
	var routeCount = 0
	var snrCount = 0
	var route *[]uint32
	var snrList *[]int32

	if isTowardsDestination {
		routeCount = len(disco.Route)
		snrCount = len(disco.SnrTowards)
		route = &disco.Route
		snrList = &disco.SnrTowards
	} else {
		routeCount = len(disco.RouteBack)
		snrCount = len(disco.SnrBack)
		route = &disco.RouteBack
		snrList = &disco.SnrBack
	}

	if snrCount <= len(*route) {
		s := append(*snrList, 0)
		snrList = &s
		snrCount += 1

		if isTowardsDestination {
			disco.SnrTowards = *snrList
		} else {
			disco.SnrBack = *snrList
		}
	}

	if snrOnly {
		return
	}

	if routeCount <= len(*route) {
		r := append(*route, uint32(c.nodeId))
		route = &r

		if isTowardsDestination {
			disco.Route = *route
		} else {
			disco.RouteBack = *route
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
