package connector

import (
	"context"
	"fmt"
	"html"
	"strings"
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
	case *mesh.MeshEvent:
		c.handleUnknownPacket(evt)
	}
}

// Handles events that are more user-specific
func (c *MeshtasticClient) handleMeshEvent(rawEvt any) {

	switch evt := rawEvt.(type) {
	case *mesh.MeshMessageEvent:
		c.handleMeshMessage(evt)
	case *mesh.MeshReactionEvent:
		c.handleMeshReaction(evt)
	}
}

func (c *MeshtasticConnector) handleUnknownPacket(evt *mesh.MeshEvent) {
	ghost, _ := c.getRemoteGhost(context.Background(), meshid.MakeUserID(evt.From), true)
	userInfo := &bridgev2.UserInfo{
		ExtraUpdates: bridgev2.MergeExtraUpdaters(updateGhostLastSeenAt(evt.IsNeighbor)),
	}
	ghost.UpdateInfo(context.Background(), userInfo)
}

func (c *MeshtasticConnector) handleMeshLocation(evt *mesh.MeshLocationEvent) {
	log := c.log.With().
		Str("action", "location_update").
		Stringer("node_id", evt.From).
		Logger()
	log.Info().
		Float32("latitude", evt.Location.Latitude).
		Float32("longitude", evt.Location.Latitude).
		Msg("Location update received")

	ghost, _ := c.getRemoteGhost(context.Background(), meshid.MakeUserID(evt.From), true)
	userInfo := &bridgev2.UserInfo{
		ExtraUpdates: bridgev2.MergeExtraUpdaters(updateGhostLastSeenAt(evt.IsNeighbor)),
	}
	ghost.UpdateInfo(context.Background(), userInfo)
}

func (c *MeshtasticClient) joinChannel(channelName string, channelKey string) error {

	ctx := context.Background()
	login := c.UserLogin
	user := login.User
	log := c.log.With().Str("user_mxid", string(user.MXID)).Str("login_id", string(login.ID)).Logger()
	ctx = log.WithContext(ctx)

	portalKey := c.makePortalKey(channelName, &channelKey)

	portal, err := c.bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		log.Err(err).Str("portal_key", string(portalKey.ID)).Msg("Failed to get portal")
		return err
	}

	log.Info().Str("portal_id", string(portal.ID)).Msg("Successfully retrieved portal")

	chatInfo := c.wrapChatInfo(user, channelName, channelKey)
	// Create the room using portal
	return portal.CreateMatrixRoom(ctx, user.GetDefaultLogin(), chatInfo)

}

func (c *MeshtasticClient) handleMeshMessage(evt *mesh.MeshMessageEvent) {
	meta, ok := c.UserLogin.Metadata.(*meshid.UserLoginMetadata)
	if evt.IsDM && (!ok || meta.NodeID != evt.To) {
		return
	}

	ghost, _ := c.main.getRemoteGhost(context.Background(), meshid.MakeUserID(evt.From), true)

	var portalKey networkid.PortalKey
	var messIDSender = ""

	roomType := database.RoomTypeDefault
	if evt.IsDM {
		portalKey = c.makeDMPortalKey(evt.From, evt.To)
		messIDSender = evt.From.String()
		roomType = database.RoomTypeDM
		if evt.WantAck {
			c.MeshClient.SendAck(evt.To, evt.From, evt.PacketId)
		}
	} else {
		portalKey = c.makePortalKey(evt.ChannelName, evt.ChannelKey)
		messIDSender = evt.ChannelName
		if evt.WantAck {
			c.MeshClient.SendAck(c.main.GetBaseNodeID(), evt.From, evt.PacketId)
		}
	}

	mess := simplevent.Message[*mesh.MeshMessageEvent]{
		EventMeta: simplevent.EventMeta{
			Type: bridgev2.RemoteEventMessage,
			LogContext: func(c zerolog.Context) zerolog.Context {
				c = c.Stringer("sender_id", evt.From)
				c = c.Uint32("message_ts", uint32(evt.Timestamp))
				return c
			},
			PortalKey:    portalKey,
			CreatePortal: true,
			Sender:       c.makeEventSender(evt.From),
			Timestamp:    time.Unix(int64(evt.Timestamp), 0),
			PreHandleFunc: func(ctx context.Context, p *bridgev2.Portal) {
				p.RoomType = roomType
			},
		},
		Data:               evt,
		ID:                 meshid.MakeMessageID(messIDSender, evt.PacketId),
		ConvertMessageFunc: c.convertMessageEvent,
	}

	c.bridge.QueueRemoteEvent(c.UserLogin, &mess)
	userInfo := &bridgev2.UserInfo{
		ExtraUpdates: bridgev2.MergeExtraUpdaters(updateGhostLastSeenAt(evt.IsNeighbor)),
	}
	ghost.UpdateInfo(context.Background(), userInfo)
}

