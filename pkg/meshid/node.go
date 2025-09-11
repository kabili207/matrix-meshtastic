package meshid

import (
	"encoding/binary"
	"fmt"
	"net"
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
