package connector

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kabili207/matrix-meshtastic/pkg/mesh"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

var (
	shortUserRegex = regexp.MustCompile(`(?:\b|@\s?)([a-f0-9]{4})\b`)
)

// Handles events that are more general to the connector
func (c *MeshtasticConnector) handleGlobalMeshEvent(rawEvt any) {

	switch evt := rawEvt.(type) {
	case *mesh.MeshNodeInfoEvent:
		c.handleMeshNodeInfo(evt)
	case *mesh.MeshMapReportEvent:
		c.handleMapReport(evt)
	case *mesh.MeshLocationEvent:
		c.handleMeshLocation(evt)
	case *mesh.MeshWaypointEvent:
		c.handleMeshWaypoint(evt)
	case *mesh.MeshTracerouteEvent:
		c.handleMeshTraceroute(evt)
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
	c.getRemoteGhost(context.Background(), meshid.MakeUserID(evt.From), true)
	c.meshDB.MeshNodeInfo.SetLastSeen(context.Background(), evt.From, evt.IsNeighbor)
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

	c.getRemoteGhost(context.Background(), meshid.MakeUserID(evt.From), true)
	c.meshDB.MeshNodeInfo.SetLastSeen(context.Background(), evt.From, evt.IsNeighbor)
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

	ctx := context.Background()

	c.main.getRemoteGhost(ctx, meshid.MakeUserID(evt.From), true)

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

	if !evt.IsDM {
		logins, err := c.bridge.GetUserLoginsInPortal(ctx, portalKey)
		if err != nil {
			return
		}
		found := false
		for _, l := range logins {
			if l.ID == c.UserLogin.ID {
				found = true
				break
			}
		}
		if !found {
			return
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
	c.main.meshDB.MeshNodeInfo.SetLastSeen(ctx, evt.From, evt.IsNeighbor)
}

func (c *MeshtasticClient) convertMessageEvent(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, data *mesh.MeshMessageEvent) (*bridgev2.ConvertedMessage, error) {
	mess := data.Message
	formatted := ""
	mentions := &event.Mentions{}
	seenUsers := make(map[id.UserID]struct{})

	matches := shortUserRegex.FindAllStringSubmatchIndex(mess, -1)
	if matches != nil {
		var bodyBuilder, formattedBuilder strings.Builder
		lastIndex := 0

		for _, match := range matches {
			start, end := match[0], match[1]         // full match
			userStart, userEnd := match[2], match[3] // capture group

			bodyBuilder.WriteString(mess[lastIndex:start])
			formattedBuilder.WriteString(data.Message[lastIndex:start])

			potential := mess[userStart:userEnd]
			if ni, _ := c.main.meshDB.MeshNodeInfo.GetByShortUserID(ctx, potential); ni != nil {
				nodeUserID := meshid.MakeUserID(ni.NodeID)
				ghost, err := c.main.getRemoteGhost(ctx, nodeUserID, true)
				if err != nil {
					c.log.Err(err).Msg("unable to fetch remote node info")
					bodyBuilder.WriteString(mess[start:end])
					formattedBuilder.WriteString(data.Message[start:end])
					lastIndex = end
					continue
				}

				ghostMXID := ghost.Intent.GetMXID()
				if gm, ok := ghost.Metadata.(*meshid.GhostMetadata); ok && gm.UserMXID != "" {
					ghostMXID = gm.UserMXID
				}
				userTag := ghostMXID.String()
				userDisplay := userTag
				if ni.LongName != "" {
					userDisplay = fmt.Sprintf("@%s", ni.LongName)
				}

				bodyBuilder.WriteString(userTag)
				formattedBuilder.WriteString(fmt.Sprintf(
					`<a href="%s">%s</a>`,
					ghostMXID.URI().MatrixToURL(),
					html.EscapeString(userDisplay),
				))

				if _, seen := seenUsers[ghostMXID]; !seen {
					seenUsers[ghostMXID] = struct{}{}
					mentions.UserIDs = append(mentions.UserIDs, ghostMXID)
				}
			} else {
				bodyBuilder.WriteString(mess[start:end])
				formattedBuilder.WriteString(data.Message[start:end])
			}

			lastIndex = end
		}

		// copy the remainder
		bodyBuilder.WriteString(mess[lastIndex:])
		formattedBuilder.WriteString(data.Message[lastIndex:])

		mess = bodyBuilder.String()
		formatted = formattedBuilder.String()
	}

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
	m := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			Type:    event.EventMessage,
			Content: content,
		}},
	}
	if data.ReplyId != 0 {
		var messIDSender string
		if data.IsDM {
			messIDSender = data.From.String()
		} else {
			messIDSender = data.ChannelName
		}
		m.ReplyTo = &networkid.MessageOptionalPartID{
			MessageID: meshid.MakeMessageID(messIDSender, data.ReplyId),
		}
	}
	return m, nil
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

	pubKey, _ := c.getGhostPublicKey(context.Background(), c.GetBaseNodeID())
	err = c.meshClient.SendNodeInfo(c.GetBaseNodeID(), nodeId, c.Config.LongName, c.Config.ShortName, true, pubKey)
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

	if evt.To != meshid.BROADCAST_ID && evt.WantResponse {
		c.sendNodeInfo(evt.To, evt.From, false)
	}

	mn, err := c.meshDB.MeshNodeInfo.GetByNodeID(ctx, evt.From)
	if mn == nil || err != nil {
		mn = c.meshDB.MeshNodeInfo.New()
		mn.NodeID = evt.From
	}
	needUpdate := false
	if mn.UserID != evt.UserID || mn.LongName != evt.LongName || mn.ShortName != evt.ShortName || slices.Equal(mn.PublicKey, evt.PublicKey) {
		needUpdate = true
		mn.UserID = evt.UserID
		mn.LongName = evt.LongName
		mn.ShortName = evt.ShortName
		mn.PublicKey = evt.PublicKey
	}
	mn.Role = evt.Role
	mn.IsDirect = evt.IsNeighbor
	mn.IsLicensed = evt.IsLicensed
	mn.IsUnmessagable = evt.IsUnmessagable
	mn.LastSeen = ptr.Ptr(time.Unix(int64(evt.Timestamp), 0))
	err = mn.SetAll(ctx)
	if err != nil {
		log.
			Err(err).
			Str("long_name", evt.LongName).
			Str("short_name", evt.ShortName).
			Msg("Failed to update node db")
	}

	if evt.LongName == "" {
		return
	}

	userInfo := &bridgev2.UserInfo{
		Name:        &evt.LongName,
		IsBot:       ptr.Ptr(false),
		Identifiers: []string{},
	}
	ghost.UpdateInfo(ctx, userInfo)
	if needUpdate {
		c.updateDMPortalInfo(ctx, ghost)
	}
	log.
		Debug().
		Str("long_name", evt.LongName).
		Str("short_name", evt.ShortName).
		Msg("Updated ghost info")
}

