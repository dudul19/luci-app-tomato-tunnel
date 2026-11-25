//go:build !linux

package daemon

import (
	"context"

	"github.com/dudul19/tomato-tunnel/internal/logx"
)

func reapXrayLoop(ctx context.Context, log *logx.Logger) {}
