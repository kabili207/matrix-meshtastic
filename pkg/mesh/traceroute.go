package mesh

import (
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
)

func (c *MeshtasticClient) handleTraceroute(env *pb.ServiceEnvelope, message *pb.Data, disco *pb.RouteDiscovery) {

	toNode := meshid.NodeID(env.Packet.To)
	fromNode := meshid.NodeID(env.Packet.From)
	if !c.managedNodeFunc(toNode) {
		return
	}

	isTowardsDestination := message.RequestId == 0

	c.insertUnknownHops(env.Packet, disco, isTowardsDestination)
	if toNode != c.nodeId {
		env.Packet.HopLimit -= 1
		c.appendMyIdAndSnr(env.Packet, disco, isTowardsDestination, false)
	}
	c.appendMyIdAndSnr(env.Packet, disco, isTowardsDestination, true)

	if toNode != c.nodeId {
		c.appendMyIdAndSnr(env.Packet, disco, !isTowardsDestination, false)
	}

	c.sendProtoMessage(env.ChannelId, disco, PacketInfo{
		PortNum:   pb.PortNum_TRACEROUTE_APP,
		Encrypted: true,
		From:      toNode,
		To:        fromNode,
		RequestId: message.RequestId,
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
				s := append(*snrList, -128) // int8 min value
				snrList = &s
				snrCount += 1
			}
		}
	}
}

func (c *MeshtasticClient) appendMyIdAndSnr(packet *pb.MeshPacket, disco *pb.RouteDiscovery, isTowardsDestination bool, snrOnly bool) {
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
		s := append(*snrList, 40) // A random value
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
