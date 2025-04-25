package mesh

import (
	"fmt"

	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
	"github.com/meshnet-gophers/meshtastic-go/mqtt"
	"github.com/meshnet-gophers/meshtastic-go/radio"
	"google.golang.org/protobuf/proto"
)

func (c *MeshtasticClient) channelHandler(channel string) mqtt.HandlerFunc {
	return func(m mqtt.Message) {
		log := c.log.With().
			Str("channel", channel).
			Bytes("payload", m.Payload).
			Logger()
		var env pb.ServiceEnvelope
		err := proto.Unmarshal(m.Payload, &env)
		if err != nil {
			log.Err(err).Msg("failed unmarshalling to service envelope")
			return
		}

		if c.managedNodeFunc(meshid.NodeID(env.Packet.From)) {
			return
		}

		key := c.channelKeys[channel]

		message, err := radio.TryDecode(env.GetPacket(), key)
		if err != nil {
			log.Err(err).Msg("failed to decrypt the message packet")
		}

		_ = c.processMessage(&env, message)
		//log.Info(res, "topic", m.Topic, "channel", channel, "portnum", message.Portnum.String())
	}
}

func (c *MeshtasticClient) processMessage(envelope *pb.ServiceEnvelope, message *pb.Data) error {
	if message == nil {
		return fmt.Errorf("nil message")
	}

	if c.managedNodeFunc(meshid.NodeID(envelope.Packet.From)) {
		return nil
	}

	chanKey := c.channelKeyStrings[envelope.ChannelId]
	meshEventEnv := MeshEnvelope{
		PacketId:   envelope.Packet.Id,
		To:         meshid.NodeID(envelope.Packet.To),
		From:       meshid.NodeID(envelope.Packet.From),
		ChannelID:  envelope.ChannelId,
		ChannelKey: &chanKey,
		Timestamp:  envelope.Packet.RxTime,
	}
	var err error
	var evt any = meshEventEnv

	switch message.Portnum {
	case pb.PortNum_TEXT_MESSAGE_APP:
		if message.Emoji == 1 {
			evt = &MeshReactionEvent{
				Envelope: meshEventEnv,
				Emoji:    string(message.Payload),
				IsDM:     envelope.Packet.To != uint32(meshid.BROADCAST_ID),
				ReplyId:  message.ReplyId,
			}
		} else {
			evt = &MeshMessageEvent{
				Envelope: meshEventEnv,
				Message:  string(message.Payload),
				IsDM:     envelope.Packet.To != uint32(meshid.BROADCAST_ID),
			}
		}
	case pb.PortNum_NODEINFO_APP:
		var user = pb.User{}
		proto.Unmarshal(message.Payload, &user)
		evt = &MeshNodeInfoEvent{
			Envelope:  meshEventEnv,
			LongName:  user.LongName,
			ShortName: user.ShortName,
			PublicKey: user.PublicKey,
		}

	case pb.PortNum_POSITION_APP:
		var pos = pb.Position{}
		proto.Unmarshal(message.Payload, &pos)
		if pos.LatitudeI != nil && pos.LongitudeI != nil {
			lat := float32(*pos.LatitudeI) * 1e-7
			lon := float32(*pos.LongitudeI) * 1e-7
			evt = &MeshLocationEvent{
				Envelope:    meshEventEnv,
				Latitude:    lat,
				Longitude:   lon,
				Altitude:    pos.Altitude,
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
				Envelope:    meshEventEnv,
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
