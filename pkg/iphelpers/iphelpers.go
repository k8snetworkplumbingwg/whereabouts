package iphelpers

import (
	"net"
)

// CompareIPs reports whether out of 2 given IPs, ipX and ipY, ipY is smaller (-1), the same (0) or larger (1).
// It does so by comparing each of the 16 bytes individually.
// IPs are stored in 16 byte representation regardless of the IP address family.
// For example:
// 192.168.0.3 - [0 0 0 0 0 0 0 0 0 0 255 255 192 168 0 3]
// 2000::3     - [32 0 0 0 0 0 0 0 0 0 0 0 0 0 0 3]
func CompareIPs(ipX net.IP, ipY net.IP) int {
	x := []byte(ipX.To16())
	y := []byte(ipY.To16())
	for i := 0; i < len(x); i++ {
		if x[i] < y[i] {
			return -1
		} else if x[i] > y[i] {
			return 1
		}
	}
	return 0
}
