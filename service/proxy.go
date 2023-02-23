package service

import (
	"context"
	"sync"

	"github.com/pkg/errors"
	"github.com/rs/zerolog"
)

type Proxy struct {
	ctx     context.Context
	cancel  context.CancelFunc
	logger  *zerolog.Logger
	apps    []*application
	fnds    []*frontend
	bnds    []*backend
	bufPool *sync.Pool
}

func NewProxy(ctx context.Context, logger *zerolog.Logger, config ProxyConfig) (Proxy, error) {
	nCtx, cancel := context.WithCancel(ctx)

	bufPool := sync.Pool{
		New: func() any {
			b := make([]byte, 4*1024)
			return &b
		},
	}

	apps := make([]*application, 0, len(config.Apps))
	fnds := make([]*frontend, 0, len(config.Apps))
	bnds := make([]*backend, 0, len(config.Apps))

	for _, configApp := range config.Apps {
		// Create backends for the app
		appBnds := make([]*backend, 0, len(configApp.Targets))
		for _, target := range configApp.Targets {
			bnd, err := newBackend(ctx, logger, target, &bufPool)
			if err != nil {
				cancel()
				return Proxy{}, errors.Wrap(err, "newBackend()")
			}
			appBnds = append(appBnds, bnd)
		}
		bnds = append(bnds, appBnds...)

		// Create app
		app := newApplication(nCtx, logger, configApp.Name, appBnds)
		apps = append(apps, app)

		// Create frontends for the app
		for _, port := range configApp.Ports {
			fnd, err := newFrontend(nCtx, logger, port, app, &bufPool)
			if err != nil {
				cancel()
				return Proxy{}, errors.Wrap(err, "newFrontend()")
			}
			fnds = append(fnds, fnd)
		}
	}

	return Proxy{
		ctx:     nCtx,
		cancel:  cancel,
		logger:  logger,
		apps:    apps,
		fnds:    fnds,
		bnds:    bnds,
		bufPool: &bufPool,
	}, nil
}

// Run blocks until all frontends and backends finish work (ctx is done).
func (p Proxy) Run() {
	var wg sync.WaitGroup

	for _, bnd := range p.bnds {
		bnd := bnd
		wg.Add(1)
		go bnd.run(&wg)
	}
	for _, fnd := range p.fnds {
		fnd := fnd
		wg.Add(1)
		go fnd.run(&wg)
	}

	wg.Wait()
}

// ProxyConfig represents Proxy config file.
type ProxyConfig struct {
	Apps []ConfigApp
}

type ConfigApp struct {
	Name    string
	Ports   []int
	Targets []string
}
