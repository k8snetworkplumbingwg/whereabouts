# Whereabouts support for Ipv4/IPv6 DualStack

# Table of contents

- [Introduction](#introduction)
  - [Goal](#goal)
- [Design](#design)
  - [Changes in IPAM Config](#configChanges)
  - [Changes in Modules](#moduleChanges)
- [Alternative Design](#alternative)
- [Summary](#summary)

<hr>

## Introduction <a name="introduction"></a>

Starting v1.20, Kubernetes officially started supporting DualStack, which is being used extensively in many projects. 

Whereabouts being an important IPAM CNI plugin, should also make a plan for supporting DualStack.
This is a design proposal document for introducing IPv4/IPv6 DualStack support in whereabouts.


### Goal <a name="goal"></a>

- Allocate IPv4 and/or IPv6 addresses to pods depending on IPAM configuration
- Above feature should not break the backward compatibility for SingleStack configuration
- Allocate multiple (even more than 2) IPs to the pods irrespective of IPv4/IPv6 _(Optional and depends on design choice)_

<hr>

## Design <a name="design"></a>

The idea is to update the IPAM config format to include multiple IP configurations.

Basically, rather than having IP related configurations directly as key members of `IPAMConfig` object, they be included as element of an object type, let's say `IPConfig` and `IPAMConfig` should have an array of `IPConfig` as it's element.

### Changes in IPAM Config <a name="configChanges"></a>

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
-       "range": "192.168.2.225/28",
-       "exclude": [
-          "192.168.2.229/30",
-          "192.168.2.236/32"
-       ]
+       "ips": [
+         {
+           "range": "192.168.2.225/28",
+           "exclude": [
+              "192.168.2.229/30",
+              "192.168.2.236/32"
+           ]
+         },
+         {
+           "range": "2001::0/116",
+         }
+       ]
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
        "ips": [
          {
            "range": "192.168.2.225/28",
            "exclude": [
               "192.168.2.229/30",
               "192.168.2.236/32"
            ]
          },
          {
            "range": "2001::0/116",
          }
        ]
      }
}
```

</td>
</tr>
</table>

_Note: But at the same time we also need to support the old field (i.e. field duplication) in order to make the design backward compatible._

### Changes in Modules <a name="moduleChanges"></a>

#### whereabouts/pkg/types/types.go

```diff

+type IPConfig struct {
+     Addresses           []Address         `json:"addresses,omitempty"`
+     OmitRanges          []string          `json:"exclude,omitempty"`
+     Range               string            `json:"range"`
+     RangeStart          net.IP            `json:"range_start,omitempty"`
+     RangeEnd            net.IP            `json:"range_end,omitempty"`
+}

 type IPAMConfig struct {
      Name                string
      Type                string            `json:"type"`
      Routes              []*cnitypes.Route `json:"routes"`
      Datastore           string            `json:"datastore"`
+     IPs                 []IPConfig        `json:"ips"`
      Addresses           []Address         `json:"addresses,omitempty"`
      OmitRanges          []string          `json:"exclude,omitempty"`
      DNS                 cnitypes.DNS      `json:"dns"`
      Range               string            `json:"range"`
      RangeStart          net.IP            `json:"range_start,omitempty"`
      RangeEnd            net.IP            `json:"range_end,omitempty"`
      GatewayStr          string            `json:"gateway"`
      EtcdHost            string            `json:"etcd_host,omitempty"`
      EtcdUsername        string            `json:"etcd_username,omitempty"`
      EtcdPassword        string            `json:"etcd_password,omitempty"`
      EtcdKeyFile         string            `json:"etcd_key_file,omitempty"`
      EtcdCertFile        string            `json:"etcd_cert_file,omitempty"`
      EtcdCACertFile      string            `json:"etcd_ca_cert_file,omitempty"`
      LeaderLeaseDuration int               `json:"leader_lease_duration,omitempty"`
      LeaderRenewDeadline int               `json:"leader_renew_deadline,omitempty"`
      LeaderRetryPeriod   int               `json:"leader_retry_period,omitempty"`
      LogFile             string            `json:"log_file"`
      LogLevel            string            `json:"log_level"`
      OverlappingRanges   bool              `json:"enable_overlapping_ranges,omitempty"`
      SleepForRace        int               `json:"sleep_for_race,omitempty"`
      Gateway             net.IP
      Kubernetes          KubernetesConfig `json:"kubernetes,omitempty"`
      ConfigurationPath   string           `json:"configuration_path"`
      PodName             string
      PodNamespace        string
 }
```

Corresponding changes will also be required in `whereabouts/pkg/allocate/allocate.go`, `whereabouts/pkg/config/config.go` etc.

<hr>

## Alternative Design <a name="alternative"></a>

Alternatively, if we only want to support DualStack (i.e. at max one IPv4 and one IPv6 address), we can have a simpler config change which is easily backward compatible. _(But I still prefer the first approach even though it is requires more effort)_

We can have change in IPAM Config as below.

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
+       "secondary_range": "2001::0/116",
        "exclude": [
           "192.168.2.229/30",
           "192.168.2.236/32",
+          "2001::0/124"
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
        "secondary_range": "2001::0/116",
        "exclude": [
           "192.168.2.229/30",
           "192.168.2.236/32",
           "2001::0/124"
        ]
      }
}
```

</td>
</tr>
</table>

And we can simply add `secondary_*` fields in `IPAMConfig`.

<hr>

### Summary <a name="summary"></a>

Currently, we have above two approaches for supporting DualStack in whereabouts.

First approach requires addition on a new _type_ for encapsulating all the IP related information on `IPAMConfig` and requires significant amount of change in the current code. But with this additional effort, it will have additional benefit of allowing us to allocate as many as we required IP addresses of any IP family without any constraints. _(We need to support the existing fields too for backward compatibility)_

Second approach is somewhat simpler. It just adds some additional fields for _secondary_ IP address related configuration (which might be IPv4 or IPv6 depending on stack policy). The downside is that we will limit ourselves to adding at max 2 IP address for a pod. _(TBH, it also seems okay for now, but I prefer the first approach)_
