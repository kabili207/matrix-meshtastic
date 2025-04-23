package connector

import (
	"context"

	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

func capID() string {
	base := "works.catgirl.capabilities.2025_04_23"
	return base
}

func (tc *MeshtasticClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {

	return &event.RoomFeatures{
		ID: capID(),

		// Technically this is the max number of bytes and not characters, but it provides a good enough hint.
		// The mesh layer will toss an error if the serialized load is too big to send
		MaxTextLength:        int(pb.Constants_DATA_PAYLOAD_LEN) - 1,
		Delete:               event.CapLevelRejected,
		Edit:                 event.CapLevelRejected,
		Reaction:             event.CapLevelPartialSupport, // Supported by client apps, but not embedded devices
		CustomEmojiReactions: false,
		Poll:                 event.CapLevelRejected,
		Reply:                event.CapLevelPartialSupport,
		Thread:               event.CapLevelUnsupported,
		LocationMessage:      event.CapLevelPartialSupport,
		ReadReceipts:         false,
		TypingNotifications:  false,
		Archive:              false,
		MarkAsUnread:         false,
		File:                 event.FileFeatureMap{},
		Formatting: event.FormattingFeatureMap{
			event.FmtBold:               event.CapLevelUnsupported,
			event.FmtItalic:             event.CapLevelUnsupported,
			event.FmtStrikethrough:      event.CapLevelUnsupported,
			event.FmtInlineCode:         event.CapLevelUnsupported,
			event.FmtCodeBlock:          event.CapLevelUnsupported,
			event.FmtSyntaxHighlighting: event.CapLevelUnsupported,
			event.FmtBlockquote:         event.CapLevelUnsupported,
			event.FmtInlineLink:         event.CapLevelUnsupported,
			event.FmtUserLink:           event.CapLevelUnsupported,
			event.FmtRoomLink:           event.CapLevelUnsupported,
			event.FmtEventLink:          event.CapLevelUnsupported,
			event.FmtAtRoomMention:      event.CapLevelUnsupported,
			event.FmtUnorderedList:      event.CapLevelUnsupported,
			event.FmtOrderedList:        event.CapLevelUnsupported,
			event.FmtListStart:          event.CapLevelUnsupported,
			event.FmtListJumpValue:      event.CapLevelUnsupported,
			event.FmtCustomEmoji:        event.CapLevelUnsupported,
		},
	}
}
