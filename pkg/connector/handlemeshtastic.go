package connector

import (
	"context"
	"time"

	"github.com/kabili207/matrix-meshtastic/pkg/mesh"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"github.com/rs/zerolog"
	"go.mau.fi/util/jsontime"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
)

// Handles events that are more general to the connector
func (c *MeshtasticConnector) handleGlobalMeshEvent(rawEvt any) {

	switch evt := rawEvt.(type) {
	case *mesh.MeshNodeInfoEvent:
		c.handleMeshNodeInfo(evt)
	case *mesh.MeshLocationEvent:
		c.handleMeshLocation(evt)
	case *mesh.MeshWaypointEvent:
		c.handleMeshWaypoint(evt)
	case *mesh.MeshEnvelope:
		c.handleUnknownPacket(evt)
	}
}

// Handles events that are more user-specific
func (c *MeshtasticClient) handleMeshEvent(rawEvt any) {

	switch evt := rawEvt.(type) {
	case *mesh.MeshMessageEvent:
		c.handleMeshMessage(evt)
	case *mesh.MeshChannelJoined:
		c.handleMeshChannelJoined(evt)
	case *mesh.MeshReactionEvent:
		c.handleMeshReaction(evt)
	}
}

func (c *MeshtasticConnector) handleUnknownPacket(evt *mesh.MeshEnvelope) {
	ghost, _ := c.getRemoteGhost(context.Background(), meshid.MakeUserID(evt.From), true)
	userInfo := &bridgev2.UserInfo{
		ExtraUpdates: bridgev2.MergeExtraUpdaters(updateGhostLastSeenAt),
	}
	ghost.UpdateInfo(context.Background(), userInfo)
}

func (c *MeshtasticConnector) handleMeshLocation(evt *mesh.MeshLocationEvent) {
	log := c.log.With().
		Str("action", "location_update").
		Stringer("node_id", evt.Envelope.From).
		Logger()
	log.Info().
		Float32("latitude", evt.Latitude).
		Float32("longitude", evt.Latitude).
		Msg("Location update received")

	ghost, _ := c.getRemoteGhost(context.Background(), meshid.MakeUserID(evt.Envelope.From), true)
	userInfo := &bridgev2.UserInfo{
		ExtraUpdates: bridgev2.MergeExtraUpdaters(updateGhostLastSeenAt),
	}
	ghost.UpdateInfo(context.Background(), userInfo)
}

func (c *MeshtasticClient) handleMeshChannelJoined(evt *mesh.MeshChannelJoined) {

	ctx := context.Background()
	login := c.UserLogin
	user := login.User
	log := c.log.With().Str("user_mxid", string(user.MXID)).Str("login_id", string(login.ID)).Logger()
	ctx = log.WithContext(ctx)

	portalKey := c.makePortalKey(evt.ChannelID, evt.ChannelKey)

	portal, err := c.bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		log.Err(err).Str("portal_key", string(portalKey.ID)).Msg("Failed to get portal")
		return
	}

	//meta := login.Metadata.(*UserLoginMetadata)

	log.Info().Str("portal_id", string(portal.ID)).Msg("Successfully retrieved portal")

	chatInfo := c.wrapChatInfo(user, evt.ChannelID, *evt.ChannelKey)
	// Create the room using portal
	err = portal.CreateMatrixRoom(ctx, user.GetDefaultLogin(), chatInfo)
	if err != nil {
		log.Err(err).Msg("Failed to create matrix room")
		return
	}
	log.Info().Msg("Successfully created matrix room")

}

