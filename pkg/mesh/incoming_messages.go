package mesh

import (
	"errors"
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/kabili207/matrix-meshtastic/pkg/mesh/connectors"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
	"github.com/meshnet-gophers/meshtastic-go/radio"
	"go.mau.fi/util/ptr"
	"google.golang.org/protobuf/proto"
)

func (c *MeshtasticClient) getChannelNameFromHash(idHash uint32) string {
	for _, v := range c.channels {
		if hash, err := radio.ChannelHash(v.GetName(), v.GetKeyBytes()); err == nil && hash == idHash {
			return v.GetName()
		}
	}
	return ""
}

func (c *MeshtasticClient) handleMeshPacket(packet connectors.NetworkMeshPacket) {
	gateway := packet.GatewayNode
	if gateway == 0 {
		// This will only be the last byte of the relay node
		if packet.HopStart == packet.HopLimit && packet.RelayNode == (packet.From&0xFF) {
			gateway = meshid.NodeID(packet.From)
		} else {
			gateway = meshid.NodeID(packet.RelayNode)
		}
	}
	log := c.log.With().
		Uint32("packet_id", packet.Id).
		Stringer("from", meshid.NodeID(packet.From)).
		Stringer("to", meshid.NodeID(packet.To)).
		Str("source", string(packet.Source)).
		Stringer("via", gateway).
		Logger()

	c.packetCacheLock.Lock()
	if c.isDuplicatePacket(packet) {
		c.packetCacheLock.Unlock()
		log.Debug().Msg("Ignoring duplicate packet")
		return
	}
	c.cachePacket(packet)
	c.packetCacheLock.Unlock()

	if c.managedNodeFunc(meshid.NodeID(packet.From)) {
		return
	}

	var err error

	data := packet.GetDecoded()

	if data == nil {
		if c.shouldUsePKIDecryption(packet) {
			data, err = c.tryDecryptPKI(&packet)
			if err != nil {
				log.Warn().AnErr("error", err).Msg("failed to decrypt as PKI packet")
			}
		}

		if data == nil {
			data, err = c.tryDecryptPSK(&packet)
			if err != nil {
				log.Err(err).Msg("failed to decrypt the message packet")
				return
			}
		}
	}

	_ = c.processMessage(packet, data)
}

func (c *MeshtasticClient) isDuplicatePacket(packet connectors.NetworkMeshPacket) bool {
	if packet.Id == 0 {
		return false
	}
	cacheKey := (uint64(packet.From) << 32) | uint64(packet.Id)
	return c.packetCache.Has(cacheKey)
}

func (c *MeshtasticClient) cachePacket(packet connectors.NetworkMeshPacket) {
	cacheKey := (uint64(packet.From) << 32) | uint64(packet.Id)
	c.packetCache.Set(cacheKey, nil, ttlcache.DefaultTTL)
}

func (c *MeshtasticClient) shouldUsePKIDecryption(packet connectors.NetworkMeshPacket) bool {
	return packet.Channel == 0 && packet.To > 0 &&
		meshid.NodeID(packet.To) != meshid.BROADCAST_ID &&
		c.managedNodeFunc(meshid.NodeID(packet.To))
}

func (c *MeshtasticClient) tryDecryptPKI(packet *connectors.NetworkMeshPacket) (*pb.Data, error) {
	toNode := meshid.NodeID(packet.To)
	fromNode := meshid.NodeID(packet.From)

	privKey, err := c.requestKey(toNode, c.privKeyRequestHandler)
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve private key for %s: %w", toNode, err)
	}

	pubKey, err := c.requestKey(fromNode, c.pubKeyRequestHandler)
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve public key for %s: %w", fromNode, err)
	}

	packet.ChannelName = "PKI"
	return radio.TryDecodePKI(packet.MeshPacket, pubKey, privKey)
}

