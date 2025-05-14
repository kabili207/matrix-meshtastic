package msgconv

import (
	"context"
	"fmt"

	"github.com/kabili207/matrix-meshtastic/pkg/mesh"
	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

func (mc *MessageConverter) ToMeshtastic(ctx context.Context, evt *event.Event, content *event.MessageEventContent) (string, error) {
	return mc.parseText(ctx, content), nil
}

func (mc *MessageConverter) parseText(ctx context.Context, content *event.MessageEventContent) (text string) {
	parseCtx := format.NewContext(ctx)
	if content.Format == event.FormatHTML {
		text = mc.HTMLParser.Parse(content.FormattedBody, parseCtx)
	} else {
		text = content.Body
	}
	return
}

func (mc *MessageConverter) convertPill(displayname, mxid, eventID string, ctx format.Context) string {

	if len(mxid) == 0 || mxid[0] != '@' {
		if displayname == "@room" {
			return mesh.BellCharacter
		}
		return format.DefaultPillConverter(displayname, mxid, eventID, ctx)
	}

	var g *bridgev2.Ghost
	var node meshid.NodeID
	m := id.UserID(mxid)
	if mc.Bridge.IsGhostMXID(m) {
		g, _ = mc.Bridge.GetGhostByMXID(ctx.Ctx, m)
	} else {
		node = meshid.MXIDToNodeID(m)
		g, _ = mc.Bridge.GetGhostByID(ctx.Ctx, meshid.MakeUserID(node))
	}

	if g != nil {
		if meta, ok := g.Metadata.(*meshid.GhostMetadata); ok && meta.ShortName != "" {
			return fmt.Sprintf("@%s", meta.ShortName)
		}
	}
	nodeStr := node.String()
	shortName := nodeStr[len(nodeStr)-4:]
	return fmt.Sprintf("@%s", shortName)
}