func (c *MeshtasticConnector) handleMapReport(evt *mesh.MeshMapReportEvent) {
	log := c.log.With().
		Str("action", "handle_mesh_map_report").
		Stringer("from_node_id", evt.From).
		Stringer("to_node_id", evt.To).
		Logger()
	ctx := log.WithContext(context.Background())
	ghost, err := c.getRemoteGhost(ctx, meshid.MakeUserID(evt.From), evt.To != c.GetBaseNodeID())
	if err != nil {
		log.Err(err).Msg("Failed to get ghost")
		return
	}

	if evt.To != meshid.BROADCAST_ID && evt.WantResponse {
		c.sendNodeInfo(evt.To, evt.From, false)
	}

	mn, err := c.meshDB.MeshNodeInfo.GetByNodeID(ctx, evt.From)
	if mn == nil || err != nil {
		mn = c.meshDB.MeshNodeInfo.New()
		mn.NodeID = evt.From
	}

	needUpdate := false
	if mn.UserID == "" {
		mn.UserID = evt.From.String()
	}

	if mn.LongName != evt.LongName || mn.ShortName != evt.ShortName {
		mn.LongName = evt.LongName
		mn.ShortName = evt.ShortName
		needUpdate = true
	}
	if mn.Role != evt.Role {
		// MapReports don't actually contain the IsMessageable flag, however the firmware
		// forcibly updates this flag when changing to a different role, so it is relatively
		// safe for us to do the same if we haven't received a NodeInfo packet yet
		mn.IsUnmessagable = evt.IsUnmessagableRole
	}
	mn.Role = evt.Role
	mn.IsDirect = evt.IsNeighbor

	mn.LastSeen = ptr.Ptr(time.Unix(int64(evt.Timestamp), 0))
	err = mn.SetAll(ctx)
	if err != nil {
		log.
			Err(err).
			Str("long_name", evt.LongName).
			Str("short_name", evt.ShortName).
			Msg("Failed to update node db")
	}

	if evt.LongName == "" {
		return
	}

	userInfo := &bridgev2.UserInfo{
		Name:        &evt.LongName,
		IsBot:       ptr.Ptr(false),
		Identifiers: []string{},
	}
	ghost.UpdateInfo(ctx, userInfo)
	if needUpdate {
		c.updateDMPortalInfo(ctx, ghost)
	}
	log.
		Debug().
		Str("long_name", evt.LongName).
		Str("short_name", evt.ShortName).
		Msg("Updated ghost info")
}

