package mesh

import (
	"errors"
	"fmt"

	"github.com/jellydator/ttlcache/v3"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
	"github.com/meshnet-gophers/meshtastic-go/mqtt"
	"github.com/meshnet-gophers/meshtastic-go/radio"
	"go.mau.fi/util/ptr"
	"google.golang.org/protobuf/proto"
)

func (c *MeshtasticClient) handleMQTTMessage(m mqtt.Message) {
	log := c.log.With().Logger()
	var env pb.ServiceEnvelope
	err := proto.Unmarshal(m.Payload, &env)
	if err != nil {
		log.Err(err).Msg("failed unmarshalling to service envelope")
		return
	}
	packet := env.GetPacket()

	log = log.With().
		Str("channel", env.ChannelId).
		Stringer("from", meshid.NodeID(packet.From)).
		Stringer("to", meshid.NodeID(packet.To)).
		Logger()

	cacheKey := (uint64(packet.From) << 32) | uint64(packet.Id)
	if packet.Id != 0 && c.packetCache.Has(cacheKey) {
		log.Debug().Msg("Ignoring duplicate packet")
		return
	}
	c.packetCache.Set(cacheKey, nil, ttlcache.DefaultTTL)

	if c.managedNodeFunc(meshid.NodeID(packet.From)) {
		return
	}

	key := c.channelKeys[env.ChannelId]

	if packet.PkiEncrypted || (env.ChannelId == "PKI") {

		if !c.managedNodeFunc(meshid.NodeID(packet.To)) {
			log.Debug().Msg("Ignoring PKI packet to unmanaged node")
			return
		}

		pubKey, err := c.requestKey(meshid.NodeID(packet.From), c.pubKeyRequestHandler)
		if err != nil {
			log.Err(err).Msg("Error getting public key for sending node")
			return
		}
		packet.PublicKey = pubKey
		packet.PkiEncrypted = true

		key, err = c.requestKey(meshid.NodeID(packet.To), c.privKeyRequestHandler)
		if err != nil {
			log.Err(err).Msg("Error getting private key for receiving node")
			return
		}
	}

	message, err := radio.TryDecode(packet, key)
	if err != nil {
		log.Err(err).Msg("failed to decrypt the message packet")
	}

	_ = c.processMessage(&env, message)
}

func (c *MeshtasticClient) requestKey(nodeID meshid.NodeID, handler KeyRequestFunc) ([]byte, error) {
	if handler == nil {
		return nil, errors.New("no handler for key request")
	}
	keyString := handler(nodeID)
	if keyString == nil || *keyString == "" {
		return nil, errors.New("no key provided for node")
	}
	return radio.ParseKey(*keyString)
}

func (c *MeshtasticClient) processMessage(envelope *pb.ServiceEnvelope, message *pb.Data) error {
	if message == nil {
		return fmt.Errorf("nil message")
	}

	if c.managedNodeFunc(meshid.NodeID(envelope.Packet.From)) {
		return nil
	}

	chanKey := c.channelKeyStrings[envelope.ChannelId]
	meshEventEnv := MeshEvent{
		PacketId:    envelope.Packet.Id,
		To:          meshid.NodeID(envelope.Packet.To),
		From:        meshid.NodeID(envelope.Packet.From),
		ChannelName: envelope.ChannelId,
		ChannelKey:  &chanKey,
		Timestamp:   envelope.Packet.RxTime,
		WantAck:     envelope.Packet.WantAck,
	}
	var err error
	var evt any = meshEventEnv

	switch message.Portnum {
	case pb.PortNum_TEXT_MESSAGE_APP:
		if message.Emoji == 1 {
			evt = &MeshReactionEvent{
				MeshEvent: meshEventEnv,
				Emoji:     string(message.Payload),
				IsDM:      envelope.Packet.To != uint32(meshid.BROADCAST_ID),
				ReplyId:   message.ReplyId,
			}
		} else {
			evt = &MeshMessageEvent{
				MeshEvent: meshEventEnv,
				Message:   string(message.Payload),
				IsDM:      envelope.Packet.To != uint32(meshid.BROADCAST_ID),
			}
		}
	case pb.PortNum_NODEINFO_APP:
		var user = pb.User{}
		proto.Unmarshal(message.Payload, &user)
		evt = &MeshNodeInfoEvent{
			MeshEvent: meshEventEnv,
			LongName:  user.LongName,
			ShortName: user.ShortName,
			PublicKey: user.PublicKey,
		}

	case pb.PortNum_POSITION_APP:
		var pos = pb.Position{}
		proto.Unmarshal(message.Payload, &pos)
		if pos.LatitudeI != nil && pos.LongitudeI != nil {
			alt := (*float32)(nil)
			if pos.Altitude != nil {
				alt = ptr.Ptr(float32(*pos.Altitude))
			}
			evt = &MeshLocationEvent{
				MeshEvent: meshEventEnv,
				Location: meshid.GeoURI{
					Latitude:    float32(*pos.LatitudeI) * 1e-7,
					Longitude:   float32(*pos.LongitudeI) * 1e-7,
					Altitude:    alt,
					Uncertainty: ptr.Ptr(float32(GetPositionPrecisionInMeters(pos.PrecisionBits))),
				},
				GroundSpeed: pos.GroundSpeed,
				GroundTrack: pos.GroundTrack,
			}
		}

	case pb.PortNum_TRACEROUTE_APP:
		var r = pb.RouteDiscovery{}
		err = proto.Unmarshal(message.Payload, &r)
		c.printPacketDetails(envelope, &r)
		c.handleTraceroute(envelope, message, &r)

	case pb.PortNum_TELEMETRY_APP:
		var t = pb.Telemetry{}
		err = proto.Unmarshal(message.Payload, &t)
		c.printPacketDetails(envelope, &t)

	case pb.PortNum_NEIGHBORINFO_APP:
		var n = pb.NeighborInfo{}
		err = proto.Unmarshal(message.Payload, &n)
		c.printPacketDetails(envelope, &n)

	case pb.PortNum_STORE_FORWARD_APP:
		var s = pb.StoreAndForward{}
		err = proto.Unmarshal(message.Payload, &s)

	case pb.PortNum_ROUTING_APP:
		var r = pb.Routing{}
		err = proto.Unmarshal(message.Payload, &r)
		c.printPacketDetails(envelope, &r)

	case pb.PortNum_WAYPOINT_APP:
		var w = pb.Waypoint{}
		err = proto.Unmarshal(message.Payload, &w)
		if w.LatitudeI != nil && w.LongitudeI != nil {
			lat := float32(*w.LatitudeI) * 1e-7
			lon := float32(*w.LongitudeI) * 1e-7
			evt = &MeshWaypointEvent{
				MeshEvent:   meshEventEnv,
				Latitude:    lat,
				Longitude:   lon,
				Name:        w.Name,
				Description: w.Description,
				Icon:        string(rune(w.Icon)),
			}
		}
	}

	c.notifyEvent(evt)
	return err
}
