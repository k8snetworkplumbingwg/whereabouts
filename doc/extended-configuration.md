# Extended configuration

Should you need to further configure Whereabouts, you might find these options valuable.

## IP Reconciliation

Whereabouts includes a tool which is intended be run as a k8s `CronJob`. This
utility scans the currently allocated IP addresses, and reconciles them against
the currently running pods, and deallocates IP addresses which have been left
stranded.
Stranded IP addresses can occur due to node failures (e.g. a sudden power off /
reboot event) or potentially from pods that have been force deleted
(e.g. `kubectl delete pod foo --grace-period=0 --force`)

A reference deployment of this tool is available in the
`/docs/ip-reconcilier-job.yaml` file.

## Installation options

The daemonset installation as shown on the README is for use with Kubernetes version 1.16 and later. It may also be useful with previous versions, however you'll need to change the `apiVersion` of the daemonset in the provided yaml, [see the deprecation notice](https://kubernetes.io/blog/2019/07/18/api-deprecations-in-1-16/).

You can compile from this repo (with `./hack/build-go.sh`) and copy the resulting binary onto each node in the `/opt/cni/bin` directory (by default).

Not that we're also including a Custom Resource Definition (CRD) to use the `kubernetes` datastore option. This installs the kubernetes CRD specification for the `ippools.whereabouts.cni.k8s.io/v1alpha1` type.

### Example etcd datastore configuration

If you'll use the etcd datastore option, you'll likely want to install etcd first. Etcd installation suggestions follow below.

*NOTE*: You'll almost certainly want to change `etcd_host`.

```
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "etcd_host": "example-etcd-cluster-client.cluster.local:2379",
        "range": "192.168.2.225/28",
        "exclude": [
           "192.168.2.229/30",
           "192.168.2.236/32"
        ],
        "log_file" : "/tmp/whereabouts.log",
        "log_level" : "debug",
        "gateway": "192.168.2.1"
      }
}
```


### etcd Parameters

**Required:**
* `etcd_host`: This is a connection string for your etcd hosts. It can take a single address or a list, or any other valid etcd connection string.

**Optional:**
* `etcd_username`: Basic Auth username to use when accessing the etcd API.
* `etcd_password`: Basic Auth password to use when accessing the etcd API.
* `etcd_key_file`: Path to the file containing the etcd private key matching the CNI plugin’s client certificate.
* `etcd_cert_file`: Path to the file containing the etcd client certificate issued to the CNI plugin.
* `etcd_ca_cert_file`: Path to the file containing the root certificate of the certificate authority (CA) that issued the etcd server certificate.

### Logging Parameters

There are two optional parameters for logging, they are:

* `log_file`: A file path to a logfile to log to.
* `log_level`: Set the logging verbosity, from most to least: `debug`,`error`,`panic`

## Flatfile configuration

During installation using the daemonset-style install, Whereabouts creates a configuration file @ `/etc/cni/net.d/whereabouts.d/whereabouts.conf`. Any parameter that you do not wish to repeatly put into the `ipam` section of a CNI configuration can be put into this file (such as etcd and Kubernetes configuration parameters, or logging).

There is one option for flat file configuration:

* `configuration_path`: A file path to a Whereabouts configuration file.

If you're using [Multus CNI](http://multus-cni.io/) or another meta-plugin, you may wish to reduce the number of parameters you need to specify in the IPAM section by putting commonly used options into a flat file -- primarily to make it simpler to type and to reduce having to copy and paste the same parameters repeatedly.

Whereabouts will look for the configuration in these locations, in this order:

* The location specified by the `configuration_path` option.
* `/etc/kubernetes/cni/net.d/whereabouts.d/whereabouts.conf`
* `/etc/cni/net.d/whereabouts.d/whereabouts.conf`

You may specify the `configuration_path` to point to another location should it be desired.

Any options added to the `whereabouts.conf` are overridden by configuration options that are in the primary CNI configuration (e.g. in a custom resource `NetworkAttachmentDefinition` used by Multus CNI or in the first file ASCII-betically in the CNI configuration directory -- which is `/etc/cni/net.d/` by default).


### Example flat file configuration

You can reduce the number of parameters used if you need to make more than one Whereabouts configuration (such as if you're using [Multus CNI](http://multus-cni.io/))

Create a file named `/etc/cni/net.d/whereabouts.d/whereabouts.conf`, with the contents:

```
{
  "datastore": "kubernetes",
  "kubernetes": {
    "kubeconfig": "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"
  },
  "log_file": "/tmp/whereabouts.log",
  "log_level": "debug"
}
```

With that in place, you can now create an IPAM configuration that has a lot less options, in this case we'll give an example using a `NetworkAttachmentDefinition` as used with Multus CNI (or other implementations of the [Network Plumbing Working Group specification](https://github.com/k8snetworkplumbingwg/multi-net-spec))

An example configuration using a `NetworkAttachmentDefinition`:

```
apiVersion: "k8s.cni.cncf.io/v1"
kind: NetworkAttachmentDefinition
metadata:
  name: whereabouts-conf
spec:
  config: '{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/28"
      }
    }'
```

You'll note that in the `ipam` section there's a lot less parameters than are used in the previous examples.

### Reconciler Cron Expression Configuration (optional)

You may want to provide a cron expression to configure how frequently the ip-reconciler runs. This is done via the flatfile.

Look for the following parameter `"reconciler_cron_expression"` located in `script/install-cni.sh` and change to your desired schedule.

## Installing etcd. (optional)

etcd installation is optional. By default, we recommend the custom resource backend (given in the first example configuration).

We recommend that you if you're trying it out in a lab, that you use the [etcd-operator](https://github.com/coreos/etcd-operator), the [installation guide](https://github.com/coreos/etcd-operator/blob/master/doc/user/install_guide.md) is just a few steps. 

*NOTE*: The etcd operator is deprecated.

Once you've got etcd running -- all you'll need to provide Whereabouts is the endpoint(s) for it. In the etcd-operator style installation, you'd find those with:

```
kubectl get svc | grep "etcd-cluster-client"
```

This will give you the service name and the port to use, in this case you'll specify it in the configuration in a `service-name:port` format, the default port for etcd clients is `2379`.

*Note*: It's important to remember that CNI plugins (typically) run directly on the host and not inside pods. This means that if you use the DNS name (which might look something like `example-etcd-cluster-client.default.svc.cluster.local`) for the service (recommended) make sure that you can resolve those hostnames directly from your hosts. You may find some tips regarding that [here](https://blog.heptio.com/configuring-your-linux-host-to-resolve-a-local-kubernetes-clusters-service-urls-a8c7bdb212a7).