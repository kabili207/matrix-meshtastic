package connector

import (
	_ "embed"
	"fmt"

	"go.mau.fi/util/configupgrade"
)

//go:embed example-config.yml
var ExampleConfig string

type Config struct {
	LongName       string        `yaml:"long_name"`
	ShortName      string        `yaml:"short_name"`
	PrimaryChannel ChannelConfig `yaml:"primary_channel"`
	Mqtt           MqttConfig    `yaml:"mqtt"`
}

type MqttConfig struct {
	Uri      string `yaml:"server"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type ChannelConfig struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}

func upgradeConfig(helper configupgrade.Helper) {
	//helper.Copy(configupgrade.Int, "node_id")
	helper.Copy(configupgrade.Str, "long_name")
	helper.Copy(configupgrade.Str, "short_name")
	helper.Copy(configupgrade.Str, "primary_channel", "name")
	helper.Copy(configupgrade.Str, "primary_channel", "key")
	helper.Copy(configupgrade.Str, "mqtt", "server")
	helper.Copy(configupgrade.Str, "mqtt", "username")
	helper.Copy(configupgrade.Str, "mqtt", "password")
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
	return nil
}
