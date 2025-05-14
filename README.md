# matrix-meshtastic
A Matrix-Meshtastic bridge based on [meshtastic-go](https://github.com/meshnet-gophers/meshtastic-go).

This bridge makes use of the MQTT functionality to create virtual devices, and therefore requires a
functioning MQTT server.

## Documentation

### Setup
The official Mautrix setup covers most of the generic options [Bridge setup](https://docs.mau.fi/bridges/go/setup.html)
(or [with Docker](https://docs.mau.fi/bridges/general/docker-setup.html))

The following options are specific to the Meshtastic bridge and all of them are required.

```yaml
# Network-specific config options
network:
    # The full name of the bridge on the Meshtastic network.
    # Must be less than 40 bytes
    long_name: "Matrix Bridge"
    # A VERY short name for use on tiny OLED screens
    # Must be less than 5 bytes
    short_name: "MB"
    # Primary channel. This will be used to broadcast and receive node info,
    # location updates, and unencrypted DMs between nodes. Should be left as
    # the default LongFast with the "AQ==" key
    primary_channel:
        name: LongFast
        key: "1PG7OiApB1nwvP+rz05pAQ=="
    # Credentials for connecting to the MQTT server
    # Using the public MQTT server is not advised due to various limitations
    mqtt:
        server: tcp://mqtt.example.com:1883
        username: meshdev
        password: large4cats
        root_topic: msh/US
```
### General Use
The general use instructions [from Mautrix](https://docs.mau.fi/bridges/general/using-bridges.html)

#### Logging in
Although not required in order to chat in existing bridged rooms, you will not be able
DM users on Meshtastic or bridge new channels unless you login.

To do so, send a direct message to the Meshtastic bot user (typically `@meshtastic_bot:example.com`)
and use the `login` command. You will be prompted for the long and short names you wish to use
on the Meshtastic network. Your Meshtastic Node ID will be generated based on your matrix user ID.

#### Joining a channel
Channels on Meshtastic are based on the name of the channel and a pre-shared key (PSK). To join a
channel, message the bot user and use the `join-channel` command, passing both the name and PSK like so:
`join-channel LongFast 1PG7OiApB1nwvP+rz05pAQ==`

### Features & Roadmap
[ROADMAP.md](ROADMAP.md) contains a general overview of what is supported by the bridge.
