package ipcamera

import (
	"context"
	"net"
	"time"
)

type arpSource interface {
	SendProbe(net.IP) error
	ReadFrame(context.Context, time.Time) ([]byte, string, error)
	Close() error
}
