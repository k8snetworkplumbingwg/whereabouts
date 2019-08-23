package allocate

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/dougbtv/whereabouts/pkg/logging"
	"github.com/dougbtv/whereabouts/pkg/types"
	"math/big"
	"net"
	"strconv"
)

// AssignIP assigns an IP using a range and a reserve list.
func AssignIP(ipamConf types.IPAMConfig, reservelist []types.IPReservation, containerID string) (net.IPNet, []types.IPReservation, error) {

	// Setup the basics here.
	ip, ipnet, _ := net.ParseCIDR(ipamConf.Range)

	newip, updatedreservelist, err := IterateForAssignment(ip, *ipnet, reservelist, ipamConf.OmitRanges, containerID)
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
func IterateForAssignment(ip net.IP, ipnet net.IPNet, reservelist []types.IPReservation, excludeRanges []string, containerID string) (net.IP, []types.IPReservation, error) {

	firstip, lastip, err := GetIPRange(ip, ipnet)
	logging.Debugf("IterateForAssignment input >> ip: %v | ipnet: %v | first IP: %v | last IP: %v", ip, ipnet, firstip, lastip)
	if err != nil {
		logging.Errorf("GetIPRange request failed with: %v", err)
		return net.IP{}, reservelist, err
	}

	reserved := make(map[string]bool)
	for _, r := range reservelist {
		reserved[r.IP.String()] = true
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
	for i := ip2Long(firstip); ip2Long(lastip).Cmp(i) == 1; i.Add(i, big.NewInt(1)) {

		// For each address see if it has been allocated
		if reserved[fmt.Sprint(longToIP(*i))] {
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
			if subnet.Contains(longToIP(*i).To4()) {
				continue MAINITERATION
			}
		}

		// Ok, this one looks like we can assign it!
		performedassignment = true
		assignedip = longToIP(*i)
		stringip := fmt.Sprint(assignedip)
		logging.Debugf("Reserving IP: |%v|", stringip+" "+containerID)
		reservelist = append(reservelist, types.IPReservation{IP: assignedip, ContainerID: containerID})
		break

	}

	if !performedassignment {
		return net.IP{}, reservelist, fmt.Errorf("Could not allocate IP in range: ip: %v / range: %v", ip, ipnet)
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
	// logging.Debugf("Mask: %v / Ones: %v / Bits: %v / masklen: %v", mask, ones, bits, masklen)

	// Error when the mask isn't large enough.
	if ones < 3 {
		return nil, nil, fmt.Errorf("Net mask is too short, must be 3 or more: %v", masklen)
	}

	// Get a long from the current IP address
	longip := ip2Long(ip)

	// logging.Debugf("binary rep of IP: %b", longip)

	// Shift out to get the lowest IP value.
	var lowestiplong big.Int
	lowestiplong.Rsh(longip, uint(masklen))
	lowestiplong.Lsh(&lowestiplong, uint(masklen))
	// logging.Debugf("lowest value:     %b", &lowestiplong)

	// Get the mask as a long, shift it out
	var masklong big.Int
	// We need to generate the largest number...
	// Let's try to figure out if it's IPv4 or v6
	var maxval big.Int
	if len(lowestiplong.Bytes()) == 16 {
		// It's v6
		// Maximum IPv6 value: 0xffffffffffffffffffffffffffffffff
		maxval.SetString("0xffffffffffffffffffffffffffffffff", 0)
	} else {
		// It's v4
		// Maximum IPv4 value: 4294967295
		maxval.SetUint64(4294967295)
	}

	masklong.Rsh(&maxval, uint(ones))
	// logging.Debugf("max val:          %b", &maxval)
	// logging.Debugf("mask:             %b", &masklong)

	// Now figure out the highest value...
	// We can OR that value...
	var highestiplong big.Int
	highestiplong.Or(&lowestiplong, &masklong)
	// logging.Debugf("highest value:    %b", &highestiplong)

	// Now let's send it back to IPs, we can get first and last in the range...
	firstip := longToIP(lowestiplong)
	lastip := longToIP(highestiplong)

	// Some debugging information...
	// logging.Debugf("first ip: %v", firstip)
	// logging.Debugf("last ip:  %v", lastip)
	// logging.Debugf("mask len: %v", len(mask))
	// logging.Debugf("mask raw: %v", mask)
	// logging.Debugf("mask size: 1's: %v / bits: %v / len: %v", ones, bits, masklen)

	return firstip, lastip, nil

}

// from: https://www.socketloop.com/tutorials/golang-convert-ip-address-string-to-long-unsigned-32-bit-integer
func _ip2Long(ip net.IP) big.Int {
	var long big.Int
	binary.Read(bytes.NewBuffer(ip.To4()), binary.BigEndian, &long)
	return long
}

// IsIPv4 checks if an IP is v4.
func IsIPv4(checkip net.IP) bool {
	return isIntIPv4(ip2Long(checkip))
}

func isIntIPv4(checkipint *big.Int) bool {
	return !(len(checkipint.Bytes()) == 16)
}

func longToIP(inipint big.Int) net.IP {

	var outip net.IP

	// Create an IPv6 (to make it 16 bytes)
	outip = net.ParseIP("0::")
	intbytes := inipint.Bytes()

	// logging.Debugf("length intbytes: %v", len(intbytes))
	// This is an IPv6 address.

	if len(intbytes) == 16 {
		for i := 0; i < len(intbytes); i++ {
			// logging.Debugf("i: %v", i)
			outip[i] = intbytes[i]
			// logging.Debugf("longtoip-byte[%v]: %x ", i, intbytes[i])
		}

	} else {
		// It's an IPv4 address.
		for i := 0; i < len(intbytes); i++ {
			// logging.Debugf("i: %v", i)
			outip[i+10] = intbytes[i]
			// logging.Debugf("longtoip-byte[%v]: %x ", i, intbytes[i])
		}
	}
	return outip
}

func _longToIP(inIPInt uint32) net.IP {
	// we process it as an int64
	ipInt := int64(inIPInt)
	// need to do two bit shifting and "0xff" masking
	b0 := strconv.FormatInt((ipInt>>24)&0xff, 10)
	b1 := strconv.FormatInt((ipInt>>16)&0xff, 10)
	b2 := strconv.FormatInt((ipInt>>8)&0xff, 10)
	b3 := strconv.FormatInt((ipInt & 0xff), 10)
	return net.ParseIP(b0 + "." + b1 + "." + b2 + "." + b3)
}

func tryIt(inputiprange string) error {
	logging.Debugf("Input range: %s", inputiprange)
	theip, ipnet, err := net.ParseCIDR(inputiprange)
	if err != nil {
		logging.Errorf("Couldn't parse IP: %s", inputiprange)
		return fmt.Errorf("Couldn't parse IP: %s", inputiprange)
	}

	logging.Debugf("The IP: %s", theip)
	logging.Debugf("The IPnet: %+v", ipnet)
	// logging.Debugf("The IPnet IP: %+v", ipnet.IP)
	// logging.Debugf("The IPnet IPMask: %+v", ipnet.IPMask)
	theonesix := theip // .To16()
	// Can I change a byte? YES!
	theonesix[15] = 255
	logging.Debugf("theonesix: %+v", theonesix)
	for i := 0; i < len(theonesix); i++ {
		logging.Debugf("byte[%v]: %x ", i, theonesix[i])
	}
	// theonesix++
	// logging.Debugf("theonesixPLUSONE: %+v", theonesix)
	// Can I access the bytes in the int? YES
	asint := ip2Long(theonesix)
	logging.Debugf("asint: %v", asint)
	intbytes := asint.Bytes()
	for i := 0; i < len(intbytes); i++ {
		logging.Debugf("asint[%v]: %x ", i, intbytes[i])
	}

	return nil
}

func ip2Long(IPv6Addr net.IP) *big.Int {
	IPv6Int := big.NewInt(0)
	IPv6Int.SetBytes(IPv6Addr)
	return IPv6Int
}