func (c *MeshtasticClient) handleMeshMessage(evt *mesh.MeshMessageEvent) {
	meta, ok := c.UserLogin.Metadata.(*UserLoginMetadata)
	if evt.IsDM && (!ok || meta.NodeID != evt.Envelope.To) {
		return
	}

	ghost, _ := c.main.getRemoteGhost(context.Background(), meshid.MakeUserID(evt.Envelope.From), true)

	var portalKey networkid.PortalKey
	var messIDSender = ""

	roomType := database.RoomTypeDefault
	if evt.IsDM {
		portalKey = c.makeDMPortalKey(evt.Envelope.From, evt.Envelope.To)
		messIDSender = evt.Envelope.From.String()
		roomType = database.RoomTypeDM
	} else {
		portalKey = c.makePortalKey(evt.Envelope.ChannelID, evt.Envelope.ChannelKey)
		messIDSender = evt.Envelope.ChannelID
	}

	mess := simplevent.Message[*mesh.MeshMessageEvent]{
		EventMeta: simplevent.EventMeta{
			Type: bridgev2.RemoteEventMessage,
			LogContext: func(c zerolog.Context) zerolog.Context {
				c = c.Stringer("sender_id", evt.Envelope.From)
				c = c.Uint32("message_ts", uint32(evt.Envelope.Timestamp))
				return c
			},
			PortalKey:    portalKey,
			CreatePortal: true,
			Sender:       c.makeEventSender(evt.Envelope.From),
			Timestamp:    time.Unix(int64(evt.Envelope.Timestamp), 0),
			PreHandleFunc: func(ctx context.Context, p *bridgev2.Portal) {
				p.RoomType = roomType
			},
		},
		Data:               evt,
		ID:                 meshid.MakeMessageID(messIDSender, evt.Envelope.PacketId),
		ConvertMessageFunc: convertMessageEvent,
	}

	c.bridge.QueueRemoteEvent(c.UserLogin, &mess)
	userInfo := &bridgev2.UserInfo{
		ExtraUpdates: bridgev2.MergeExtraUpdaters(updateGhostLastSeenAt),
	}
	ghost.UpdateInfo(context.Background(), userInfo)
}

func convertMessageEvent(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, data *mesh.MeshMessageEvent) (*bridgev2.ConvertedMessage, error) {
	content := &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    data.Message,
	}
	return &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			Type:    event.EventMessage,
			Content: content,
		}},
	}, nil
}

func (c *MeshtasticConnector) getRemoteGhost(ctx context.Context, ghostID networkid.UserID, requestInfoIfNew bool) (*bridgev2.Ghost, error) {
	if !requestInfoIfNew {
		return c.bridge.GetGhostByID(ctx, ghostID)
	}
	ghost, err := c.bridge.GetExistingGhostByID(ctx, ghostID)
	if ghost != nil || err != nil {
		return ghost, err
	}
	go c.requestGhostNodeInfo(ghostID)
	return c.bridge.GetGhostByID(ctx, ghostID)
}

func (c *MeshtasticConnector) requestGhostNodeInfo(ghostID networkid.UserID) {
	log := c.log.With().
		Str("action", "request_nodeinfo").
		Str("ghost_id", string(ghostID)).
		Logger()
	nodeId, err := meshid.ParseUserID(ghostID)
	if err != nil {
		log.Err(err).
			Msg("unable to request node info")
		return
	}

	err = c.meshClient.SendNodeInfo(c.GetBaseNodeID(), nodeId, c.Config.LongName, c.Config.ShortName, true, nil)
	if err != nil {
		log.Err(err).
			Msg("unable to request node info")
		return
	}
	log.Debug().Msg("Sent request for node info")
}

func (c *MeshtasticConnector) handleMeshNodeInfo(evt *mesh.MeshNodeInfoEvent) {
	log := c.log.With().
		Str("action", "handle_mesh_nodeinfo").
		Stringer("from_node_id", evt.Envelope.From).
		Stringer("to_node_id", evt.Envelope.To).
		Logger()
	ctx := log.WithContext(context.Background())
	ghost, err := c.getRemoteGhost(ctx, meshid.MakeUserID(evt.Envelope.From), evt.Envelope.To != c.GetBaseNodeID())
	if err != nil {
		log.Err(err).Msg("Failed to get ghost")
		return
	}

	//c.sendMeshPresense(&evt.Envelope)

	if evt.Envelope.To != meshid.BROADCAST_ID {
		log.Debug().Msg("Broadcasting node info")
		c.sendNodeInfo(evt.Envelope.To, meshid.BROADCAST_ID, false)
	}

	if evt.LongName == "" {
		return
	}

	userInfo := &bridgev2.UserInfo{
		Name:         &evt.LongName,
		IsBot:        ptr.Ptr(false),
		Identifiers:  []string{},
		ExtraUpdates: bridgev2.MergeExtraUpdaters(c.updateGhostNames(evt.LongName, evt.ShortName), c.updateGhostPublicKey(evt.PublicKey), updateGhostLastSeenAt),
	}
	ghost.UpdateInfo(ctx, userInfo)
	log.Debug().Msg("Synced ghost info")

}

