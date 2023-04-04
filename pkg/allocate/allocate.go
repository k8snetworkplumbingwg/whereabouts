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

// IterateForAssignment iterates given an IP/IPNet and a list of reserved IPs and exluded subnets.
// Valid IPs are contained within the ipnet, excluding the network and broadcast address.
// If rangeStart is specified, it is respected if it lies within the ipnet.
// If rangeEnd is specified, it is respected if it lies within the ipnet and if it is >= rangeStart.
// reserveList holds a list of reserved IPs.
// excludeRanges holds a list of subnets to be excluded (meaning the full subnet, including the network and broadcast IP).
func IterateForAssignment(ipnet net.IPNet, rangeStart net.IP, rangeEnd net.IP, reserveList []types.IPReservation, excludeRanges []string, containerID string, podRef string) (net.IP, []types.IPReservation, error) {
	// Get the valid range, delimited by the ipnet's first and last usable IP as well as the rangeStart and rangeEnd.
	firstIP, lastIP, err := iphelpers.GetIPRange(ipnet, rangeStart, rangeEnd)
	if err != nil {
		logging.Errorf("GetIPRange request failed with: %v", err)
		return net.IP{}, reserveList, err
	}
	logging.Debugf("IterateForAssignment input >> range_start: %v | range_end: %v | ipnet: %v | first IP: %v | last IP: %v",
		rangeStart, rangeEnd, ipnet, firstIP, lastIP)

	// Build reserved map.
	reserved := make(map[string]bool)
	for _, r := range reserveList {
		reserved[r.IP.String()] = true
	}
	// Build excluded list, "192.168.2.229/30", "192.168.1.229/30".
	excluded := []*net.IPNet{}
	for _, v := range excludeRanges {
		_, subnet, _ := net.ParseCIDR(v)
		excluded = append(excluded, subnet)
	}

	// Iterate over every IP address in the range, accounting for reserved IPs and exclude ranges. Make sure that ip is
	// within ipnet, and make sure that ip is smaller than lastIP.
	for ip := firstIP; ipnet.Contains(ip) && iphelpers.CompareIPs(ip, lastIP) <= 0; ip = iphelpers.IncIP(ip) {
		// If already reserved, skip it.
		if reserved[ip.String()] {
			continue
		}
		// If this IP is within the range of one of the excluded subnets, jump to the exluded subnet's broadcast address
		// and skip.
		if skipTo := skipExcludedSubnets(ip, excluded); skipTo != nil {
			ip = skipTo
			continue
		}
		// Assign and reserve the IP and return.
		logging.Debugf("Reserving IP: |%v|", ip.String()+" "+containerID)
		reserveList = append(reserveList, types.IPReservation{IP: ip, ContainerID: containerID, PodRef: podRef})
		return ip, reserveList, nil
	}

	// No IP address for assignment found, return an error.
	return net.IP{}, reserveList, AssignmentError{firstIP, lastIP, ipnet}
}

// skipExcludedSubnets iterates through all subnets and checks if ip is part of them. If i is part of one of the subnets,
// return the subnet's broadcast address.
func skipExcludedSubnets(ip net.IP, excluded []*net.IPNet) net.IP {
	for _, subnet := range excluded {
		if subnet.Contains(ip) {
			broadcastIP := iphelpers.SubnetBroadcastIP(*subnet)
			logging.Debugf("excluding %v and moving to the end of the excluded range: %v", subnet, broadcastIP)
			return broadcastIP
		}
	}
	return nil
}
