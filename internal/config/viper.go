package config

import (
	"fmt"
	"net"
	"strconv"

	"github.com/spf13/viper"
)

// NewViper is a function to load config from config.json
// You can change the implementation, for example load from env file, consul, etcd, etc
func NewViper() *viper.Viper {
	config := viper.New()

	config.SetConfigName("config")
	config.SetConfigType("json")
	config.AddConfigPath("./../")
	config.AddConfigPath("./")
	err := config.ReadInConfig()

	if err != nil {
		panic(fmt.Errorf("Fatal error config file: %w \n", err))
	}

	normalizeLegacyKeys(config)
	return config
}

// normalizeLegacyKeys keeps older config.json files working. The README
// historically documented database.database / redis.host+port /
// redis.password / app.port, while the code reads database.name /
// redis.addr / redis.pass / web.port. Without these aliases a Redis
// heartbeat silently targets the wrong server or the web app listens on
// a random port.
func normalizeLegacyKeys(config *viper.Viper) {
	if config.GetString("database.name") == "" && config.IsSet("database.database") {
		config.Set("database.name", config.GetString("database.database"))
	}

	if config.GetString("redis.addr") == "" {
		if host := config.GetString("redis.host"); host != "" {
			if port := config.GetInt("redis.port"); port > 0 {
				config.Set("redis.addr", net.JoinHostPort(host, strconv.Itoa(port)))
			} else {
				config.Set("redis.addr", host)
			}
		}
	}
	if config.GetString("redis.pass") == "" && config.IsSet("redis.password") {
		config.Set("redis.pass", config.GetString("redis.password"))
	}
	if config.GetString("redis.user") == "" && config.IsSet("redis.username") {
		config.Set("redis.user", config.GetString("redis.username"))
	}
	if config.GetInt("web.port") == 0 && config.IsSet("app.port") {
		config.Set("web.port", config.GetInt("app.port"))
	}

	// logrus level 0 is PanicLevel, which hides the Warnf logs that make
	// Redis/location failures diagnosable. Default to Info when unset.
	config.SetDefault("log.level", 4)
}
