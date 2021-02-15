package allocate

import (
	"fmt"
	"math/big"
	"net"

	"github.com/dougbtv/whereabouts/pkg/logging"
	"github.com/dougbtv/whereabouts/pkg/types"
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
func AssignIP(ipamConf types.IPAMConfig, reservelist []types.IPReservation, containerID string) (net.IPNet, []types.IPReservation, error) {

	// Setup the basics here.
	_, ipnet, _ := net.ParseCIDR(ipamConf.Range)

	newip, updatedreservelist, err := IterateForAssignment(*ipnet, ipamConf.RangeStart, ipamConf.RangeEnd, reservelist, ipamConf.OmitRanges, containerID)
	if err != nil {
		return net.IPNet{}, nil, err
	}

	return net.IPNet{IP: newip, Mask: ipnet.Mask}, updatedreservelist, nil
}

// DeallocateIP assigns an IP using a range and a reserve list.
func DeallocateIP(iprange string, reservelist []types.IPReservation, containerID string) ([]types.IPReservation, error) {

	updatedreservelist, err := IterateForDeallocation(reservelist, containerID)
	if err != nil {
		return nil, err
	}

	return updatedreservelist, nil
}

// IterateForDeallocation iterates overs currently reserved IPs and the deallocates given the container id.
func IterateForDeallocation(reservelist []types.IPReservation, containerID string) ([]types.IPReservation, error) {

	// Cycle through and find the index that corresponds to our containerID
	foundidx := -1
	for idx, v := range reservelist {
		if v.ContainerID == containerID {
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

func removeIdxFromSlice(s []types.IPReservation, i int) []types.IPReservation {
	s[i] = s[len(s)-1]
	return s[:len(s)-1]
}

// IterateForAssignment iterates given an IP/IPNet and a list of reserved IPs
func IterateForAssignment(ipnet net.IPNet, rangeStart net.IP, rangeEnd net.IP, reservelist []types.IPReservation, excludeRanges []string, containerID string) (net.IP, []types.IPReservation, error) {

	firstip := rangeStart
	var lastip net.IP
	if rangeEnd != nil {
		lastip = rangeEnd
	} else {
		var err error
		firstip, lastip, err = GetIPRange(rangeStart, ipnet)
		if err != nil {
			logging.Errorf("GetIPRange request failed with: %v", err)
			return net.IP{}, reservelist, err
		}
	}

	// Check if this is ipv4mode
	ipv4mode := IsIPv4(firstip)

	logging.Debugf("IterateForAssignment input >> ip: %v | ipnet: %v | first IP: %v | last IP: %v", rangeStart, ipnet, firstip, lastip)

	reserved := make(map[string]bool)
	for _, r := range reservelist {
		ip := BigIntToIP(*IPToBigInt(r.IP), ipv4mode)
		reserved[ip.String()] = true
	}

	excluded := []*net.IPNet{}
	for _, v := range excludeRanges {
		_, subnet, _ := net.ParseCIDR(v)
		excluded = append(excluded, subnet)
	}

	// Iterate every IP address in the range
	var assignedip net.IP
	performedassignment := false
MAINITERATION:
	for i := IPToBigInt(firstip); IPToBigInt(lastip).Cmp(i) == 1 || IPToBigInt(lastip).Cmp(i) == 0; i.Add(i, big.NewInt(1)) {

		// logging.Debugf("!trace firstip bigint: %v", i)

		assignedip = BigIntToIP(*i, ipv4mode)
		// logging.Debugf("!trace assignedip: %v", assignedip)
		stringip := fmt.Sprint(assignedip)
		// For each address see if it has been allocated
		if reserved[stringip] {
			// Continue if this IP is allocated.
			continue
		}

		// We can try to work with the current IP
		// However, let's skip 0-based addresses in IPv4
		ipbytes := i.Bytes()
		if ipv4mode {
			if ipbytes[len(ipbytes)-1] == 0 {
				continue
			}
		}

		// Lastly, we need to check if this IP is within the range of excluded subnets
		for _, subnet := range excluded {
			if subnet.Contains(BigIntToIP(*i, ipv4mode).To16()) {
				continue MAINITERATION
			}
		}

		// Ok, this one looks like we can assign it!
		performedassignment = true

		logging.Debugf("Reserving IP: |%v|", stringip+" "+containerID)
		reservelist = append(reservelist, types.IPReservation{IP: assignedip, ContainerID: containerID})
		break
	}

	if !performedassignment {
		return net.IP{}, reservelist, AssignmentError{firstip, lastip, ipnet}
	}

	return assignedip, reservelist, nil
}

func mergeIPAddress(net, host []byte) ([]byte, error) {
	if len(net) != len(host) {
		return nil, fmt.Errorf("netmask and host do not match")
	}
	addr := append([]byte{}, net...)
	for i := range net {
		addr[i] = net[i] | host[i]
	}
	return addr, nil
}

// GetIPRange returns the first and last IP in a range
func GetIPRange(ip net.IP, ipnet net.IPNet) (net.IP, net.IP, error) {
	// ====== BEGIN =====
	// TODO: consider need to have this check.... this is only for
	// unit-testing code
	//
	// Good hints here: http://networkbit.ch/golang-ip-address-manipulation/
	// Nice info on bitwise operations: https://yourbasic.org/golang/bitwise-operator-cheat-sheet/
	// Get info about the mask.
	mask := ipnet.Mask
	ones, bits := mask.Size()
	masklen := bits - ones

	// Error when the mask isn't large enough.
	if masklen < 2 {
		return nil, nil, fmt.Errorf("Net mask is too short, must be 2 or more: %v", masklen)
	}
	// ====== END =====

	// real code here
	// NOTE:
	// this function splits ip address into network part and host part
	// first. Then start/end IP address should be following:
	// start IP address <network part> + host part (0 or args)
	// end IP address <network part> | <max value of host part bits (*1)>
	// *1 should be the inverse from "network mask"
	//				(e.g. ffffff00 -> 000000ff)

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
	firstIP, _ := mergeIPAddress([]byte(network), first)
	lastIP, _ := mergeIPAddress([]byte(network), last)

	return firstIP, lastIP, nil
}

// IsIPv4 checks if an IP is v4.
func IsIPv4(checkip net.IP) bool {
	return checkip.To4() != nil
}

func isIntIPv4(checkipint *big.Int) bool {
	return !(len(checkipint.Bytes()) == net.IPv6len)
}

// BigIntToIP converts a big.Int to a net.IP
func BigIntToIP(inipint big.Int, isipv4 bool) net.IP {
	var outip net.IP
	intbytes := inipint.Bytes()

	// For IPv6 addresses
	if !isipv4 {
		outip = net.IP(make([]byte, net.IPv6len))

		// We want to pad bytes to the left of this INT until we have an IPv6 length.
		if len(intbytes) < net.IPv6len {
			offsetnumberofbytes := net.IPv6len - len(intbytes)
			for m := 0; m < offsetnumberofbytes; m++ {
				intbytes = append(make([]byte, 1), intbytes...)
			}
		}

		// Assign the bytes.
		for i := 0; i < len(intbytes); i++ {
			outip[i] = intbytes[i]
		}

	}

	// It's an IPv4 address.
	// Make a phony IPv4 address so we get the "magic bytes" (10 & 11 == 255)
	outip = net.ParseIP("0.0.0.0")

	for i := 0; i < len(intbytes); i++ {
		outip[15-i] = intbytes[len(intbytes)-i-1]
	}
	return outip

}

// IPToBigInt converts a net.IP to a big.Int
func IPToBigInt(IPv6Addr net.IP) *big.Int {
	IPv6Int := big.NewInt(0)
	foo := []byte(IPv6Addr)
	IPv6Int.SetBytes(foo)

	return IPv6Int
}
