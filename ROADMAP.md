# Features & roadmap
* Matrix → Meshtastic
  * [x] Message content
    * [x] Plain text
    * [ ] ~~Formatted messages~~ (not supported by Meshtastic)
    * [ ] ~~Media/files~~ (not supported by Meshtastic)
    * [x] Location messages (sent as location update to primary channel)
  * [x] Reactions
  * [ ] ~~Initial room metadata~~ (not applicable)
* Meshtastic → Matrix
  * [x] Message content
    * [x] Plain text
    * [ ] ~~Formatted messages~~ (not supported by Meshtastic)
    * [ ] ~~Media/files~~ (not supported by Meshtastic)
    * [ ] Location messages (pending support of MSC3489/MSC3672 in https://github.com/mautrix/go)
    * [ ] Waypoint messages
  * [x] Chat types
    * [x] Channels
    * [x] Direct Messages
  * [x] Reactions
  * [ ] ~~Group metadata changes~~ (not applicable)
  * [x] User metadata changes
    * [x] Long Name
    * [x] Short Name
* Misc
  * [ ] Connection methods
    * [x] MQTT
    * [x] Mesh over LAN (UDP, v2.6+)
    * [ ] Phone API (serial/bluetooth/TCP)
  * [x] Automatic portal creation
    * [x] When receiving message
  * [x] Private chat creation by inviting Matrix puppet of Meshtastic user to new room
  * [x] Shared group chat portals
