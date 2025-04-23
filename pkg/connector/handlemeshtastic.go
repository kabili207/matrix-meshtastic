package connector

import (
	"context"
	"fmt"
	"time"

	"github.com/kabili207/matrix-meshtastic/pkg/mesh"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
)

func (c *MeshtasticClient) handleMeshEvent(rawEvt any) {

	switch evt := rawEvt.(type) {
	case *mesh.MeshMessageEvent:
		c.handleMeshMessage(evt)
	case *mesh.MeshNodeInfoEvent:
		c.handleMeshNodeInfo(evt)
	case *mesh.MeshChannelJoined:
		c.handleMeshChannelJoined(evt)
	case *mesh.MeshReactionEvent:
		c.handleMeshReaction(evt)
	case *mesh.MeshLocationEvent:
		c.handleMeshLocation(evt)
	case *mesh.MeshWaypointEvent:
		c.handleMeshWaypoint(evt)
		//case *mesh.MeshEnvelope:
		//	c.sendMeshPresense(evt)
	}
}

func MakeMessageID(sender string, packetId uint32) networkid.MessageID {
	return networkid.MessageID(fmt.Sprintf("%s|%d", sender, packetId))
}

//unc (c *MeshtasticClient) sendMeshPresense(evt *mesh.MeshEnvelope) {
//	c.bridge.QueueRemoteEvent(c.UserLogin, &simplevent.Typing{
//		EventMeta: simplevent.EventMeta{
//			Type:       bridgev2.RemoteEventTyping,
//			LogContext: nil,
//			PortalKey:  c.makePortalKey(evt.ChannelID, evt.ChannelKey),
//			Sender:     c.makeEventSender(evt.From),
//			Timestamp:  time.Unix(int64(evt.Timestamp), 0),
//		},
//		Timeout: 1 * time.Millisecond,
//		Type:    bridgev2.TypingTypeText,
//	})
//

