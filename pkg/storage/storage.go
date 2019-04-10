package storage

import (
  "net"
)

// AssignIP assigns an IP by database
func AssignIP() (net.IPNet, error) {
  _, ipnet, err := net.ParseCIDR("192.168.2.200/32")
  return *ipnet, err
}
