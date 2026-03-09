// Package allocate implements the IP address assignment algorithm for
// whereabouts. It finds the lowest available IP within a configured range
// while respecting exclusion CIDRs and existing reservations.
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
	firstIP       net.IP
	lastIP        net.IP
	ipnet         net.IPNet
	excludeRanges []string
}

func (a AssignmentError) Error() string {
	return fmt.Sprintf("Could not allocate IP in range: ip: %v - %v / range: %s / excludeRanges: %v -- "+
		"the pool may be exhausted; consider expanding the range, checking for orphaned allocations "+
		"(kubectl get ippools -A), or adding additional ranges via ipRanges",
		a.firstIP, a.lastIP, a.ipnet.String(), a.excludeRanges)
}

// AssignIP assigns an IP using a range and a reserve list.
// If ipamConf.PreferredIP is set and available within the range, it is
// assigned directly. Otherwise, the lowest available IP is assigned.
func AssignIP(ipamConf types.RangeConfiguration, reservelist []types.IPReservation, containerID, podRef, ifName string) (net.IPNet, []types.IPReservation, error) {
	// Setup the basics here.
	_, ipnet, err := net.ParseCIDR(ipamConf.Range)
	if err != nil {
		return net.IPNet{}, nil, fmt.Errorf("invalid CIDR %q in IPAM config: %w", ipamConf.Range, err)
	}

	// Verify if podRef and ifName have already an allocation.
	for i, r := range reservelist {
		if r.PodRef == podRef && r.IfName == ifName {
			logging.Debugf("IP already allocated for podRef: %q - ifName:%q - IP: %s", podRef, ifName, r.IP.String())
			if r.ContainerID != containerID {
				logging.Debugf("updating container ID: %q", containerID)
				reservelist[i].ContainerID = containerID
			}

			return net.IPNet{IP: r.IP, Mask: ipnet.Mask}, reservelist, nil
		}
	}

	// Try preferred IP first if one is specified and available.
	if ipamConf.PreferredIP != nil && ipnet.Contains(ipamConf.PreferredIP) {
		reserved := false
		for _, r := range reservelist {
			if r.IP.Equal(ipamConf.PreferredIP) {
				reserved = true
				break
			}
		}
		if !reserved {
			// Verify not in exclude ranges.
			excluded := false
			for _, er := range ipamConf.OmitRanges {
				_, subnet, parseErr := net.ParseCIDR(er)
				if parseErr != nil {
					// Also try parsing as a plain IP.
					ip := net.ParseIP(er)
					if ip != nil && ip.Equal(ipamConf.PreferredIP) {
						excluded = true
						break
					}
					continue
				}
				if subnet.Contains(ipamConf.PreferredIP) {
					excluded = true
					break
				}
			}
			if !excluded {
				logging.Debugf("Assigning preferred IP: %q - container ID %q - podRef: %q - ifName: %q",
					ipamConf.PreferredIP, containerID, podRef, ifName)
				reservelist = append(reservelist, types.IPReservation{
					IP: ipamConf.PreferredIP, ContainerID: containerID, PodRef: podRef, IfName: ifName,
				})
				return net.IPNet{IP: ipamConf.PreferredIP, Mask: ipnet.Mask}, reservelist, nil
			}
		}
		logging.Debugf("Preferred IP %s not available, falling back to lowest-available", ipamConf.PreferredIP)
	}

	newip, updatedreservelist, err := IterateForAssignment(*ipnet, ipamConf.RangeStart, ipamConf.RangeEnd, reservelist, ipamConf.OmitRanges, containerID, podRef, ifName, ipamConf.L3)
	if err != nil {
		return net.IPNet{}, nil, err
	}

	return net.IPNet{IP: newip, Mask: ipnet.Mask}, updatedreservelist, nil
}

// DeallocateIP removes allocation from reserve list. Returns the updated reserve list and the deallocated IP.
func DeallocateIP(reservelist []types.IPReservation, containerID, ifName string) ([]types.IPReservation, net.IP) {
	index := getMatchingIPReservationIndex(reservelist, containerID, ifName)
	if index < 0 {
		// Allocation not found. Return the original reserve list and nil IP.
		return reservelist, nil
	}

	ip := reservelist[index].IP
	logging.Debugf("Deallocating given previously used IP: %v", ip.String())

	return removeIdxFromSlice(reservelist, index), ip
}

func getMatchingIPReservationIndex(reservelist []types.IPReservation, id, ifName string) int {
	for idx, v := range reservelist {
		if v.ContainerID == id && v.IfName == ifName {
			return idx
		}
	}
	return -1
}

