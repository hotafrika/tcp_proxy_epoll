package service

import (
	"context"

	"github.com/pkg/errors"
	"github.com/rs/zerolog"
)

type application struct {
	logger *zerolog.Logger
	name   string
	bnds   []*backend
}

func newApplication(ctx context.Context, logger *zerolog.Logger, name string, bnds []*backend) *application {
	return &application{
		logger: logger,
		name:   name,
		bnds:   bnds,
	}
}

var (
	errNoActiveBackend = errors.New("no active backends")
)

// nextBackend chooses the next available backend with MIN number of connections.
func (a *application) nextBackend() (*backend, error) {
	var next *backend
	var minConnCount int
	for _, bnd := range a.bnds {
		if bnd.active.Load() {
			if next == nil {
				next = bnd
				minConnCount = bnd.getConnCount()
				continue
			}
			if bnd.getConnCount() < minConnCount {
				next = bnd
			}
		}
	}
	if next == nil {
		return nil, errNoActiveBackend
	}
	return next, nil
}

// createRemoteConnection creates new outgoing connection Conn.
func (a *application) createRemoteConnection() (*Conn, error) {
	nextBackend, err := a.nextBackend()
	if err != nil {
		return nil, errors.Wrap(err, "unable to get next backend")
	}
	rNetConn, err := nextBackend.createConn()
	if err != nil {
		// TODO add feature to find another next backend
		return nil, errors.Wrap(err, "unable to connect to remote backend")
	}
	return newConn(rNetConn, nextBackend), nil
}
