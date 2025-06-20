package connector

import (
	"context"
	"fmt"

	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

var _ bridgev2.PortalBridgeInfoFillingNetwork = (*MeshtasticConnector)(nil)

// FillPortalBridgeInfo implements bridgev2.PortalBridgeInfoFillingNetwork.
func (mc *MeshtasticConnector) FillPortalBridgeInfo(portal *bridgev2.Portal, content *event.BridgeEventContent) {
	// This is where we can modify the MSC2346 m.bridge / uk.half-shot.bridge state event
	// We don't actually need to modify it currently, but that may change in the future
}

func (mc *MeshtasticClient) wrapDMInfo(synthNode, remoteNode meshid.NodeID) *bridgev2.ChatInfo {
	info := &bridgev2.ChatInfo{
		Topic: ptr.Ptr(fmt.Sprintf("Meshtastic node %s", remoteNode)),
		Members: &bridgev2.ChatMemberList{
			IsFull:           true,
			TotalMemberCount: 2,
			OtherUserID:      meshid.MakeUserID(remoteNode),
			MemberMap: map[networkid.UserID]bridgev2.ChatMember{
				meshid.MakeUserID(remoteNode): {EventSender: mc.makeEventSender(remoteNode)},
				meshid.MakeUserID(synthNode):  {EventSender: mc.makeEventSender(synthNode)},
			},
			PowerLevels: nil,
		},
		Type: ptr.Ptr(database.RoomTypeDM),
	}
	if ghost, err := mc.bridge.GetExistingGhostByID(context.Background(), meshid.MakeUserID(remoteNode)); ghost != nil && err == nil {
		mc.main.setDMNames(info, ghost)
	}
	return info
}

func (c MeshtasticConnector) setDMNames(info *bridgev2.ChatInfo, ghost *bridgev2.Ghost) {
	if ghost.Name != "" {
		info.Name = &ghost.Name
		if meta, ok := ghost.Metadata.(*meshid.GhostMetadata); ok && meta.ShortName != "" {
			info.Name = ptr.Ptr(fmt.Sprintf("%s (%s)", *info.Name, meta.ShortName))
		}
	}
}

func (mc *MeshtasticClient) wrapChatInfo(user *bridgev2.User, channelID, channelKey string) *bridgev2.ChatInfo {
	members := map[networkid.UserID]bridgev2.ChatMember{}
	if user != nil {
		nodeID := meshid.MXIDToNodeID(user.MXID)
		members[meshid.MakeUserID(nodeID)] = bridgev2.ChatMember{
			EventSender: bridgev2.EventSender{
				Sender:      meshid.MakeUserID(nodeID),
				SenderLogin: meshid.MakeUserLoginID(nodeID),
			},
		}
	}

	topic := "⚠️ Unencrypted"
	if channelKey == "" {
		topic = fmt.Sprintf("Shared Key: %s", channelKey)
		channelID = fmt.Sprintf("%s 🔓", channelID)
	}

	info := &bridgev2.ChatInfo{
		Name:  &channelID,
		Topic: &topic,
		Type:  ptr.Ptr(database.RoomTypeDefault),
		Members: &bridgev2.ChatMemberList{
			IsFull:    false,
			MemberMap: members,
		},
	}
	return info
}
