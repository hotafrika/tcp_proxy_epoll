package boot

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"os"

	"github.com/hotafrika/tcp_proxy_epoll/service"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var configFile string
var pprofEnabled bool
var logLevel int

//nolint:gosec
func InitAndStart(ctx context.Context) error {
	flag.StringVar(&configFile, "config", "config.json", "config file path")
	flag.BoolVar(&pprofEnabled, "pprof", false, "run pprof on 6060 port")
	flag.IntVar(&logLevel, "loglevel", 3, "log level: 0-4 (debug - fatal), 7 - disabled")
	flag.Parse()

	level := zerolog.Level(logLevel)
	logger := log.Level(level)

	b, err := os.ReadFile(configFile)
	if err != nil {
		return errors.Wrap(err, "ReadFile() config")
	}
	var config Config
	err = json.Unmarshal(b, &config)
	if err != nil {
		return errors.Wrap(err, "Unmarshal() config")
	}
	proxyConfig := config.toProxyConfig()

	proxy, err := service.NewProxy(ctx, &logger, proxyConfig)
	if err != nil {
		return errors.Wrap(err, "NewProxy()")
	}

	if pprofEnabled {
		go func() {
			if err := http.ListenAndServe(":6060", nil); err != nil {
				logger.Error().Err(err).Msg("pprof failed")
			}
		}()
	}

	// here proxy blocks the main routine until ctx cancelled.
	proxy.Run()

	return nil
}
