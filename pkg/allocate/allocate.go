package allocate

import (
	"fmt"
	"math"
	"net"

	"inet.af/netaddr"

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
func AssignIP(ipamConf types.IPAMConfig, reservelist []types.IPReservation, containerID string, podRef string) (net.IPNet, []types.IPReservation, error) {

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
		return reservelist, nil, fmt.Errorf("Did not find reserved IP for container %v", containerID)
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

// byteSliceAdd adds ar1 to ar2
// note: ar1/ar2 should be 16-length array
func byteSliceAdd(ar1, ar2 []byte) ([]byte, error) {
	if len(ar1) != len(ar2) {
		return nil, fmt.Errorf("byteSliceAdd: bytes array mismatch: %v != %v", len(ar1), len(ar2))
	}
	carry := uint(0)

	sumByte := make([]byte, 16)
	for n := range ar1 {
		var sum uint
		sum = uint(ar1[15-n]) + uint(ar2[15-n]) + carry
		carry = 0
		if sum > 255 {
			carry = 1
		}
		sumByte[15-n] = uint8(sum)
	}

	return sumByte, nil
}

// byteSliceSub subtracts ar2 from ar1. This function assumes that ar1 > ar2
// note: ar1/ar2 should be 16-length array
func byteSliceSub(ar1, ar2 []byte) ([]byte, error) {
	if len(ar1) != len(ar2) {
		return nil, fmt.Errorf("byteSliceSub: bytes array mismatch")
	}
	carry := int(0)

	sumByte := make([]byte, 16)
	for n := range ar1 {
		var sum int
		sum = int(ar1[15-n]) - int(ar2[15-n]) - carry
		if sum < 0 {
			sum = 0x100 - int(ar1[15-n]) - int(ar2[15-n]) - carry
			carry = 1
		} else {
			carry = 0
		}
		sumByte[15-n] = uint8(sum)
	}

	return sumByte, nil
}

func ipAddrToUint64(ip net.IP) uint64 {
	num := uint64(0)
	ipArray := []byte(ip)
	for n := range ipArray {
		num = num << 8
		num = uint64(ipArray[n]) + num
	}
	return num
}

func ipAddrFromUint64(num uint64) net.IP {
	idxByte := make([]byte, 16)
	i := num
	for n := range idxByte {
		idxByte[15-n] = byte(0xff & i)
		i = i >> 8
	}
	return net.IP(idxByte)
}

// IPGetOffset gets offset between ip1 and ip2. This assumes ip1 > ip2 (from IP representation point of view)
func IPGetOffset(ip1, ip2 net.IP) uint64 {
	if ip1.To4() == nil && ip2.To4() != nil {
		return 0
	}

	if ip1.To4() != nil && ip2.To4() == nil {
		return 0
	}

	if len([]byte(ip1)) != len([]byte(ip2)) {
		return 0
	}

	ipOffset, _ := byteSliceSub([]byte(ip1.To16()), []byte(ip2.To16()))
	return ipAddrToUint64(ipOffset)
}

// IPAddOffset show IP address plus given offset
func IPAddOffset(ip net.IP, offset uint64) net.IP {
	// Check IPv4 and its offset range
	if ip.To4() != nil && offset >= math.MaxUint32 {
		return nil
	}

	// make pseudo IP variable for offset
	idxIP := ipAddrFromUint64(offset)

	b, _ := byteSliceAdd([]byte(ip.To16()), []byte(idxIP))
	return net.IP(b)
}

// IterateForAssignment iterates given an IP/IPNet and a list of reserved IPs
func IterateForAssignment(ipnet net.IPNet, rangeStart net.IP, rangeEnd net.IP, reservelist []types.IPReservation, excludeRanges []string, containerID string, podRef string) (net.IP, []types.IPReservation, error) {
	firstip, lastip, err := GetIPRange(rangeStart, ipnet)
	if err != nil {
		return net.IP{}, reservelist, err
	}
	if rangeEnd != nil {
		lastip = rangeEnd.To16()
	}
	firstIP, ok := netaddr.FromStdIP(firstip)
	if !ok {
		return nil, nil, fmt.Errorf("ip invalid: %v", firstip)
	}
	lastIP, ok := netaddr.FromStdIP(lastip)
	if !ok {
		return nil, nil, fmt.Errorf("ip invalid: %v", lastip)
	}

	builder := netaddr.IPSetBuilder{}
	builder.AddRange(netaddr.IPRangeFrom(firstIP, lastIP))

	// exclude reserved IPs
	for i, r := range reservelist {
		ip, ok := netaddr.FromStdIP(r.IP)
		if !ok {
			return net.IP{}, reservelist, fmt.Errorf("ip[%d] in reservelist invalid: %s", i, r.IP)
		}
		builder.Remove(ip)
	}
	logging.Debugf("IterateForAssignment input >> ip: %v | ipnet: %v | first IP: %v | last IP: %v", rangeStart, ipnet, firstIP, lastIP)

	// remove excluded ranges
	for i, v := range excludeRanges {
		prefix, err := netaddr.ParseIPPrefix(v)
		if err != nil {
			return net.IP{}, reservelist, fmt.Errorf("subnet[%d] (%q) in excludeRanges invalid: %w", i, v, err)
		}
		builder.RemovePrefix(prefix)
	}

	// return the current set of IPs contained by the builder
	ipset, err := builder.IPSet()
	if err != nil {
		return net.IP{}, reservelist, err

	}
	var assignedip net.IP
	performedassignment := false
	for _, r := range ipset.Ranges() {
		// the first range should start with the first available IP
		performedassignment = true
		assignedip = r.From().IPAddr().IP
		logging.Debugf("Reserving IP: |%v|", assignedip.String()+" "+containerID)
		reservelist = append(reservelist, types.IPReservation{IP: assignedip, ContainerID: containerID, PodRef: podRef})
		break
	}
	if !performedassignment {
		return net.IP{}, reservelist, AssignmentError{firstIP.IPAddr().IP, lastIP.IPAddr().IP, ipnet}
	}
	return assignedip, reservelist, nil
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
	// convert to netaddr IPPrefix for range start/end calculation
	prefix, ok := netaddr.FromStdIPNet(&ipnet)
	if !ok {
		return nil, nil, fmt.Errorf("IPNet invalid: %v", ipnet)

	}
	lastIP := prefix.Range().To()
	firstIP, ok := netaddr.FromStdIP(ip)
	if !ok {
		return nil, nil, fmt.Errorf("ip invalid: %v", firstIP)
	}
	// if ip is just same as ipnet.IP, i.e. just network address,
	// increment it for start ip
	if firstIP.IPAddr().IP.Equal(ipnet.IP) {
		firstIP = firstIP.Next()
	}
	ipRange := netaddr.IPRangeFrom(firstIP, lastIP)
	firstIP = ipRange.From()
	lastIP = ipRange.To()
	// if IPv4 case, decrement 1 for broadcasting address
	if lastIP.Is4() {
		lastIP = lastIP.Prior()
	}
	return firstIP.IPAddr().IP, lastIP.IPAddr().IP, nil
}

// IsIPv4 checks if an IP is v4.
func IsIPv4(checkip net.IP) bool {
	return checkip.To4() != nil
}