func (c *MeshtasticClient) handleMeshLocation(evt *mesh.MeshLocationEvent) {
	log := c.UserLogin.Log.With().
		Str("action", "location_update").
		Stringer("node_id", evt.Envelope.From).
		Logger()
	log.Info().
		Float32("latitude", evt.Latitude).
		Float32("longitude", evt.Latitude).
		Msg("Location update received")
	c.getRemoteGhost(context.Background(), meshid.MakeUserID(evt.Envelope.From), true)
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
	if evt.IsDM {
		// We don't handle DMs yet
		return
	}

	c.getRemoteGhost(context.Background(), meshid.MakeUserID(evt.Envelope.From), true)

	mess := simplevent.Message[*mesh.MeshMessageEvent]{
		EventMeta: simplevent.EventMeta{
			Type: bridgev2.RemoteEventMessage,
			LogContext: func(c zerolog.Context) zerolog.Context {
				c = c.Stringer("sender_id", evt.Envelope.From)
				c = c.Uint32("message_ts", uint32(evt.Envelope.Timestamp))
				return c
			},
			PortalKey:    c.makePortalKey(evt.Envelope.ChannelID, evt.Envelope.ChannelKey),
			CreatePortal: true,
			Sender:       c.makeEventSender(evt.Envelope.From),
			Timestamp:    time.Unix(int64(evt.Envelope.Timestamp), 0),
		},
		Data:               evt,
		ID:                 MakeMessageID(evt.Envelope.ChannelID, evt.Envelope.PacketId),
		ConvertMessageFunc: convertMessageEvent,
	}

	c.bridge.QueueRemoteEvent(c.UserLogin, &mess)
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

func (c *MeshtasticClient) getRemoteGhost(ctx context.Context, ghostID networkid.UserID, requestInfoIfNew bool) (*bridgev2.Ghost, error) {
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

func (c *MeshtasticClient) requestGhostNodeInfo(ghostID networkid.UserID) {
	log := c.log.With().
		Str("action", "requeset_nodeinfo").
		Str("ghost_id", string(ghostID)).
		Logger()
	nodeId, err := meshid.ParseUserID(ghostID)
	if err != nil {
		log.Err(err).
			Msg("unable to request node info")
		return
	}
	// TODO: call c.sendNodeInfo instead for consistency?
	err = c.MeshClient.SendNodeInfo(c.main.GetBaseNodeID(), nodeId, c.main.Config.LongName, c.main.Config.ShortName, true)
	if err != nil {
		log.Err(err).
			Msg("unable to request node info")
		return
	}
	log.Debug().Msg("Sent request for node info")
}

func (c *MeshtasticClient) handleMeshNodeInfo(evt *mesh.MeshNodeInfoEvent) {
	log := c.UserLogin.Log.With().
		Str("action", "handle_mesh_nodeinfo").
		Stringer("from_node_id", evt.Envelope.From).
		Stringer("to_node_id", evt.Envelope.To).
		Logger()
	ctx := log.WithContext(context.Background())
	ghost, err := c.getRemoteGhost(ctx, meshid.MakeUserID(evt.Envelope.From), evt.Envelope.To != c.main.GetBaseNodeID())
	if err != nil {
		log.Err(err).Msg("Failed to get ghost")
		return
	}

	//c.sendMeshPresense(&evt.Envelope)

	if evt.Envelope.To != mesh.BROADCAST_ID {
		log.Debug().Msg("Broadcasting node info")
		c.sendNodeInfo(evt.Envelope.To, mesh.BROADCAST_ID, false)
	}

	if evt.LongName == "" {
		return
	}

	userInfo := &bridgev2.UserInfo{
		Name:         &evt.LongName,
		IsBot:        ptr.Ptr(false),
		Identifiers:  []string{},
		ExtraUpdates: bridgev2.MergeExtraUpdaters(c.updateGhostNames(evt.LongName, evt.ShortName), c.updateGhostPublicKey(evt.PublicKey)),
	}
	ghost.UpdateInfo(ctx, userInfo)
	log.Debug().Msg("Synced ghost info")

}

func (c *MeshtasticClient) sendNodeInfo(fromNode, toNode meshid.NodeID, wantReply bool) {
	log := c.UserLogin.Log.With().
		Str("action", "send_nodeinfo").
		Stringer("from_node_id", fromNode).
		Stringer("to_node_id", toNode).
		Logger()
	if !c.main.IsManagedNode(fromNode) {
		// We have no authority over this node
		log.Debug().Msg("Ignoring node info request. Not our node")
		return
	}

	ctx := log.WithContext(context.Background())
	if fromNode == c.main.GetBaseNodeID() {
		log.Debug().Msg("Sending from base node")
		err := c.MeshClient.SendNodeInfo(fromNode, toNode, c.main.Config.LongName, c.main.Config.ShortName, wantReply)
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
			err = c.MeshClient.SendNodeInfo(fromNode, toNode, meta.LongName, meta.ShortName, wantReply)
		} else {
			log.Debug().Msg("Sending generated node info")
			senderStr := fromNode.String()
			shortName := senderStr[len(senderStr)-4:]
			err = c.MeshClient.SendNodeInfo(fromNode, toNode, senderStr, shortName, wantReply)
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

//func updateGhostLastSyncAt(_ context.Context, ghost *bridgev2.Ghost) bool {
//	meta := ghost.Metadata.(*waid.GhostMetadata)
//	forceSave := time.Since(meta.LastSync.Time) > 24*time.Hour
//	meta.LastSync = jsontime.UnixNow()
//	return forceSave
//}

func (c *MeshtasticClient) handleMeshReaction(evt *mesh.MeshReactionEvent) {
	if evt.IsDM {
		// We don't handle DMs yet
		c.MeshClient.SendNack(evt.Envelope.To, evt.Envelope.From, evt.Envelope.PacketId)
		return
	}

	c.getRemoteGhost(context.Background(), meshid.MakeUserID(evt.Envelope.From), true)

	mess := simplevent.Reaction{
		EventMeta: simplevent.EventMeta{
			Type: bridgev2.RemoteEventReaction,
			LogContext: func(c zerolog.Context) zerolog.Context {
				c = c.Stringer("sender_id", evt.Envelope.From)
				c = c.Uint32("message_ts", uint32(evt.Envelope.Timestamp))
				return c
			},
			PortalKey:    c.makePortalKey(evt.Envelope.ChannelID, evt.Envelope.ChannelKey),
			CreatePortal: false,
			Sender:       c.makeEventSender(evt.Envelope.From),
			Timestamp:    time.Unix(int64(evt.Envelope.Timestamp), 0),
		},
		EmojiID:       networkid.EmojiID(evt.Emoji),
		Emoji:         evt.Emoji,
		TargetMessage: MakeMessageID(evt.Envelope.ChannelID, evt.ReplyId),
	}

	c.bridge.QueueRemoteEvent(c.UserLogin, &mess)
}

func (c *MeshtasticClient) handleMeshWaypoint(evt *mesh.MeshWaypointEvent) {
	log := c.UserLogin.Log.With().
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
	c.getRemoteGhost(context.Background(), meshid.MakeUserID(evt.Envelope.From), true)

	// TODO: Save these somewhere and allow other users to retrieve them later?
	// Possibly drop an m.location event in the default channel?
}
