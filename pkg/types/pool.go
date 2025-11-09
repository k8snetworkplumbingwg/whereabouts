package types

import (
	"net"
)

type Pool struct {
	IPNet net.IPNet

	RangeStart            net.IP
	IncludeNetworkAddress bool

	RangeEnd                net.IP
	IncludeBroadcastAddress bool
}
