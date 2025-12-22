package connector

import (
	_ "embed"
	"fmt"

	"github.com/kabili207/matrix-meshtastic/pkg/meshid"
	"go.mau.fi/util/configupgrade"
)

//go:embed example-config.yml
var ExampleConfig string

type Config struct {
	LongName            string        `yaml:"long_name"`
	ShortName           string        `yaml:"short_name"`
	HopLimit            uint32        `yaml:"hop_limit"`
	PrimaryChannel      ChannelConfig `yaml:"primary_channel"`
	UDP                 bool          `yaml:"udp"`
	Mqtt                MqttConfig    `yaml:"mqtt"`
	InactivityThreshold int           `yaml:"inactivity_threshold_days"`
}

type MqttConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Uri       string `yaml:"server"`
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
	RootTopic string `yaml:"root_topic"`
}

type ChannelConfig struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}

func upgradeConfig(helper configupgrade.Helper) {
	helper.Copy(configupgrade.Str, "long_name")
	helper.Copy(configupgrade.Str, "short_name")
	helper.Copy(configupgrade.Int, "hop_limit")
	helper.Copy(configupgrade.Bool, "udp")
	helper.Copy(configupgrade.Str, "primary_channel", "name")
	helper.Copy(configupgrade.Str, "primary_channel", "key")
	helper.Copy(configupgrade.Bool, "mqtt", "enabled")
	helper.Copy(configupgrade.Str, "mqtt", "server")
	helper.Copy(configupgrade.Str, "mqtt", "username")
	helper.Copy(configupgrade.Str, "mqtt", "password")
	helper.Copy(configupgrade.Str, "mqtt", "root_topic")
	helper.Copy(configupgrade.Int, "inactivity_threshold_days")
}

func (mc *MeshtasticConnector) GetConfig() (example string, data any, upgrader configupgrade.Upgrader) {
	return ExampleConfig, &mc.Config, configupgrade.SimpleUpgrader(upgradeConfig)
}

func (c *MeshtasticConnector) ValidateConfig() error {
	if c.Config.LongName == "" || c.Config.ShortName == "" {
		return fmt.Errorf("both long_name and short_name are required")
	}
	if len([]byte(c.Config.LongName)) >= 40 {
		return fmt.Errorf("long_name must be less than 40 bytes")
	}
	if len([]byte(c.Config.ShortName)) >= 5 {
		return fmt.Errorf("short_name must be less than 5 bytes")
	}
	if c.Config.HopLimit >= meshid.MAX_HOPS {
		return fmt.Errorf("hop_limit must be less than %d", meshid.MAX_HOPS)
	}
	if !c.Config.UDP && !c.Config.Mqtt.Enabled {
		return fmt.Errorf("at least one connection method must be enabled")
	}
	return nil
}