func (c *MeshtasticConnector) sendNodeInfo(fromNode, toNode meshid.NodeID, wantResponse bool) {
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

	nodeInfo, err := c.meshDB.MeshNodeInfo.GetByNodeID(ctx, fromNode)
	if err != nil {
		log.Err(err).Msg("Failed to query node info")
		return
	}

	var pubKey []byte = nil
	notifyUser := false
	longName, shortName := fromNode.GetDefaultNodeNames()

	if nodeInfo != nil {
		longName, shortName, pubKey = nodeInfo.LongName, nodeInfo.ShortName, nodeInfo.PublicKey
	} else {
		notifyUser = true
	}

	if fromNode == c.GetBaseNodeID() {
		notifyUser = false
		longName, shortName = c.Config.LongName, c.Config.ShortName
	}

	err = c.meshClient.SendNodeInfo(fromNode, toNode, longName, shortName, wantResponse, pubKey)

	if err != nil {
		log.Err(err).Msg("Failed to send node info")
		return
	}

	if notifyUser {
		log.Warn().Msg("User does not have their node names configured")
		// TODO: Send a notice in the portal informing the user how to set their mesh names
	}
}

func (c *MeshtasticClient) handleMeshReaction(evt *mesh.MeshReactionEvent) {
	meta, ok := c.UserLogin.Metadata.(*meshid.UserLoginMetadata)
	if evt.IsDM && (!ok || meta.NodeID != evt.To) {
		return
	}

	c.main.getRemoteGhost(context.Background(), meshid.MakeUserID(evt.From), true)

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

	if !evt.IsDM {
		logins, err := c.bridge.GetUserLoginsInPortal(context.Background(), portalKey)
		if err != nil {
			return
		}
		found := false
		for _, l := range logins {
			if l.ID == c.UserLogin.ID {
				found = true
				break
			}
		}
		if !found {
			return
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

	c.main.meshDB.MeshNodeInfo.SetLastSeen(context.Background(), evt.From, evt.IsNeighbor)

	c.bridge.QueueRemoteEvent(c.UserLogin, &mess)
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
		//Stringer("locked_to", evt.LockedTo).
		//Time("expires", *evt.Expires).
		Msg("Waypoint received")

	ctx := context.Background()
	c.meshDB.MeshNodeInfo.SetLastSeen(ctx, evt.From, evt.IsNeighbor)
	if evt.IsDelete {
		if err := c.meshDB.Waypoint.DeleteByID(ctx, evt.WaypointID); err != nil {
			log.Err(err).Msg("Error deleting waypoint")
		}
	} else if waypoint, err := c.meshDB.Waypoint.GetByWaypointID(ctx, evt.WaypointID); err != nil {
		log.Err(err).Msg("Error checking for existing waypoint")
	} else if waypoint != nil && waypoint.LockedTo != nil && *waypoint.LockedTo != evt.From {
		log.Err(err).Msgf("This waypoint is locked to another node")
	} else {
		if waypoint == nil {
			waypoint = c.meshDB.Waypoint.New()
			waypoint.WaypointID = evt.WaypointID
		}
		waypoint.Name = evt.Name
		waypoint.Description = evt.Description
		waypoint.Icon = evt.Icon
		waypoint.Expires = evt.Expires
		waypoint.Latitude = evt.Latitude
		waypoint.Longitude = evt.Longitude
		waypoint.UpdatedBy = evt.From
		waypoint.UpdatedDate = ptr.Ptr(time.Unix(int64(evt.Timestamp), 0))
		waypoint.LockedTo = evt.LockedTo
		if err := waypoint.SetAll(ctx); err != nil {
			log.Err(err).Msg("Error saving waypoint")
		}
	}
	// TODO: Possibly drop an m.location event in associated channel?
}
