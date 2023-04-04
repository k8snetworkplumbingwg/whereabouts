package allocate

import (
	"fmt"
	"net"

	"github.com/k8snetworkplumbingwg/whereabouts/pkg/iphelpers"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/logging"
	"github.com/k8snetworkplumbingwg/whereabouts/pkg/types"
)

// AssignmentError defines an IP assignment error.
type AssignmentError struct {
	firstIP net.IP
	lastIP  net.IP
	ipnet   net.IPNet
}

func (a AssignmentError) Error() string {
	return fmt.Sprintf("Could not allocate IP in range: ip: %v / - %v / range: %#v", a.firstIP, a.lastIP, a.ipnet)
}

// AssignIP assigns an IP using a range and a reserve list.
func AssignIP(ipamConf types.RangeConfiguration, reservelist []types.IPReservation, containerID string, podRef string) (net.IPNet, []types.IPReservation, error) {

	// Setup the basics here.
	_, ipnet, _ := net.ParseCIDR(ipamConf.Range)

	newip, updatedreservelist, err := IterateForAssignment(*ipnet, ipamConf.RangeStart, ipamConf.RangeEnd, reservelist, ipamConf.OmitRanges, containerID, podRef)
	if err != nil {
		return net.IPNet{}, nil, err
	}

	return net.IPNet{IP: newip, Mask: ipnet.Mask}, updatedreservelist, nil
}

// DeallocateIP assigns an IP using a range and a reserve list.
func DeallocateIP(reservelist []types.IPReservation, containerID string) ([]types.IPReservation, net.IP, error) {

	updatedreservelist, hadip, err := IterateForDeallocation(reservelist, containerID, getMatchingIPReservationIndex)
	if err != nil {
		return nil, nil, err
	}

	logging.Debugf("Deallocating given previously used IP: %v", hadip)

	return updatedreservelist, hadip, nil
}

// IterateForDeallocation iterates overs currently reserved IPs and the deallocates given the container id.
func IterateForDeallocation(
	reservelist []types.IPReservation,
	containerID string,
	matchingFunction func(reservation []types.IPReservation, id string) int) ([]types.IPReservation, net.IP, error) {

	foundidx := matchingFunction(reservelist, containerID)
	// Check if it's a valid index
	if foundidx < 0 {
		return reservelist, nil, fmt.Errorf("did not find reserved IP for container %v", containerID)
	}

	returnip := reservelist[foundidx].IP

	updatedreservelist := removeIdxFromSlice(reservelist, foundidx)
	return updatedreservelist, returnip, nil
}

func getMatchingIPReservationIndex(reservelist []types.IPReservation, id string) int {
	foundidx := -1
	for idx, v := range reservelist {
		if v.ContainerID == id {
			foundidx = idx
			break
		}
	}
	return foundidx
}

func removeIdxFromSlice(s []types.IPReservation, i int) []types.IPReservation {
	s[i] = s[len(s)-1]
	return s[:len(s)-1]
}

// IterateForAssignment iterates given an IP/IPNet and a list of reserved IPs
func IterateForAssignment(ipnet net.IPNet, rangeStart net.IP, rangeEnd net.IP, reservelist []types.IPReservation, excludeRanges []string, containerID string, podRef string) (net.IP, []types.IPReservation, error) {
	firstip := rangeStart.To16()
	var lastip net.IP
	if rangeEnd != nil {
		lastip = rangeEnd.To16()
	} else {
		var err error
		firstip, lastip, err = GetIPRange(rangeStart, ipnet)
		if err != nil {
			logging.Errorf("GetIPRange request failed with: %v", err)
			return net.IP{}, reservelist, err
		}
	}
	logging.Debugf("IterateForAssignment input >> ip: %v | ipnet: %v | first IP: %v | last IP: %v", rangeStart, ipnet, firstip, lastip)

	reserved := make(map[string]bool)
	for _, r := range reservelist {
		reserved[r.IP.String()] = true
	}

	// excluded,            "192.168.2.229/30", "192.168.1.229/30",
	excluded := []*net.IPNet{}
	for _, v := range excludeRanges {
		_, subnet, _ := net.ParseCIDR(v)
		excluded = append(excluded, subnet)
	}

	// Iterate every IP address in the range
	var assignedip net.IP
	performedassignment := false
	endip := iphelpers.IPAddOffset(lastip, uint64(1))
	for i := firstip; ipnet.Contains(i) && iphelpers.CompareIPs(i, endip) < 0; i = iphelpers.IPAddOffset(i, uint64(1)) {
		// if already reserved, skip it
		if reserved[i.String()] {
			continue
		}

		// Lastly, we need to check if this IP is within the range of excluded subnets
		isAddrExcluded := false
		for _, subnet := range excluded {
			if subnet.Contains(i) {
				isAddrExcluded = true
				firstExcluded, _, _ := net.ParseCIDR(subnet.String())
				_, lastExcluded, _ := GetIPRange(firstExcluded, *subnet)
				if lastExcluded != nil {
					if i.To4() != nil {
						// exclude broadcast address
						i = iphelpers.IPAddOffset(lastExcluded, uint64(1))
					} else {
						i = lastExcluded
					}
					logging.Debugf("excluding %v and moving to the next available ip: %v", subnet, i)
				}
			}
		}
		if isAddrExcluded {
			continue
		}

		// Ok, this one looks like we can assign it!
		performedassignment = true

		assignedip = i
		logging.Debugf("Reserving IP: |%v|", assignedip.String()+" "+containerID)
		reservelist = append(reservelist, types.IPReservation{IP: assignedip, ContainerID: containerID, PodRef: podRef})
		break
	}

	if !performedassignment {
		return net.IP{}, reservelist, AssignmentError{firstip, lastip, ipnet}
	}

	return assignedip, reservelist, nil
}

func mergeIPAddress(net, host []byte) ([]byte, error) {
	if len(net) != len(host) {
		return nil, fmt.Errorf("not matched")
	}
	addr := append([]byte{}, net...)
	for i := range net {
		addr[i] = net[i] | host[i]
	}
	return addr, nil
}

// GetIPRange returns the first and last IP in a range
func GetIPRange(ip net.IP, ipnet net.IPNet) (net.IP, net.IP, error) {
	mask := ipnet.Mask
	ones, bits := mask.Size()
	masklen := bits - ones

	// Error when the mask isn't large enough.
	if masklen < 2 {
		return nil, nil, fmt.Errorf("net mask is too short, must be 2 or more: %v", masklen)
	}

	// get network part
	network := ip.Mask(ipnet.Mask)
	// get bitmask for host
	hostMask := net.IPMask(append([]byte{}, ipnet.Mask...))
	for i, n := range hostMask {
		hostMask[i] = ^n
	}
	// get host part of ip
	first := ip.Mask(net.IPMask(hostMask))
	// if ip is just same as ipnet.IP, i.e. just network address,
	// increment it for start ip
	if ip.Equal(ipnet.IP) {
		first[len(first)-1] = 0x1
	}
	// calculate last byte
	last := hostMask
	// if IPv4 case, decrement 1 for broadcasting address
	if ip.To4() != nil {
		last[len(last)-1]--
	}
	// get first ip and last ip based on network part + host part
	firstIPbyte, _ := mergeIPAddress([]byte(network), first)
	lastIPbyte, _ := mergeIPAddress([]byte(network), last)
	firstIP := net.IP(firstIPbyte).To16()
	lastIP := net.IP(lastIPbyte).To16()

	return firstIP, lastIP, nil
}
