package meshid

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"net"
	"strings"

	"strconv"

	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

const (
	separatorMessageID  = "|"
	separatorPortalID   = "|||"
	separatorDMPortalID = "^"

	// Node ID used for broadcasting
	BROADCAST_ID NodeID = 0xffffffff
	// Node ID used for broadcasting exclusively over MQTT or BLE mesh
	BROADCAST_ID_NO_LORA NodeID = 1

	// The maximum allowed number of hops. The bridge consumes one hop
	// when relaying messages, so the functional limit is one less
	MAX_HOPS = 7
)

type NodeID uint32

func (n NodeID) String() string {
	return fmt.Sprintf("!%08x", uint32(n))
}

// ToMacAddress converts this NodeID into a byte representation of
// an EUI48 mac address.
// Note: This should only be used with synthetic NodeIDs as the generated
// address will always be marked as locally administered.
func (n NodeID) ToMacAddress() net.HardwareAddr {
	a := make([]byte, 4)
	binary.BigEndian.PutUint32(a, uint32(n))
	return net.HardwareAddr{0xA, 0, a[0], a[1], a[2], a[3]}
}

// GetNodeColor returns the RGB values used by various user-interfaces
func (n NodeID) GetNodeColor() (r, g, b uint8) {
	r = uint8((n & 0xFF0000) >> 16)
	g = uint8((n & 0x00FF00) >> 8)
	b = uint8(n & 0x0000FF)
	return
}

// GetDefaultNodeNames returns the default long and short name for an unnamed node
func (n NodeID) GetDefaultNodeNames() (longName, shortName string) {
	name := n.String()
	shortName = name[len(name)-4:]
	longName = fmt.Sprintf("Meshtastic %s", shortName)
	return
}

func ParseNodeID(nodeID string) (NodeID, error) {
	v, _ := strings.CutPrefix(nodeID, "!")
	packet64, err := strconv.ParseUint(string(v), 16, 32)
	if err != nil {
		return NodeID(uint32(0)), err
	}
	return NodeID(uint32(packet64)), nil
}

func MXIDToNodeID(mxid id.UserID) NodeID {
	mxidBytes := []byte(mxid.String())
	return NodeID(crc32.ChecksumIEEE(mxidBytes))
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

func MakeDMPortalID(remote, synth NodeID) networkid.PortalID {
	return networkid.PortalID(fmt.Sprintf("%s%s%s", remote, separatorDMPortalID, synth))
}

func MakeMessageID(senderOrChannel string, packetId uint32) networkid.MessageID {
	return networkid.MessageID(fmt.Sprintf("%d|%s", packetId, senderOrChannel))
}

func ParseUserID(userID networkid.UserID) (nodeID NodeID, err error) {
	packet64, err := strconv.ParseUint(string(userID), 16, 32)
	nodeID = NodeID(uint32(packet64))
	return
}

func ParseMessageID(messageID networkid.MessageID) (senderOrChannelID string, packetID uint32, err error) {
	packetRaw, senderOrChannelID, ok := strings.Cut(string(messageID), separatorMessageID)
	if !ok {
		err = fmt.Errorf("invalid message ID: expected two pipe-separated parts")
		return
	}

	packet64, err := strconv.ParseUint(packetRaw, 10, 32)
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

func ParseDMPortalID(portalID networkid.PortalID) (remote, synth NodeID, err error) {
	parts := strings.Split(string(portalID), separatorDMPortalID)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid DM portal ID: expected two caret-separated parts")
	}
	remote, err = ParseNodeID(parts[0])
	if err != nil {
		return remote, 0, err
	}
	synth, err = ParseNodeID(parts[1])
	return remote, synth, err
}
