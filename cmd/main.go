//nolint:gosec
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	_ "net/http/pprof"

	"github.com/hotafrika/tcp_proxy_epoll/boot"
	"github.com/pkg/errors"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer cancel()

	err := boot.InitAndStart(ctx)
	if err != nil {
		err = errors.Wrap(err, "InitAndStart()")
		_, _ = fmt.Fprintf(os.Stderr, "%v", err)
		return
	}
}
