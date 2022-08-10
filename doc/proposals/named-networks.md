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

When whereabouts assigns an IP to a Pod this fact is recorded in a CR of kind `IPPool` that has its name derived from the CIDR range in question.

Should the user configure multiple overlapping ranges, it is possible to configure whereabouts to allow assigning duplicate IPs.

However, since the storage of the assignments is done in a CR that is named like the CIDR range, it is not possible to configure *the same CIDR range* twice and have whereabouts assign from the ranges independently.

This is, for example, useful in multi-tenant situations where more than one group is responsible for selecting CIDR ranges.

### Goal of this Proposal

- Allow configuring the same CIDR range multiple times (e.g. in separate multus-`NetworkAttachmentDefinitions`)

<hr>

## Design

The network configuration already has a field `name`:

```json
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/28"
      }
}
```

That is also parsed into the internal representation of the `IPAMConfig`.

This proposal shows three schemes of implementing using that name to distinguish the assignment of IPs:

1. Store the CR with the name of the network configuration instead of the canonicalized CIDR range
2. Store the CR with the name of the network configuration prepended (or appended) to the canonicalized CIDR range
3. Add a new field into the `IPAMConfig` to allow users to decide when whereabouts should use the name or the CIDR range for identifying the configuration

### Analysis of the proposed schemes

#### Store the CR under the name of the network configuration

<table>
<tr>
<th>Pros</th>
<th>Cons</th>
</tr>
<tr>
<td>
:green_circle: Clean design<br/>
:green_circle: Ranges are easy to find during debugging<br/>
:green_circle: No more IP-to-string canonicalization<br/>
</td>
<td>
:red_circle: Not backwards compatible, existing installation would need to carefully migrate the existing `IPPool`s to not get duplicate IPs<br/>
:red_circle: Unclear semantics when two ranges with the same name but different CIDR-ranges are created<br/>
</td>
</tr>
</table>

#### Store the CR under a name combined from the name of the network configuration and the CIDR range

<table>
<tr>
<th>Pros</th>
<th>Cons</th>
</tr>
<tr>
<td>
:green_circle: Clean design<br/>
:green_circle: Ranges are easy to find during debugging<br/>
</td>
<td>
:red_circle: Not backwards compatible, existing installation would need to carefully migrate the existing `IPPool`s to not get duplicate IPs<br/>
</td>
</tr>
</table>

#### Add a new field to decide whether this is a named range or not

<table>
<tr>
<th>Pros</th>
<th>Cons</th>
</tr>
<tr>
<td>
:green_circle: Backwards compatible, existing `IPPool`s are still where we left them<br/>
:yellow_circle: Named ranges are easy to find during debugging, other ranges are unchanged<br/>
</td>
<td>
:red_circle: "API" change<br/>
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
      ThisIsANamedRange   bool               `json:"named_range,omitempty"`
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

+   if ThisIsANamedRange {
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
