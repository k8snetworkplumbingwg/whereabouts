# whereabouts

![whereabouts-logo](doc/logo.png)

Whereabouts - An IP Address Management (IPAM) CNI plugin that assigns IP addresses cluster-wide.

## Usage

[... more to come ...]

## Example Config

```
{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/28",
        "etcd_host": "127.0.0.1:2379",
        "log_file" : "/tmp/whereabouts.log",
        "log_level" : "debug",
        "gateway": "192.168.2.1"
      }
}
```

## Building

Run the build command from the `./hack` directory:

```
./hack/build-go.sh
```

## Acknowledgements

Thanks big time to [Tomofumi Hayashi](https://github.com/s1061123), I utilized his [static CNI IPAM plugin](https://github.com/containernetworking/plugins/tree/master/plugins/ipam/static) as a basis for this project to give me a head start!

## Known limitations

* This only works for IPv4 addresses.
* It has write locking, but, it's not optimized. It's write locked for all ranges.
* If you specify overlapping ranges -- you're almost certain to have collisions, so if you specify one config with `192.168.0.0/16` and another with `192.168.0.0/24`, you'll have collisions.
* There's approximately a cap of 18,500 possible addresses in a given range before you'll have to configure etcd to allow more than 1.5 megs in a value.
* There's probably a lot of comparison of IP addresses that could be optimized, lots of string conversion.
* The etcd method that I use is all ASCII. If this was binary, it could probably store more and have more efficient IP address comparison.