func (c *MeshtasticConnector) sendNodeInfo(fromNode, toNode meshid.NodeID, wantReply bool) {
	log := c.log.With().
		Str("action", "send_nodeinfo").
		Stringer("from_node_id", fromNode).
		Stringer("to_node_id", toNode).
		Logger()
	if !c.IsManagedNode(fromNode) {
		// We have no authority over this node
		log.Debug().Msg("Ignoring node info request. Not our node")
		return
	}

	ctx := log.WithContext(context.Background())
	if fromNode == c.GetBaseNodeID() {
		log.Debug().Msg("Sending from base node")
		err := c.meshClient.SendNodeInfo(fromNode, toNode, c.Config.LongName, c.Config.ShortName, wantReply, nil)
		if err != nil {
			log.Err(err).Msg("Failed to send node info")
		}
		return
	}

	notifyUser := false
	ghost, err := c.bridge.GetGhostByID(ctx, meshid.MakeUserID(fromNode))
	if err != nil {
		log.Err(err).Msg("Failed to get ghost")
	} else {
		if meta, ok := ghost.Metadata.(*GhostMetadata); ok && meta.LongName != "" && meta.ShortName != "" {
			log.Debug().Msg("Sending user configured node info")
			err = c.meshClient.SendNodeInfo(fromNode, toNode, meta.LongName, meta.ShortName, wantReply, meta.PublicKey)
		} else {
			log.Debug().Msg("Sending generated node info")
			senderStr := fromNode.String()
			shortName := senderStr[len(senderStr)-4:]
			err = c.meshClient.SendNodeInfo(fromNode, toNode, senderStr, shortName, wantReply, nil)
			notifyUser = true
		}
		if err != nil {
			log.Err(err).Msg("Failed to send node info")
			return
		}
	}
	if notifyUser {
		log.Debug().Msg("Notifying user...")
		// TODO: Send a notice in the portal informing the user how to set their mesh names
	}
}

func updateGhostLastSeenAt(_ context.Context, ghost *bridgev2.Ghost) bool {
	meta := ghost.Metadata.(*GhostMetadata)
	meta.LastSeen = ptr.Ptr(jsontime.UnixNow())
	return true
}

func (c *MeshtasticClient) handleMeshReaction(evt *mesh.MeshReactionEvent) {
	meta, ok := c.UserLogin.Metadata.(*UserLoginMetadata)
	if evt.IsDM && (!ok || meta.NodeID != evt.Envelope.To) {
		return
	}

	ghost, _ := c.main.getRemoteGhost(context.Background(), meshid.MakeUserID(evt.Envelope.From), true)

	var portalKey networkid.PortalKey
	var messIDSender = ""

	if evt.IsDM {
		portalKey = c.makeDMPortalKey(evt.Envelope.From, evt.Envelope.To)
		messIDSender = evt.Envelope.From.String()
	} else {
		portalKey = c.makePortalKey(evt.Envelope.ChannelID, evt.Envelope.ChannelKey)
		messIDSender = evt.Envelope.ChannelID
	}

	mess := simplevent.Reaction{
		EventMeta: simplevent.EventMeta{
			Type: bridgev2.RemoteEventReaction,
			LogContext: func(c zerolog.Context) zerolog.Context {
				c = c.Stringer("sender_id", evt.Envelope.From)
				c = c.Uint32("message_ts", uint32(evt.Envelope.Timestamp))
				return c
			},
			PortalKey:    portalKey,
			CreatePortal: false,
			Sender:       c.makeEventSender(evt.Envelope.From),
			Timestamp:    time.Unix(int64(evt.Envelope.Timestamp), 0),
		},
		EmojiID:       networkid.EmojiID(evt.Emoji),
		Emoji:         evt.Emoji,
		TargetMessage: meshid.MakeMessageID(messIDSender, evt.ReplyId),
	}

	c.bridge.QueueRemoteEvent(c.UserLogin, &mess)
	userInfo := &bridgev2.UserInfo{
		ExtraUpdates: bridgev2.MergeExtraUpdaters(updateGhostLastSeenAt),
	}
	ghost.UpdateInfo(context.Background(), userInfo)
}

func (c *MeshtasticConnector) handleMeshWaypoint(evt *mesh.MeshWaypointEvent) {
	log := c.log.With().
		Str("action", "waypoint_update").
		Stringer("node_id", evt.Envelope.From).
		Logger()
	log.Info().
		Float32("latitude", evt.Latitude).
		Float32("longitude", evt.Latitude).
		Str("name", evt.Name).
		Str("description", evt.Description).
		Str("icon", evt.Icon).
		Msg("Waypoint received")

	ghost, _ := c.getRemoteGhost(context.Background(), meshid.MakeUserID(evt.Envelope.From), true)
	userInfo := &bridgev2.UserInfo{
		ExtraUpdates: bridgev2.MergeExtraUpdaters(updateGhostLastSeenAt),
	}
	ghost.UpdateInfo(context.Background(), userInfo)
	// TODO: Save these somewhere and allow other users to retrieve them later?
	// Possibly drop an m.location event in the default channel?
}