func (c *MeshtasticClient) tryDecryptPSK(packet *connectors.NetworkMeshPacket) (*pb.Data, error) {
	packet.ChannelName = c.getChannelNameFromHash(packet.Channel)
	if packet.ChannelName == "" {
		return nil, fmt.Errorf("unknown channel hash: %d", packet.Channel)
	}
	data, err := (*pb.Data)(nil), (error)(nil)
	for _, v := range c.channels {
		if v.GetName() == packet.ChannelName && v.GetKeyString() != "" {
			data, err = radio.TryDecode(packet.MeshPacket, v.GetKeyBytes())
			if err == nil && data != nil {
				packet.ChannelKey = ptr.Ptr(v.GetKeyString())
				return data, err
			}
		}
	}
	return data, err
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

// isUnmessagable indicates if a particular node is either flagged or has a role that is unable to receive messages
func isUnmessagable(user *pb.User) bool {
	if user.IsUnmessagable != nil {
		return *user.IsUnmessagable
	}
	return isUnmessagableRole(user.Role)
}

func isUnmessagableRole(role pb.Config_DeviceConfig_Role) bool {
	// https://github.com/meshtastic/Meshtastic-Android/blob/6408b22c6d946098ce9d80efb918876c8b7122cf/app/src/main/java/com/geeksville/mesh/model/Node.kt#L148
	return role == pb.Config_DeviceConfig_REPEATER ||
		role == pb.Config_DeviceConfig_ROUTER ||
		role == pb.Config_DeviceConfig_ROUTER_LATE ||
		role == pb.Config_DeviceConfig_SENSOR ||
		role == pb.Config_DeviceConfig_TRACKER ||
		role == pb.Config_DeviceConfig_TAK ||
		role == pb.Config_DeviceConfig_TAK_TRACKER
}

func (c *MeshtasticClient) processMessage(packet connectors.NetworkMeshPacket, message *pb.Data) error {
	if message == nil {
		return fmt.Errorf("nil message")
	}

	if packet.From == 0 {
		return fmt.Errorf("sender has zero node ID")
	}

	if c.managedNodeFunc(meshid.NodeID(packet.From)) {
		return nil
	}

	meshEventEnv := MeshEvent{
		PacketId:     packet.Id,
		To:           meshid.NodeID(packet.To),
		From:         meshid.NodeID(packet.From),
		Via:          packet.GatewayNode,
		ChannelName:  packet.ChannelName,
		ChannelKey:   packet.ChannelKey,
		Timestamp:    packet.RxTime,
		WantAck:      packet.WantAck,
		WantResponse: message.WantResponse,
		IsNeighbor: packet.Source != connectors.PacketSourceMQTT &&
			packet.HopStart == packet.HopLimit && !packet.ViaMqtt,
	}

	if meshEventEnv.Timestamp == 0 {
		meshEventEnv.Timestamp = uint32(time.Now().Unix())
	}

	var err error
	var evt any = meshEventEnv

	switch message.Portnum {
	case pb.PortNum_TEXT_MESSAGE_APP:
		if message.Emoji == 1 {
			evt = &MeshReactionEvent{
				MeshEvent: meshEventEnv,
				Emoji:     string(message.Payload),
				IsDM:      packet.To != uint32(meshid.BROADCAST_ID),
				ReplyId:   message.ReplyId,
			}
		} else {
			evt = &MeshMessageEvent{
				MeshEvent: meshEventEnv,
				Message:   string(message.Payload),
				IsDM:      packet.To != uint32(meshid.BROADCAST_ID),
				ReplyId:   message.ReplyId,
			}
		}
	case pb.PortNum_NODEINFO_APP:
		var user = pb.User{}
		proto.Unmarshal(message.Payload, &user)
		evt = &MeshNodeInfoEvent{
			MeshEvent:      meshEventEnv,
			UserID:         user.Id,
			LongName:       user.LongName,
			ShortName:      user.ShortName,
			Role:           user.Role.String(),
			PublicKey:      user.PublicKey,
			IsLicensed:     user.IsLicensed,
			IsUnmessagable: isUnmessagable(&user),
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

	case pb.PortNum_MAP_REPORT_APP:
		var pos = pb.MapReport{}
		proto.Unmarshal(message.Payload, &pos)
		alt := (*float32)(nil)
		if pos.Altitude != 0 {
			alt = ptr.Ptr(float32(pos.Altitude))
		}
		evt = &MeshMapReportEvent{
			MeshEvent:          meshEventEnv,
			LongName:           pos.LongName,
			ShortName:          pos.ShortName,
			Role:               pos.Role.String(),
			FirmwareVersion:    pos.FirmwareVersion,
			OnlineLocalNodes:   pos.NumOnlineLocalNodes,
			IsUnmessagableRole: isUnmessagableRole(pos.Role),
			Location: meshid.GeoURI{
				Latitude:    float32(pos.LatitudeI) * 1e-7,
				Longitude:   float32(pos.LongitudeI) * 1e-7,
				Altitude:    alt,
				Uncertainty: ptr.Ptr(float32(GetPositionPrecisionInMeters(pos.PositionPrecision))),
			},
		}

	case pb.PortNum_TRACEROUTE_APP:
		var r = pb.RouteDiscovery{}
		err = proto.Unmarshal(message.Payload, &r)
		c.printPacketDetails(packet, &r)

		// Check if this is a response (has RequestId) or a request (no RequestId)
		if message.RequestId != 0 {
			// This is a traceroute response - process the return route before creating event
			c.handleTracerouteResponse(packet, &r)

			evt = &MeshTracerouteEvent{
				MeshEvent:  meshEventEnv,
				Route:      r.Route,
				SnrTowards: r.SnrTowards,
				RouteBack:  r.RouteBack,
				SnrBack:    r.SnrBack,
				RequestId:  message.RequestId,
			}
		} else {
			// This is an incoming traceroute request - handle it
			c.handleTracerouteRequest(packet, &r)
		}

	case pb.PortNum_TELEMETRY_APP:
		var t = pb.Telemetry{}
		err = proto.Unmarshal(message.Payload, &t)

		// Check if this is a telemetry request (firmware 2.7.15+)
		// Requests have WantResponse set and are sent to a specific node (not broadcast)
		if message.WantResponse && packet.To != uint32(meshid.BROADCAST_ID) {
			c.handleTelemetryRequest(packet, &t)
		}

	case pb.PortNum_NEIGHBORINFO_APP:
		var n = pb.NeighborInfo{}
		err = proto.Unmarshal(message.Payload, &n)

		// Check if this is a neighbor info request (firmware 2.7.15+)
		// Requests have WantResponse set and are sent to a specific node (not broadcast)
		if message.WantResponse && packet.To != uint32(meshid.BROADCAST_ID) {
			c.handleNeighborInfoRequest(packet, &n)
		}

	case pb.PortNum_STORE_FORWARD_APP:
		var s = pb.StoreAndForward{}
		err = proto.Unmarshal(message.Payload, &s)

	case pb.PortNum_STORE_FORWARD_PLUSPLUS_APP:
		var s = pb.StoreAndForward{}
		err = proto.Unmarshal(message.Payload, &s)

	case pb.PortNum_ROUTING_APP:
		var r = pb.Routing{}
		err = proto.Unmarshal(message.Payload, &r)
		c.printPacketDetails(packet, &r)

	case pb.PortNum_WAYPOINT_APP:
		var w = pb.Waypoint{}
		err = proto.Unmarshal(message.Payload, &w)
		if w.LatitudeI != nil && w.LongitudeI != nil {
			lat := float32(*w.LatitudeI) * 1e-7
			lon := float32(*w.LongitudeI) * 1e-7
			expiration := time.Unix(int64(w.Expire), 0)
			var lockedTo *meshid.NodeID
			if w.LockedTo != 0 && w.LockedTo != uint32(meshid.BROADCAST_ID) && w.LockedTo != uint32(meshid.BROADCAST_ID_NO_LORA) {
				lockedTo = ptr.Ptr(meshid.NodeID(w.LockedTo))
			}
			evt = &MeshWaypointEvent{
				MeshEvent:   meshEventEnv,
				IsDelete:    expiration.Before(time.Now()),
				Latitude:    lat,
				Longitude:   lon,
				LockedTo:    lockedTo,
				Name:        w.Name,
				Description: w.Description,
				Icon:        string(rune(w.Icon)),
				Expires:     &expiration,
			}
		}
	}

	c.notifyEvent(evt)
	return err
}
