package app

import (
	"context"

	"github.com/chobits02/provena/internal/config"
)

// ReconcilePacketCapture applies config changes without restarting the main API.
func (a *App) ReconcilePacketCapture(cfg config.PacketCaptureConfig) error {
	if a == nil || a.packetCapture == nil {
		return nil
	}
	return a.packetCapture.Configure(cfg)
}

func (a *App) shutdownPacketCapture() {
	if a == nil || a.packetCapture == nil {
		return
	}
	_ = a.packetCapture.Stop(context.Background())
}
