package allocate

import (
  "bytes"
  "encoding/binary"
  "fmt"
  "github.com/dougbtv/whereabouts/pkg/logging"
  "net"
  "strconv"
)

// AssignIP assigns an IP by database
func AssignIP(iprange string) (net.IPNet, error) {

  // Setup the basics here.
  ip, ipnet, err := net.ParseCIDR(iprange)

  firstip, lastip, err := GetIPRange(ip, *ipnet)
  if err != nil {
    logging.Errorf("GetIPRange request failed with: %v", err)
    return net.IPNet{}, err
  }

  logging.Debugf("Input IP range: %v | first IP: %v | last IP: %v", iprange, firstip, lastip)

  useaddr := net.IPNet{IP: firstip, Mask: ipnet.Mask}

  return useaddr, nil
}

// GetIPRange returns the first and last IP in a range
func GetIPRange(ip net.IP, ipnet net.IPNet) (net.IP, net.IP, error) {

  // Good hints here: http://networkbit.ch/golang-ip-address-manipulation/
  // Nice info on bitwise operations: https://yourbasic.org/golang/bitwise-operator-cheat-sheet/
  // Get info about the mask.
  mask := ipnet.Mask
  ones, bits := mask.Size()
  masklen := bits - ones

  // Error when the mask isn't large enough.
  if ones < 3 {
    return nil, nil, fmt.Errorf("Net mask is too short, must be 3 or more: %v", masklen)
  }

  // Get a long from the current IP address
  longip := ip2Long(ip)

  // logging.Debugf("binary rep of IP: %v", strconv.FormatInt(int64(longip), 2))

  // Shift out to get the lowest IP value.
  lowestiplong := longip >> uint(masklen)
  lowestiplong = lowestiplong << uint(masklen)
  // logging.Debugf("lowest value:     %v", strconv.FormatInt(int64(lowestiplong), 2))

  // Get the mask as a long, shift it out
  masklong := ^uint32(0) >> uint(ones)
  // logging.Debugf("mask:             %v", strconv.FormatInt(int64(masklong), 2))

  // Now figure out the highest value...
  // We can OR that value...
  highestiplong := lowestiplong | masklong
  // logging.Debugf("highest value:    %v", strconv.FormatInt(int64(highestiplong), 2))

  // Now let's send it back to IPs, we can get first and last in the range...
  firstip := longToIP4(lowestiplong)
  lastip := longToIP4(highestiplong)

  // Some debugging information...
  // logging.Debugf("first ip: %v", firstip)
  // logging.Debugf("last ip:  %v", lastip)

  // logging.Debugf("mask len: %v", len(mask))
  // logging.Debugf("mask raw: %v", mask)
  // logging.Debugf("mask size: 1's: %v / bits: %v / len: %v", ones, bits, masklen)

  return firstip, lastip, nil

}

// from: https://www.socketloop.com/tutorials/golang-convert-ip-address-string-to-long-unsigned-32-bit-integer
func ip2Long(ip net.IP) uint32 {
  var long uint32
  binary.Read(bytes.NewBuffer(ip.To4()), binary.BigEndian, &long)
  return long
}

func longToIP4(inIPInt uint32) net.IP {
  // we process it as an int64
  ipInt := int64(inIPInt)
  // need to do two bit shifting and "0xff" masking
  b0 := strconv.FormatInt((ipInt>>24)&0xff, 10)
  b1 := strconv.FormatInt((ipInt>>16)&0xff, 10)
  b2 := strconv.FormatInt((ipInt>>8)&0xff, 10)
  b3 := strconv.FormatInt((ipInt & 0xff), 10)
  return net.ParseIP(b0 + "." + b1 + "." + b2 + "." + b3)
}
