package config

import (
	"fmt"
	"net"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/disgoorg/snowflake/v2"
)

type Config struct {
	Debug        bool
	DiscordToken string
	SelfAddr     *net.TCPAddr
	ServerAddr   *net.TCPAddr
	Whitelist    bool
	GeoIPTimeout time.Duration
}

type Player struct {
	Username string       `toml:"username"`
	UserId   snowflake.ID `toml:"user_id"`
}

type configRaw struct {
	Debug        bool          `toml:"debug"`
	DiscordToken string        `toml:"discord_token"`
	SelfAddr     string        `toml:"self_addr"`
	ServerAddr   string        `toml:"server_addr"`
	Whitelist    bool          `toml:"whitelist"`
	GeoIPTimeout time.Duration `toml:"geoip_timeout"`
}

const defaultSelfAddr = ":25565"
const defaultServerAddr = ":25566"
const defaultGeoIPTimeout = 3 * time.Second

func LoadConfig(filePath string) (*Config, error) {
	cfgRaw := &configRaw{}
	if _, err := toml.DecodeFile(filePath, cfgRaw); err != nil {
		return nil, fmt.Errorf("failed to load toml file: %w", err)
	}

	cfg := &Config{
		Debug:        cfgRaw.Debug,
		DiscordToken: cfgRaw.DiscordToken,
		Whitelist:    cfgRaw.Whitelist,
		GeoIPTimeout: cfgRaw.GeoIPTimeout,
	}
	var err error

	if cfg.DiscordToken == "" {
		return nil, fmt.Errorf("missing discord token")
	}

	if cfg.GeoIPTimeout <= 0 {
		cfg.GeoIPTimeout = defaultGeoIPTimeout
	}

	if cfgRaw.SelfAddr == "" {
		cfgRaw.SelfAddr = defaultSelfAddr
	}
	if cfg.SelfAddr, err = net.ResolveTCPAddr("tcp", cfgRaw.SelfAddr); err != nil {
		return nil, fmt.Errorf("failed to resolve self addr: %w", err)
	}

	if cfgRaw.ServerAddr == "" {
		cfgRaw.ServerAddr = defaultServerAddr
	}
	if cfg.ServerAddr, err = net.ResolveTCPAddr("tcp", cfgRaw.ServerAddr); err != nil {
		return nil, fmt.Errorf("failed to resolve origin addr: %w", err)
	}

	return cfg, nil
}
