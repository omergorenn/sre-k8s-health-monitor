package config

import (
	"fmt"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

var (
	configWatcher = viper.New()
	secretWatcher = viper.New()
)

func Get() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("json")
	viper.AddConfigPath("./config")
	viper.AutomaticEnv()

	err := viper.ReadInConfig()
	if err != nil {
		return nil, err
	}

	config := Default()
	err = watchFile[Config](config, "config", configWatcher)
	if err != nil {
		return nil, err
	}

	err = watchFile[Secret](config.Secret, "secret", secretWatcher)
	if err != nil {
		return nil, err
	}

	return config, nil
}

func watchFile[T any](bindTo *T, file string, watcher *viper.Viper) error {
	watcher.AddConfigPath("./config")    // local folder
	watcher.AddConfigPath("/app/config") // required for container
	watcher.SetConfigName(file)
	watcher.SetConfigType("json")
	watcher.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := watcher.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read in config. err: %s", err)
	}

	if err := watcher.Unmarshal(bindTo); err != nil {
		return fmt.Errorf("failed to unmarshal config. err: %s", err)
	}

	watcher.WatchConfig()
	watcher.OnConfigChange(func(event fsnotify.Event) {
		if err := watcher.Unmarshal(bindTo); err == nil {
			zap.L().Info(fmt.Sprintf("config %s updated", file))
		} else {
			zap.L().Error(fmt.Sprintf("could not unmarshal %s config.", file), zap.Error(err))
		}
	})

	return nil
}
