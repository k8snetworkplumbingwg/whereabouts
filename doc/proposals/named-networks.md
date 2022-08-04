# Support for named networks

# Table of contents

- [Introduction](#introduction)
  - [Goal](#goal-of-this-proposal)
- [Design](#design)
  - [Changes in IPAM Config](#changes-in-ipam-config)
  - [Changes in Modules](#changes-in-modules)
- [Summary](#summary)
- [Discussions and Decisions](#discussions-and-decisions)

<hr>

## Introduction

When whereabouts assigns an IP to a Pod this fact is recorded in a document of kind `IPPool` that has its name derived from the CIDR range in question.
Should the user configure multiple overlapping ranges, it is possible to configure whereabouts to allow assigning duplicate IPs.
However, since the storage of the assignments is done in a document that is named like the CIDR range, it is not possible to configure *the same CIDR range* twice and have whereabouts assign from the ranges independently.

This is, for example, useful in multi-tenant situations where more than one group is responsible for selecting CIDR ranges.

### Goal of this Proposal

- Allow configuring the same CIDR range multiple times (e.g. in separate multus-`NetworkAttachmentDefinitions`)

<hr>

## Design

The IPAM configuration format would be updated to include an optional field for the name of the network in question.
If this field is left empty, whereabouts will behave as it does now, without these changes.

### Changes in IPAM Config

This will lead to change in IPAM config something as follows:

<table>
<tr>
<th>Old IPAM Config</th>
<th>Changes</th>
<th>New IPAM Config</th>
</tr>
<tr>
<td>
  
```json
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/28",
        "exclude": [
           "192.168.2.229/30",
           "192.168.2.236/32"
        ]
      }
}
```
  
</td>
<td>

```diff
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/28",
+       "network_name": "whereaboutsexample",
        "exclude": [
           "192.168.2.229/30",
           "192.168.2.236/32"
        ]
      }
}
```

</td>
<td>

```json
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/28",
        "network_name": "whereaboutsexample",
        "exclude": [
           "192.168.2.229/30",
           "192.168.2.236/32"
        ]
      }
}
```

</td>
</tr>
</table>

### Changes in Modules

#### whereabouts/pkg/types/types.go

```diff

 type IPAMConfig struct {
      Name                string
      Type                string               `json:"type"`
      Routes              []*cnitypes.Route    `json:"routes"`
      Datastore           string               `json:"datastore"`
      Addresses           []Address            `json:"addresses,omitempty"`
      OmitRanges          []string             `json:"exclude,omitempty"`
      DNS                 cnitypes.DNS         `json:"dns"`
      Range               string               `json:"range"`
      RangeStart          net.IP               `json:"range_start,omitempty"`
      RangeEnd            net.IP               `json:"range_end,omitempty"`
      GatewayStr          string               `json:"gateway"`
      EtcdHost            string               `json:"etcd_host,omitempty"`
      EtcdUsername        string               `json:"etcd_username,omitempty"`
      EtcdPassword        string               `json:"etcd_password,omitempty"`
      EtcdKeyFile         string               `json:"etcd_key_file,omitempty"`
      EtcdCertFile        string               `json:"etcd_cert_file,omitempty"`
      EtcdCACertFile      string               `json:"etcd_ca_cert_file,omitempty"`
      LeaderLeaseDuration int                  `json:"leader_lease_duration,omitempty"`
      LeaderRenewDeadline int                  `json:"leader_renew_deadline,omitempty"`
      LeaderRetryPeriod   int                  `json:"leader_retry_period,omitempty"`
      LogFile             string               `json:"log_file"`
      LogLevel            string               `json:"log_level"`
      OverlappingRanges   bool                 `json:"enable_overlapping_ranges,omitempty"`
      SleepForRace        int                  `json:"sleep_for_race,omitempty"`
      Gateway             net.IP
      Kubernetes          KubernetesConfig     `json:"kubernetes,omitempty"`
      ConfigurationPath   string               `json:"configuration_path"`
      PodName             string
      PodNamespace        string
      NetworkName         string               `json:"network_name,omitempty"`
 }
```

#### whereabouts/pkg/storage/kubernetes/ipam.go

```diff
-func NormalizeRange(ipRange string) string {
+func NormalizeRange(ipRange string, networkName string) string {
    // v6 filter
    normalized := strings.ReplaceAll(ipRange, ":", "-")
    // replace subnet cidr slash
    normalized = strings.ReplaceAll(normalized, "/", "-")
-   return normalized

+   if networkName != "" {
+       return networkName + "-" + normalized
+   } else {
+       return normalized
+   }
}
```

This will ensure that every time whereabouts looks up the current assignments on a range, it queries not for `192.168.2.225-28` but for `whereaboutsexample-192.168.2.225-28`.
Should the network name be left empty, the lookup is for the unchanged name `192.168.2.225-28`.

### Summary

whereabouts supports disabling the check for overlapping IP assignments, however it does not alllow actually configuring two identical ranges.

This proposal (and the prototypical implementation in https://github.com/k8snetworkplumbingwg/whereabouts/pull/256) allows doing exactly that by introducing a new IPAM parameter `network_name`.

### Discussions and Decisions

See
- https://github.com/k8snetworkplumbingwg/whereabouts/pull/256
- https://github.com/k8snetworkplumbingwg/whereabouts/issues/50#issuecomment-874040513
