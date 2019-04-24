package allocate

import (
  "bytes"
  "encoding/binary"
  "fmt"
  "github.com/dougbtv/whereabouts/pkg/logging"
  "github.com/dougbtv/whereabouts/pkg/types"
  "net"
  "strconv"
  "strings"
)

// AssignIP assigns an IP using a range and a reserve list.
func AssignIP(ipamConf types.IPAMConfig, reservelist string, containerID string) (net.IPNet, string, error) {

  // Setup the basics here.
  ip, ipnet, err := net.ParseCIDR(ipamConf.Range)

  newip, updatedreservelist, err := IterateForAssignment(ip, *ipnet, SplitReserveList(reservelist), ipamConf.OmitRanges, containerID)
  if err != nil {
    logging.Errorf("IterateForAssignment request failed with: %v", err)
    return net.IPNet{}, "", err
  }

  // logging.Debugf("newip: %v, updatedreservelist: %+v", newip, updatedreservelist)

  return net.IPNet{IP: newip, Mask: ipnet.Mask}, JoinReserveList(updatedreservelist), nil
}

// DeallocateIP assigns an IP using a range and a reserve list.
func DeallocateIP(iprange string, reservelist string, containerID string) (string, error) {

  updatedreservelist, err := IterateForDeallocation(SplitReserveList(reservelist), containerID)
  if err != nil {
    logging.Errorf("IterateForDeallocation request failed with: %v", err)
    return "", err
  }

  // logging.Debugf("deallocate updatedreservelist: %+v", newip, updatedreservelist)

  return JoinReserveList(updatedreservelist), nil
}

// IterateForDeallocation iterates overs currently reserved IPs and the deallocates given the container id.
func IterateForDeallocation(reservelist []string, containerID string) ([]string, error) {

  // Cycle through and find the index that corresponds to our containerID
  foundidx := -1
  for idx, v := range reservelist {
    if strings.Contains(v, " "+containerID) {
      foundidx = idx
      break
    }
  }

  // Check if it's a valid index
  if foundidx < 0 {
    return reservelist, fmt.Errorf("Did not find reserved IP for container %v", containerID)
  }

  updatedreservelist := removeIdxFromSlice(reservelist, foundidx)
  return updatedreservelist, nil

}

func removeIdxFromSlice(s []string, i int) []string {
  s[i] = s[len(s)-1]
  return s[:len(s)-1]
}

// IterateForAssignment iterates given an IP/IPNet and a list of reserved IPs
func IterateForAssignment(ip net.IP, ipnet net.IPNet, reservelist []string, excludeRanges []string, containerID string) (net.IP, []string, error) {

  firstip, lastip, err := GetIPRange(ip, ipnet)
  logging.Debugf("IterateForAssignment input >> ip: %v | ipnet: %v | first IP: %v | last IP: %v", ip, ipnet, firstip, lastip)
  if err != nil {
    logging.Errorf("GetIPRange request failed with: %v", err)
    return net.IP{}, reservelist, err
  }

  // Iterate every IP address in the range
  var assignedip net.IP
  performedassignment := false
MAINITERATION:
  for i := ip2Long(firstip); i < ip2Long(lastip); i++ {
    // For each address see if it has been allocated
    isallocated := false
    for _, v := range reservelist {
      // Skip to the next IP if it's already allocated
      // We look for the space at the end so 192.168.1.1 doesn't match 192.168.1.100
      if strings.Contains(v, fmt.Sprint(longToIP4(i))+" ") {
        isallocated = true
        break
      }
    }

    // Continue if this IP is allocated.
    if isallocated {
      continue
    }

    // We can try to work with the current IP
    // However, let's skip 0-based addresses
    // So go ahead and continue if the 4th byte equals 0
    ipbytes := longToIP4(i).To4()
    if ipbytes[3] == 0 {
      continue
    }

    // Lastly, we need to check if this IP is within the range of excluded subnets
    for _, v := range excludeRanges {
      _, subnet, _ := net.ParseCIDR(v)
      if subnet.Contains(longToIP4(i).To4()) {
        continue MAINITERATION
      }
    }

    // Ok, this one looks like we can assign it!
    performedassignment = true
    assignedip = longToIP4(i)
    stringip := fmt.Sprint(assignedip)
    logging.Debugf("Reserving IP: |%v|", stringip+" "+containerID)
    reservelist = append(reservelist, stringip+" "+containerID)
    break

  }

  if !performedassignment {
    return net.IP{}, reservelist, fmt.Errorf("Could not allocate IP in range: ip: %v / range: %v", ip, ipnet)
  }

  return assignedip, reservelist, nil

}

// SplitReserveList splits line breaks in a string into a list
func SplitReserveList(reservelist string) []string {
  return strings.Split(reservelist, "\n")
}

// JoinReserveList joins a reservelist back together with line breaks
func JoinReserveList(reservelist []string) string {
  return strings.Join(reservelist, "\n")
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
