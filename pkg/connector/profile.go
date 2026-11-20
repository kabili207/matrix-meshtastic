package connector

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"

	"github.com/kabili207/matrix-meshtastic/pkg/connector/meshdb"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

// Extended profile fields, as Sable and Commet read them. They only reach Matrix
// when the homeserver supports profile fields and the bridge config enables
// matrix.ghost_extra_profile_info.
const (
	colorSchemeField  = "chat.commet.profile_color_scheme"
	statusField       = "org.matrix.msc4426.status"
	commetStatusField = "chat.commet.profile_status"
	bioField          = "gay.fomx.biography" // MSC4440
	commetBioField    = "chat.commet.profile_bio"
)

// nodeProfile builds a node's extended profile. A field with nothing to show is
// set to nil, which bridgev2 sends as a deletion.
func nodeProfile(nodeID meshid.NodeID, mn *meshdb.MeshNodeInfo) database.ExtraProfile {
	color, brightness := nodeColor(nodeID)
	fields := map[string]any{
		colorSchemeField:  map[string]string{"color": color, "brightness": brightness},
		statusField:       nil,
		commetStatusField: nil,
	}
	if mn != nil && mn.Status != "" {
		fields[statusField] = map[string]string{"text": mn.Status}
		fields[commetStatusField] = mn.Status
	}
	plain, formatted := nodeBio(nodeID, mn)
	fields[bioField] = map[string]any{"m.text": []map[string]string{
		{"body": formatted, "mimetype": "text/html"},
		{"body": plain},
	}}
	fields[commetBioField] = map[string]string{
		"body":           plain,
		"format":         string(event.FormatHTML),
		"formatted_body": formatted,
	}

	var profile database.ExtraProfile
	for key, value := range fields {
		// Only fails for values that can't be marshalled, and these all can.
		_ = profile.Set(key, value)
	}
	return profile
}

// nodeColor is the color the Meshtastic apps give a node: the low 24 bits of its
// number as RGB. Brightness follows the apps' choice of text color on it (see
// nodeColorsFromNum in Meshtastic-Android), so Commet builds a light scheme
// around a bright color and a dark one around a dark color.
func nodeColor(nodeID meshid.NodeID) (hex, brightness string) {
	r, g, b := nodeID.GetNodeColor()
	brightness = "dark"
	if (float64(r)*0.299+float64(g)*0.587+float64(b)*0.114)/255 > 0.5 {
		brightness = "light"
	}
	return fmt.Sprintf("#%02x%02x%02x", r, g, b), brightness
}

// nodeBio lists what the bridge knows about a node, as plain text and HTML.
func nodeBio(nodeID meshid.NodeID, mn *meshdb.MeshNodeInfo) (plain, formatted string) {
	type line struct{ label, value string }
	lines := []line{{"Node ID", nodeID.String()}}
	if mn != nil {
		if mn.ShortName != "" {
			lines = append(lines, line{"Short name", mn.ShortName})
		}
		if mn.Role != "" {
			lines = append(lines, line{"Role", mn.Role})
		}
		if mn.HwModel != "" {
			lines = append(lines, line{"Hardware", mn.HwModel})
		}
		if mn.IsLicensed {
			lines = append(lines, line{"Licensed", "yes"})
		}
		if mn.IsUnmessagable {
			lines = append(lines, line{"Messages", "not accepted"})
		}
	}
	var p, f []string
	for _, l := range lines {
		p = append(p, l.label+": "+l.value)
		f = append(f, "<b>"+html.EscapeString(l.label)+":</b> "+html.EscapeString(l.value))
	}
	return strings.Join(p, "\n"), strings.Join(f, "<br>")
}

var errNotManaged = errors.New("node is not managed by the bridge")

// setManagedStatus stores a bridged Matrix user's status, shows it on their
// node's profile and broadcasts it. Like firmware, an empty status is never
// sent; it only stops the periodic broadcast.
func (c *MeshtasticConnector) setManagedStatus(ctx context.Context, nodeID meshid.NodeID, status string) error {
	mn, err := c.meshDB.MeshNodeInfo.GetByNodeID(ctx, nodeID)
	if err != nil {
		return err
	} else if mn == nil || !mn.IsManaged {
		return errNotManaged
	}
	if err := c.meshDB.MeshNodeInfo.SetStatus(ctx, nodeID, status); err != nil {
		return err
	}
	mn.Status = status
	if ghost, err := c.bridge.GetExistingGhostByID(ctx, meshid.MakeUserID(nodeID)); err == nil && ghost != nil {
		ghost.UpdateInfo(ctx, &bridgev2.UserInfo{ExtraProfile: nodeProfile(nodeID, mn)})
	}
	if status == "" {
		return nil
	}
	_, err = c.meshBridge.SendStatusAs(ctx, nodeID.Core(), status)
	return err
}
