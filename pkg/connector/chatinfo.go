package connector

import (
	"fmt"

	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

func (mc *MeshtasticClient) wrapDMInfo(synthNode, remoteNode meshid.NodeID) *bridgev2.ChatInfo {
	info := &bridgev2.ChatInfo{
		Topic: ptr.Ptr("Meshtastic DM"),
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
	return info
}

func (mc *MeshtasticClient) wrapChatInfo(user *bridgev2.User, channelID, channelKey string) *bridgev2.ChatInfo {
	nodeID := mc.main.MXIDToNodeId(user.MXID)
	info := &bridgev2.ChatInfo{
		Name:  ptr.Ptr(channelID),
		Topic: ptr.Ptr(fmt.Sprintf("ID: %s\nKey: %s", channelID, channelKey)),
		Members: &bridgev2.ChatMemberList{
			IsFull: false,
			MemberMap: map[networkid.UserID]bridgev2.ChatMember{
				meshid.MakeUserID(nodeID): {
					EventSender: bridgev2.EventSender{
						IsFromMe:    true,
						Sender:      meshid.MakeUserID(nodeID),
						SenderLogin: networkid.UserLoginID(user.GetDefaultLogin().ID),
					},
					Membership: event.MembershipJoin,
					Nickname:   ptr.Ptr(nodeID.String()),
				},
			},
		},
	}
	return info
}