func (c *MeshtasticClient) convertMessageEvent(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, data *mesh.MeshMessageEvent) (*bridgev2.ConvertedMessage, error) {
	mess := data.Message
	formatted := ""
	mentions := &event.Mentions{}
	if strings.Contains(mess, mesh.BellCharacter) {
		if portal.RoomType == database.RoomTypeDM {
			user := c.bridge.GetCachedUserLoginByID(portal.Receiver)
			userTag := user.UserMXID.String()

			mess = strings.ReplaceAll(mess, mesh.BellCharacter, userTag)
			formatted = strings.ReplaceAll(data.Message, mesh.BellCharacter, fmt.Sprintf(`<a href="%s">%s</a>`, user.UserMXID.URI().MatrixToURL(), html.EscapeString(userTag)))
			mentions.UserIDs = append(mentions.UserIDs, user.UserMXID)
		} else {
			mess = strings.ReplaceAll(mess, mesh.BellCharacter, portal.MXID.String())
			formatted = strings.ReplaceAll(data.Message, mesh.BellCharacter, fmt.Sprintf(`<a href="%s">%s</a>`, portal.MXID.URI().MatrixToURL(), html.EscapeString("@room")))
			mentions.Room = true
		}
	}
	content := &event.MessageEventContent{
		MsgType:       event.MsgText,
		Body:          mess,
		FormattedBody: formatted,
		Mentions:      mentions,
	}
	if formatted != "" {
		content.Format = event.FormatHTML
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
		Stringer("from_node_id", evt.From).
		Stringer("to_node_id", evt.To).
		Logger()
	ctx := log.WithContext(context.Background())
	ghost, err := c.getRemoteGhost(ctx, meshid.MakeUserID(evt.From), evt.To != c.GetBaseNodeID())
	if err != nil {
		log.Err(err).Msg("Failed to get ghost")
		return
	}

	//c.sendMeshPresense(&evt.Envelope)

	if evt.To != meshid.BROADCAST_ID {
		c.sendNodeInfo(evt.To, meshid.BROADCAST_ID, false)
	}

	if evt.LongName == "" {
		return
	}

	userInfo := &bridgev2.UserInfo{
		Name:         &evt.LongName,
		IsBot:        ptr.Ptr(false),
		Identifiers:  []string{},
		ExtraUpdates: bridgev2.MergeExtraUpdaters(c.updateGhostNames(evt.LongName, evt.ShortName), c.updateGhostPublicKey(evt.PublicKey), updateGhostLastSeenAt(evt.IsNeighbor)),
	}
	ghost.UpdateInfo(ctx, userInfo)
	log.
		Debug().
		Str("long_name", evt.LongName).
		Str("short_name", evt.ShortName).
		Msg("Updated ghost info")
}

func (c *MeshtasticConnector) sendNodeInfo(fromNode, toNode meshid.NodeID, wantReply bool) {
	log := c.log.With().
		Str("action", "send_nodeinfo").
		Stringer("from_node_id", fromNode).
		Stringer("to_node_id", toNode).
		Logger()

	if !c.IsManagedNode(fromNode) {
		// We have no authority over this node
		return
	}

	ctx := log.WithContext(context.Background())
	if fromNode == c.GetBaseNodeID() {
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
		if meta, ok := ghost.Metadata.(*meshid.GhostMetadata); ok && meta.LongName != "" && meta.ShortName != "" {
			err = c.meshClient.SendNodeInfo(fromNode, toNode, meta.LongName, meta.ShortName, wantReply, meta.PublicKey)
		} else {
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
		log.Warn().Msg("User does not have their node names configured")
		// TODO: Send a notice in the portal informing the user how to set their mesh names
	}
}

func updateGhostLastSeenAt(isDirectNeighbor bool) bridgev2.ExtraUpdater[*bridgev2.Ghost] {
	return func(_ context.Context, ghost *bridgev2.Ghost) bool {
		meta := ghost.Metadata.(*meshid.GhostMetadata)
		meta.LastSeen = ptr.Ptr(jsontime.UnixNow())
		meta.IsDirectNeighbor = isDirectNeighbor
		return true
	}
}

func (c *MeshtasticClient) handleMeshReaction(evt *mesh.MeshReactionEvent) {
	meta, ok := c.UserLogin.Metadata.(*meshid.UserLoginMetadata)
	if evt.IsDM && (!ok || meta.NodeID != evt.To) {
		return
	}

	ghost, _ := c.main.getRemoteGhost(context.Background(), meshid.MakeUserID(evt.From), true)

	var portalKey networkid.PortalKey
	var messIDSender = ""

	if evt.IsDM {
		portalKey = c.makeDMPortalKey(evt.From, evt.To)
		messIDSender = evt.From.String()
		if evt.WantAck {
			c.MeshClient.SendAck(evt.To, evt.From, evt.PacketId)
		}
	} else {
		portalKey = c.makePortalKey(evt.ChannelName, evt.ChannelKey)
		messIDSender = evt.ChannelName
		if evt.WantAck {
			c.MeshClient.SendAck(c.main.GetBaseNodeID(), evt.From, evt.PacketId)
		}
	}

	mess := simplevent.Reaction{
		EventMeta: simplevent.EventMeta{
			Type: bridgev2.RemoteEventReaction,
			LogContext: func(c zerolog.Context) zerolog.Context {
				c = c.Stringer("sender_id", evt.From)
				c = c.Uint32("message_ts", uint32(evt.Timestamp))
				return c
			},
			PortalKey:    portalKey,
			CreatePortal: false,
			Sender:       c.makeEventSender(evt.From),
			Timestamp:    time.Unix(int64(evt.Timestamp), 0),
		},
		EmojiID:       networkid.EmojiID(evt.Emoji),
		Emoji:         evt.Emoji,
		TargetMessage: meshid.MakeMessageID(messIDSender, evt.ReplyId),
	}

	c.bridge.QueueRemoteEvent(c.UserLogin, &mess)
	userInfo := &bridgev2.UserInfo{
		ExtraUpdates: bridgev2.MergeExtraUpdaters(updateGhostLastSeenAt(evt.IsNeighbor)),
	}
	ghost.UpdateInfo(context.Background(), userInfo)
}

func (c *MeshtasticConnector) handleMeshWaypoint(evt *mesh.MeshWaypointEvent) {
	log := c.log.With().
		Str("action", "waypoint_update").
		Stringer("node_id", evt.From).
		Logger()
	log.Info().
		Float32("latitude", evt.Latitude).
		Float32("longitude", evt.Latitude).
		Str("name", evt.Name).
		Str("description", evt.Description).
		Str("icon", evt.Icon).
		Msg("Waypoint received")

	ghost, _ := c.getRemoteGhost(context.Background(), meshid.MakeUserID(evt.From), true)
	userInfo := &bridgev2.UserInfo{
		ExtraUpdates: bridgev2.MergeExtraUpdaters(updateGhostLastSeenAt(evt.IsNeighbor)),
	}
	ghost.UpdateInfo(context.Background(), userInfo)
	// TODO: Save these somewhere and allow other users to retrieve them later?
	// Possibly drop an m.location event in the default channel?
}
