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
	logging.Debugf("IterateForAssignment input >> ip: %v | ipnet: %v | first IP: %v | last IP: %v", rangeStart, ipnet, firstip, lastip)

	reserved := make(map[string]bool)
	for _, r := range reservelist {
		ip := BigIntToIP(*IPToBigInt(r.IP))
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

		assignedip = BigIntToIP(*i)
		stringip := fmt.Sprint(assignedip)
		// For each address see if it has been allocated
		if reserved[stringip] {
			// Continue if this IP is allocated.
			continue
		}

		// We can try to work with the current IP
		// However, let's skip 0-based addresses
		// So go ahead and continue if the 4th/16th byte equals 0
		ipbytes := i.Bytes()
		if isIntIPv4(i) {
			if ipbytes[5] == 0 {
				continue
			}
		} else {
			if ipbytes[15] == 0 {
				continue
			}
		}

		// Lastly, we need to check if this IP is within the range of excluded subnets
		for _, subnet := range excluded {
			if subnet.Contains(BigIntToIP(*i).To16()) {
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

// GetIPRange returns the first and last IP in a range
func GetIPRange(ip net.IP, ipnet net.IPNet) (net.IP, net.IP, error) {

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

	// Get a long from the current IP address
	longip := IPToBigInt(ip)

	// Shift out to get the lowest IP value.
	var lowestiplong big.Int
	lowestiplong.Rsh(longip, uint(masklen))
	lowestiplong.Lsh(&lowestiplong, uint(masklen))

	// Get the mask as a long, shift it out
	var masklong big.Int
	// We need to generate the largest number...
	// Let's try to figure out if it's IPv4 or v6
	var maxval big.Int
	if len(lowestiplong.Bytes()) == net.IPv6len {
		// It's v6
		// Maximum IPv6 value: 0xffffffffffffffffffffffffffffffff
		maxval.SetString("0xffffffffffffffffffffffffffffffff", 0)
	} else {
		// It's v4
		// Maximum IPv4 value: 4294967295
		maxval.SetUint64(4294967295)
	}

	masklong.Rsh(&maxval, uint(ones))

	// Now figure out the highest value...
	// We can OR that value...
	var highestiplong big.Int
	highestiplong.Or(&lowestiplong, &masklong)
	// remove network and broadcast address from the  range
	var incIP big.Int
	incIP.SetInt64(1)
	lowestiplong.Add(&lowestiplong, &incIP)   // fixes to remove network address
	highestiplong.Sub(&highestiplong, &incIP) //fixes to remove broadcast address

	// Convert to net.IPs
	firstip := BigIntToIP(lowestiplong)
	if lowestiplong.Cmp(longip) < 0 { // if range_start was provided and its greater.
		firstip = BigIntToIP(*longip)
	}
	lastip := BigIntToIP(highestiplong)

	return firstip, lastip, nil

}

// IsIPv4 checks if an IP is v4.
func IsIPv4(checkip net.IP) bool {
	return checkip.To4() != nil
}

func isIntIPv4(checkipint *big.Int) bool {
	return !(len(checkipint.Bytes()) == net.IPv6len)
}

// BigIntToIP converts a big.Int to a net.IP
func BigIntToIP(inipint big.Int) net.IP {
	var outip net.IP
	outip = net.IP(make([]byte, net.IPv6len))
	intbytes := inipint.Bytes()
	if len(intbytes) == net.IPv6len {
		// This is an IPv6 address.
		for i := 0; i < len(intbytes); i++ {
			outip[i] = intbytes[i]
		}
	} else {
		// It's an IPv4 address.
		for i := 0; i < len(intbytes); i++ {
			outip[i+10] = intbytes[i]
		}
	}
	return outip
}

// IPToBigInt converts a net.IP to a big.Int
func IPToBigInt(IPv6Addr net.IP) *big.Int {
	IPv6Int := big.NewInt(0)
	IPv6Int.SetBytes(IPv6Addr)
	return IPv6Int
}
