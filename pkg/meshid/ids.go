package meshid

import (
	"fmt"
	"strings"

	"strconv"

	"maunium.net/go/mautrix/bridgev2/networkid"
)

const (
	separatorMessageID = "|"
	separatorPortalID  = "|||"
)

type NodeID uint32

func (n NodeID) String() string {
	return fmt.Sprintf("!%08x", uint32(n))
}

func ParseNodeID(nodeID string) (NodeID, error) {
	v, _ := strings.CutPrefix(nodeID, "!")
	packet64, err := strconv.ParseUint(string(v), 16, 32)
	if err != nil {
		return NodeID(uint32(0)), err
	}
	return NodeID(uint32(packet64)), nil
}

func MakeUserID(nodeID NodeID) networkid.UserID {
	return networkid.UserID(fmt.Sprintf("%08x", uint32(nodeID)))
}

func MakeUserLoginID(nodeID NodeID) networkid.UserLoginID {
	return networkid.UserLoginID(fmt.Sprintf("%08x", uint32(nodeID)))
}

func MakePortalID(channelId string, channelKey *string) networkid.PortalID {
	// TODO: Add key hash to differeciate between chats?
	if channelKey == nil {
		return networkid.PortalID(fmt.Sprintf("%s%s", channelId, separatorPortalID))
	}
	return networkid.PortalID(fmt.Sprintf("%s%s%s", channelId, separatorPortalID, *channelKey))
}

func ParseUserID(userID networkid.UserID) (nodeID NodeID, err error) {
	packet64, err := strconv.ParseUint(string(userID), 16, 32)
	nodeID = NodeID(uint32(packet64))
	return
}

func ParseMessageID(messageID networkid.MessageID) (channelID string, packetID uint32, err error) {
	parts := strings.Split(string(messageID), separatorMessageID)
	if len(parts) != 2 {
		err = fmt.Errorf("invalid message ID: expected two pipe-separated parts")
		return

	}
	channelID = parts[0]
	packet64, err := strconv.ParseUint(parts[1], 10, 32)
	packetID = uint32(packet64)
	return
}

func ParsePortalID(portalID networkid.PortalID) (channelID string, channelKey string, err error) {
	parts := strings.Split(string(portalID), separatorPortalID)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid portal ID: expected two pipe-separated parts")
	}
	return parts[0], parts[1], nil
}