func removeIdxFromSlice(s []types.IPReservation, i int) []types.IPReservation {
	s[i] = s[len(s)-1]
	return s[:len(s)-1]
}

// IterateForAssignment iterates given an IP/IPNet and a list of reserved IPs and excluded subnets.
// When l3 is false (default, L2 mode): valid IPs exclude the network and broadcast address.
// When l3 is true (L3/routed mode): all IPs in the subnet are considered valid, including
// the network address and broadcast address, since there is no broadcast domain in L3.
// If rangeStart is specified, it is respected if it lies within the subnet.
// If rangeEnd is specified, it is respected if it lies within the subnet and if it is >= rangeStart.
// reserveList holds a list of reserved IPs.
// excludeRanges holds a list of subnets to be excluded (meaning the full subnet, including the network and broadcast IP).
func IterateForAssignment(ipnet net.IPNet, rangeStart net.IP, rangeEnd net.IP, reserveList []types.IPReservation, excludeRanges []string, containerID, podRef, ifName string, l3 bool) (net.IP, []types.IPReservation, error) {
	var firstIP, lastIP net.IP
	var err error

	if l3 {
		// L3/routed mode: all IPs in the subnet are usable (no network/broadcast exclusion).
		firstIP = iphelpers.NetworkIP(ipnet)
		lastIP = iphelpers.SubnetBroadcastIP(ipnet)
		// Respect explicit rangeStart/rangeEnd if set.
		if rangeStart != nil && ipnet.Contains(rangeStart) && iphelpers.CompareIPs(rangeStart, firstIP) >= 0 {
			firstIP = rangeStart
		}
		if rangeEnd != nil && ipnet.Contains(rangeEnd) && iphelpers.CompareIPs(rangeEnd, firstIP) >= 0 && iphelpers.CompareIPs(rangeEnd, lastIP) <= 0 {
			lastIP = rangeEnd
		}
	} else {
		// L2 mode: exclude network and broadcast addresses.
		firstIP, lastIP, err = iphelpers.GetIPRange(ipnet, rangeStart, rangeEnd)
		if err != nil {
			logging.Errorf("GetIPRange request failed with: %w", err)
			return net.IP{}, reserveList, err
		}
	}
	logging.Debugf("IterateForAssignment input >> range_start: %v | range_end: %v | ipnet: %v | first IP: %v | last IP: %v | l3: %v",
		rangeStart, rangeEnd, ipnet.String(), firstIP, lastIP, l3)

	// Build reserved map.
	reserved := make(map[string]bool)
	for _, r := range reserveList {
		reserved[r.IP.String()] = true
	}

	// Build excluded list, "192.168.2.229/30", "192.168.1.229/30".
	excluded := []*net.IPNet{}
	for _, v := range excludeRanges {
		subnet, err := parseExcludedRange(v)
		if err != nil {
			return net.IP{}, reserveList, fmt.Errorf("could not parse exclude range, err: %q", err)
		}
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
		logging.Debugf("Reserving IP: %q - container ID %q - podRef: %q - ifName: %q", ip.String(), containerID, podRef, ifName)
		reserveList = append(reserveList, types.IPReservation{IP: ip, ContainerID: containerID, PodRef: podRef, IfName: ifName})
		return ip, reserveList, nil
	}

	// No IP address for assignment found, return an error.
	return net.IP{}, reserveList, AssignmentError{firstIP, lastIP, ipnet, excludeRanges}
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

// parseExcludedRange parses a provided string to a net.IPNet.
// If the provided string is a valid CIDR, return the net.IPNet for that CIDR.
// If the provided string is a valid IP address, add the /32 or /128 prefix to form the CIDR and return the net.IPNet.
// Otherwise, return the error.
func parseExcludedRange(s string) (*net.IPNet, error) {
	// Try parsing CIDRs.
	_, subnet, err := net.ParseCIDR(s)
	if err == nil {
		return subnet, nil
	}
	// The user might have given a single IP address, try parsing that - if it does not parse, return the error that
	// we got earlier.
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, err
	}
	// If the address parses, check if it's IPv4 or IPv6 and add the correct prefix.
	if ip.To4() != nil {
		_, subnet, err = net.ParseCIDR(fmt.Sprintf("%s/32", s))
	} else {
		_, subnet, err = net.ParseCIDR(fmt.Sprintf("%s/128", s))
	}
	return subnet, err
}
